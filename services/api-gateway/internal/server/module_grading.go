package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/reviewannotation"
	"edugrade-enterprise/services/api-gateway/internal/reviewbookmark"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/taskmaterials"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
)

type GradingQualityStores struct {
	Grading          grading.Store
	Subjective       subjective.Store
	Evidence         evidence.Store
	Review           review.Store
	ReviewAnnotation reviewannotation.Store
	ReviewBookmark   reviewbookmark.Store
	GoldPaper        goldpaper.Store
	Calibration      calibration.Store
	AnswerGroup      answergroup.Store
	Backmark         backmark.Store
	Regrade          regrade.Store
	GraderDrift      graderdrift.Store
	SeedQuality      seedquality.Store
	QualityDashboard *qualitydashboard.Service
}

type GradingQualityModule struct {
	ScoringCoordinator      *grading.ScoringCoordinator
	MaterialReceipts        *subjective.MaterialReceiptStore
	ReviewStore             review.Store
	RegradeService          *regrade.Service
	QualityDashboardService *qualitydashboard.Service

	GradingHandler          *grading.Handler
	SubjectiveHandler       *subjective.Handler
	EvidenceHandler         *evidence.Handler
	ReviewHandler           *review.Handler
	ReviewAnnotationHandler *reviewannotation.Handler
	ReviewBookmarkHandler   *reviewbookmark.Handler
	GoldPaperHandler        *goldpaper.Handler
	CalibrationHandler      *calibration.Handler
	AnswerGroupHandler      *answergroup.Handler
	BackmarkHandler         *backmark.Handler
	RegradeHandler          *regrade.Handler
	GraderDriftHandler      *graderdrift.Handler
	SeedQualityHandler      *seedquality.Handler
	QualityDashboardHandler *qualitydashboard.Handler
}

type GradingQualityDependencies struct {
	ImageModelResolver     func(context.Context, string) (*subjective.ImageModelConfig, error)
	ImageModelByIDResolver func(context.Context, string, string, string) (*subjective.ImageModelConfig, error)
	ModelGovernance        modelgovernance.Store
	DB                     *sql.DB
	Auth                   auth.Store
	Exams                  exam.Store
	Schools                org.Store
	Files                  files.Store
	Objects                files.ObjectStorage
	Segments               segment.Store
	Assessments            assessment.Store
	WorkerRuntime          workerruntime.Store
	Processing             *processing.Service
	Eligibility            *aieligibility.Service
	EvaluationEvidence     subjective.EvaluationEvidenceProvider
	CalibrationEvidence    subjective.CalibrationEvidenceProvider
	DisagreementObserver   review.AIHumanDisagreementObserver
	SegmentImage           segment.CropImageReader
	FileDownload           files.DownloadReader
	MathUnderstanding      mathunderstanding.Store
	MathCorrections        mathunderstanding.CorrectionStore
	AIRequestObserver      subjective.AIGradingRequestObserver
}

// 复核、校准、种子样本和漂移服务按依赖顺序组装；可选的 math grading v2 只有在配置开启且具备证据来源时才挂载。
func NewGradingQualityModule(cfg config.Config, stores GradingQualityStores, dependencies GradingQualityDependencies) *GradingQualityModule {
	materialReceipts := subjective.NewMaterialReceiptStore(dependencies.DB, cfg.AIService.Token).WithImageAdmission(referenceImageAdmission(cfg, dependencies.ModelGovernance, stores.Subjective))
	gradingHandler := grading.NewHandler(stores.Grading, grading.NewEngine(), dependencies.Auth)
	gradingHandler.SetProductionDependencies(dependencies.WorkerRuntime, dependencies.Files)
	cropEvidence, _ := dependencies.Segments.(subjective.ActiveCropEvidenceStore)
	activeCrops := subjective.NewActiveCropResolver(cropEvidence, dependencies.Files, dependencies.Objects)

	subjectiveHandler := subjective.NewHandler(stores.Subjective, newSubjectiveAdapter(cfg, dependencies.AIRequestObserver), dependencies.Auth).
		WithImageModelResolver(dependencies.ImageModelResolver).
		WithImageModelByIDResolver(dependencies.ImageModelByIDResolver).
		WithImageCropResolver(activeCrops).
		WithReferenceMaterials(paper.NewReferenceMaterialsService(dependencies.DB, dependencies.Files, dependencies.Objects, cfg.Files.Bucket)).
		WithReferenceMaterialPreflight(referenceImagePreflight(cfg, dependencies.ModelGovernance)).
		WithWorkerRuntimeStore(dependencies.WorkerRuntime).
		WithEvaluationEvidence(dependencies.EvaluationEvidence).
		WithCalibrationEvidence(dependencies.CalibrationEvidence).
		WithParserQuality(dependencies.Processing)
	gradingHandler.WithGradingAIProbe(subjectiveHandler.ProbeExamImageModel)
	if dependencies.Eligibility != nil {
		subjectiveHandler.WithEligibilityGate(dependencies.Eligibility)
	}
	if cfg.AIService.MathGradingV2 {
		mathAdapter := newSubjectiveAdapterV2(cfg, dependencies.AIRequestObserver)
		if adapter, ok := mathAdapter.(*subjective.HTTPAdapterV2); ok {
			adapter.WithMaterialReceipts(materialReceipts)
		}
		subjectiveHandler.WithMathGradingV2(true, mathAdapter, subjective.MathEvidenceSource{
			Artifacts: dependencies.MathUnderstanding, Corrections: dependencies.MathCorrections,
		}, activeCrops)
	}

	calibrationService := calibration.NewService(stores.Calibration, stores.GoldPaper)
	if dependencies.DB != nil {
		calibrationService.WithQuestionSource(taskmaterials.PostgresReader{DB: dependencies.DB})
	}
	seedQualityService := seedquality.NewService(stores.SeedQuality, stores.GoldPaper, calibrationService, dependencies.Assessments)
	graderDriftService := graderdrift.NewService(stores.GraderDrift, seedQualityService, calibrationService)
	backmarkService := backmark.NewService(stores.Backmark)
	if contextStore, ok := stores.Review.(review.TaskContextStore); ok {
		backmarkService.WithContextSource(contextStore)
	}
	backmarkService.WithTaskSource(stores.Review)
	regradeService := regrade.NewService(stores.Regrade)
	if contextStore, ok := stores.Regrade.(regrade.ContextSource); ok {
		regradeService.WithContextSource(contextStore)
	}
	regradeHandler := regrade.NewHandler(regradeService, dependencies.Auth).WithSegmentImage(dependencies.SegmentImage)
	backmarkHandler := backmark.NewHandler(backmarkService, dependencies.Auth).
		WithSegmentImage(dependencies.SegmentImage).
		WithRegradeService(regradeService)

	qualityDashboardService := stores.QualityDashboard
	if qualityDashboardService == nil && dependencies.DB != nil {
		qualityDashboardService = newQualityDashboardService(dependencies.DB, stores, graderDriftService, backmarkService)
	}
	reviewHandler := review.NewHandler(
		stores.Review, dependencies.Auth, dependencies.SegmentImage, dependencies.FileDownload,
	).WithQualificationGate(calibrationService).
		WithReferenceMaterials(paper.NewReferenceMaterialsService(dependencies.DB, dependencies.Files, dependencies.Objects, cfg.Files.Bucket)).
		WithSeedHook(seedQualityService).
		WithSeedObservationRefresher(graderDriftService).
		WithAIHumanDisagreementObserver(dependencies.DisagreementObserver)
	if failureReader, ok := stores.Subjective.(review.AIFailureReader); ok {
		reviewHandler.WithAIFailureReader(failureReader)
	}

	module := &GradingQualityModule{
		MaterialReceipts:        materialReceipts,
		ReviewStore:             stores.Review,
		RegradeService:          regradeService,
		QualityDashboardService: qualityDashboardService,
		GradingHandler:          gradingHandler,
		SubjectiveHandler:       subjectiveHandler,
		EvidenceHandler:         evidence.NewHandler(stores.Evidence, evidence.NewEngine(), dependencies.Auth),
		ReviewHandler:           reviewHandler,
		ReviewAnnotationHandler: reviewannotation.NewHandler(stores.ReviewAnnotation, dependencies.Auth),
		ReviewBookmarkHandler:   newPersonalReviewBookmarkHandler(cfg, stores, dependencies),
		GoldPaperHandler:        goldpaper.NewHandler(stores.GoldPaper, dependencies.Auth),
		CalibrationHandler:      calibration.NewHandler(calibrationService, dependencies.Auth),
		AnswerGroupHandler:      answergroup.NewHandlerWithReferences(stores.AnswerGroup, dependencies.Auth, stores.GoldPaper).WithSegmentImage(dependencies.SegmentImage),
		BackmarkHandler:         backmarkHandler,
		RegradeHandler:          regradeHandler,
		GraderDriftHandler:      graderdrift.NewHandler(graderDriftService, dependencies.Auth),
		SeedQualityHandler:      seedquality.NewHandler(seedQualityService, dependencies.Auth),
	}
	if store, ok := stores.Grading.(*grading.PostgresStore); ok && dependencies.WorkerRuntime != nil {
		module.ScoringCoordinator = grading.NewScoringCoordinator(store, dependencies.WorkerRuntime, scoringImageExecutor{handler: subjectiveHandler}, cfg.AIService.Timeout)
	}
	if qualityDashboardService != nil {
		module.QualityDashboardHandler = qualitydashboard.NewHandler(qualityDashboardService)
	}
	return module
}

// 协调器只传不可变输入和本轮快照；观察与评分的协议校验仍由主观服务执行。
type scoringImageExecutor struct{ handler *subjective.Handler }

func (e scoringImageExecutor) Observe(ctx context.Context, tenantID string, input grading.ScoringObservationInput) (json.RawMessage, error) {
	return e.handler.ObserveStudentAnswers(ctx, tenantID, input)
}

func (e scoringImageExecutor) Schedule(ctx context.Context, tenantID, actorID string, input grading.ScoringAIInput) (string, string, error) {
	return e.handler.ScheduleScoringItem(ctx, tenantID, actorID, input)
}

func newQualityDashboardService(db *sql.DB, stores GradingQualityStores, graderDriftService *graderdrift.Service, backmarkService *backmark.Service) *qualitydashboard.Service {
	return qualitydashboard.NewService(qualitydashboard.Sources{
		Questions:   qualitydashboard.NewPostgresQuestionReader(db),
		Gold:        stores.GoldPaper,
		Calibration: qualitydashboard.NewPostgresCalibrationReader(db),
		Seeds:       stores.SeedQuality,
		Groups:      stores.AnswerGroup,
		Review:      stores.Review,
		Drift:       qualitydashboard.NewDriftReader(graderDriftService),
		Backmark:    qualitydashboard.NewBackmarkReader(backmarkService),
	})
}

// 真实服务优先；只有开发/测试或显式允许的 demo 环境才使用 mock，其余未配置情况返回禁用适配器。
func newSubjectiveAdapter(cfg config.Config, observer subjective.AIGradingRequestObserver) subjective.LLMGradingAdapter {
	if useRealAIService(cfg) {
		return subjective.NewHTTPAdapter(subjective.HTTPAdapterConfig{
			BaseURL:           cfg.AIService.URL,
			Token:             cfg.AIService.Token,
			Timeout:           cfg.AIService.Timeout,
			MaxRetries:        cfg.AIService.MaxRetries,
			ModelVersion:      cfg.AIService.ModelVersion,
			PromptVersion:     cfg.AIService.PromptVersion,
			MinConfidence:     cfg.AIService.MinConfidence,
			ProviderKey:       cfg.AIService.ProviderKey,
			DeploymentKey:     cfg.AIService.DeploymentKey,
			AdapterType:       cfg.AIService.AdapterType,
			DeploymentRegion:  cfg.AIService.DeploymentRegion,
			CapabilityProfile: cfg.AIService.CapabilityProfile,
			RequestObserver:   observer,
		})
	}
	if allowMockAI(cfg) {
		return subjective.NewMockLLMAdapter()
	}
	return subjective.NewDisabledAdapter("ai_grading_disabled", cfg.AIService.ModelVersion, cfg.AIService.PromptVersion)
}

func newSubjectiveAdapterV2(cfg config.Config, observer subjective.AIGradingRequestObserver) subjective.LLMGradingAdapter {
	if !useRealAIService(cfg) {
		return subjective.NewDisabledAdapter("math_grading_v2_not_configured", cfg.AIService.ModelVersion, cfg.AIService.PromptVersion)
	}
	return subjective.NewHTTPAdapterV2(subjective.HTTPAdapterConfig{
		BaseURL: cfg.AIService.URL, Token: cfg.AIService.Token, Timeout: cfg.AIService.Timeout, MaxRetries: cfg.AIService.MaxRetries,
		ModelVersion: cfg.AIService.ModelVersion, PromptVersion: cfg.AIService.PromptVersion, MinConfidence: cfg.AIService.MinConfidence,
		ProviderKey: cfg.AIService.ProviderKey, DeploymentKey: cfg.AIService.DeploymentKey, AdapterType: cfg.AIService.AdapterType,
		DeploymentRegion: cfg.AIService.DeploymentRegion, CapabilityProfile: cfg.AIService.CapabilityProfile,
		RequestObserver: observer,
	})
}
