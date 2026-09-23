package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/platformschools"
	"edugrade-enterprise/services/api-gateway/internal/processing"
)

type ApplicationModules struct {
	Identity        *IdentityModule
	Exam            *ExamPreparationModule
	Capture         *CaptureProcessingModule
	Grading         *GradingQualityModule
	Release         *ReleaseModule
	AIGovernance    *AIGovernanceModule
	PlatformSchools *platformschools.Handler
	Idempotency     idempotency.Store
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
		DB: dependencies.DB, FileStore: stores.Exam.Files, ObjectStore: dependencies.ObjectStore,
		FileBucket: dependencies.Config.Files.Bucket,
	})
	aiGovernance := NewAIGovernanceModule(dependencies.Config, stores.AIGovernance, AIGovernanceDependencies{
		Auth: identity.AuthStore, WorkerRuntime: captureModule.WorkerRuntimeStore, Reviews: gradingQuality.ReviewStore,
		EligibilityHandler: aiFoundation.EligibilityHandler, GradingEvaluationHandler: aiFoundation.GradingEvaluationHandler,
		ModelCalibrationHandler: aiFoundation.ModelCalibrationHandler, DisagreementHandler: aiFoundation.DisagreementHandler,
	})
	return ApplicationModules{
		Identity: identity, Exam: examModule, Capture: captureModule, Grading: gradingQuality,
		Release: releaseModule, AIGovernance: aiGovernance, PlatformSchools: platformschools.NewHandler(stores.PlatformSchools), Idempotency: stores.Idempotency,
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
			AdapterType: connection.Config.AdapterType, ProviderKey: connection.Config.ProviderKey, BaseURL: connection.Config.BaseURL,
			APIKey: connection.APIKey, ModelName: connection.Config.ModelName, ModelVersion: connection.Config.ModelVersion,
		}, nil
	}
}
