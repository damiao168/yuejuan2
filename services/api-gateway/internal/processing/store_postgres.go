package processing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// RefreshExam is a set-based projection. It reads the existing durable
// pipeline facts in one statement, so an exam with a large page count does
// not turn into an application-level N+1 query.
func (s *PostgresStore) RefreshExam(ctx context.Context, tenantID, examID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(examID) == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, refreshStateSQL, tenantID, examID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, closeInvalidPageExceptionsSQL, tenantID, examID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, deleteInvalidPageStatesSQL, tenantID, examID); err != nil {
		return err
	}
	// An exception is keyed by the concrete source which can be retried. If a
	// registration/image-quality retry creates a new run, the stale exception
	// is closed below and a new one names the new source run.
	if _, err = tx.ExecContext(ctx, upsertExceptionsSQL, tenantID, examID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, closeStaleExceptionsSQL, tenantID, examID); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyProjection publishes one claimed source version and acknowledges that
// exact version in the same transaction. If the lease expired, every state and
// exception write is rolled back. A newer request that arrives concurrently is
// left pending because requested_version is never copied into projected_version.
func (s *PostgresStore) ApplyProjection(ctx context.Context, owner string, refresh ProjectionRefresh) error {
	if strings.TrimSpace(owner) == "" || refresh.TenantID == "" || refresh.ExamID == "" || refresh.RequestedVersion <= 0 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var leasedVersion int64
	if err = tx.QueryRowContext(ctx, `SELECT requested_version FROM processing_projection_cursor
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND lease_owner=$3 AND lease_expires_at>clock_timestamp()
FOR UPDATE`, refresh.TenantID, refresh.ExamID, owner+":"+refresh.claimID).Scan(&leasedVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectionLeaseLost
		}
		return err
	}
	if leasedVersion < refresh.RequestedVersion {
		return ErrProjectionLeaseLost
	}
	for _, statement := range []string{refreshStateSQL, closeInvalidPageExceptionsSQL, deleteInvalidPageStatesSQL, upsertExceptionsSQL, closeStaleExceptionsSQL} {
		if _, err = tx.ExecContext(ctx, statement, refresh.TenantID, refresh.ExamID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE processing_projection_cursor
SET projected_version=GREATEST(projected_version,$4),projected_at=now(),available_at=now(),
    lease_owner=NULL,lease_expires_at=NULL,attempt_count=0,last_error=NULL
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND lease_owner=$3 AND lease_expires_at>clock_timestamp()`,
		refresh.TenantID, refresh.ExamID, owner+":"+refresh.claimID, refresh.RequestedVersion)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return ErrProjectionLeaseLost
	}
	return tx.Commit()
}

func (s *PostgresStore) Summary(ctx context.Context, tenantID, examID string) (Summary, error) {
	// 汇总和阶段明细在同一只读事务中读取，避免返回相互矛盾的计数。
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(examID) == "" {
		return Summary{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback()
	result := Summary{ExamID: examID, ByStage: []StageCount{}, Issues: []IssueCount{}}
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE current_stage='READY'),
       COUNT(*) FILTER (WHERE blocking),
       COUNT(*) FILTER (WHERE current_stage<>'READY' AND NOT blocking),
       COALESCE((SELECT projected_at FROM processing_projection_cursor
                 WHERE tenant_id=$1::uuid AND exam_id=$2::uuid), 'epoch'::timestamptz)
FROM submission_page_processing_state
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
`, tenantID, examID).Scan(&result.TotalPages, &result.ReadyPages, &result.BlockedPages, &result.PendingPages, &result.GeneratedAt); err != nil {
		return Summary{}, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT current_stage, COUNT(*)
FROM submission_page_processing_state
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
GROUP BY current_stage ORDER BY current_stage
`, tenantID, examID)
	if err != nil {
		return Summary{}, err
	}
	for rows.Next() {
		var item StageCount
		if err := rows.Scan(&item.Stage, &item.Count); err != nil {
			rows.Close()
			return Summary{}, err
		}
		result.ByStage = append(result.ByStage, item)
	}
	if err := rows.Close(); err != nil {
		return Summary{}, err
	}
	rows, err = tx.QueryContext(ctx, `
SELECT issue_code, COUNT(*)
FROM submission_page_processing_state
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND issue_code IS NOT NULL
GROUP BY issue_code ORDER BY issue_code
`, tenantID, examID)
	if err != nil {
		return Summary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item IssueCount
		if err := rows.Scan(&item.Code, &item.Count); err != nil {
			return Summary{}, err
		}
		result.Issues = append(result.Issues, item)
	}
	if err = rows.Err(); err != nil {
		return Summary{}, err
	}
	if err = rows.Close(); err != nil {
		return Summary{}, err
	}
	if err = tx.Commit(); err != nil {
		return Summary{}, err
	}
	return result, nil
}

func (s *PostgresStore) ListExceptions(ctx context.Context, tenantID string, filter ExceptionFilter) (ListResult, error) {
	// 使用创建时间加 ID 的键集游标，避免深分页 OFFSET 在异常新增时跳项或重复。
	if strings.TrimSpace(tenantID) == "" {
		return ListResult{}, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	var cursorAt any
	if !filter.CursorAt.IsZero() {
		cursorAt = filter.CursorAt.UTC()
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id::text,e.exam_id::text,e.page_id::text,e.source_type,e.source_id::text,e.code,e.severity,e.blocking,e.status,
       COALESCE(e.assigned_to::text,''),e.details_json,e.created_at,e.updated_at,e.resolved_at,COALESCE(e.resolution,''),
       COALESCE(e.details_json->>'retry_source_type',''),COALESCE(e.details_json->>'retry_source_id','')
FROM operational_exception e
JOIN submission_page_processing_state ps ON ps.tenant_id=e.tenant_id AND ps.page_id=e.page_id AND ps.exam_id=e.exam_id
JOIN exam x ON x.tenant_id=e.tenant_id AND x.id=e.exam_id AND x.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid
  AND ($2='' OR e.exam_id::text=$2)
  AND ($3='' OR e.severity=$3)
  AND ($4='' OR ps.current_stage=$4)
  AND ($5='' OR lower(x.subject)=lower($5))
  AND ($6='' OR e.status=$6)
  AND ($7::timestamptz IS NULL OR e.created_at < $7 OR (e.created_at=$7 AND e.id::text < $8))
ORDER BY e.created_at DESC,e.id DESC
LIMIT $9
`, tenantID, filter.ExamID, filter.Severity, filter.Stage, filter.Subject, filter.Status, cursorAt, filter.CursorID, filter.Limit+1)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	items := make([]Exception, 0, filter.Limit)
	for rows.Next() {
		var item Exception
		if err := scanException(rows, &item); err != nil {
			return ListResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}
	result := ListResult{Exceptions: items}
	if len(result.Exceptions) > filter.Limit {
		result.HasMore = true
		result.Exceptions = result.Exceptions[:filter.Limit]
	}
	if result.HasMore && len(result.Exceptions) > 0 {
		last := result.Exceptions[len(result.Exceptions)-1]
		result.NextCursor = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	var projectedAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
SELECT MAX(projected_at)
FROM processing_projection_cursor
WHERE tenant_id=$1::uuid AND ($2='' OR exam_id::text=$2)
`, tenantID, filter.ExamID).Scan(&projectedAt); err != nil {
		return ListResult{}, err
	}
	if projectedAt.Valid {
		value := projectedAt.Time.UTC()
		result.ProjectedAt = &value
	}
	return result, nil
}

func (s *PostgresStore) GetException(ctx context.Context, tenantID, exceptionID string) (Exception, error) {
	row := s.db.QueryRowContext(ctx, exceptionSelect+` WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid`, tenantID, exceptionID)
	var item Exception
	if err := scanException(row, &item); err != nil {
		return Exception{}, err
	}
	return item, nil
}

func (s *PostgresStore) AssignException(ctx context.Context, tenantID, exceptionID, actorID string, input AssignInput) (Exception, error) {
	if strings.TrimSpace(input.AssigneeID) == "" {
		return Exception{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE operational_exception
SET assigned_to=$3::uuid,status='assigned',updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('open','assigned')
RETURNING id::text,exam_id::text,page_id::text,source_type,source_id::text,code,severity,blocking,status,
  COALESCE(assigned_to::text,''),details_json,created_at,updated_at,resolved_at,COALESCE(resolution,''),
  COALESCE(details_json->>'retry_source_type',''),COALESCE(details_json->>'retry_source_id','')
`, tenantID, exceptionID, input.AssigneeID)
	var item Exception
	if err := scanException(row, &item); err != nil {
		return Exception{}, err
	}
	_ = actorID // Audit ownership is recorded by the HTTP boundary.
	return item, nil
}

func (s *PostgresStore) ResolveException(ctx context.Context, tenantID, exceptionID, actorID string, input ResolveInput) (Exception, error) {
	if strings.TrimSpace(input.Resolution) == "" {
		return Exception{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE operational_exception
SET status='resolved',resolved_at=now(),resolution=$3,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('open','assigned')
RETURNING id::text,exam_id::text,page_id::text,source_type,source_id::text,code,severity,blocking,status,
  COALESCE(assigned_to::text,''),details_json,created_at,updated_at,resolved_at,COALESCE(resolution,''),
  COALESCE(details_json->>'retry_source_type',''),COALESCE(details_json->>'retry_source_id','')
`, tenantID, exceptionID, strings.TrimSpace(input.Resolution))
	var item Exception
	if err := scanException(row, &item); err != nil {
		return Exception{}, err
	}
	_ = actorID
	return item, nil
}

func (s *PostgresStore) RetryTarget(ctx context.Context, tenantID, exceptionID string) (RetryTarget, error) {
	// 只允许仍处于 open/assigned 且标记 retryable 的异常取出原 worker 来源。
	var result RetryTarget
	var retryable bool
	err := s.db.QueryRowContext(ctx, `
SELECT id::text,COALESCE(details_json->>'retry_source_type',''),COALESCE(details_json->>'retry_source_id',''),
       COALESCE((details_json->>'retryable')::boolean,false)
FROM operational_exception
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('open','assigned')
`, tenantID, exceptionID).Scan(&result.ExceptionID, &result.SourceType, &result.SourceID, &retryable)
	if errors.Is(err, sql.ErrNoRows) {
		return RetryTarget{}, ErrNotFound
	}
	if err != nil {
		return RetryTarget{}, err
	}
	if !retryable || result.SourceType == "" || result.SourceID == "" {
		return RetryTarget{}, ErrRetryForbidden
	}
	return result, nil
}

func (s *PostgresStore) ParserQualityForSegment(ctx context.Context, tenantID, segmentID string, subject assessment.SubjectCode, archetype string) (*float64, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `
SELECT ps.parser_quality_json
FROM answer_segment seg
JOIN submission_page_processing_state ps ON ps.tenant_id=seg.tenant_id AND ps.page_id=seg.submission_page_id
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL
`, tenantID, segmentID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	quality, err := parserQualityFromJSON(raw)
	if err != nil {
		return nil, err
	}
	return quality.For(subject, archetype), nil
}

func parserQualityFromJSON(raw []byte) (ParserQuality, error) {
	// 超出 0..1 或缺失的质量值转为 nil，让调用方明确 abstain，而不是使用失真的分数。
	values := map[string]float64{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &values); err != nil {
			return ParserQuality{}, err
		}
	}
	toPointer := func(key string) *float64 {
		value, ok := values[key]
		if !ok || value < 0 || value > 1 {
			return nil
		}
		return &value
	}
	return ParserQuality{Text: toPointer("text_quality"), MathExpression: toPointer("math_expression_quality"), ChemicalExpression: toPointer("chemical_expression_quality"), TableStructure: toPointer("table_structure_quality"), Diagram: toPointer("diagram_quality")}, nil
}

const exceptionSelect = `
SELECT e.id::text,e.exam_id::text,e.page_id::text,e.source_type,e.source_id::text,e.code,e.severity,e.blocking,e.status,
       COALESCE(e.assigned_to::text,''),e.details_json,e.created_at,e.updated_at,e.resolved_at,COALESCE(e.resolution,''),
       COALESCE(e.details_json->>'retry_source_type',''),COALESCE(e.details_json->>'retry_source_id','')
FROM operational_exception e`

type exceptionScanner interface{ Scan(...any) error }

func scanException(row exceptionScanner, out *Exception) error {
	var details []byte
	var resolved sql.NullTime
	if err := row.Scan(&out.ID, &out.ExamID, &out.PageID, &out.SourceType, &out.SourceID, &out.Code, &out.Severity, &out.Blocking, &out.Status,
		&out.AssignedTo, &details, &out.CreatedAt, &out.UpdatedAt, &resolved, &out.Resolution, &out.RetrySourceType, &out.RetrySourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := json.Unmarshal(details, &out.Details); err != nil {
		return err
	}
	if out.Details == nil {
		out.Details = map[string]any{}
	}
	if resolved.Valid {
		value := resolved.Time.UTC()
		out.ResolvedAt = &value
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return nil
}

// The source query intentionally selects only metadata, worker states and
// numeric quality. It never copies OCR text, page images or answer crops into
// the operational projection.
const refreshStateSQL = `
WITH facts AS (
  SELECT p.tenant_id,p.id AS page_id,p.submission_id,sub.exam_id,
         sub.identity_status,
         cp.id AS capture_page_id,COALESCE(cp.status,'') AS capture_status,
         q.id AS quality_run_id,COALESCE(q.processing_status,'') AS quality_processing,
         COALESCE(q.quality_status,'') AS quality_status,
         r.id AS registration_run_id,COALESCE(r.processing_status,'') AS registration_processing,
         COALESCE(r.match_status,'') AS registration_match,
         o.id AS ocr_task_id,COALESCE(o.status,'') AS ocr_status,o.min_confidence AS ocr_min_confidence,
         o.minimum_result_confidence,
         COALESCE(seg.has_completed,false) AS segment_completed,COALESCE(seg.has_terminal_error,false) AS segment_terminal,
         existing.issue_code AS prior_issue,existing.parser_quality_json AS prior_parser_quality,
         GREATEST(p.updated_at,sub.updated_at,COALESCE(cp.updated_at,'-infinity'::timestamptz),COALESCE(q.updated_at,'-infinity'::timestamptz),COALESCE(r.updated_at,'-infinity'::timestamptz),COALESCE(o.updated_at,'-infinity'::timestamptz),COALESCE(seg.updated_at,'-infinity'::timestamptz),COALESCE(existing.source_observed_at,'-infinity'::timestamptz)) AS observed_at
  FROM submission_page p
  JOIN processing_active_submission_page active ON active.tenant_id=p.tenant_id AND active.page_id=p.id
  JOIN submission sub ON sub.tenant_id=p.tenant_id AND sub.id=p.submission_id AND sub.deleted_at IS NULL
  LEFT JOIN LATERAL (
    SELECT id,status,updated_at FROM capture_page
    WHERE tenant_id=p.tenant_id AND submission_page_id=p.id AND deleted_at IS NULL
    ORDER BY updated_at DESC,id DESC LIMIT 1
  ) cp ON true
  LEFT JOIN LATERAL (
    SELECT id,processing_status,quality_status,updated_at FROM submission_page_quality_run
    WHERE tenant_id=p.tenant_id AND submission_page_id=p.id AND deleted_at IS NULL
    ORDER BY updated_at DESC,id DESC LIMIT 1
  ) q ON true
  LEFT JOIN LATERAL (
    SELECT id,processing_status,match_status,updated_at FROM page_registration_run
    WHERE tenant_id=p.tenant_id AND submission_page_id=p.id AND deleted_at IS NULL AND processing_status<>'invalidated'
    ORDER BY updated_at DESC,id DESC LIMIT 1
  ) r ON true
  LEFT JOIN LATERAL (
    SELECT task.id,task.status,task.min_confidence,MIN(result.confidence) AS minimum_result_confidence,task.updated_at
    FROM ocr_task task
    LEFT JOIN ocr_result result ON result.tenant_id=task.tenant_id AND result.ocr_task_id=task.id AND result.submission_page_id=p.id AND result.deleted_at IS NULL
    WHERE task.tenant_id=p.tenant_id AND task.submission_id=p.submission_id AND task.deleted_at IS NULL
    GROUP BY task.id,task.status,task.min_confidence,task.updated_at,task.created_at
    ORDER BY task.created_at DESC,task.id DESC LIMIT 1
  ) o ON true
  LEFT JOIN LATERAL (
    SELECT bool_or(processing_status='completed') AS has_completed,
           bool_or(processing_status='terminal_error') AS has_terminal_error,
           MAX(updated_at) AS updated_at
    FROM answer_segment
    WHERE tenant_id=p.tenant_id AND submission_page_id=p.id AND deleted_at IS NULL
  ) seg ON true
  LEFT JOIN submission_page_processing_state existing ON existing.tenant_id=p.tenant_id AND existing.page_id=p.id
  WHERE p.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND p.deleted_at IS NULL
), projected AS (
  SELECT *,
    CASE
      -- A raw capture status can be stale after a later identity/quality/OCR
      -- result. READY is only emitted when no operational prerequisite below
      -- still needs attention.
      WHEN capture_page_id IS NULL THEN 'RECEIVED'
      WHEN identity_status IS DISTINCT FROM 'matched' THEN 'IDENTIFIED'
      WHEN capture_status='quality_rejected' OR quality_status IN ('failed','review') OR quality_processing='terminal_error' THEN 'QUALITY_CHECKED'
      WHEN registration_processing IN ('retryable_error','terminal_error') OR registration_match IN ('needs_review','failed') THEN 'REGISTERED'
      WHEN segment_terminal THEN 'SEGMENTED'
      WHEN ocr_status='failed' OR (ocr_status='completed' AND (minimum_result_confidence IS NULL OR minimum_result_confidence < ocr_min_confidence)) THEN 'PARSED'
      WHEN capture_status='ready' THEN 'READY'
      WHEN segment_completed THEN 'SEGMENTED'
      WHEN ocr_status='completed' THEN 'PARSED'
      WHEN registration_processing='completed' THEN 'REGISTERED'
      WHEN capture_status IN ('registration','segmenting') THEN 'IDENTIFIED'
      WHEN capture_status IN ('normalized','page_matching') OR quality_processing='completed' THEN 'QUALITY_CHECKED'
      WHEN capture_status IN ('decoded','grouped','quality_checking') THEN 'VALIDATED'
      ELSE 'RECEIVED'
    END AS current_stage,
    CASE
      WHEN identity_status IS DISTINCT FROM 'matched' THEN 'BLOCKED_MISSING_IDENTITY'
      WHEN capture_page_id IS NULL THEN 'BLOCKED_MISSING_PAGE'
      WHEN capture_status='quality_rejected' OR quality_status IN ('failed','review') OR quality_processing='terminal_error' THEN 'BLOCKED_LOW_IMAGE_QUALITY'
      WHEN registration_processing IN ('retryable_error','terminal_error') OR registration_match IN ('needs_review','failed') THEN 'BLOCKED_BAD_ALIGNMENT'
      WHEN segment_terminal THEN 'SEGMENTATION_FAILED'
      WHEN ocr_status='failed' OR (ocr_status='completed' AND (minimum_result_confidence IS NULL OR minimum_result_confidence < ocr_min_confidence)) THEN 'OCR_LOW_CONFIDENCE'
      WHEN prior_issue IN ('MATH_PARSE_FAILED','CHEMISTRY_PARSE_FAILED','TABLE_PARSE_FAILED','DIAGRAM_PARSE_FAILED') THEN prior_issue
      ELSE NULL
    END AS issue_code,
    CASE
      WHEN identity_status IS DISTINCT FROM 'matched' OR capture_page_id IS NULL OR capture_status='quality_rejected' OR quality_status='failed' OR quality_processing='terminal_error' OR registration_processing IN ('retryable_error','terminal_error') OR registration_match IN ('needs_review','failed') THEN true
      ELSE false
    END AS blocking,
    CASE
      WHEN quality_processing IN ('retryable_error','terminal_error') THEN true
      WHEN registration_processing IN ('retryable_error','terminal_error') THEN true
      WHEN ocr_status='failed' THEN true
      WHEN segment_terminal AND registration_run_id IS NOT NULL THEN true
      ELSE false
    END AS retryable,
    CASE
      WHEN quality_processing IN ('retryable_error','terminal_error') THEN 'image_quality_run'
      WHEN registration_processing IN ('retryable_error','terminal_error') OR registration_match IN ('needs_review','failed') OR segment_terminal THEN 'page_registration_run'
      WHEN ocr_status='failed' THEN 'ocr_task'
      ELSE ''
    END AS retry_source_type,
    CASE
      WHEN quality_processing IN ('retryable_error','terminal_error') THEN quality_run_id::text
      WHEN registration_processing IN ('retryable_error','terminal_error') OR registration_match IN ('needs_review','failed') OR segment_terminal THEN registration_run_id::text
      WHEN ocr_status='failed' THEN ocr_task_id::text
      ELSE ''
    END AS retry_source_id,
    COALESCE(prior_parser_quality,'{}'::jsonb) || jsonb_strip_nulls(jsonb_build_object('text_quality',minimum_result_confidence)) AS parser_quality_json
  FROM facts
)
INSERT INTO submission_page_processing_state (tenant_id,page_id,submission_id,exam_id,current_stage,blocking,issue_code,retryable,retry_source_type,retry_source_id,parser_quality_json,source_observed_at,updated_at)
SELECT tenant_id,page_id,submission_id,exam_id,current_stage,blocking,NULLIF(issue_code,''),retryable,NULLIF(retry_source_type,''),NULLIF(retry_source_id,'')::uuid,parser_quality_json,observed_at,observed_at
FROM projected
ON CONFLICT (tenant_id,page_id) DO UPDATE SET
  submission_id=EXCLUDED.submission_id,exam_id=EXCLUDED.exam_id,current_stage=EXCLUDED.current_stage,blocking=EXCLUDED.blocking,
  issue_code=EXCLUDED.issue_code,retryable=EXCLUDED.retryable,retry_source_type=EXCLUDED.retry_source_type,retry_source_id=EXCLUDED.retry_source_id,
  parser_quality_json=EXCLUDED.parser_quality_json,source_observed_at=EXCLUDED.source_observed_at,updated_at=EXCLUDED.updated_at`

const closeInvalidPageExceptionsSQL = `
UPDATE operational_exception e
SET status='resolved',resolved_at=COALESCE(e.resolved_at,now()),
    resolution=CASE
      WHEN EXISTS(SELECT 1 FROM processing_active_submission_page active WHERE active.tenant_id=e.tenant_id AND active.page_id=e.page_id) THEN 'source_reassigned'
      ELSE 'source_deleted'
    END,
    updated_at=now()
WHERE e.tenant_id=$1::uuid AND e.exam_id=$2::uuid AND e.status IN ('open','assigned')
  AND NOT EXISTS(SELECT 1 FROM processing_active_submission_page active WHERE active.tenant_id=e.tenant_id AND active.page_id=e.page_id AND active.exam_id=e.exam_id)`

const deleteInvalidPageStatesSQL = `
DELETE FROM submission_page_processing_state ps
WHERE ps.tenant_id=$1::uuid AND ps.exam_id=$2::uuid
  AND NOT EXISTS (
    SELECT 1 FROM processing_active_submission_page active
    WHERE active.tenant_id=ps.tenant_id AND active.page_id=ps.page_id AND active.exam_id=$2::uuid
  )`

// 已指派异常保留指派状态；人工结案且详情未变时保留结案，忽略观测时间的变化。
// 因源数据恢复而自动关闭的异常，如果再次出现，则重新打开。
const upsertExceptionsSQL = `
INSERT INTO operational_exception (tenant_id,exam_id,source_type,source_id,page_id,code,severity,blocking,status,details_json,created_at,updated_at)
SELECT ps.tenant_id,ps.exam_id,
       COALESCE(ps.retry_source_type,'submission_page'),COALESCE(ps.retry_source_id,ps.page_id),ps.page_id,ps.issue_code,
       CASE WHEN ps.blocking THEN 'P0' WHEN ps.issue_code='OCR_LOW_CONFIDENCE' THEN 'P1' ELSE 'P2' END,
       ps.blocking,'open',
       jsonb_strip_nulls(jsonb_build_object('current_stage',ps.current_stage,'retryable',ps.retryable,
         'retry_source_type',ps.retry_source_type,'retry_source_id',ps.retry_source_id,'source_observed_at',ps.source_observed_at)),
       ps.source_observed_at,ps.source_observed_at
FROM submission_page_processing_state ps
WHERE ps.tenant_id=$1::uuid AND ps.exam_id=$2::uuid AND ps.issue_code IS NOT NULL
ON CONFLICT (tenant_id,exam_id,source_type,source_id,code) DO UPDATE SET
  severity=EXCLUDED.severity,blocking=EXCLUDED.blocking,
  status=CASE
    WHEN operational_exception.status='assigned' THEN 'assigned'
    WHEN operational_exception.status='resolved' AND COALESCE(operational_exception.resolution,'') NOT IN ('source_deleted','source_reassigned','source_replaced','source_recovered') AND (operational_exception.details_json-'source_observed_at')=(EXCLUDED.details_json-'source_observed_at') THEN 'resolved'
    ELSE 'open'
  END,
  details_json=EXCLUDED.details_json,updated_at=EXCLUDED.updated_at,
  resolved_at=CASE WHEN operational_exception.status='resolved' AND COALESCE(operational_exception.resolution,'') NOT IN ('source_deleted','source_reassigned','source_replaced','source_recovered') AND (operational_exception.details_json-'source_observed_at')=(EXCLUDED.details_json-'source_observed_at') THEN operational_exception.resolved_at ELSE NULL END,
  resolution=CASE WHEN operational_exception.status='resolved' AND COALESCE(operational_exception.resolution,'') NOT IN ('source_deleted','source_reassigned','source_replaced','source_recovered') AND (operational_exception.details_json-'source_observed_at')=(EXCLUDED.details_json-'source_observed_at') THEN operational_exception.resolution ELSE NULL END`

const closeStaleExceptionsSQL = `
UPDATE operational_exception e
SET status='resolved',resolved_at=COALESCE(resolved_at,now()),resolution=COALESCE(NULLIF(resolution,''),'source_recovered'),updated_at=now()
WHERE e.tenant_id=$1::uuid AND e.exam_id=$2::uuid AND e.status IN ('open','assigned')
  AND NOT EXISTS (
    SELECT 1 FROM submission_page_processing_state ps
    WHERE ps.tenant_id=e.tenant_id AND ps.exam_id=e.exam_id AND ps.page_id=e.page_id AND ps.issue_code=e.code
      AND COALESCE(ps.retry_source_type,'submission_page')=e.source_type
      AND COALESCE(ps.retry_source_id,ps.page_id)=e.source_id
  )`

var _ Store = (*PostgresStore)(nil)
