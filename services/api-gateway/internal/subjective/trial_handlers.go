package subjective

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) trialPolicy() ModelPolicy {
	policy := NormalizePolicy(ModelPolicy{})
	if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	return policy
}

func (h *Handler) trialStore(w http.ResponseWriter, r *http.Request) (TrialPersistence, bool) {
	store, ok := h.store.(TrialPersistence)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_trial_unavailable", "scoring trial persistence is unavailable")
	}
	return store, ok
}

func writeTrialError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrTrialConflict) {
		httpx.Error(w, r, http.StatusConflict, "scoring_trial_conflict", "trial state, scope, revision or frozen versions no longer permit this operation")
		return
	}
	writeStoreError(w, r, err)
}

func (h *Handler) ListScoringTrials(w http.ResponseWriter, r *http.Request) {
	store, ok := h.trialStore(w, r)
	if !ok {
		return
	}
	user := mustUser(r)
	trials, err := store.ListScoringTrials(r.Context(), user.TenantID, r.PathValue("runId"), h.trialPolicy())
	if err != nil {
		writeTrialError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"trials": trials})
}

func (h *Handler) CreateScoringTrial(w http.ResponseWriter, r *http.Request) {
	store, ok := h.trialStore(w, r)
	if !ok {
		return
	}
	user := mustUser(r)
	var input CreateTrialInput
	if !decodeJSON(w, r, &input) {
		return
	}
	policy := h.trialPolicy()
	if err := ValidatePolicy(policy); err != nil {
		writeTrialError(w, r, err)
		return
	}
	trial, err := store.CreateScoringTrial(r.Context(), user.TenantID, user.ID, r.PathValue("runId"), input, policy)
	if err != nil {
		writeTrialError(w, r, err)
		return
	}
	h.auditAction(r, "subjective.trial_created", "scoring_ai_trial", trial.ID, "record independent human baselines before sample AI enqueue")
	httpx.JSON(w, http.StatusCreated, map[string]any{"trial": trial})
}

// 入队仍沿用已有准入、预算和 worker 幂等流水线；试改只授权它自己的样本或已批准同题扩批。
func (h *Handler) EnqueueScoringTrial(w http.ResponseWriter, r *http.Request) {
	store, ok := h.trialStore(w, r)
	if !ok {
		return
	}
	user := mustUser(r)
	r = r.WithContext(withReferencePreflightCache(r.Context()))
	policy := h.trialPolicy()
	if err := h.preflightTrialReferenceMaterial(r.Context(), store, user.TenantID, r.PathValue("runId"), r.PathValue("trialId"), policy); err != nil {
		writeTrialError(w, r, err)
		return
	}
	trial, err := store.PrepareScoringTrialEnqueue(r.Context(), user.TenantID, r.PathValue("runId"), r.PathValue("trialId"), policy)
	if err != nil {
		writeTrialError(w, r, err)
		return
	}
	batchID := trial.SampleBatchID
	if trial.Status == "expanded" {
		batchID = trial.ExpansionBatchID
	}
	r.SetPathValue("batchId", batchID)
	h.EnqueueBatch(w, r)
}

func (h *Handler) preflightTrialReferenceMaterial(ctx context.Context, store TrialPersistence, tenant, runID, trialID string, policy ModelPolicy) error {
	trials, err := store.ListScoringTrials(ctx, tenant, runID, policy)
	if err != nil {
		return err
	}
	for _, trial := range trials {
		if trial.ID != trialID || trial.RunID != runID {
			continue
		}
		segmentID := ""
		if trial.Status == "expanded" {
			if len(trial.RemainingSegmentIDs) > 0 {
				segmentID = trial.RemainingSegmentIDs[0]
			}
		} else if len(trial.Samples) > 0 {
			segmentID = trial.Samples[0].SegmentID
		}
		if segmentID == "" {
			return ErrTrialConflict
		}
		value, err := h.store.LoadContext(ctx, tenant, segmentID)
		if err != nil {
			return err
		}
		if value.Question.ID != trial.QuestionID {
			return ErrTrialConflict
		}
		// A02 的同题冻结范围由后面的 Prepare 再核验。这里只取该精确批次的一份上下文，
		// 避免 1000 份扩批重复读取同一材料与能力；旧单图不提前准备数学证据。
		return h.prepareReferenceMaterial(ctx, tenant, &value)
	}
	return ErrNotFound
}

func (h *Handler) DecideScoringTrial(w http.ResponseWriter, r *http.Request) {
	store, ok := h.trialStore(w, r)
	if !ok {
		return
	}
	user := mustUser(r)
	var input TrialDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	trial, err := store.DecideScoringTrial(r.Context(), user.TenantID, user.ID, r.PathValue("runId"), r.PathValue("trialId"), input, h.trialPolicy())
	if err != nil {
		writeTrialError(w, r, err)
		return
	}
	h.auditAction(r, "subjective.trial_"+input.Decision, "scoring_ai_trial", trial.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"trial": trial})
}
