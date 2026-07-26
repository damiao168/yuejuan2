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

	"github.com/google/uuid"
)

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
func (s *PostgresStore) IssueStudentBarcodes(ctx context.Context, tenantID, templateID string, input IssueStudentBarcodesInput) (IssuedStudentBarcodes, error) {
	if len(input.StudentIDs) == 0 || len(input.StudentIDs) > 2000 {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}
	var examID, contentHash string
	var layoutRaw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT exam_id::text,content_hash,layout FROM answer_sheet_template WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL`, tenantID, templateID).Scan(&examID, &contentHash, &layoutRaw); err != nil {
		return IssuedStudentBarcodes{}, mapNotFound(err)
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil || len(layout.Pages) == 0 {
		return IssuedStudentBarcodes{}, ErrInvalidInput
	}
	out := IssuedStudentBarcodes{TemplateID: templateID, TemplateContentHash: contentHash, KeyID: s.barcodeKeyring.ActiveKeyID, Students: []IssuedStudentBarcodeSet{}}
	seen := map[string]bool{}
	for _, studentID := range input.StudentIDs {
		if seen[studentID] {
			return IssuedStudentBarcodes{}, ErrInvalidInput
		}
		seen[studentID] = true
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM student WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL)`, tenantID, studentID).Scan(&exists); err != nil {
			return IssuedStudentBarcodes{}, err
		}
		if !exists {
			return IssuedStudentBarcodes{}, ErrInvalidInput
		}
		serial := uuid.NewString()
		set := IssuedStudentBarcodeSet{StudentID: studentID, SheetSerial: serial, Pages: []IssuedPageBarcode{}}
		for _, page := range layout.Pages {
			value, err := s.barcodeKeyring.Sign(BarcodeClaims{
				TenantID: tenantID, ExamID: examID, TemplateID: templateID, TemplateContentHash: contentHash,
				PageNo: page.PageNo, Nonce: uuid.NewString(), StudentID: studentID, SheetSerial: serial,
			})
			if err != nil {
				return IssuedStudentBarcodes{}, ErrInvalidTransition
			}
			set.Pages = append(set.Pages, IssuedPageBarcode{PageNo: page.PageNo, Value: value})
		}
		out.Students = append(out.Students, set)
	}
	return out, nil
}

type barcodeEvaluation struct {
	Evidence            []any
	Candidates          []any
	AssignedPageNo      int
	AssignedStudentID   string
	AssignedSheetSerial string
	NeedsReview         bool
}

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
		if claims.boundToStudent() {
			out.AssignedStudentID = claims.StudentID
			out.AssignedSheetSerial = claims.SheetSerial
		}
	}
	if len(unique) > 1 {
		out.AssignedPageNo = 0
		out.NeedsReview = true
	}
	// Two different verified student identities on one page can only come from
	// a scan mix-up or a forged sheet: never guess, always route to a human.
	if len(students) > 1 {
		out.AssignedStudentID = ""
		out.AssignedSheetSerial = ""
		out.NeedsReview = true
	}
	return out
}

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
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM student WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL)`, tenantID, claims.StudentID).Scan(&exists); err != nil || !exists {
			return "student_unknown"
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
