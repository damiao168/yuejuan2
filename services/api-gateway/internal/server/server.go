package server

import (
	"context"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"edugrade-enterprise/services/api-gateway/internal/deps"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/handlers"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/middleware"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/observability"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/regraderelease"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/studentportal"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

type Server struct {
	handler http.Handler
}

// releaseGatePublisher adapts A20's evidence-producing coordinator to A18's
// narrow publish seam. Evidence stays append-only in A20 and is retrieved
// through its scoped endpoint, not echoed as mutable publish input.
type releaseGatePublisher struct {
	coordinator *releasegate.PublicationCoordinator
}

func (p releaseGatePublisher) PublishPublication(ctx context.Context, tenantID, examID, releaseID, actorID string) (scorerelease.Release, error) {
	release, _, err := p.coordinator.Publish(ctx, tenantID, examID, releaseID, actorID)
	return release, err
}

// regradeBlocker projects only publication-relevant state. A20 does not need
// regrade scores, reviewers, or answer material to decide whether publishing
// must wait for a correction plan.
type regradeBlocker struct{ service *regrade.Service }

func (b regradeBlocker) BlockingRegradeCount(ctx context.Context, tenantID, examID string) (int, error) {
	jobs, err := b.service.List(ctx, tenantID, examID, "")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, job := range jobs {
		// A ready plan is frozen and can safely be materialised as its own
		// successor release. All earlier states are still changing evidence and
		// must block public publication.
		if job.Status != regrade.StatusCancelled && job.Status != regrade.StatusReadyForRelease {
			count++
		}
	}
	return count, nil
}

func New(cfg config.Config, logg *logger.Logger) (*Server, func(), error) {
	infra, err := newInfrastructure(cfg, logg)
	if err != nil {
		return nil, nil, err
	}
	modules, err := NewPostgresApplicationModules(infra)
	if err != nil {
		infra.Close()
		return nil, nil, err
	}
	parseExecutor, err := paper.NewParseTaskExecutor(modules.Exam.PaperImportService, modules.Capture.WorkerRuntimeStore, cfg.AIService.Timeout)
	if err != nil {
		infra.Close()
		return nil, nil, err
	}
	infra.startPaperParseExecutor(parseExecutor)
	router := NewRouterComplete(RouterDependencies{
		Config: cfg, Logger: logg, Checkers: infra.Checkers, Metrics: infra.Metrics,
		Modules: modules,
	})
	return &Server{handler: router}, infra.Close, nil
}

type RouterDependencies struct {
	Config   config.Config
	Logger   *logger.Logger
	Checkers []deps.Checker
	Metrics  *observability.Registry
	Modules  ApplicationModules
}

func NewRouter(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store) http.Handler {
	return NewRouterWithFiles(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
}

func NewRouterWithFiles(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store, fileStore files.Store, objectStore files.ObjectStorage) http.Handler {
	return NewRouterFull(cfg, logg, checkers, authStore, orgStore, examStore, paperStore, fileStore, objectStore, submission.NewMemoryStore())
}

func NewRouterFull(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store, fileStore files.Store, objectStore files.ObjectStorage, submissionStore submission.Store) http.Handler {
	stores := NewMemoryApplicationStores()
	stores.Identity = IdentityStores{Auth: authStore, Org: orgStore}
	stores.Exam.Exam = examStore
	stores.Exam.Paper = paperStore
	stores.Exam.Files = fileStore
	stores.Exam.Submissions = submissionStore
	return NewRouterWithApplicationStores(cfg, logg, checkers, objectStore, stores)
}

func NewRouterWithApplicationStores(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, objectStore files.ObjectStorage, stores ApplicationStores) http.Handler {
	modules := NewApplicationModules(ApplicationDependencies{
		Config: cfg, ObjectStore: objectStore,
	}, stores)
	return NewRouterComplete(RouterDependencies{
		Config: cfg, Logger: logg, Checkers: checkers, Modules: modules,
	})
}

func NewMemoryRouter(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, objectStore files.ObjectStorage, configure func(*ApplicationStores)) http.Handler {
	stores := NewMemoryApplicationStores()
	if configure != nil {
		configure(&stores)
	}
	return NewRouterWithApplicationStores(cfg, logg, checkers, objectStore, stores)
}

func NewRouterComplete(dependencies RouterDependencies) http.Handler {
	cfg := dependencies.Config
	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" {
		cfg.Auth.SessionCookieName = auth.DefaultSessionCookieName
	}
	logg := dependencies.Logger
	modules := dependencies.Modules
	metricsRegistry := dependencies.Metrics
	if metricsRegistry == nil {
		metricsRegistry = observability.NewRegistry()
	}

	mux := http.NewServeMux()
	questionBankImportMux := http.NewServeMux()
	h := handlers.New(cfg, dependencies.Checkers)
	h.WithWorkerRuntimeStore(modules.Capture.WorkerRuntimeStore)

	authStore := modules.Identity.AuthStore
	authHandler := modules.Identity.AuthHandler
	orgHandler := modules.Identity.OrgHandler
	examHandler := modules.Exam.ExamHandler
	paperHandler := modules.Exam.PaperHandler
	fileHandler := modules.Exam.FileHandler
	submissionHandler := modules.Exam.SubmissionHandler
	segmentHandler := modules.Exam.SegmentHandler
	assessmentHandler := modules.Exam.AssessmentHandler
	questionBankHandler := modules.Exam.QuestionBankHandler
	workspaceHandler := modules.Exam.WorkspaceHandler
	dashboardHandler := modules.Exam.DashboardHandler

	workerRuntimeStore := modules.Capture.WorkerRuntimeStore
	orchestratorHandler := modules.Capture.OrchestratorHandler
	ocrHandler := modules.Capture.OCRHandler
	imageQualityHandler := modules.Capture.ImageQualityHandler
	workerRuntimeHandler := modules.Capture.WorkerRuntimeHandler
	captureHandler := modules.Capture.CaptureHandler
	captureUploadHandler := modules.Capture.CaptureUploadHandler
	processingHandler := modules.Capture.ProcessingHandler

	gradingHandler := modules.Grading.GradingHandler
	subjectiveHandler := modules.Grading.SubjectiveHandler
	evidenceHandler := modules.Grading.EvidenceHandler
	reviewHandler := modules.Grading.ReviewHandler
	reviewAnnotationHandler := modules.Grading.ReviewAnnotationHandler
	goldPaperHandler := modules.Grading.GoldPaperHandler
	calibrationHandler := modules.Grading.CalibrationHandler
	answerGroupHandler := modules.Grading.AnswerGroupHandler
	backmarkHandler := modules.Grading.BackmarkHandler
	regradeHandler := modules.Grading.RegradeHandler
	graderDriftHandler := modules.Grading.GraderDriftHandler
	seedQualityHandler := modules.Grading.SeedQualityHandler
	qualityDashboardHandler := modules.Grading.QualityDashboardHandler

	scoreHandler := modules.Release.ScoreHandler
	scoreReleaseHandler := modules.Release.ScoreReleaseHandler
	releaseGateHandler := modules.Release.ReleaseGateHandler
	studentPortalHandler := modules.Release.StudentPortalHandler
	regradeReleaseHandler := modules.Release.RegradeReleaseHandler
	appealHandler := modules.Release.AppealHandler
	publishedQuestionAppealHandler := modules.Release.PublishedQuestionAppealHandler
	reportHandler := modules.Release.ReportHandler

	modelGovernanceHandler := modules.AIGovernance.ModelGovernanceHandler
	mathUnderstandingHandler := modules.AIGovernance.MathUnderstandingHandler
	eligibilityHandler := modules.AIGovernance.Foundation.EligibilityHandler
	gradingEvaluationHandler := modules.AIGovernance.Foundation.GradingEvaluationHandler
	modelCalibrationHandler := modules.AIGovernance.Foundation.ModelCalibrationHandler
	aiDisagreementHandler := modules.AIGovernance.Foundation.DisagreementHandler
	idempotencyStore := modules.Idempotency

	authenticate := auth.AuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	authenticateAccount := auth.AccountAuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	authenticateLockedAccount := auth.ReauthenticationAuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	resourceResolver, _ := authStore.(auth.ResourceBoundaryResolver)
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	idempotent := idempotency.Middleware(idempotencyStore, idempotency.Options{Enforce: environment == "production" || environment == "staging"})
	requireAuth := func(handler http.Handler) http.Handler {
		return authenticate(idempotent(auth.RequireRequestResourceBoundary(resourceResolver)(handler)))
	}
	requireAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(idempotent(handler))
	}
	// One-time enrollment secrets, recovery codes and scoped grants must never
	// be persisted or replayed by generic idempotency receipts, even if a
	// client supplies Idempotency-Key. MFAStore owns atomic consumption.
	requireMFAAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(handler)
	}
	requireLockedAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateLockedAccount(idempotent(handler))
	}
	requireRecentPermission := func(permission string, handler http.HandlerFunc) http.Handler {
		// Check current authorization and authentication freshness before serving
		// a cached command receipt, not only before executing a new command.
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(
			auth.RequirePermission(permission)(auth.RequireRecentAuth(cfg.Auth.RecentAuthTTL)(idempotent(handler)))))
	}
	requireSensitiveMutation := func(permission string, handler http.HandlerFunc) http.Handler {
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(
			auth.RequirePermission(permission)(auth.RequireRecentAuthForMutations(cfg.Auth.RecentAuthTTL)(idempotent(handler)))))
	}
	requireOrgManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("org:manage")(handler))
	}
	requireStudentImport := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("student:import")(handler))
	}
	requireExamManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("exam:manage")(handler))
	}
	requireAssessmentRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("exam:manage", "review:manage", "review:work")(handler))
	}
	requireDashboardRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("dashboard:read")(handler))
	}
	requireQuestionBank := func(permission string, handler http.HandlerFunc) http.Handler {
		return authenticate(auth.RequirePermission("question_bank:" + permission)(idempotency.CommandIdentity(handler)))
	}
	requireQuestionBankAny := func(handler http.HandlerFunc, permissions ...string) http.Handler {
		actions := make([]string, 0, len(permissions))
		for _, permission := range permissions {
			actions = append(actions, "question_bank:"+permission)
		}
		return authenticate(auth.RequireAnyPermission(actions...)(idempotency.CommandIdentity(handler)))
	}
	requireQuestionBankImport := func(command bool, handler http.HandlerFunc) http.Handler {
		var guarded http.Handler = handler
		if command {
			guarded = idempotency.CommandIdentity(guarded)
		}
		guarded = auth.RequireAnyPermission("exam:manage", "review:manage", "review:work")(guarded)
		guarded = auth.RequirePermission("question_bank:edit")(guarded)
		guarded = auth.RequirePermission("question_bank:create")(guarded)
		return authenticate(guarded)
	}
	mux.Handle("GET /api/v1/question-banks", requireQuestionBankAny(questionBankHandler.ListBanks, "read", "manage"))
	mux.Handle("POST /api/v1/question-banks", requireQuestionBank("create", questionBankHandler.CreateBank))
	mux.Handle("GET /api/v1/question-banks/{bankId}", requireQuestionBankAny(questionBankHandler.GetBank, "read", "manage"))
	mux.Handle("PATCH /api/v1/question-banks/{bankId}", requireQuestionBank("manage", questionBankHandler.UpdateBank))
	mux.Handle("GET /api/v1/question-banks/{bankId}/items", requireQuestionBank("read", questionBankHandler.ListItems))
	mux.Handle("POST /api/v1/question-banks/{bankId}/items", requireQuestionBank("create", questionBankHandler.CreateItem))
	mux.Handle("GET /api/v1/question-bank/items/{itemId}", requireQuestionBank("read", questionBankHandler.GetItem))
	mux.Handle("GET /api/v1/question-bank/items/{itemId}/versions", requireQuestionBank("read", questionBankHandler.ListVersions))
	mux.Handle("POST /api/v1/question-bank/items/{itemId}/versions", requireQuestionBank("edit", questionBankHandler.CreateVersion))
	mux.Handle("GET /api/v1/question-bank/versions/{versionId}", requireQuestionBank("read", questionBankHandler.GetVersion))
	mux.Handle("PATCH /api/v1/question-bank/versions/{versionId}", requireQuestionBank("edit", questionBankHandler.UpdateVersion))
	mux.Handle("PUT /api/v1/question-bank/versions/{versionId}/scoring", requireQuestionBank("edit", questionBankHandler.UpdateScoring))
	mux.Handle("GET /api/v1/question-bank/versions/{versionId}/reviews", requireQuestionBank("read", questionBankHandler.ListReviews))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/submit-review", requireQuestionBank("edit", questionBankHandler.Transition("submit-review")))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/approve", requireQuestionBank("review", questionBankHandler.Transition("approve")))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/return-to-draft", requireQuestionBankAny(questionBankHandler.Transition("return-to-draft"), "edit", "review"))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/publish", requireQuestionBank("publish", questionBankHandler.Transition("publish")))
	mux.Handle("POST /api/v1/question-banks/{bankId}/reviewers", requireQuestionBank("manage", questionBankHandler.BindReviewers))
	mux.Handle("GET /api/v1/question-banks/{bankId}/metadata-schema", requireQuestionBankAny(questionBankHandler.GetMetadataSchema, "read", "manage"))
	mux.Handle("PUT /api/v1/question-banks/{bankId}/metadata-schema", requireQuestionBank("manage", questionBankHandler.UpdateMetadataSchema))
	mux.Handle("POST /api/v1/question-banks/{bankId}/metadata-schema/validate", requireQuestionBankAny(questionBankHandler.ValidateMetadata, "read", "manage"))
	mux.Handle("GET /api/v1/question-banks/{bankId}/acl", requireQuestionBank("manage", questionBankHandler.GetACL))
	mux.Handle("PUT /api/v1/question-banks/{bankId}/acl", requireQuestionBank("manage", questionBankHandler.UpdateACL))
	mux.Handle("GET /api/v1/question-bank/items", requireQuestionBank("read", questionBankHandler.SearchItems))
	mux.Handle("POST /api/v1/question-bank/items/{itemId}/retire", requireQuestionBank("retire", questionBankHandler.RetireItem))
	mux.Handle("GET /api/v1/question-bank/rubric-templates", requireQuestionBank("read", questionBankHandler.ListTemplates))
	mux.Handle("POST /api/v1/question-bank/rubric-templates", requireQuestionBank("create", questionBankHandler.CreateTemplate))
	mux.Handle("POST /api/v1/question-bank/imports/preview", requireQuestionBankImport(false, questionBankHandler.PreviewImports))
	mux.Handle("POST /api/v1/question-bank/imports/confirm", requireQuestionBankImport(false, questionBankHandler.ConfirmImportBatch))
	questionBankImportMux.Handle("POST /api/v1/question-bank/items/import-from-question/{questionId}", requireQuestionBankImport(true, questionBankHandler.ImportQuestion))
	requireFileManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("file:manage")(handler))
	}
	requireTaskScopedFileManage := func(pathParam string, handler http.HandlerFunc) http.Handler {
		// The durable task scope must be derived before file authorization. The
		// normal request-boundary middleware sees a service account's declared
		// (empty) browser scope and would reject legitimate worker downloads
		// before the task capability can authorize its payload files.
		guarded := workerruntime.RequireTaskFile(pathParam)(auth.RequirePermission("file:manage")(handler))
		return authenticate(idempotent(workerruntime.TaskScope(workerRuntimeStore)(guarded)))
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
	requireRosterManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin")(
			auth.RequirePermission("score:manage")(handler),
		))
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
	requireQuestionAppealRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("student:grade:read", "appeal:read", "appeal:work", "appeal:manage")(handler))
	}
	requireAuditRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("audit:read")(handler))
	}
	requireReportRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("report:read")(handler))
	}
	requireSystemRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("system:read")(handler))
	}
	requireModelRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("model:read")(handler))
	}
	requirePlatformModelManage := func(handler http.HandlerFunc) http.Handler {
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(auth.RequireAnyRole("platform_admin")(
			// A signed-in platform administrator may manage encrypted provider
			// credentials without typing the same login password again. Current
			// role/permission/resource checks still run on every request, and the
			// model-governance store keeps the existing immutable audit trail.
			auth.RequirePermission("model:provider:manage")(idempotent(handler)),
		)))
	}
	requireModelPolicyManage := func(handler http.HandlerFunc) http.Handler {
		return requireRecentPermission("model:policy:manage", handler)
	}
	requireModelEvaluationManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("model:evaluation:manage")(handler))
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
	withScopedExam := func(handler http.HandlerFunc) http.HandlerFunc {
		return handler
	}
	withWorkerTaskScope := func(handler http.HandlerFunc) http.HandlerFunc {
		return workerruntime.TaskScope(workerRuntimeStore)(handler).ServeHTTP
	}
	withWorkerTaskSource := func(sourceType string, pathParam string, handler http.HandlerFunc) http.HandlerFunc {
		guarded := workerruntime.RequireTaskSource(sourceType, pathParam)(handler)
		return workerruntime.TaskScope(workerRuntimeStore)(guarded).ServeHTTP
	}
	withWorkerTaskPayload := func(pathParam string, payloadKey string, handler http.HandlerFunc) http.HandlerFunc {
		guarded := workerruntime.RequireTaskPayloadValue(pathParam, payloadKey)(handler)
		return workerruntime.TaskScope(workerRuntimeStore)(guarded).ServeHTTP
	}
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("GET /health/live", h.Health)
	mux.HandleFunc("GET /health/ready", h.Ready)
	mux.Handle("GET /metrics", metricsRegistry)
	// /ready remains permission-protected for backward compatibility. New
	// infrastructure probes must use the public, redacted /health/ready route.
	mux.Handle("GET /ready", requireSystemRead(h.Ready))
	mux.Handle("GET /api/v1/system/info", requireSystemRead(h.SystemInfo))
	mux.Handle("GET /api/v1/system/status", requireSystemRead(h.SystemStatus))
	mux.Handle("GET /api/v1/ocr/availability", requireOCRAvailabilityRead(h.OCRAvailability))
	mux.Handle("POST /api/v1/auth/login", http.HandlerFunc(authHandler.Login))
	mux.Handle("POST /api/v1/auth/token", http.HandlerFunc(authHandler.TokenLogin))
	mux.Handle("POST /api/v1/auth/activation/verify", http.HandlerFunc(authHandler.VerifyActivation))
	mux.Handle("POST /api/v1/auth/activation/complete", http.HandlerFunc(authHandler.CompleteActivation))
	mux.Handle("POST /api/v1/auth/recovery/verify", http.HandlerFunc(authHandler.VerifyRecovery))
	mux.Handle("POST /api/v1/auth/recovery/complete", http.HandlerFunc(authHandler.CompleteRecovery))
	mux.Handle("POST /api/v1/auth/logout", requireLockedAccount(authHandler.Logout))
	mux.Handle("POST /api/v1/auth/password", requireAccount(authHandler.ChangePassword))
	mux.Handle("POST /api/v1/auth/lock", requireAccount(authHandler.LockSession))
	mux.Handle("POST /api/v1/auth/reauthenticate", requireLockedAccount(authHandler.Reauthenticate))
	mux.Handle("GET /api/v1/auth/mfa", requireMFAAccount(authHandler.MFAStatus))
	mux.Handle("POST /api/v1/auth/mfa/totp/enroll", requireMFAAccount(authHandler.EnrollTOTP))
	mux.Handle("POST /api/v1/auth/mfa/totp/confirm", requireMFAAccount(authHandler.ConfirmTOTP))
	mux.Handle("POST /api/v1/auth/step-up/start", requireMFAAccount(authHandler.StartMFAChallenge))
	mux.Handle("POST /api/v1/auth/step-up/verify", requireMFAAccount(authHandler.VerifyMFAChallenge))
	mux.Handle("POST /api/v1/auth/mfa/totp/disable", requireMFAAccount(authHandler.DisableTOTP))
	mux.Handle("POST /api/v1/auth/mfa/recovery-codes/rotate", requireMFAAccount(authHandler.RotateMFARecoveryCodes))
	mux.Handle("GET /api/v1/auth/me", requireAccount(authHandler.Me))
	mux.Handle("GET /api/v1/auth/sessions", requireAccount(authHandler.ListSessions))
	mux.Handle("GET /api/v1/auth/security-events", requireAccount(authHandler.ListSecurityEvents))
	mux.Handle("DELETE /api/v1/auth/sessions/{id}", requireAccount(authHandler.RevokeSession))
	mux.Handle("POST /api/v1/auth/logout-all", requireAccount(authHandler.LogoutAll))
	mux.Handle("DELETE /api/v1/users/{id}/sessions", requireRecentPermission("session:revoke", authHandler.AdminRevokeUserSessions))
	dashboard.RegisterRoutes(mux, dashboardHandler, requireDashboardRead)
	mux.Handle("GET /api/v1/ai-grading/status", requireAuth(auth.RequireAnyPermission("grading:manage", "review:work", "review:manage", "model:read", "system:read")(http.HandlerFunc(subjectiveHandler.Availability))))
	mux.Handle("GET /api/v1/users", requireOrgManage(authHandler.ListManagedUsers))
	mux.Handle("POST /api/v1/users", requireRecentPermission("org:manage", authHandler.CreateManagedUser))
	mux.Handle("POST /api/v1/users/{id}/activation", requireRecentPermission("org:manage", authHandler.AdminCreateActivation))
	mux.Handle("PATCH /api/v1/users/{id}/status", requireRecentPermission("org:manage", authHandler.UpdateManagedUserStatus))
	mux.Handle("POST /api/v1/users/{id}/credential-reset", requireRecentPermission("org:manage", authHandler.AdminCreateRecovery))
	mux.Handle("GET /api/v1/roles", requireOrgManage(authHandler.ListAssignableRoles))
	mux.Handle("GET /api/v1/audit-logs", requireAuditRead(authHandler.ListAudits))
	mux.Handle("POST /api/v1/audit-logs/export", requireRecentPermission("audit:export", authHandler.ExportAudits))
	mux.Handle("GET /api/v1/model-providers", requireModelRead(modelGovernanceHandler.ListProviders))
	mux.Handle("POST /api/v1/model-providers", requireRecentPermission("model:provider:manage", modelGovernanceHandler.CreateProvider))
	mux.Handle("PATCH /api/v1/model-providers/{id}/status", requireRecentPermission("model:provider:manage", modelGovernanceHandler.UpdateProviderStatus))
	mux.Handle("GET /api/v1/model-deployments", requireModelRead(modelGovernanceHandler.ListDeployments))
	mux.Handle("POST /api/v1/model-deployments", requireRecentPermission("model:provider:manage", modelGovernanceHandler.CreateDeployment))
	mux.Handle("PATCH /api/v1/model-deployments/{id}/state", requireRecentPermission("model:provider:manage", modelGovernanceHandler.UpdateDeploymentState))
	mux.Handle("GET /api/v1/model-policy", requireModelRead(modelGovernanceHandler.GetPolicy))
	mux.Handle("GET /api/v1/model-prompts/current", requireModelRead(modelGovernanceHandler.GetCurrentPrompt))
	mux.Handle("PUT /api/v1/model-policy", requireRecentPermission("model:policy:manage", modelGovernanceHandler.UpdatePolicy))
	mux.Handle("POST /api/v1/model-secrets/probe", requireRecentPermission("model:provider:manage", modelGovernanceHandler.ProbeSecret))
	mux.Handle("GET /api/v1/platform/model-api-configs", requirePlatformModelManage(modelGovernanceHandler.ListManagedAPIConfigs))
	mux.Handle("POST /api/v1/platform/model-api-configs", requirePlatformModelManage(modelGovernanceHandler.CreateManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/resolve", requirePlatformModelManage(modelGovernanceHandler.ResolveManagedAPIProvider))
	mux.Handle("POST /api/v1/platform/model-api-configs/validate", requirePlatformModelManage(modelGovernanceHandler.ValidateManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/models", requirePlatformModelManage(modelGovernanceHandler.ListAvailableManagedAPIModels))
	mux.Handle("POST /api/v1/platform/model-api-configs/auto", requirePlatformModelManage(modelGovernanceHandler.AutoCreateManagedAPIConfig))
	mux.Handle("PATCH /api/v1/platform/model-api-configs/{id}", requirePlatformModelManage(modelGovernanceHandler.UpdateManagedAPIConfig))
	mux.Handle("DELETE /api/v1/platform/model-api-configs/{id}", requirePlatformModelManage(modelGovernanceHandler.DeleteManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/{id}/probe", requirePlatformModelManage(modelGovernanceHandler.ProbeManagedAPIConfig))
	mux.Handle("GET /api/v1/model-sandbox-approvals", requireModelRead(modelGovernanceHandler.ListSandboxApprovals))
	mux.Handle("POST /api/v1/model-sandbox-approvals", requireRecentPermission("model:provider:manage", modelGovernanceHandler.CreateSandboxApproval))
	mux.Handle("POST /api/v1/model-sandbox-approvals/{id}/revoke", requireRecentPermission("model:provider:manage", modelGovernanceHandler.RevokeSandboxApproval))
	mux.Handle("GET /api/v1/model-evaluation-runs", requireModelRead(modelGovernanceHandler.ListEvaluationRuns))
	mux.Handle("POST /api/v1/model-evaluation-runs", requireModelEvaluationManage(modelGovernanceHandler.CreateEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/candidates", requireModelEvaluationManage(modelGovernanceHandler.AddEvaluationCandidate))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/complete", requireModelEvaluationManage(modelGovernanceHandler.CompleteEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/invalidate", requireModelEvaluationManage(modelGovernanceHandler.InvalidateEvaluationRun))
	mux.Handle("GET /api/v1/model-approvals", requireModelRead(modelGovernanceHandler.ListModelApprovals))
	mux.Handle("POST /api/v1/model-approvals", requireModelEvaluationManage(modelGovernanceHandler.CreateModelApproval))
	mux.Handle("POST /api/v1/model-approvals/{id}/revoke", requireModelEvaluationManage(modelGovernanceHandler.RevokeModelApproval))
	if eligibilityHandler != nil {
		aieligibility.RegisterRoutes(mux, eligibilityHandler, requireModelRead, requireModelPolicyManage)
	}
	gradingevaluation.RegisterRoutes(mux, gradingEvaluationHandler, requireModelEvaluationManage)
	modelcalibration.RegisterRoutes(mux, modelCalibrationHandler, requireModelEvaluationManage)
	aidisagreement.RegisterRoutes(mux, aiDisagreementHandler, requireReviewWork, requireReviewManage)

	mux.Handle("POST /api/v1/tenants", requireRecentPermission("tenant:manage", orgHandler.CreateTenant))
	mux.Handle("GET /api/v1/tenants", requireAuth(http.HandlerFunc(orgHandler.ListTenants)))
	mux.Handle("PATCH /api/v1/tenants/{id}", requireRecentPermission("tenant:manage", orgHandler.UpdateTenant))
	mux.Handle("POST /api/v1/schools", requireOrgManage(orgHandler.CreateSchool))
	mux.Handle("GET /api/v1/schools", requireOrgManage(orgHandler.ListSchools))
	mux.Handle("GET /api/v1/academic-years", requireOrgManage(orgHandler.ListAcademicYears))
	mux.Handle("GET /api/v1/grade-cohorts", requireOrgManage(orgHandler.ListGradeCohorts))
	mux.Handle("POST /api/v1/grades", requireOrgManage(orgHandler.CreateGrade))
	mux.Handle("GET /api/v1/grades", requireOrgManage(orgHandler.ListGrades))
	mux.Handle("POST /api/v1/classes", requireOrgManage(orgHandler.CreateClass))
	mux.Handle("GET /api/v1/classes", requireOrgManage(orgHandler.ListClasses))
	mux.Handle("POST /api/v1/students", requireOrgManage(orgHandler.CreateStudent))
	mux.Handle("GET /api/v1/students", requireOrgManage(orgHandler.ListStudents))
	mux.Handle("PATCH /api/v1/students/{id}", requireOrgManage(orgHandler.UpdateStudent))
	mux.Handle("GET /api/v1/students/{id}/enrollments", requireOrgManage(orgHandler.ListStudentEnrollments))
	mux.Handle("PUT /api/v1/students/{id}/enrollment", requireOrgManage(orgHandler.TransferStudent))
	mux.Handle("POST /api/v1/students/import-csv", requireStudentImport(orgHandler.ImportStudentsCSV))
	mux.Handle("POST /api/v1/classes/{id}/teachers", requireOrgManage(orgHandler.BindTeacherClass))

	mux.Handle("POST /api/v1/exams", requireExamManage(examHandler.CreateExam))
	mux.Handle("GET /api/v1/exam-templates", requireExamManage(examHandler.ListExamTemplates))
	mux.Handle("POST /api/v1/exam-sessions", requireExamManage(examHandler.CreateExamSession))
	mux.Handle("GET /api/v1/exam-sessions/commands/{commandId}", requireExamManage(examHandler.RecoverExamSessionCommand))
	mux.Handle("GET /api/v1/exams", requireExamManage(examHandler.ListExams))
	mux.Handle("GET /api/v1/exams/{id}", requireExamManage(examHandler.GetExam))
	mux.Handle("PATCH /api/v1/exams/{id}", requireExamManage(examHandler.UpdateExam))
	mux.Handle("POST /api/v1/exams/{id}/archive", requireExamManage(examHandler.Archive))
	mux.Handle("POST /api/v1/exams/{id}/status", requireExamManage(examHandler.UpdateStatus))
	mux.Handle("POST /api/v1/exams/{id}/candidates/refresh", requireExamManage(examHandler.RefreshCandidates))
	workspace.RegisterRoutes(mux, workspaceHandler, requireDashboardRead)
	mux.Handle("GET /api/v1/assessment/subject-profiles", requireAssessmentRead(assessmentHandler.ListSubjectProfiles))
	mux.Handle("GET /api/v1/assessment/question-archetypes", requireAssessmentRead(assessmentHandler.ListQuestionArchetypes))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/assessment-profile", requireAssessmentRead(withScopedExam(assessmentHandler.GetQuestionConfig)))
	mux.Handle("PUT /api/v1/exams/{examId}/questions/{questionId}/assessment-profile", requireExamManage(withScopedExam(assessmentHandler.ConfigureQuestion)))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/assessment-snapshot", requireAssessmentRead(withScopedExam(assessmentHandler.GetQuestionSnapshot)))
	mathunderstanding.RegisterRoutes(mux, mathUnderstandingHandler, requireReviewWork, requireReviewManage)
	mathunderstanding.RegisterRuntimeRoutes(mux, mathUnderstandingHandler, func(handler http.HandlerFunc) http.Handler {
		return requireWorkerExecute(withWorkerTaskScope(handler))
	})
	mux.Handle("GET /api/v1/exams/{examId}/answer-sheet-templates", requireExamManage(withScopedExam(paperHandler.ListTemplates)))
	mux.Handle("POST /api/v1/exams/{examId}/answer-sheet-templates", requireExamManage(withScopedExam(paperHandler.CreateTemplate)))
	mux.Handle("PATCH /api/v1/answer-sheet-templates/{id}", requireExamManage(paperHandler.UpdateTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/lock", requireExamManage(paperHandler.LockTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/clone", requireExamManage(paperHandler.CloneTemplate))
	mux.Handle("GET /api/v1/exams/{examId}/answer-sheet-template-binding", requireExamManage(withScopedExam(paperHandler.GetExamTemplateBinding)))
	mux.Handle("PUT /api/v1/exams/{examId}/answer-sheet-template-binding", requireExamManage(withScopedExam(paperHandler.BindExamTemplate)))
	mux.Handle("DELETE /api/v1/exams/{examId}/answer-sheet-template-binding", requireExamManage(withScopedExam(paperHandler.UnbindExamTemplate)))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/page-barcodes", requireExamManage(captureHandler.IssueTemplateBarcodes))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/student-barcodes", requireExamManage(captureHandler.IssueStudentBarcodes))
	mux.Handle("GET /api/v1/answer-sheet-templates/{id}/print-context", requireExamManage(captureHandler.GetStudentPrintContext))
	mux.Handle("GET /api/v1/answer-sheet-print-batches/{id}/package.pdf", requireExamManage(captureHandler.DownloadStudentPrintPackage))
	mux.Handle("POST /api/v1/answer-sheet-print-sheets/{id}/revoke", requireExamManage(captureHandler.RevokeStudentSheet))
	mux.Handle("POST /api/v1/answer-sheet-print-sheets/{id}/reprint", requireExamManage(captureHandler.ReprintStudentSheet))
	mux.Handle("GET /api/v1/exams/{examId}/readiness", requireExamManage(withScopedExam(paperHandler.GetReadiness)))
	mux.Handle("POST /api/v1/exams/{examId}/readiness/confirm", requireExamManage(withScopedExam(paperHandler.ConfirmReadiness)))
	mux.Handle("POST /api/v1/exams/{examId}/start-collection", requireExamManage(withScopedExam(paperHandler.StartCollection)))

	mux.Handle("POST /api/v1/exams/{examId}/papers", requireExamManage(withScopedExam(paperHandler.CreatePaper)))
	mux.Handle("GET /api/v1/exams/{examId}/papers", requireExamManage(withScopedExam(paperHandler.ListPapers)))
	mux.Handle("POST /api/v1/exams/{examId}/paper-imports", requireExamManage(withScopedExam(paperHandler.CreatePaperImport)))
	mux.Handle("GET /api/v1/exams/{examId}/paper-imports", requireExamManage(withScopedExam(paperHandler.ListPaperImports)))
	mux.Handle("GET /api/v1/paper-imports/{id}", requireExamManage(paperHandler.GetPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/sources", requireExamManage(paperHandler.AddPaperImportSources))
	mux.Handle("PUT /api/v1/paper-imports/{id}/sources", requireExamManage(paperHandler.ReplacePaperImportSources))
	mux.Handle("PUT /api/v1/paper-imports/{id}/review", requireExamManage(paperHandler.SavePaperImportReview))
	mux.Handle("POST /api/v1/paper-imports/{id}/apply", requireExamManage(paperHandler.ApplyPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/cancel", requireExamManage(paperHandler.CancelPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/retry-parse", requireExamManage(paperHandler.RetryPaperImportParse))
	mux.Handle("POST /api/v1/exams/{examId}/questions", requireExamManage(withScopedExam(paperHandler.CreateQuestion)))
	mux.Handle("POST /api/v1/exams/{examId}/questions/materialize-from-bank", authenticate(auth.RequirePermission("exam:manage")(auth.RequirePermission("question_bank:read")(idempotency.CommandIdentity(withScopedExam(questionBankHandler.Materialize))))))
	mux.Handle("GET /api/v1/exams/{examId}/questions", requireExamManage(withScopedExam(paperHandler.ListQuestions)))
	mux.Handle("PATCH /api/v1/questions/{id}", requireExamManage(paperHandler.UpdateQuestion))
	mux.Handle("DELETE /api/v1/questions/{id}", requireExamManage(paperHandler.DeleteQuestion))
	mux.Handle("POST /api/v1/questions/{id}/rubric", requireExamManage(paperHandler.CreateRubric))
	mux.Handle("POST /api/v1/exams/{examId}/validate-paper-config", requireExamManage(withScopedExam(paperHandler.ValidatePaperConfig)))

	mux.Handle("POST /api/v1/files", requireFileManage(withWorkerTaskScope(fileHandler.Upload)))
	mux.Handle("GET /api/v1/files/{id}", requireFileManage(fileHandler.Get))
	mux.Handle("GET /api/v1/files/{id}/download", requireTaskScopedFileManage("id", fileHandler.Download))
	mux.Handle("DELETE /api/v1/files/{id}", requireFileManage(fileHandler.Delete))
	mux.Handle("GET /api/v1/system/file-reconciliation", requireAuth(auth.RequireAnyRole("platform_admin")(http.HandlerFunc(fileHandler.ReconciliationStatus))))

	mux.Handle("POST /api/v1/exams/{examId}/submissions", requireSubmissionManage(withScopedExam(submissionHandler.Create)))
	mux.Handle("GET /api/v1/exams/{examId}/submissions", requireSubmissionManage(withScopedExam(submissionHandler.ListByExam)))
	mux.Handle("GET /api/v1/submissions/{id}", requireSubmissionManage(submissionHandler.Get))
	mux.Handle("POST /api/v1/submissions/{id}/pages", requireSubmissionManage(submissionHandler.AddPage))
	mux.Handle("PUT /api/v1/submissions/{id}/pages/{pageNo}", requireSubmissionManage(submissionHandler.ReplacePage))
	mux.Handle("GET /api/v1/submissions/{id}/pages", requireSubmissionManage(submissionHandler.ListPages))
	mux.Handle("POST /api/v1/submissions/{id}/quality-check", requireSubmissionManage(submissionHandler.QualityCheck))
	mux.Handle("POST /api/v1/submissions/{id}/run-quality-check", requireSubmissionManage(imageQualityHandler.RunQualityCheck))
	mux.Handle("GET /api/v1/submission-pages/{id}/quality-runs", requireSubmissionManage(imageQualityHandler.ListPageRuns))
	mux.Handle("POST /api/v1/submission-pages/{id}/quality-override", requireCaptureManage(imageQualityHandler.OverridePageQuality))
	mux.Handle("POST /api/v1/submissions/{id}/status", requireSubmissionManage(submissionHandler.UpdateStatus))
	mux.Handle("POST /api/v1/exams/{examId}/capture-batches", requireCaptureManage(withScopedExam(captureHandler.CreateBatch)))
	mux.Handle("GET /api/v1/exams/{examId}/capture-batches/commands/{commandId}", requireCaptureManage(withScopedExam(captureHandler.RecoverBatchCommand)))
	if captureUploadHandler != nil {
		captureupload.RegisterRoutes(mux, captureUploadHandler, requireCaptureManage)
	}
	processing.RegisterRoutes(mux, processingHandler, func(handler http.HandlerFunc) http.Handler {
		return requireCaptureManage(withScopedExam(handler))
	}, requireCaptureManage, requireCaptureManage)
	mux.Handle("GET /api/v1/exams/{examId}/capture-batches", requireCaptureManage(withScopedExam(captureHandler.ListBatches)))
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
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/normalized-assets", requireOCRManage(withWorkerTaskSource("image_quality_run", "runId", imageQualityHandler.CreateNormalizedAssetSlot)))
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/result", requireOCRManage(withWorkerTaskSource("image_quality_run", "runId", imageQualityHandler.SubmitResult)))
	mux.Handle("POST /api/v1/internal/worker/tasks", requireWorkerExecute(workerRuntimeHandler.CreateTask))
	mux.Handle("POST /api/v1/internal/worker/tasks/claim", requireWorkerExecute(workerRuntimeHandler.Claim))
	mux.Handle("GET /api/v1/internal/worker/tasks/{taskId}", requireWorkerRead(withWorkerTaskScope(workerRuntimeHandler.Get)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/heartbeat", requireWorkerExecute(withWorkerTaskScope(workerRuntimeHandler.Heartbeat)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/complete", requireWorkerExecute(withWorkerTaskScope(workerRuntimeHandler.Complete)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/fail", requireWorkerExecute(withWorkerTaskScope(workerRuntimeHandler.Fail)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/cancel", requireWorkerExecute(withWorkerTaskScope(workerRuntimeHandler.Cancel)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/requeue", requireWorkerExecute(withWorkerTaskScope(workerRuntimeHandler.Requeue)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/decode-result", requireWorkerExecute(withWorkerTaskSource("paper_import_job", "id", paperHandler.CompletePaperImportDecode)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/ocr-result", requireWorkerExecute(withWorkerTaskSource("paper_import_job", "id", paperHandler.CompletePaperImportOCR)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/formula-result", requireWorkerExecute(withWorkerTaskSource("paper_import_job", "id", paperHandler.CompletePaperImportFormula)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/failure", requireWorkerExecute(withWorkerTaskSource("paper_import_job", "id", paperHandler.FailPaperImportRuntime)))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/result", requireWorkerExecute(withWorkerTaskSource("capture_file", "fileId", captureHandler.CompleteFile)))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/fail", requireWorkerExecute(withWorkerTaskSource("capture_file", "fileId", captureHandler.FailFile)))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/result", requireWorkerExecute(withWorkerTaskSource("page_registration_run", "runId", captureHandler.CompleteRegistration)))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/fail", requireWorkerExecute(withWorkerTaskSource("page_registration_run", "runId", captureHandler.FailRegistration)))
	mux.Handle("POST /api/v1/internal/page-template-match-runs/{runId}/result", requireWorkerExecute(withWorkerTaskSource("page_template_match_run", "runId", captureHandler.CompleteTemplateMatch)))
	mux.Handle("POST /api/v1/internal/page-template-match-runs/{runId}/fail", requireWorkerExecute(withWorkerTaskSource("page_template_match_run", "runId", captureHandler.FailTemplateMatch)))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/result", requireWorkerExecute(withWorkerTaskSource("page_registration_correction", "id", captureHandler.CompleteRegistrationCorrection)))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/failure", requireWorkerExecute(withWorkerTaskSource("page_registration_correction", "id", captureHandler.FailRegistrationCorrection)))
	mux.Handle("GET /api/v1/internal/answer-segments/{id}/image", requireWorkerExecute(withWorkerTaskPayload("id", "answer_segment_id", segmentHandler.GetImage)))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/result", requireWorkerExecute(withWorkerTaskSource("omr_run", "runId", gradingHandler.CompleteOMR)))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/failure", requireWorkerExecute(withWorkerTaskSource("omr_run", "runId", gradingHandler.FailOMR)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/result", requireWorkerExecute(withWorkerTaskSource("subjective_grading_run", "runId", subjectiveHandler.CompleteWorker)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/failure", requireWorkerExecute(withWorkerTaskSource("subjective_grading_run", "runId", subjectiveHandler.FailWorker)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/execute", requireWorkerExecute(withWorkerTaskSource("subjective_grading_run", "runId", subjectiveHandler.ExecuteWorker)))
	mux.Handle("GET /api/v1/internal/worker/metrics", requireWorkerRead(workerRuntimeHandler.Metrics))

	mux.Handle("POST /api/v1/submissions/{id}/ocr-tasks", requireOCRManage(ocrHandler.CreateTask))
	mux.Handle("GET /api/v1/submissions/{id}/ocr-tasks", requireOCRManage(ocrHandler.ListBySubmission))
	mux.Handle("GET /api/v1/ocr-tasks/pending", requireOCRManage(ocrHandler.ListPending))
	mux.Handle("GET /api/v1/ocr-tasks/{id}", requireOCRManage(ocrHandler.GetTask))
	mux.Handle("GET /api/v1/ocr-tasks/{id}/input", requireOCRManage(withWorkerTaskSource("ocr_task", "id", ocrHandler.GetTaskInput)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/start", requireOCRManage(withWorkerTaskSource("ocr_task", "id", ocrHandler.StartTask)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/results", requireOCRManage(withWorkerTaskSource("ocr_task", "id", ocrHandler.CompleteTask)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/fail", requireOCRManage(withWorkerTaskSource("ocr_task", "id", ocrHandler.FailTask)))

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
	mux.Handle("GET /api/v1/exams/{examId}/scoring-readiness", requireGradingManage(withScopedExam(gradingHandler.GetScoringReadiness)))
	mux.Handle("POST /api/v1/exams/{examId}/scoring-runs", requireGradingManage(withScopedExam(gradingHandler.StartScoringRun)))
	mux.Handle("GET /api/v1/exams/{examId}/scoring-runs/commands/{commandId}", requireGradingManage(withScopedExam(gradingHandler.RecoverScoringCommand)))
	mux.Handle("GET /api/v1/exams/{examId}/scoring-summary", requireGradingManage(withScopedExam(gradingHandler.GetScoringSummary)))
	mux.Handle("GET /api/v1/exams/{examId}/automation-results", requireGradingManage(withScopedExam(gradingHandler.GetExamAutomationResults)))
	mux.Handle("GET /api/v1/scoring-runs/{runId}", requireGradingManage(gradingHandler.GetScoringRun))
	mux.Handle("POST /api/v1/scoring-runs/{runId}/cancel", requireGradingManage(gradingHandler.CancelScoringRun))
	mux.Handle("POST /api/v1/scoring-runs/{runId}/retry-failed", requireGradingManage(gradingHandler.RetryFailedScoringRun))
	mux.Handle("POST /api/v1/answer-segments/{id}/reprocess-score", requireGradingManage(gradingHandler.ReprocessSegmentScore))
	mux.Handle("POST /api/v1/answer-segments/{id}/subjective-ai-grade", requireGradingManage(subjectiveHandler.Grade))
	mux.Handle("POST /api/v1/subjective-grading-batches", requireGradingManage(subjectiveHandler.CreateBatch))
	mux.Handle("GET /api/v1/subjective-grading-batch-commands/{commandId}", requireGradingManage(subjectiveHandler.RecoverBatchCommand))
	mux.Handle("GET /api/v1/subjective-grading-batches/{batchId}", requireGradingManage(subjectiveHandler.GetBatch))
	mux.Handle("GET /api/v1/subjective-grading-batches/{batchId}/enqueue-command", requireGradingManage(subjectiveHandler.RecoverEnqueueCommand))
	mux.Handle("POST /api/v1/subjective-grading-batches/{batchId}/enqueue", requireGradingManage(subjectiveHandler.EnqueueBatch))
	mux.Handle("POST /api/v1/ai-grades/{id}/verify-evidence", requireEvidenceManage(evidenceHandler.Verify))
	mux.Handle("PUT /api/v1/exams/{examId}/double-mark-policy", requireReviewManage(withScopedExam(reviewHandler.SetExamDoubleMarkPolicy)))
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
	mux.Handle("POST /api/v1/exams/{examId}/finalize", requireRecentPermission("score:manage", withScopedExam(scoreHandler.FinalizeExam)))
	mux.Handle("GET /api/v1/exams/{examId}/grades", requireScoreManage(withScopedExam(scoreHandler.ListExamGrades)))
	mux.Handle("GET /api/v1/exams/{examId}/grades/quality", requireScoreManage(withScopedExam(scoreHandler.CheckQuality)))
	mux.Handle("GET /api/v1/exams/{examId}/roster", requireRosterManage(withScopedExam(scoreHandler.ListRoster)))
	mux.Handle("PUT /api/v1/exams/{examId}/roster/{studentId}/attendance", requireRosterManage(withScopedExam(scoreHandler.SetAttendance)))
	mux.Handle("POST /api/v1/exams/{examId}/confirm-grades", requireRecentPermission("score:manage", withScopedExam(scoreHandler.ConfirmGrades)))
	mux.Handle("POST /api/v1/exams/{examId}/publish", requireRecentPermission("score:manage", withScopedExam(scoreHandler.PublishGrades)))
	scoreReleaseExamManage := func(handler http.HandlerFunc) http.Handler {
		return requireSensitiveMutation("score:manage", withScopedExam(handler))
	}
	scoreReleaseManage := func(handler http.HandlerFunc) http.Handler {
		return requireSensitiveMutation("score:manage", handler)
	}
	scorerelease.RegisterRoutes(mux, scoreReleaseHandler, scoreReleaseExamManage, scoreReleaseManage, requireStudentGradeAccess)
	releasegate.RegisterRoutes(mux, releaseGateHandler, scoreReleaseExamManage)
	studentportal.RegisterRoutes(mux, studentPortalHandler, requireStudentGradeAccess)
	mux.Handle("GET /api/v1/exams/{examId}/grades/export", requireRecentPermission("score:manage", withScopedExam(scoreHandler.ExportGrades)))
	mux.Handle("GET /api/v1/students/{studentId}/exams/{examId}/grade", requireStudentGradeAccess(withScopedExam(scoreHandler.GetStudentGrade)))
	mux.Handle("POST /api/v1/appeals", requireAppealCreate(appealHandler.CreateAppeal))
	mux.Handle("GET /api/v1/appeals", requireAppealRead(appealHandler.ListAppeals))
	mux.Handle("GET /api/v1/appeals/statistics", requireAppealManage(appealHandler.Statistics))
	mux.Handle("GET /api/v1/appeals/{id}", requireAppealRead(appealHandler.GetAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/assign", requireAppealManage(appealHandler.AssignAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/recommendation", requireAppealWork(appealHandler.SubmitRecommendation))
	mux.Handle("POST /api/v1/appeals/{id}/review", requireAppealManage(appealHandler.ReviewAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/close", requireAppealManage(appealHandler.CloseAppeal))
	mux.Handle("POST /api/v1/student/exams/{examId}/question-appeals", requireStudentGradeAccess(publishedQuestionAppealHandler.Create))
	mux.Handle("GET /api/v1/student/question-appeals", requireStudentGradeAccess(publishedQuestionAppealHandler.List))
	mux.Handle("GET /api/v1/question-appeals", requireQuestionAppealRead(publishedQuestionAppealHandler.List))
	mux.Handle("GET /api/v1/question-appeals/{id}", requireQuestionAppealRead(publishedQuestionAppealHandler.Get))
	mux.Handle("GET /api/v1/question-appeals/{id}/context", requireQuestionAppealRead(publishedQuestionAppealHandler.Context))
	mux.Handle("GET /api/v1/question-appeals/{id}/answer-image", requireQuestionAppealRead(publishedQuestionAppealHandler.AnswerImage))
	mux.Handle("POST /api/v1/question-appeals/{id}/start-review", requireAppealManage(publishedQuestionAppealHandler.StartReview))
	mux.Handle("POST /api/v1/question-appeals/{id}/decide", requireQuestionAppealRead(publishedQuestionAppealHandler.Decide))
	mux.Handle("POST /api/v1/question-appeals/{id}/resolve", requireQuestionAppealRead(publishedQuestionAppealHandler.Resolve))
	mux.Handle("GET /api/v1/question-appeals/{id}/events", requireQuestionAppealRead(publishedQuestionAppealHandler.Events))
	mux.Handle("GET /api/v1/exams/{examId}/reports/overview", requireReportRead(withScopedExam(reportHandler.Overview)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/classes", requireReportRead(withScopedExam(reportHandler.Classes)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/questions", requireReportRead(withScopedExam(reportHandler.Questions)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/grading-quality", requireReportRead(withScopedExam(reportHandler.GradingQuality)))
	mux.Handle("GET /api/v1/students/{studentId}/reports/{examId}", requireAuth(http.HandlerFunc(reportHandler.StudentReport)))
	mux.Handle("POST /api/v1/exams/{examId}/reports/export", requireRecentPermission("report:export", withScopedExam(reportHandler.Export)))
	// Gold sets, answer-group reference cases, Seed observations and drift
	// evidence are quality-management facts.  A grader receives only the
	// current calibration/Seed task through the ordinary review flow; exposing
	// these lists to review:work would reveal reference scores or make dark
	// samples identifiable.
	goldpaper.RegisterRoutes(mux, goldPaperHandler, requireReviewManage, requireReviewManage)
	calibration.RegisterRoutes(mux, calibrationHandler, requireReviewWork, requireReviewManage, requireReviewWork)
	answergroup.RegisterRoutes(mux, answerGroupHandler, requireReviewManage, requireReviewManage)
	seedquality.RegisterRoutes(mux, seedQualityHandler, requireReviewManage, requireReviewManage)
	graderdrift.RegisterRoutes(mux, graderDriftHandler, requireReviewManage, requireReviewManage)
	backmark.RegisterRoutes(mux, backmarkHandler, requireReviewManage, requireReviewWork)
	regrade.RegisterRoutes(mux, regradeHandler, requireReviewManage, requireReviewWork)
	regraderelease.RegisterRoutes(mux, regradeReleaseHandler, scoreReleaseManage)
	if qualityDashboardHandler != nil {
		qualitydashboard.RegisterRoutes(mux, qualityDashboardHandler, requireReviewManage)
	}
	mux.Handle("POST /api/v1/review-tasks", requireReviewManage(reviewHandler.CreateTask))
	mux.Handle("GET /api/v1/review-tasks", requireReviewWork(reviewHandler.ListTasks))
	mux.Handle("POST /api/v1/review-tasks/next", requireReviewWork(reviewHandler.ClaimNextTask))
	mux.Handle("POST /api/v1/review-tasks/batch-assign", requireReviewManage(reviewHandler.BatchAssignTasks))
	mux.Handle("GET /api/v1/review-tasks/{id}", requireReviewWork(reviewHandler.GetTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/context", requireReviewWork(reviewHandler.GetTaskContext))
	mux.Handle("GET /api/v1/review-tasks/{id}/workspace", requireReviewWork(reviewHandler.GetWorkspace))
	mux.Handle("GET /api/v1/review-tasks/{id}/segment-image", requireReviewWork(reviewHandler.GetWorkspaceSegmentImage))
	mux.Handle("GET /api/v1/review-tasks/{id}/original-image", requireOriginalReviewImage(reviewHandler.GetWorkspaceOriginalImage))
	mux.Handle("POST /api/v1/review-tasks/{id}/renew", requireReviewWork(reviewHandler.RenewTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/release", requireReviewWork(reviewHandler.ReleaseTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/assign", requireReviewManage(reviewHandler.AssignTask))
	mux.Handle("GET /api/v1/review-commands/{commandId}", requireAuth(auth.RequireAnyPermission("review:manage", "review:work", "arbitration:manage", "arbitration:work")(http.HandlerFunc(reviewHandler.RecoverCommand))))
	mux.Handle("GET /api/v1/score-commands/{commandId}", requireScoreManage(scoreHandler.RecoverCommand))
	mux.Handle("GET /api/v1/report-commands/{commandId}", requireRecentPermission("report:export", reportHandler.RecoverCommand))
	mux.Handle("POST /api/v1/review-tasks/{id}/submit", requireReviewWork(reviewHandler.SubmitGrade))
	mux.Handle("POST /api/v1/review-tasks/{id}/return", requireReviewWork(reviewHandler.ReturnTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/draft", requireReviewWork(reviewHandler.GetDraft))
	mux.Handle("PUT /api/v1/review-tasks/{id}/draft", requireReviewWork(reviewHandler.SaveDraft))
	mux.Handle("GET /api/v1/review-tasks/{id}/annotations", requireReviewWork(reviewAnnotationHandler.ListAnnotations))
	mux.Handle("POST /api/v1/review-tasks/{id}/annotations", requireReviewWork(reviewAnnotationHandler.CreateAnnotation))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/annotations", requireStudentGradeAccess(reviewAnnotationHandler.ListStudentQuestionAnnotations))
	mux.Handle("GET /api/v1/review/annotations/{annotationId}", requireReviewWork(reviewAnnotationHandler.GetAnnotation))
	mux.Handle("PUT /api/v1/review/annotations/{annotationId}", requireReviewWork(reviewAnnotationHandler.UpdateAnnotation))
	mux.Handle("DELETE /api/v1/review/annotations/{annotationId}", requireReviewWork(reviewAnnotationHandler.DeleteAnnotation))
	mux.Handle("GET /api/v1/review/comment-templates", requireReviewWork(reviewAnnotationHandler.ListCommentTemplates))
	mux.Handle("POST /api/v1/review/comment-templates", requireReviewWork(reviewAnnotationHandler.CreateCommentTemplate))
	mux.Handle("GET /api/v1/review/comment-templates/{templateId}", requireReviewWork(reviewAnnotationHandler.GetCommentTemplate))
	mux.Handle("PUT /api/v1/review/comment-templates/{templateId}", requireReviewWork(reviewAnnotationHandler.UpdateCommentTemplate))
	mux.Handle("DELETE /api/v1/review/comment-templates/{templateId}", requireReviewWork(reviewAnnotationHandler.DeleteCommentTemplate))
	mux.Handle("POST /api/v1/review/comment-templates/{shortcut}/use", requireReviewWork(reviewAnnotationHandler.UseCommentTemplate))
	mux.HandleFunc("/", h.NotFound)

	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/question-bank/items/import-from-question/") {
			questionBankImportMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return middleware.Chain(
		rootHandler,
		middleware.Recover(logg),
		middleware.RequestID(),
		middleware.AccessLog(logg, cfg.Observability.SlowRequestThreshold),
		metricsRegistry.Middleware(),
		middleware.SecurityHeaders(),
		middleware.CORS(cfg.Security.CORSAllowedOrigins, cfg.Security.CORSAllowedMethods, cfg.Security.CORSAllowedHeaders),
		middleware.BrowserCSRF(cfg.Auth.SessionCookieName),
		middleware.BodyLimit(cfg.Security.MaxRequestBodyBytes, skipGlobalBodyLimit),
	)
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func useRealAIService(cfg config.Config) bool {
	if strings.TrimSpace(cfg.AIService.URL) == "" {
		return false
	}
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	if environment == "production" || environment == "demo" || environment == "staging" {
		return cfg.AIService.Enabled
	}
	return true
}

func allowMockAI(cfg config.Config) bool {
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	switch environment {
	case "", "development", "dev", "test", "local":
		return strings.TrimSpace(cfg.AIService.URL) == ""
	case "demo":
		return cfg.AIService.Enabled && cfg.AIService.AllowMock && strings.TrimSpace(cfg.AIService.URL) == ""
	default:
		return false
	}
}

func localModelBaseline(cfg config.Config) modelgovernance.LocalBaseline {
	return modelgovernance.LocalBaseline{
		ProviderKey:       cfg.AIService.ProviderKey,
		ProviderName:      "Local grading runtime",
		DeploymentKey:     cfg.AIService.DeploymentKey,
		ModelName:         cfg.AIService.ModelVersion,
		ModelVersion:      cfg.AIService.ModelVersion,
		AdapterType:       cfg.AIService.AdapterType,
		Region:            cfg.AIService.DeploymentRegion,
		CapabilityProfile: cfg.AIService.CapabilityProfile,
	}
}

func skipGlobalBodyLimit(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/v1/files"
}
