package capture

import (
	"database/sql"
	"encoding/json"
	"errors"
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
normal_count, review_count, failed_count, started_at, completed_at, created_at, purpose`

const fileColumns = `id::text, tenant_id::text, capture_batch_id::text, file_asset_id::text,
original_name, content_type, sha256, byte_size, page_count, status, COALESCE(error_code, ''),
idempotency_key, uploaded_by::text, created_at, COALESCE(upload_group_id::text,''),
COALESCE(upload_group_file_count,0), COALESCE(upload_group_file_index,0), COALESCE(pages_per_student,0)`

const pageColumns = `id::text, tenant_id::text, capture_batch_id::text, capture_file_id::text,
source_index, COALESCE(submission_id::text, ''), COALESCE(submission_page_id::text, ''),
COALESCE(assigned_page_no, 0), sequence_no, rotation_degrees, decoded_file_asset_id::text,
COALESCE(barcode_student_id::text, ''), COALESCE(barcode_template_id::text, ''),
COALESCE(sheet_serial::text, ''),
status, COALESCE(duplicate_of_page_id::text, ''), revision, page_identity, match_candidates,
manual_override, created_at`

type scanner interface{ Scan(...any) error }

func scanBatch(r scanner) (Batch, error) {
	var x Batch
	var a, b sql.NullTime
	err := r.Scan(&x.ID, &x.TenantID, &x.ExamID, &x.Name, &x.SourceType, &x.Status, &x.Revision, &x.OperatorID, &x.ScannerDevice, &x.FileCount, &x.PageCount, &x.SubmissionCount, &x.NormalCount, &x.ReviewCount, &x.FailedCount, &a, &b, &x.CreatedAt, &x.Purpose)
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
	err := r.Scan(&x.ID, &x.TenantID, &x.CaptureBatchID, &x.FileAssetID, &x.OriginalName, &x.ContentType, &x.SHA256, &x.ByteSize, &x.PageCount, &x.Status, &x.ErrorCode, &x.IdempotencyKey, &x.UploadedBy, &x.CreatedAt, &x.UploadGroupID, &x.UploadGroupFileCount, &x.UploadGroupFileIndex, &x.PagesPerStudent)
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
