package grading

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

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
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
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
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
		return
	}
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
		return
	}
	run, taskIDs, err := store.BeginScoringRunCancellation(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if run.Status == "cancelled" {
		httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": run})
		return
	}
	// 先记录操作者的取消请求。即使连接断开，后台仍可完成已写入的 cancelling 运行。
	h.auditAction(r, "grading.scoring_run_cancel_requested", "scoring_run", run.ID, "request scoring run cancellation")
	if err := cancelScoringWorkerTasks(r.Context(), h.runtime, user.TenantID, taskIDs); err != nil {
		httpx.Error(w, r, http.StatusConflict, "scoring_run_cancellation_pending", "scoring run cancellation is waiting for worker task state")
		return
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
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
		return
	}
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
		return
	}
	commands, ok := h.store.(OMRRetryCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
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
		// 每个来源命令连同入队和汇总一起提交，故障时仍保留可再次枚举的终止失败项。
		if _, retryErr := commands.RetryOMRTask(r.Context(), user.TenantID, failedTask.OMRRunID); retryErr != nil {
			skipped++
			continue
		}
		requeued++
	}
	if coordinator, supported := h.store.(interface {
		ResumeScoringRun(context.Context, string, string, bool) (ScoringRun, int, int, error)
	}); supported {
		_, extra, extraSkipped, resumeErr := coordinator.ResumeScoringRun(r.Context(), user.TenantID, runID, true)
		if resumeErr != nil {
			writeStoreError(w, r, resumeErr)
			return
		}
		requeued += extra
		skipped += extraSkipped
	}
	run, err := store.RefreshScoringRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_run_retry_failed", "scoring_run", run.ID, "requeue failed OMR worker tasks")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": run, "requeued": requeued, "skipped": skipped})
}

func (h *Handler) ResumeScoringRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.store.(interface {
		ResumeScoringRun(context.Context, string, string, bool) (ScoringRun, int, int, error)
	})
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
		return
	}
	run, requeued, skipped, err := store.ResumeScoringRun(r.Context(), user.TenantID, r.PathValue("runId"), false)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_run_resumed", "scoring_run", run.ID, "resume frozen scoring plan")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_run": run, "requeued": requeued, "skipped": skipped})
}

func (h *Handler) ReprocessSegmentScore(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.scoringRecoveryStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "scoring_recovery_unavailable", scoringRecoveryUnavailable)
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
