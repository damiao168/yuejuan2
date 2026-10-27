package capture

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func fileFailureRuntimeInput(input FileFailureInput) workerruntime.FailInput {
	return workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS}
}

// SubmitFileFailureCommand 沿用批次→文件→运行时锁顺序，失败状态与批次汇总一起提交。
func (s *PostgresStore) SubmitFileFailureCommand(ctx context.Context, tenantID, fileID string, input FileFailureInput, _ workerruntime.Store) (File, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, "", err
	}
	defer tx.Rollback()
	if err = lockFileBatchTx(ctx, tx, tenantID, fileID); err != nil {
		return File{}, "", err
	}
	file, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID))
	if err != nil {
		return File{}, "", err
	}
	if file.Status == "completed" {
		return File{}, "", ErrInvalidTransition
	}
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, fileFailureRuntimeInput(input))
	if err != nil {
		return File{}, "", err
	}
	if task.SourceType != "capture_file" || task.SourceID != fileID {
		return File{}, "", workerruntime.ErrConflict
	}
	file, err = s.applyFileFailureInTx(ctx, tx, tenantID, fileID, input.ErrorCode, task.Status == workerruntime.StatusQueued)
	if err != nil {
		return File{}, "", err
	}
	return file, task.Status, tx.Commit()
}

func (s *MemoryStore) SubmitFileFailureCommand(ctx context.Context, tenantID, fileID string, input FileFailureInput, runtime workerruntime.Store) (file File, status string, err error) {
	atomic, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return File{}, "", ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.files[fileID]
	if !ok || current.TenantID != tenantID {
		return File{}, "", ErrNotFound
	}
	if current.Status == "completed" {
		return File{}, "", ErrInvalidTransition
	}
	// 两个内存后端在同一锁范围完成；领域变更在所有可能失败的租约检查之后执行。
	err = atomic.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		task, e := tx.Get(ctx, tenantID, input.TaskID)
		if e != nil {
			return e
		}
		if task.SourceType != "capture_file" || task.SourceID != fileID {
			return workerruntime.ErrConflict
		}
		task, e = tx.Fail(ctx, tenantID, input.TaskID, fileFailureRuntimeInput(input))
		if e != nil {
			return e
		}
		file, e = s.applyFileFailureLocked(tenantID, fileID, input.ErrorCode, task.Status == workerruntime.StatusQueued)
		status = task.Status
		return e
	})
	return file, status, err
}
