package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 只从锁定模板的实际页码签发通用版本 1 条码；模板未锁定或布局无效时不生成可扫描凭据。
func (s *PostgresStore) IssueTemplateBarcodes(ctx context.Context, tenantID, templateID string) (IssuedTemplateBarcodes, error) {
	var examID, contentHash string
	var layoutRaw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT exam_id::text,content_hash,layout FROM answer_sheet_template WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL`, tenantID, templateID).Scan(&examID, &contentHash, &layoutRaw); err != nil {
		return IssuedTemplateBarcodes{}, mapNotFound(err)
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil || len(layout.Pages) == 0 {
		return IssuedTemplateBarcodes{}, ErrInvalidInput
	}
	out := IssuedTemplateBarcodes{TemplateID: templateID, TemplateContentHash: contentHash, KeyID: s.barcodeKeyring.ActiveKeyID, Pages: []IssuedPageBarcode{}}
	for _, page := range layout.Pages {
		value, err := s.barcodeKeyring.Sign(BarcodeClaims{TenantID: tenantID, ExamID: examID, TemplateID: templateID, TemplateContentHash: contentHash, PageNo: page.PageNo, Nonce: uuid.NewString()})
		if err != nil {
			return IssuedTemplateBarcodes{}, ErrInvalidTransition
		}
		out.Pages = append(out.Pages, IssuedPageBarcode{PageNo: page.PageNo, Value: value})
	}
	return out, nil
}

// IssueStudentBarcodes signs one barcode set per student for a locked template.
// Every sheet gets a fresh serial, so two printouts for the same student are
// distinguishable and a re-observed serial can be routed to manual review.
// Identity in the claims is the student row UUID — school numbers or names
// never enter the printed payload.
func (s *PostgresStore) IssueStudentBarcodes(ctx context.Context, tenantID, templateID, actorID string, input IssueStudentBarcodesInput) (IssuedStudentBarcodes, error) {
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if len(input.StudentIDs) == 0 || len(input.StudentIDs) > 2000 || len(idempotencyKey) == 0 || len(idempotencyKey) > 120 {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}
	studentIDs := make([]string, 0, len(input.StudentIDs))
	seen := map[string]bool{}
	for _, rawStudentID := range input.StudentIDs {
		parsed, err := uuid.Parse(strings.TrimSpace(rawStudentID))
		if err != nil {
			return IssuedStudentBarcodes{}, ErrInvalidInput
		}
		studentID := parsed.String()
		if seen[studentID] {
			return IssuedStudentBarcodes{}, ErrInvalidInput
		}
		seen[studentID] = true
		studentIDs = append(studentIDs, studentID)
	}
	requestHash := studentBarcodeRequestHash(studentIDs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedStudentBarcodes{}, err
	}
	defer tx.Rollback()
	var examID, contentHash string
	var layoutRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT exam_id::text,content_hash,layout FROM answer_sheet_template WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL FOR SHARE`, tenantID, templateID).Scan(&examID, &contentHash, &layoutRaw); err != nil {
		return IssuedStudentBarcodes{}, mapNotFound(err)
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil || len(layout.Pages) == 0 {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}
	var existingBatchID, existingRequestHash string
	err = tx.QueryRowContext(ctx, `
SELECT id::text, request_hash
FROM answer_sheet_print_batch
WHERE tenant_id=$1 AND template_id=$2::uuid AND idempotency_key=$3
`, tenantID, templateID, idempotencyKey).Scan(&existingBatchID, &existingRequestHash)
	if err == nil {
		if existingRequestHash != requestHash {
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

	studentPayload, _ := json.Marshal(studentIDs)
	var rosterCount int
	if err = tx.QueryRowContext(ctx, `
WITH requested AS (
  SELECT value::uuid AS student_id
  FROM jsonb_array_elements_text($3::jsonb)
)
SELECT count(DISTINCT candidate.student_id)
FROM requested r
JOIN exam_candidate_snapshot candidate
  ON candidate.tenant_id=$1 AND candidate.exam_id=$2::uuid
  AND candidate.student_id=r.student_id
`, tenantID, examID, string(studentPayload)).Scan(&rosterCount); err != nil {
		return IssuedStudentBarcodes{}, err
	}
	if rosterCount != len(studentIDs) {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}

	var printBatchID string
	var issuedAt time.Time
	err = tx.QueryRowContext(ctx, `
INSERT INTO answer_sheet_print_batch (
  tenant_id,exam_id,template_id,template_content_hash,key_id,idempotency_key,
  request_hash,sheet_count,page_count,issued_by
)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid)
ON CONFLICT (tenant_id,template_id,idempotency_key) DO NOTHING
RETURNING id::text,issued_at
`, tenantID, examID, templateID, contentHash, s.barcodeKeyring.ActiveKeyID, idempotencyKey,
		requestHash, len(studentIDs), len(layout.Pages), actorID).Scan(&printBatchID, &issuedAt)
	if err == sql.ErrNoRows {
		if err = tx.QueryRowContext(ctx, `
SELECT id::text,request_hash
FROM answer_sheet_print_batch
WHERE tenant_id=$1 AND template_id=$2::uuid AND idempotency_key=$3
`, tenantID, templateID, idempotencyKey).Scan(&existingBatchID, &existingRequestHash); err != nil {
			return IssuedStudentBarcodes{}, err
		}
		if existingRequestHash != requestHash {
			return IssuedStudentBarcodes{}, ErrConflict
		}
		out, loadErr := loadIssuedStudentBarcodesTx(ctx, tx, tenantID, existingBatchID)
		if loadErr != nil {
			return IssuedStudentBarcodes{}, loadErr
		}
		return out, tx.Commit()
	}
	if err != nil {
		return IssuedStudentBarcodes{}, err
	}

	out := IssuedStudentBarcodes{
		PrintBatchID: printBatchID, TemplateID: templateID, TemplateContentHash: contentHash,
		KeyID: s.barcodeKeyring.ActiveKeyID, Students: []IssuedStudentBarcodeSet{}, IssuedAt: issuedAt.UTC(),
	}
	for ordinal, studentID := range studentIDs {
		serial := uuid.NewString()
		if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_sheet_print_sheet (
  id,tenant_id,print_batch_id,exam_id,template_id,template_content_hash,student_id,ordinal
)
VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8)
`, serial, tenantID, printBatchID, examID, templateID, contentHash, studentID, ordinal+1); err != nil {
			return IssuedStudentBarcodes{}, err
		}
		set := IssuedStudentBarcodeSet{StudentID: studentID, SheetSerial: serial, Pages: []IssuedPageBarcode{}}
		for _, page := range layout.Pages {
			nonce := uuid.NewString()
			value, err := s.barcodeKeyring.Sign(BarcodeClaims{
				TenantID: tenantID, ExamID: examID, TemplateID: templateID, TemplateContentHash: contentHash,
				PageNo: page.PageNo, Nonce: nonce, StudentID: studentID, SheetSerial: serial,
			})
			if err != nil {
				return IssuedStudentBarcodes{}, ErrInvalidTransition
			}
			if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_sheet_print_page (tenant_id,print_sheet_id,page_no,nonce,barcode_value)
VALUES ($1,$2::uuid,$3,$4::uuid,$5)
`, tenantID, serial, page.PageNo, nonce, value); err != nil {
				return IssuedStudentBarcodes{}, err
			}
			set.Pages = append(set.Pages, IssuedPageBarcode{PageNo: page.PageNo, Value: value})
		}
		out.Students = append(out.Students, set)
	}
	return out, tx.Commit()
}

// 幂等比较按学生集合进行；仅改变请求顺序仍恢复原签发批次及其原有排序。
func studentBarcodeRequestHash(studentIDs []string) string {
	requestStudents := append([]string(nil), studentIDs...)
	sort.Strings(requestStudents)
	requestPayload, _ := json.Marshal(requestStudents)
	requestSum := sha256.Sum256(requestPayload)
	return "sha256:" + hex.EncodeToString(requestSum[:])
}

func loadIssuedStudentBarcodesTx(ctx context.Context, tx *sql.Tx, tenantID, printBatchID string) (IssuedStudentBarcodes, error) {
	var out IssuedStudentBarcodes
	if err := tx.QueryRowContext(ctx, `
SELECT id::text,template_id::text,template_content_hash,key_id,issued_at
FROM answer_sheet_print_batch
WHERE tenant_id=$1 AND id=$2::uuid
`, tenantID, printBatchID).Scan(
		&out.PrintBatchID, &out.TemplateID, &out.TemplateContentHash, &out.KeyID, &out.IssuedAt,
	); err != nil {
		return IssuedStudentBarcodes{}, mapNotFound(err)
	}
	out.IssuedAt = out.IssuedAt.UTC()
	out.Students = []IssuedStudentBarcodeSet{}
	rows, err := tx.QueryContext(ctx, `
SELECT s.student_id::text,s.id::text,p.page_no,p.barcode_value
FROM answer_sheet_print_sheet s
JOIN answer_sheet_print_page p
  ON p.tenant_id=s.tenant_id AND p.print_sheet_id=s.id
WHERE s.tenant_id=$1 AND s.print_batch_id=$2::uuid
ORDER BY s.ordinal,p.page_no
`, tenantID, printBatchID)
	if err != nil {
		return IssuedStudentBarcodes{}, err
	}
	defer rows.Close()
	currentSerial := ""
	for rows.Next() {
		var studentID, serial, value string
		var pageNo int
		if err = rows.Scan(&studentID, &serial, &pageNo, &value); err != nil {
			return IssuedStudentBarcodes{}, err
		}
		if serial != currentSerial {
			out.Students = append(out.Students, IssuedStudentBarcodeSet{
				StudentID: studentID, SheetSerial: serial, Pages: []IssuedPageBarcode{},
			})
			currentSerial = serial
		}
		last := len(out.Students) - 1
		out.Students[last].Pages = append(out.Students[last].Pages, IssuedPageBarcode{PageNo: pageNo, Value: value})
	}
	if err = rows.Err(); err != nil {
		return IssuedStudentBarcodes{}, err
	}
	if len(out.Students) == 0 {
		return IssuedStudentBarcodes{}, ErrInvalidTransition
	}
	return out, nil
}

type barcodeEvaluation struct {
	Evidence            []any
	Candidates          []any
	AssignedPageNo      int
	AssignedStudentID   string
	AssignedSheetSerial string
	AssignedTemplateID  string
	NeedsReview         bool
}

// 先验证每个观测，再按模板、页码和学生身份去重；出现多个可信身份时宁可转人工，也不猜测归属。
func (s *PostgresStore) evaluateBarcodesTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, observations []BarcodeObservation) barcodeEvaluation {
	out := barcodeEvaluation{Evidence: []any{}, Candidates: []any{}}
	unique := map[string]BarcodeClaims{}
	for _, observation := range observations {
		sum := sha256.Sum256([]byte(observation.Text))
		evidence := map[string]any{
			"format": observation.Format, "value_sha256": "sha256:" + hex.EncodeToString(sum[:]),
			"polygon": observation.Polygon, "orientation": observation.Orientation,
		}
		if !strings.HasPrefix(observation.Text, controlledBarcodePrefix+".") {
			evidence["status"] = "ignored"
			evidence["code"] = "not_controlled"
			out.Evidence = append(out.Evidence, evidence)
			continue
		}
		claims, err := s.barcodeKeyring.Verify(observation.Text)
		if err != nil {
			evidence["status"] = "rejected"
			evidence["code"] = barcodeRejectionCode(err)
			out.NeedsReview = true
			out.Evidence = append(out.Evidence, evidence)
			continue
		}
		code := s.validateBarcodeOwnershipTx(ctx, tx, tenantID, examID, claims)
		if code != "" {
			evidence["status"] = "rejected"
			evidence["code"] = code
			out.NeedsReview = true
			out.Evidence = append(out.Evidence, evidence)
			continue
		}
		evidence["status"] = "verified"
		evidence["kid"] = claims.KeyID
		evidence["template_id"] = claims.TemplateID
		evidence["template_content_hash"] = claims.TemplateContentHash
		evidence["page_no"] = claims.PageNo
		if claims.boundToStudent() {
			evidence["student_id"] = claims.StudentID
			evidence["sheet_serial"] = claims.SheetSerial
		}
		out.Evidence = append(out.Evidence, evidence)
		unique[claims.TemplateID+":"+claims.TemplateContentHash+":"+strconv.Itoa(claims.PageNo)+":"+claims.StudentID+":"+claims.SheetSerial] = claims
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	students := map[string]bool{}
	for _, key := range keys {
		claims := unique[key]
		candidate := map[string]any{
			"method": "controlled_barcode", "confidence": 1.0, "verified": true,
			"template_id": claims.TemplateID, "template_content_hash": claims.TemplateContentHash,
			"page_no": claims.PageNo, "kid": claims.KeyID,
		}
		if claims.boundToStudent() {
			candidate["student_id"] = claims.StudentID
			candidate["sheet_serial"] = claims.SheetSerial
			students[claims.StudentID] = true
		}
		out.Candidates = append(out.Candidates, candidate)
		out.AssignedPageNo = claims.PageNo
		out.AssignedTemplateID = claims.TemplateID
		if claims.boundToStudent() {
			out.AssignedStudentID = claims.StudentID
			out.AssignedSheetSerial = claims.SheetSerial
		}
	}
	if len(unique) > 1 {
		out.AssignedPageNo = 0
		out.AssignedStudentID = ""
		out.AssignedSheetSerial = ""
		out.AssignedTemplateID = ""
		out.NeedsReview = true
	}
	// Two different verified student identities on one page can only come from
	// a scan mix-up or a forged sheet: never guess, always route to a human.
	if len(students) > 1 {
		out.AssignedStudentID = ""
		out.AssignedSheetSerial = ""
		out.AssignedTemplateID = ""
		out.NeedsReview = true
	}
	return out
}

// 签名正确只证明内容来自持钥方；还需核对当前考试、锁定模板、名册和纸张撤销状态。
func (s *PostgresStore) validateBarcodeOwnershipTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, claims BarcodeClaims) string {
	if claims.TenantID != tenantID {
		return "tenant_mismatch"
	}
	if claims.ExamID != examID {
		return "exam_mismatch"
	}
	var contentHash string
	var layoutRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT content_hash,layout FROM answer_sheet_template WHERE tenant_id=$1 AND exam_id=$2::uuid AND id=$3::uuid AND status='locked' AND deleted_at IS NULL`, tenantID, examID, claims.TemplateID).Scan(&contentHash, &layoutRaw); err != nil {
		return "template_not_locked"
	}
	if contentHash != claims.TemplateContentHash {
		return "template_hash_mismatch"
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil {
		return "template_layout_invalid"
	}
	pageKnown := false
	for _, page := range layout.Pages {
		if page.PageNo == claims.PageNo {
			pageKnown = true
			break
		}
	}
	if !pageKnown {
		return "page_not_in_template"
	}
	if claims.boundToStudent() {
		var inRoster bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id=$1 AND candidate.exam_id=$2::uuid
    AND candidate.student_id=$3::uuid
)
`, tenantID, examID, claims.StudentID).Scan(&inRoster); err != nil || !inRoster {
			return "student_not_in_roster"
		}
		var sheetStatus string
		err := tx.QueryRowContext(ctx, `
SELECT status
FROM answer_sheet_print_sheet
WHERE tenant_id=$1
  AND id=$2::uuid
  AND exam_id=$3::uuid
  AND template_id=$4::uuid
  AND template_content_hash=$5
  AND student_id=$6::uuid
`, tenantID, claims.SheetSerial, examID, claims.TemplateID, claims.TemplateContentHash, claims.StudentID).Scan(&sheetStatus)
		if err != nil {
			return "sheet_not_issued"
		}
		if sheetStatus == "revoked" {
			return "sheet_revoked"
		}
	}
	return ""
}

func barcodeRejectionCode(err error) string {
	for _, code := range []string{"value_too_long", "format_invalid", "payload_invalid", "key_unknown", "signature_invalid", "claims_invalid"} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return "verification_failed"
}
