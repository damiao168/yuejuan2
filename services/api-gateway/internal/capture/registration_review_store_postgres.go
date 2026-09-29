package capture

import (
	"context"
	"strings"
)

func (s *PostgresStore) ConfirmRegistration(ctx context.Context, tenantID, runID, actorID, reason string) (RegistrationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return RegistrationRun{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationRun{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, tenantID, runID))
	if err != nil {
		return RegistrationRun{}, err
	}
	var batchID string
	if err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID).Scan(&batchID); err != nil {
		return RegistrationRun{}, err
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationRun{}, err
	}
	if current.ProcessingStatus != "completed" || current.MatchStatus != "needs_review" || current.RegisteredFileAssetID == "" {
		return RegistrationRun{}, ErrInvalidTransition
	}
	out, err := scanRegistrationRun(tx.QueryRowContext(ctx, `UPDATE page_registration_run SET match_status='matched',confirmed_by=$3::uuid,confirmed_at=now(),confirmation_reason=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+registrationColumns, tenantID, runID, actorID, strings.TrimSpace(reason)))
	if err != nil {
		return RegistrationRun{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='ready',revision=revision+1,manual_override=manual_override||jsonb_build_object('registration_confirmed_by',$3::text,'registration_reason',$4::text),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID, actorID, reason)
	if err != nil {
		return RegistrationRun{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationRun{}, err
	}
	return out, tx.Commit()
}
func (s *PostgresStore) PrepareRegistrationRetry(ctx context.Context, tenantID, runID string) (RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationRun{}, err
	}
	defer tx.Rollback()
	var batchID string
	if err = tx.QueryRowContext(ctx, `SELECT cp.capture_batch_id::text FROM page_registration_run pr JOIN capture_page cp ON cp.tenant_id=pr.tenant_id AND cp.id=pr.capture_page_id WHERE pr.tenant_id=$1 AND pr.id=$2::uuid FOR UPDATE OF pr`, tenantID, runID).Scan(&batchID); err != nil {
		return RegistrationRun{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationRun{}, err
	}
	out, err := scanRegistrationRun(tx.QueryRowContext(ctx, `UPDATE page_registration_run SET processing_status='processing',match_status=NULL,error_code=NULL,error_detail='{}',started_at=now(),completed_at=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND processing_status IN('terminal_error','retryable_error') RETURNING `+registrationColumns, tenantID, runID))
	if err != nil {
		return RegistrationRun{}, err
	}
	return out, tx.Commit()
}
func (s *PostgresStore) GetProcessingSummary(ctx context.Context, tenantID, submissionID string) (ProcessingSummary, error) {
	items, err := s.queryProcessingSummaries(ctx, tenantID, "cp.submission_id=$2::uuid", submissionID)
	if err != nil {
		return ProcessingSummary{}, err
	}
	if len(items) == 0 {
		return ProcessingSummary{}, ErrNotFound
	}
	return items[0], nil
}

func (s *PostgresStore) ListProcessingSummaries(ctx context.Context, tenantID, batchID string) ([]ProcessingSummary, error) {
	return s.queryProcessingSummaries(ctx, tenantID, "cp.capture_batch_id=$2::uuid", batchID)
}

// 每页只统计最近未失效配准的切片，不能让旧运行的成功结果掩盖当前处理阻塞。
func (s *PostgresStore) queryProcessingSummaries(ctx context.Context, tenantID, predicate, resourceID string) ([]ProcessingSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH latest_registration AS (
  SELECT DISTINCT ON (r.capture_page_id)
    r.capture_page_id, r.id, r.processing_status, r.match_status
  FROM page_registration_run r
  WHERE r.tenant_id=$1 AND r.processing_status<>'invalidated' AND r.deleted_at IS NULL
  ORDER BY r.capture_page_id, r.created_at DESC, r.id DESC
), completed_segments AS (
  SELECT a.registration_run_id, count(*) AS segment_count
  FROM answer_segment a
  WHERE a.tenant_id=$1 AND a.processing_status='completed' AND a.deleted_at IS NULL
  GROUP BY a.registration_run_id
)
SELECT cp.submission_id::text, cp.id::text, cp.assigned_page_no, cp.status,
       COALESCE(sp.quality_status,''), COALESCE(pr.id::text,''),
       COALESCE(pr.processing_status,''), COALESCE(pr.match_status,''),
       COALESCE(seg.segment_count,0)
FROM capture_page cp
LEFT JOIN submission_page sp ON sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id AND sp.deleted_at IS NULL
LEFT JOIN latest_registration pr ON pr.capture_page_id=cp.id
LEFT JOIN completed_segments seg ON seg.registration_run_id=pr.id
WHERE cp.tenant_id=$1 AND `+predicate+` AND cp.submission_id IS NOT NULL
  AND cp.status<>'deleted' AND cp.deleted_at IS NULL
ORDER BY cp.submission_id, cp.sequence_no, cp.id`, tenantID, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProcessingSummary, 0)
	index := make(map[string]int)
	for rows.Next() {
		var submissionID, id, status, quality, runID, processing, match string
		var pageNo, segments int
		if err = rows.Scan(&submissionID, &id, &pageNo, &status, &quality, &runID, &processing, &match, &segments); err != nil {
			return nil, err
		}
		position, ok := index[submissionID]
		if !ok {
			position = len(items)
			index[submissionID] = position
			items = append(items, ProcessingSummary{SubmissionID: submissionID, Blockers: []ProcessingBlocker{}})
		}
		out := &items[position]
		out.TotalPages++
		if status == "ready" {
			out.ReadyPages++
			continue
		}
		b := ProcessingBlocker{PageID: id, PageNo: pageNo, RegistrationRunID: runID}
		switch {
		case quality == "review" || quality == "failed":
			b.Stage = "quality"
			b.Code = "quality_" + quality
			b.Action = "review_quality"
			out.BlockedPages++
		case processing == "terminal_error":
			b.Stage = "registration"
			b.Code = "registration_failed"
			b.Action = "retry_registration"
			out.BlockedPages++
		case match == "needs_review":
			b.Stage = "registration"
			b.Code = "low_confidence"
			b.Action = "confirm_registration"
			out.BlockedPages++
		case segments == 0 && processing == "completed":
			b.Stage = "segmentation"
			b.Code = "segments_missing"
			b.Action = "retry_registration"
			out.BlockedPages++
		default:
			b.Stage = "processing"
			b.Code = "pending"
			b.Action = "wait"
			out.PendingPages++
		}
		out.Blockers = append(out.Blockers, b)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].CanComplete = items[i].ReadyPages == items[i].TotalPages
	}
	return items, nil
}
