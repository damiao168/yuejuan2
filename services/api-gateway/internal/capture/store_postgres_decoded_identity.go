package capture

// 条码身份只在签名与台账检查后参与归属；重复纸张与学生冲突沿用解码结果事务，不能覆盖已存在的有效提交。
import (
	"context"
	"database/sql"
	"encoding/json"
)

type decodedFileIdentity struct {
	studentID, templateID, sheetSerial string
	conflict                           bool
	verifiedPageNumbers                map[int]bool
	unverifiedCapturePageIDs           []string
}

func (identity *decodedFileIdentity) observePage(page decodedCapturePageResult) {
	if page.barcodeStatus != "verified" {
		identity.unverifiedCapturePageIDs = append(identity.unverifiedCapturePageIDs, page.capturePageID)
		return
	}
	barcode := page.barcode
	if barcode.AssignedStudentID == "" || barcode.AssignedTemplateID == "" || barcode.AssignedSheetSerial == "" {
		return
	}
	if identity.sheetSerial == "" {
		identity.studentID, identity.templateID, identity.sheetSerial = barcode.AssignedStudentID, barcode.AssignedTemplateID, barcode.AssignedSheetSerial
	} else if identity.studentID != barcode.AssignedStudentID || identity.templateID != barcode.AssignedTemplateID || identity.sheetSerial != barcode.AssignedSheetSerial {
		identity.conflict = true
	}
	identity.verifiedPageNumbers[page.assignedPageNo] = true
}

// 已验证页的身份一致时可以绑定学生；无条码页仍单独转人工，混入不同纸张则整份标记冲突。
func reconcileDecodedFileIdentityTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, identity decodedFileIdentity) error {
	if identity.conflict {
		return markMixedDecodedSheetIdentityTx(ctx, tx, scope)
	}
	if identity.sheetSerial == "" {
		return nil
	}
	tenantID, examID, submissionID := scope.tenantID, scope.examID, scope.submissionID
	var expectedPageCount int
	if err := tx.QueryRowContext(ctx, `
SELECT page_count
FROM answer_sheet_template
WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL
`, tenantID, identity.templateID).Scan(&expectedPageCount); err != nil {
		return mapNotFound(err)
	}
	evidence, _ := json.Marshal(map[string]any{
		"method":       "controlled_barcode",
		"confidence":   1.0,
		"sheet_serial": identity.sheetSerial,
		"template_id":  identity.templateID,
	})
	var existingSubmissionID string
	existingErr := tx.QueryRowContext(ctx, `
SELECT id::text
FROM submission
WHERE tenant_id=$1 AND exam_id=$2::uuid AND student_id=$3::uuid
  AND id<>$4::uuid AND deleted_at IS NULL
FOR UPDATE
`, tenantID, examID, identity.studentID, submissionID).Scan(&existingSubmissionID)
	if existingErr != nil && existingErr != sql.ErrNoRows {
		return existingErr
	}
	if existingSubmissionID != "" {
		return markDecodedStudentConflictTx(ctx, tx, scope, identity, evidence, expectedPageCount, existingSubmissionID)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET student_id=$3::uuid,
    identity_status='matched',
    identity_revision=identity_revision+1,
    identity_evidence=$4,
    expected_page_count=$5,
    actual_page_count=$6,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID, identity.studentID, evidence, expectedPageCount, len(identity.verifiedPageNumbers)); err != nil {
		return err
	}
	return markUnverifiedDecodedPagesTx(ctx, tx, tenantID, identity.unverifiedCapturePageIDs)
}

// 已有有效提交时，新的扫描与纸张转冲突，不能覆盖先前学生归属。
func markDecodedStudentConflictTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, identity decodedFileIdentity, evidence []byte, expectedPageCount int, existingSubmissionID string) error {
	tenantID, submissionID := scope.tenantID, scope.submissionID
	if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',
    identity_revision=identity_revision+1,
    identity_evidence=$3::jsonb || jsonb_build_object(
      'conflict_code','student_already_has_submission',
      'conflict_submission_id',$4::text
    ),
    expected_page_count=$5,
    actual_page_count=$6,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID, string(evidence), existingSubmissionID, expectedPageCount, len(identity.verifiedPageNumbers)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status='needs_review',
    page_identity=page_identity || jsonb_build_object(
      'barcode_status','needs_review',
      'barcode_conflict_code','student_already_has_submission',
      'barcode_conflict_page_id',''
    ),
    revision=revision+1,
    updated_at=now()
WHERE tenant_id=$1
  AND submission_id=$2::uuid
  AND deleted_at IS NULL
`, tenantID, submissionID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status='conflict',updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, identity.sheetSerial)
	return err
}

func markUnverifiedDecodedPagesTx(ctx context.Context, tx *sql.Tx, tenantID string, pageIDs []string) error {
	for _, capturePageID := range pageIDs {
		if _, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status='needs_review',
    page_identity=page_identity || jsonb_build_object(
      'barcode_status','needs_review',
      'barcode_conflict_code','unbound_page_in_controlled_sheet'
    ),
    revision=revision+1,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, capturePageID); err != nil {
			return err
		}
	}
	return nil
}

func markMixedDecodedSheetIdentityTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope) error {
	_, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',
    identity_revision=identity_revision+1,
    identity_evidence=identity_evidence || jsonb_build_object(
      'method','controlled_barcode',
      'conflict_code','multiple_sheet_identities'
    ),
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, scope.tenantID, scope.submissionID)
	return err
}

func (s *PostgresStore) aggregateDecodedFileBatchesTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string, conflictBatchIDs map[string]bool) error {
	if err := s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return err
	}
	for conflictBatchID := range conflictBatchIDs {
		if conflictBatchID == batchID {
			continue
		}
		if err := s.aggregateBatchTx(ctx, tx, tenantID, conflictBatchID); err != nil {
			return err
		}
	}
	return nil
}

type sheetPageDuplicate struct {
	PageID       string
	SubmissionID string
	BatchID      string
}

func lockIssuedSheetAndFindDuplicateTx(ctx context.Context, tx *sql.Tx, tenantID, sheetSerial string, pageNo int) (sheetPageDuplicate, error) {
	var lockedSerial string
	if err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM answer_sheet_print_sheet
WHERE tenant_id=$1 AND id=$2::uuid AND status IN ('issued','observed','conflict')
FOR UPDATE
`, tenantID, sheetSerial).Scan(&lockedSerial); err != nil {
		return sheetPageDuplicate{}, mapNotFound(err)
	}
	var out sheetPageDuplicate
	err := tx.QueryRowContext(ctx, `
SELECT id::text,COALESCE(submission_id::text,''),capture_batch_id::text
FROM capture_page
WHERE tenant_id=$1 AND sheet_serial=$2::uuid AND assigned_page_no=$3
  AND status<>'deleted' AND deleted_at IS NULL
ORDER BY created_at,id
LIMIT 1
`, tenantID, sheetSerial, pageNo).Scan(&out.PageID, &out.SubmissionID, &out.BatchID)
	if err == sql.ErrNoRows {
		return sheetPageDuplicate{}, nil
	}
	if err != nil {
		return sheetPageDuplicate{}, err
	}
	return out, nil
}

func markSheetPageObservedTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, sheetSerial, capturePageID, submissionID string,
	duplicate sheetPageDuplicate,
) error {
	conflict := duplicate.PageID != ""
	if _, err := tx.ExecContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status=CASE
      WHEN $3::boolean THEN 'conflict'
      WHEN status='issued' THEN 'observed'
      ELSE status
    END,
    first_observed_at=COALESCE(first_observed_at,now()),
    last_observed_at=now(),
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, sheetSerial, conflict); err != nil {
		return err
	}
	if !conflict {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status='needs_review',
    page_identity=page_identity || jsonb_build_object(
      'barcode_status','needs_review',
      'barcode_conflict_code','sheet_page_duplicate',
      'barcode_conflict_page_id',$3::text
    ),
    revision=revision+1,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, duplicate.PageID, capturePageID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',
    identity_revision=identity_revision+1,
    identity_evidence=identity_evidence || jsonb_build_object(
      'method','controlled_barcode',
      'conflict_code','sheet_page_duplicate',
      'sheet_serial',$4::text
    ),
    updated_at=now()
WHERE tenant_id=$1
  AND id IN ($2::uuid,$3::uuid)
  AND deleted_at IS NULL
`, tenantID, submissionID, duplicate.SubmissionID, sheetSerial)
	return err
}
