package review

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
	"strings"
)

func (h *Handler) SetExamDoubleMarkPolicy(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input SetDoubleMarkPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := h.store.SetExamDoubleMarkPolicy(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_policy_set", "exam", policy.ExamID, "set exam double mark policy")
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) SetQuestionDoubleMarkPolicy(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input SetDoubleMarkPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := h.store.SetQuestionDoubleMarkPolicy(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_policy_set", "question", policy.QuestionID, "set question double mark policy")
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) ListDoubleMarkPolicies(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	policyFilter := PolicyFilter{
		ExamID:     strings.TrimSpace(r.URL.Query().Get("exam_id")),
		QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")),
	}
	access, ok := h.doubleMarkCollectionAccess(w, r, user)
	if !ok {
		return
	}
	policyFilter.Access = access
	policies, err := h.store.ListDoubleMarkPolicies(r.Context(), user.TenantID, policyFilter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policies": policies})
}

func (h *Handler) CreateDoubleMarkSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input CreateDoubleMarkSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeManagedSegment(w, r, user, input.AnswerSegmentID) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.FirstReviewerID) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.SecondReviewerID) {
		return
	}
	session, err := h.store.CreateDoubleMarkSession(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_session_created", "double_mark_session", session.ID, "create double mark session")
	httpx.JSON(w, http.StatusCreated, map[string]any{"double_mark_session": session})
}

func (h *Handler) ListDoubleMarkSessions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	sessionFilter := DoubleMarkSessionFilter{
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		AnswerSegmentID: strings.TrimSpace(r.URL.Query().Get("answer_segment_id")),
		ExamID:          strings.TrimSpace(r.URL.Query().Get("exam_id")),
		QuestionID:      strings.TrimSpace(r.URL.Query().Get("question_id")),
	}
	access, ok := h.doubleMarkCollectionAccess(w, r, user)
	if !ok {
		return
	}
	sessionFilter.Access = access
	sessions, err := h.store.ListDoubleMarkSessions(r.Context(), user.TenantID, sessionFilter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"double_mark_sessions": sessions})
}

func (h *Handler) GetDoubleMarkSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	session, err := h.store.GetDoubleMarkSession(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"double_mark_session": session})
}

// 集合路由没有路径对象可供边界中间件检查，必须把认证范围交给存储筛选；请求参数只能继续缩小范围。
func (h *Handler) doubleMarkCollectionAccess(w http.ResponseWriter, r *http.Request, user auth.User) (auth.ExamCollectionAccess, bool) {
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return auth.ExamCollectionAccess{}, false
	}
	resolver, _ := h.audit.(auth.ResourceBoundaryResolver)
	return auth.ExamCollectionAccess{Scope: &scope, Resolver: resolver, Management: true}, true
}
