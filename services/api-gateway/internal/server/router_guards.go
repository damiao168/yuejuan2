package server

import (
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const (
	permissionExamManage         = "exam:manage"
	permissionReviewManage       = "review:manage"
	permissionReviewWork         = "review:work"
	permissionOCRManage          = "ocr:manage"
	permissionGradingManage      = "grading:manage"
	permissionArbitrationManage  = "arbitration:manage"
	permissionEvidenceManage     = "evidence:manage"
	permissionOrchestratorManage = "orchestrator:manage"
	permissionScoreManage        = "score:manage"
	permissionSystemRead         = "system:read"
)

type routerGuards struct {
	authenticate                    func(http.Handler) http.Handler
	requireAuth                     func(http.Handler) http.Handler
	requireAccount                  func(http.HandlerFunc) http.Handler
	requireMFAAccount               func(http.HandlerFunc) http.Handler
	requireLockedAccount            func(http.HandlerFunc) http.Handler
	requirePermission               func(string, http.HandlerFunc) http.Handler
	requireRecentPermission         func(string, http.HandlerFunc) http.Handler
	requireOrgManage                func(http.HandlerFunc) http.Handler
	requireStudentImport            func(http.HandlerFunc) http.Handler
	requireExamManage               func(http.HandlerFunc) http.Handler
	requireAssessmentRead           func(http.HandlerFunc) http.Handler
	requireDashboardRead            func(http.HandlerFunc) http.Handler
	requireQuestionBank             func(string, http.HandlerFunc) http.Handler
	requireQuestionBankAny          func(http.HandlerFunc, ...string) http.Handler
	requireQuestionBankImport       func(bool, http.HandlerFunc) http.Handler
	requireFileManage               func(http.HandlerFunc) http.Handler
	requireTaskScopedFileManage     func(string, http.HandlerFunc) http.Handler
	requireSubmissionManage         func(http.HandlerFunc) http.Handler
	requireCaptureManage            func(http.HandlerFunc) http.Handler
	requireOCRManage                func(http.HandlerFunc) http.Handler
	requireSegmentManage            func(http.HandlerFunc) http.Handler
	requireSegmentEvidenceRead      func(http.HandlerFunc) http.Handler
	requireOrchestratorManage       func(http.HandlerFunc) http.Handler
	requireGradingManage            func(http.HandlerFunc) http.Handler
	requireEvidenceManage           func(http.HandlerFunc) http.Handler
	requireReviewManage             func(http.HandlerFunc) http.Handler
	requireReviewWork               func(http.HandlerFunc) http.Handler
	requirePersonalReviewRead       func(http.HandlerFunc) http.Handler
	requirePersonalReviewWrite      func(http.HandlerFunc) http.Handler
	requirePersonalReviewCleanup    func(http.HandlerFunc) http.Handler
	requireOriginalReviewImage      func(http.HandlerFunc) http.Handler
	requireArbitrationManage        func(http.HandlerFunc) http.Handler
	requireArbitrationWork          func(http.HandlerFunc) http.Handler
	requireScoreManage              func(http.HandlerFunc) http.Handler
	requireRosterManage             func(http.HandlerFunc) http.Handler
	requireStudentGradeAccess       func(http.HandlerFunc) http.Handler
	requireAppealCreate             func(http.HandlerFunc) http.Handler
	requireAppealRead               func(http.HandlerFunc) http.Handler
	requireAppealManage             func(http.HandlerFunc) http.Handler
	requireAppealWork               func(http.HandlerFunc) http.Handler
	requireQuestionAppealRead       func(http.HandlerFunc) http.Handler
	requireAuditRead                func(http.HandlerFunc) http.Handler
	requireReportRead               func(http.HandlerFunc) http.Handler
	requireSystemRead               func(http.HandlerFunc) http.Handler
	requireOnboardingRead           func(http.HandlerFunc) http.Handler
	requireSchoolAdmin              func(http.HandlerFunc) http.Handler
	requireModelRead                func(http.HandlerFunc) http.Handler
	requirePlatformModelRead        func(http.HandlerFunc) http.Handler
	requirePlatformManagedAPIManage func(http.HandlerFunc) http.Handler
	requirePlatformPanelManage      func(http.HandlerFunc) http.Handler
	requirePlatformSchoolRead       func(http.HandlerFunc) http.Handler
	requireModelProviderManage      func(http.HandlerFunc) http.Handler
	requireModelPolicyManage        func(http.HandlerFunc) http.Handler
	requireModelEvaluationManage    func(http.HandlerFunc) http.Handler
	requireOCRAvailabilityRead      func(http.HandlerFunc) http.Handler
	requireWorkerExecute            func(http.HandlerFunc) http.Handler
	requireWorkerCompletion         func(http.HandlerFunc) http.Handler
	requireWorkerRead               func(http.HandlerFunc) http.Handler
	withScopedExam                  func(http.HandlerFunc) http.HandlerFunc
	withWorkerTaskScope             func(http.HandlerFunc) http.HandlerFunc
	withWorkerTaskCompletionScope   func(http.HandlerFunc) http.HandlerFunc
	withWorkerTaskSource            func(string, string, http.HandlerFunc) http.HandlerFunc
	withWorkerTaskCompletionSource  func(string, string, http.HandlerFunc) http.HandlerFunc
	withWorkerTaskPayload           func(string, string, http.HandlerFunc) http.HandlerFunc
}

// guard 的组合顺序本身就是权限边界；身份、资源作用域、幂等、租约和业务权限不能随意调换。
func buildRouterGuards(cfg config.Config, modules ApplicationModules) routerGuards {
	authStore := modules.Identity.AuthStore
	idempotencyStore := modules.Idempotency
	workerRuntimeStore := modules.Capture.WorkerRuntimeStore
	authenticate := auth.AuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	authenticateAccount := auth.AccountAuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	authenticateLockedAccount := auth.ReauthenticationAuthMiddleware(authStore, auth.HandlerOptions{CookieName: cfg.Auth.SessionCookieName})
	resourceResolver, _ := authStore.(auth.ResourceBoundaryResolver)
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	idempotent := idempotency.Middleware(idempotencyStore, idempotency.Options{Enforce: environment == "production" || environment == "staging"})
	requireAuth := func(handler http.Handler) http.Handler {
		// 授权必须先于收据重放；具体 guard 在权限检查之后包装命令幂等层。
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(handler))
	}
	requireAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(idempotent(handler))
	}
	// 只剩历史任务的教师可能没有活动数据作用域；书签 provider 逐次核验本人源材料，
	// 不能扩大普通任务授权，也不能由通用响应缓存重放个人说明或材料。
	requirePersonalReviewRead := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(auth.RequirePermission(permissionReviewWork)(handler))
	}
	requirePersonalReviewWrite := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(auth.RequirePermission(permissionReviewWork)(idempotency.CommandIdentity(handler)))
	}
	requirePersonalReviewCleanup := func(handler http.HandlerFunc) http.Handler {
		// 撤销阅卷角色后仍可清理本人元数据；GET/HEAD 不要求写命令键。
		return authenticateAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				handler.ServeHTTP(w, r)
				return
			}
			idempotency.CommandIdentity(handler).ServeHTTP(w, r)
		}))
	}
	// 一次性注册秘密、恢复码和作用域授权绝不能被通用幂等收据持久化或重放，即使客户端提供 Idempotency-Key。
	// MFAStore 负责原子消费。
	requireMFAAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateAccount(handler)
	}
	requireLockedAccount := func(handler http.HandlerFunc) http.Handler {
		return authenticateLockedAccount(idempotent(handler))
	}
	requirePermission := func(permission string, handler http.HandlerFunc) http.Handler {
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(
			auth.RequirePermission(permission)(idempotent(handler))))
	}
	requireRecentPermission := func(permission string, handler http.HandlerFunc) http.Handler {
		// 返回缓存命令收据之前也需检查当前授权和认证时效，不能只在执行新命令前检查。
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(
			auth.RequirePermission(permission)(auth.RequireRecentAuth(cfg.Auth.RecentAuthTTL)(idempotent(handler)))))
	}
	requireOrgManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("org:manage")(idempotent(handler)))
	}
	requireStudentImport := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("student:import")(idempotent(handler)))
	}
	requireExamManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionExamManage)(idempotent(handler)))
	}
	requireAssessmentRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(permissionExamManage, permissionReviewManage, permissionReviewWork)(idempotent(handler)))
	}
	requireDashboardRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("dashboard:read")(idempotent(handler)))
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
		guarded = auth.RequireAnyPermission(permissionExamManage, permissionReviewManage, permissionReviewWork)(guarded)
		guarded = auth.RequirePermission("question_bank:edit")(guarded)
		guarded = auth.RequirePermission("question_bank:create")(guarded)
		return authenticate(guarded)
	}
	requireFileManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("file:manage")(idempotent(handler)))
	}
	requireTaskScopedFileManage := func(pathParam string, handler http.HandlerFunc) http.Handler {
		// 文件授权前必须先取得持久化任务作用域。
		// 普通请求边界中间件看到服务账户声明的空浏览器作用域，
		// 会在任务能力授权其载荷文件之前拒绝合法 worker 下载。
		guarded := workerruntime.RequireTaskFile(pathParam)(auth.RequirePermission("file:manage")(idempotent(handler)))
		return authenticate(workerruntime.TaskScope(workerRuntimeStore)(guarded))
	}
	requireSubmissionManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("submission:manage")(idempotent(handler)))
	}
	requireCaptureManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("capture:manage")(idempotent(handler)))
	}
	requireOCRManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionOCRManage)(idempotent(handler)))
	}
	requireSegmentManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("segment:manage")(idempotent(handler)))
	}
	requireSegmentEvidenceRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin", "page_processing_worker")(
			auth.RequireAnyPermission("segment:manage", permissionOCRManage, permissionGradingManage, permissionEvidenceManage, permissionReviewManage, permissionArbitrationManage)(idempotent(handler)),
		))
	}
	requireOrchestratorManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionOrchestratorManage)(idempotent(handler)))
	}
	requireGradingManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionGradingManage)(idempotent(handler)))
	}
	requireEvidenceManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionEvidenceManage)(idempotent(handler)))
	}
	requireReviewManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionReviewManage)(idempotent(handler)))
	}
	requireReviewWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(permissionReviewManage, permissionReviewWork)(idempotent(handler)))
	}
	requireOriginalReviewImage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin")(
			auth.RequireAnyPermission(permissionReviewManage, permissionEvidenceManage, "tenant:manage")(idempotent(handler)),
		))
	}
	requireArbitrationManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionArbitrationManage)(idempotent(handler)))
	}
	requireArbitrationWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(permissionArbitrationManage, "arbitration:work")(idempotent(handler)))
	}
	requireScoreManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionScoreManage)(idempotent(handler)))
	}
	requireRosterManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin")(
			auth.RequirePermission(permissionScoreManage)(idempotent(handler)),
		))
	}
	requireStudentGradeAccess := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("student:grade:read", permissionScoreManage)(idempotent(handler)))
	}
	requireAppealCreate := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:create")(idempotent(handler)))
	}
	requireAppealRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:read")(idempotent(handler)))
	}
	requireAppealManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:manage")(idempotent(handler)))
	}
	requireAppealWork := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("appeal:work")(idempotent(handler)))
	}
	requireQuestionAppealRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("student:grade:read", "appeal:read", "appeal:work", "appeal:manage")(idempotent(handler)))
	}
	requireAuditRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("audit:read")(idempotent(handler)))
	}
	requireReportRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("report:read")(idempotent(handler)))
	}
	requireSystemRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission(permissionSystemRead)(idempotent(handler)))
	}
	requireOnboardingRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyRole("platform_admin", "tenant_admin", "school_admin")(idempotent(handler)))
	}
	requireSchoolAdmin := func(handler http.HandlerFunc) http.Handler {
		// 聊天正文和模型答案不得持久化到幂等收据中。
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(auth.RequireAnyRole("school_admin")(handler)))
	}
	requireModelRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission("model:governance:read", "model:read")(idempotent(handler)))
	}
	requirePlatformModelPermission := func(permission string, handler http.HandlerFunc) http.Handler {
		return authenticate(auth.RequireRequestResourceBoundary(resourceResolver)(auth.RequireAnyRole("platform_admin")(
			// 每次请求都执行角色、权限和资源检查。模型治理存储保留写入审计轨迹。
			auth.RequireAnyPermission(permission, "model:config:manage")(idempotent(handler)),
		)))
	}
	requirePlatformModelRead := func(handler http.HandlerFunc) http.Handler {
		return requirePlatformModelPermission("model:read", handler)
	}
	requirePlatformManagedAPIManage := func(handler http.HandlerFunc) http.Handler {
		return requirePlatformModelPermission("model:managed_api:manage", handler)
	}
	requirePlatformPanelManage := func(handler http.HandlerFunc) http.Handler {
		return requirePlatformModelPermission("model:panel:manage", handler)
	}
	requirePlatformSchoolRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePlatformAdmin(
			auth.RequirePermission("tenant:manage")(idempotent(handler)),
		))
	}
	requireModelProviderManage := func(handler http.HandlerFunc) http.Handler {
		return requirePermission("model:provider:manage", handler)
	}
	requireModelPolicyManage := func(handler http.HandlerFunc) http.Handler {
		return requirePermission("model:policy:manage", handler)
	}
	requireModelEvaluationManage := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequirePermission("model:evaluation:manage")(idempotent(handler)))
	}
	requireOCRAvailabilityRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(
			permissionReviewWork,
			permissionReviewManage,
			"submission:manage",
			"capture:manage",
			permissionOCRManage,
			permissionGradingManage,
			permissionSystemRead,
		)(idempotent(handler)))
	}
	// worker 写接口只接受具备 OCR 或编排权限的服务身份，具体任务的租户、来源和租约仍由 withWorkerTask* 再核验。
	requireWorkerExecute := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(permissionOCRManage, permissionOrchestratorManage)(idempotent(handler)))
	}
	requireWorkerCompletion := func(handler http.HandlerFunc) http.Handler {
		// 完成回调由 completion scope 在租约校验后包装收据，不能在这里提前重放。
		return requireAuth(auth.RequireAnyPermission(permissionOCRManage, permissionOrchestratorManage)(handler))
	}
	requireWorkerRead := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireAnyPermission(permissionOCRManage, permissionOrchestratorManage, permissionSystemRead)(idempotent(handler)))
	}
	withScopedExam := func(handler http.HandlerFunc) http.HandlerFunc {
		return handler
	}
	withWorkerTaskScope := func(handler http.HandlerFunc) http.HandlerFunc {
		return workerruntime.TaskScope(workerRuntimeStore)(handler).ServeHTTP
	}
	// 已成功任务仅允许原租约主体重试完成；执行、失败、心跳和文件访问仍要求运行中租约。
	withWorkerTaskCompletionScope := func(handler http.HandlerFunc) http.HandlerFunc {
		return workerruntime.TaskCompletionScope(workerRuntimeStore)(idempotent(handler)).ServeHTTP
	}
	withWorkerTaskSource := func(sourceType string, pathParam string, handler http.HandlerFunc) http.HandlerFunc {
		guarded := workerruntime.RequireTaskSource(sourceType, pathParam)(handler)
		return workerruntime.TaskScope(workerRuntimeStore)(guarded).ServeHTTP
	}
	withWorkerTaskCompletionSource := func(sourceType string, pathParam string, handler http.HandlerFunc) http.HandlerFunc {
		guarded := workerruntime.RequireTaskSource(sourceType, pathParam)(idempotent(handler))
		return workerruntime.TaskCompletionScope(workerRuntimeStore)(guarded).ServeHTTP
	}
	withWorkerTaskPayload := func(pathParam string, payloadKey string, handler http.HandlerFunc) http.HandlerFunc {
		guarded := workerruntime.RequireTaskPayloadValue(pathParam, payloadKey)(handler)
		return workerruntime.TaskScope(workerRuntimeStore)(guarded).ServeHTTP
	}
	return routerGuards{
		authenticate:                    authenticate,
		requireAuth:                     requireAuth,
		requireAccount:                  requireAccount,
		requirePersonalReviewRead:       requirePersonalReviewRead,
		requirePersonalReviewWrite:      requirePersonalReviewWrite,
		requirePersonalReviewCleanup:    requirePersonalReviewCleanup,
		requireMFAAccount:               requireMFAAccount,
		requireLockedAccount:            requireLockedAccount,
		requirePermission:               requirePermission,
		requireRecentPermission:         requireRecentPermission,
		requireOrgManage:                requireOrgManage,
		requireStudentImport:            requireStudentImport,
		requireExamManage:               requireExamManage,
		requireAssessmentRead:           requireAssessmentRead,
		requireDashboardRead:            requireDashboardRead,
		requireQuestionBank:             requireQuestionBank,
		requireQuestionBankAny:          requireQuestionBankAny,
		requireQuestionBankImport:       requireQuestionBankImport,
		requireFileManage:               requireFileManage,
		requireTaskScopedFileManage:     requireTaskScopedFileManage,
		requireSubmissionManage:         requireSubmissionManage,
		requireCaptureManage:            requireCaptureManage,
		requireOCRManage:                requireOCRManage,
		requireSegmentManage:            requireSegmentManage,
		requireSegmentEvidenceRead:      requireSegmentEvidenceRead,
		requireOrchestratorManage:       requireOrchestratorManage,
		requireGradingManage:            requireGradingManage,
		requireEvidenceManage:           requireEvidenceManage,
		requireReviewManage:             requireReviewManage,
		requireReviewWork:               requireReviewWork,
		requireOriginalReviewImage:      requireOriginalReviewImage,
		requireArbitrationManage:        requireArbitrationManage,
		requireArbitrationWork:          requireArbitrationWork,
		requireScoreManage:              requireScoreManage,
		requireRosterManage:             requireRosterManage,
		requireStudentGradeAccess:       requireStudentGradeAccess,
		requireAppealCreate:             requireAppealCreate,
		requireAppealRead:               requireAppealRead,
		requireAppealManage:             requireAppealManage,
		requireAppealWork:               requireAppealWork,
		requireQuestionAppealRead:       requireQuestionAppealRead,
		requireAuditRead:                requireAuditRead,
		requireReportRead:               requireReportRead,
		requireSystemRead:               requireSystemRead,
		requireOnboardingRead:           requireOnboardingRead,
		requireSchoolAdmin:              requireSchoolAdmin,
		requireModelRead:                requireModelRead,
		requirePlatformModelRead:        requirePlatformModelRead,
		requirePlatformManagedAPIManage: requirePlatformManagedAPIManage,
		requirePlatformPanelManage:      requirePlatformPanelManage,
		requirePlatformSchoolRead:       requirePlatformSchoolRead,
		requireModelProviderManage:      requireModelProviderManage,
		requireModelPolicyManage:        requireModelPolicyManage,
		requireModelEvaluationManage:    requireModelEvaluationManage,
		requireOCRAvailabilityRead:      requireOCRAvailabilityRead,
		requireWorkerExecute:            requireWorkerExecute,
		requireWorkerCompletion:         requireWorkerCompletion,
		requireWorkerRead:               requireWorkerRead,
		withScopedExam:                  withScopedExam,
		withWorkerTaskScope:             withWorkerTaskScope,
		withWorkerTaskCompletionScope:   withWorkerTaskCompletionScope,
		withWorkerTaskSource:            withWorkerTaskSource,
		withWorkerTaskCompletionSource:  withWorkerTaskCompletionSource,
		withWorkerTaskPayload:           withWorkerTaskPayload,
	}
}
