package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type PostgresStore struct {
	db             *sql.DB
	barcodeKeyring BarcodeKeyring
}

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func NewPostgresStoreWithBarcodeKeyring(db *sql.DB, keyring BarcodeKeyring) *PostgresStore {
	return &PostgresStore{db: db, barcodeKeyring: keyring}
}

const batchColumns = `id::text, tenant_id::text, exam_id::text, name, source_type, status, revision,
operator_id::text, COALESCE(scanner_device, ''), file_count, page_count, submission_count,
normal_count, review_count, failed_count, started_at, completed_at, created_at`

const fileColumns = `id::text, tenant_id::text, capture_batch_id::text, file_asset_id::text,
original_name, content_type, sha256, byte_size, page_count, status, COALESCE(error_code, ''),
idempotency_key, uploaded_by::text, created_at`

const pageColumns = `id::text, tenant_id::text, capture_batch_id::text, capture_file_id::text,
source_index, COALESCE(submission_id::text, ''), COALESCE(submission_page_id::text, ''),
COALESCE(assigned_page_no, 0), sequence_no, rotation_degrees, decoded_file_asset_id::text,
COALESCE(barcode_student_id::text, ''), COALESCE(barcode_template_id::text, ''),
COALESCE(sheet_serial::text, ''),
status, COALESCE(duplicate_of_page_id::text, ''), revision, page_identity, match_candidates,
manual_override, created_at`

func (s *PostgresStore) CreateBatch(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error) {
	if tenantID == "" || examID == "" || actorID == "" || validateCreateBatch(&input) != nil {
		return Batch{}, ErrInvalidInput
	}
	if input.IdempotencyKey != "" {
		return s.createBatchCommand(ctx, tenantID, examID, actorID, input)
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO capture_batch (tenant_id, exam_id, name, source_type, operator_id, scanner_device, idempotency_key)
VALUES ($1, $2::uuid, $3, $4, $5::uuid, NULLIF($6, ''), NULLIF($7, ''))
ON CONFLICT (tenant_id, exam_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key <> '' AND deleted_at IS NULL
DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
RETURNING `+batchColumns, tenantID, examID, input.Name, input.SourceType, actorID, input.ScannerDevice, input.IdempotencyKey)
	return scanBatch(row)
}

func (s *PostgresStore) ListBatches(ctx context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+batchColumns+` FROM capture_batch
WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL
  AND ($4 = '' OR created_at < $3 OR (created_at = $3 AND id::text < $4))
ORDER BY created_at DESC, id::text DESC
LIMIT NULLIF($5, 0)`, tenantID, examID, filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		item, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch
WHERE tenant_id = $1 AND id = $2::uuid AND deleted_at IS NULL`, tenantID, batchID))
}

func (s *PostgresStore) RegisterFile(ctx context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error) {
	if validateRegisterFile(&input, asset) != nil {
		return File{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return File{}, err
	}
	var examID, status string
	if err := tx.QueryRowContext(ctx, `SELECT exam_id::text, status FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&examID, &status); err != nil {
		return File{}, mapNotFound(err)
	}
	if status == "completed" || status == "cancelled" {
		return File{}, ErrInvalidTransition
	}
	if asset.ExamID != "" && asset.ExamID != examID {
		return File{}, ErrInvalidInput
	}
	duplicateStatus := "uploaded"
	var duplicateID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM capture_file
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND sha256=$3
  AND status IN ('uploaded','queued','processing','completed')
  AND deleted_at IS NULL
LIMIT 1`, tenantID, batchID, asset.SHA256).Scan(&duplicateID)
	if err == nil {
		duplicateStatus = "duplicate"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return File{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO capture_file (tenant_id, capture_batch_id, file_asset_id, original_name, content_type, sha256, byte_size, status, idempotency_key, uploaded_by)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid)
ON CONFLICT (tenant_id, capture_batch_id, idempotency_key)
DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
RETURNING `+fileColumns, tenantID, batchID, asset.ID, asset.OriginalName, asset.ContentType, asset.SHA256, asset.SizeBytes, duplicateStatus, input.IdempotencyKey, actorID)
	item, err := scanFile(row)
	if err != nil {
		return File{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE capture_batch SET status=CASE WHEN status='draft' THEN 'uploading' ELSE status END,
file_count=(SELECT count(*) FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL),
revision=revision+1, updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, batchID)
	if err != nil {
		return File{}, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) ListFiles(ctx context.Context, tenantID, batchID string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		item, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetFile(ctx context.Context, tenantID, fileID string) (File, error) {
	return scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, fileID))
}

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

// 事务锁住批次和待处理文件；失败文件会重置其任务租约，避免重试留下旧租约或重复页面。
func (s *PostgresStore) QueueBatch(ctx context.Context, tenantID, batchID, actorID string) (Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return Batch{}, err
	}
	var current, examID string
	if err := tx.QueryRowContext(ctx, `SELECT status, exam_id::text FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&current, &examID); err != nil {
		return Batch{}, mapNotFound(err)
	}
	if current == "completed" || current == "cancelled" {
		return Batch{}, ErrInvalidTransition
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+fileColumns+` FROM capture_file
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND status IN ('uploaded','failed')
  AND deleted_at IS NULL
ORDER BY created_at
FOR UPDATE`, tenantID, batchID)
	if err != nil {
		return Batch{}, err
	}
	files := []File{}
	for rows.Next() {
		item, err := scanFile(rows)
		if err != nil {
			rows.Close()
			return Batch{}, err
		}
		files = append(files, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Batch{}, err
	}
	if err := rows.Close(); err != nil {
		return Batch{}, err
	}
	if len(files) == 0 {
		return Batch{}, ErrInvalidTransition
	}
	for _, item := range files {
		payload, _ := json.Marshal(map[string]any{"capture_file_id": item.ID, "file_asset_id": item.FileAssetID, "exam_id": examID, "download_url": "/api/v1/files/" + item.FileAssetID + "/download", "content_type": item.ContentType, "sha256": item.SHA256, "max_pages": 500, "render_dpi": 300})
		key := "capture-decode:" + item.ID + ":v1"
		if item.Status == "failed" {
			result, retryErr := tx.ExecContext(ctx, `UPDATE agent_worker_task
SET status='queued',
    not_before=NULL,
    lease_token=NULL,
    lease_expires_at=NULL,
    leased_by=NULL,
    completed_at=NULL,
    max_attempts=GREATEST(max_attempts,attempt_count+1),
    updated_at=now()
WHERE tenant_id=$1
  AND source_type='capture_file'
  AND source_id=$2::uuid
  AND status IN ('failed','dead_letter')`, tenantID, item.ID)
			if retryErr != nil {
				return Batch{}, retryErr
			}
			affected, retryErr := result.RowsAffected()
			if retryErr != nil {
				return Batch{}, retryErr
			}
			if affected != 1 {
				return Batch{}, ErrConflict
			}
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1,'capture_file_decode','page-processing','capture_file',$2::uuid,50,$3,'capture-file-decode-v1',$4,$4,5,5,NULLIF($5,'')::uuid)
ON CONFLICT (tenant_id,task_type,idempotency_key) DO NOTHING`, tenantID, item.ID, payload, key, actorID)
			if err != nil {
				return Batch{}, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE capture_file SET status='queued',error_code=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, item.ID); err != nil {
			return Batch{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `UPDATE capture_batch SET status='processing',started_at=COALESCE(started_at,now()),revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+batchColumns, tenantID, batchID)
	out, err := scanBatch(row)
	if err != nil {
		return Batch{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) ApplyFileResult(ctx context.Context, tenantID, fileID string, pages []DecodedPageInput) (File, error) {
	if len(pages) == 0 {
		return File{}, ErrInvalidInput
	}
	for i, p := range pages {
		if p.SourceIndex != i+1 || p.FileAssetID == "" || p.SHA256 == "" || p.Width <= 0 || p.Height <= 0 || !validBarcodeObservations(p.Barcodes, p.Width, p.Height) {
			return File{}, ErrInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	item, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID))
	if err != nil {
		return File{}, err
	}
	// 重复提交相同页数直接返回已完成文件；页数不同视为同一文件的冲突，避免追加第二套页面。
	if item.Status == "completed" {
		if item.PageCount == len(pages) {
			return item, tx.Commit()
		}
		return File{}, ErrConflict
	}
	if item.Status != "queued" && item.Status != "processing" && item.Status != "failed" {
		return File{}, ErrInvalidTransition
	}
	var examID string
	if err = tx.QueryRowContext(ctx, `SELECT exam_id::text FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, item.CaptureBatchID).Scan(&examID); err != nil {
		return File{}, err
	}
	sourceType := "image_upload"
	if strings.Contains(item.ContentType, "pdf") {
		sourceType = "pdf_upload"
	}
	var submissionID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO submission (tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by)
VALUES ($1,$2::uuid,$3,'pages_uploaded',$4,$4,'unchecked','[]',$5::uuid) RETURNING id::text`, tenantID, examID, sourceType, len(pages), item.UploadedBy).Scan(&submissionID); err != nil {
		return File{}, err
	}
	var sequenceBase int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence_no),0) FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL`, tenantID, item.CaptureBatchID).Scan(&sequenceBase); err != nil {
		return File{}, err
	}
	conflictBatchIDs := map[string]bool{}
	verifiedPageNumbers := map[int]bool{}
	identifiedStudentID := ""
	identifiedTemplateID := ""
	identifiedSheetSerial := ""
	identityConflict := false
	unverifiedCapturePageIDs := []string{}
	for i, p := range pages {
		barcode := s.evaluateBarcodesTx(ctx, tx, tenantID, examID, p.Barcodes)
		assignedPageNo := p.SourceIndex
		if barcode.AssignedPageNo > 0 {
			assignedPageNo = barcode.AssignedPageNo
		}
		var duplicate sheetPageDuplicate
		if barcode.AssignedSheetSerial != "" {
			duplicate, err = lockIssuedSheetAndFindDuplicateTx(
				ctx, tx, tenantID, barcode.AssignedSheetSerial, assignedPageNo,
			)
			if err != nil {
				return File{}, err
			}
			if duplicate.PageID != "" {
				barcode.NeedsReview = true
			}
		}
		submissionPageNo := assignedPageNo
		var pageNumberExists bool
		if err = tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM submission_page
  WHERE tenant_id=$1 AND submission_id=$2::uuid AND page_no=$3 AND deleted_at IS NULL
)
`, tenantID, submissionID, submissionPageNo).Scan(&pageNumberExists); err != nil {
			return File{}, err
		}
		if pageNumberExists {
			if err = tx.QueryRowContext(ctx, `
SELECT COALESCE(max(page_no),0)+1
FROM submission_page
WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID).Scan(&submissionPageNo); err != nil {
				return File{}, err
			}
		}
		var submissionPageID string
		if err = tx.QueryRowContext(ctx, `INSERT INTO submission_page (tenant_id,submission_id,file_asset_id,page_no,status,quality_status,quality_override,quality_issues)
VALUES ($1,$2::uuid,$3::uuid,$4,'uploaded','unchecked','{}','[]') RETURNING id::text`, tenantID, submissionID, p.FileAssetID, submissionPageNo).Scan(&submissionPageID); err != nil {
			return File{}, err
		}
		barcodeStatus := "none"
		if len(barcode.Candidates) == 1 && !barcode.NeedsReview {
			barcodeStatus = "verified"
		} else if barcode.NeedsReview {
			barcodeStatus = "needs_review"
		}
		if barcodeStatus == "verified" && barcode.AssignedStudentID != "" &&
			barcode.AssignedTemplateID != "" && barcode.AssignedSheetSerial != "" {
			if identifiedSheetSerial == "" {
				identifiedStudentID = barcode.AssignedStudentID
				identifiedTemplateID = barcode.AssignedTemplateID
				identifiedSheetSerial = barcode.AssignedSheetSerial
			} else if identifiedStudentID != barcode.AssignedStudentID ||
				identifiedTemplateID != barcode.AssignedTemplateID ||
				identifiedSheetSerial != barcode.AssignedSheetSerial {
				identityConflict = true
			}
			verifiedPageNumbers[assignedPageNo] = true
		}
		identityFields := map[string]any{
			"decoder": "page-processing", "width": p.Width, "height": p.Height,
			"sha256": p.SHA256, "barcode_status": barcodeStatus, "barcode_observations": barcode.Evidence,
		}
		if duplicate.PageID != "" {
			identityFields["barcode_conflict_code"] = "sheet_page_duplicate"
			identityFields["barcode_conflict_page_id"] = duplicate.PageID
		}
		identity, _ := json.Marshal(identityFields)
		candidates, _ := json.Marshal(barcode.Candidates)
		pageStatus := "quality_checking"
		if duplicate.PageID != "" {
			pageStatus = "needs_review"
		}
		var capturePageID string
		err = tx.QueryRowContext(ctx, `INSERT INTO capture_page (
tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
assigned_page_no,sequence_no,decoded_file_asset_id,barcode_student_id,barcode_template_id,
sheet_serial,status,duplicate_of_page_id,page_identity,match_candidates
)
VALUES (
$1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,$9::uuid,
NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,NULLIF($12,'')::uuid,$13,
NULLIF($14,'')::uuid,$15,$16
)
RETURNING id::text`, tenantID, item.CaptureBatchID, item.ID, p.SourceIndex, submissionID,
			submissionPageID, assignedPageNo, sequenceBase+i+1, p.FileAssetID,
			barcode.AssignedStudentID, barcode.AssignedTemplateID, barcode.AssignedSheetSerial,
			pageStatus, duplicate.PageID, identity, candidates).Scan(&capturePageID)
		if err != nil {
			return File{}, err
		}
		if barcodeStatus != "verified" {
			unverifiedCapturePageIDs = append(unverifiedCapturePageIDs, capturePageID)
		}
		if barcode.AssignedSheetSerial != "" {
			if err = markSheetPageObservedTx(
				ctx, tx, tenantID, barcode.AssignedSheetSerial, capturePageID, submissionID, duplicate,
			); err != nil {
				return File{}, err
			}
			if duplicate.BatchID != "" {
				conflictBatchIDs[duplicate.BatchID] = true
			}
		}
		var qualityRunID string
		err = tx.QueryRowContext(ctx, `INSERT INTO submission_page_quality_run (
tenant_id,submission_id,submission_page_id,source_file_asset_id,source_sha256,processing_status,
profile_name,profile_version,profile_config_hash,metric_schema_version,report_schema_version)
VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,'pending','opencv-default','v2','sha256:opencv-default-v2-local-focus-hard-gates','image-quality-metrics-v2','image-quality-report-v2') RETURNING id::text`, tenantID, submissionID, submissionPageID, p.FileAssetID, p.SHA256).Scan(&qualityRunID)
		if err != nil {
			return File{}, err
		}
		payload, _ := json.Marshal(map[string]any{
			"run_id": qualityRunID, "submission_id": submissionID, "submission_page_id": submissionPageID,
			"capture_page_id": capturePageID, "page_no": p.SourceIndex, "source_file_asset_id": p.FileAssetID,
			"source_sha256": p.SHA256, "download_url": "/api/v1/files/" + p.FileAssetID + "/download",
			"profile_name": "opencv-default", "profile_version": "v2", "profile_config_hash": "sha256:opencv-default-v2-local-focus-hard-gates",
			"metric_schema_version": "image-quality-metrics-v2", "report_schema_version": "image-quality-report-v2",
		})
		key := "capture-quality:" + capturePageID + ":" + p.SHA256
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1,'image_quality','image-quality','image_quality_run',$2::uuid,70,$3,'image-quality.v1',$4,$4,3,30,$5::uuid)`, tenantID, qualityRunID, payload, key, item.UploadedBy)
		if err != nil {
			return File{}, err
		}
	}
	// 只有整批页面指向同一受控纸张且没有重复身份时才自动绑定学生，否则保留人工处理状态。
	if identifiedSheetSerial != "" && !identityConflict {
		var expectedPageCount int
		if err = tx.QueryRowContext(ctx, `
SELECT page_count
FROM answer_sheet_template
WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL
`, tenantID, identifiedTemplateID).Scan(&expectedPageCount); err != nil {
			return File{}, mapNotFound(err)
		}
		evidence, _ := json.Marshal(map[string]any{
			"method":       "controlled_barcode",
			"confidence":   1.0,
			"sheet_serial": identifiedSheetSerial,
			"template_id":  identifiedTemplateID,
		})
		var existingSubmissionID string
		existingErr := tx.QueryRowContext(ctx, `
SELECT id::text
FROM submission
WHERE tenant_id=$1 AND exam_id=$2::uuid AND student_id=$3::uuid
  AND id<>$4::uuid AND deleted_at IS NULL
FOR UPDATE
`, tenantID, examID, identifiedStudentID, submissionID).Scan(&existingSubmissionID)
		if existingErr != nil && existingErr != sql.ErrNoRows {
			return File{}, existingErr
		}
		if existingSubmissionID != "" {
			if _, err = tx.ExecContext(ctx, `
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
`, tenantID, submissionID, string(evidence), existingSubmissionID, expectedPageCount, len(verifiedPageNumbers)); err != nil {
				return File{}, err
			}
			if _, err = tx.ExecContext(ctx, `
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
				return File{}, err
			}
			if _, err = tx.ExecContext(ctx, `
UPDATE answer_sheet_print_sheet
SET status='conflict',updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, identifiedSheetSerial); err != nil {
				return File{}, err
			}
		} else if _, err = tx.ExecContext(ctx, `
UPDATE submission
SET student_id=$3::uuid,
    identity_status='matched',
    identity_revision=identity_revision+1,
    identity_evidence=$4,
    expected_page_count=$5,
    actual_page_count=$6,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID, identifiedStudentID, evidence, expectedPageCount, len(verifiedPageNumbers)); err != nil {
			return File{}, err
		}
		for _, capturePageID := range unverifiedCapturePageIDs {
			if existingSubmissionID != "" {
				break
			}
			if _, err = tx.ExecContext(ctx, `
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
				return File{}, err
			}
		}
	} else if identityConflict {
		if _, err = tx.ExecContext(ctx, `
UPDATE submission
SET identity_status='conflict',
    identity_revision=identity_revision+1,
    identity_evidence=identity_evidence || jsonb_build_object(
      'method','controlled_barcode',
      'conflict_code','multiple_sheet_identities'
    ),
    updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID); err != nil {
			return File{}, err
		}
	}
	item, err = scanFile(tx.QueryRowContext(ctx, `UPDATE capture_file SET status='completed',page_count=$3,error_code=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+fileColumns, tenantID, fileID, len(pages)))
	if err != nil {
		return File{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, item.CaptureBatchID); err != nil {
		return File{}, err
	}
	for conflictBatchID := range conflictBatchIDs {
		if conflictBatchID == item.CaptureBatchID {
			continue
		}
		if err = s.aggregateBatchTx(ctx, tx, tenantID, conflictBatchID); err != nil {
			return File{}, err
		}
	}
	return item, tx.Commit()
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

func validBarcodeObservations(items []BarcodeObservation, width, height int) bool {
	if len(items) > 8 {
		return false
	}
	for _, item := range items {
		if item.Format == "" || item.Text == "" || len(item.Text) > 2048 || len(item.Polygon) != 4 {
			return false
		}
		for _, point := range item.Polygon {
			if point["x"] < 0 || point["y"] < 0 || point["x"] >= width || point["y"] >= height {
				return false
			}
		}
	}
	return true
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

// ApplyQualityOutcomeInTx updates a linked capture page and aggregate. A
// submission created outside capture is reported as linked=false, not an error.
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

func (s *PostgresStore) ApplyFileFailure(ctx context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error) {
	status := "failed"
	if retryable {
		status = "queued"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	var batchID string
	if err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID).Scan(&batchID); err != nil {
		return File{}, mapNotFound(err)
	}
	out, err := scanFile(tx.QueryRowContext(ctx, `UPDATE capture_file SET status=$3,error_code=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL RETURNING `+fileColumns, tenantID, fileID, status, strings.TrimSpace(errorCode)))
	if err != nil {
		return File{}, err
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return File{}, err
	}
	return out, tx.Commit()
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

func (s *PostgresStore) SetBatchStatus(ctx context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	current, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID))
	if err != nil {
		return Batch{}, err
	}
	if !canSetBatchStatus(current.Status, status) {
		return Batch{}, ErrInvalidTransition
	}
	out, err := scanBatch(tx.QueryRowContext(ctx, `UPDATE capture_batch SET status=$3,revision=revision+1,completed_at=CASE WHEN $3='completed' THEN now() ELSE NULL END,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+batchColumns, tenantID, batchID, status))
	if err != nil {
		return Batch{}, err
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(out)
	op := status
	if status == "completed" {
		op = "complete"
	}
	if status == "draft" {
		op = "reopen"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_operation (tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES ($1,$2::uuid,$3,'capture_batch',$2::uuid,$4::uuid,NULLIF($5,''),$6,$7)`, tenantID, batchID, op, actorID, strings.TrimSpace(reason), before, after)
	if err != nil {
		return Batch{}, err
	}
	return out, tx.Commit()
}

// 聚合状态按文件失败、页面质量、身份匹配和页面就绪的优先级计算，供批次列表统一展示。
func aggregateBatchTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE capture_batch b SET
file_count=(SELECT count(*) FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.deleted_at IS NULL),
page_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL),
submission_count=(SELECT count(DISTINCT p.submission_id) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL),
normal_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status='ready' AND p.deleted_at IS NULL),
failed_count=(SELECT count(*) FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status='failed' AND f.deleted_at IS NULL),
review_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status IN ('needs_review','quality_rejected','failed') AND p.deleted_at IS NULL),
status=CASE
 WHEN EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status IN ('uploaded','queued','processing') AND f.deleted_at IS NULL) THEN 'processing'
 WHEN EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status='failed' AND f.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status IN ('needs_review','quality_rejected','failed') AND p.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND s.identity_status IN ('unknown','conflict') AND p.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND s.identity_status<>'matched' AND p.deleted_at IS NULL) THEN 'matching'
 WHEN EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status NOT IN ('ready','deleted') AND p.deleted_at IS NULL) THEN 'ready'
 ELSE 'matching' END,
revision=revision+1,updated_at=now() WHERE b.tenant_id=$1 AND b.id=$2::uuid`, tenantID, batchID)
	return err
}

func (s *PostgresStore) aggregateBatchTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	return aggregateBatchTx(ctx, tx, tenantID, batchID)
}

func ensureBatchWritableTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&status); err != nil {
		return mapNotFound(err)
	}
	if status == "completed" || status == "cancelled" {
		return ErrInvalidTransition
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanBatch(r scanner) (Batch, error) {
	var x Batch
	var a, b sql.NullTime
	err := r.Scan(&x.ID, &x.TenantID, &x.ExamID, &x.Name, &x.SourceType, &x.Status, &x.Revision, &x.OperatorID, &x.ScannerDevice, &x.FileCount, &x.PageCount, &x.SubmissionCount, &x.NormalCount, &x.ReviewCount, &x.FailedCount, &a, &b, &x.CreatedAt)
	if err != nil {
		return Batch{}, mapNotFound(err)
	}
	if a.Valid {
		v := a.Time.UTC()
		x.StartedAt = &v
	}
	if b.Valid {
		v := b.Time.UTC()
		x.CompletedAt = &v
	}
	return x, nil
}
func scanFile(r scanner) (File, error) {
	var x File
	err := r.Scan(&x.ID, &x.TenantID, &x.CaptureBatchID, &x.FileAssetID, &x.OriginalName, &x.ContentType, &x.SHA256, &x.ByteSize, &x.PageCount, &x.Status, &x.ErrorCode, &x.IdempotencyKey, &x.UploadedBy, &x.CreatedAt)
	if err != nil {
		return File{}, mapNotFound(err)
	}
	return x, nil
}

// 页面附带的 JSON 证据来自历史行；解析失败不让整条记录消失，空值统一成可安全遍历的空集合。
func scanPage(r scanner) (Page, error) {
	var x Page
	var identity, candidates, override []byte
	err := r.Scan(
		&x.ID, &x.TenantID, &x.CaptureBatchID, &x.CaptureFileID, &x.SourceIndex,
		&x.SubmissionID, &x.SubmissionPageID, &x.AssignedPageNo, &x.SequenceNo,
		&x.RotationDegrees, &x.DecodedFileAssetID, &x.BarcodeStudentID,
		&x.BarcodeTemplateID, &x.SheetSerial, &x.Status, &x.DuplicateOfPageID,
		&x.Revision, &identity, &candidates, &override, &x.CreatedAt,
	)
	if err != nil {
		return Page{}, mapNotFound(err)
	}
	_ = json.Unmarshal(identity, &x.PageIdentity)
	_ = json.Unmarshal(candidates, &x.MatchCandidates)
	_ = json.Unmarshal(override, &x.ManualOverride)
	if x.PageIdentity == nil {
		x.PageIdentity = map[string]any{}
	}
	if x.MatchCandidates == nil {
		x.MatchCandidates = []any{}
	}
	if x.ManualOverride == nil {
		x.ManualOverride = map[string]any{}
	}
	return x, nil
}
func mapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
