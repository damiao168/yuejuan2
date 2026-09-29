package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) DeletePage(ctx context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	return s.setPageDeleted(ctx, tenantID, pageID, actorID, input, true)
}
func (s *PostgresStore) RestorePage(ctx context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	return s.setPageDeleted(ctx, tenantID, pageID, actorID, input, false)
}
func (s *PostgresStore) setPageDeleted(ctx context.Context, tenantID, pageID, actorID string, input PageLifecycleInput, deleting bool) (Page, error) {
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return Page{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	current, err := scanPage(tx.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, pageID))
	if err != nil {
		return Page{}, err
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, current.CaptureBatchID); err != nil {
		return Page{}, err
	}
	if current.Revision != input.Revision {
		return Page{}, ErrConflict
	}
	if deleting && current.Status == "deleted" || !deleting && current.Status != "deleted" {
		return Page{}, ErrInvalidTransition
	}
	status := "needs_review"
	op := "restore"
	if deleting {
		status = "deleted"
		op = "delete"
	}
	override, _ := json.Marshal(map[string]any{"operation": op, "actor_id": actorID, "reason": strings.TrimSpace(input.Reason), "previous_status": current.Status})
	out, err := scanPage(tx.QueryRowContext(ctx, `UPDATE capture_page SET status=$3,revision=revision+1,manual_override=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+pageColumns, tenantID, pageID, status, override))
	if err != nil {
		return Page{}, err
	}
	if err = invalidatePageTx(ctx, tx, tenantID, pageID); err != nil {
		return Page{}, err
	}
	if current.SheetSerial != "" {
		if err = s.reconcileSheetSerialTx(ctx, tx, tenantID, current.SheetSerial); err != nil {
			return Page{}, err
		}
		out, err = scanPage(tx.QueryRowContext(ctx, `
SELECT `+pageColumns+`
FROM capture_page
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, pageID))
		if err != nil {
			return Page{}, err
		}
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(out)
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state)VALUES($1,$2::uuid,$3,'capture_page',$4::uuid,$5::uuid,$6,$7,$8)`, tenantID, current.CaptureBatchID, op, pageID, actorID, input.Reason, before, after)
	if err != nil {
		return Page{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, current.CaptureBatchID); err != nil {
		return Page{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) SplitSubmission(ctx context.Context, tenantID, batchID, actorID string, input SplitSubmissionInput) (MatchingQueue, error) {
	if input.SubmissionID == "" || len(input.PageIDs) == 0 || strings.TrimSpace(input.Reason) == "" {
		return MatchingQueue{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MatchingQueue{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingQueue{}, err
	}
	var examID, sourceType, collectedBy string
	var total int
	err = tx.QueryRowContext(ctx, `SELECT s.exam_id::text,s.source_type,s.collected_by::text,count(cp.id) FROM submission s JOIN capture_page cp ON cp.tenant_id=s.tenant_id AND cp.submission_id=s.id AND cp.capture_batch_id=$3::uuid AND cp.status<>'deleted' WHERE s.tenant_id=$1 AND s.id=$2::uuid AND s.deleted_at IS NULL GROUP BY s.id`, tenantID, input.SubmissionID, batchID).Scan(&examID, &sourceType, &collectedBy, &total)
	if err != nil {
		return MatchingQueue{}, mapNotFound(err)
	}
	if len(input.PageIDs) >= total {
		return MatchingQueue{}, ErrInvalidInput
	}
	var newID string
	err = tx.QueryRowContext(ctx, `INSERT INTO submission(tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by,identity_status)VALUES($1,$2::uuid,$3,'pages_uploaded',$4,$4,'unchecked','[]',$5::uuid,'unassigned')RETURNING id::text`, tenantID, examID, sourceType, len(input.PageIDs), collectedBy).Scan(&newID)
	if err != nil {
		return MatchingQueue{}, err
	}
	if err = movePagesTx(ctx, tx, tenantID, batchID, input.SubmissionID, newID, input.PageIDs); err != nil {
		return MatchingQueue{}, err
	}
	if err = recordGroupOperation(ctx, tx, tenantID, batchID, actorID, "split", input.SubmissionID, input.Reason, map[string]any{"new_submission_id": newID, "page_ids": input.PageIDs}); err != nil {
		return MatchingQueue{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingQueue{}, err
	}
	if err = tx.Commit(); err != nil {
		return MatchingQueue{}, err
	}
	return s.GetMatchingQueue(ctx, tenantID, batchID)
}

func (s *PostgresStore) MergeSubmissions(ctx context.Context, tenantID, batchID, actorID string, input MergeSubmissionsInput) (MatchingQueue, error) {
	if input.TargetSubmissionID == "" || input.SourceSubmissionID == "" || input.TargetSubmissionID == input.SourceSubmissionID || strings.TrimSpace(input.Reason) == "" {
		return MatchingQueue{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MatchingQueue{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingQueue{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND status<>'deleted' AND deleted_at IS NULL ORDER BY sequence_no FOR UPDATE`, tenantID, batchID, input.SourceSubmissionID)
	if err != nil {
		return MatchingQueue{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return MatchingQueue{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return MatchingQueue{}, err
	}
	if err = rows.Close(); err != nil {
		return MatchingQueue{}, err
	}
	if len(ids) == 0 {
		return MatchingQueue{}, ErrNotFound
	}
	if err = movePagesTx(ctx, tx, tenantID, batchID, input.SourceSubmissionID, input.TargetSubmissionID, ids); err != nil {
		return MatchingQueue{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE submission SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, input.SourceSubmissionID)
	if err != nil {
		return MatchingQueue{}, err
	}
	if err = recordGroupOperation(ctx, tx, tenantID, batchID, actorID, "merge", input.SourceSubmissionID, input.Reason, map[string]any{"target_submission_id": input.TargetSubmissionID}); err != nil {
		return MatchingQueue{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingQueue{}, err
	}
	if err = tx.Commit(); err != nil {
		return MatchingQueue{}, err
	}
	return s.GetMatchingQueue(ctx, tenantID, batchID)
}

// 拆合答卷后重新排页并清空双方身份绑定；旧配准与切片失效，不能沿用原答卷的识别结论。
func movePagesTx(ctx context.Context, tx *sql.Tx, tenantID, batchID, sourceID, targetID string, pageIDs []string) error {
	for i, id := range pageIDs {
		var oldPageID, newPageID string
		err := tx.QueryRowContext(ctx, `SELECT submission_page_id::text FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND id=$4::uuid AND status<>'deleted' FOR UPDATE`, tenantID, batchID, sourceID, id).Scan(&oldPageID)
		if err != nil {
			return mapNotFound(err)
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status,latest_quality_run_id,normalized_file_asset_id,quality_status,quality_override,quality_issues)
SELECT tenant_id,$3::uuid,file_asset_id,$4,'uploaded',NULL,NULL,'unchecked','{}','[]' FROM submission_page WHERE tenant_id=$1 AND id=$2::uuid RETURNING id::text`, tenantID, oldPageID, targetID, 100000+i).Scan(&newPageID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE capture_page SET submission_id=$4::uuid,submission_page_id=$5::uuid,assigned_page_no=$6,revision=revision+1,status='needs_review',updated_at=now() WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND id=$7::uuid`, tenantID, batchID, sourceID, targetID, newPageID, 100000+i, id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE submission_page SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, oldPageID)
		if err != nil {
			return err
		}
		if err = invalidatePageTx(ctx, tx, tenantID, id); err != nil {
			return err
		}
	}
	for _, sid := range []string{sourceID, targetID} {
		_, err := tx.ExecContext(ctx, `WITH historical AS(SELECT id,row_number()OVER(ORDER BY created_at,id) n FROM submission_page WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NOT NULL) UPDATE submission_page sp SET page_no=2000000+h.n,updated_at=now() FROM historical h WHERE sp.id=h.id`, tenantID, sid)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `WITH ordered AS(SELECT submission_page_id,row_number()OVER(ORDER BY sequence_no) n FROM capture_page WHERE tenant_id=$1 AND submission_id=$2::uuid AND status<>'deleted' AND deleted_at IS NULL) UPDATE submission_page sp SET page_no=o.n,updated_at=now() FROM ordered o WHERE sp.tenant_id=$1 AND sp.id=o.submission_page_id`, tenantID, sid)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE capture_page cp SET assigned_page_no=sp.page_no,updated_at=now() FROM submission_page sp WHERE cp.tenant_id=$1 AND cp.submission_id=$2::uuid AND sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id`, tenantID, sid)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE submission SET actual_page_count=(SELECT count(*)FROM submission_page WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NULL),identity_status='unassigned',student_id=NULL,candidate_no=NULL,identity_revision=identity_revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, sid)
		if err != nil {
			return err
		}
	}
	return nil
}
func invalidatePageTx(ctx context.Context, tx *sql.Tx, tenantID, pageID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE answer_segment SET processing_status='invalidated',status='needs_manual_review',updated_at=now() WHERE tenant_id=$1 AND registration_run_id IN(SELECT id FROM page_registration_run WHERE tenant_id=$1 AND capture_page_id=$2::uuid)`, tenantID, pageID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='invalidated',updated_at=now() WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND processing_status<>'invalidated'`, tenantID, pageID)
	return err
}
func recordGroupOperation(ctx context.Context, tx *sql.Tx, tenantID, batchID, actorID, op, targetID, reason string, after any) error {
	raw, _ := json.Marshal(after)
	_, err := tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state)VALUES($1,$2::uuid,$3,'submission',$4::uuid,$5::uuid,$6,'{}',$7)`, tenantID, batchID, op, targetID, actorID, reason, raw)
	return err
}
