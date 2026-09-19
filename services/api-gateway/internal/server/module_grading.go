package server

import (
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/reviewannotation"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

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
