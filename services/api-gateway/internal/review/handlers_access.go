package review

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
	"strings"
)

func (h *Handler) authorizeTaskWorker(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if reviewWorkerScoped(user) && task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

// 草稿属于被分配复核人员的可变工作成果。
// 复核管理员可以检查任务上下文和答案图像，但不能仅凭管理队列权限读取或覆盖复核人员的私有草稿。
func (h *Handler) authorizeAssignedReviewer(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if task.AssignedTo == "" || task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

// 队列分配的限制比 review:manage 更严格：目标必须是同一租户内启用的阅卷账户。
func (h *Handler) authorizeReviewAssignee(w http.ResponseWriter, r *http.Request, tenantID, assigneeID string) bool {
	return h.authorizeActiveRole(w, r, tenantID, assigneeID, "grader")
}

func (h *Handler) authorizeArbitrationAssignee(w http.ResponseWriter, r *http.Request, tenantID, assigneeID string) bool {
	return h.authorizeActiveRole(w, r, tenantID, assigneeID, "arbitrator")
}

func (h *Handler) authorizeActiveRole(w http.ResponseWriter, r *http.Request, tenantID, userID, roleCode string) bool {
	if userID == "" {
		writeStoreError(w, r, ErrInvalidInput)
		return false
	}
	users, err := h.audit.ListManagedUsers(r.Context(), tenantID, auth.ManagedUserFilter{UserID: userID, Limit: 2})
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	for _, candidate := range users {
		if candidate.ID != userID || candidate.Status != "active" {
			continue
		}
		for _, role := range candidate.Roles {
			if role == roleCode {
				return true
			}
		}
		break
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

func (h *Handler) authorizeReviewManager(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	if reviewManagerScoped(user) {
		return true
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

// 请求体中的答题区域不经过路径中间件；创建任务前必须按同一学校管理边界解析。
func (h *Handler) authorizeManagedSegment(w http.ResponseWriter, r *http.Request, user auth.User, segmentID string) bool {
	return h.authorizeManagedBodyResource(w, r, "answer_segment", segmentID)
}

func (h *Handler) authorizeManagedBodyResource(w http.ResponseWriter, r *http.Request, resourceType, resourceID string) bool {
	scope, ok := auth.AccessScopeFromContext(r.Context())
	resolver, supported := h.audit.(auth.ResourceBoundaryResolver)
	if !ok || !supported || strings.TrimSpace(resourceID) == "" {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	boundary, err := resolver.ResolveResourceBoundary(r.Context(), scope, resourceType, resourceID)
	if err != nil || !auth.AllowsManagedResourceBoundary(scope, boundary) {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

func (h *Handler) authorizeArbitrationManager(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	if arbitrationManagerScoped(user) {
		return true
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

// 原始整页可能含身份信息；即使能复核题目，也须同时满足这里的管理角色与权限条件。
func canViewOriginalReviewImage(user auth.User) bool {
	if !hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") {
		return false
	}
	return auth.HasPermission(user, "review:manage") ||
		auth.HasPermission(user, "evidence:manage") ||
		auth.HasPermission(user, "tenant:manage")
}

func hasAnyRole(user auth.User, roles ...string) bool {
	for _, current := range user.Roles {
		for _, allowed := range roles {
			if current == allowed {
				return true
			}
		}
	}
	return false
}

func reviewWorkerScoped(user auth.User) bool {
	return !reviewManagerScoped(user)
}

func applyReviewListScope(filter *ListFilter, scope auth.AccessScope) {
	filter.ScopeMode = scope.QueryMode()
	filter.ScopeActorID = scope.ActorID
	filter.ScopeSchoolIDs = append([]string(nil), scope.SchoolIDs...)
	if filter.ScopeMode == "school" {
		// 班级与外校分派会扩展导航学校；完整学校范围只能来自持久化的管理员绑定，空绑定不能退回导航集合。
		filter.ScopeSchoolIDs = scope.ManagedSchoolIDs()
	}
	filter.ScopeExamIDs = append([]string(nil), scope.ExamIDs...)
	filter.ScopeTaskIDs = append([]string(nil), scope.ReviewTaskIDs...)
}

func applyArbitrationListScope(filter *ArbitrationFilter, scope auth.AccessScope) {
	filter.ScopeMode = scope.QueryMode()
	filter.ScopeActorID = scope.ActorID
	filter.ScopeSchoolIDs = append([]string(nil), scope.SchoolIDs...)
	if filter.ScopeMode == "school" {
		// 外校任课或任务分派只能增加导航范围；仲裁管理列表与复核列表共用可信的管理学校边界。
		filter.ScopeSchoolIDs = scope.ManagedSchoolIDs()
	}
	filter.ScopeExamIDs = append([]string(nil), scope.ExamIDs...)
	filter.ScopeTaskIDs = append([]string(nil), scope.ArbitrationTaskIDs...)
}

func arbitrationWorkerScoped(user auth.User) bool {
	return !arbitrationManagerScoped(user)
}

func reviewManagerScoped(user auth.User) bool {
	return hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") && auth.HasPermission(user, "review:manage")
}

func arbitrationManagerScoped(user auth.User) bool {
	return hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") && auth.HasPermission(user, "arbitration:manage")
}
