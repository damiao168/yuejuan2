package assessment

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store Store
	audit auth.AuditRecorder
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func (h *Handler) ListSubjectProfiles(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	stage := EducationStage(strings.TrimSpace(r.URL.Query().Get("stage")))
	if stage != "" && !stage.Valid() {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_education_stage", "stage must be junior or senior")
		return
	}
	var subject SubjectCode
	if raw := strings.TrimSpace(r.URL.Query().Get("subject")); raw != "" {
		var ok bool
		subject, ok = NormalizeSubjectCode(raw)
		if !ok {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_subject_code", "subject is not supported")
			return
		}
	}
	items, err := h.store.ListSubjectProfiles(r.Context(), user.TenantID, stage, subject)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subject_profiles": items})
}

func (h *Handler) ListQuestionArchetypes(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListQuestionArchetypes(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"question_archetypes": items})
}

func (h *Handler) GetQuestionConfig(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	item, err := h.store.GetQuestionConfig(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"assessment_profile": item})
}

func (h *Handler) ConfigureQuestion(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input ConfigureQuestionInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	item, err := h.store.ConfigureQuestion(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID,
		Action: "assessment.question_profile_configured", TargetType: "question", TargetID: item.QuestionID,
		Reason:    "configure versioned subject, evidence and scoring policy",
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"assessment_profile": item})
}

func (h *Handler) GetQuestionSnapshot(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	item, err := h.store.GetQuestionSnapshot(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"assessment_snapshot": item})
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
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

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "assessment_not_found", "assessment configuration was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_assessment_configuration", "assessment configuration is invalid")
	case errors.Is(err, ErrPolicyViolation):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "assessment_policy_violation", "R3 extended responses cannot use AI fast confirmation")
	case errors.Is(err, ErrExamFrozen):
		httpx.Error(w, r, http.StatusConflict, "assessment_configuration_frozen", "assessment configuration is frozen after exam readiness confirmation")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "assessment_revision_conflict", "assessment configuration changed; reload before saving")
	case errors.Is(err, ErrSnapshotConflict):
		httpx.Error(w, r, http.StatusConflict, "assessment_snapshot_conflict", "assessment snapshot conflicts with the frozen exam configuration")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "assessment_operation_failed", "assessment operation failed")
	}
}
