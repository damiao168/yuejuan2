package capture

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderStudentPrintPackageIsDeterministicAndContainsOnePagePerLedgerPage(t *testing.T) {
	pkg := StudentPrintPackage{
		PrintBatchID:        "11111111-1111-4111-8111-111111111111",
		ExamID:              "22222222-2222-4222-8222-222222222222",
		TemplateID:          "33333333-3333-4333-8333-333333333333",
		TemplateContentHash: "sha256:" + strings.Repeat("a", 64),
		KeyID:               "2026-01",
		IssuedAt:            time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
		Sheets: []PrintPackageSheet{{
			StudentID:   "44444444-4444-4444-8444-444444444444",
			SheetSerial: "55555555-5555-4555-8555-555555555555",
			Ordinal:     1,
			Pages: []PrintPackagePage{
				{
					PageNo: 1, TemplateWidth: 1000, TemplateHeight: 1400,
					BarcodeValue: "EG1." + strings.Repeat("A", 180) + ".signature",
					QuestionRegions: []PrintPackageRegion{
						{Label: "Q1", X: 0.08, Y: 0.1, Width: 0.84, Height: 0.25},
						{Label: "Q2", X: 0.08, Y: 0.45, Width: 0.84, Height: 0.35},
					},
				},
				{
					PageNo: 2, TemplateWidth: 1000, TemplateHeight: 1400,
					BarcodeValue: "EG1." + strings.Repeat("B", 180) + ".signature",
				},
			},
		}},
	}

	// 打印台账是不可变凭据；相同输入必须得到相同 PDF 字节和摘要，便于下载核对。
	first, firstDigest, err := RenderStudentPrintPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := RenderStudentPrintPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstDigest != secondDigest {
		index := firstByteDifference(first, second)
		t.Fatalf("immutable print ledger must render byte-identical packages: first_difference=%d first_digest=%s second_digest=%s",
			index, firstDigest, secondDigest)
	}
	if !bytes.HasPrefix(first, []byte("%PDF-")) || !bytes.Contains(first, []byte("%%EOF")) {
		t.Fatal("renderer did not produce a complete PDF")
	}
	if got := bytes.Count(first, []byte("/Type /Page\n")); got != 2 {
		t.Fatalf("expected one PDF page per print-ledger page, got %d", got)
	}
	if !strings.HasPrefix(firstDigest, "sha256:") || len(firstDigest) != len("sha256:")+64 {
		t.Fatalf("unexpected package digest: %q", firstDigest)
	}
}

func firstByteDifference(first, second []byte) int {
	for index := 0; index < len(first) && index < len(second); index++ {
		if first[index] != second[index] {
			return index
		}
	}
	if len(first) != len(second) {
		return min(len(first), len(second))
	}
	return -1
}

func TestRenderStudentPrintPackageRejectsInvalidLedger(t *testing.T) {
	if _, _, err := RenderStudentPrintPackage(StudentPrintPackage{}); err == nil {
		t.Fatal("empty print ledger must be rejected")
	}
}
