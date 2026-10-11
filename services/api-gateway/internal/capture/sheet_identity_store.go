package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const studentSheetColumns = `
id::text,print_batch_id::text,exam_id::text,template_id::text,
template_content_hash,student_id::text,status,
COALESCE(supersedes_sheet_id::text,''),COALESCE(revoked_by::text,''),
revoked_at,COALESCE(revoke_reason,''),first_observed_at,last_observed_at`

func (s *PostgresStore) RevokeStudentSheet(
	ctx context.Context,
	tenantID, sheetSerial, actorID string,
	input StudentSheetLifecycleInput,
) (StudentSheet, error) {
	reason := strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(sheetSerial); err != nil || len(reason) == 0 || len(reason) > 500 {
		return StudentSheet{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StudentSheet{}, err
	}
	defer tx.Rollback()
	current, err := scanStudentSheet(tx.QueryRowContext(ctx, `
SELECT `+studentSheetColumns+`
FROM answer_sheet_print_sheet
WHERE tenant_id=$1 AND id=$2::uuid
FOR UPDATE
`, tenantID, sheetSerial))
	if err != nil {
		return StudentSheet{}, err
	}
	if current.Status == "revoked" {
		return StudentSheet{}, ErrInvalidTransition
	}
	out, err := scanStudentSheet(tx.QueryRowContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status='revoked',revoked_by=$3::uuid,revoked_at=now(),revoke_reason=$4,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
RETURNING `+studentSheetColumns, tenantID, sheetSerial, actorID, reason))
	if err != nil {
		return StudentSheet{}, err
	}
	if err = s.reconcileSheetSerialTx(ctx, tx, tenantID, sheetSerial); err != nil {
		return StudentSheet{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) ReprintStudentSheet(
	ctx context.Context,
	tenantID, sheetSerial, actorID string,
	input ReprintStudentSheetInput,
) (IssuedStudentBarcodes, error) {
	reason := strings.TrimSpace(input.Reason)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if _, err := uuid.Parse(sheetSerial); err != nil ||
		len(reason) == 0 || len(reason) > 500 ||
		len(idempotencyKey) == 0 || len(idempotencyKey) > 120 {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedStudentBarcodes{}, err
	}
	defer tx.Rollback()
	current, err := scanStudentSheet(tx.QueryRowContext(ctx, `
SELECT `+studentSheetColumns+`
FROM answer_sheet_print_sheet
WHERE tenant_id=$1 AND id=$2::uuid
FOR UPDATE
`, tenantID, sheetSerial))
	if err != nil {
		return IssuedStudentBarcodes{}, err
	}

	var existingBatchID, existingSupersedes string
	err = tx.QueryRowContext(ctx, `
SELECT b.id::text,COALESCE(s.supersedes_sheet_id::text,'')
FROM answer_sheet_print_batch b
LEFT JOIN answer_sheet_print_sheet s
  ON s.tenant_id=b.tenant_id AND s.print_batch_id=b.id
WHERE b.tenant_id=$1 AND b.template_id=$2::uuid AND b.idempotency_key=$3
`, tenantID, current.TemplateID, idempotencyKey).Scan(&existingBatchID, &existingSupersedes)
	if err == nil {
		if existingSupersedes != sheetSerial {
			return IssuedStudentBarcodes{}, ErrConflict
		}
		out, loadErr := loadIssuedStudentBarcodesTx(ctx, tx, tenantID, existingBatchID)
		if loadErr != nil {
			return IssuedStudentBarcodes{}, loadErr
		}
		return out, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return IssuedStudentBarcodes{}, err
	}
	var replacementBatchID string
	err = tx.QueryRowContext(ctx, `
SELECT print_batch_id::text
FROM answer_sheet_print_sheet
WHERE tenant_id=$1 AND supersedes_sheet_id=$2::uuid
`, tenantID, sheetSerial).Scan(&replacementBatchID)
	if err == nil {
		return IssuedStudentBarcodes{}, ErrConflict
	}
	if err != sql.ErrNoRows {
		return IssuedStudentBarcodes{}, err
	}

	var layoutRaw []byte
	var contentHash string
	if err = tx.QueryRowContext(ctx, `
SELECT content_hash,layout
FROM answer_sheet_template
WHERE tenant_id=$1 AND id=$2::uuid AND exam_id=$3::uuid
  AND status='locked' AND deleted_at IS NULL
FOR SHARE
`, tenantID, current.TemplateID, current.ExamID).Scan(&contentHash, &layoutRaw); err != nil {
		return IssuedStudentBarcodes{}, ErrInvalidTransition
	}
	if contentHash != current.TemplateContentHash {
		return IssuedStudentBarcodes{}, ErrInvalidTransition
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil || len(layout.Pages) == 0 {
		return IssuedStudentBarcodes{}, ErrInvalidTransition
	}
	var inRoster bool
	if err = tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id=$1 AND candidate.exam_id=$2::uuid
    AND candidate.student_id=$3::uuid
)
`, tenantID, current.ExamID, current.StudentID).Scan(&inRoster); err != nil {
		return IssuedStudentBarcodes{}, err
	}
	if !inRoster {
		return IssuedStudentBarcodes{}, ErrInvalidTransition
	}
	requestHash := studentBarcodeRequestHash([]string{current.StudentID})
	var printBatchID string
	var issuedAt time.Time
	if err = tx.QueryRowContext(ctx, `
INSERT INTO answer_sheet_print_batch (
  tenant_id,exam_id,template_id,template_content_hash,key_id,idempotency_key,
  request_hash,sheet_count,page_count,issued_by,operation,reason
)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,1,$8,$9::uuid,'reprint',$10)
RETURNING id::text,issued_at
`, tenantID, current.ExamID, current.TemplateID, contentHash, s.barcodeKeyring.ActiveKeyID,
		idempotencyKey, requestHash, len(layout.Pages), actorID, reason).Scan(&printBatchID, &issuedAt); err != nil {
		return IssuedStudentBarcodes{}, err
	}

	// 新序列签发、旧序列撤销及已扫描页的冲突标记共用事务，不产生两个同时有效的替代品。
	newSerial := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_sheet_print_sheet (
  id,tenant_id,print_batch_id,exam_id,template_id,template_content_hash,
  student_id,ordinal,supersedes_sheet_id
)
VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,1,$8::uuid)
`, newSerial, tenantID, printBatchID, current.ExamID, current.TemplateID,
		contentHash, current.StudentID, sheetSerial); err != nil {
		return IssuedStudentBarcodes{}, err
	}
	set := IssuedStudentBarcodeSet{
		StudentID: current.StudentID, SheetSerial: newSerial, Pages: []IssuedPageBarcode{},
	}
	for _, page := range layout.Pages {
		nonce := uuid.NewString()
		value, signErr := s.barcodeKeyring.Sign(BarcodeClaims{
			TenantID: tenantID, ExamID: current.ExamID, TemplateID: current.TemplateID,
			TemplateContentHash: contentHash, PageNo: page.PageNo, Nonce: nonce,
			StudentID: current.StudentID, SheetSerial: newSerial,
		})
		if signErr != nil {
			return IssuedStudentBarcodes{}, ErrInvalidTransition
		}
		if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_sheet_print_page (tenant_id,print_sheet_id,page_no,nonce,barcode_value)
VALUES ($1,$2::uuid,$3,$4::uuid,$5)
`, tenantID, newSerial, page.PageNo, nonce, value); err != nil {
			return IssuedStudentBarcodes{}, err
		}
		set.Pages = append(set.Pages, IssuedPageBarcode{PageNo: page.PageNo, Value: value})
	}
	if current.Status != "revoked" {
		if _, err = tx.ExecContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status='revoked',revoked_by=$3::uuid,revoked_at=now(),revoke_reason=$4,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, sheetSerial, actorID, reason); err != nil {
			return IssuedStudentBarcodes{}, err
		}
	}
	if err = s.reconcileSheetSerialTx(ctx, tx, tenantID, sheetSerial); err != nil {
		return IssuedStudentBarcodes{}, err
	}
	out := IssuedStudentBarcodes{
		PrintBatchID: printBatchID, TemplateID: current.TemplateID,
		TemplateContentHash: contentHash, KeyID: s.barcodeKeyring.ActiveKeyID,
		Students: []IssuedStudentBarcodeSet{set}, IssuedAt: issuedAt.UTC(),
	}
	return out, tx.Commit()
}

type sheetCapturePageState struct {
	PageID         string
	SubmissionID   string
	BatchID        string
	PageNo         int
	Status         string
	QualityStatus  string
	ConflictCode   string
	ConflictPageID string
}

// 纸张状态与所有已扫描页一起锁定重算；撤销、重复和冲突都要回写页面与批次聚合。
func (s *PostgresStore) reconcileSheetSerialTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, sheetSerial string,
) error {
	var sheetStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT status
FROM answer_sheet_print_sheet
WHERE tenant_id=$1 AND id=$2::uuid
FOR UPDATE
`, tenantID, sheetSerial).Scan(&sheetStatus); err != nil {
		return mapNotFound(err)
	}
	pages, err := loadSheetCapturePagesTx(ctx, tx, tenantID, sheetSerial)
	if err != nil {
		return err
	}
	groups := groupSheetCapturePages(pages)
	if sheetStatus == "revoked" {
		if err = markRevokedSheetCapturesTx(ctx, tx, tenantID, sheetSerial, groups); err != nil {
			return err
		}
		return s.aggregateSheetBatchesTx(ctx, tx, tenantID, groups.batchIDs)
	}
	hasDuplicate, err := reconcileActiveSheetGroupsTx(ctx, tx, tenantID, groups.activeGroups)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status=$3,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, sheetSerial, sheetCaptureStatus(len(pages), hasDuplicate)); err != nil {
		return err
	}
	if err = reconcileSheetSubmissionsTx(ctx, tx, tenantID, sheetSerial, groups.submissionIDs); err != nil {
		return err
	}
	return s.aggregateSheetBatchesTx(ctx, tx, tenantID, groups.batchIDs)
}

func loadSheetCapturePagesTx(ctx context.Context, tx *sql.Tx, tenantID, sheetSerial string) ([]sheetCapturePageState, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT cp.id::text,COALESCE(cp.submission_id::text,''),cp.capture_batch_id::text,
       COALESCE(cp.assigned_page_no,0),cp.status,COALESCE(sp.quality_status,'unchecked'),
       COALESCE(cp.page_identity->>'barcode_conflict_code',''),
       COALESCE(cp.page_identity->>'barcode_conflict_page_id','')
FROM capture_page cp
LEFT JOIN submission_page sp
  ON sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id
WHERE cp.tenant_id=$1 AND cp.sheet_serial=$2::uuid AND cp.deleted_at IS NULL
ORDER BY cp.created_at,cp.id
FOR UPDATE OF cp
`, tenantID, sheetSerial)
	if err != nil {
		return nil, err
	}
	pages := []sheetCapturePageState{}
	for rows.Next() {
		var page sheetCapturePageState
		if err = rows.Scan(
			&page.PageID, &page.SubmissionID, &page.BatchID, &page.PageNo,
			&page.Status, &page.QualityStatus, &page.ConflictCode, &page.ConflictPageID,
		); err != nil {
			rows.Close()
			return nil, err
		}
		pages = append(pages, page)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return pages, nil
}

type sheetCaptureGroups struct {
	activeGroups        map[int][]sheetCapturePageState
	submissionIDs       map[string]bool
	activeSubmissionIDs map[string]bool
	batchIDs            map[string]bool
}

func groupSheetCapturePages(pages []sheetCapturePageState) sheetCaptureGroups {
	groups := sheetCaptureGroups{
		activeGroups: map[int][]sheetCapturePageState{}, submissionIDs: map[string]bool{},
		activeSubmissionIDs: map[string]bool{}, batchIDs: map[string]bool{},
	}
	for _, page := range pages {
		if page.SubmissionID != "" {
			groups.submissionIDs[page.SubmissionID] = true
		}
		groups.batchIDs[page.BatchID] = true
		if page.Status != "deleted" {
			groups.activeGroups[page.PageNo] = append(groups.activeGroups[page.PageNo], page)
			if page.SubmissionID != "" {
				groups.activeSubmissionIDs[page.SubmissionID] = true
			}
		}
	}
	return groups
}

func markRevokedSheetCapturesTx(ctx context.Context, tx *sql.Tx, tenantID, sheetSerial string, groups sheetCaptureGroups) error {
	for _, group := range groups.activeGroups {
		for _, page := range group {
			if _, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status='needs_review',
    page_identity=page_identity || jsonb_build_object(
      'barcode_status','needs_review',
      'barcode_conflict_code','sheet_revoked',
      'barcode_conflict_page_id',$3::text
    ),
    revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, page.PageID, sheetSerial); err != nil {
				return err
			}
		}
	}
	for submissionID := range groups.activeSubmissionIDs {
		if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',identity_revision=identity_revision+1,
    identity_evidence=identity_evidence || jsonb_build_object(
      'method','controlled_barcode',
      'conflict_code','sheet_revoked',
      'sheet_serial',$3::text
    ),
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID, sheetSerial); err != nil {
			return err
		}
	}
	return nil
}

func reconcileActiveSheetGroupsTx(ctx context.Context, tx *sql.Tx, tenantID string, activeGroups map[int][]sheetCapturePageState) (bool, error) {
	hasDuplicate := false
	for _, group := range activeGroups {
		if len(group) > 1 {
			hasDuplicate = true
			if err := markDuplicateSheetGroupTx(ctx, tx, tenantID, group); err != nil {
				return false, err
			}
			continue
		}
		if err := recoverSheetDuplicatePageTx(ctx, tx, tenantID, group[0]); err != nil {
			return false, err
		}
	}
	return hasDuplicate, nil
}

func markDuplicateSheetGroupTx(ctx context.Context, tx *sql.Tx, tenantID string, group []sheetCapturePageState) error {
	firstPageID := group[0].PageID
	for _, page := range group {
		conflictPageID := firstPageID
		if page.PageID == firstPageID {
			conflictPageID = group[1].PageID
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status='needs_review',
    duplicate_of_page_id=CASE
      WHEN id=$3::uuid THEN duplicate_of_page_id
      ELSE COALESCE(duplicate_of_page_id,$3::uuid)
    END,
    page_identity=page_identity || jsonb_build_object(
      'barcode_status','needs_review',
      'barcode_conflict_code','sheet_page_duplicate',
      'barcode_conflict_page_id',$4::text
    ),
    revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, page.PageID, firstPageID, conflictPageID); err != nil {
			return err
		}
	}
	return nil
}

// 删除重复扫描后只解除重复页这一种冲突，并回到质检结果对应阶段，不直接标为就绪。
func recoverSheetDuplicatePageTx(ctx context.Context, tx *sql.Tx, tenantID string, page sheetCapturePageState) error {
	if page.ConflictCode != "sheet_page_duplicate" {
		return nil
	}
	recoveredStatus := recoveredSheetPageStatus(page.QualityStatus)
	_, err := tx.ExecContext(ctx, `
UPDATE capture_page
SET status=$3,
    duplicate_of_page_id=NULL,
    page_identity=(page_identity - 'barcode_conflict_code' - 'barcode_conflict_page_id')
      || jsonb_build_object('barcode_status','verified'),
    revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, page.PageID, recoveredStatus)
	return err
}

func recoveredSheetPageStatus(qualityStatus string) string {
	switch qualityStatus {
	case "passed":
		return "normalized"
	case "review":
		return "needs_review"
	case "failed":
		return "quality_rejected"
	default:
		return "quality_checking"
	}
}

func sheetCaptureStatus(pageCount int, hasDuplicate bool) string {
	if hasDuplicate {
		return "conflict"
	}
	if pageCount > 0 {
		return "observed"
	}
	return "issued"
}

func reconcileSheetSubmissionsTx(ctx context.Context, tx *sql.Tx, tenantID, sheetSerial string, submissionIDs map[string]bool) error {
	for submissionID := range submissionIDs {
		var hasActiveDuplicate bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM capture_page
  WHERE tenant_id=$1 AND submission_id=$2::uuid
    AND status<>'deleted' AND deleted_at IS NULL
    AND page_identity->>'barcode_conflict_code'='sheet_page_duplicate'
)
`, tenantID, submissionID).Scan(&hasActiveDuplicate); err != nil {
			return err
		}
		if hasActiveDuplicate {
			if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',identity_revision=identity_revision+1,
    identity_evidence=identity_evidence || jsonb_build_object(
      'method','controlled_barcode',
      'conflict_code','sheet_page_duplicate',
      'sheet_serial',$3::text
    ),
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID, sheetSerial); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='unassigned',identity_revision=identity_revision+1,
    identity_evidence=identity_evidence - 'method' - 'conflict_code' - 'sheet_serial',
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
  AND identity_status='conflict'
  AND identity_evidence->>'conflict_code'='sheet_page_duplicate'
`, tenantID, submissionID); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) aggregateSheetBatchesTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	batchIDs map[string]bool,
) error {
	for batchID := range batchIDs {
		if err := s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
			return err
		}
	}
	return nil
}

func scanStudentSheet(row scanner) (StudentSheet, error) {
	var out StudentSheet
	var revokedAt, firstObservedAt, lastObservedAt sql.NullTime
	if err := row.Scan(
		&out.SheetSerial, &out.PrintBatchID, &out.ExamID, &out.TemplateID,
		&out.TemplateContentHash, &out.StudentID, &out.Status,
		&out.SupersedesSheetID, &out.RevokedBy, &revokedAt, &out.RevokeReason,
		&firstObservedAt, &lastObservedAt,
	); err != nil {
		return StudentSheet{}, mapNotFound(err)
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		out.RevokedAt = &value
	}
	if firstObservedAt.Valid {
		value := firstObservedAt.Time.UTC()
		out.FirstObservedAt = &value
	}
	if lastObservedAt.Valid {
		value := lastObservedAt.Time.UTC()
		out.LastObservedAt = &value
	}
	return out, nil
}
