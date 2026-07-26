package server

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/deps"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/handlers"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/middleware"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/orchestrator"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Server struct {
	handler http.Handler
}

func New(cfg config.Config, logg *logger.Logger) (*Server, func(), error) {
	checkers := make([]deps.Checker, 0, 5)
	cleanups := make([]func() error, 0, 2)

	postgresDB, closePostgres, err := db.OpenPostgres(cfg.Postgres)
	if err != nil {
		return nil, nil, err
	}
	authStore := auth.NewPostgresStore(postgresDB)
	orgStore := org.NewPostgresStore(postgresDB)
	examStore := exam.NewPostgresStore(postgresDB)
	paperStore := paper.NewPostgresStore(postgresDB)
	fileStore := files.NewPostgresStore(postgresDB)
	submissionStore := submission.NewPostgresStore(postgresDB)
	imageQualityStore := imagequality.NewPostgresStore(postgresDB)
	workerRuntimeStore := workerruntime.NewPostgresStore(postgresDB)
	ocrStore := ocrpkg.NewPostgresStore(postgresDB)
	ocrQueue := ocrpkg.NewMemoryQueue()
	segmentStore := segment.NewPostgresStore(postgresDB)
	orchestratorStore := orchestrator.NewPostgresStore(postgresDB)
	gradingStore := grading.NewPostgresStore(postgresDB)
	subjectiveStore := subjective.NewPostgresStore(postgresDB)
	evidenceStore := evidence.NewPostgresStore(postgresDB)
	reviewStore := review.NewPostgresStore(postgresDB)
	scoreStore := score.NewPostgresStore(postgresDB)
	appealStore := appeal.NewPostgresStore(postgresDB)
	reportStore := report.NewPostgresStore(postgresDB)
	captureStore := capture.NewPostgresStoreWithBarcodeKeyring(postgresDB, capture.BarcodeKeyring{ActiveKeyID: cfg.Barcode.ActiveKeyID, Keys: cfg.Barcode.HMACKeys})
	postgresChecker := deps.NewPostgresChecker(postgresDB)
	checkers = append(checkers, postgresChecker)
	cleanups = append(cleanups, closePostgres)

	redisChecker, closeRedis := deps.NewRedisChecker(cfg.Redis)
	checkers = append(checkers, redisChecker)
	cleanups = append(cleanups, closeRedis)

	minioChecker, err := deps.NewMinIOChecker(cfg.MinIO)
	if err != nil {
		return nil, nil, err
	}
	checkers = append(checkers, minioChecker)
	checkers = append(checkers, deps.NewQdrantChecker(cfg.Qdrant))
	checkers = append(checkers, deps.NewAIServiceChecker(cfg.AIService))
	objectStore, err := files.NewMinIOObjectStorage(cfg.MinIO)
	if err != nil {
		return nil, nil, err
	}

	router := NewRouterComplete(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, fileStore, objectStore, submissionStore, ocrStore, ocrQueue, segmentStore, imageQualityStore, workerRuntimeStore, orchestratorStore, gradingStore, subjectiveStore, evidenceStore, reviewStore, scoreStore, appealStore, reportStore, captureStore)
	cleanup := func() {
		for _, closeFn := range cleanups {
			_ = closeFn()
		}
	}
	return &Server{handler: router}, cleanup, nil
}

func NewRouter(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store) http.Handler {
	return NewRouterWithFiles(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
}

func NewRouterWithFiles(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store, fileStore files.Store, objectStore files.ObjectStorage) http.Handler {
	return NewRouterFull(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, fileStore, objectStore, submission.NewMemoryStore())
}

func NewRouterFull(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store, fileStore files.Store, objectStore files.ObjectStorage, submissionStore submission.Store) http.Handler {
	return NewRouterComplete(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, fileStore, objectStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue(), segment.NewMemoryStore())
}

func NewRouterComplete(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store, fileStore files.Store, objectStore files.ObjectStorage, submissionStore submission.Store, ocrStore ocrpkg.Store, ocrQueue ocrpkg.Queue, segmentStore segment.Store, optionalStores ...any) http.Handler {
	mux := http.NewServeMux()
	h := handlers.New(cfg, checkers)
	authHandler := auth.NewHandler(authStore, cfg.Auth.SessionTTL, auth.HandlerOptions{
		LoginFailureLimit:  cfg.Auth.LoginFailureLimit,
		LoginFailureWindow: cfg.Auth.LoginFailureWindow,
		CookieName:         cfg.Auth.SessionCookieName,
		CookieSecure:       cfg.Auth.SessionCookieSecure,
	})
	orgHandler := org.NewHandler(orgStore, authStore)
	examHandler := exam.NewHandler(examStore, authStore)
	paperHandler := paper.NewHandler(paperStore, authStore)
	fileHandler := files.NewHandler(fileStore, objectStore, authStore, cfg.Files)
	submissionHandler := submission.NewHandler(submissionStore, fileStore, authStore)
	segmentHandler := segment.NewHandler(segmentStore, paperStore, submissionStore, authStore, fileStore, objectStore)
	var imageQualityStore imagequality.Store = imagequality.NewMemoryStore()
	var workerRuntimeStore workerruntime.Store = workerruntime.NewMemoryStore()
	var orchestratorStore orchestrator.Store = orchestrator.NewMemoryStore()
	var gradingStore grading.Store = grading.NewMemoryStore()
	var subjectiveStore subjective.Store = subjective.NewMemoryStore()
	var evidenceStore evidence.Store = evidence.NewMemoryStore()
	var reviewStore review.Store = review.NewMemoryStore()
	var scoreStore score.Store = score.NewMemoryStore()
	var appealStore appeal.Store = appeal.NewMemoryStore()
	var reportStore report.Store = report.NewMemoryStore()
	var captureStore capture.Store = capture.NewMemoryStore()
	for _, optionalStore := range optionalStores {
		switch store := optionalStore.(type) {
		case imagequality.Store:
			if store != nil {
				imageQualityStore = store
			}
		case workerruntime.Store:
			if store != nil {
				workerRuntimeStore = store
			}
		case orchestrator.Store:
			if store != nil {
				orchestratorStore = store
			}
		case grading.Store:
			if store != nil {
				gradingStore = store
			}
		case subjective.Store:
			if store != nil {
				subjectiveStore = store
			}
		case evidence.Store:
			if store != nil {
				evidenceStore = store
			}
		case review.Store:
			if store != nil {
				reviewStore = store
			}
		case score.Store:
			if store != nil {
				scoreStore = store
			}
		case appeal.Store:
			if store != nil {
				appealStore = store
			}
		case report.Store:
			if store != nil {
				reportStore = store
			}
		case capture.Store:
			if store != nil {
				captureStore = store
			}
		}
	}
	h.WithWorkerRuntimeStore(workerRuntimeStore)
	orchestratorHandler := orchestrator.NewHandler(orchestratorStore, authStore)
	ocrHandler := ocrpkg.NewHandler(ocrStore, ocrQueue, submissionStore, authStore, workerRuntimeStore)
	imageQualityHandler := imagequality.NewHandler(imageQualityStore, submissionStore, fileStore, authStore, workerRuntimeStore).WithCaptureStore(captureStore)
	workerRuntimeHandler := workerruntime.NewHandler(workerRuntimeStore, authStore)
	captureHandler := capture.NewHandler(captureStore, fileStore, examStore, workerRuntimeStore, authStore)
	gradingHandler := grading.NewHandler(gradingStore, grading.NewEngine(), authStore)
	gradingHandler.SetProductionDependencies(workerRuntimeStore, fileStore)
	var subjectiveAdapter subjective.LLMGradingAdapter = subjective.NewMockLLMAdapter()
	if cfg.AIService.URL != "" {
		subjectiveAdapter = subjective.NewHTTPAdapter(subjective.HTTPAdapterConfig{
			BaseURL:       cfg.AIService.URL,
			Token:         cfg.AIService.Token,
			Timeout:       cfg.AIService.Timeout,
			MaxRetries:    cfg.AIService.MaxRetries,
			ModelVersion:  cfg.AIService.ModelVersion,
			PromptVersion: cfg.AIService.PromptVersion,
			MinConfidence: cfg.AIService.MinConfidence,
		})
	}
	subjectiveHandler := subjective.NewHandler(subjectiveStore, subjectiveAdapter, authStore)
	evidenceHandler := evidence.NewHandler(evidenceStore, evidence.NewEngine(), authStore)
	reviewHandler := review.NewHandler(reviewStore, authStore, segmentHandler.GetImage, fileHandler.Download)
	scoreHandler := score.NewHandler(scoreStore, authStore)
	appealHandler := appeal.NewHandler(appealStore, authStore)
	reportHandler := report.NewHandler(reportStore, authStore)
	requireAuth := auth.AuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	requireOrgManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("org:manage")(handler))
	}
	requireTenantManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("tenant:manage")(handler))
	}
	requireStudentImport := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("student:import")(handler))
	}
	requireExamManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("exam:manage")(handler))
	}
	requireFileManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("file:manage")(handler))
	}
	requireSubmissionManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("submission:manage")(handler))
	}
	requireCaptureManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("capture:manage")(handler))
	}
	requireOCRManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("ocr:manage")(handler))
	}
	requireSegmentManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("segment:manage")(handler))
	}
	requireSegmentEvidenceRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin", "page_processing_worker")(
			auth.RequireAnyPermission("segment:manage", "ocr:manage", "grading:manage", "evidence:manage", "review:manage", "arbitration:manage")(handler),
		))
	}
	requireOrchestratorManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("orchestrator:manage")(handler))
	}
	requireGradingManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("grading:manage")(handler))
	}
	requireEvidenceManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("evidence:manage")(handler))
	}
	requireReviewManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("review:manage")(handler))
	}
	requireReviewWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("review:manage", "review:work")(handler))
	}
	requireOriginalReviewImage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin")(
			auth.RequireAnyPermission("review:manage", "evidence:manage", "tenant:manage")(handler),
		))
	}
	requireArbitrationManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("arbitration:manage")(handler))
	}
	requireArbitrationWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("arbitration:manage", "arbitration:work")(handler))
	}
	requireScoreManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("score:manage")(handler))
	}
	requireStudentGradeAccess := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("student:grade:read", "score:manage")(handler))
	}
	requireAppealCreate := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:create")(handler))
	}
	requireAppealRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:read")(handler))
	}
	requireAppealManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:manage")(handler))
	}
	requireAppealWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:work")(handler))
	}
	requireAuditRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("audit:read")(handler))
	}
	requireAuditExport := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("audit:export")(handler))
	}
	requireReportRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("report:read")(handler))
	}
	requireReportExport := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("report:export")(handler))
	}
	requireSystemRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("system:read")(handler))
	}
	requireOCRAvailabilityRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(
			"review:work",
			"review:manage",
			"submission:manage",
			"capture:manage",
			"ocr:manage",
			"grading:manage",
			"system:read",
		)(handler))
	}
	requireWorkerExecute := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("ocr:manage", "orchestrator:manage")(handler))
	}
	requireWorkerRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("ocr:manage", "orchestrator:manage", "system:read")(handler))
	}

	mux.HandleFunc("GET /health", h.Health)
	mux.Handle("GET /ready", requireSystemRead(h.Ready))
	mux.Handle("GET /api/v1/system/info", requireSystemRead(h.SystemInfo))
	mux.Handle("GET /api/v1/system/status", requireSystemRead(h.SystemStatus))
	mux.Handle("GET /api/v1/ocr/availability", requireOCRAvailabilityRead(h.OCRAvailability))
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.Handle("POST /api/v1/auth/logout", requireAuth(http.HandlerFunc(authHandler.Logout)))
	mux.Handle("GET /api/v1/auth/me", requireAuth(http.HandlerFunc(authHandler.Me)))
	mux.Handle("GET /api/v1/users", requireOrgManage(authHandler.ListManagedUsers))
	mux.Handle("POST /api/v1/users", requireOrgManage(authHandler.CreateManagedUser))
	mux.Handle("GET /api/v1/roles", requireOrgManage(authHandler.ListAssignableRoles))
	mux.Handle("GET /api/v1/audit-logs", requireAuditRead(authHandler.ListAudits))
	mux.Handle("POST /api/v1/audit-logs/export", requireAuditExport(authHandler.ExportAudits))

	mux.Handle("POST /api/v1/tenants", requireTenantManage(orgHandler.CreateTenant))
	mux.Handle("GET /api/v1/tenants", requireAuth(http.HandlerFunc(orgHandler.ListTenants)))
	mux.Handle("PATCH /api/v1/tenants/{id}", requireTenantManage(orgHandler.UpdateTenant))
	mux.Handle("POST /api/v1/schools", requireOrgManage(orgHandler.CreateSchool))
	mux.Handle("GET /api/v1/schools", requireOrgManage(orgHandler.ListSchools))
	mux.Handle("POST /api/v1/grades", requireOrgManage(orgHandler.CreateGrade))
	mux.Handle("GET /api/v1/grades", requireOrgManage(orgHandler.ListGrades))
	mux.Handle("POST /api/v1/classes", requireOrgManage(orgHandler.CreateClass))
	mux.Handle("GET /api/v1/classes", requireOrgManage(orgHandler.ListClasses))
	mux.Handle("POST /api/v1/students", requireOrgManage(orgHandler.CreateStudent))
	mux.Handle("GET /api/v1/students", requireOrgManage(orgHandler.ListStudents))
	mux.Handle("PATCH /api/v1/students/{id}", requireOrgManage(orgHandler.UpdateStudent))
	mux.Handle("POST /api/v1/students/import-csv", requireStudentImport(orgHandler.ImportStudentsCSV))
	mux.Handle("POST /api/v1/classes/{id}/teachers", requireOrgManage(orgHandler.BindTeacherClass))

	mux.Handle("POST /api/v1/exams", requireExamManage(examHandler.CreateExam))
	mux.Handle("GET /api/v1/exams", requireExamManage(examHandler.ListExams))
	mux.Handle("GET /api/v1/exams/{id}", requireExamManage(examHandler.GetExam))
	mux.Handle("PATCH /api/v1/exams/{id}", requireExamManage(examHandler.UpdateExam))
	mux.Handle("POST /api/v1/exams/{id}/archive", requireExamManage(examHandler.Archive))
	mux.Handle("POST /api/v1/exams/{id}/status", requireExamManage(examHandler.UpdateStatus))
	mux.Handle("GET /api/v1/exams/{examId}/answer-sheet-templates", requireExamManage(paperHandler.ListTemplates))
	mux.Handle("POST /api/v1/exams/{examId}/answer-sheet-templates", requireExamManage(paperHandler.CreateTemplate))
	mux.Handle("PATCH /api/v1/answer-sheet-templates/{id}", requireExamManage(paperHandler.UpdateTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/lock", requireExamManage(paperHandler.LockTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/clone", requireExamManage(paperHandler.CloneTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/page-barcodes", requireExamManage(captureHandler.IssueTemplateBarcodes))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/student-barcodes", requireExamManage(captureHandler.IssueStudentBarcodes))
	mux.Handle("GET /api/v1/exams/{examId}/readiness", requireExamManage(paperHandler.GetReadiness))
	mux.Handle("POST /api/v1/exams/{examId}/readiness/confirm", requireExamManage(paperHandler.ConfirmReadiness))
	mux.Handle("POST /api/v1/exams/{examId}/start-collection", requireExamManage(paperHandler.StartCollection))

	mux.Handle("POST /api/v1/exams/{examId}/papers", requireExamManage(paperHandler.CreatePaper))
	mux.Handle("GET /api/v1/exams/{examId}/papers", requireExamManage(paperHandler.ListPapers))
	mux.Handle("POST /api/v1/exams/{examId}/questions", requireExamManage(paperHandler.CreateQuestion))
	mux.Handle("GET /api/v1/exams/{examId}/questions", requireExamManage(paperHandler.ListQuestions))
	mux.Handle("PATCH /api/v1/questions/{id}", requireExamManage(paperHandler.UpdateQuestion))
	mux.Handle("DELETE /api/v1/questions/{id}", requireExamManage(paperHandler.DeleteQuestion))
	mux.Handle("POST /api/v1/questions/{id}/rubric", requireExamManage(paperHandler.CreateRubric))
	mux.Handle("POST /api/v1/exams/{examId}/validate-paper-config", requireExamManage(paperHandler.ValidatePaperConfig))

	mux.Handle("POST /api/v1/files", requireFileManage(fileHandler.Upload))
	mux.Handle("GET /api/v1/files/{id}", requireFileManage(fileHandler.Get))
	mux.Handle("GET /api/v1/files/{id}/download", requireFileManage(fileHandler.Download))
	mux.Handle("DELETE /api/v1/files/{id}", requireFileManage(fileHandler.Delete))

	mux.Handle("POST /api/v1/exams/{examId}/submissions", requireSubmissionManage(submissionHandler.Create))
	mux.Handle("GET /api/v1/exams/{examId}/submissions", requireSubmissionManage(submissionHandler.ListByExam))
	mux.Handle("GET /api/v1/submissions/{id}", requireSubmissionManage(submissionHandler.Get))
	mux.Handle("POST /api/v1/submissions/{id}/pages", requireSubmissionManage(submissionHandler.AddPage))
	mux.Handle("PUT /api/v1/submissions/{id}/pages/{pageNo}", requireSubmissionManage(submissionHandler.ReplacePage))
	mux.Handle("GET /api/v1/submissions/{id}/pages", requireSubmissionManage(submissionHandler.ListPages))
	mux.Handle("POST /api/v1/submissions/{id}/quality-check", requireSubmissionManage(submissionHandler.QualityCheck))
	mux.Handle("POST /api/v1/submissions/{id}/run-quality-check", requireSubmissionManage(imageQualityHandler.RunQualityCheck))
	mux.Handle("POST /api/v1/submissions/{id}/status", requireSubmissionManage(submissionHandler.UpdateStatus))
	mux.Handle("POST /api/v1/exams/{examId}/capture-batches", requireCaptureManage(captureHandler.CreateBatch))
	mux.Handle("GET /api/v1/exams/{examId}/capture-batches", requireCaptureManage(captureHandler.ListBatches))
	mux.Handle("GET /api/v1/capture-batches/{id}", requireCaptureManage(captureHandler.GetBatch))
	mux.Handle("GET /api/v1/capture-batches/{id}/matching-queue", requireCaptureManage(captureHandler.GetMatchingQueue))
	mux.Handle("POST /api/v1/capture-batches/{id}/files", requireCaptureManage(captureHandler.RegisterFile))
	mux.Handle("POST /api/v1/capture-batches/{id}/process", requireCaptureManage(captureHandler.ProcessBatch))
	mux.Handle("GET /api/v1/capture-batches/{id}/pages", requireCaptureManage(captureHandler.ListPages))
	mux.Handle("PATCH /api/v1/capture-pages/{id}", requireCaptureManage(captureHandler.UpdatePage))
	mux.Handle("POST /api/v1/capture-pages/{id}/page-match/confirm", requireCaptureManage(captureHandler.ConfirmPageMatch))
	mux.Handle("POST /api/v1/capture-pages/{id}/delete", requireCaptureManage(captureHandler.DeletePage))
	mux.Handle("POST /api/v1/capture-pages/{id}/restore", requireCaptureManage(captureHandler.RestorePage))
	mux.Handle("POST /api/v1/capture-batches/{id}/submissions/split", requireCaptureManage(captureHandler.SplitSubmission))
	mux.Handle("POST /api/v1/capture-batches/{id}/submissions/merge", requireCaptureManage(captureHandler.MergeSubmissions))
	mux.Handle("POST /api/v1/submissions/{id}/student-match/confirm", requireCaptureManage(captureHandler.ConfirmStudentMatch))
	mux.Handle("POST /api/v1/submissions/{id}/student-match/unknown", requireCaptureManage(captureHandler.MarkStudentUnknown))
	mux.Handle("POST /api/v1/capture-batches/{id}/cancel", requireCaptureManage(captureHandler.CancelBatch))
	mux.Handle("POST /api/v1/capture-batches/{id}/reopen", requireCaptureManage(captureHandler.ReopenBatch))
	mux.Handle("POST /api/v1/capture-batches/{id}/complete", requireCaptureManage(captureHandler.CompleteBatch))
	mux.Handle("POST /api/v1/submissions/{id}/process-pages", requireCaptureManage(captureHandler.ProcessSubmissionPages))
	mux.Handle("GET /api/v1/submission-pages/{id}/registration-runs", requireCaptureManage(captureHandler.ListRegistrationRuns))
	mux.Handle("GET /api/v1/submissions/{id}/processing-summary", requireCaptureManage(captureHandler.GetProcessingSummary))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/confirm", requireCaptureManage(captureHandler.ConfirmRegistration))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/retry", requireCaptureManage(captureHandler.RetryRegistration))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/corrections", requireCaptureManage(captureHandler.CreateRegistrationCorrection))
	mux.Handle("GET /api/v1/page-registration-runs/{id}/correction-context", requireCaptureManage(captureHandler.GetRegistrationCorrectionContext))
	mux.Handle("GET /api/v1/page-registration-corrections/{id}", requireCaptureManage(captureHandler.GetRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/preview", requireCaptureManage(captureHandler.PreviewRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/apply", requireCaptureManage(captureHandler.ApplyRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/undo", requireCaptureManage(captureHandler.UndoRegistrationCorrection))

	mux.Handle("POST /api/v1/internal/image-quality/jobs/claim", requireOCRManage(imageQualityHandler.ClaimJobs))
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/normalized-assets", requireOCRManage(imageQualityHandler.CreateNormalizedAssetSlot))
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/result", requireOCRManage(imageQualityHandler.SubmitResult))
	mux.Handle("POST /api/v1/internal/worker/tasks", requireWorkerExecute(workerRuntimeHandler.CreateTask))
	mux.Handle("POST /api/v1/internal/worker/tasks/claim", requireWorkerExecute(workerRuntimeHandler.Claim))
	mux.Handle("GET /api/v1/internal/worker/tasks/{taskId}", requireWorkerRead(workerRuntimeHandler.Get))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/heartbeat", requireWorkerExecute(workerRuntimeHandler.Heartbeat))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/complete", requireWorkerExecute(workerRuntimeHandler.Complete))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/fail", requireWorkerExecute(workerRuntimeHandler.Fail))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/cancel", requireWorkerExecute(workerRuntimeHandler.Cancel))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/requeue", requireWorkerExecute(workerRuntimeHandler.Requeue))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/result", requireWorkerExecute(captureHandler.CompleteFile))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/fail", requireWorkerExecute(captureHandler.FailFile))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/result", requireWorkerExecute(captureHandler.CompleteRegistration))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/fail", requireWorkerExecute(captureHandler.FailRegistration))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/result", requireWorkerExecute(captureHandler.CompleteRegistrationCorrection))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/failure", requireWorkerExecute(captureHandler.FailRegistrationCorrection))
	mux.Handle("GET /api/v1/internal/answer-segments/{id}/image", requireWorkerExecute(segmentHandler.GetImage))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/result", requireWorkerExecute(gradingHandler.CompleteOMR))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/failure", requireWorkerExecute(gradingHandler.FailOMR))
	mux.Handle("GET /api/v1/internal/worker/metrics", requireWorkerRead(workerRuntimeHandler.Metrics))

	mux.Handle("POST /api/v1/submissions/{id}/ocr-tasks", requireOCRManage(ocrHandler.CreateTask))
	mux.Handle("GET /api/v1/submissions/{id}/ocr-tasks", requireOCRManage(ocrHandler.ListBySubmission))
	mux.Handle("GET /api/v1/ocr-tasks/pending", requireOCRManage(ocrHandler.ListPending))
	mux.Handle("GET /api/v1/ocr-tasks/{id}", requireOCRManage(ocrHandler.GetTask))
	mux.Handle("GET /api/v1/ocr-tasks/{id}/input", requireOCRManage(ocrHandler.GetTaskInput))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/start", requireOCRManage(ocrHandler.StartTask))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/results", requireOCRManage(ocrHandler.CompleteTask))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/fail", requireOCRManage(ocrHandler.FailTask))

	mux.Handle("POST /api/v1/submissions/{id}/segment-answers", requireSegmentManage(segmentHandler.Generate))
	mux.Handle("GET /api/v1/submissions/{id}/answer-segments", requireSegmentManage(segmentHandler.ListBySubmission))
	mux.Handle("PATCH /api/v1/answer-segments/{id}", requireSegmentManage(segmentHandler.Update))
	mux.Handle("GET /api/v1/answer-segments/{id}/evidence", requireSegmentEvidenceRead(segmentHandler.GetEvidence))
	mux.Handle("GET /api/v1/answer-segments/{id}/image", requireSegmentEvidenceRead(segmentHandler.GetImage))
	mux.Handle("HEAD /api/v1/answer-segments/{id}/image", requireSegmentEvidenceRead(segmentHandler.GetImage))

	mux.Handle("POST /api/v1/orchestrations", requireOrchestratorManage(orchestratorHandler.CreateRun))
	mux.Handle("GET /api/v1/orchestrations/{id}", requireOrchestratorManage(orchestratorHandler.GetRun))
	mux.Handle("GET /api/v1/orchestrations/{id}/tasks", requireOrchestratorManage(orchestratorHandler.ListTasks))
	mux.Handle("POST /api/v1/orchestrations/{id}/tasks", requireOrchestratorManage(orchestratorHandler.CreateTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/start", requireOrchestratorManage(orchestratorHandler.StartTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/complete", requireOrchestratorManage(orchestratorHandler.CompleteTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/fail", requireOrchestratorManage(orchestratorHandler.FailTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/retry", requireOrchestratorManage(orchestratorHandler.RetryTask))

	mux.Handle("PUT /api/v1/answer-segments/{id}/answer", requireGradingManage(gradingHandler.RecordAnswer))
	mux.Handle("POST /api/v1/answer-segments/{id}/rule-grade", requireGradingManage(gradingHandler.RuleGrade))
	mux.Handle("GET /api/v1/answer-segments/{id}/ai-grades", requireGradingManage(gradingHandler.ListGrades))
	mux.Handle("POST /api/v1/questions/{id}/scoring-rules", requireGradingManage(gradingHandler.CreateScoringRule))
	mux.Handle("GET /api/v1/questions/{id}/scoring-rules", requireGradingManage(gradingHandler.ListScoringRules))
	mux.Handle("PATCH /api/v1/scoring-rules/{id}", requireGradingManage(gradingHandler.UpdateScoringRule))
	mux.Handle("POST /api/v1/scoring-rules/{id}/publish", requireGradingManage(gradingHandler.PublishScoringRule))
	mux.Handle("GET /api/v1/answer-sheet-templates/{id}/omr-calibrations", requireGradingManage(gradingHandler.ListOMRCalibrations))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/omr-calibrations", requireGradingManage(gradingHandler.CreateOMRCalibration))
	mux.Handle("GET /api/v1/omr-calibrations/{id}", requireGradingManage(gradingHandler.GetOMRCalibration))
	mux.Handle("POST /api/v1/omr-calibrations/{id}/cases/{caseId}/label", requireGradingManage(gradingHandler.LabelOMRCalibrationCase))
	mux.Handle("POST /api/v1/omr-calibrations/{id}/approve", requireGradingManage(gradingHandler.ApproveOMRCalibration))
	mux.Handle("POST /api/v1/omr-calibrations/{id}/revoke", requireGradingManage(gradingHandler.RevokeOMRCalibration))
	mux.Handle("POST /api/v1/omr-calibrations/{id}/discard", requireGradingManage(gradingHandler.DiscardOMRCalibration))
	mux.Handle("POST /api/v1/exams/{examId}/scoring-runs", requireGradingManage(gradingHandler.StartScoringRun))
	mux.Handle("GET /api/v1/exams/{examId}/scoring-summary", requireGradingManage(gradingHandler.GetScoringSummary))
	mux.Handle("GET /api/v1/exams/{examId}/automation-results", requireGradingManage(gradingHandler.GetExamAutomationResults))
	mux.Handle("GET /api/v1/scoring-runs/{runId}", requireGradingManage(gradingHandler.GetScoringRun))
	mux.Handle("POST /api/v1/scoring-runs/{runId}/cancel", requireGradingManage(gradingHandler.CancelScoringRun))
	mux.Handle("POST /api/v1/scoring-runs/{runId}/retry-failed", requireGradingManage(gradingHandler.RetryFailedScoringRun))
	mux.Handle("POST /api/v1/answer-segments/{id}/reprocess-score", requireGradingManage(gradingHandler.ReprocessSegmentScore))
	mux.Handle("POST /api/v1/answer-segments/{id}/subjective-ai-grade", requireGradingManage(subjectiveHandler.Grade))
	mux.Handle("POST /api/v1/ai-grades/{id}/verify-evidence", requireEvidenceManage(evidenceHandler.Verify))
	mux.Handle("PUT /api/v1/exams/{examId}/double-mark-policy", requireReviewManage(reviewHandler.SetExamDoubleMarkPolicy))
	mux.Handle("PUT /api/v1/questions/{id}/double-mark-policy", requireReviewManage(reviewHandler.SetQuestionDoubleMarkPolicy))
	mux.Handle("GET /api/v1/double-mark-policies", requireReviewManage(reviewHandler.ListDoubleMarkPolicies))
	mux.Handle("POST /api/v1/double-mark-sessions", requireReviewManage(reviewHandler.CreateDoubleMarkSession))
	mux.Handle("GET /api/v1/double-mark-sessions", requireReviewManage(reviewHandler.ListDoubleMarkSessions))
	mux.Handle("GET /api/v1/double-mark-sessions/{id}", requireReviewManage(reviewHandler.GetDoubleMarkSession))
	mux.Handle("POST /api/v1/arbitration-tasks", requireArbitrationManage(reviewHandler.CreateArbitrationTask))
	mux.Handle("GET /api/v1/arbitration-tasks", requireArbitrationWork(reviewHandler.ListArbitrationTasks))
	mux.Handle("GET /api/v1/arbitration-tasks/{id}", requireArbitrationWork(reviewHandler.GetArbitrationTask))
	mux.Handle("POST /api/v1/arbitration-tasks/{id}/assign", requireArbitrationManage(reviewHandler.AssignArbitrationTask))
	mux.Handle("POST /api/v1/arbitration-tasks/{id}/submit", requireArbitrationWork(reviewHandler.SubmitArbitration))
	mux.Handle("POST /api/v1/exams/{examId}/finalize", requireScoreManage(scoreHandler.FinalizeExam))
	mux.Handle("GET /api/v1/exams/{examId}/grades", requireScoreManage(scoreHandler.ListExamGrades))
	mux.Handle("GET /api/v1/exams/{examId}/grades/quality", requireScoreManage(scoreHandler.CheckQuality))
	mux.Handle("POST /api/v1/exams/{examId}/confirm-grades", requireScoreManage(scoreHandler.ConfirmGrades))
	mux.Handle("POST /api/v1/exams/{examId}/publish", requireScoreManage(scoreHandler.PublishGrades))
	mux.Handle("GET /api/v1/exams/{examId}/grades/export", requireScoreManage(scoreHandler.ExportGrades))
	mux.Handle("GET /api/v1/students/{studentId}/exams/{examId}/grade", requireStudentGradeAccess(scoreHandler.GetStudentGrade))
	mux.Handle("POST /api/v1/appeals", requireAppealCreate(appealHandler.CreateAppeal))
	mux.Handle("GET /api/v1/appeals", requireAppealRead(appealHandler.ListAppeals))
	mux.Handle("GET /api/v1/appeals/statistics", requireAppealManage(appealHandler.Statistics))
	mux.Handle("GET /api/v1/appeals/{id}", requireAppealRead(appealHandler.GetAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/assign", requireAppealManage(appealHandler.AssignAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/recommendation", requireAppealWork(appealHandler.SubmitRecommendation))
	mux.Handle("POST /api/v1/appeals/{id}/review", requireAppealManage(appealHandler.ReviewAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/close", requireAppealManage(appealHandler.CloseAppeal))
	mux.Handle("GET /api/v1/exams/{examId}/reports/overview", requireReportRead(reportHandler.Overview))
	mux.Handle("GET /api/v1/exams/{examId}/reports/classes", requireReportRead(reportHandler.Classes))
	mux.Handle("GET /api/v1/exams/{examId}/reports/questions", requireReportRead(reportHandler.Questions))
	mux.Handle("GET /api/v1/exams/{examId}/reports/grading-quality", requireReportRead(reportHandler.GradingQuality))
	mux.Handle("GET /api/v1/students/{studentId}/reports/{examId}", requireAuth(http.HandlerFunc(reportHandler.StudentReport)))
	mux.Handle("POST /api/v1/exams/{examId}/reports/export", requireReportExport(reportHandler.Export))
	mux.Handle("POST /api/v1/review-tasks", requireReviewManage(reviewHandler.CreateTask))
	mux.Handle("GET /api/v1/review-tasks", requireReviewWork(reviewHandler.ListTasks))
	mux.Handle("POST /api/v1/review-tasks/next", requireReviewWork(reviewHandler.ClaimNextTask))
	mux.Handle("POST /api/v1/review-tasks/batch-assign", requireReviewManage(reviewHandler.BatchAssignTasks))
	mux.Handle("GET /api/v1/review-tasks/{id}", requireReviewWork(reviewHandler.GetTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/workspace", requireReviewWork(reviewHandler.GetWorkspace))
	mux.Handle("GET /api/v1/review-tasks/{id}/segment-image", requireReviewWork(reviewHandler.GetWorkspaceSegmentImage))
	mux.Handle("GET /api/v1/review-tasks/{id}/original-image", requireOriginalReviewImage(reviewHandler.GetWorkspaceOriginalImage))
	mux.Handle("POST /api/v1/review-tasks/{id}/renew", requireReviewWork(reviewHandler.RenewTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/release", requireReviewWork(reviewHandler.ReleaseTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/assign", requireReviewManage(reviewHandler.AssignTask))
	mux.Handle("POST /api/v1/review-tasks/{id}/submit", requireReviewWork(reviewHandler.SubmitGrade))
	mux.Handle("POST /api/v1/review-tasks/{id}/return", requireReviewManage(reviewHandler.ReturnTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/draft", requireReviewWork(reviewHandler.GetDraft))
	mux.Handle("PUT /api/v1/review-tasks/{id}/draft", requireReviewWork(reviewHandler.SaveDraft))
	mux.HandleFunc("/", h.NotFound)

	return middleware.Chain(
		mux,
		middleware.Recover(logg),
		middleware.RequestID(),
		middleware.AccessLog(logg, cfg.Observability.SlowRequestThreshold),
		middleware.SecurityHeaders(),
		middleware.CORS(cfg.Security.CORSAllowedOrigins, cfg.Security.CORSAllowedMethods, cfg.Security.CORSAllowedHeaders),
		middleware.BodyLimit(cfg.Security.MaxRequestBodyBytes, skipGlobalBodyLimit),
	)
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func skipGlobalBodyLimit(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/v1/files"
}
