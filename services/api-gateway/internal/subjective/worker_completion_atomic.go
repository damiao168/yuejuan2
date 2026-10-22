package subjective

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func workerResultHash(input WorkerResultInput) (string, error) {
	if input.resultHash != "" {
		return input.resultHash, nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func workerCompletionInput(run GradingRun, grade Grade, input WorkerResultInput) (workerruntime.CompleteInput, error) {
	hash, err := workerResultHash(input)
	if err != nil {
		return workerruntime.CompleteInput{}, err
	}
	return workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: input.ResultSchemaVersion,
		Result: map[string]any{"run_id": run.ID, "grade_id": grade.ID, "worker_result_hash": hash}, DurationMS: input.DurationMS}, nil
}

func validateWorkerCompletion(run GradingRun, task workerruntime.Task, prepared WorkerExecutionContext, input WorkerResultInput, grade Grade) error {
	if task.Status != workerruntime.StatusSucceeded && !workerRunActive(run) {
		return errors.Join(ErrWorkerCompletionRejected, workerruntime.ErrInvalidTransition)
	}
	if task.Status == workerruntime.StatusSucceeded && run.Status != RunSucceeded {
		// runtime 已报告成功但来源运行不是成功态时，不能用回放把失败运行复活。
		return errors.Join(ErrWorkerCompletionRejected, workerruntime.ErrConflict)
	}
	if task.Status != workerruntime.StatusSucceeded && !runContextVersionMatches(run, prepared.Context) {
		return errors.Join(ErrWorkerCompletionRejected, ErrWorkerResultVersionConflict)
	}
	if run.TenantID != task.TenantID || task.ID != input.TaskID || task.SourceType != "subjective_grading_run" || task.SourceID != run.ID ||
		run.ID != prepared.Run.ID || run.RequestID != prepared.Run.RequestID || run.AnswerSegmentID != prepared.Run.AnswerSegmentID ||
		run.ExamQuestionSnapshotID != prepared.Run.ExamQuestionSnapshotID ||
		run.AnswerVersion != prepared.Run.AnswerVersion || run.QuestionID != prepared.Run.QuestionID || run.RubricVersion != prepared.Run.RubricVersion ||
		run.ModelVersion != prepared.Run.ModelVersion || run.PromptVersion != prepared.Run.PromptVersion || run.MinConfidence != prepared.Run.MinConfidence ||
		run.MathArtifactID != prepared.Run.MathArtifactID || run.MathArtifactVersion != prepared.Run.MathArtifactVersion ||
		run.MathCorrectionRevision != prepared.Run.MathCorrectionRevision || run.MathScoringVersion != prepared.Run.MathScoringVersion ||
		task.WorkerService != prepared.Task.WorkerService || task.WorkerInstanceID != prepared.Task.WorkerInstanceID ||
		grade.RunID != run.ID || grade.AnswerSegmentID != run.AnswerSegmentID || grade.QuestionID != run.QuestionID ||
		grade.AnswerVersion != run.AnswerVersion || grade.RubricVersion != run.RubricVersion || grade.ModelVersion != run.ModelVersion || grade.PromptVersion != run.PromptVersion ||
		grade.MathArtifactID != run.MathArtifactID || grade.MathArtifactVersion != run.MathArtifactVersion ||
		grade.MathCorrectionRevision != run.MathCorrectionRevision || grade.MathScoringVersion != run.MathScoringVersion ||
		grade.AdapterRequestID != run.RequestID || !workerResultMatchesRun(run, input) {
		return errors.Join(ErrWorkerCompletionRejected, ErrWorkerTaskMismatch)
	}
	if task.Status == workerruntime.StatusSucceeded {
		hash, err := workerResultHash(input)
		if err != nil {
			return err
		}
		// 成绩 ID 相同不代表模型结果相同；回放必须绑定首次接受的完整请求。
		if task.Result["worker_result_hash"] != hash || task.Result["run_id"] != run.ID || task.Result["grade_id"] != grade.ID {
			return errors.Join(ErrWorkerCompletionRejected, workerruntime.ErrConflict)
		}
	}
	return nil
}

func workerRunActive(run GradingRun) bool {
	return run.Status == RunQueued || run.Status == RunProcessing
}

func validateCompletionCapability(ctx context.Context, tenantID string, input WorkerResultInput) error {
	if task, ok := workerruntime.TaskCapabilityFromContext(ctx); ok &&
		(task.TenantID != tenantID || task.ID != input.TaskID || task.LeaseToken != input.LeaseToken) {
		return errors.Join(ErrWorkerCompletionRejected, ErrWorkerTaskMismatch)
	}
	return nil
}

func validateCompletionWorker(ctx context.Context, task workerruntime.Task) error {
	if capability, ok := workerruntime.TaskCapabilityFromContext(ctx); ok &&
		(capability.WorkerService != task.WorkerService || capability.WorkerInstanceID != task.WorkerInstanceID || capability.SourceType != task.SourceType || capability.SourceID != task.SourceID) {
		return errors.Join(ErrWorkerCompletionRejected, ErrWorkerTaskMismatch)
	}
	return nil
}

// CompleteWorkerAtomic 按来源锁、runtime 锁的顺序提交；失败或 panic 时两边都恢复原事实。
func (s *MemoryStore) CompleteWorkerAtomic(ctx context.Context, tenantID, actorID string, prepared WorkerExecutionContext, input WorkerResultInput, grade Grade, runtime workerCompletionRuntime) (result WorkerCompletionResult, err error) {
	if err := validateCompletionCapability(ctx, tenantID, input); err != nil {
		return result, err
	}
	runtimeStore, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return result, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	oldNext := s.next
	oldGrades := make(map[string][]Grade, len(s.grades))
	for key, value := range s.grades {
		oldGrades[key] = append([]Grade(nil), value...)
	}
	oldRuns := make(map[string]GradingRun, len(s.runs))
	for key, value := range s.runs {
		oldRuns[key] = value
	}
	committed := false
	defer func() {
		if !committed {
			s.next, s.grades, s.runs = oldNext, oldGrades, oldRuns
		}
	}()
	err = runtimeStore.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		task, err := tx.LockTaskForCompletion(ctx, tenantID, input.TaskID, input.LeaseToken)
		if err != nil {
			return errors.Join(ErrWorkerCompletionRejected, err)
		}
		run, err := s.getRunLocked(tenantID, prepared.Run.ID)
		if err != nil {
			return err
		}
		if task.Status != workerruntime.StatusSucceeded && run.ExamQuestionSnapshotID != "" {
			current, ok := s.contexts[key(tenantID, run.AnswerSegmentID)]
			if !ok || !runContextVersionMatches(run, current) {
				return errors.Join(ErrWorkerCompletionRejected, ErrWorkerResultVersionConflict)
			}
		}
		if err := validateWorkerCompletion(run, task, prepared, input, grade); err != nil {
			return err
		}
		if err := validateCompletionWorker(ctx, task); err != nil {
			return err
		}
		if actorID == "" {
			actorID = task.CreatedBy
		}
		grade, err = s.createGradeLocked(tenantID, actorID, grade)
		if err != nil {
			return err
		}
		complete, err := workerCompletionInput(run, grade, input)
		if err != nil {
			return err
		}
		task, err = tx.Complete(ctx, tenantID, input.TaskID, complete)
		if err != nil {
			return errors.Join(ErrWorkerCompletionRejected, err)
		}
		run, err = s.updateRunLocked(tenantID, run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID, AttemptCount: task.AttemptCount, ExecutionDiagnostics: diagnosticsFromOutput(input.Output, "")})
		if err != nil {
			return err
		}
		result = WorkerCompletionResult{Run: run, Grade: grade, Task: task}
		return nil
	})
	committed = err == nil
	return result, err
}

// CompleteWorkerAtomic 把成绩、用量、runtime 完成和运行终态放入同一 PostgreSQL 事务。
// runtime 校验或运行更新失败会回滚成绩；不能靠调整独立提交顺序修复半成功。
func (s *PostgresStore) CompleteWorkerAtomic(ctx context.Context, tenantID, actorID string, prepared WorkerExecutionContext, input WorkerResultInput, grade Grade, _ workerCompletionRuntime) (WorkerCompletionResult, error) {
	if err := validateCompletionCapability(ctx, tenantID, input); err != nil {
		return WorkerCompletionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	defer tx.Rollback()
	run, err := getRunInDB(ctx, tx, tenantID, prepared.Run.ID, true)
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, input.TaskID, input.LeaseToken)
	if err != nil {
		return WorkerCompletionResult{}, errors.Join(ErrWorkerCompletionRejected, err)
	}
	if err := validateWorkerCompletion(run, task, prepared, input, grade); err != nil {
		return WorkerCompletionResult{}, err
	}
	if task.Status != workerruntime.StatusSucceeded && run.ExamQuestionSnapshotID != "" {
		var current bool
		if prepared.Context.InputVersionID != "" {
			current, err = imageBindingCurrentInTx(ctx, tx, tenantID, run)
			if err != nil {
				return WorkerCompletionResult{}, err
			}
		} else {
			// 结算事务再次核对数据库事实；不能只相信先前 Prepare 返回的对象。
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM answer_segment seg
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN LATERAL (SELECT id,rubric_snapshot_json FROM exam_question_snapshot WHERE tenant_id=seg.tenant_id AND exam_id=q.exam_id AND question_id=q.id ORDER BY snapshot_version DESC,id DESC LIMIT 1) snapshot ON snapshot.id=$3::uuid AND snapshot.rubric_snapshot_json->>'version'=$4
JOIN LATERAL (SELECT id FROM answer_segment_answer WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1) answer ON answer.id::text=$5
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.question_id=$6::uuid AND seg.deleted_at IS NULL
)`, tenantID, run.AnswerSegmentID, run.ExamQuestionSnapshotID, run.RubricVersion, run.AnswerVersion, run.QuestionID).Scan(&current); err != nil {
				return WorkerCompletionResult{}, err
			}
		}
		if !current {
			return WorkerCompletionResult{}, errors.Join(ErrWorkerCompletionRejected, ErrWorkerResultVersionConflict)
		}
	}
	if err := validateCompletionWorker(ctx, task); err != nil {
		return WorkerCompletionResult{}, err
	}
	if actorID == "" {
		actorID = task.CreatedBy
	}
	grade, err = createGradeInTx(ctx, tx, tenantID, actorID, grade)
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	complete, err := workerCompletionInput(run, grade, input)
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	task, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, complete)
	if err != nil {
		return WorkerCompletionResult{}, errors.Join(ErrWorkerCompletionRejected, err)
	}
	run, err = updateRunInDB(ctx, tx, tenantID, run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID, AttemptCount: task.AttemptCount, ExecutionDiagnostics: diagnosticsFromOutput(input.Output, "")})
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerCompletionResult{}, err
	}
	return WorkerCompletionResult{Run: run, Grade: grade, Task: task}, nil
}

// MarkWorkerProcessing 用同一锁序检查任务后标记运行；不能让迟到的准备请求把成功运行改回 processing。
func (s *MemoryStore) MarkWorkerProcessing(ctx context.Context, tenantID, runID, taskID, leaseToken string, runtime workerExecutionRuntime) (GradingRun, workerruntime.Task, error) {
	if err := validateCompletionCapability(ctx, tenantID, WorkerResultInput{TaskID: taskID, LeaseToken: leaseToken}); err != nil {
		return GradingRun{}, workerruntime.Task{}, err
	}
	runtimeStore, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return GradingRun{}, workerruntime.Task{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var run GradingRun
	var task workerruntime.Task
	err := runtimeStore.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		var err error
		run, err = s.getRunLocked(tenantID, runID)
		if err != nil {
			return err
		}
		task, err = tx.LockTaskForCompletion(ctx, tenantID, taskID, leaseToken)
		if err != nil {
			return errors.Join(ErrWorkerLeaseMismatch, err)
		}
		if task.Status == workerruntime.StatusSucceeded {
			return ErrWorkerLeaseMismatch
		}
		// 手工重新入队 runtime 不会生成新模型运行；终态 run 不能因此重新获得推理权限。
		if !workerRunActive(run) {
			return ErrWorkerLeaseMismatch
		}
		if task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
			return ErrWorkerTaskMismatch
		}
		if err := validateCompletionWorker(ctx, task); err != nil {
			return err
		}
		run, err = s.updateRunLocked(tenantID, run.ID, UpdateRunInput{Status: RunProcessing, AttemptCount: task.AttemptCount})
		return err
	})
	return run, task, err
}

// MarkWorkerProcessing 与结算保持运行锁、任务锁的顺序，状态检查和 processing 更新不能分开提交。
func (s *PostgresStore) MarkWorkerProcessing(ctx context.Context, tenantID, runID, taskID, leaseToken string, _ workerExecutionRuntime) (GradingRun, workerruntime.Task, error) {
	if err := validateCompletionCapability(ctx, tenantID, WorkerResultInput{TaskID: taskID, LeaseToken: leaseToken}); err != nil {
		return GradingRun{}, workerruntime.Task{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GradingRun{}, workerruntime.Task{}, err
	}
	defer tx.Rollback()
	run, err := getRunInDB(ctx, tx, tenantID, runID, true)
	if err != nil {
		return run, workerruntime.Task{}, err
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, taskID, leaseToken)
	if err != nil {
		return run, task, errors.Join(ErrWorkerLeaseMismatch, err)
	}
	if task.Status == workerruntime.StatusSucceeded {
		return run, task, ErrWorkerLeaseMismatch
	}
	if !workerRunActive(run) {
		return run, task, ErrWorkerLeaseMismatch
	}
	if task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
		return run, task, ErrWorkerTaskMismatch
	}
	if err := validateCompletionWorker(ctx, task); err != nil {
		return run, task, err
	}
	run, err = updateRunInDB(ctx, tx, tenantID, runID, UpdateRunInput{Status: RunProcessing, AttemptCount: task.AttemptCount})
	if err != nil {
		return run, task, err
	}
	return run, task, tx.Commit()
}
