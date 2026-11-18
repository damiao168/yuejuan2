package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/phpdave11/gofpdf"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	// 同步 HTTP 渲染有明确规模上限；更大的签发任务必须拆成打印批次，或移至异步产物任务。
	maxPrintPackagePages = 10000
)

// RenderStudentPrintPackage 仅从不可变打印台账派生可打印页面；PDF 和 QR 载荷中都不放入学生姓名或学号。
func RenderStudentPrintPackage(pkg StudentPrintPackage) ([]byte, string, error) {
	if pkg.PrintBatchID == "" || pkg.ExamID == "" || pkg.TemplateID == "" || len(pkg.Sheets) == 0 {
		return nil, "", ErrInvalidInput
	}
	if pkg.background != nil {
		return renderStudentPrintWithBackground(pkg)
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
	page = printPageWithConfirmedQR(page, pkg.QRLayout)
	if page.PageNo <= 0 || page.TemplateWidth <= 0 || page.TemplateHeight <= 0 || page.BarcodeValue == "" {
		return ErrInvalidInput
	}
	code, err := qrcode.New(page.BarcodeValue, qrcode.High)
	if err != nil {
		return fmt.Errorf("encode controlled QR: %w", err)
	}
	widthMM, heightMM, err := printPageSizeMM(page)
	if err != nil {
		return err
	}
	qr, err := printQRRegion(page, widthMM, heightMM)
	if err != nil {
		return err
	}
	pdf.AddPageFormat("P", gofpdf.SizeType{Wd: widthMM, Ht: heightMM})
	pdf.SetDrawColor(20, 20, 20)
	pdf.SetFillColor(20, 20, 20)
	// 定位标记、身份区和题区共用原模板整页坐标；不再生成模板未声明的固定四角或内框。
	for _, mark := range page.RegistrationMarks {
		x, y, w, h, ok := mapPrintRegion(mark, 0, 0, widthMM, heightMM)
		if !ok {
			return ErrInvalidInput
		}
		pdf.Rect(x, y, w, h, "F")
	}
	regions := append(append([]PrintPackageRegion{}, page.GradingGroups...), page.QuestionRegions...)
	for _, identity := range page.IdentityRegions {
		if !namedQRRegion(identity) {
			regions = append(regions, identity)
		}
	}
	if err := drawPrintRegions(pdf, regions, widthMM, heightMM); err != nil {
		return err
	}
	x, y, w, _, _ := mapPrintRegion(qr, 0, 0, widthMM, heightMM)
	drawQRCode(pdf, code.Bitmap(), x, y, w)
	return nil
}

func drawPrintRegions(pdf *gofpdf.Fpdf, regions []PrintPackageRegion, widthMM, heightMM float64) error {
	for _, region := range regions {
		x, y, w, h, ok := mapPrintRegion(region, 0, 0, widthMM, heightMM)
		if !ok {
			return ErrInvalidInput
		}
		pdf.SetLineWidth(.25)
		pdf.Rect(x, y, w, h, "D")
		for _, option := range printQuestionOptions(region) {
			ox, oy, ow, oh, ok := mapPrintRegion(option, 0, 0, widthMM, heightMM)
			if !ok {
				return ErrInvalidInput
			}
			pdf.Rect(ox, oy, ow, oh, "D")
		}
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
		// len(row) 是冲刷末尾黑段的哨兵；短路条件保证该位置和空行都不读取 bitmap。
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

// 所有模板区域都是整页归一化比例，调用者必须传入同一整页的毫米尺寸。
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
