package capture

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	printpdf "github.com/boxesandglue/baseline-pdf"
	pdfsource "github.com/speedata/pdfdisassembler"
)

func TestPrintPackageKeepsSourceTextGeometryAndRepeatableBytes(t *testing.T) {
	background := samplePrintBackground(t)
	pages := []PrintPackagePage{
		{PageNo: 1, TemplateWidth: 420, TemplateHeight: 594, BarcodeValue: "sample-answer-sheet-question-1"},
		{PageNo: 2, TemplateWidth: 380, TemplateHeight: 340, BarcodeValue: "sample-answer-sheet-question-2"},
	}
	pkg := StudentPrintPackage{
		PrintBatchID: "sample", ExamID: "sample", TemplateID: "sample",
		IssuedAt: time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC), background: background,
		QRLayout: &StudentPrintLayout{Pages: []StudentPrintQRPage{
			{PageNo: 1, X: .03, Y: .70, Width: .24, Height: .27},
			{PageNo: 2, X: .03, Y: .70, Width: .24, Height: .27},
		}},
		Sheets: []PrintPackageSheet{{StudentID: "private-student", SheetSerial: "sample-sheet", Ordinal: 1, Pages: pages}},
	}
	pdf, digest, err := RenderStudentPrintPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	repeated, nextDigest, err := RenderStudentPrintPackage(pkg)
	if err != nil || digest != nextDigest || !bytes.Equal(pdf, repeated) {
		t.Fatalf("same frozen background and print ledger must keep the same PDF: %v", err)
	}
	doc, err := pdfsource.Open(bytes.NewReader(pdf))
	if err != nil {
		t.Fatal(err)
	}
	output, err := doc.Pages()
	if err != nil || len(output) != 2 {
		t.Fatalf("print output pages=%d error=%v", len(output), err)
	}
	for i, want := range [][2]float64{{420, 594}, {380, 340}} {
		page := output[i]
		box, ok := page.Box(pdfsource.MediaBox)
		if !ok || box.Width() != want[0] || box.Height() != want[1] {
			t.Fatalf("page %d did not preserve source dimensions: %+v", i+1, box)
		}
		content, err := page.Content()
		if err != nil || !bytes.Contains(content, []byte("/SheetBackground Do")) || !bytes.Contains(content, []byte(" re f")) {
			t.Fatalf("page %d missing source layer or QR: %v", i+1, err)
		}
	}
	if bytes.Contains(pdf, []byte("private-student")) {
		t.Fatal("printed output must not contain the student's identifier")
	}
	if path := os.Getenv("YUEJUAN_SAMPLE_PRINT_PDF"); path != "" {
		if err := os.WriteFile(path, pdf, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrintPreflightRejectsSmallQRAndMissingSourcePage(t *testing.T) {
	background := samplePrintBackground(t)
	pages := map[int]printPackagePageLayout{1: {width: 420, height: 594, identity: []PrintPackageRegion{
		{ID: "qr", X: .75, Y: .1, Width: .05, Height: .05},
	}}}
	err := validatePrintPackageBackgroundAndGeometry(pages, background)
	if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "第 1 页") {
		t.Fatalf("small dedicated QR must be rejected before issuing: %v", err)
	}
	pages = map[int]printPackagePageLayout{3: {width: 420, height: 594}}
	err = validatePrintPackageBackgroundAndGeometry(pages, background)
	if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "第 3 页") {
		t.Fatalf("missing source page must be rejected before issuing: %v", err)
	}
	_, err = printPackagePages(nil, 1800, 6)
	if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "10000") {
		t.Fatalf("1800 six-page sheets must be rejected before issuing: %v", err)
	}
}

func samplePrintBackground(t *testing.T) *studentPrintBackground {
	t.Helper()
	var data bytes.Buffer
	writer := printpdf.NewPDFWriter(&data)
	font := writer.NewObject()
	err := font.Dict(printpdf.Dict{"Type": "/Font", "Subtype": "/Type1", "BaseFont": "/Helvetica"}).Save()
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		"BT /F0 11 Tf 280 570 Td (EXAM NOTICE) Tj ET BT /F0 16 Tf 40 475 Td (Q1. Identify the correct option) Tj 0 -34 Td (A. Alpha   B. Beta   C. Gamma) Tj ET",
		"BT /F0 16 Tf 0 1 -1 0 115 80 Tm (Q2. Explain your choice) Tj 0 -34 Td (Include a short reason.) Tj ET",
	} {
		stream := writer.NewObject()
		stream.Data = bytes.NewBufferString(content)
		page := writer.AddPage(stream, 0)
		page.Width, page.Height = 420, 594
		page.Dict = printpdf.Dict{"Resources": printpdf.Dict{"Font": printpdf.Dict{"F0": font.ObjectNumber.Ref()}}}
		// 第二页有旋转和非零横向裁切偏移。检查底图能按每页实际方向和尺寸显示。
		if strings.Contains(content, "Q2.") {
			page.Width, page.Height = 440, 400
			page.Dict["CropBox"] = "[30 0 370 380]"
			page.Dict["Rotate"] = "90"
		}
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	return &studentPrintBackground{contentType: "application/pdf", payload: data.Bytes()}
}
