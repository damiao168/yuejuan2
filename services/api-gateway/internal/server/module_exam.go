package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

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
