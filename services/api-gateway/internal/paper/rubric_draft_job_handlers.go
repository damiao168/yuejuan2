package paper

import (
	"context"
	"errors"
	"net/http"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

func (h *Handler) WithRubricDraftJobs(jobs *RubricDraftJobs) *Handler {
	h.rubricDraftJobs = jobs
	return h
}

func (h *Handler) rubricJobScope(w http.ResponseWriter, r *http.Request) (auth.AccessScope, bool) {
	if h.rubricDraftJobs == nil {
		httpx.Error(w, r, 503, "rubric_draft_jobs_unavailable", "分值建议任务服务未配置")
		return auth.AccessScope{}, false
	}
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		httpx.Error(w, r, 403, "access_scope_forbidden", "考试不在当前访问范围内")
		return auth.AccessScope{}, false
	}
	return scope, true
}

func (h *Handler) CreateQuestionRubricDraftJob(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.rubricJobScope(w, r)
	if !ok {
		return
	}
	var input CreateRubricDraftJobInput
	if !decodeJSON(w, r, &input) {
		return
	}
	job, err := h.rubricDraftJobs.Create(r.Context(), scope, r.PathValue("examId"), r.PathValue("questionId"), input)
	if err != nil {
		writeRubricDraftJobError(w, r, err)
		return
	}
	job.Result = nil
	httpx.JSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (h *Handler) ListQuestionRubricDraftJobs(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.rubricJobScope(w, r)
	if !ok {
		return
	}
	jobs, err := h.rubricDraftJobs.store.listRubricDraftJobs(r.Context(), scope.TenantID, scope.ActorID, r.PathValue("examId"))
	if err != nil {
		writeRubricDraftJobError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"jobs": jobs, "concurrency": RubricDraftConcurrency})
}

// 读取建议时重新核对来源，后台成功之后修改答案也不能采用旧结果。
func (h *Handler) GetQuestionRubricDraftJob(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.rubricJobScope(w, r)
	if !ok {
		return
	}
	job, err := h.rubricDraftJobs.Result(r.Context(), scope.TenantID, scope.ActorID, r.PathValue("examId"), r.PathValue("jobId"))
	if err != nil {
		writeRubricDraftJobError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"job": job})
}

func (h *Handler) CancelQuestionRubricDraftJob(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.rubricJobScope(w, r)
	if !ok {
		return
	}
	job, err := h.rubricDraftJobs.store.cancelRubricDraftJob(r.Context(), scope.TenantID, scope.ActorID, r.PathValue("examId"), r.PathValue("jobId"))
	if err != nil {
		writeRubricDraftJobError(w, r, err)
		return
	}
	job.Result = nil
	httpx.JSON(w, http.StatusOK, map[string]any{"job": job})
}

func writeRubricDraftJobError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrRubricDraftQueueFull) {
		httpx.Error(w, r, 429, "rubric_draft_queue_full", "已有 20 个建议等待生成，请稍后再试")
		return
	}
	if errors.Is(err, ErrConflict) {
		httpx.Error(w, r, 409, "rubric_draft_job_conflict", "题目内容已变更或已有生成任务，请刷新并核对任务")
		return
	}
	if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRubricLocked) || errors.Is(err, ErrExamFrozen) {
		writeStoreError(w, r, err)
		return
	}
	httpx.Error(w, r, 503, "rubric_draft_job_unavailable", "建议任务暂不可用，请稍后重试")
}

func (h *Handler) waitQuestionRubricDraftJob(w http.ResponseWriter, r *http.Request, scope auth.AccessScope, input QuestionMathRubricDraftInput) {
	s := h.rubricDraftJobs
	job, err := s.Create(r.Context(), scope, r.PathValue("examId"), r.PathValue("questionId"), CreateRubricDraftJobInput{QuestionMathRubricDraftInput: input, RequestID: uuid.NewString()})
	if err != nil {
		writeRubricDraftJobError(w, r, err)
		return
	}
	for {
		current, readErr := s.Result(r.Context(), scope.TenantID, scope.ActorID, job.ExamID, job.ID)
		if readErr != nil {
			writeRubricDraftJobError(w, r, readErr)
			return
		}
		if current.Status == "succeeded" {
			httpx.JSON(w, http.StatusOK, current.Result)
			return
		}
		if !rubricDraftActive(current.Status) {
			writeRubricDraftJobError(w, r, ErrConflict)
			return
		}
		if current.Status == "queued" {
			// 兼容内存装配及旧同步入口；只领取本次已授权的任务，计数仍跨副本、跨租户共享。
			claimCtx, cancel := context.WithTimeout(db.WithTenantMaintenance(context.Background()), 5*time.Second)
			record, claimErr := s.store.claimRubricDraftJob(claimCtx, s.timeout+time.Minute, job.ID)
			cancel()
			if claimErr != nil {
				writeRubricDraftJobError(w, r, claimErr)
				return
			}
			if record != nil {
				go s.execute(context.Background(), *record, nil)
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
