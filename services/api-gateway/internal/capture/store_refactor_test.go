package capture

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecodedIdentitySeparatesUnverifiedPagesFromControlledEvidence(t *testing.T) {
	identity := decodedFileIdentity{verifiedPageNumbers: map[int]bool{}}
	controlled := barcodeEvaluation{
		AssignedStudentID: "student-1", AssignedTemplateID: "template-1", AssignedSheetSerial: "sheet-1",
	}
	identity.observePage(decodedCapturePageResult{barcode: controlled, barcodeStatus: "verified", assignedPageNo: 2, capturePageID: "page-2"})
	identity.observePage(decodedCapturePageResult{barcode: controlled, barcodeStatus: "verified", assignedPageNo: 2, capturePageID: "same-number"})
	identity.observePage(decodedCapturePageResult{barcode: controlled, barcodeStatus: "needs_review", assignedPageNo: 3, capturePageID: "review-page"})
	identity.observePage(decodedCapturePageResult{barcodeStatus: "none", assignedPageNo: 4, capturePageID: "no-barcode"})
	identity.observePage(decodedCapturePageResult{barcode: barcodeEvaluation{AssignedTemplateID: "template-1"}, barcodeStatus: "verified", assignedPageNo: 5, capturePageID: "template-only"})
	if identity.studentID != "student-1" || identity.templateID != "template-1" || identity.sheetSerial != "sheet-1" || identity.conflict {
		t.Fatalf("consistent controlled pages lost their identity: %#v", identity)
	}
	if !reflect.DeepEqual(identity.verifiedPageNumbers, map[int]bool{2: true}) {
		t.Fatalf("only distinct student-bound verified pages may count: %#v", identity.verifiedPageNumbers)
	}
	if !reflect.DeepEqual(identity.unverifiedCapturePageIDs, []string{"review-page", "no-barcode"}) {
		t.Fatalf("review pages must keep input order without including template-only pages: %#v", identity.unverifiedCapturePageIDs)
	}
}

func TestDecodedIdentityRetainsConflictForAnyMixedControlledIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		barcode barcodeEvaluation
	}{
		{"student", barcodeEvaluation{AssignedStudentID: "other", AssignedTemplateID: "template-1", AssignedSheetSerial: "sheet-1"}},
		{"template", barcodeEvaluation{AssignedStudentID: "student-1", AssignedTemplateID: "other", AssignedSheetSerial: "sheet-1"}},
		{"sheet", barcodeEvaluation{AssignedStudentID: "student-1", AssignedTemplateID: "template-1", AssignedSheetSerial: "other"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := decodedFileIdentity{verifiedPageNumbers: map[int]bool{}}
			first := decodedCapturePageResult{
				barcode:       barcodeEvaluation{AssignedStudentID: "student-1", AssignedTemplateID: "template-1", AssignedSheetSerial: "sheet-1"},
				barcodeStatus: "verified", assignedPageNo: 1,
			}
			identity.observePage(first)
			identity.observePage(decodedCapturePageResult{barcode: test.barcode, barcodeStatus: "verified", assignedPageNo: 2})
			identity.observePage(first)
			if !identity.conflict || identity.sheetSerial != "sheet-1" || len(identity.verifiedPageNumbers) != 2 {
				t.Fatalf("later matching pages must not erase a mixed-identity conflict: %#v", identity)
			}
		})
	}
}

func TestControlledRegistrationTemplateAllowsUnidentifiedPagesAndRejectsIncompleteOrMixedIdentity(t *testing.T) {
	for _, test := range []struct {
		name       string
		pages      []registrationPageRow
		controlled bool
		wantErr    error
	}{
		{"unidentified", []registrationPageRow{{}, {}}, false, nil},
		{"controlled_with_unidentified", []registrationPageRow{{}, {templateID: "template-1", templateHash: "hash-1"}, {}}, true, nil},
		{"missing_hash", []registrationPageRow{{templateID: "template-1"}}, false, ErrInvalidTransition},
		{"missing_template", []registrationPageRow{{templateHash: "hash-1"}}, false, ErrInvalidTransition},
		{"mixed_template", []registrationPageRow{{templateID: "template-1", templateHash: "hash-1"}, {templateID: "template-2", templateHash: "hash-1"}}, false, ErrInvalidTransition},
		{"mixed_hash", []registrationPageRow{{templateID: "template-1", templateHash: "hash-1"}, {templateID: "template-1", templateHash: "hash-2"}}, false, ErrInvalidTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, hash, controlled, err := controlledRegistrationTemplate(test.pages)
			if !errors.Is(err, test.wantErr) || controlled != test.controlled {
				t.Fatalf("template routing returned %q/%q controlled=%v err=%v", id, hash, controlled, err)
			}
			if controlled && (id != "template-1" || hash != "hash-1") {
				t.Fatalf("controlled identity changed: %q/%q", id, hash)
			}
		})
	}
}

func TestSheetCaptureGroupsExcludeDeletedPagesButRetainTheirReconciliationScope(t *testing.T) {
	groups := groupSheetCapturePages([]sheetCapturePageState{
		{PageID: "first", SubmissionID: "active-submission", BatchID: "active-batch", PageNo: 1, Status: "normalized"},
		{PageID: "deleted", SubmissionID: "deleted-submission", BatchID: "deleted-batch", PageNo: 1, Status: "deleted"},
		{PageID: "unassigned", BatchID: "active-batch", PageNo: 2, Status: "needs_review"},
	})
	if len(groups.activeGroups[1]) != 1 || groups.activeGroups[1][0].PageID != "first" || len(groups.activeGroups[2]) != 1 {
		t.Fatalf("deleted scans must not participate in active duplicate groups: %#v", groups.activeGroups)
	}
	if !reflect.DeepEqual(groups.submissionIDs, map[string]bool{"active-submission": true, "deleted-submission": true}) {
		t.Fatalf("all referenced submissions still need reconciliation: %#v", groups.submissionIDs)
	}
	if !reflect.DeepEqual(groups.activeSubmissionIDs, map[string]bool{"active-submission": true}) {
		t.Fatalf("only active submissions may be marked revoked: %#v", groups.activeSubmissionIDs)
	}
	if !reflect.DeepEqual(groups.batchIDs, map[string]bool{"active-batch": true, "deleted-batch": true}) {
		t.Fatalf("deletion must not hide an affected batch from aggregation: %#v", groups.batchIDs)
	}
}
