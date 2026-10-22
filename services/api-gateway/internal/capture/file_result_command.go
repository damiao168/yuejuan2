package capture

import (
	"context"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func fileCompleteInput(input FileResultInput, result map[string]any) workerruntime.CompleteInput {
	return workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "capture-file-result-v1", Result: result, DurationMS: input.DurationMS}
}

// SubmitFileResultCommand 以批次→采集文件→运行时任务的顺序取锁；页面、归组和成功回执一起提交。
// 物化期间租约到期会在最终完成检查被拒绝，并回滚所有领域写入；成功重放只核对原回执指纹。
func (s *PostgresStore) SubmitFileResultCommand(ctx context.Context, tenantID, fileID string, input FileResultInput, _ workerruntime.Store, result map[string]any) (File, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	if err = lockFileBatchTx(ctx, tx, tenantID, fileID); err != nil {
		return File{}, err
	}
	file, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID))
	if err != nil {
		return File{}, err
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, input.TaskID, input.LeaseToken)
	if err != nil {
		return File{}, err
	}
	if task.SourceType != "capture_file" || task.SourceID != fileID {
		return File{}, workerruntime.ErrConflict
	}
	if task.Status != workerruntime.StatusSucceeded {
		if file.Status == "completed" {
			return File{}, ErrConflict
		}
		file, err = s.applyFileResultInTx(ctx, tx, tenantID, fileID, input.Pages)
		if err != nil {
			return File{}, err
		}
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, fileCompleteInput(input, result)); err != nil {
		return File{}, err
	}
	return file, tx.Commit()
}

// 内存协调器先持领域锁再进入运行时原子事务；两边失败一起恢复，其他领取不能在物化期间接管。
func (s *MemoryStore) SubmitFileResultCommand(ctx context.Context, tenantID, fileID string, input FileResultInput, runtime workerruntime.Store, result map[string]any) (out File, err error) {
	atomic, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return File{}, ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 仅这三类领域事实由文件物化改写；序列/页身份已复制，失败不能留下半份答卷。
	before, marshalErr := json.Marshal(struct {
		Batches map[string]Batch
		Files   map[string]File
		Pages   map[string]Page
	}{s.batches, s.files, s.pages})
	if marshalErr != nil {
		return File{}, marshalErr
	}
	committed := false
	defer func() {
		if !committed {
			var saved struct {
				Batches map[string]Batch
				Files   map[string]File
				Pages   map[string]Page
			}
			_ = json.Unmarshal(before, &saved)
			s.batches, s.files, s.pages = saved.Batches, saved.Files, saved.Pages
		}
	}()
	err = atomic.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		file, exists := s.files[fileID]
		if !exists || file.TenantID != tenantID {
			return ErrNotFound
		}
		task, e := tx.LockTaskForCompletion(ctx, tenantID, input.TaskID, input.LeaseToken)
		if e != nil {
			return e
		}
		if task.SourceType != "capture_file" || task.SourceID != fileID {
			return workerruntime.ErrConflict
		}
		if task.Status != workerruntime.StatusSucceeded {
			if file.Status == "completed" {
				return ErrConflict
			}
			if e = validateDecodedFilePages(input.Pages); e != nil {
				return e
			}
			file, e = s.applyFileResultLocked(tenantID, fileID, input.Pages)
			if e != nil {
				return e
			}
		}
		if _, e = tx.Complete(ctx, tenantID, input.TaskID, fileCompleteInput(input, result)); e != nil {
			return e
		}
		out = file
		return nil
	})
	if err != nil {
		return File{}, err
	}
	committed = true
	return out, nil
}
