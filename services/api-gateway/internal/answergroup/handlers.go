package answergroup

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store      Store
	audit      auth.AuditRecorder
	references goldpaper.ActiveApprovedReader
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func NewHandlerWithReferences(store Store, audit auth.AuditRecorder, references goldpaper.ActiveApprovedReader) *Handler {
	return &Handler{store: store, audit: audit, references: references}
}

func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/answer-groups/build", requireManage(handler.Build))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/answer-groups", requireRead(handler.List))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/answer-group-metrics", requireRead(handler.Metrics))
	mux.Handle("GET /api/v1/answer-groups/{groupId}", requireRead(handler.Get))
	mux.Handle("PUT /api/v1/answer-groups/{groupId}/samples/{segmentId}", requireManage(handler.ReviewSample))
	mux.Handle("PUT /api/v1/answer-groups/{groupId}/decision", requireManage(handler.PutDecision))
	mux.Handle("POST /api/v1/answer-groups/{groupId}/confirm", requireManage(handler.Confirm))
	mux.Handle("POST /api/v1/answer-groups/{groupId}/rollback", requireManage(handler.Rollback))
}

func (h *Handler) Build(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input BuildInput
	if !decodeJSON(w, r, &input) {
		return
	}
	groups, err := h.store.Build(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "answer_group.built", r.PathValue("questionId"), map[string]any{"group_count": len(groups)})
	httpx.JSON(w, http.StatusCreated, map[string]any{"answer_groups": groups})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	groups, err := h.store.List(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	references, err := h.referenceCases(r, user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_groups": groups, "teacher_reference_cases": references})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	group, err := h.store.Get(r.Context(), user.TenantID, r.PathValue("groupId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	references, err := h.referenceCases(r, user.TenantID, group.ExamID, group.QuestionID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_group": group, "teacher_reference_cases": references})
}

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	metric, err := h.store.Metrics(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"metrics": metric})
}

func (h *Handler) ReviewSample(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input SampleReviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	group, err := h.store.ReviewSample(r.Context(), user.TenantID, r.PathValue("groupId"), r.PathValue("segmentId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "answer_group.sample_reviewed", group.ID, map[string]any{"segment_id": r.PathValue("segmentId"), "outcome": input.Outcome})
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_group": group})
}

func (h *Handler) PutDecision(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input DecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	group, err := h.store.PutDecision(r.Context(), user.TenantID, r.PathValue("groupId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "answer_group.decision_saved", group.ID, map[string]any{"revision": group.Decision.Revision, "minimum_sample": group.MinimumSample})
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_group": group})
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input ConfirmInput
	if !decodeJSON(w, r, &input) {
		return
	}
	group, candidates, err := h.store.Confirm(r.Context(), user.TenantID, r.PathValue("groupId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "answer_group.confirmed", group.ID, map[string]any{
		"affected_count": len(candidates), "algorithm_version": group.AlgorithmVersion,
		"representation_version": group.RepresentationVersion, "rollback_reference": group.Decision.RollbackReference,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_group": group, "automation_candidates": candidates})
}

func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input RollbackInput
	if !decodeJSON(w, r, &input) {
		return
	}
	group, candidates, err := h.store.Rollback(r.Context(), user.TenantID, r.PathValue("groupId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "answer_group.rolled_back", group.ID, map[string]any{
		"affected_count": len(candidates), "algorithm_version": group.AlgorithmVersion,
		"rollback_reference": input.RollbackReference, "reason": input.Reason,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"answer_group": group, "automation_candidates": candidates})
}

func currentUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON with known fields")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "answer_group_not_found", "answer group resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_answer_group_input", "answer group input is invalid")
	case errors.Is(err, ErrNoEligibleAnswers):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "answer_group_no_eligible_answers", "only reliably textified exact-text or short-constructed answers can be grouped")
	case errors.Is(err, ErrSamplingIncomplete):
		httpx.Error(w, r, http.StatusConflict, "answer_group_sampling_incomplete", "minimum sample review and required outlier review must be completed before confirmation")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "answer_group_revision_conflict", "answer group decision changed; reload before saving")
	case errors.Is(err, ErrRollbackConflict):
		httpx.Error(w, r, http.StatusConflict, "answer_group_rollback_conflict", "rollback reference does not match the active group candidates")
	case errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "answer_group_state_conflict", "answer group state does not allow this operation")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "answer_group_operation_failed", "answer group operation failed")
	}
}

func (h *Handler) auditEvent(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "answer_group", TargetID: targetID,
		AfterValue: after, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}

// 只返回当前生效且已审批的 Gold Paper 版本，避免把历史或未审定答案当作教师参考。
func (h *Handler) referenceCases(r *http.Request, tenantID, examID, questionID string) ([]TeacherReferenceCase, error) {
	if h.references == nil {
		return []TeacherReferenceCase{}, nil
	}
	items, err := h.references.ListActiveApproved(r.Context(), tenantID, examID, questionID)
	if err != nil {
		return nil, err
	}
	out := make([]TeacherReferenceCase, 0, len(items))
	for _, item := range items {
		for _, version := range item.Versions {
			if version.Version != item.ActiveVersion || version.ApprovedAt == nil {
				continue
			}
			out = append(out, TeacherReferenceCase{
				GoldPaperID: item.ID, SubmissionID: item.SubmissionID, Version: version.Version,
				ReferenceScore: version.ReferenceScore, MaxScore: version.MaxScore, Explanation: version.Explanation,
				TraitScores: cloneMap(version.TraitScores), ErrorTags: append([]string(nil), version.ErrorTags...),
			})
			break
		}
	}
	return out, nil
}
