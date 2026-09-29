package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/phpdave11/gofpdf"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	printPageWidthMM  = 210.0
	printPageHeightMM = 297.0
	// Synchronous HTTP rendering is intentionally bounded. Larger issuance
	// runs must be split into print batches or moved to an async artifact job.
	maxPrintPackagePages = 10000
)

// RenderStudentPrintPackage derives printable pages exclusively from the
// immutable print ledger. No student name or school number is placed in the
// PDF or QR payload.
func RenderStudentPrintPackage(pkg StudentPrintPackage) ([]byte, string, error) {
	if pkg.PrintBatchID == "" || pkg.ExamID == "" || pkg.TemplateID == "" || len(pkg.Sheets) == 0 {
		return nil, "", ErrInvalidInput
	}
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(true)
	pdf.SetCatalogSort(true)
	// 使用签发时间固定 PDF 元数据，重复下载同一份打印台账才会产生相同字节与摘要。
	pdf.SetCreationDate(pkg.IssuedAt)
	pdf.SetModificationDate(pkg.IssuedAt)
	pdf.SetTitle("EduGrade controlled answer sheets", true)
	pdf.SetAuthor("EduGrade", true)
	pdf.SetSubject("Student-bound answer sheet print package", true)
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)

	totalPages := 0
	for _, sheet := range pkg.Sheets {
		totalPages += len(sheet.Pages)
	}
	if totalPages == 0 || totalPages > maxPrintPackagePages {
		return nil, "", ErrInvalidInput
	}
	for _, sheet := range pkg.Sheets {
		if sheet.StudentID == "" || sheet.SheetSerial == "" || sheet.Ordinal <= 0 || len(sheet.Pages) == 0 {
			return nil, "", ErrInvalidInput
		}
		for _, page := range sheet.Pages {
			if err := renderStudentPrintPage(pdf, pkg, sheet, page); err != nil {
				return nil, "", err
			}
		}
	}
	if pdf.Error() != nil {
		return nil, "", pdf.Error()
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), "sha256:" + hex.EncodeToString(sum[:]), nil
}

func renderStudentPrintPage(pdf *gofpdf.Fpdf, pkg StudentPrintPackage, sheet PrintPackageSheet, page PrintPackagePage) error {
	if page.PageNo <= 0 || page.TemplateWidth <= 0 || page.TemplateHeight <= 0 || page.BarcodeValue == "" {
		return ErrInvalidInput
	}
	code, err := qrcode.New(page.BarcodeValue, qrcode.High)
	if err != nil {
		return fmt.Errorf("encode controlled QR: %w", err)
	}
	pdf.AddPage()
	pdf.SetDrawColor(20, 20, 20)
	pdf.SetFillColor(20, 20, 20)
	drawRegistrationMark(pdf, 7, 7)
	drawRegistrationMark(pdf, printPageWidthMM-11, 7)
	drawRegistrationMark(pdf, 7, printPageHeightMM-11)
	drawRegistrationMark(pdf, printPageWidthMM-11, printPageHeightMM-11)

	pdf.SetFont("Helvetica", "B", 15)
	pdf.SetTextColor(18, 18, 18)
	pdf.Text(14, 18, "EduGrade Controlled Answer Sheet")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.Text(14, 25, "Exam ref: "+shortRef(pkg.ExamID))
	pdf.Text(14, 30, "Template ref: "+shortRef(pkg.TemplateID))
	pdf.Text(14, 35, "Roster position: "+strconv.Itoa(sheet.Ordinal))
	pdf.Text(14, 40, "Student ref: "+shortRef(sheet.StudentID))
	pdf.Text(14, 45, "Sheet serial: "+sheet.SheetSerial)
	pdf.Text(14, 50, fmt.Sprintf("Page %d of %d", page.PageNo, len(sheet.Pages)))

	drawQRCode(pdf, code.Bitmap(), 164, 13, 32)
	pdf.SetFont("Helvetica", "", 5.5)
	pdf.Text(164, 48, "Do not cover, crop, or alter this code")

	pdf.SetLineWidth(0.3)
	pdf.Line(12, 54, 198, 54)
	pdf.SetFont("Helvetica", "", 6.5)
	pdf.SetTextColor(85, 85, 85)
	pdf.Text(14, 291, "Controlled copy - "+shortHash(pkg.TemplateContentHash)+" - key "+safeASCII(pkg.KeyID))

	contentX, contentY, contentW, contentH := 12.0, 60.0, 186.0, 222.0
	pdf.SetDrawColor(35, 35, 35)
	if len(page.QuestionRegions) == 0 {
		drawRuledAnswerArea(pdf, contentX, contentY, contentW, contentH)
		return nil
	}
	for index, region := range page.QuestionRegions {
		x, y, width, height, ok := mapPrintRegion(region, contentX, contentY, contentW, contentH)
		if !ok {
			continue
		}
		pdf.SetLineWidth(0.25)
		pdf.Rect(x, y, width, height, "D")
		label := safeASCII(region.Label)
		if label == "" {
			label = "Q" + strconv.Itoa(index+1)
		}
		pdf.SetTextColor(30, 30, 30)
		pdf.SetFont("Helvetica", "B", 8)
		pdf.SetFillColor(255, 255, 255)
		labelWidth := math.Min(math.Max(width-2, 4), 18)
		pdf.Rect(x+1, y-1.5, labelWidth, 4, "F")
		pdf.Text(x+2, y+1.5, label)
	}
	return nil
}

func drawQRCode(pdf *gofpdf.Fpdf, bitmap [][]bool, x, y, size float64) {
	if len(bitmap) == 0 {
		return
	}
	moduleSize := size / float64(len(bitmap))
	pdf.SetFillColor(0, 0, 0)
	for rowIndex, row := range bitmap {
		runStart := -1
		for columnIndex := 0; columnIndex <= len(row); columnIndex++ {
			dark := columnIndex < len(row) && row[columnIndex]
			if dark && runStart < 0 {
				runStart = columnIndex
			}
			if !dark && runStart >= 0 {
				pdf.Rect(
					x+float64(runStart)*moduleSize,
					y+float64(rowIndex)*moduleSize,
					float64(columnIndex-runStart)*moduleSize,
					moduleSize,
					"F",
				)
				runStart = -1
			}
		}
	}
}

func drawRegistrationMark(pdf *gofpdf.Fpdf, x, y float64) {
	pdf.Rect(x, y, 4, 4, "F")
}

func drawRuledAnswerArea(pdf *gofpdf.Fpdf, x, y, width, height float64) {
	pdf.Rect(x, y, width, height, "D")
	pdf.SetDrawColor(205, 205, 205)
	for lineY := y + 12; lineY < y+height; lineY += 12 {
		pdf.Line(x+3, lineY, x+width-3, lineY)
	}
}

// 题区坐标是 0 到 1 的相对比例，映射目标为页内答题区域的毫米尺寸。
func mapPrintRegion(region PrintPackageRegion, x, y, width, height float64) (float64, float64, float64, float64, bool) {
	values := []float64{region.X, region.Y, region.Width, region.Height}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, 0, 0, 0, false
		}
	}
	if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 ||
		region.X+region.Width > 1.000001 || region.Y+region.Height > 1.000001 {
		return 0, 0, 0, 0, false
	}
	return x + region.X*width, y + region.Y*height, region.Width * width, region.Height * height, true
}

func shortRef(value string) string {
	value = safeASCII(value)
	if len(value) <= 18 {
		return value
	}
	return value[:8] + "..." + value[len(value)-6:]
}

func shortHash(value string) string {
	value = safeASCII(value)
	if len(value) <= 22 {
		return value
	}
	return value[:22]
}

func safeASCII(value string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= 32 && r <= unicode.MaxASCII && r != '(' && r != ')' && r != '\\' {
			out.WriteRune(r)
		}
	}
	return out.String()
}
