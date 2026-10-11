package aieligibility

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const authenticationRequired = "authentication required"

// Handler 只提供策略与审计查询。准入由进程内 Gate 调用；此处若接受客户端任意快照，会允许调用方伪造 OCR 或评测证据。
type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/ai-eligibility/policy", requireRead(h.GetPolicy))
	mux.Handle("PUT /api/v1/ai-eligibility/policy", requireManage(h.PutPolicy))
	mux.Handle("GET /api/v1/ai-eligibility/decisions/{runItemId}", requireRead(h.GetDecision))
}
func (h *Handler) PutPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authenticationRequired)
		return
	}
	var input PutPolicyInput
	if !decodeBody(w, r, &input) {
		return
	}
	item, err := h.service.PutPolicy(r.Context(), user.TenantID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}
func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authenticationRequired)
		return
	}
	subject, subjectOK := assessmentSubject(r.URL.Query().Get("subject_code"))
	stage, stageOK := assessmentStage(r.URL.Query().Get("education_stage"))
	risk, riskOK := assessmentRisk(r.URL.Query().Get("risk_tier"))
	archetype := strings.TrimSpace(r.URL.Query().Get("archetype_code"))
	if !subjectOK || !stageOK || !riskOK || archetype == "" {
		writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.service.GetActivePolicy(r.Context(), user.TenantID, subject, stage, archetype, risk)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}
func (h *Handler) GetDecision(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authenticationRequired)
		return
	}
	item, err := h.service.GetDecision(r.Context(), user.TenantID, r.PathValue("runItemId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"decision": item})
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
		httpx.Error(w, r, http.StatusNotFound, "ai_eligibility_not_found", "AI eligibility resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_ai_eligibility_input", "AI eligibility input is invalid")
	case errors.Is(err, ErrPolicyConflict):
		httpx.Error(w, r, http.StatusConflict, "ai_eligibility_policy_conflict", "policy version conflicts with current policy")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "ai_eligibility_operation_failed", "AI eligibility operation failed")
	}
}
func assessmentSubject(value string) (assessment.SubjectCode, bool) {
	return assessment.NormalizeSubjectCode(value)
}
func assessmentStage(value string) (assessment.EducationStage, bool) {
	stage := assessment.EducationStage(strings.TrimSpace(value))
	return stage, stage.Valid()
}
func assessmentRisk(value string) (assessment.RiskTier, bool) {
	risk := assessment.RiskTier(strings.TrimSpace(value))
	return risk, risk.Valid()
}
