package grading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// ScoringCoordinator 消费持久观察任务，并扫描已满足依赖的条目。浏览器退出不影响接续。
// 租约保护来源提交；供应商若没有幂等回执，网络断开仍可能产生未知外部调用，不能承诺恰好调用一次。
type ScoringCoordinator struct {
	store      *PostgresStore
	runtime    workerruntime.Store
	images     ScoringImageExecutor
	instanceID string
	timeout    time.Duration
}

// NewScoringCoordinator 仅组装执行器；Run 需由维护上下文调用，单题来源操作仍限定任务租户。
func NewScoringCoordinator(store *PostgresStore, runtime workerruntime.Store, images ScoringImageExecutor, timeout time.Duration) *ScoringCoordinator {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &ScoringCoordinator{store: store, runtime: runtime, images: images, instanceID: uuid.NewString(), timeout: timeout}
}

// Run 在进程生命周期内恢复队列和依赖，运行结束不会删除已保存的观察回执。
func (c *ScoringCoordinator) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.RunOnce(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce 每次最多领取一个观察任务，先保存观察回执，再接续独立题目；某题失败不阻止其余题恢复。
func (c *ScoringCoordinator) RunOnce(ctx context.Context) error {
	if c == nil || c.store == nil || c.runtime == nil || c.images == nil {
		return ErrInvalidInput
	}
	tasks, err := c.runtime.ClaimAcrossTenants(ctx, "", workerruntime.ClaimInput{QueueName: scoringCoordinatorQueue, WorkerService: "api-gateway-scoring", WorkerInstanceID: c.instanceID, Limit: 1, LeaseSeconds: int(c.timeout.Seconds()) + 60})
	if err != nil {
		return err
	}
	var first error
	for _, task := range tasks {
		if err = c.observe(ctx, task); err != nil {
			first = err
		}
	}
	if err = c.resumePending(ctx); first == nil {
		first = err
	}
	return first
}

func (c *ScoringCoordinator) observe(ctx context.Context, task workerruntime.Task) error {
	input, runID, actorID, err := c.store.loadScoringObservationInput(ctx, task)
	if err != nil {
		return c.fail(ctx, task, err)
	}
	executionCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	heartbeatCtx, stopHeartbeat := context.WithCancel(executionCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if _, beatErr := c.runtime.Heartbeat(heartbeatCtx, task.TenantID, task.ID, workerruntime.HeartbeatInput{LeaseToken: task.LeaseToken, WorkerService: "api-gateway-scoring", WorkerInstanceID: c.instanceID, State: workerruntime.StatusRunning, LeaseSeconds: int(c.timeout.Seconds()) + 60}); beatErr != nil {
					cancel()
					return
				}
			}
		}
	}()
	raw, err := c.images.Observe(executionCtx, task.TenantID, input)
	stopHeartbeat()
	<-done
	// 心跳已停止后再提交，避免完成后续租触发伪失败；存储仍重新检查有效租约和输入版本。
	if err == nil {
		err = c.store.completeScoringObservation(ctx, task, input, runID, actorID, raw)
	}
	if err != nil {
		return c.fail(ctx, task, err)
	}
	return nil
}

func scoringExecutionFailure(err error) (string, bool) {
	var classified interface{ ScoringFailure() (string, bool) }
	if errors.As(err, &classified) {
		return classified.ScoringFailure()
	}
	switch {
	case errors.Is(err, ErrInvalidInput):
		return "observation_contract_invalid", false
	case errors.Is(err, ErrRevisionConflict):
		return "input_version_changed", false
	case errors.Is(err, ErrInvalidTransition):
		return "scoring_run_inactive", false
	default:
		return "observation_temporarily_unavailable", true
	}
}

func (c *ScoringCoordinator) fail(ctx context.Context, task workerruntime.Task, cause error) error {
	code, retryable := scoringExecutionFailure(cause)
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, _ := task.Payload["scoring_run_id"].(string)
	if runID != "" {
		// 失败、完成和取消均先锁父运行，再锁 runtime，避免取消与失败互相等待。
		var runStatus string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, task.TenantID, runID).Scan(&runStatus); err != nil {
			return err
		}
	}
	failed, err := workerruntime.FailTaskInTx(ctx, tx, task.TenantID, task.ID, workerruntime.FailInput{LeaseToken: task.LeaseToken, Retryable: retryable, ErrorCode: code})
	if err != nil {
		return err
	}
	status, next := "failed", "retry"
	if failed.Status == workerruntime.StatusQueued {
		status = "retry_wait"
		next = "wait"
	} else if !retryable {
		status = "blocked"
		next = "check_configuration"
	}
	if code == "input_version_changed" {
		status = "superseded"
		next = "restart_scoring"
	}
	if code == "scoring_run_inactive" {
		status = "cancelled"
		next = "none"
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET processing_status=$3,reason_code=$4,retryable=$5,next_action=$6,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND observation_task_id=$2::uuid AND observation_json IS NULL AND processing_status NOT IN ('cancelled','superseded')`, task.TenantID, task.ID, status, code, retryable, next)
	if err != nil {
		return err
	}
	if runID != "" {
		if _, err = c.store.refreshScoringRunTx(ctx, tx, task.TenantID, runID); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return fmt.Errorf("scoring observation %s: %w", task.ID, cause)
}

type pendingScoringItem struct {
	tenantID, runID, segmentID, inputID, cropHash, snapshotID, actorID, route string
	processingStatus                                                          string
	observation                                                               json.RawMessage
}

// 成功建议或已阻塞条目缺正式任务时也进入恢复扫描；阻塞条目只补人工队列，不重试模型。
func (c *ScoringCoordinator) resumePending(ctx context.Context) error {
	rows, err := c.store.db.QueryContext(ctx, `SELECT item.tenant_id::text,item.scoring_run_id::text,item.answer_segment_id::text,item.input_version_id::text,iv.crop_sha256,item.assessment_snapshot_id::text,sr.started_by::text,item.route,item.processing_status,item.observation_json FROM scoring_run_item item JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id WHERE sr.deleted_at IS NULL AND sr.status IN ('queued','processing','needs_review','failed','completed') AND ((item.route IN ('observe_rule','rule_input','image_ai') AND item.processing_status IN ('waiting_dependency','queued','running','retry_wait')) OR (item.route='image_ai' AND item.processing_status='succeeded' AND item.decision_status='suggestion_ready' AND sr.status<>'completed' AND NOT EXISTS(SELECT 1 FROM review_task rt WHERE rt.tenant_id=item.tenant_id AND rt.answer_segment_id=item.answer_segment_id AND rt.deleted_at IS NULL AND rt.status IN ('pending','assigned','in_progress','returned','submitted','completed'))) OR (item.route IN ('observe_rule','rule_input','image_ai') AND item.processing_status='blocked' AND sr.status<>'completed' AND NOT EXISTS(SELECT 1 FROM review_task rt WHERE rt.tenant_id=item.tenant_id AND rt.answer_segment_id=item.answer_segment_id AND rt.deleted_at IS NULL AND rt.status IN ('pending','assigned','in_progress','returned','submitted','completed'))) OR (item.decision_status<>'human_confirmed' AND EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=item.tenant_id AND g.answer_segment_id=item.answer_segment_id AND g.source='human' AND g.is_current AND g.deleted_at IS NULL))) ORDER BY item.updated_at LIMIT 100`)
	if err != nil {
		return err
	}
	items := []pendingScoringItem{}
	for rows.Next() {
		var item pendingScoringItem
		var raw []byte
		if err = rows.Scan(&item.tenantID, &item.runID, &item.segmentID, &item.inputID, &item.cropHash, &item.snapshotID, &item.actorID, &item.route, &item.processingStatus, &raw); err != nil {
			rows.Close()
			return err
		}
		item.observation = raw
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var first error
	for _, item := range items {
		if item.processingStatus == "blocked" {
			// blocked 的自动工作仍保留原失败原因；补任务不能顺带调用配置尚未恢复的供应商。
			err = nil
		} else if item.route == scoringRouteImageAI && len(item.observation) > 0 {
			err = c.scheduleAI(ctx, item)
		} else if item.route == scoringRouteRuleInput || (item.route == scoringRouteObserveRule && len(item.observation) > 0) {
			err = c.store.ProcessRuleCandidates(ctx, item.tenantID, item.runID, item.actorID, NewEngine())
		}
		if err != nil && first == nil {
			first = err
		}
		if _, refreshErr := c.store.RefreshScoringRun(ctx, item.tenantID, item.runID); refreshErr != nil && first == nil {
			first = refreshErr
		}
	}
	return first
}

func (c *ScoringCoordinator) scheduleAI(ctx context.Context, item pendingScoringItem) error {
	// advisory lock 仅串行化持久任务的建立；模型计算由 subjective Worker 在独立租约里执行。
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	key := item.tenantID + ":" + item.runID + ":" + item.segmentID + ":scoring-ai"
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil || !locked {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
	var existing, status string
	err = conn.QueryRowContext(ctx, `SELECT COALESCE(item.subjective_run_id::text,''),sr.status FROM scoring_run_item item JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.answer_segment_id=$3::uuid`, item.tenantID, item.runID, item.segmentID).Scan(&existing, &status)
	if err != nil {
		return err
	}
	if existing != "" || status == "cancelling" || status == "cancelled" {
		return nil
	}
	runID, taskID, err := c.images.Schedule(ctx, item.tenantID, item.actorID, ScoringAIInput{ScoringRunID: item.runID, SegmentID: item.segmentID, InputVersionID: item.inputID, CropSHA256: item.cropHash, AssessmentSnapshotID: item.snapshotID, IdempotencyKey: "scoring-ai:" + item.runID + ":" + item.segmentID + ":" + item.inputID, Observation: item.observation})
	if err != nil {
		code, retryable := scoringExecutionFailure(err)
		// 确定性配置问题停在 blocked；瞬时排队错误交给恢复扫描，不重复执行已经持久化的主观运行。
		if !retryable {
			_, updateErr := conn.ExecContext(ctx, `UPDATE scoring_run_item SET processing_status='blocked',reason_code=$4,next_action='check_configuration',retryable=false,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND subjective_run_id IS NULL`, item.tenantID, item.runID, item.segmentID, code)
			if updateErr != nil {
				return updateErr
			}
		}
		return err
	}
	if runID == "" || taskID == "" {
		return ErrInvalidInput
	}
	_, err = conn.ExecContext(ctx, `UPDATE scoring_run_item SET subjective_run_id=$4::uuid,runtime_task_id=$5::uuid,stage='score',processing_status='queued',reason_code='ai_suggestion_pending',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND subjective_run_id IS NULL AND processing_status NOT IN ('cancelled','superseded')`, item.tenantID, item.runID, item.segmentID, runID, taskID)
	return err
}

// ResumeScoringRun 只唤醒当前冻结计划，不把新规则混入旧运行；失败重排另由显式 retry 命令负责。
func (s *PostgresStore) ResumeScoringRun(ctx context.Context, tenantID, runID string, retryFailed bool) (ScoringRun, int, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, 0, 0, err
	}
	defer tx.Rollback()
	run, err := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return run, 0, 0, err
	}
	if run.Status == "cancelling" || run.Status == "cancelled" {
		return run, 0, 0, ErrInvalidTransition
	}
	requeued, skipped := 0, 0
	{
		// 显式 resume 可在配置修正后唤醒 blocked；普通失败仅由 retry-failed 重排，成功任务不重建。
		rows, queryErr := tx.QueryContext(ctx, `SELECT DISTINCT wt.id::text FROM scoring_run_item item JOIN agent_worker_task wt ON wt.tenant_id=item.tenant_id AND wt.id=item.runtime_task_id JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.route IN ('observe_rule','image_ai') AND item.processing_status IN ('failed','blocked') AND ($3 OR item.processing_status='blocked') AND wt.status IN ('failed','dead_letter') AND seg.deleted_at IS NULL AND seg.crop_sha256=iv.crop_sha256 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.registration_run_id IS NOT DISTINCT FROM iv.registration_run_id AND NOT EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=item.tenant_id AND g.answer_segment_id=item.answer_segment_id AND g.source='human' AND g.is_current AND g.deleted_at IS NULL)`, tenantID, runID, retryFailed)
		if queryErr != nil {
			return run, 0, 0, queryErr
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return run, 0, 0, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return run, 0, 0, err
		}
		for _, id := range ids {
			if _, err = workerruntime.RequeueTaskInTx(ctx, tx, tenantID, id); err != nil {
				return run, 0, 0, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE subjective_grading_run ai SET status='queued',error_code='',completed_at=NULL,updated_at=now() FROM scoring_run_item item WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.runtime_task_id=$3::uuid AND ai.tenant_id=item.tenant_id AND ai.id=item.subjective_run_id AND ai.status IN ('failed','conflict') AND NOT EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=item.tenant_id AND g.answer_segment_id=item.answer_segment_id AND g.source='human' AND g.is_current AND g.deleted_at IS NULL)`, tenantID, runID, id); err != nil {
				return run, 0, 0, err
			}
			requeued++
		}
	}
	// 原始 crop 不再相同时要求建立新计划，不重新观察旧输入。
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item item SET processing_status='superseded',reason_code='input_version_changed',next_action='restart_scoring',revision=item.revision+1,updated_at=now() FROM answer_input_version iv,answer_segment seg WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id AND seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id AND (seg.deleted_at IS NOT NULL OR seg.crop_sha256<>iv.crop_sha256 OR seg.crop_file_asset_id<>iv.crop_file_asset_id OR seg.registration_run_id IS DISTINCT FROM iv.registration_run_id) AND item.processing_status NOT IN ('succeeded','cancelled','superseded')`, tenantID, runID)
	if err != nil {
		return run, 0, 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET processing_status='waiting_dependency',reason_code='',next_action='wait',retryable=false,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND processing_status='blocked' AND observation_json IS NOT NULL AND subjective_run_id IS NULL`, tenantID, runID)
	if err != nil {
		return run, 0, 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item item SET processing_status=CASE WHEN item.observation_json IS NOT NULL AND item.subjective_run_id IS NULL THEN 'waiting_dependency' ELSE 'queued' END,reason_code='',next_action='wait',retryable=false,revision=item.revision+1,updated_at=now() FROM agent_worker_task wt WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND wt.tenant_id=item.tenant_id AND wt.id=item.runtime_task_id AND wt.status='queued' AND item.processing_status IN ('failed','blocked','retry_wait')`, tenantID, runID)
	if err != nil {
		return run, 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return run, 0, 0, err
	}
	run, err = s.RefreshScoringRun(ctx, tenantID, runID)
	return run, requeued, skipped, err
}
