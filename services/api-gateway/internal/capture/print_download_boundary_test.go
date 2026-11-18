package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"
)

type printDownloadStore struct {
	Store
	packageValue StudentPrintPackage
	readError    error
}

func (s *printDownloadStore) GetStudentPrintPackage(_ context.Context, tenantID, batchID string) (StudentPrintPackage, error) {
	if tenantID != "tenant" || batchID != s.packageValue.PrintBatchID {
		return StudentPrintPackage{}, ErrNotFound
	}
	return s.packageValue, s.readError
}

func printDownloadFixture() StudentPrintPackage {
	return StudentPrintPackage{
		PrintBatchID: "batch", ExamID: "exam", TemplateID: "template", KeyID: "synthetic-key",
		IssuedAt: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
		Sheets: []PrintPackageSheet{{
			StudentID: "student", SheetSerial: "sheet", Ordinal: 1,
			Pages: []PrintPackagePage{{PageNo: 1, TemplateWidth: 1000, TemplateHeight: 1400, BarcodeValue: "synthetic-signed-barcode",
				IdentityRegions: []PrintPackageRegion{{ID: "qr", X: .75, Y: .05, Width: .2, Height: .18}}}},
		}},
	}
}

func TestStudentPrintDownloadServesStablePDFWithMatchingDigestAndAudit(t *testing.T) {
	store := &printDownloadStore{packageValue: printDownloadFixture()}
	audit := &collectionAuditRecorder{}
	handler := NewHandler(store, nil, nil, nil, audit)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/answer-sheet-print-batches/{id}/package.pdf", handler.DownloadStudentPrintPackage)
	var first []byte
	for range 2 {
		response := captureWorkflowRequest(t, mux, "GET", "/api/v1/answer-sheet-print-batches/batch/package.pdf", "", nil, nil)
		body := response.Body.Bytes()
		if response.Code != http.StatusOK || !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("%%EOF")) {
			t.Fatalf("download did not return a complete PDF: HTTP %d", response.Code)
		}
		digest := sha256.Sum256(body)
		hexDigest := hex.EncodeToString(digest[:])
		if response.Header().Get("X-EduGrade-SHA256") != "sha256:"+hexDigest || response.Header().Get("ETag") != `"`+hexDigest+`"` ||
			response.Header().Get("Content-Length") != strconv.Itoa(len(body)) || response.Header().Get("Content-Type") != "application/pdf" ||
			response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Disposition") != `attachment; filename="edugrade-answer-sheets-batch.pdf"` {
			t.Fatalf("download metadata does not describe the actual PDF: %+v", response.Header())
		}
		// 重复下载同一台账应保持相同字节；HTTP 元数据必须跟随实际文件摘要。
		if first != nil && !bytes.Equal(first, body) {
			t.Fatal("repeat download changed immutable ledger output")
		}
		first = append([]byte(nil), body...)
	}
	if len(audit.events) != 2 {
		t.Fatalf("expected one audit per completed download, got %d", len(audit.events))
	}
	for _, event := range audit.events {
		if event.TenantID != "tenant" || event.ActorID != "operator" || event.TargetID != "batch" || event.Action != "answer_sheet_print_batch.package_downloaded" {
			t.Fatalf("download audit did not identify the actual print batch: %+v", event)
		}
	}
}

func TestStudentPrintDownloadRejectsUnavailableLedgerWithoutDownloadAudit(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		err    error
		broken bool
		status int
		code   string
	}{
		{"missing batch", ErrNotFound, false, http.StatusNotFound, "capture_not_found"},
		{"observed or revoked sheet", ErrInvalidTransition, false, http.StatusConflict, "capture_invalid_transition"},
		{"invalid page geometry", nil, true, http.StatusInternalServerError, "print_package_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := &printDownloadStore{packageValue: printDownloadFixture(), readError: scenario.err}
			if scenario.broken {
				store.packageValue.Sheets[0].Pages[0].TemplateWidth = 0
			}
			audit := &collectionAuditRecorder{}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/answer-sheet-print-batches/{id}/package.pdf", NewHandler(store, nil, nil, nil, audit).DownloadStudentPrintPackage)
			response := captureWorkflowRequest(t, mux, "GET", "/api/v1/answer-sheet-print-batches/batch/package.pdf", "", nil, nil)
			requireCaptureWorkflowError(t, response, scenario.status, scenario.code)
			if response.Header().Get("ETag") != "" || bytes.HasPrefix(response.Body.Bytes(), []byte("%PDF-")) || len(audit.events) != 0 {
				t.Fatalf("failed download exposed an artifact or recorded successful delivery: headers=%v audit=%+v", response.Header(), audit.events)
			}
		})
	}
}
