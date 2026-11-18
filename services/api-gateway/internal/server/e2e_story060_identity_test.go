package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStory060StudentSheetIdentityE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-060 sheet identity PostgreSQL acceptance workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)

	var rosterStudentID, gradeID string
	if err := db.QueryRow(`
SELECT st.id::text,cls.grade_id::text
FROM exam_class ec
JOIN school_class cls
  ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id
JOIN student st
  ON st.tenant_id=ec.tenant_id AND st.class_id=ec.class_id
WHERE ec.tenant_id=$1::uuid AND ec.exam_id=$2::uuid
  AND ec.deleted_at IS NULL AND st.status='active' AND st.deleted_at IS NULL
ORDER BY st.created_at
LIMIT 1
`, fixture.TenantID, fixture.ExamID).Scan(&rosterStudentID, &gradeID); err != nil {
		t.Fatalf("lookup STORY-060 roster student: %v", err)
	}

	outsideClass := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, story056JSON(t, map[string]any{
		"school_id": fixture.SchoolID,
		"grade_id":  gradeID,
		"name":      "STORY-060 非应考班级",
		"code":      "story060-outside-" + suffix,
	}), http.StatusCreated)["class"].(map[string]any)
	outsideStudent := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, story056JSON(t, map[string]any{
		"school_id":  fixture.SchoolID,
		"class_id":   e2eString(t, outsideClass, "id"),
		"student_no": "S060-OUTSIDE-" + suffix,
		"name":       "非本场应考学生",
		"password":   "SyntheticStudent123!", "password_confirm": "SyntheticStudent123!",
	}), http.StatusCreated)["student"].(map[string]any)
	outsideStudentID := e2eString(t, outsideStudent, "id")

	e2eExpectStatus(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/student-barcodes",
		adminToken,
		story056JSON(t, map[string]any{
			"student_ids":     []string{rosterStudentID, outsideStudentID},
			"idempotency_key": "story060-reject-outside-" + suffix,
			"qr_layout":       e2eStory060ConfirmedPrintLayout(t, router, adminToken, fixture.TemplateID),
		}),
		http.StatusBadRequest,
	)

	scenario := sheetIdentityScenario{DB: db, Router: router, AdminToken: adminToken, Acceptance: fixture, Suffix: suffix}
	sheetSerial, barcodeValue := e2eAssertSinglePageSheetIssuance(t, scenario, rosterStudentID, outsideStudentID)
	firstAssetID := e2eUploadSyntheticPDF(
		t, router, adminToken, "story060-sheet-first-"+suffix+".pdf",
		"%PDF-1.4\n% STORY-060 first physical page "+suffix+"\n",
	)
	secondAssetID := e2eUploadSyntheticPDF(
		t, router, adminToken, "story060-sheet-duplicate-"+suffix+".pdf",
		"%PDF-1.4\n% STORY-060 duplicate physical page "+suffix+"\n",
	)
	batch := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/capture-batches", adminToken, story056JSON(t, map[string]any{
		"name":            "STORY-060 答题卡序列冲突验收",
		"source_type":     "web_upload",
		"idempotency_key": "story060-capture-" + suffix,
	}), http.StatusCreated)["batch"].(map[string]any)
	batchID := e2eString(t, batch, "id")
	firstFile := e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/files", adminToken, story056JSON(t, map[string]any{
		"file_asset_id":   firstAssetID,
		"idempotency_key": "story060-file-first-" + suffix,
	}), http.StatusCreated)["file"].(map[string]any)
	secondFile := e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/files", adminToken, story056JSON(t, map[string]any{
		"file_asset_id":   secondAssetID,
		"idempotency_key": "story060-file-duplicate-" + suffix,
	}), http.StatusCreated)["file"].(map[string]any)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/process", adminToken, `{}`, http.StatusOK)

	store := capture.NewPostgresStoreWithBarcodeKeyring(db, e2eBarcodeKeyring())
	applyPage := func(label string, file map[string]any, assetID string) {
		t.Helper()
		var hash string
		if err := db.QueryRow(`
SELECT hash_sha256
FROM file_asset
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, fixture.TenantID, assetID).Scan(&hash); err != nil {
			t.Fatalf("lookup capture asset hash: %v", err)
		}
		_, err := store.ApplyFileResult(context.Background(), fixture.TenantID, e2eString(t, file, "id"), []capture.DecodedPageInput{{
			SourceIndex: 1,
			FileAssetID: assetID,
			SHA256:      hash,
			Width:       1000,
			Height:      1400,
			Barcodes: []capture.BarcodeObservation{{
				Format: "QR_CODE",
				Text:   barcodeValue,
				Polygon: []map[string]int{
					{"x": 10, "y": 10},
					{"x": 110, "y": 10},
					{"x": 110, "y": 110},
					{"x": 10, "y": 110},
				},
			}},
		}})
		if err != nil {
			t.Fatalf("apply %s captured student sheet page: %v", label, err)
		}
	}
	applyPage("first", firstFile, firstAssetID)
	applyPage("duplicate", secondFile, secondAssetID)

	secondPage := e2eAssertDuplicatePhysicalSheetConflict(t, scenario, batchID, sheetSerial)
	e2eDeleteAndRestoreDuplicateSheetPage(t, scenario, batchID, sheetSerial, secondPage)
	e2eRevokeObservedSheet(t, scenario, batchID, sheetSerial)
	e2eAssertSheetReprintIdentityAndLineage(t, scenario, sheetSerial)
}

// 相同发行请求重放打印批次、卡片序列和条码；改变学生集合则冲突，台账仍仅一份。
func e2eAssertSinglePageSheetIssuance(t *testing.T, scenario sheetIdentityScenario, rosterStudentID, outsideStudentID string) (string, string) {
	t.Helper()
	db, router, adminToken, fixture, suffix := scenario.DB, scenario.Router, scenario.AdminToken, scenario.Acceptance, scenario.Suffix
	issueBody := story056JSON(t, map[string]any{
		"student_ids":     []string{rosterStudentID},
		"idempotency_key": "story060-issue-" + suffix,
		"qr_layout":       e2eStory060ConfirmedPrintLayout(t, router, adminToken, fixture.TemplateID),
	})
	issued := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/student-barcodes",
		adminToken,
		issueBody,
		http.StatusOK,
	)["barcodes"].(map[string]any)
	replayed := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/student-barcodes",
		adminToken,
		issueBody,
		http.StatusOK,
	)["barcodes"].(map[string]any)
	printBatchID := e2eString(t, issued, "print_batch_id")
	if e2eString(t, replayed, "print_batch_id") != printBatchID {
		t.Fatalf("idempotent issuance created a second print batch: first=%#v replay=%#v", issued, replayed)
	}
	issuedStudent := issued["students"].([]any)[0].(map[string]any)
	replayedStudent := replayed["students"].([]any)[0].(map[string]any)
	sheetSerial := e2eString(t, issuedStudent, "sheet_serial")
	if e2eString(t, replayedStudent, "sheet_serial") != sheetSerial {
		t.Fatalf("idempotent issuance changed the physical sheet serial: first=%#v replay=%#v", issuedStudent, replayedStudent)
	}
	barcodeValue := e2eString(t, issuedStudent["pages"].([]any)[0].(map[string]any), "value")
	if e2eString(t, replayedStudent["pages"].([]any)[0].(map[string]any), "value") != barcodeValue {
		t.Fatal("idempotent issuance must return the exact persisted barcode value")
	}
	e2eExpectStatus(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/student-barcodes",
		adminToken,
		story056JSON(t, map[string]any{
			"student_ids":     []string{outsideStudentID},
			"idempotency_key": "story060-issue-" + suffix,
		}),
		http.StatusConflict,
	)
	var printBatchCount, printSheetCount, printPageCount int
	if err := db.QueryRow(`
SELECT
  (SELECT count(*) FROM answer_sheet_print_batch WHERE tenant_id=$1::uuid AND id=$2::uuid),
  (SELECT count(*) FROM answer_sheet_print_sheet WHERE tenant_id=$1::uuid AND print_batch_id=$2::uuid),
  (SELECT count(*) FROM answer_sheet_print_page p
     JOIN answer_sheet_print_sheet s ON s.tenant_id=p.tenant_id AND s.id=p.print_sheet_id
   WHERE s.tenant_id=$1::uuid AND s.print_batch_id=$2::uuid)
`, fixture.TenantID, printBatchID).Scan(&printBatchCount, &printSheetCount, &printPageCount); err != nil {
		t.Fatalf("query persisted print ledger: %v", err)
	}
	if printBatchCount != 1 || printSheetCount != 1 || printPageCount != 1 {
		t.Fatalf("unexpected print ledger counts: batch=%d sheet=%d page=%d", printBatchCount, printSheetCount, printPageCount)
	}

	return sheetSerial, barcodeValue
}

// 两份物理页都保留可见并进入冲突；台账序列、重复链接和两个提交的冲突事实分别核对。
func e2eAssertDuplicatePhysicalSheetConflict(t *testing.T, scenario sheetIdentityScenario, batchID, sheetSerial string) map[string]any {
	t.Helper()
	db, router, adminToken, fixture := scenario.DB, scenario.Router, scenario.AdminToken, scenario.Acceptance
	pages := e2eGetJSON(t, router, "/api/v1/capture-batches/"+batchID+"/pages", adminToken, http.StatusOK)["pages"].([]any)
	if len(pages) != 2 {
		t.Fatalf("expected both repeated physical pages to remain visible for review: %#v", pages)
	}
	firstPage := pages[0].(map[string]any)
	secondPage := pages[1].(map[string]any)
	firstPageID := e2eString(t, firstPage, "id")
	if firstPage["status"] != "needs_review" || secondPage["status"] != "needs_review" {
		t.Fatalf("both sides of a sheet serial conflict must enter review: %#v", pages)
	}
	if e2eString(t, firstPage, "sheet_serial") != sheetSerial ||
		e2eString(t, secondPage, "sheet_serial") != sheetSerial ||
		e2eString(t, secondPage, "duplicate_of_page_id") != firstPageID {
		t.Fatalf("typed serial ownership or duplicate link missing: %#v", pages)
	}
	for _, rawPage := range pages {
		page := rawPage.(map[string]any)
		identity := page["page_identity"].(map[string]any)
		if identity["barcode_status"] != "needs_review" || identity["barcode_conflict_code"] != "sheet_page_duplicate" {
			t.Fatalf("page conflict evidence is incomplete: %#v", page)
		}
	}
	var sheetStatus string
	var conflictingSubmissions int
	if err := db.QueryRow(`
SELECT status
FROM answer_sheet_print_sheet
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, fixture.TenantID, sheetSerial).Scan(&sheetStatus); err != nil {
		t.Fatalf("query conflicted print sheet: %v", err)
	}
	if err := db.QueryRow(`
SELECT count(DISTINCT s.id)
FROM capture_page p
JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id
WHERE p.tenant_id=$1::uuid AND p.sheet_serial=$2::uuid
  AND p.deleted_at IS NULL AND s.identity_status='conflict'
`, fixture.TenantID, sheetSerial).Scan(&conflictingSubmissions); err != nil {
		t.Fatalf("query conflicting submissions: %v", err)
	}
	if sheetStatus != "conflict" || conflictingSubmissions != 2 {
		t.Fatalf("duplicate scan must persist a sheet and submission conflict: sheet=%s submissions=%d", sheetStatus, conflictingSubmissions)
	}

	return secondPage
}

// 明确删除重复页后恢复剩余页身份，随后原页恢复必须重新计算两侧冲突，不能沿用删除时的通过状态。
func e2eDeleteAndRestoreDuplicateSheetPage(t *testing.T, scenario sheetIdentityScenario, batchID, sheetSerial string, secondPage map[string]any) {
	t.Helper()
	db, router, adminToken, fixture := scenario.DB, scenario.Router, scenario.AdminToken, scenario.Acceptance
	deletedPage := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/capture-pages/"+e2eString(t, secondPage, "id")+"/delete",
		adminToken,
		story056JSON(t, map[string]any{
			"revision": e2eFloat(t, secondPage, "revision"),
			"reason":   "STORY-060 confirmed duplicate physical page",
		}),
		http.StatusOK,
	)["page"].(map[string]any)
	pages := e2eGetJSON(t, router, "/api/v1/capture-batches/"+batchID+"/pages", adminToken, http.StatusOK)["pages"].([]any)
	firstPage := pages[0].(map[string]any)
	secondPage = pages[1].(map[string]any)
	firstIdentity := firstPage["page_identity"].(map[string]any)
	if firstPage["status"] != "quality_checking" ||
		firstIdentity["barcode_status"] != "verified" ||
		firstIdentity["barcode_conflict_code"] != nil ||
		secondPage["status"] != "deleted" {
		t.Fatalf("deleting the confirmed duplicate must resolve the remaining serial conflict: %#v", pages)
	}
	var unassignedSubmissions int
	var sheetStatus string
	if err := db.QueryRow(`
SELECT count(DISTINCT s.id)
FROM capture_page p
JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id
WHERE p.tenant_id=$1::uuid AND p.sheet_serial=$2::uuid
  AND p.deleted_at IS NULL AND s.identity_status='unassigned'
`, fixture.TenantID, sheetSerial).Scan(&unassignedSubmissions); err != nil {
		t.Fatalf("query reconciled submissions: %v", err)
	}
	if err := db.QueryRow(`
SELECT status
FROM answer_sheet_print_sheet
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, fixture.TenantID, sheetSerial).Scan(&sheetStatus); err != nil {
		t.Fatalf("query reconciled sheet: %v", err)
	}
	if sheetStatus != "observed" || unassignedSubmissions != 2 {
		t.Fatalf("resolved duplicate must restore observed/unassigned state: sheet=%s submissions=%d", sheetStatus, unassignedSubmissions)
	}

	e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/capture-pages/"+e2eString(t, deletedPage, "id")+"/restore",
		adminToken,
		story056JSON(t, map[string]any{
			"revision": e2eFloat(t, deletedPage, "revision"),
			"reason":   "STORY-060 restore verifies conflict recalculation",
		}),
		http.StatusOK,
	)
	pages = e2eGetJSON(t, router, "/api/v1/capture-batches/"+batchID+"/pages", adminToken, http.StatusOK)["pages"].([]any)
	for _, rawPage := range pages {
		page := rawPage.(map[string]any)
		identity := page["page_identity"].(map[string]any)
		if page["status"] != "needs_review" || identity["barcode_conflict_code"] != "sheet_page_duplicate" {
			t.Fatalf("restoring the duplicate must restore both sides of the conflict: %#v", pages)
		}
	}

}

// 撤销保留原因，并使旧序列的所有已观察页重新需要复核。
func e2eRevokeObservedSheet(t *testing.T, scenario sheetIdentityScenario, batchID, sheetSerial string) {
	t.Helper()
	router, adminToken := scenario.Router, scenario.AdminToken
	revoked := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/revoke",
		adminToken,
		`{"reason":"STORY-060 physical sheet damaged"}`,
		http.StatusOK,
	)["sheet"].(map[string]any)
	if revoked["status"] != "revoked" || revoked["revoke_reason"] != "STORY-060 physical sheet damaged" {
		t.Fatalf("sheet revocation metadata is incomplete: %#v", revoked)
	}
	e2eExpectStatus(t, router, http.MethodPost,
		"/api/v1/answer-sheet-print-batches/"+e2eString(t, revoked, "print_batch_id")+"/confirm-layout",
		adminToken, story056JSON(t, e2eStory060ConfirmedPrintLayout(t, router, adminToken, scenario.Acceptance.TemplateID)), http.StatusConflict)
	pages := e2eGetJSON(t, router, "/api/v1/capture-batches/"+batchID+"/pages", adminToken, http.StatusOK)["pages"].([]any)
	for _, rawPage := range pages {
		page := rawPage.(map[string]any)
		identity := page["page_identity"].(map[string]any)
		if page["status"] != "needs_review" || identity["barcode_conflict_code"] != "sheet_revoked" {
			t.Fatalf("revocation must invalidate every observed page of the old serial: %#v", pages)
		}
	}

}

// 补打只生成一个新物理序列并可原请求重放；再次替代冲突，数据库保留旧序列、操作及原因谱系。
func e2eAssertSheetReprintIdentityAndLineage(t *testing.T, scenario sheetIdentityScenario, sheetSerial string) {
	t.Helper()
	db, router, adminToken, fixture, suffix := scenario.DB, scenario.Router, scenario.AdminToken, scenario.Acceptance, scenario.Suffix
	reprintBody := story056JSON(t, map[string]any{
		"reason":          "STORY-060 replace damaged physical sheet",
		"idempotency_key": "story060-reprint-" + suffix,
	})
	// 位置预检失败不能占用命令键或创建替代品；随后省略位置可沿用原批次的冻结确认。
	invalidLayout := e2eStory060ConfirmedPrintLayout(t, router, adminToken, fixture.TemplateID)
	invalidPage := invalidLayout["pages"].([]any)[0].(map[string]any)
	invalidPage["width"], invalidPage["height"] = .001, .001
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/reprint", adminToken,
		story056JSON(t, map[string]any{"reason": "STORY-060 replace damaged physical sheet", "idempotency_key": "story060-reprint-" + suffix, "qr_layout": invalidLayout}), http.StatusConflict)
	reprinted := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/reprint",
		adminToken,
		reprintBody,
		http.StatusOK,
	)["barcodes"].(map[string]any)
	replayedReprint := e2ePostJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/reprint",
		adminToken,
		reprintBody,
		http.StatusOK,
	)["barcodes"].(map[string]any)
	reprintedStudent := reprinted["students"].([]any)[0].(map[string]any)
	reprintedSerial := e2eString(t, reprintedStudent, "sheet_serial")
	e2eExpectStatus(t, router, http.MethodGet, "/api/v1/answer-sheet-print-batches/"+e2eString(t, reprinted, "print_batch_id")+"/package.pdf", adminToken, "", http.StatusOK)
	changedLayout := e2eStory060ConfirmedPrintLayout(t, router, adminToken, fixture.TemplateID)
	changedLayout["pages"].([]any)[0].(map[string]any)["x"] = .65
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/reprint", adminToken,
		story056JSON(t, map[string]any{"reason": "STORY-060 replace damaged physical sheet", "idempotency_key": "story060-reprint-" + suffix, "qr_layout": changedLayout}), http.StatusConflict)
	if reprintedSerial == sheetSerial ||
		e2eString(t, replayedReprint, "print_batch_id") != e2eString(t, reprinted, "print_batch_id") ||
		e2eString(t, replayedReprint["students"].([]any)[0].(map[string]any), "sheet_serial") != reprintedSerial {
		t.Fatalf("reprint must create one new immutable serial and replay it idempotently: first=%#v replay=%#v", reprinted, replayedReprint)
	}
	e2eExpectStatus(
		t,
		router,
		http.MethodPost,
		"/api/v1/answer-sheet-print-sheets/"+sheetSerial+"/reprint",
		adminToken,
		story056JSON(t, map[string]any{
			"reason":          "attempt a second replacement",
			"idempotency_key": "story060-second-reprint-" + suffix,
		}),
		http.StatusConflict,
	)
	var supersedesSerial, operation, reprintReason string
	if err := db.QueryRow(`
SELECT s.supersedes_sheet_id::text,b.operation,b.reason
FROM answer_sheet_print_sheet s
JOIN answer_sheet_print_batch b
  ON b.tenant_id=s.tenant_id AND b.id=s.print_batch_id
WHERE s.tenant_id=$1::uuid AND s.id=$2::uuid
`, fixture.TenantID, reprintedSerial).Scan(&supersedesSerial, &operation, &reprintReason); err != nil {
		t.Fatalf("query replacement lineage: %v", err)
	}
	if supersedesSerial != sheetSerial || operation != "reprint" ||
		reprintReason != "STORY-060 replace damaged physical sheet" {
		t.Fatalf("replacement lineage is incomplete: supersedes=%s operation=%s reason=%s", supersedesSerial, operation, reprintReason)
	}

}
