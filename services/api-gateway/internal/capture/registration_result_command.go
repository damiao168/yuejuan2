package capture

import (
	"context"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 回执绑定全部领域结果，包括矩阵、校验报告与切片；租约和耗时属于本次提交而不改变产物身份。
func registrationCompleteInput(input RegistrationResultInput) workerruntime.CompleteInput {
	result := input
	result.TaskID, result.LeaseToken, result.DurationMS = "", "", 0
	return workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-registration-result-v1", Result: map[string]any{"registration": result}, DurationMS: input.DurationMS}
}

func correctionCompleteInput(input CorrectionPreviewResultInput) workerruntime.CompleteInput {
	result := input
	result.TaskID, result.LeaseToken, result.DurationMS = "", "", 0
	return workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-registration-correction-result-v1", Result: map[string]any{"correction": result}, DurationMS: input.DurationMS}
}

// SubmitRegistrationResultCommand 按配准运行→任务取锁，领域写入与任务回执由同一事务提交。
// 即使物化期间租约到期，最终完成检查也会拒绝并回滚切片、页面状态与批次统计。
func (s *PostgresStore) SubmitRegistrationResultCommand(ctx context.Context, tenantID, runID string, input RegistrationResultInput, _ workerruntime.Store) (RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationRun{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return RegistrationRun{}, err
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, input.TaskID, input.LeaseToken)
	if err != nil {
		return RegistrationRun{}, err
	}
	if task.SourceType != "page_registration_run" || task.SourceID != runID || current.RuntimeTaskID != task.ID {
		return RegistrationRun{}, workerruntime.ErrConflict
	}
	if task.Status != workerruntime.StatusSucceeded {
		if current.ProcessingStatus == "completed" {
			return RegistrationRun{}, ErrConflict
		}
		current, err = s.applyRegistrationResultInTx(ctx, tx, tenantID, runID, input)
		if err != nil {
			return RegistrationRun{}, err
		}
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, registrationCompleteInput(input)); err != nil {
		return RegistrationRun{}, err
	}
	return current, tx.Commit()
}

// SubmitRegistrationCorrectionResultCommand 先锁修正记录再锁任务；拒绝旧任务写入新一轮预览。
// 成功任务只核对原始完整结果指纹，不能借重放改变已经审阅或应用的预览。
func (s *PostgresStore) SubmitRegistrationCorrectionResultCommand(ctx context.Context, tenantID, correctionID string, input CorrectionPreviewResultInput, _ workerruntime.Store) (RegistrationCorrection, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `SELECT `+correctionColumns+` FROM page_registration_correction WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, correctionID))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, input.TaskID, input.LeaseToken)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	if task.SourceType != "page_registration_correction" || task.SourceID != correctionID || current.RuntimeTaskID != task.ID {
		return RegistrationCorrection{}, workerruntime.ErrConflict
	}
	if task.Status != workerruntime.StatusSucceeded {
		if current.Status == "preview_ready" {
			return RegistrationCorrection{}, ErrConflict
		}
		current, err = s.applyRegistrationCorrectionPreviewInTx(ctx, tx, tenantID, correctionID, input)
		if err != nil {
			return RegistrationCorrection{}, err
		}
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, correctionCompleteInput(input)); err != nil {
		return RegistrationCorrection{}, err
	}
	return current, tx.Commit()
}

// 内存后端在领域锁内进入运行时原子操作，错误或 panic 同时恢复两边的快照。
func (s *MemoryStore) SubmitRegistrationResultCommand(ctx context.Context, tenantID, runID string, input RegistrationResultInput, runtime workerruntime.Store) (out RegistrationRun, err error) {
	atomic, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return RegistrationRun{}, ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before, err := json.Marshal(s.registrations)
	if err != nil {
		return RegistrationRun{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = json.Unmarshal(before, &s.registrations)
		}
	}()
	err = atomic.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		current, exists := s.registrations[runID]
		if !exists {
			return ErrNotFound
		}
		task, e := tx.LockTaskForCompletion(ctx, tenantID, input.TaskID, input.LeaseToken)
		if e != nil {
			return e
		}
		if task.SourceType != "page_registration_run" || task.SourceID != runID || current.RuntimeTaskID != task.ID {
			return workerruntime.ErrConflict
		}
		if task.Status != workerruntime.StatusSucceeded {
			if current.ProcessingStatus == "completed" {
				return ErrConflict
			}
			if current.ProcessingStatus != "processing" && current.ProcessingStatus != "retryable_error" {
				return ErrInvalidTransition
			}
			current, e = s.applyRegistrationResultLocked(tenantID, runID, input)
			if e != nil {
				return e
			}
		}
		if _, e = tx.Complete(ctx, tenantID, input.TaskID, registrationCompleteInput(input)); e != nil {
			return e
		}
		out = current
		return nil
	})
	if err != nil {
		return RegistrationRun{}, err
	}
	committed = true
	return out, nil
}

// 内存存储没有修正预览领域模型；继续拒绝该操作，不能只完成任务回执。
func (s *MemoryStore) SubmitRegistrationCorrectionResultCommand(context.Context, string, string, CorrectionPreviewResultInput, workerruntime.Store) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}
