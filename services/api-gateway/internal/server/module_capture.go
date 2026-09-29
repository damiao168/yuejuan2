package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/orchestrator"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"fmt"
	"reflect"
)

type CaptureProcessingStores struct {
	ImageQuality  imagequality.Store
	WorkerRuntime workerruntime.Store
	OCR           ocrpkg.Store
	OCRQueue      ocrpkg.Queue
	Orchestrator  orchestrator.Store
	Capture       capture.Store
	CaptureUpload captureupload.Store
	Processing    processing.Store
}

type CaptureProcessingModule struct {
	WorkerRuntimeStore workerruntime.Store
	CaptureStore       capture.Store
	ProcessingService  *processing.Service

	OrchestratorHandler  *orchestrator.Handler
	OCRHandler           *ocrpkg.Handler
	ImageQualityHandler  *imagequality.Handler
	WorkerRuntimeHandler *workerruntime.Handler
	CaptureHandler       *capture.Handler
	CaptureUploadHandler *captureupload.Handler
	ProcessingHandler    *processing.Handler
}

type CaptureProcessingDependencies struct {
	Auth        auth.Store
	Exams       exam.Store
	Files       files.Store
	Objects     files.ObjectStorage
	Submissions submission.Store
	Processing  *processing.Service
}

func NewCaptureProcessingModule(cfg config.Config, stores CaptureProcessingStores, dependencies CaptureProcessingDependencies) *CaptureProcessingModule {
	module, _ := newCaptureProcessingModule(cfg, stores, dependencies, false)
	return module
}

// NewTransactionalCaptureProcessingModule is the production composition root.
// It fails startup unless the image-quality command can run through the atomic
// coordinator, without depending on a concrete store implementation name.
func NewTransactionalCaptureProcessingModule(cfg config.Config, stores CaptureProcessingStores, dependencies CaptureProcessingDependencies) (*CaptureProcessingModule, error) {
	if err := validateCaptureProcessingDependencies(dependencies); err != nil {
		return nil, err
	}
	return newCaptureProcessingModule(cfg, stores, dependencies, true)
}

// 生产模式要求 image-quality 使用事务协调器；内存模式保留普通 handler，便于测试而不改变路由契约。
func newCaptureProcessingModule(cfg config.Config, stores CaptureProcessingStores, dependencies CaptureProcessingDependencies, transactional bool) (*CaptureProcessingModule, error) {
	processingService := dependencies.Processing
	if processingService == nil {
		processingService = processing.NewService(stores.Processing, stores.WorkerRuntime)
	}
	imageQualityHandler := imagequality.NewHandler(stores.ImageQuality, dependencies.Submissions, dependencies.Files, dependencies.Auth, stores.WorkerRuntime).WithCaptureStore(stores.Capture)
	if transactional {
		coordinator, ok := stores.ImageQuality.(imagequality.TransactionalStore)
		if !ok {
			return nil, fmt.Errorf("production image quality store does not provide transactional command coordination")
		}
		var err error
		imageQualityHandler, err = imagequality.NewTransactionalHandler(imagequality.TransactionalHandlerDependencies{
			Coordinator: coordinator, Submissions: dependencies.Submissions, Files: dependencies.Files,
			Audit: dependencies.Auth, Runtime: stores.WorkerRuntime, Captures: stores.Capture,
		})
		if err != nil {
			return nil, err
		}
	}
	module := &CaptureProcessingModule{
		WorkerRuntimeStore:   stores.WorkerRuntime,
		CaptureStore:         stores.Capture,
		ProcessingService:    processingService,
		OrchestratorHandler:  orchestrator.NewHandler(stores.Orchestrator, dependencies.Auth),
		OCRHandler:           ocrpkg.NewHandler(stores.OCR, stores.OCRQueue, dependencies.Submissions, dependencies.Auth, stores.WorkerRuntime),
		ImageQualityHandler:  imageQualityHandler,
		WorkerRuntimeHandler: workerruntime.NewHandler(stores.WorkerRuntime, dependencies.Auth, workerSourceLeaseRenewer{imageQuality: stores.ImageQuality}),
		CaptureHandler:       capture.NewHandler(stores.Capture, dependencies.Files, dependencies.Exams, stores.WorkerRuntime, dependencies.Auth),
		ProcessingHandler:    processing.NewHandler(processingService, dependencies.Auth),
	}
	if lifecycleFiles, ok := dependencies.Files.(files.LifecycleStore); ok {
		module.CaptureUploadHandler = captureupload.NewHandler(
			captureupload.NewService(stores.CaptureUpload, stores.Capture, lifecycleFiles, dependencies.Objects, cfg.Files),
			dependencies.Auth,
		)
	}
	return module, nil
}

// 启动前拒绝缺失能力，避免服务已监听后才在上传或处理请求中出现 nil 接口崩溃。
func validateCaptureProcessingDependencies(dependencies CaptureProcessingDependencies) error {
	values := []struct {
		name  string
		value any
	}{
		{"Auth", dependencies.Auth},
		{"Exams", dependencies.Exams},
		{"Files", dependencies.Files},
		{"Objects", dependencies.Objects},
		{"Submissions", dependencies.Submissions},
		{"Processing", dependencies.Processing},
	}
	for _, dependency := range values {
		if isNilCapability(dependency.value) {
			return fmt.Errorf("capture dependency %s is not configured", dependency.name)
		}
	}
	return nil
}

func isNilCapability(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
