package grading

import (
	"encoding/json"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	filespkg "edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store   Store
	engine  *Engine
	audit   auth.AuditRecorder
	runtime workerruntime.Store
	files   filespkg.Store
}

func NewHandler(store Store, engine *Engine, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, engine: engine, audit: audit}
}

func (h *Handler) SetProductionDependencies(runtime workerruntime.Store, files filespkg.Store) {
	h.runtime = runtime
	h.files = files
}

func (h *Handler) RecordAnswer(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RecordAnswerInput
	if !decodeJSON(w, r, &input) {
		return
	}
	answer, err := h.store.RecordAnswer(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.answer_recorded", "answer_segment", answer.AnswerSegmentID, "record answer for rule grading")
	httpx.JSON(w, http.StatusOK, map[string]any{"answer": answer})
}

func (h *Handler) RuleGrade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	ctx, err := h.store.LoadContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	evaluation, err := h.engine.Grade(ctx)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	grade, err := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, evaluation)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.rule_grade_created", "ai_grade", grade.ID, "create rule-based objective grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

func (h *Handler) ListGrades(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	grades, err := h.store.ListGrades(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grades": grades})
}

func (h *Handler) CreateScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateScoringRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := h.ruleStore().CreateScoringRule(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_created", "scoring_rule", rule.ID, "create scoring rule draft")
	httpx.JSON(w, http.StatusCreated, map[string]any{"scoring_rule": rule})
}

func (h *Handler) ListScoringRules(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	rules, err := h.ruleStore().ListScoringRules(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rules": rules})
}

func (h *Handler) UpdateScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdateScoringRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := h.ruleStore().UpdateScoringRule(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_updated", "scoring_rule", rule.ID, "update scoring rule draft")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rule": rule})
}

func (h *Handler) PublishScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	rule, err := h.ruleStore().PublishScoringRule(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_published", "scoring_rule", rule.ID, "publish immutable scoring rule")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rule": rule})
}

func (h *Handler) CreateOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	var input CreateOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.CreateOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_created", "omr_calibration_session", detail.Session.ID, "create deterministic OMR calibration sample")
	httpx.JSON(w, http.StatusCreated, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) ListOMRCalibrations(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	items, err := store.ListOMRCalibrations(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibrations": items})
}

func (h *Handler) GetOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	detail, err := store.GetOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) LabelOMRCalibrationCase(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	var input LabelOMRCalibrationCaseInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.LabelOMRCalibrationCase(r.Context(), user.TenantID, r.PathValue("id"), r.PathValue("caseId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_case_labeled", "omr_calibration_case", r.PathValue("caseId"), "record immutable manual OMR calibration label")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) ApproveOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	var input ApproveOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.ApproveOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_approved", "omr_calibration_session", detail.Session.ID, "approve OMR automatic-confirmation calibration")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) RevokeOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	var input RevokeOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.RevokeOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_revoked", "omr_calibration_session", detail.Session.ID, "revoke OMR automatic-confirmation calibration")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) DiscardOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", "OMR calibration requires the production grading store")
		return
	}
	var input DiscardOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.DiscardOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_discarded", "omr_calibration_session", detail.Session.ID, "discard draft OMR calibration without altering its evidence")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) RecoverScoringCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.scoringStore().RecoverScoringCommand(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, r.PathValue("commandId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) StartScoringRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input StartScoringRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.scoringStore().StartScoringRun(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err = h.scoringStore().ProcessRuleCandidates(r.Context(), user.TenantID, run.ID, user.ID, h.engine); err != nil {
		writeStoreError(w, r, err)
		return
	}
	runSummary, summaryErr := h.scoringStore().GetScoringSummary(r.Context(), user.TenantID, run.ExamID)
	if summaryErr == nil && runSummary.Run != nil && runSummary.Run.ID == run.ID {
		run = *runSummary.Run
	}
	h.auditAction(r, "grading.scoring_run_started", "scoring_run", run.ID, "start exam scoring orchestration")
	httpx.JSON(w, http.StatusCreated, map[string]any{"scoring_run": run})
}

func (h *Handler) GetScoringReadiness(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	readiness, err := h.scoringStore().GetScoringReadiness(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_readiness": readiness})
}

func (h *Handler) GetScoringSummary(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	summary, err := h.scoringStore().GetScoringSummary(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_summary": summary})
}

func (h *Handler) GetExamAutomationResults(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_results_unavailable", "scoring results are unavailable")
		return
	}
	results, err := store.GetExamAutomationResults(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, results)
}

func (h *Handler) GetScoringRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	detail, err := store.GetScoringRunDetail(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": detail.Run, "items": detail.Items})
}

func (h *Handler) CancelScoringRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	run, taskIDs, err := store.BeginScoringRunCancellation(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	for _, taskID := range taskIDs {
		task, getErr := h.runtime.Get(r.Context(), user.TenantID, taskID)
		if getErr != nil {
			httpx.Error(w, r, http.StatusConflict, "scoring_run_cancellation_pending", "scoring run cancellation is waiting for worker task state")
			return
		}
		if task.Status != workerruntime.StatusQueued && task.Status != workerruntime.StatusLeased && task.Status != workerruntime.StatusRunning {
			continue
		}
		if _, cancelErr := h.runtime.Cancel(r.Context(), user.TenantID, taskID); cancelErr != nil {
			httpx.Error(w, r, http.StatusConflict, "scoring_run_cancellation_pending", "scoring run cancellation is waiting for worker task state")
			return
		}
	}
	run, err = store.FinalizeScoringRunCancellation(r.Context(), user.TenantID, run.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_run_cancelled", "scoring_run", run.ID, "cancel scoring run and its active worker tasks")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": run})
}

func (h *Handler) RetryFailedScoringRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	runID := r.PathValue("runId")
	failed, err := store.ListFailedOMRTasks(r.Context(), user.TenantID, runID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	requeued := 0
	skipped := 0
	for _, failedTask := range failed {
		prepared, prepareErr := store.PrepareOMRRetry(r.Context(), user.TenantID, failedTask.OMRRunID)
		if prepareErr != nil {
			skipped++
			continue
		}
		if _, requeueErr := h.runtime.Requeue(r.Context(), user.TenantID, prepared.RuntimeTaskID); requeueErr != nil {
			_ = store.RestoreOMRRetry(r.Context(), user.TenantID, prepared.OMRRunID)
			skipped++
			continue
		}
		requeued++
	}
	run, err := store.RefreshScoringRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_run_retry_failed", "scoring_run", run.ID, "requeue failed OMR worker tasks")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": run, "requeued": requeued, "skipped": skipped})
}

func (h *Handler) ReprocessSegmentScore(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", "scoring recovery is unavailable")
		return
	}
	var input StartScoringRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := store.ReprocessSegmentScore(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err = h.scoringStore().ProcessRuleCandidates(r.Context(), user.TenantID, run.ID, user.ID, h.engine); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if refreshed, refreshErr := store.RefreshScoringRun(r.Context(), user.TenantID, run.ID); refreshErr == nil {
		run = refreshed
	}
	h.auditAction(r, "grading.segment_score_reprocessed", "answer_segment", r.PathValue("id"), "reprocess score for one answer segment")
	httpx.JSON(w, http.StatusCreated, map[string]any{"scoring_run": run})
}

func (h *Handler) CompleteOMR(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil || h.files == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_dependencies_unavailable", "OMR result dependencies are unavailable")
		return
	}
	var input OMRResultInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.scoringStore().GetOMRRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "omr_run" || task.SourceID != run.ID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "omr_task_mismatch", "worker task does not belong to this OMR run")
		return
	}
	overlay, err := h.files.Get(r.Context(), user.TenantID, input.OverlayFileAssetID)
	if err != nil || overlay.ExamID != run.ExamID || overlay.OwnerType != "omr_evidence" || overlay.OwnerID != run.ID || overlay.HashSHA256 != input.OverlaySHA256 {
		httpx.Error(w, r, http.StatusBadRequest, "omr_overlay_invalid", "OMR overlay has an invalid owner, hash, or exam")
		return
	}
	out, questionGrade, err := h.scoringStore().ApplyOMRResult(r.Context(), user.TenantID, run.ID, user.ID, input, h.engine)
	if err != nil {
		if isOMRRuntimeCompletionError(err) {
			httpx.Error(w, r, http.StatusConflict, "omr_task_completion_failed", "OMR task lease or result is no longer valid")
			return
		}
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_completed", "omr_run", out.ID, "complete OMR extraction")
	response := map[string]any{"omr_run": out}
	if questionGrade != nil {
		response["question_grade"] = questionGrade
	}
	httpx.JSON(w, http.StatusOK, response)
}

func (h *Handler) FailOMR(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_dependencies_unavailable", "OMR result dependencies are unavailable")
		return
	}
	var input OMRFailureInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.scoringStore().GetOMRRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "omr_run" || task.SourceID != run.ID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "omr_task_mismatch", "worker task does not belong to this OMR run")
		return
	}
	updated, err := h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		httpx.Error(w, r, http.StatusConflict, "omr_task_failure_rejected", "OMR task failure is no longer valid")
		return
	}
	out, err := h.scoringStore().ApplyOMRFailure(r.Context(), user.TenantID, run.ID, input.ErrorCode, input.ErrorDetail, updated.Status == workerruntime.StatusQueued)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_failed", "omr_run", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"omr_run": out, "task_status": updated.Status})
}

func (h *Handler) ruleStore() RuleStore {
	return h.store.(RuleStore)
}

func (h *Handler) scoringStore() ScoringRunStore { return h.store.(ScoringRunStore) }

func (h *Handler) omrCalibrationStore() (OMRCalibrationStore, bool) {
	store, ok := h.store.(OMRCalibrationStore)
	return store, ok
}

func (h *Handler) scoringRecoveryStore() (ScoringRecoveryStore, bool) {
	store, ok := h.store.(ScoringRecoveryStore)
	return store, ok
}

func isOMRRuntimeCompletionError(err error) bool {
	return errors.Is(err, workerruntime.ErrNotFound) ||
		errors.Is(err, workerruntime.ErrInvalidInput) ||
		errors.Is(err, workerruntime.ErrLeaseExpired) ||
		errors.Is(err, workerruntime.ErrLeaseMismatch) ||
		errors.Is(err, workerruntime.ErrConflict) ||
		errors.Is(err, workerruntime.ErrInvalidTransition)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var readinessErr *ScoringReadinessError
	if errors.As(err, &readinessErr) {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    "scoring_not_ready",
				"message": "考试尚未满足评分启动条件",
			},
			"scoring_readiness": readinessErr.Readiness,
		})
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "grading_resource_not_found", "grading resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_grading_input", "grading input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_scoring_run_transition", "scoring run operation is not allowed in its current state")
	case errors.Is(err, ErrCommandConflict):
		httpx.Error(w, r, http.StatusConflict, "scoring_command_conflict", "command ID is already associated with another request")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "grading_action_forbidden", "grading action is not permitted for this user")
	case errors.Is(err, ErrAnswerMissing):
		httpx.Error(w, r, http.StatusConflict, "answer_segment_answer_missing", "answer segment has no recorded answer")
	case errors.Is(err, ErrAnswerKeyMissing):
		httpx.Error(w, r, http.StatusConflict, "question_answer_key_missing", "question has no answer key")
	case errors.Is(err, ErrUnsupportedQuestionType):
		httpx.Error(w, r, http.StatusBadRequest, "unsupported_rule_grading_question_type", "question type is not supported by rule grading")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "scoring_rule_revision_conflict", "scoring rule was changed by another user")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "grading_operation_failed", "grading operation failed")
	}
}

func decorateOMRCalibrationDetail(detail OMRCalibrationDetail) OMRCalibrationDetail {
	for index := range detail.Cases {
		detail.Cases[index].SegmentImageURL = "/api/v1/answer-segments/" + detail.Cases[index].AnswerSegmentID + "/image"
		if detail.Cases[index].Matches == nil {
			// Blind labels must not be influenceable through browser devtools or
			// a direct API call. Reveal the immutable worker snapshot only after
			// this case has received its one-time human label.
			detail.Cases[index].ObservedDecision = ""
			detail.Cases[index].ObservedOptions = nil
			detail.Cases[index].ObservedConfidence = 0
			detail.Cases[index].Measurements = nil
			detail.Cases[index].SampleStratum = ""
		}
	}
	return detail
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
