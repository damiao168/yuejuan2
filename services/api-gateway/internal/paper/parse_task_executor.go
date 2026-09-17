package paper

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

type paperImportParseInputStore interface {
	LoadPaperImportParseRunInput(context.Context, string, string) (PaperImportRunBinding, error)
}

type paperImportParseCommitter interface {
	CompletePaperImportParseTask(context.Context, string, string, string, PaperImportRunBinding, PaperImportParseResult, int) (PaperImportJob, error)
	FailPaperImportRuntime(context.Context, string, string, PaperImportRuntimeFailure) error
}

type paperImportDispatchStore interface {
	ReconcileExpiredPaperImportTask(context.Context) (bool, error)
	ClaimPendingPaperImportDispatch(context.Context, string, time.Duration) (PaperImportJob, bool, error)
	FailPendingPaperImportDispatch(context.Context, PaperImportJob, string, string) error
}

const parseFinalizationTimeout = 10 * time.Second
const parseHeartbeatInterval = 5 * time.Second

type ParseTaskExecutor struct {
	service           *DocumentImportService
	inputs            paperImportParseInputStore
	committer         paperImportParseCommitter
	runtime           workerruntime.Store
	instanceID        string
	leaseSeconds      int
	executionTimeout  time.Duration
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	dispatches        paperImportDispatchStore
}

type parseTaskHeartbeat struct {
	runtime         workerruntime.Store
	task            workerruntime.Task
	workerInstance  string
	leaseSeconds    int
	interval        time.Duration
	cancelExecution context.CancelFunc

	mu       sync.Mutex
	callMu   sync.Mutex
	progress map[string]any
	eventSeq int
	err      error
	stop     context.CancelFunc
	done     chan struct{}
}

func newParseTaskHeartbeat(runtime workerruntime.Store, task workerruntime.Task, workerInstance string, leaseSeconds int, interval time.Duration, cancelExecution context.CancelFunc) *parseTaskHeartbeat {
	return &parseTaskHeartbeat{
		runtime: runtime, task: task, workerInstance: workerInstance, leaseSeconds: leaseSeconds,
		interval: interval, cancelExecution: cancelExecution, done: make(chan struct{}),
	}
}

func (h *parseTaskHeartbeat) setProgress(ctx context.Context, phase string, completed, total int, route, message string) error {
	if completed < 0 || total < 0 || (total > 0 && completed > total) {
		return errors.New("invalid paper parse progress")
	}
	now := time.Now().UTC()
	h.mu.Lock()
	h.eventSeq++
	h.progress = map[string]any{
		"stage":               "paper_parse",
		"phase":               phase,
		"completed":           completed,
		"total":               total,
		"unit":                "parse_chunk",
		"event_seq":           h.eventSeq,
		"progress_changed_at": now.Format(time.RFC3339Nano),
		"message":             message,
	}
	if route != "" {
		h.progress["parse_route"] = route
	}
	h.mu.Unlock()
	return h.beat(ctx)
}

func (h *parseTaskHeartbeat) beat(ctx context.Context) error {
	h.callMu.Lock()
	defer h.callMu.Unlock()
	h.mu.Lock()
	if h.err != nil {
		err := h.err
		h.mu.Unlock()
		return err
	}
	progress := make(map[string]any, len(h.progress))
	for key, value := range h.progress {
		progress[key] = value
	}
	h.mu.Unlock()
	_, err := h.runtime.Heartbeat(ctx, h.task.TenantID, h.task.ID, workerruntime.HeartbeatInput{
		LeaseToken: h.task.LeaseToken, WorkerService: "api-gateway-paper-parser", WorkerInstanceID: h.workerInstance,
		State: workerruntime.StatusRunning, Progress: progress, LeaseSeconds: h.leaseSeconds,
	})
	if err != nil {
		h.mu.Lock()
		if h.err == nil {
			h.err = err
			h.cancelExecution()
		}
		h.mu.Unlock()
	}
	return err
}

func (h *parseTaskHeartbeat) start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	h.stop = cancel
	go func() {
		defer close(h.done)
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if h.beat(loopCtx) != nil {
					return
				}
			}
		}
	}()
}

func (h *parseTaskHeartbeat) close() error {
	if h.stop != nil {
		h.stop()
		<-h.done
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// ParseExecutionError keeps operational correlation data without exposing the
// lease token. Unwrap preserves the domain error used by retry decisions/tests.
type ParseExecutionError struct {
	Err             error
	ImportID        string
	RunID           string
	Generation      int64
	TaskID          string
	Attempt         int
	ErrorCode       string
	LeaseValidation string
}

func (e *ParseExecutionError) Error() string { return e.Err.Error() }
func (e *ParseExecutionError) Unwrap() error { return e.Err }

func executionError(err error, task workerruntime.Task, binding PaperImportRunBinding, errorCode string, leaseValidation string) error {
	if err == nil {
		return nil
	}
	return &ParseExecutionError{
		Err: err, ImportID: binding.ImportID, RunID: binding.RunID, Generation: binding.Generation,
		TaskID: task.ID, Attempt: task.AttemptCount, ErrorCode: errorCode, LeaseValidation: leaseValidation,
	}
}

func parseCommitErrorCode(err error) string {
	switch {
	case errors.Is(err, workerruntime.ErrLeaseExpired):
		return "paper_import_lease_expired"
	case errors.Is(err, workerruntime.ErrLeaseMismatch):
		return "paper_import_lease_mismatch"
	case errors.Is(err, ErrConflict):
		return "paper_import_version_or_result_conflict"
	default:
		return "paper_import_result_commit_failed"
	}
}

func NewParseTaskExecutor(service *DocumentImportService, runtime workerruntime.Store, timeout time.Duration) (*ParseTaskExecutor, error) {
	if service == nil || runtime == nil {
		return nil, errors.New("paper parse executor dependencies are required")
	}
	if service.client != nil && timeout < service.client.Timeout {
		timeout = service.client.Timeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if timeout > 59*time.Minute {
		return nil, errors.New("paper parse timeout must leave at least one minute of lease safety margin")
	}
	inputs, ok := service.store.(paperImportParseInputStore)
	if !ok {
		return nil, errors.New("paper parse input store is unavailable")
	}
	committer, ok := service.store.(paperImportParseCommitter)
	if !ok {
		return nil, errors.New("paper parse transactional committer is unavailable")
	}
	dispatches, ok := service.store.(paperImportDispatchStore)
	if !ok {
		return nil, errors.New("paper import durable dispatch reconciler is unavailable")
	}
	leaseSeconds := int((timeout + time.Minute).Seconds())
	if leaseSeconds < 300 {
		leaseSeconds = 300
	}
	if leaseSeconds > 3600 {
		leaseSeconds = 3600
	}
	executor := &ParseTaskExecutor{
		service: service, inputs: inputs, committer: committer, runtime: runtime,
		instanceID:   fmt.Sprintf("api-gateway-%d", time.Now().UTC().UnixNano()),
		leaseSeconds: leaseSeconds, pollInterval: time.Second, heartbeatInterval: parseHeartbeatInterval,
		executionTimeout: timeout,
	}
	executor.dispatches = dispatches
	return executor, nil
}

func (e *ParseTaskExecutor) RunOnce(ctx context.Context) (bool, error) {
	if e.dispatches != nil {
		if repaired, err := e.dispatches.ReconcileExpiredPaperImportTask(ctx); repaired || err != nil {
			return repaired, err
		}
		owner := e.instanceID + ":" + uuid.NewString()
		job, ok, claimErr := e.dispatches.ClaimPendingPaperImportDispatch(ctx, owner, time.Duration(e.leaseSeconds)*time.Second)
		if claimErr != nil {
			return false, claimErr
		}
		if ok {
			executionCtx, cancelExecution := e.executionContext(ctx)
			_, dispatchErr := e.service.processSources(executionCtx, job.TenantID, job.CreatedBy, job)
			cancelExecution()
			if dispatchErr != nil {
				finalizeCtx, cancelFinalize := finalizationContext(ctx)
				persistenceErr := e.dispatches.FailPendingPaperImportDispatch(finalizeCtx, job, owner, dispatchErr.Error())
				cancelFinalize()
				if persistenceErr != nil {
					dispatchErr = errors.Join(dispatchErr, fmt.Errorf("persist dispatch failure: %w", persistenceErr))
				}
			}
			if dispatchErr != nil {
				return true, &ParseExecutionError{Err: dispatchErr, ImportID: job.ID, RunID: job.RunID, Generation: job.Generation, ErrorCode: "paper_import_dispatch_failed"}
			}
			return true, nil
		}
	}
	tasks, err := e.runtime.ClaimAcrossTenants(ctx, "", workerruntime.ClaimInput{
		QueueName: "paper-parse", WorkerService: "api-gateway-paper-parser", WorkerInstanceID: e.instanceID,
		Limit: 1, LeaseSeconds: e.leaseSeconds,
	})
	if err != nil || len(tasks) == 0 {
		return false, err
	}
	task := tasks[0]
	started := time.Now()
	if task.TaskType != "paper_parse" || task.SourceType != "paper_import_parse" {
		_, failErr := e.runtime.Fail(ctx, task.TenantID, task.ID, workerruntime.FailInput{
			LeaseToken: task.LeaseToken, Retryable: false, ErrorCode: "invalid_paper_parse_task",
		})
		return true, failErr
	}
	executionCtx, cancelExecution := e.executionContext(ctx)
	heartbeat := newParseTaskHeartbeat(e.runtime, task, e.instanceID, e.leaseSeconds, e.heartbeatInterval, cancelExecution)
	if err = heartbeat.setProgress(executionCtx, "preparing", 0, 0, "", "正在读取本次识别结果"); err != nil {
		cancelExecution()
		return true, executionError(err, task, PaperImportRunBinding{}, parseCommitErrorCode(err), "rejected")
	}
	heartbeat.start(executionCtx)
	binding, err := e.inputs.LoadPaperImportParseRunInput(executionCtx, task.TenantID, task.SourceID)
	if binding.ImportID == "" {
		if value, ok := task.Payload["paper_import_id"].(string); ok {
			binding.ImportID = value
		}
	}
	var parsed documentParseResponse
	if err == nil {
		parsed, err = e.service.computeParseRun(executionCtx, task.TenantID, binding, func(progress map[string]any) error {
			phase, completed, total, route := normalizedParseProgress(progress)
			return heartbeat.setProgress(executionCtx, phase, completed, total, route, parseProgressMessage(phase, completed, total, route))
		})
	}
	if err == nil {
		err = heartbeat.setProgress(executionCtx, "persisting", 0, 0, "", "解析结果已校验，正在写入待核对区")
	}
	heartbeatErr := heartbeat.close()
	cancelExecution()
	if err == nil && heartbeatErr != nil {
		err = heartbeatErr
	}
	duration := int(time.Since(started).Milliseconds())
	if err != nil {
		retryable := task.AttemptCount < task.MaxAttempts && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalidInput)
		finalizeCtx, cancelFinalize := finalizationContext(ctx)
		failErr := e.committer.FailPaperImportRuntime(finalizeCtx, task.TenantID, binding.ImportID, PaperImportRuntimeFailure{TaskID: task.ID, LeaseToken: task.LeaseToken, Retryable: retryable, ErrorCode: "paper_parse_failed", ErrorDetail: map[string]any{"message": err.Error()}, DurationMS: duration})
		cancelFinalize()
		if failErr != nil {
			return true, executionError(failErr, task, binding, "paper_import_failure_commit_failed", "accepted")
		}
		return true, executionError(err, task, binding, "paper_parse_failed", "accepted")
	}
	finalizeCtx, cancelFinalize := finalizationContext(ctx)
	_, err = e.committer.CompletePaperImportParseTask(finalizeCtx, task.TenantID, task.ID, task.LeaseToken, binding, parsed, duration)
	cancelFinalize()
	leaseValidation := "accepted"
	if errors.Is(err, workerruntime.ErrLeaseExpired) || errors.Is(err, workerruntime.ErrLeaseMismatch) {
		leaseValidation = "rejected"
	}
	return true, executionError(err, task, binding, parseCommitErrorCode(err), leaseValidation)
}

func normalizedParseProgress(progress map[string]any) (phase string, completed, total int, route string) {
	phase, _ = progress["phase"].(string)
	route, _ = progress["route"].(string)
	allowedPhases := map[string]bool{"routing": true, "deterministic_structuring": true, "model_request": true, "model_response_validation": true}
	allowedRoutes := map[string]bool{"pending": true, "unrelated_guard": true, "anchored": true, "full_model": true, "compact_model": true, "visual_model": true}
	if !allowedPhases[phase] {
		phase = "structuring"
	}
	if !allowedRoutes[route] {
		route = ""
	}
	completed, completedOK := boundedJSONInteger(progress["completed"])
	total, totalOK := boundedJSONInteger(progress["total"])
	if !completedOK || !totalOK || total == 0 || completed > total {
		completed = 0
		total = 0
	}
	return phase, completed, total, route
}

func boundedJSONInteger(value any) (int, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case int:
		number = float64(typed)
	default:
		return 0, false
	}
	if number < 0 || number > 100_000 || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

func parseProgressMessage(phase string, completed, total int, route string) string {
	switch phase {
	case "routing":
		return "正在根据版面锚点选择解析路径"
	case "deterministic_structuring":
		if completed == total && total > 0 {
			return "确定性结构重建完成，未调用大模型"
		}
		return "正在按可靠题号和答案锚点重建结构"
	case "model_request":
		if route == "visual_model" {
			return "多模态模型正在读取原始试卷页面"
		}
		if total > 0 {
			return fmt.Sprintf("大模型已完成 %d/%d 个实际解析块", completed, total)
		}
		return "大模型正在处理需消歧的内容"
	case "model_response_validation":
		if route == "visual_model" {
			return "原图识别已返回，正在与 OCR 结果交叉校验"
		}
		return "模型解析块已返回，正在校验来源并合并"
	default:
		if route == "" {
			return "解析服务正在处理"
		}
		return "解析服务正在处理已选择的解析路径"
	}
}

func (e *ParseTaskExecutor) executionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.executionTimeout > 0 {
		return context.WithTimeout(ctx, e.executionTimeout)
	}
	return context.WithCancel(ctx)
}

func finalizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), parseFinalizationTimeout)
}

func (e *ParseTaskExecutor) Run(ctx context.Context, onError func(error)) {
	for {
		worked, err := e.RunOnce(ctx)
		if err != nil && onError != nil && !errors.Is(err, context.Canceled) {
			onError(err)
		}
		if ctx.Err() != nil {
			return
		}
		if worked {
			continue
		}
		timer := time.NewTimer(e.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
