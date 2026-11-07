package capture

// 解码结果仍由文件命令事务持有批次和文件锁；页面、质检排队和文件终态随调用方一起提交。
import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) ApplyFileResult(ctx context.Context, tenantID, fileID string, pages []DecodedPageInput) (File, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	item, err := s.applyFileResultInTx(ctx, tx, tenantID, fileID, pages)
	if err != nil {
		return File{}, err
	}
	return item, tx.Commit()
}

// applyFileResultInTx 沿用命令事务；调用方负责提交，任一步失败都回滚页面和质检排队。
func (s *PostgresStore) applyFileResultInTx(ctx context.Context, tx *sql.Tx, tenantID, fileID string, pages []DecodedPageInput) (File, error) {
	if err := validateDecodedFilePages(pages); err != nil {
		return File{}, err
	}
	if err := lockFileBatchTx(ctx, tx, tenantID, fileID); err != nil {
		return File{}, err
	}
	item, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID))
	if err != nil {
		return File{}, err
	}
	// 重复提交相同页数直接返回已完成文件；页数不同视为同一文件的冲突，避免追加第二套页面。
	if item.Status == "completed" {
		if item.PageCount == len(pages) {
			return item, nil
		}
		return File{}, ErrConflict
	}
	if item.Status != "queued" && item.Status != "processing" && item.Status != "failed" {
		return File{}, ErrInvalidTransition
	}
	scope, err := createDecodedSubmissionTx(ctx, tx, tenantID, item, len(pages))
	if err != nil {
		return File{}, err
	}
	identity, conflictBatchIDs, err := s.applyDecodedPagesTx(ctx, tx, scope, pages)
	if err != nil {
		return File{}, err
	}
	if item.UploadGroupID == "" {
		if err = reconcileDecodedFileIdentityTx(ctx, tx, scope, identity); err != nil {
			return File{}, err
		}
	}
	item, err = scanFile(tx.QueryRowContext(ctx, `UPDATE capture_file SET status='completed',page_count=$3,error_code=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+fileColumns, tenantID, fileID, len(pages)))
	if err != nil {
		return File{}, err
	}
	if item.UploadGroupID != "" {
		if err = s.finalizeUploadGroupTx(ctx, tx, scope); err != nil {
			return File{}, err
		}
	}
	if err = s.aggregateDecodedFileBatchesTx(ctx, tx, tenantID, item.CaptureBatchID, conflictBatchIDs); err != nil {
		return File{}, err
	}
	return item, nil
}

type decodedFileScope struct {
	tenantID, examID, submissionID string
	file                           File
	sequenceBase                   int
}

type decodedCapturePageResult struct {
	barcode                      barcodeEvaluation
	assignedPageNo, sequenceNo   int
	barcodeStatus, capturePageID string
	submissionPageID             string
	duplicate                    sheetPageDuplicate
}

func validateDecodedFilePages(pages []DecodedPageInput) error {
	if len(pages) == 0 {
		return ErrInvalidInput
	}
	for i, page := range pages {
		if page.SourceIndex != i+1 || page.FileAssetID == "" || page.SHA256 == "" || page.Width <= 0 || page.Height <= 0 || !validBarcodeObservations(page.Barcodes, page.Width, page.Height) {
			return ErrInvalidInput
		}
	}
	return nil
}

func createDecodedSubmissionTx(ctx context.Context, tx *sql.Tx, tenantID string, item File, pageCount int) (decodedFileScope, error) {
	var examID string
	if err := tx.QueryRowContext(ctx, `SELECT exam_id::text FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, item.CaptureBatchID).Scan(&examID); err != nil {
		return decodedFileScope{}, err
	}
	sourceType := "image_upload"
	if strings.Contains(item.ContentType, "pdf") {
		sourceType = "pdf_upload"
	}
	var submissionID string
	if err := tx.QueryRowContext(ctx, `INSERT INTO submission (tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by)
VALUES ($1,$2::uuid,$3,'pages_uploaded',$4,$4,'unchecked','[]',$5::uuid) RETURNING id::text`, tenantID, examID, sourceType, pageCount, item.UploadedBy).Scan(&submissionID); err != nil {
		return decodedFileScope{}, err
	}
	var sequenceBase int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence_no),0) FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL`, tenantID, item.CaptureBatchID).Scan(&sequenceBase); err != nil {
		return decodedFileScope{}, err
	}
	return decodedFileScope{tenantID: tenantID, examID: examID, submissionID: submissionID, file: item, sequenceBase: sequenceBase}, nil
}

func (s *PostgresStore) applyDecodedPagesTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, pages []DecodedPageInput) (decodedFileIdentity, map[string]bool, error) {
	identity := decodedFileIdentity{verifiedPageNumbers: map[int]bool{}}
	conflictBatchIDs := map[string]bool{}
	for i, page := range pages {
		result, err := s.applyDecodedPageTx(ctx, tx, scope, page, scope.sequenceBase+i+1)
		if err != nil {
			return decodedFileIdentity{}, nil, err
		}
		identity.observePage(result)
		if result.duplicate.BatchID != "" {
			conflictBatchIDs[result.duplicate.BatchID] = true
		}
	}
	return identity, conflictBatchIDs, nil
}

// 页面、纸张冲突与质检任务共用文件结果事务；任一步失败都不提交半套页面。
func (s *PostgresStore) applyDecodedPageTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, p DecodedPageInput, sequenceNo int) (decodedCapturePageResult, error) {
	tenantID, examID, submissionID := scope.tenantID, scope.examID, scope.submissionID
	barcode := s.evaluateBarcodesTx(ctx, tx, tenantID, examID, p.Barcodes)
	assignedPageNo := p.SourceIndex
	if barcode.AssignedPageNo > 0 {
		assignedPageNo = barcode.AssignedPageNo
	}
	var duplicate sheetPageDuplicate
	var err error
	if barcode.AssignedSheetSerial != "" {
		duplicate, err = lockIssuedSheetAndFindDuplicateTx(ctx, tx, tenantID, barcode.AssignedSheetSerial, assignedPageNo)
		if err != nil {
			return decodedCapturePageResult{}, err
		}
		if duplicate.PageID != "" {
			barcode.NeedsReview = true
		}
	}
	submissionPageID, err := insertDecodedSubmissionPageTx(ctx, tx, scope, p, assignedPageNo)
	if err != nil {
		return decodedCapturePageResult{}, err
	}
	result := decodedCapturePageResult{
		barcode: barcode, assignedPageNo: assignedPageNo, sequenceNo: sequenceNo,
		barcodeStatus: decodedBarcodeStatus(barcode), duplicate: duplicate, submissionPageID: submissionPageID,
	}
	result.capturePageID, err = insertDecodedCapturePageTx(ctx, tx, scope, p, result)
	if err != nil {
		return decodedCapturePageResult{}, err
	}
	if barcode.AssignedSheetSerial != "" {
		if err = markSheetPageObservedTx(ctx, tx, tenantID, barcode.AssignedSheetSerial, result.capturePageID, submissionID, duplicate); err != nil {
			return decodedCapturePageResult{}, err
		}
	}
	// 同组文件可能乱序解码；组齐后才创建质检任务，避免移动页面时破坏运行记录的答卷外键。
	if scope.file.UploadGroupID == "" {
		if err = queueDecodedPageQualityTx(ctx, tx, scope, p, submissionPageID, result.capturePageID); err != nil {
			return decodedCapturePageResult{}, err
		}
	}
	return result, nil
}

// 受控条码页码用于归组；同号扫描仍需独立保存，使用新的提交页号避开唯一约束。
func insertDecodedSubmissionPageTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, p DecodedPageInput, assignedPageNo int) (string, error) {
	tenantID, submissionID := scope.tenantID, scope.submissionID
	submissionPageNo := assignedPageNo
	var pageNumberExists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM submission_page
  WHERE tenant_id=$1 AND submission_id=$2::uuid AND page_no=$3 AND deleted_at IS NULL
)
`, tenantID, submissionID, submissionPageNo).Scan(&pageNumberExists); err != nil {
		return "", err
	}
	if pageNumberExists {
		if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(max(page_no),0)+1
FROM submission_page
WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NULL
`, tenantID, submissionID).Scan(&submissionPageNo); err != nil {
			return "", err
		}
	}
	var submissionPageID string
	err := tx.QueryRowContext(ctx, `INSERT INTO submission_page (tenant_id,submission_id,file_asset_id,page_no,status,quality_status,quality_override,quality_issues)
VALUES ($1,$2::uuid,$3::uuid,$4,'uploaded','unchecked','{}','[]') RETURNING id::text`, tenantID, submissionID, p.FileAssetID, submissionPageNo).Scan(&submissionPageID)
	if err != nil {
		return "", err
	}
	return submissionPageID, nil
}

func decodedBarcodeStatus(barcode barcodeEvaluation) string {
	if len(barcode.Candidates) == 1 && !barcode.NeedsReview {
		return "verified"
	}
	if barcode.NeedsReview {
		return "needs_review"
	}
	return "none"
}

func insertDecodedCapturePageTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, p DecodedPageInput, result decodedCapturePageResult) (string, error) {
	tenantID, submissionID, item, barcode := scope.tenantID, scope.submissionID, scope.file, result.barcode
	duplicate := result.duplicate
	identityFields := map[string]any{
		"decoder": "page-processing", "width": p.Width, "height": p.Height,
		"sha256": p.SHA256, "barcode_status": result.barcodeStatus, "barcode_observations": barcode.Evidence,
	}
	if duplicate.PageID != "" {
		identityFields["barcode_conflict_code"] = "sheet_page_duplicate"
		identityFields["barcode_conflict_page_id"] = duplicate.PageID
	}
	identity, _ := json.Marshal(identityFields)
	candidates, _ := json.Marshal(barcode.Candidates)
	pageStatus := "quality_checking"
	if item.UploadGroupID != "" {
		pageStatus = "decoded"
		identityFields["grouping_status"] = "waiting_files"
		identityFields["expected_pages"] = item.PagesPerStudent
		identityFields["upload_group_id"] = item.UploadGroupID
		identity, _ = json.Marshal(identityFields)
	}
	if duplicate.PageID != "" {
		pageStatus = "needs_review"
	}
	var capturePageID string
	err := tx.QueryRowContext(ctx, `INSERT INTO capture_page (
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
		result.submissionPageID, result.assignedPageNo, result.sequenceNo, p.FileAssetID,
		barcode.AssignedStudentID, barcode.AssignedTemplateID, barcode.AssignedSheetSerial,
		pageStatus, duplicate.PageID, identity, candidates).Scan(&capturePageID)
	return capturePageID, err
}

func queueDecodedPageQualityTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope, p DecodedPageInput, submissionPageID, capturePageID string) error {
	tenantID, submissionID, item := scope.tenantID, scope.submissionID, scope.file
	var qualityRunID string
	err := tx.QueryRowContext(ctx, `INSERT INTO submission_page_quality_run (
tenant_id,submission_id,submission_page_id,source_file_asset_id,source_sha256,processing_status,
profile_name,profile_version,profile_config_hash,metric_schema_version,report_schema_version)
VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,'pending','opencv-default','v2','sha256:opencv-default-v2-local-focus-hard-gates','image-quality-metrics-v2','image-quality-report-v2') RETURNING id::text`, tenantID, submissionID, submissionPageID, p.FileAssetID, p.SHA256).Scan(&qualityRunID)
	if err != nil {
		return err
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
