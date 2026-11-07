package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"net/http"
	"strings"
)

func (h *Handler) ListEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 30, 100)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	items, err := h.store.ListEvaluationRuns(r.Context(), tenantID, EvaluationListFilter{
		Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID,
	})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_evaluation_list_failed", "failed to list model evaluations")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_runs": items, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) CreateEvaluationRun(w http.ResponseWriter, r *http.Request) {
	var input EvaluationRunInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if ValidateEvaluationRunInput(input) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation", "invalid offline model evaluation")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateEvaluationRun(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_create_failed", "failed to create model evaluation")
		return
	}
	h.auditAction(r, "model.evaluation_created", "model_evaluation_run", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_key": item.Key,
			"dataset_reference": item.DatasetReference, "dataset_sha256": item.DatasetSHA256,
			"authorization_reference": item.AuthorizationRef,
			"evidence_class":          item.EvidenceClass, "subject": item.Subject,
			"grade": item.Grade, "question_type": item.QuestionType,
			"modality": item.Modality, "sample_count": item.SampleCount,
			"repeat_count": item.RepeatCount,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"evaluation_run": item})
}

func (h *Handler) AddEvaluationCandidate(w http.ResponseWriter, r *http.Request) {
	var input EvaluationCandidateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation_candidate", "candidate reason is required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.AddEvaluationCandidate(
		r.Context(), tenantID, actorID, r.PathValue("id"), input,
	)
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_candidate_create_failed", "failed to add model evaluation candidate")
		return
	}
	h.auditAction(r, "model.evaluation_candidate_added", "model_evaluation_candidate", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_id": item.RunID,
			"provider_key": item.ProviderKey, "deployment_key": item.DeploymentKey,
			"model_version": item.ModelVersion, "prompt_version": item.PromptVersion,
			"rubric_version": item.RubricVersion, "evaluated_samples": item.EvaluatedSamples,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"candidate": item})
}

func (h *Handler) CompleteEvaluationRun(w http.ResponseWriter, r *http.Request) {
	h.transitionEvaluationRun(w, r, "complete")
}

func (h *Handler) InvalidateEvaluationRun(w http.ResponseWriter, r *http.Request) {
	h.transitionEvaluationRun(w, r, "invalidate")
}

func (h *Handler) transitionEvaluationRun(w http.ResponseWriter, r *http.Request, transition string) {
	var input EvaluationTransitionInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation_transition", "transition reason is required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	var (
		item EvaluationRun
		err  error
	)
	switch transition {
	case "complete":
		item, err = h.store.CompleteEvaluationRun(
			r.Context(), tenantID, actorID, r.PathValue("id"), input.Reason,
		)
	case "invalidate":
		item, err = h.store.InvalidateEvaluationRun(
			r.Context(), tenantID, actorID, r.PathValue("id"), input.Reason,
		)
	default:
		err = ErrInvalidEvaluation
	}
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_transition_failed", "failed to transition model evaluation")
		return
	}
	action := "model.evaluation_completed"
	if transition == "invalidate" {
		action = "model.evaluation_invalidated"
	}
	h.auditAction(r, action, "model_evaluation_run", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_key": item.Key,
			"evidence_class": item.EvidenceClass, "status": item.Status,
			"candidate_count": len(item.Candidates),
		})
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_run": item})
}
