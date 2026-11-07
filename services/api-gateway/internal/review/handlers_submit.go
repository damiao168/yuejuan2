package review

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"errors"
	"net/http"
)

func (h *Handler) SubmitGrade(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	var input SubmitGradeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	r = r.WithContext(commandreceipt.WithInput(r.Context(), input))
	if h.trySubmitSeedGrade(w, r, user, input) {
		return
	}
	r = r.WithContext(prepareSubmissionQualification(r.Context(), h.store, h.qualificationGate, user.TenantID, r.PathValue("id"), user.ID))
	result, err := h.store.SubmitGrade(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.human_grade_submitted", "review_task", result.Task.ID, "submit human grade")
	if h.disagreement != nil && result.Task.GradeRound == "single" && result.Grade.AIGradeID != "" {
		// Capture 直接从租户作用域内的存储取得两侧来源事实。
		// 临时投影错误不能使已提交成绩失效。
		_, _, _ = h.disagreement.ObserveHumanGrade(r.Context(), user.TenantID, result.Grade.AIGradeID, result.Grade.ID)
	}
	if result.FinalGrade != nil {
		h.auditAction(r, "review.double_mark_auto_finalized", "final_grade", result.FinalGrade.ID, "double mark score difference within threshold")
	}
	if result.ArbitrationTask != nil {
		h.auditAction(r, "arbitration.task_created", "arbitration_task", result.ArbitrationTask.ID, "double mark score difference exceeds threshold")
	}
	response := map[string]any{"task": graderTaskView(user, result.Task), "human_grade": result.Grade}
	if result.QuestionGradeID != "" {
		response["question_grade_id"] = result.QuestionGradeID
	}
	if result.DoubleMarkSession != nil {
		response["double_mark_session"] = result.DoubleMarkSession
	}
	if result.FinalGrade != nil {
		response["final_grade"] = result.FinalGrade
	}
	if result.ArbitrationTask != nil {
		response["arbitration_task"] = result.ArbitrationTask
	}
	httpx.JSON(w, http.StatusCreated, response)
}

// trySubmitSeedGrade 返回 true 表示已写成功或错误响应；仅未接管时允许普通存储继续提交。
func (h *Handler) trySubmitSeedGrade(w http.ResponseWriter, r *http.Request, user auth.User, input SubmitGradeInput) bool {
	if h.seedHook == nil {
		return false
	}
	seedExamID, seedQuestionID := "", ""
	if h.seedRefresher != nil {
		// 任务上下文只携带合成标识，但保留聚合质量监控所需的考试与题目作用域。
		if seedContext, handled, contextErr := h.seedHook.GetGraderTaskContext(r.Context(), user.TenantID, r.PathValue("id"), user.ID); contextErr == nil && handled {
			seedExamID, seedQuestionID = seedContext.Task.ExamID, seedContext.Task.QuestionID
		}
	}
	selections := make(map[string]any, len(input.RubricSelections))
	for _, selection := range input.RubricSelections {
		selections[selection.PointID] = selection.Score
	}
	receipt, handled, seedErr := h.seedHook.TrySubmit(r.Context(), user.TenantID, r.PathValue("id"), user.ID, seedquality.SubmitInput{
		AutoSubmit: input.AutoSubmit, Score: input.Score, RubricSelections: selections, ExpectedRevision: input.ExpectedRevision,
	})
	// 钩子错误优先于 handled；失败不能回退普通存储，避免同次请求提交另一种任务。
	if seedErr != nil {
		writeSeedHookError(w, r, seedErr)
		return true
	}
	if !handled {
		return false
	}
	h.auditAction(r, "review.human_grade_submitted", "review_task", receipt.TaskID, "submit quality review task")
	if h.seedRefresher != nil && seedExamID != "" && seedQuestionID != "" {
		// 即使暂时无法刷新异步质量投影，盲测提交仍保持成功。
		_ = h.seedRefresher.RefreshSeedObservation(r.Context(), user.TenantID, seedExamID, seedQuestionID, user.ID)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": map[string]any{
		"id": receipt.TaskID, "status": seedquality.PublicTaskStatus(receipt.Status), "revision": receipt.Revision,
	}})
	return true
}

func writeSeedHookError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, seedquality.ErrTaskMarked) {
		writeStoreError(w, r, ErrTaskMarked)
		return
	}
	if errors.Is(err, commandreceipt.ErrConflict) {
		writeStoreError(w, r, err)
		return
	}
	switch {
	case errors.Is(err, seedquality.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_resource_not_found", "review resource not found")
	case errors.Is(err, seedquality.ErrInvalidInput), errors.Is(err, seedquality.ErrContextUnavailable):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_task", "review task is invalid")
	case errors.Is(err, seedquality.ErrQualificationNeeded), errors.Is(err, seedquality.ErrSeedTaskForbidden):
		httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "current grader qualification is required")
	case errors.Is(err, seedquality.ErrGoldSetChanged), errors.Is(err, seedquality.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "review_task_conflict", "review task state changed; reload before continuing")
	case errors.Is(err, seedquality.ErrGoldSetMissing):
		httpx.Error(w, r, http.StatusConflict, "seed_gold_set_missing", "no approved blind samples are available; ask an administrator to confirm the sampling policy")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_operation_failed", "review operation failed")
	}
}
