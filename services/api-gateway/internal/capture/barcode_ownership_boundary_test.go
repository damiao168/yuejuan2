package capture

import (
	"crypto/sha256"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func captureBarcodeFixture() (BarcodeKeyring, BarcodeClaims) {
	return BarcodeKeyring{ActiveKeyID: "test-key", Keys: map[string][]byte{"test-key": []byte(strings.Repeat("k", 32))}}, BarcodeClaims{
		TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222",
		TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:frozen-answer-sheet",
		StudentID: "44444444-4444-4444-8444-444444444444", SheetSerial: "55555555-5555-4555-8555-555555555555",
		PageNo: 1, Nonce: "seeded-nonce",
	}
}

func captureBarcodeTemplateStep(claims BarcodeClaims, hash, layout string, exists bool) collectionSQLStep {
	query := printLedgerQuery{
		fragments: []string{"FROM answer_sheet_template WHERE tenant_id=$1 AND exam_id=$2::uuid", "status='locked' AND deleted_at IS NULL"},
		args:      []driver.Value{claims.TenantID, claims.ExamID, claims.TemplateID}, columns: 2,
	}
	if exists {
		query.values = [][]driver.Value{{hash, []byte(layout)}}
	}
	return collectionSQLStep{kind: "query", printLedgerQuery: query}
}

func captureBarcodeRosterStep(claims BarcodeClaims, inRoster bool) collectionSQLStep {
	return collectionSQLStep{kind: "query", printLedgerQuery: printLedgerQuery{
		fragments: []string{"FROM exam_candidate_snapshot candidate", "candidate.student_id=$3::uuid"},
		args:      []driver.Value{claims.TenantID, claims.ExamID, claims.StudentID}, columns: 1, values: [][]driver.Value{{inRoster}},
	}}
}

func captureBarcodeSheetStep(claims BarcodeClaims, status string) collectionSQLStep {
	query := printLedgerQuery{
		fragments: []string{"FROM answer_sheet_print_sheet", "AND template_content_hash=$5", "AND student_id=$6::uuid"},
		args:      []driver.Value{claims.TenantID, claims.SheetSerial, claims.ExamID, claims.TemplateID, claims.TemplateContentHash, claims.StudentID}, columns: 1,
	}
	if status != "" {
		query.values = [][]driver.Value{{status}}
	}
	return collectionSQLStep{kind: "query", printLedgerQuery: query}
}

func TestCaptureControlledBarcodeRequiresCurrentStudentAndSheetOwnership(t *testing.T) {
	const layout = `{"pages":[{"page_no":1}]}`
	keyring, claims := captureBarcodeFixture()
	signed, err := keyring.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	template := captureBarcodeTemplateStep(claims, claims.TemplateContentHash, layout, true)
	roster := captureBarcodeRosterStep(claims, true)
	for _, tc := range []struct {
		name, value, tenant, exam, status, code string
		queries                                 []collectionSQLStep
	}{
		{name: "普通条码只保存摘要", value: "local-inventory-code", tenant: claims.TenantID, exam: claims.ExamID, status: "ignored", code: "not_controlled"},
		{name: "签名损坏转人工", value: signed[:strings.LastIndex(signed, ".")+1] + "AAAA", tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "signature_invalid"},
		{name: "其他租户的有效签名", value: signed, tenant: claims.StudentID, exam: claims.ExamID, status: "rejected", code: "tenant_mismatch"},
		{name: "本租户另一考试", value: signed, tenant: claims.TenantID, exam: claims.TemplateID, status: "rejected", code: "exam_mismatch"},
		{name: "模板未锁定", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "template_not_locked",
			queries: []collectionSQLStep{captureBarcodeTemplateStep(claims, "", "", false)}},
		{name: "模板快照已变化", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "template_hash_mismatch",
			queries: []collectionSQLStep{captureBarcodeTemplateStep(claims, "sha256:old", layout, true)}},
		{name: "模板布局损坏", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "template_layout_invalid",
			queries: []collectionSQLStep{captureBarcodeTemplateStep(claims, claims.TemplateContentHash, `{"pages":false}`, true)}},
		{name: "条码页码不在模板内", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "page_not_in_template",
			queries: []collectionSQLStep{captureBarcodeTemplateStep(claims, claims.TemplateContentHash, `{"pages":[{"page_no":2}]}`, true)}},
		{name: "学生不在冻结名册", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "student_not_in_roster",
			queries: []collectionSQLStep{template, captureBarcodeRosterStep(claims, false)}},
		{name: "该模板和学生组合未发此卷", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "sheet_not_issued",
			queries: []collectionSQLStep{template, roster, captureBarcodeSheetStep(claims, "")}},
		{name: "已撤销的印卷", value: signed, tenant: claims.TenantID, exam: claims.ExamID, status: "rejected", code: "sheet_revoked",
			queries: []collectionSQLStep{template, roster, captureBarcodeSheetStep(claims, "revoked")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]collectionSQLStep{{kind: "begin"}}, tc.queries...)
			store, _ := collectionProbeStore(t, append(steps, collectionSQLStep{kind: "rollback"}))
			store.barcodeKeyring = keyring
			tx, err := store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			got := store.evaluateBarcodesTx(t.Context(), tx, tc.tenant, tc.exam, []BarcodeObservation{{Format: "QR", Text: tc.value, Orientation: 90}})
			if got.NeedsReview != (tc.status == "rejected") || len(got.Evidence) != 1 || len(got.Candidates) != 0 ||
				got.AssignedStudentID != "" || got.AssignedSheetSerial != "" || got.AssignedTemplateID != "" || got.AssignedPageNo != 0 {
				t.Fatalf("unverified barcode affected the assignment: %+v", got)
			}
			evidence, ok := got.Evidence[0].(map[string]any)
			wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(tc.value)))
			if !ok || evidence["status"] != tc.status || evidence["code"] != tc.code || evidence["value_sha256"] != wantDigest ||
				evidence["format"] != "QR" || evidence["orientation"] != 90 || evidence["student_id"] != nil || strings.Contains(fmt.Sprint(evidence), tc.value) {
				t.Fatalf("unexpected barcode evidence: %+v", evidence)
			}
		})
	}
}

func TestCaptureBarcodeDuplicateAndCompetingStudentBindings(t *testing.T) {
	keyring, first := captureBarcodeFixture()
	second := first
	second.StudentID = "66666666-6666-4666-8666-666666666666"
	second.SheetSerial = "77777777-7777-4777-8777-777777777777"
	for _, tc := range []struct {
		name         string
		next         BarcodeClaims
		wantConflict bool
	}{
		{name: "重复观测只产生一个候选", next: first},
		{name: "同页出现两个可信学生则交由人工确认", next: second, wantConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []collectionSQLStep{{kind: "begin"}}
			var observations []BarcodeObservation
			for _, claims := range []BarcodeClaims{first, tc.next} {
				text, err := keyring.Sign(claims)
				if err != nil {
					t.Fatal(err)
				}
				observations = append(observations, BarcodeObservation{Format: "QR", Text: text})
				steps = append(steps, captureBarcodeTemplateStep(claims, claims.TemplateContentHash, `{"pages":[{"page_no":1}]}`, true),
					captureBarcodeRosterStep(claims, true), captureBarcodeSheetStep(claims, "issued"))
			}
			store, _ := collectionProbeStore(t, append(steps, collectionSQLStep{kind: "rollback"}))
			store.barcodeKeyring = keyring
			tx, err := store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			got := store.evaluateBarcodesTx(t.Context(), tx, first.TenantID, first.ExamID, observations)
			wantCandidates := []BarcodeClaims{first}
			if tc.wantConflict {
				wantCandidates = append(wantCandidates, second)
			}
			if got.NeedsReview != tc.wantConflict || len(got.Candidates) != len(wantCandidates) || len(got.Evidence) != 2 {
				t.Fatalf("unexpected matching result: %+v", got)
			}
			for i, want := range wantCandidates {
				candidate, ok := got.Candidates[i].(map[string]any)
				if !ok || candidate["student_id"] != want.StudentID || candidate["sheet_serial"] != want.SheetSerial ||
					candidate["template_id"] != want.TemplateID || candidate["page_no"] != want.PageNo || candidate["verified"] != true {
					t.Fatalf("candidate %d = %+v; want authenticated claims for %+v", i, candidate, want)
				}
			}
			// 冲突时保留候选用于人工核对，但任何身份或页码都不得自动写入。
			if tc.wantConflict {
				if got.AssignedStudentID != "" || got.AssignedSheetSerial != "" || got.AssignedTemplateID != "" || got.AssignedPageNo != 0 {
					t.Fatalf("conflicting candidates were assigned: %+v", got)
				}
			} else if got.AssignedStudentID != first.StudentID || got.AssignedSheetSerial != first.SheetSerial ||
				got.AssignedTemplateID != first.TemplateID || got.AssignedPageNo != first.PageNo {
				t.Fatalf("verified duplicate observations lost their assignment: %+v", got)
			}
		})
	}
}
