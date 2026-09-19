package server

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/orchestrator"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/regraderelease"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/reviewannotation"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/studentportal"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

type IdentityStores struct {
	Auth auth.Store
	Org  org.Store
}

type IdentityModule struct {
	AuthStore   auth.Store
	AuthHandler *auth.Handler
	OrgHandler  *org.Handler
}

func NewIdentityModule(cfg config.Config, stores IdentityStores, loginGuard auth.LoginAttemptGuard) *IdentityModule {
	return &IdentityModule{
		AuthStore: stores.Auth,
		AuthHandler: auth.NewHandler(stores.Auth, cfg.Auth.SessionTTL, auth.HandlerOptions{
			LoginFailureLimit:      cfg.Auth.LoginFailureLimit,
			LoginFailureWindow:     cfg.Auth.LoginFailureWindow,
			RememberedSessionTTL:   cfg.Auth.RememberedSessionTTL,
			PublicSessionTTL:       cfg.Auth.PublicSessionTTL,
			CookieName:             cfg.Auth.SessionCookieName,
			DeviceCookieName:       cfg.Auth.DeviceCookieName,
			CookieSecure:           cfg.Auth.SessionCookieSecure,
			RiskMode:               cfg.Auth.RiskMode,
			DeviceBindingTTL:       cfg.Auth.DeviceBindingTTL,
			MFAEnabled:             cfg.Auth.MFAEnabled,
			MFAMasterKey:           cfg.Auth.MFAMasterKey,
			LoginGuard:             loginGuard,
			LoginLimiterFailClosed: cfg.Auth.LoginLimiterFailClosed,
			TrustedProxyCIDRs:      cfg.Security.TrustedProxyCIDRs,
		}),
		OrgHandler: org.NewHandler(stores.Org, stores.Auth),
	}
}

type ExamPreparationStores struct {
	Exam                   exam.Store
	Paper                  paper.Store
	Files                  files.Store
	Submissions            submission.Store
	Segments               segment.Store
	Assessments            assessment.Store
	QuestionBank           questionbank.Store
	DashboardOrganizations dashboard.OrganizationSummaryStore
	DashboardActivities    dashboard.ActivityStore
}

type ExamPreparationModule struct {
	ExamStore          exam.Store
	PaperStore         paper.Store
	FileStore          files.Store
	ObjectStore        files.ObjectStorage
	SubmissionStore    submission.Store
	SegmentStore       segment.Store
	AssessmentStore    assessment.Store
	PaperImportService *paper.DocumentImportService

	ExamHandler         *exam.Handler
	PaperHandler        *paper.Handler
	FileHandler         *files.Handler
	SubmissionHandler   *submission.Handler
	SegmentHandler      *segment.Handler
	SegmentImages       *segment.ImageService
	FileDownloads       *files.DownloadService
	AssessmentHandler   *assessment.Handler
	QuestionBankHandler *questionbank.Handler
	WorkspaceHandler    *workspace.Handler
	DashboardHandler    *dashboard.Handler
}

type ExamPreparationDependencies struct {
	AuthStore             auth.Store
	ObjectStore           files.ObjectStorage
	Reconciliation        files.ReconciliationReader
	Reviews               review.Store
	Processing            workspace.ProcessingReader
	DocumentModelResolver func(context.Context, string) (*paper.DocumentModelConfig, error)
}

func NewExamPreparationModule(cfg config.Config, stores ExamPreparationStores, dependencies ExamPreparationDependencies) *ExamPreparationModule {
	fileHandler := files.NewHandler(stores.Files, dependencies.ObjectStore, dependencies.AuthStore, cfg.Files).WithReconciliationReader(dependencies.Reconciliation)
	segmentHandler := segment.NewHandler(stores.Segments, stores.Paper, stores.Submissions, dependencies.AuthStore, stores.Files, dependencies.ObjectStore)
	paperImportService := paper.NewDocumentImportService(stores.Paper, stores.Files, dependencies.ObjectStore, cfg.AIService.URL, cfg.AIService.Token, cfg.AIService.Timeout)
	paperHandler := paper.NewHandler(stores.Paper, dependencies.AuthStore).WithDocumentImport(paperImportService)
	if dependencies.DocumentModelResolver != nil {
		paperHandler.WithDocumentModelResolver(dependencies.DocumentModelResolver)
	}
	return &ExamPreparationModule{
		ExamStore:           stores.Exam,
		PaperStore:          stores.Paper,
		FileStore:           stores.Files,
		ObjectStore:         dependencies.ObjectStore,
		SubmissionStore:     stores.Submissions,
		SegmentStore:        stores.Segments,
		AssessmentStore:     stores.Assessments,
		PaperImportService:  paperImportService,
		ExamHandler:         exam.NewHandler(stores.Exam, dependencies.AuthStore),
		PaperHandler:        paperHandler,
		FileHandler:         fileHandler,
		SubmissionHandler:   submission.NewHandler(stores.Submissions, stores.Files, dependencies.AuthStore),
		SegmentHandler:      segmentHandler,
		SegmentImages:       segment.NewImageService(stores.Segments, stores.Submissions, stores.Files, dependencies.ObjectStore),
		FileDownloads:       files.NewDownloadService(stores.Files, dependencies.ObjectStore),
		AssessmentHandler:   assessment.NewHandler(stores.Assessments, dependencies.AuthStore),
		QuestionBankHandler: questionbank.NewHandler(stores.QuestionBank),
		WorkspaceHandler: workspace.NewHandler(workspace.Dependencies{
			Exams: stores.Exam, Papers: stores.Paper, PaperImports: stores.Paper, Submissions: stores.Submissions, Assessments: stores.Assessments,
			Reviews: dependencies.Reviews, Processing: dependencies.Processing,
		}),
		DashboardHandler: dashboard.NewHandler(dashboard.Dependencies{
			Exams: stores.Exam, Submissions: stores.Submissions, Reviews: dependencies.Reviews, Audits: dependencies.AuthStore,
			Organizations: stores.DashboardOrganizations, Activities: stores.DashboardActivities,
		}),
	}
}

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

type AIFoundationStores struct {
	Eligibility       aieligibility.Store
	GradingEvaluation gradingevaluation.Store
	ModelCalibration  modelcalibration.Store
	Disagreement      aidisagreement.Store
}

type AIFoundation struct {
	EligibilityService       *aieligibility.Service
	GradingEvaluationService *gradingevaluation.Service
	ModelCalibrationService  *modelcalibration.Service
	DisagreementService      *aidisagreement.Service

	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

func NewAIFoundation(stores AIFoundationStores) *AIFoundation {
	gradingEvaluationService := gradingevaluation.NewService(stores.GradingEvaluation)
	modelCalibrationService := modelcalibration.NewService(stores.ModelCalibration, modelcalibration.NewEvaluationReader(gradingEvaluationService))
	disagreementService := aidisagreement.NewService(stores.Disagreement)
	module := &AIFoundation{
		GradingEvaluationService: gradingEvaluationService,
		ModelCalibrationService:  modelCalibrationService,
		DisagreementService:      disagreementService,
		GradingEvaluationHandler: gradingevaluation.NewHandler(gradingEvaluationService),
		ModelCalibrationHandler:  modelcalibration.NewHandler(modelCalibrationService),
		DisagreementHandler:      aidisagreement.NewHandler(disagreementService),
	}
	if stores.Eligibility != nil {
		module.EligibilityService = aieligibility.NewService(stores.Eligibility)
		module.EligibilityHandler = aieligibility.NewHandler(module.EligibilityService)
	}
	return module
}

type GradingQualityStores struct {
	Grading          grading.Store
	Subjective       subjective.Store
	Evidence         evidence.Store
	Review           review.Store
	ReviewAnnotation reviewannotation.Store
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
	ReviewStore             review.Store
	RegradeService          *regrade.Service
	QualityDashboardService *qualitydashboard.Service

	GradingHandler          *grading.Handler
	SubjectiveHandler       *subjective.Handler
	EvidenceHandler         *evidence.Handler
	ReviewHandler           *review.Handler
	ReviewAnnotationHandler *reviewannotation.Handler
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
	DB                   *sql.DB
	Auth                 auth.Store
	Files                files.Store
	Objects              files.ObjectStorage
	Segments             segment.Store
	Assessments          assessment.Store
	WorkerRuntime        workerruntime.Store
	Processing           *processing.Service
	Eligibility          *aieligibility.Service
	EvaluationEvidence   subjective.EvaluationEvidenceProvider
	CalibrationEvidence  subjective.CalibrationEvidenceProvider
	DisagreementObserver review.AIHumanDisagreementObserver
	SegmentImage         segment.CropImageReader
	FileDownload         files.DownloadReader
	MathUnderstanding    mathunderstanding.Store
	MathCorrections      mathunderstanding.CorrectionStore
}

func NewGradingQualityModule(cfg config.Config, stores GradingQualityStores, dependencies GradingQualityDependencies) *GradingQualityModule {
	gradingHandler := grading.NewHandler(stores.Grading, grading.NewEngine(), dependencies.Auth)
	gradingHandler.SetProductionDependencies(dependencies.WorkerRuntime, dependencies.Files)

	subjectiveHandler := subjective.NewHandler(stores.Subjective, newSubjectiveAdapter(cfg), dependencies.Auth).
		WithWorkerRuntimeStore(dependencies.WorkerRuntime).
		WithEvaluationEvidence(dependencies.EvaluationEvidence).
		WithCalibrationEvidence(dependencies.CalibrationEvidence).
		WithParserQuality(dependencies.Processing)
	if dependencies.Eligibility != nil {
		subjectiveHandler.WithEligibilityGate(dependencies.Eligibility)
	}
	if cfg.AIService.MathGradingV2 {
		cropEvidence, _ := dependencies.Segments.(subjective.ActiveCropEvidenceStore)
		subjectiveHandler.WithMathGradingV2(true, newSubjectiveAdapterV2(cfg), subjective.MathEvidenceSource{
			Artifacts: dependencies.MathUnderstanding, Corrections: dependencies.MathCorrections,
		}, subjective.NewActiveCropResolver(cropEvidence, dependencies.Files, dependencies.Objects))
	}

	calibrationService := calibration.NewService(stores.Calibration, stores.GoldPaper)
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

	module := &GradingQualityModule{
		ReviewStore:             stores.Review,
		RegradeService:          regradeService,
		QualityDashboardService: qualityDashboardService,
		GradingHandler:          gradingHandler,
		SubjectiveHandler:       subjectiveHandler,
		EvidenceHandler:         evidence.NewHandler(stores.Evidence, evidence.NewEngine(), dependencies.Auth),
		ReviewHandler: review.NewHandler(
			stores.Review, dependencies.Auth, dependencies.SegmentImage, dependencies.FileDownload,
		).WithQualificationGate(calibrationService).
			WithSeedHook(seedQualityService).
			WithSeedObservationRefresher(graderDriftService).
			WithAIHumanDisagreementObserver(dependencies.DisagreementObserver),
		ReviewAnnotationHandler: reviewannotation.NewHandler(stores.ReviewAnnotation, dependencies.Auth),
		GoldPaperHandler:        goldpaper.NewHandler(stores.GoldPaper, dependencies.Auth),
		CalibrationHandler:      calibration.NewHandler(calibrationService, dependencies.Auth),
		AnswerGroupHandler:      answergroup.NewHandlerWithReferences(stores.AnswerGroup, dependencies.Auth, stores.GoldPaper),
		BackmarkHandler:         backmarkHandler,
		RegradeHandler:          regradeHandler,
		GraderDriftHandler:      graderdrift.NewHandler(graderDriftService, dependencies.Auth),
		SeedQualityHandler:      seedquality.NewHandler(seedQualityService, dependencies.Auth),
	}
	if qualityDashboardService != nil {
		module.QualityDashboardHandler = qualitydashboard.NewHandler(qualityDashboardService)
	}
	return module
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

func newSubjectiveAdapter(cfg config.Config) subjective.LLMGradingAdapter {
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
		})
	}
	if allowMockAI(cfg) {
		return subjective.NewMockLLMAdapter()
	}
	return subjective.NewDisabledAdapter("ai_grading_disabled", cfg.AIService.ModelVersion, cfg.AIService.PromptVersion)
}

func newSubjectiveAdapterV2(cfg config.Config) subjective.LLMGradingAdapter {
	if !useRealAIService(cfg) {
		return subjective.NewDisabledAdapter("math_grading_v2_not_configured", cfg.AIService.ModelVersion, cfg.AIService.PromptVersion)
	}
	return subjective.NewHTTPAdapterV2(subjective.HTTPAdapterConfig{
		BaseURL: cfg.AIService.URL, Token: cfg.AIService.Token, Timeout: cfg.AIService.Timeout, MaxRetries: cfg.AIService.MaxRetries,
		ModelVersion: cfg.AIService.ModelVersion, PromptVersion: cfg.AIService.PromptVersion, MinConfidence: cfg.AIService.MinConfidence,
		ProviderKey: cfg.AIService.ProviderKey, DeploymentKey: cfg.AIService.DeploymentKey, AdapterType: cfg.AIService.AdapterType,
		DeploymentRegion: cfg.AIService.DeploymentRegion, CapabilityProfile: cfg.AIService.CapabilityProfile,
	})
}

type ReleaseStores struct {
	Score                   score.Store
	ScoreRelease            scorerelease.Store
	ReleaseGate             releasegate.Store
	StudentPortal           studentportal.Store
	Appeal                  appeal.Store
	PublishedQuestionAppeal appeal.PublishedQuestionAppealStore
	Report                  report.Store
}

type ReleaseModule struct {
	ScoreHandler                   *score.Handler
	ScoreReleaseHandler            *scorerelease.Handler
	ReleaseGateHandler             *releasegate.Handler
	StudentPortalHandler           *studentportal.Handler
	RegradeReleaseHandler          *regraderelease.Handler
	AppealHandler                  *appeal.Handler
	PublishedQuestionAppealHandler *appeal.PublishedQuestionAppealHandler
	ReportHandler                  *report.Handler
}

type ReleaseDependencies struct {
	Auth                  auth.Store
	Regrade               *regrade.Service
	StudentQuestionImage  segment.CropImageReader
	StudentPaperPageImage segment.PageImageReader
}

func NewReleaseModule(stores ReleaseStores, dependencies ReleaseDependencies) *ReleaseModule {
	scoreReleaseService := scorerelease.NewService(stores.ScoreRelease)
	releaseGateService := releasegate.NewService(stores.ReleaseGate, scoreReleaseService).
		WithRegradeBlockerReader(regradeBlocker{service: dependencies.Regrade})
	return &ReleaseModule{
		ScoreHandler: score.NewHandler(stores.Score, dependencies.Auth),
		ScoreReleaseHandler: scorerelease.NewHandler(scoreReleaseService, dependencies.Auth).
			WithPublicationPublisher(releaseGatePublisher{
				coordinator: releasegate.NewPublicationCoordinator(releaseGateService, scoreReleaseService),
			}).
			WithStudentQuestionImage(dependencies.StudentQuestionImage).
			WithStudentPaperPageImage(dependencies.StudentPaperPageImage),
		ReleaseGateHandler:   releasegate.NewHandler(releaseGateService, dependencies.Auth),
		StudentPortalHandler: studentportal.NewHandler(studentportal.NewService(stores.StudentPortal)),
		RegradeReleaseHandler: regraderelease.NewHandler(
			regraderelease.NewService(dependencies.Regrade, scoreReleaseService),
		),
		AppealHandler: appeal.NewHandler(stores.Appeal, dependencies.Auth),
		PublishedQuestionAppealHandler: appeal.NewPublishedQuestionAppealHandler(
			appeal.NewPublishedQuestionAppealService(stores.PublishedQuestionAppeal), dependencies.Auth,
		).WithSegmentImage(dependencies.StudentQuestionImage),
		ReportHandler: report.NewHandler(stores.Report, dependencies.Auth),
	}
}

type AIGovernanceStores struct {
	ModelGovernance   modelgovernance.Store
	MathUnderstanding mathunderstanding.Store
	MathCorrections   mathunderstanding.CorrectionStore
	MathPilotGates    mathunderstanding.PilotGateStore
}

type AIGovernanceModule struct {
	modelStore               modelgovernance.Store
	ModelGovernanceHandler   *modelgovernance.Handler
	MathUnderstandingHandler *mathunderstanding.Handler
	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

type AIGovernanceDependencies struct {
	Auth                     auth.Store
	WorkerRuntime            workerruntime.Store
	Reviews                  review.Store
	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

func NewAIGovernanceModule(cfg config.Config, stores AIGovernanceStores, dependencies AIGovernanceDependencies) *AIGovernanceModule {
	return &AIGovernanceModule{
		modelStore:               stores.ModelGovernance,
		EligibilityHandler:       dependencies.EligibilityHandler,
		GradingEvaluationHandler: dependencies.GradingEvaluationHandler,
		ModelCalibrationHandler:  dependencies.ModelCalibrationHandler,
		DisagreementHandler:      dependencies.DisagreementHandler,
		ModelGovernanceHandler: modelgovernance.NewHandler(
			stores.ModelGovernance,
			dependencies.Auth,
			modelgovernance.NewEnvironmentSecretResolver(""),
			localModelBaseline(cfg),
		).WithRuntimePromptSource(modelgovernance.NewHTTPRuntimePromptSource(
			cfg.AIService.URL,
			cfg.AIService.Token,
			cfg.AIService.Timeout,
		)),
		MathUnderstandingHandler: mathunderstanding.NewHandler(
			stores.MathUnderstanding, stores.MathCorrections, stores.MathPilotGates, dependencies.Reviews, dependencies.Auth,
		).WithRuntime(dependencies.WorkerRuntime),
	}
}

type ApplicationModules struct {
	Identity     *IdentityModule
	Exam         *ExamPreparationModule
	Capture      *CaptureProcessingModule
	Grading      *GradingQualityModule
	Release      *ReleaseModule
	AIGovernance *AIGovernanceModule
	Idempotency  idempotency.Store
}

type ApplicationStores struct {
	Identity     IdentityStores
	Exam         ExamPreparationStores
	Capture      CaptureProcessingStores
	AIFoundation AIFoundationStores
	Grading      GradingQualityStores
	Release      ReleaseStores
	AIGovernance AIGovernanceStores
	Idempotency  idempotency.Store
}

func NewMemoryApplicationStores() ApplicationStores {
	mathStore := mathunderstanding.NewMemoryStore()
	stores := ApplicationStores{
		Identity: IdentityStores{Auth: auth.NewMemoryStore(), Org: org.NewMemoryStore()},
		Exam: ExamPreparationStores{
			Exam: exam.NewMemoryStore(), Paper: paper.NewMemoryStore(), Files: files.NewMemoryStore(),
			Submissions: submission.NewMemoryStore(), Segments: segment.NewMemoryStore(), Assessments: assessment.NewMemoryStore(),
			QuestionBank: questionbank.NewMemoryStore(),
		},
		Capture: CaptureProcessingStores{
			ImageQuality: imagequality.NewMemoryStore(), WorkerRuntime: workerruntime.NewMemoryStore(),
			OCR: ocrpkg.NewMemoryStore(), OCRQueue: ocrpkg.NewMemoryQueue(), Orchestrator: orchestrator.NewMemoryStore(),
			Capture: capture.NewMemoryStore(), CaptureUpload: captureupload.NewMemoryStore(), Processing: processing.NewMemoryStore(),
		},
		AIFoundation: AIFoundationStores{
			GradingEvaluation: gradingevaluation.NewMemoryStore(), ModelCalibration: modelcalibration.NewMemoryStore(),
			Disagreement: aidisagreement.NewMemoryStore(),
		},
		Grading: GradingQualityStores{
			Grading: grading.NewMemoryStore(), Subjective: subjective.NewMemoryStore(), Evidence: evidence.NewMemoryStore(),
			Review: review.NewMemoryStore(), ReviewAnnotation: reviewannotation.NewMemoryStore(),
			GoldPaper: goldpaper.NewMemoryStore(), Calibration: calibration.NewMemoryStore(),
			AnswerGroup: answergroup.NewMemoryStore(nil, answergroup.DefaultPolicy()), Backmark: backmark.NewMemoryStore(),
			Regrade: regrade.NewMemoryStore(), GraderDrift: graderdrift.NewMemoryStore(), SeedQuality: seedquality.NewMemoryStore(),
		},
		Release: ReleaseStores{
			Score: score.NewMemoryStore(), ScoreRelease: scorerelease.NewMemoryStore(), ReleaseGate: releasegate.NewMemoryStore(),
			StudentPortal: studentportal.NewMemoryStore(), Appeal: appeal.NewMemoryStore(),
			PublishedQuestionAppeal: appeal.NewPublishedQuestionAppealMemoryStore(), Report: report.NewMemoryStore(),
		},
		AIGovernance: AIGovernanceStores{
			ModelGovernance: modelgovernance.NewMemoryStore(), MathUnderstanding: mathStore,
			MathCorrections: mathunderstanding.NewMemoryCorrectionStore(mathStore), MathPilotGates: mathunderstanding.NewMemoryPilotGateStore(),
		},
		Idempotency: idempotency.NewMemoryStore(),
	}
	return stores
}

// NewPostgresApplicationStores is the single production persistence graph.
// Integration tests use this factory too, so adding a new application store
// cannot silently leave the production-style suite backed by memory.
func NewPostgresApplicationStores(infra *Infrastructure) (ApplicationStores, error) {
	assessmentStore := assessment.NewPostgresStore(infra.DB)
	gradingStores := GradingQualityStores{
		Grading:          grading.NewPostgresStore(infra.DB),
		Subjective:       subjective.NewPostgresStore(infra.DB),
		Evidence:         evidence.NewPostgresStore(infra.DB),
		Review:           review.NewPostgresStore(infra.DB),
		ReviewAnnotation: reviewannotation.NewPostgresStore(infra.DB),
		GoldPaper:        goldpaper.NewPostgresStore(infra.DB),
		Calibration:      calibration.NewPostgresStore(infra.DB),
		AnswerGroup:      answergroup.NewPostgresStore(infra.DB, nil, answergroup.DefaultPolicy()),
		Backmark:         backmark.NewPostgresStore(infra.DB),
		Regrade:          regrade.NewPostgresStore(infra.DB),
		GraderDrift:      graderdrift.NewPostgresStore(infra.DB),
		SeedQuality:      seedquality.NewPostgresStore(infra.DB),
	}
	gradingStores.QualityDashboard = newPostgresQualityDashboard(infra.DB, gradingStores, assessmentStore)

	credentialCipher, err := modelgovernance.NewCredentialCipher(infra.Config.ModelSecrets.MasterKey)
	if err != nil {
		return ApplicationStores{}, err
	}
	governanceStore := modelgovernance.NewPostgresStore(infra.DB, credentialCipher)
	if err := governanceStore.EnsureLocalBaseline(context.Background(), "", localModelBaseline(infra.Config)); err != nil {
		return ApplicationStores{}, err
	}
	if strings.EqualFold(strings.TrimSpace(infra.Config.Service.Environment), "production") && infra.Config.AIService.Enabled {
		if err := governanceStore.ValidateProductionReadiness(context.Background(), modelgovernance.NewEnvironmentSecretResolver("")); err != nil {
			return ApplicationStores{}, err
		}
	}
	mathStore := mathunderstanding.NewPostgresStore(infra.DB)

	stores := ApplicationStores{
		Identity: IdentityStores{Auth: auth.NewPostgresStore(infra.DB), Org: org.NewPostgresStore(infra.DB)},
		Exam: ExamPreparationStores{
			Exam: exam.NewPostgresStore(infra.DB), Paper: paper.NewPostgresStore(infra.DB), Files: files.NewPostgresStore(infra.DB),
			Submissions: submission.NewPostgresStore(infra.DB), Segments: segment.NewPostgresStore(infra.DB), Assessments: assessmentStore,
			QuestionBank:           questionbank.NewPostgresStore(infra.DB),
			DashboardOrganizations: dashboard.NewPostgresOrganizationSummaryStore(infra.DB), DashboardActivities: dashboard.NewPostgresActivityStore(infra.DB),
		},
		Capture: CaptureProcessingStores{
			ImageQuality: imagequality.NewPostgresStore(infra.DB), WorkerRuntime: workerruntime.NewPostgresStore(infra.DB),
			OCR: ocrpkg.NewPostgresStore(infra.DB), Orchestrator: orchestrator.NewPostgresStore(infra.DB),
			Capture: capture.NewPostgresStoreWithBarcodeKeyring(infra.DB, capture.BarcodeKeyring{
				ActiveKeyID: infra.Config.Barcode.ActiveKeyID, Keys: infra.Config.Barcode.HMACKeys,
			}),
			CaptureUpload: captureupload.NewPostgresStore(infra.DB), Processing: processing.NewPostgresStore(infra.DB),
		},
		AIFoundation: AIFoundationStores{
			Eligibility: aieligibility.NewPostgresStore(infra.DB), GradingEvaluation: gradingevaluation.NewPostgresStore(infra.DB),
			ModelCalibration: modelcalibration.NewPostgresStore(infra.DB), Disagreement: aidisagreement.NewPostgresStore(infra.DB),
		},
		Grading: gradingStores,
		Release: ReleaseStores{
			Score: score.NewPostgresStore(infra.DB), ScoreRelease: scorerelease.NewPostgresStore(infra.DB, gradingStores.QualityDashboard),
			ReleaseGate: releasegate.NewPostgresStore(infra.DB), StudentPortal: studentportal.NewPostgresStore(infra.DB),
			Appeal: appeal.NewPostgresStore(infra.DB), PublishedQuestionAppeal: appeal.NewPublishedQuestionAppealPostgresStore(infra.DB),
			Report: report.NewPostgresStore(infra.DB),
		},
		AIGovernance: AIGovernanceStores{
			ModelGovernance: governanceStore, MathUnderstanding: mathStore,
			MathCorrections: mathunderstanding.NewPostgresCorrectionStore(infra.DB, mathStore), MathPilotGates: mathunderstanding.NewPostgresPilotGateStore(infra.DB),
		},
		Idempotency: idempotency.NewPostgresStore(infra.DB),
	}
	if err := validatePostgresStoreGraph(stores); err != nil {
		return ApplicationStores{}, err
	}
	return stores, nil
}

func validatePostgresStoreGraph(stores ApplicationStores) error {
	return validatePostgresStoreValue(reflect.ValueOf(stores), "stores")
}

func validatePostgresStoreValue(value reflect.Value, path string) error {
	typeOfValue := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := path + "." + typeOfValue.Field(index).Name
		if name == "stores.Capture.OCRQueue" {
			// PostgreSQL worker runtime is the durable OCR queue. This legacy
			// seam is used only by the memory router when no runtime is present.
			continue
		}
		switch field.Kind() {
		case reflect.Struct:
			if err := validatePostgresStoreValue(field, name); err != nil {
				return err
			}
		case reflect.Interface, reflect.Pointer:
			for field.Kind() == reflect.Interface && !field.IsNil() {
				field = field.Elem()
			}
			if field.Kind() != reflect.Pointer && field.Kind() != reflect.Interface {
				continue
			}
			if field.IsNil() {
				return fmt.Errorf("production application store %s is not configured", name)
			}
		}
	}
	return nil
}

func newPostgresQualityDashboard(db *sql.DB, stores GradingQualityStores, assessments assessment.Store) *qualitydashboard.Service {
	calibrationService := calibration.NewService(stores.Calibration, stores.GoldPaper)
	seedQualityService := seedquality.NewService(stores.SeedQuality, stores.GoldPaper, calibrationService, assessments)
	graderDriftService := graderdrift.NewService(stores.GraderDrift, seedQualityService, calibrationService)
	backmarkService := backmark.NewService(stores.Backmark)
	if contextStore, ok := stores.Review.(review.TaskContextStore); ok {
		backmarkService.WithContextSource(contextStore)
	}
	backmarkService.WithTaskSource(stores.Review)
	return newQualityDashboardService(db, stores, graderDriftService, backmarkService)
}

type ApplicationDependencies struct {
	Config         config.Config
	ObjectStore    files.ObjectStorage
	Reconciliation files.ReconciliationReader
	LoginGuard     auth.LoginAttemptGuard
	DB             *sql.DB
}

func NewApplicationModules(dependencies ApplicationDependencies, stores ApplicationStores) ApplicationModules {
	modules, _ := newApplicationModules(dependencies, stores, false)
	return modules
}

func NewTransactionalApplicationModules(dependencies ApplicationDependencies, stores ApplicationStores) (ApplicationModules, error) {
	if err := validatePostgresStoreGraph(stores); err != nil {
		return ApplicationModules{}, err
	}
	return newApplicationModules(dependencies, stores, true)
}

func newApplicationModules(dependencies ApplicationDependencies, stores ApplicationStores, transactional bool) (ApplicationModules, error) {
	identity := NewIdentityModule(dependencies.Config, stores.Identity, dependencies.LoginGuard)
	processingService := processing.NewService(stores.Capture.Processing, stores.Capture.WorkerRuntime)
	examModule := NewExamPreparationModule(dependencies.Config, stores.Exam, ExamPreparationDependencies{
		AuthStore: identity.AuthStore, ObjectStore: dependencies.ObjectStore, Reconciliation: dependencies.Reconciliation,
		Reviews: stores.Grading.Review, Processing: processingService,
		DocumentModelResolver: newDocumentModelResolver(stores.AIGovernance.ModelGovernance),
	})
	captureDependencies := CaptureProcessingDependencies{
		Auth: identity.AuthStore, Exams: stores.Exam.Exam, Files: stores.Exam.Files,
		Objects: dependencies.ObjectStore, Submissions: stores.Exam.Submissions, Processing: processingService,
	}
	var captureModule *CaptureProcessingModule
	var err error
	if transactional {
		captureModule, err = NewTransactionalCaptureProcessingModule(dependencies.Config, stores.Capture, captureDependencies)
	} else {
		captureModule = NewCaptureProcessingModule(dependencies.Config, stores.Capture, captureDependencies)
	}
	if err != nil {
		return ApplicationModules{}, err
	}
	aiFoundation := NewAIFoundation(stores.AIFoundation)
	gradingQuality := NewGradingQualityModule(dependencies.Config, stores.Grading, GradingQualityDependencies{
		DB: dependencies.DB, Auth: identity.AuthStore, Files: stores.Exam.Files, Objects: dependencies.ObjectStore,
		Segments: stores.Exam.Segments, Assessments: stores.Exam.Assessments,
		WorkerRuntime: captureModule.WorkerRuntimeStore, Processing: captureModule.ProcessingService,
		Eligibility: aiFoundation.EligibilityService, EvaluationEvidence: aiFoundation.GradingEvaluationService,
		CalibrationEvidence: aiFoundation.ModelCalibrationService, DisagreementObserver: aiFoundation.DisagreementService,
		SegmentImage: examModule.SegmentImages, FileDownload: examModule.FileDownloads,
		MathUnderstanding: stores.AIGovernance.MathUnderstanding, MathCorrections: stores.AIGovernance.MathCorrections,
	})
	releaseModule := NewReleaseModule(stores.Release, ReleaseDependencies{
		Auth: identity.AuthStore, Regrade: gradingQuality.RegradeService,
		StudentQuestionImage: examModule.SegmentImages, StudentPaperPageImage: examModule.SegmentImages,
	})
	aiGovernance := NewAIGovernanceModule(dependencies.Config, stores.AIGovernance, AIGovernanceDependencies{
		Auth: identity.AuthStore, WorkerRuntime: captureModule.WorkerRuntimeStore, Reviews: gradingQuality.ReviewStore,
		EligibilityHandler: aiFoundation.EligibilityHandler, GradingEvaluationHandler: aiFoundation.GradingEvaluationHandler,
		ModelCalibrationHandler: aiFoundation.ModelCalibrationHandler, DisagreementHandler: aiFoundation.DisagreementHandler,
	})
	return ApplicationModules{
		Identity: identity, Exam: examModule, Capture: captureModule, Grading: gradingQuality,
		Release: releaseModule, AIGovernance: aiGovernance, Idempotency: stores.Idempotency,
	}, nil
}

func NewPostgresApplicationModules(infra *Infrastructure) (ApplicationModules, error) {
	stores, err := NewPostgresApplicationStores(infra)
	if err != nil {
		return ApplicationModules{}, err
	}
	return NewTransactionalApplicationModules(ApplicationDependencies{
		Config: infra.Config, ObjectStore: infra.ObjectStore, Reconciliation: infra.FileReconciler,
		LoginGuard: infra.LoginGuard, DB: infra.DB,
	}, stores)
}

func newDocumentModelResolver(store modelgovernance.Store) func(context.Context, string) (*paper.DocumentModelConfig, error) {
	managed, ok := store.(modelgovernance.ManagedAPIConfigStore)
	if !ok {
		return nil
	}
	return func(ctx context.Context, tenantID string) (*paper.DocumentModelConfig, error) {
		connection, err := modelgovernance.ResolveDefaultManagedAPI(ctx, managed, tenantID)
		if err != nil || connection == nil {
			return nil, err
		}
		return &paper.DocumentModelConfig{
			AdapterType: connection.Config.AdapterType, BaseURL: connection.Config.BaseURL,
			APIKey: connection.APIKey, ModelName: connection.Config.ModelName, ModelVersion: connection.Config.ModelVersion,
		}, nil
	}
}
