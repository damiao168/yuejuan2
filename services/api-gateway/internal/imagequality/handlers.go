package imagequality

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store       Store
	submissions submission.Store
	files       files.Store
	audit       auth.Store
	runtime     workerruntime.Store
	captures    capture.Store
	coordinator TransactionalStore
}

func NewHandler(store Store, submissions submission.Store, fileStore files.Store, audit auth.Store, runtimes ...workerruntime.Store) *Handler {
	handler := &Handler{store: store, submissions: submissions, files: fileStore, audit: audit}
	if len(runtimes) > 0 {
		handler.runtime = runtimes[0]
	}
	return handler
}

type TransactionalHandlerDependencies struct {
	Coordinator TransactionalStore
	Submissions submission.Store
	Files       files.Store
	Audit       auth.Store
	Runtime     workerruntime.Store
	Captures    capture.Store
}

// NewTransactionalHandler constructs the production command boundary. Every
// dependency needed by the coordinated transaction is explicit; wrappers are
// accepted by capability and typed nil values are rejected.
func NewTransactionalHandler(dependencies TransactionalHandlerDependencies) (*Handler, error) {
	required := []struct {
		name  string
		value any
	}{
		{"coordinator", dependencies.Coordinator}, {"submissions", dependencies.Submissions},
		{"files", dependencies.Files}, {"audit", dependencies.Audit},
		{"runtime", dependencies.Runtime}, {"captures", dependencies.Captures},
	}
	for _, dependency := range required {
		if nilDependency(dependency.value) {
			return nil, errors.New("image quality transactional dependency " + dependency.name + " is required")
		}
	}
	return &Handler{
		store: dependencies.Coordinator, coordinator: dependencies.Coordinator,
		submissions: dependencies.Submissions, files: dependencies.Files, audit: dependencies.Audit,
		runtime: dependencies.Runtime, captures: dependencies.Captures,
	}, nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Func) && reflected.IsNil()
}

func (h *Handler) WithCaptureStore(store capture.Store) *Handler { h.captures = store; return h }

func (h *Handler) RunQualityCheck(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	submissionID := r.PathValue("id")
	item, err := h.submissions.Get(r.Context(), user.TenantID, submissionID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	pages := make([]PageSource, 0, len(item.Pages))
	for _, page := range item.Pages {
		asset, err := h.files.Get(r.Context(), user.TenantID, page.FileAssetID)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "source_file_asset_not_found", "submission page source file does not exist")
			return
		}
		pages = append(pages, PageSource{
			SubmissionPageID:  page.ID,
			PageNo:            page.PageNo,
			SourceFileAssetID: asset.ID,
			SourceSHA256:      asset.HashSHA256,
			DownloadURL:       "/api/v1/files/" + asset.ID + "/download",
		})
	}
	createInput := CreateRunsInput{
		SubmissionID: submissionID,
		Profile:      DefaultProfile(),
		Pages:        pages,
	}
	var runs []Run
	tasksCreatedAtomically := false
	if h.coordinator != nil {
		runs, err = h.coordinator.CreateRunsWithTasks(r.Context(), user.TenantID, user.ID, createInput)
		tasksCreatedAtomically = true
	} else {
		runs, err = h.store.CreateRuns(r.Context(), user.TenantID, createInput)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.runtime != nil && !tasksCreatedAtomically {
		for _, run := range runs {
			_, err := h.runtime.CreateTask(r.Context(), user.TenantID, user.ID, qualityRuntimeCreateInput(run))
			if err != nil {
				writeStoreError(w, r, err)
				return
			}
		}
	}
	h.auditAction(r, "image_quality.runs_created", "submission", submissionID, "create image quality runs")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"runs": runs})
}

func (h *Handler) ListPageRuns(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runs, err := h.store.ListRunsForPage(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Handler) OverridePageQuality(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.captures == nil {
		httpx.Error(w, r, http.StatusConflict, "capture_pipeline_unavailable", "capture pipeline is unavailable")
		return
	}
	var input submission.OverridePageQualityInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := h.captures.GetPageBySubmissionPageID(r.Context(), user.TenantID, r.PathValue("id")); err != nil {
		writeStoreError(w, r, err)
		return
	}
	page, err := h.submissions.OverridePageQuality(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err = h.captures.ApplyQualityOutcome(r.Context(), user.TenantID, page.ID, QualityPassed); err != nil {
		writeStoreError(w, r, err)
		return
	}
	runs, err := h.captures.QueueSubmissionPages(r.Context(), user.TenantID, page.SubmissionID, user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "image_quality.page_overridden", "submission_page", page.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": page, "registration_runs": runs})
}

// ClaimJobs 启用 Runtime 时先领取统一任务，再把租约同步到质量运行。
// 质量运行租约同步失败会尝试标记任务失败并跳过；心跳失败则直接返回错误。
func (h *Handler) ClaimJobs(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ClaimInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.runtime == nil {
		jobs, err := h.store.Claim(r.Context(), user.TenantID, input)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"jobs": jobs})
		return
	}
	tasks, err := h.runtime.Claim(r.Context(), user.TenantID, workerruntime.ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: input.WorkerInstanceID,
		Limit: input.Limit, LeaseSeconds: input.LeaseSeconds,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	jobs := make([]ClaimedJob, 0, len(tasks))
	for _, task := range tasks {
		if task.LeaseExpiresAt == nil {
			continue
		}
		run, leaseErr := h.store.LeaseRun(r.Context(), user.TenantID, task.SourceID, input.WorkerInstanceID, task.LeaseToken, *task.LeaseExpiresAt, task.AttemptCount)
		if leaseErr != nil {
			_, _ = h.runtime.Fail(r.Context(), user.TenantID, task.ID, workerruntime.FailInput{
				LeaseToken: task.LeaseToken, Retryable: true, ErrorCode: "source_lease_failed", ErrorDetail: map[string]any{"source_type": task.SourceType},
			})
			continue
		}
		submissionItem, submissionErr := h.submissions.Get(r.Context(), user.TenantID, run.SubmissionID)
		if submissionErr != nil {
			_, _ = h.runtime.Fail(r.Context(), user.TenantID, task.ID, workerruntime.FailInput{
				LeaseToken: task.LeaseToken, Retryable: false, ErrorCode: "quality_submission_not_found",
			})
			continue
		}
		heartbeatTask, heartbeatErr := h.runtime.Heartbeat(r.Context(), user.TenantID, task.ID, workerruntime.HeartbeatInput{
			LeaseToken: task.LeaseToken, WorkerService: "image-quality-worker", WorkerInstanceID: input.WorkerInstanceID, State: workerruntime.StatusRunning,
			LeaseSeconds: input.LeaseSeconds,
		})
		if heartbeatErr != nil {
			writeStoreError(w, r, heartbeatErr)
			return
		}
		if heartbeatTask.LeaseExpiresAt == nil {
			writeStoreError(w, r, workerruntime.ErrInvalidTransition)
			return
		}
		run, leaseErr = h.store.RenewLease(r.Context(), user.TenantID, run.ID, input.WorkerInstanceID, task.LeaseToken, *heartbeatTask.LeaseExpiresAt, task.AttemptCount)
		if leaseErr != nil {
			_, _ = h.runtime.Fail(r.Context(), user.TenantID, task.ID, workerruntime.FailInput{
				LeaseToken: task.LeaseToken, Retryable: true, ErrorCode: "source_lease_renewal_failed", ErrorDetail: map[string]any{"source_type": task.SourceType},
			})
			continue
		}
		jobs = append(jobs, ClaimedJob{
			RuntimeTaskID: task.ID, ExamID: submissionItem.ExamID, RunID: run.ID, SubmissionID: run.SubmissionID, SubmissionPageID: run.SubmissionPageID,
			PageNo: run.PageNo, SourceFileAssetID: run.SourceFileAssetID, SourceSHA256: run.SourceSHA256,
			DownloadURL: "/api/v1/files/" + run.SourceFileAssetID + "/download", LeaseToken: task.LeaseToken,
			LeaseExpiresAt: *heartbeatTask.LeaseExpiresAt, AttemptNo: task.AttemptCount,
			Profile: Profile{Name: run.ProfileName, Version: run.ProfileVersion, ConfigHash: run.ProfileConfigHash, MetricSchemaVersion: run.MetricSchemaVersion, ReportSchemaVersion: run.ReportSchemaVersion},
		})
		h.auditAction(r, "worker.task_claimed", "agent_worker_task", task.ID, "claim image quality runtime task")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *Handler) CreateNormalizedAssetSlot(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	run, err := h.store.GetRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	var input struct {
		LeaseToken  string `json:"lease_token"`
		ContentType string `json:"content_type"`
		ByteSize    int64  `json:"byte_size"`
		SHA256      string `json:"sha256"`
		PixelWidth  int    `json:"pixel_width"`
		PixelHeight int    `json:"pixel_height"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.LeaseToken) == "" || input.LeaseToken != run.LeaseToken || run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Before(time.Now().UTC()) {
		writeStoreError(w, r, ErrLeaseMismatch)
		return
	}
	if input.ContentType != "image/png" || strings.TrimSpace(input.SHA256) == "" || input.ByteSize <= 0 || input.PixelWidth <= 0 || input.PixelHeight <= 0 {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"upload_url":      "/api/v1/files",
		"upload_method":   "POST",
		"expires_at":      time.Now().UTC().Add(5 * time.Minute),
		"expected_sha256": input.SHA256,
	})
}

// 结果接口只接受当前租约和尝试次数，成功后才把标准化文件和质量结论应用到答题页。
func (h *Handler) SubmitResult(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runID := r.PathValue("runId")
	var input ResultInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateResultInput(input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if strings.TrimSpace(input.NormalizedFileAssetID) != "" {
		asset, err := h.files.Get(r.Context(), user.TenantID, input.NormalizedFileAssetID)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "normalized_file_asset_not_found", "normalized_file_asset_id does not exist for current tenant")
			return
		}
		if asset.ContentType != "image/png" {
			httpx.Error(w, r, http.StatusBadRequest, "normalized_file_asset_invalid", "normalized file asset must be image/png")
			return
		}
	}
	if h.coordinator != nil {
		run, err := h.coordinator.SubmitResultCommand(r.Context(), user.TenantID, user.ID, runID, input)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		h.auditAction(r, "image_quality.result_submitted", "image_quality_run", run.ID, "submit image quality result")
		httpx.JSON(w, http.StatusOK, map[string]any{"run": run})
		return
	}
	run, err := h.store.CompleteRun(r.Context(), user.TenantID, runID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if run.ProcessingStatus == ProcessingCompleted {
		_, err := h.submissions.ApplyPageQualityResult(r.Context(), user.TenantID, submission.ApplyPageQualityInput{
			SubmissionID:          run.SubmissionID,
			PageID:                run.SubmissionPageID,
			LatestQualityRunID:    run.ID,
			NormalizedFileAssetID: run.NormalizedFileAssetID,
			QualityStatus:         run.QualityStatus,
			QualityIssues:         toSubmissionIssues(run.QualityIssues),
		})
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		if h.captures != nil {
			captureLinked := true
			if err = h.captures.ApplyQualityOutcome(r.Context(), user.TenantID, run.SubmissionPageID, run.QualityStatus); errors.Is(err, capture.ErrNotFound) {
				captureLinked = false
			} else if err != nil {
				writeStoreError(w, r, err)
				return
			}
			if captureLinked && run.QualityStatus == QualityPassed {
				if _, err = h.captures.QueueSubmissionPages(r.Context(), user.TenantID, run.SubmissionID, user.ID); err != nil && !errors.Is(err, capture.ErrInvalidTransition) {
					writeStoreError(w, r, err)
					return
				}
			}
		}
	}
	if h.runtime != nil {
		task, taskErr := h.runtime.GetBySource(r.Context(), user.TenantID, "image_quality_run", run.ID)
		if taskErr != nil {
			writeStoreError(w, r, taskErr)
			return
		}
		if run.ProcessingStatus == ProcessingCompleted {
			_, taskErr = h.runtime.Complete(r.Context(), user.TenantID, task.ID, workerruntime.CompleteInput{
				LeaseToken: input.LeaseToken, ResultSchemaVersion: "image-quality-result.v1", DurationMS: input.DurationMS,
				Result: map[string]any{"quality_run_id": run.ID, "normalized_file_asset_id": run.NormalizedFileAssetID, "quality_status": run.QualityStatus, "result_version": run.ResultVersion},
			})
		} else {
			errorCode := run.ErrorCode
			if errorCode == "" {
				errorCode = "image_quality_processing_failed"
			}
			_, taskErr = h.runtime.Fail(r.Context(), user.TenantID, task.ID, workerruntime.FailInput{
				LeaseToken: input.LeaseToken, Retryable: run.ProcessingStatus == ProcessingRetryableError,
				ErrorCode: errorCode, ErrorDetail: run.ErrorDetail, DurationMS: input.DurationMS,
			})
		}
		if taskErr != nil {
			writeStoreError(w, r, taskErr)
			return
		}
	}
	h.auditAction(r, "image_quality.result_submitted", "image_quality_run", run.ID, "submit image quality result")
	httpx.JSON(w, http.StatusOK, map[string]any{"run": run})
}

func toSubmissionIssues(issues []Issue) []submission.QualityIssue {
	out := make([]submission.QualityIssue, 0, len(issues))
	for _, issue := range issues {
		out = append(out, submission.QualityIssue{Code: issue.Code})
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "json body is required")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one json object")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, submission.ErrNotFound), errors.Is(err, files.ErrNotFound), errors.Is(err, capture.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "image_quality_run_not_found", "image quality run not found")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, submission.ErrInvalidInput), errors.Is(err, capture.ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_image_quality_input", "image quality input is invalid")
	case errors.Is(err, ErrLeaseExpired):
		httpx.Error(w, r, http.StatusConflict, "image_quality_lease_expired", "image quality lease expired")
	case errors.Is(err, ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "image_quality_lease_mismatch", "image quality lease mismatch")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "image_quality_result_conflict", "image quality result conflicts with existing result")
	case errors.Is(err, ErrInvalidTransition), errors.Is(err, submission.ErrInvalidTransition), errors.Is(err, submission.ErrSubmissionLocked), errors.Is(err, capture.ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_image_quality_transition", "image quality status transition is not allowed")
	case errors.Is(err, workerruntime.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "worker_task_not_found", "worker task not found")
	case errors.Is(err, workerruntime.ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_worker_task_input", "worker task input is invalid")
	case errors.Is(err, workerruntime.ErrLeaseExpired):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_expired", "worker task lease expired")
	case errors.Is(err, workerruntime.ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_mismatch", "worker task lease mismatch")
	case errors.Is(err, workerruntime.ErrConflict), errors.Is(err, workerruntime.ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "worker_task_conflict", "worker task operation conflicts with current state")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "image_quality_operation_failed", "image quality operation failed")
	}
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
