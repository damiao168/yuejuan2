package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) ListPages(ctx context.Context, tenantID, batchID string) ([]Page, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pageColumns+` FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL ORDER BY sequence_no`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		item, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetPageBySubmissionPageID(ctx context.Context, tenantID, submissionPageID string) (Page, error) {
	if submissionPageID == "" {
		return Page{}, ErrInvalidInput
	}
	return scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM capture_page
WHERE tenant_id=$1 AND submission_page_id=$2::uuid AND deleted_at IS NULL`, tenantID, submissionPageID))
}

func (s *PostgresStore) ApplyQualityOutcome(ctx context.Context, tenantID, submissionPageID, qualityStatus string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	linked, err := ApplyQualityOutcomeInTx(ctx, tx, tenantID, submissionPageID, qualityStatus)
	if err != nil {
		return err
	}
	if !linked {
		return ErrNotFound
	}
	return tx.Commit()
}

// ApplyQualityOutcomeInTx 更新关联采集页及聚合数据。非采集流程创建的提交返回 linked=false，不视为错误。
// 质检结果与批次聚合必须在同一事务中提交；找不到采集页时返回 linked=false，供外部提交保持兼容。
func ApplyQualityOutcomeInTx(ctx context.Context, tx *sql.Tx, tenantID, submissionPageID, qualityStatus string) (bool, error) {
	pageStatus := "quality_rejected"
	if qualityStatus == "passed" {
		pageStatus = "normalized"
	} else if qualityStatus == "review" {
		pageStatus = "needs_review"
	} else if qualityStatus != "failed" {
		return false, ErrInvalidInput
	}
	if tx == nil {
		return false, ErrInvalidInput
	}
	var batchID string
	err := tx.QueryRowContext(ctx, `UPDATE capture_page
SET status=CASE WHEN $3='normalized' AND page_identity->>'barcode_status'='needs_review' THEN 'needs_review' ELSE $3 END,
    page_identity=page_identity || jsonb_build_object('quality_status',$4::text),
    revision=revision+1,
    updated_at=now()
WHERE tenant_id=$1 AND submission_page_id=$2::uuid AND deleted_at IS NULL RETURNING capture_batch_id::text`, tenantID, submissionPageID, pageStatus, qualityStatus).Scan(&batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return false, err
	}
	return true, nil
}

// Revision 防止旧界面覆盖新修改；旋转会改变坐标系，因此同时使配准和切片失效并要求重算。
func (s *PostgresStore) UpdatePage(ctx context.Context, tenantID, pageID, actorID string, input UpdatePageInput) (Page, error) {
	if input.Revision <= 0 {
		return Page{}, ErrInvalidInput
	}
	if input.RotationDegrees != nil && !validRotation(*input.RotationDegrees) {
		return Page{}, ErrInvalidInput
	}
	if input.SequenceNo != nil && *input.SequenceNo <= 0 {
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
	rotation := current.RotationDegrees
	if input.RotationDegrees != nil {
		rotation = *input.RotationDegrees
	}
	sequence := current.SequenceNo
	if input.SequenceNo != nil {
		sequence = *input.SequenceNo
	}
	out, err := scanPage(tx.QueryRowContext(ctx, `UPDATE capture_page SET rotation_degrees=$3,sequence_no=$4,revision=revision+1,status=CASE WHEN $3<>rotation_degrees THEN 'needs_review' ELSE status END,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+pageColumns, tenantID, pageID, rotation, sequence))
	if err != nil {
		return Page{}, err
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(out)
	op := "reorder"
	if rotation != current.RotationDegrees {
		op = "rotate"
		if _, err = tx.ExecContext(ctx, `UPDATE answer_segment SET processing_status='invalidated',status='needs_manual_review',updated_at=now() WHERE tenant_id=$1 AND registration_run_id IN (SELECT id FROM page_registration_run WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND deleted_at IS NULL) AND deleted_at IS NULL`, tenantID, pageID); err != nil {
			return Page{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='invalidated',updated_at=now() WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND processing_status<>'invalidated' AND deleted_at IS NULL`, tenantID, pageID); err != nil {
			return Page{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_operation (tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,before_state,after_state) VALUES ($1,$2::uuid,$3,'capture_page',$4::uuid,$5::uuid,$6,$7)`, tenantID, current.CaptureBatchID, op, pageID, actorID, before, after)
	if err != nil {
		return Page{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, current.CaptureBatchID); err != nil {
		return Page{}, err
	}
	return out, tx.Commit()
}
