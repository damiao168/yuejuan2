package releasegate

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// Handler 提供发布策略、证据及警告豁免操作；路由层必须注入成绩发布的管理权限守卫。
// 这里仅管理发布门禁，实际成绩发布由 PublicationCoordinator 组合执行。
type Handler struct {
	service *Service
	audit   auth.AuditRecorder
}

func NewHandler(service *Service, audit auth.AuditRecorder) *Handler {
	return &Handler{service: service, audit: audit}
}

func RegisterRoutes(mux *http.ServeMux, h *Handler, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/release-gate/policies", requireManage(h.CreatePolicy))
	mux.Handle("POST /api/v1/exams/{examId}/release-gate/preview", requireManage(h.Preview))
	mux.Handle("GET /api/v1/release-gate-evidence/{evidenceId}", requireManage(h.GetEvidence))
	mux.Handle("POST /api/v1/exams/{examId}/release-gate/waivers", requireManage(h.RequestWaiver))
	mux.Handle("POST /api/v1/release-gate-waivers/{waiverId}/decision", requireManage(h.DecideWaiver))
}

func (h *Handler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := gateUser(w, r)
	if !ok {
		return
	}
	var input CreatePolicyInput
	if !decodeBody(w, r, &input) {
		return
	}
	policy, err := h.service.CreatePolicy(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "release_gate.policy_created", policy.ID, map[string]any{"exam_id": policy.ExamID, "version": policy.Version})
	httpx.JSON(w, http.StatusCreated, map[string]any{"release_gate_policy": policy})
}
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	user, ok := gateUser(w, r)
	if !ok {
		return
	}
	var input struct {
		ReleaseID string `json:"release_id"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	evidence, err := h.service.Preview(r.Context(), user.TenantID, r.PathValue("examId"), input.ReleaseID, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "release_gate.previewed", evidence.ID, map[string]any{"exam_id": evidence.ExamID, "release_id": evidence.ReleaseID, "passed": evidence.Evaluation.Passed})
	httpx.JSON(w, http.StatusCreated, map[string]any{"release_gate_evidence": evidence})
}
func (h *Handler) GetEvidence(w http.ResponseWriter, r *http.Request) {
	user, ok := gateUser(w, r)
	if !ok {
		return
	}
	evidence, err := h.service.GetEvidence(r.Context(), user.TenantID, r.PathValue("evidenceId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"release_gate_evidence": evidence})
}
func (h *Handler) RequestWaiver(w http.ResponseWriter, r *http.Request) {
	user, ok := gateUser(w, r)
	if !ok {
		return
	}
	var input RequestWaiverInput
	if !decodeBody(w, r, &input) {
		return
	}
	waiver, err := h.service.RequestWaiver(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "release_gate.waiver_requested", waiver.ID, map[string]any{"exam_id": waiver.ExamID, "issue_code": waiver.IssueCode, "evidence_id": waiver.EvidenceID})
	httpx.JSON(w, http.StatusCreated, map[string]any{"release_gate_waiver": waiver})
}
func (h *Handler) DecideWaiver(w http.ResponseWriter, r *http.Request) {
	user, ok := gateUser(w, r)
	if !ok {
		return
	}
	var input DecideWaiverInput
	if !decodeBody(w, r, &input) {
		return
	}
	waiver, err := h.service.DecideWaiver(r.Context(), user.TenantID, r.PathValue("waiverId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "release_gate.waiver_decided", waiver.ID, map[string]any{"exam_id": waiver.ExamID, "issue_code": waiver.IssueCode, "status": waiver.Status})
	httpx.JSON(w, http.StatusOK, map[string]any{"release_gate_waiver": waiver})
}

func gateUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
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
		httpx.Error(w, r, http.StatusNotFound, "release_gate_not_found", "release gate resource was not found")
	case errors.Is(err, ErrForbiddenWaiver):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "release_gate_waiver_forbidden", "only policy-allowed non-blocking warnings may be waived")
	case errors.Is(err, ErrGateBlocked):
		httpx.Error(w, r, http.StatusConflict, "release_gate_blocked", "release gate is blocked")
	case errors.Is(err, ErrEvidenceImmutable):
		httpx.Error(w, r, http.StatusConflict, "release_gate_evidence_immutable", "release gate evidence or decision is immutable")
	default:
		httpx.Error(w, r, http.StatusBadRequest, "invalid_release_gate_input", "release gate input is invalid")
	}
}
func (h *Handler) auditEvent(r *http.Request, user auth.User, action, targetID string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "release_gate", TargetID: targetID, AfterValue: detail, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}
