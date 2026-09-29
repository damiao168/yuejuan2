package imagequality

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 创建质量运行记录和 Worker Runtime 任务必须同一事务提交，避免出现有运行记录却没有可执行任务。
func (s *PostgresStore) CreateRunsWithTasks(ctx context.Context, tenantID, actorID string, input CreateRunsInput) ([]Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runs, err := createRunsInTx(ctx, tx, tenantID, input)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if _, err = workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, qualityRuntimeCreateInput(run)); err != nil {
			return nil, err
		}
	}
	return runs, tx.Commit()
}

func qualityRuntimeCreateInput(run Run) workerruntime.CreateTaskInput {
	return workerruntime.CreateTaskInput{
		TaskType: "image_quality", QueueName: "image-quality", SourceType: "image_quality_run", SourceID: run.ID,
		IdempotencyKey: "image-quality-run:" + run.ID, PayloadSchemaVersion: "image-quality.v1",
		Payload: map[string]any{
			"run_id": run.ID, "submission_id": run.SubmissionID, "submission_page_id": run.SubmissionPageID,
			"page_no": run.PageNo, "source_file_asset_id": run.SourceFileAssetID, "source_sha256": run.SourceSHA256,
			"download_url": run.DownloadURL, "profile_name": run.ProfileName, "profile_version": run.ProfileVersion,
			"profile_config_hash": run.ProfileConfigHash, "metric_schema_version": run.MetricSchemaVersion,
			"report_schema_version": run.ReportSchemaVersion,
		},
		MaxAttempts: 3, RetryBackoffSeconds: 30,
	}
}

// 结果提交把幂等收据、质量运行和答题页状态放在同一提交边界内，重试不会重复推进页面。
func (s *PostgresStore) SubmitResultCommand(ctx context.Context, tenantID, actorID, runID string, input ResultInput) (Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := s.completeRunInTx(ctx, tx, tenantID, runID, input)
	if err != nil {
		return Run{}, err
	}
	var runtimeTaskID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM agent_worker_task
WHERE tenant_id=$1 AND source_type='image_quality_run' AND source_id=$2::uuid
ORDER BY created_at DESC LIMIT 1`, tenantID, run.ID).Scan(&runtimeTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, workerruntime.ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if run.ProcessingStatus == ProcessingCompleted {
		if _, err = submission.ApplyPageQualityResultInTx(ctx, tx, tenantID, submission.ApplyPageQualityInput{
			SubmissionID: run.SubmissionID, PageID: run.SubmissionPageID, LatestQualityRunID: run.ID,
			NormalizedFileAssetID: run.NormalizedFileAssetID, QualityStatus: run.QualityStatus,
			QualityIssues: toSubmissionIssues(run.QualityIssues),
		}); err != nil {
			return Run{}, err
		}
		linked, captureErr := capture.ApplyQualityOutcomeInTx(ctx, tx, tenantID, run.SubmissionPageID, run.QualityStatus)
		if captureErr != nil {
			return Run{}, captureErr
		}
		if linked && run.QualityStatus == QualityPassed {
			if _, err = capture.QueueSubmissionPagesInTx(ctx, tx, tenantID, run.SubmissionID, actorID); err != nil && !errors.Is(err, capture.ErrInvalidTransition) {
				return Run{}, err
			}
		}
		_, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, runtimeTaskID, workerruntime.CompleteInput{
			LeaseToken: input.LeaseToken, ResultSchemaVersion: "image-quality-result.v1", DurationMS: input.DurationMS,
			Result: map[string]any{"quality_run_id": run.ID, "normalized_file_asset_id": run.NormalizedFileAssetID, "quality_status": run.QualityStatus, "result_version": run.ResultVersion},
		})
	} else {
		errorCode := run.ErrorCode
		if errorCode == "" {
			errorCode = "image_quality_processing_failed"
		}
		_, err = workerruntime.FailTaskInTx(ctx, tx, tenantID, runtimeTaskID, workerruntime.FailInput{
			LeaseToken: input.LeaseToken, Retryable: run.ProcessingStatus == ProcessingRetryableError,
			ErrorCode: errorCode, ErrorDetail: run.ErrorDetail, DurationMS: input.DurationMS,
		})
	}
	if err != nil {
		return Run{}, err
	}
	return run, tx.Commit()
}
