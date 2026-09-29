package imagequality

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateRuns(ctx context.Context, tenantID string, input CreateRunsInput) ([]Run, error) {
	input.Profile = normalizeProfile(input.Profile)
	if err := validateCreateRunsInput(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runs, err := createRunsInTx(ctx, tx, tenantID, input)
	if err != nil {
		return nil, err
	}
	return runs, tx.Commit()
}

func (s *PostgresStore) ListRunsForPage(ctx context.Context, tenantID string, submissionPageID string) ([]Run, error) {
	if tenantID == "" || submissionPageID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
FROM submission_page_quality_run
WHERE tenant_id = $1 AND submission_page_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
`, tenantID, submissionPageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := scanRun(rows, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func createRunsInTx(ctx context.Context, tx *sql.Tx, tenantID string, input CreateRunsInput) ([]Run, error) {
	if tx == nil {
		return nil, ErrInvalidInput
	}
	input.Profile = normalizeProfile(input.Profile)
	if err := validateCreateRunsInput(input); err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(input.Pages))
	for _, page := range input.Pages {
		row := tx.QueryRowContext(ctx, `
INSERT INTO submission_page_quality_run (
  tenant_id, submission_id, submission_page_id, source_file_asset_id, source_sha256,
  processing_status, profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version
)
VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8, $9, $10)
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
`, tenantID, input.SubmissionID, page.SubmissionPageID, page.SourceFileAssetID, page.SourceSHA256,
			input.Profile.Name, input.Profile.Version, input.Profile.ConfigHash, input.Profile.MetricSchemaVersion, input.Profile.ReportSchemaVersion)
		var run Run
		if err := scanRun(row, &run); err != nil {
			return nil, err
		}
		run.PageNo = page.PageNo
		run.DownloadURL = page.DownloadURL
		runs = append(runs, run)
	}
	return runs, nil
}

// PostgreSQL 领取使用行锁和租约 token，多个 Worker 并发时同一运行只能被一个租约持有。
func (s *PostgresStore) Claim(ctx context.Context, tenantID string, input ClaimInput) ([]ClaimedJob, error) {
	input = normalizeClaimInput(input)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT r.id::text
FROM submission_page_quality_run r
WHERE r.tenant_id = $1
  AND r.deleted_at IS NULL
  AND (
    r.processing_status = 'pending'
    OR (r.processing_status = 'processing' AND r.lease_expires_at < now())
  )
ORDER BY r.created_at ASC
FOR UPDATE SKIP LOCKED
LIMIT $2
`, tenantID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	jobs := make([]ClaimedJob, 0, len(ids))
	now := time.Now().UTC()
	for _, id := range ids {
		token := newLeaseToken()
		expiresAt := now.Add(time.Duration(input.LeaseSeconds) * time.Second)
		row := tx.QueryRowContext(ctx, `
UPDATE submission_page_quality_run r
SET processing_status = 'processing',
  worker_service = 'image-quality-worker',
  worker_instance_id = NULLIF($3, ''),
  attempt_no = attempt_no + 1,
  lease_token = $4,
  lease_expires_at = $5,
  started_at = COALESCE(started_at, now()),
  updated_at = now()
FROM submission_page p
WHERE r.tenant_id = $1
  AND r.id::text = $2
  AND r.deleted_at IS NULL
  AND p.tenant_id = r.tenant_id
  AND p.submission_id = r.submission_id
  AND p.id = r.submission_page_id
RETURNING r.id::text, r.submission_id::text, r.submission_page_id::text,
  p.page_no, r.source_file_asset_id::text, r.source_sha256,
  r.lease_token, r.lease_expires_at, r.attempt_no,
  r.profile_name, r.profile_version, r.profile_config_hash, r.metric_schema_version, r.report_schema_version
`, tenantID, id, input.WorkerInstanceID, token, expiresAt)
		var job ClaimedJob
		if err := row.Scan(
			&job.RunID,
			&job.SubmissionID,
			&job.SubmissionPageID,
			&job.PageNo,
			&job.SourceFileAssetID,
			&job.SourceSHA256,
			&job.LeaseToken,
			&job.LeaseExpiresAt,
			&job.AttemptNo,
			&job.Profile.Name,
			&job.Profile.Version,
			&job.Profile.ConfigHash,
			&job.Profile.MetricSchemaVersion,
			&job.Profile.ReportSchemaVersion,
		); err != nil {
			return nil, err
		}
		job.DownloadURL = "/api/v1/files/" + job.SourceFileAssetID + "/download"
		jobs = append(jobs, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *PostgresStore) LeaseRun(ctx context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error) {
	if leaseToken == "" || attemptNo <= 0 || !leaseExpiresAt.After(time.Now().UTC()) {
		return Run{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE submission_page_quality_run
SET processing_status = 'processing', worker_service = 'image-quality-worker',
  worker_instance_id = NULLIF($3, ''), attempt_no = $4, lease_token = $5,
  lease_expires_at = $6, started_at = COALESCE(started_at, now()), updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND processing_status IN ('pending', 'processing', 'retryable_error')
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
`, tenantID, runID, workerInstanceID, attemptNo, leaseToken, leaseExpiresAt)
	var run Run
	if err := scanRun(row, &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) RenewLease(ctx context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error) {
	if workerInstanceID == "" || leaseToken == "" || attemptNo <= 0 || !leaseExpiresAt.After(time.Now().UTC()) {
		return Run{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE submission_page_quality_run
SET lease_expires_at = $6, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND processing_status = 'processing'
  AND worker_instance_id = $3
  AND lease_token = $4
  AND attempt_no = $5
  AND worker_service = 'image-quality-worker'
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
`, tenantID, runID, workerInstanceID, leaseToken, attemptNo, leaseExpiresAt)
	var run Run
	if err := scanRun(row, &run); err == nil {
		return run, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Run{}, err
	}
	current, err := s.GetRun(ctx, tenantID, runID)
	if err != nil {
		return Run{}, err
	}
	if current.ProcessingStatus != ProcessingProcessing {
		return Run{}, ErrInvalidTransition
	}
	if current.WorkerInstanceID != workerInstanceID || current.LeaseToken != leaseToken || current.AttemptNo != attemptNo {
		return Run{}, ErrLeaseMismatch
	}
	return Run{}, ErrConflict
}

func (s *PostgresStore) CompleteRun(ctx context.Context, tenantID string, runID string, input ResultInput) (Run, error) {
	if err := validateResultInput(input); err != nil {
		return Run{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := s.completeRunInTx(ctx, tx, tenantID, runID, input)
	if err != nil {
		return Run{}, err
	}
	return run, tx.Commit()
}

func (s *PostgresStore) completeRunInTx(ctx context.Context, tx *sql.Tx, tenantID string, runID string, input ResultInput) (Run, error) {
	if tx == nil {
		return Run{}, ErrInvalidInput
	}
	if err := validateResultInput(input); err != nil {
		return Run{}, err
	}
	payloadHash := resultPayloadHash(input)
	run, err := s.getRunForUpdate(ctx, tx, tenantID, runID)
	if err != nil {
		return Run{}, err
	}
	if run.ProcessingStatus == ProcessingCompleted && run.AttemptNo == input.AttemptNo && run.ResultVersion == input.ResultVersion {
		if run.LeaseToken != input.LeaseToken {
			return Run{}, ErrLeaseMismatch
		}
		if run.ResultPayloadHash == payloadHash {
			return cloneRun(run), nil
		}
		return Run{}, ErrConflict
	}
	if run.ProcessingStatus != ProcessingProcessing {
		return Run{}, ErrInvalidTransition
	}
	if run.LeaseToken != input.LeaseToken || run.AttemptNo != input.AttemptNo {
		return Run{}, ErrLeaseMismatch
	}
	if run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Before(time.Now().UTC()) {
		return Run{}, ErrLeaseExpired
	}
	reportJSON, _ := json.Marshal(input.QualityReport)
	issuesJSON, _ := json.Marshal(input.QualityIssues)
	transformJSON, _ := json.Marshal(input.NormalizationTransform)
	errorDetailJSON, _ := json.Marshal(input.ErrorDetail)
	row := tx.QueryRowContext(ctx, `
UPDATE submission_page_quality_run
SET normalized_file_asset_id = NULLIF($3, '')::uuid,
  processing_status = $4,
  quality_status = NULLIF($5, ''),
  result_version = $6,
  result_payload_hash = $7,
  duration_ms = $8,
  quality_report = $9,
  quality_issues = $10,
  normalization_transform = $11,
  error_code = NULLIF($12, ''),
  error_detail = $13,
  completed_at = now(),
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
`, tenantID, runID, input.NormalizedFileAssetID, input.ProcessingStatus, input.QualityStatus,
		input.ResultVersion, payloadHash, input.DurationMS, reportJSON, issuesJSON, transformJSON, input.ErrorCode, errorDetailJSON)
	var out Run
	if err := scanRun(row, &out); err != nil {
		return Run{}, err
	}
	return out, nil
}

func (s *PostgresStore) GetRun(ctx context.Context, tenantID string, runID string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
FROM submission_page_quality_run
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, runID)
	var run Run
	if err := scanRun(row, &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) getRunForUpdate(ctx context.Context, tx *sql.Tx, tenantID string, runID string) (Run, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  source_file_asset_id::text, source_sha256, COALESCE(normalized_file_asset_id::text, ''),
  processing_status, COALESCE(quality_status, ''), profile_name, profile_version, profile_config_hash,
  metric_schema_version, report_schema_version, quality_report, quality_issues, normalization_transform,
  COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''), attempt_no, COALESCE(result_version, ''),
  COALESCE(result_payload_hash, ''), COALESCE(lease_token, ''), lease_expires_at, started_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail, created_at
FROM submission_page_quality_run
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, runID)
	var run Run
	if err := scanRun(row, &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

type runScanner interface {
	Scan(dest ...any) error
}

func scanRun(row runScanner, out *Run) error {
	var report []byte
	var issues []byte
	var transform []byte
	var errorDetail []byte
	var leaseExpiresAt, startedAt, completedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.SubmissionID,
		&out.SubmissionPageID,
		&out.SourceFileAssetID,
		&out.SourceSHA256,
		&out.NormalizedFileAssetID,
		&out.ProcessingStatus,
		&out.QualityStatus,
		&out.ProfileName,
		&out.ProfileVersion,
		&out.ProfileConfigHash,
		&out.MetricSchemaVersion,
		&out.ReportSchemaVersion,
		&report,
		&issues,
		&transform,
		&out.WorkerService,
		&out.WorkerInstanceID,
		&out.AttemptNo,
		&out.ResultVersion,
		&out.ResultPayloadHash,
		&out.LeaseToken,
		&leaseExpiresAt,
		&startedAt,
		&completedAt,
		&out.DurationMS,
		&out.ErrorCode,
		&errorDetail,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time.UTC()
		out.LeaseExpiresAt = &value
	}
	if startedAt.Valid {
		value := startedAt.Time.UTC()
		out.StartedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		out.CompletedAt = &value
	}
	_ = json.Unmarshal(report, &out.QualityReport)
	if out.QualityReport == nil {
		out.QualityReport = map[string]any{}
	}
	_ = json.Unmarshal(issues, &out.QualityIssues)
	if out.QualityIssues == nil {
		out.QualityIssues = []Issue{}
	}
	_ = json.Unmarshal(transform, &out.NormalizationTransform)
	if out.NormalizationTransform == nil {
		out.NormalizationTransform = map[string]any{}
	}
	_ = json.Unmarshal(errorDetail, &out.ErrorDetail)
	if out.ErrorDetail == nil {
		out.ErrorDetail = map[string]any{}
	}
	return nil
}
