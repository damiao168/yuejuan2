package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"time"

	printpdf "github.com/boxesandglue/baseline-pdf"
	"github.com/boxesandglue/gofpdi"
	qrcode "github.com/skip2/go-qrcode"
	pdfsource "github.com/speedata/pdfdisassembler"
)

const pdfPointsPerMM = 72.0 / 25.4

type printBackgroundPage struct {
	width, height float64
	form          int
	matrix        [6]float64
	image         *printpdf.Imagefile
}

func makePrintPDF(out *bytes.Buffer, issuedAt time.Time) *printpdf.PDF {
	writer := printpdf.NewPDFWriter(out)
	writer.InfoDict["Title"] = "(EduGrade controlled answer sheets)"
	writer.InfoDict["CreationDate"] = "(D:" + issuedAt.UTC().Format("20060102150405") + "Z)"
	return writer
}

func loadPrintBackgroundPages(writer *printpdf.PDF, background *studentPrintBackground, pages []PrintPackagePage) (map[int]printBackgroundPage, error) {
	if background.contentType != "application/pdf" {
		if len(pages) != 1 || pages[0].PageNo != 1 {
			return nil, printPackageInputError{"图片底图只能用于单页答题卡。"}
		}
		img, err := writer.LoadImageFromReader(bytes.NewReader(background.payload), "", 1)
		if err != nil || img.Format == "pdf" || img.W <= 0 || img.H <= 0 {
			return nil, printPackageInputError{"无法读取答题卡底图图片。"}
		}
		// 图片没有可信物理纸张尺寸。保持图像比例，以短边 210 毫米打印。
		w, h, err := printPageSizeMM(PrintPackagePage{TemplateWidth: img.W, TemplateHeight: img.H})
		if err != nil {
			return nil, err
		}
		return map[int]printBackgroundPage{1: {width: w * pdfPointsPerMM, height: h * pdfPointsPerMM, image: img}}, nil
	}
	src, err := pdfsource.Open(bytes.NewReader(background.payload))
	if err != nil {
		return nil, printPackageInputError{"无法读取答题卡底图 PDF。"}
	}
	importer := gofpdi.NewImporter()
	importer.SetObjIDGetter(func() int { return int(writer.NextObject()) })
	if err = importer.SetSourceStream(bytes.NewReader(background.payload)); err != nil {
		return nil, printPackageInputError{"无法导入答题卡底图 PDF，请检查文件是否损坏或加密。"}
	}
	result := make(map[int]printBackgroundPage, len(pages))
	for _, page := range pages {
		pg, pgErr := src.Page(page.PageNo - 1)
		if pgErr != nil {
			return nil, printPageError(page.PageNo, "底图缺少该页。")
		}
		box, ok := pg.Box(pdfsource.MediaBox)
		if !ok {
			return nil, printPageError(page.PageNo, "底图缺少纸张尺寸。")
		}
		if crop, hasCrop := pg.Box(pdfsource.CropBox); hasCrop {
			box.LLX, box.LLY = max(crop.LLX, box.LLX), max(crop.LLY, box.LLY)
			box.URX, box.URY = min(crop.URX, box.URX), min(crop.URY, box.URY)
		}
		w, h := box.Width(), box.Height()
		if !finitePrintNumber(w) || !finitePrintNumber(h) || w <= 0 || h <= 0 {
			return nil, printPageError(page.PageNo, "底图页面范围无效。")
		}
		in := printBackgroundPage{width: w, height: h}
		rotation := pg.Rotation()
		if rotation == 90 || rotation == 270 {
			in.width, in.height = h, w
		}
		in.matrix = compensateImportedPageMatrix(box, rotation)
		index, importErr := importer.ImportPage(page.PageNo, "/CropBox")
		if importErr != nil {
			return nil, printPageError(page.PageNo, "底图页面无法用于打印。")
		}
		in.form = index
		result[page.PageNo] = in
	}
	names, err := importer.PutFormXobjects()
	if err != nil {
		return nil, printPackageInputError{"答题卡底图 PDF 含无法导入的页面资源。"}
	}
	imported := importer.GetImportedObjects()
	for _, id := range slices.Sorted(maps.Keys(imported)) {
		obj := writer.NewObjectWithNumber(printpdf.Objectnumber(id))
		obj.Raw = true
		obj.Data = bytes.NewBuffer(imported[id])
		if err := obj.Save(); err != nil {
			return nil, err
		}
	}
	for pageNo, page := range result {
		page.form = names[fmt.Sprintf("/GOFPDITPL%d", page.form)]
		if page.form <= 0 {
			return nil, printPageError(pageNo, "底图页面未完成导入。")
		}
		result[pageNo] = page
	}
	return result, nil
}

func compensateImportedPageMatrix(box pdfsource.Rect, rotation int) [6]float64 {
	if box.LLX != 0 && box.LLY != 0 && box.URX != 0 && box.URY != 0 {
		return [6]float64{1, 0, 0, 1, 0, 0}
	}
	// gofpdi v1.0.26 对含零坐标的裁切框只移动原点，未应用页旋转。
	// 先抵消导入器的移动，再将可见裁切框按原来的 PDF 方向归到输出页原点。
	x, y := -box.LLX, 3*box.LLY
	w, h := box.Width(), box.Height()
	switch rotation {
	case 90:
		return [6]float64{0, -1, 1, 0, -y, x + w}
	case 180:
		return [6]float64{-1, 0, 0, -1, x + w, y + h}
	case 270:
		return [6]float64{0, 1, -1, 0, y + h, -x}
	default:
		return [6]float64{1, 0, 0, 1, -x, -y}
	}
}

func validatePrintPackageBackgroundAndGeometry(pages map[int]printPackagePageLayout, background *studentPrintBackground, layouts ...*StudentPrintLayout) error {
	var layout *StudentPrintLayout
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	sheet := PrintPackageSheet{StudentID: "print-preflight", SheetSerial: "print-preflight", Ordinal: 1}
	for _, number := range slices.Sorted(maps.Keys(pages)) {
		page := pages[number]
		sheet.Pages = append(sheet.Pages, printPageWithConfirmedQR(PrintPackagePage{PageNo: number, TemplateWidth: page.width,
			TemplateHeight: page.height, RegistrationMarks: page.marks, IdentityRegions: page.identity,
			QuestionRegions: page.regions, GradingGroups: page.groups, BarcodeValue: "edugrade-print-preflight"}, layout))
	}
	// 实际走一次单份渲染，覆盖图片解码、PDF 页面资源、二维码几何和写出。
	// 同一批仅二维码值变化，渲染模板无需为每名学生重新解析。
	_, _, err := renderStudentPrintWithBackground(StudentPrintPackage{Sheets: []PrintPackageSheet{sheet}, background: background})
	return err
}

func printPageError(page int, reason string) error {
	return printPackageInputError{fmt.Sprintf("答题卡第 %d 页：%s", page, reason)}
}

func renderStudentPrintWithBackground(pkg StudentPrintPackage) ([]byte, string, error) {
	var result bytes.Buffer
	writer := makePrintPDF(&result, pkg.IssuedAt)
	if len(pkg.Sheets) == 0 || len(pkg.Sheets[0].Pages) == 0 {
		return nil, "", ErrInvalidInput
	}
	pages, err := loadPrintBackgroundPages(writer, pkg.background, pkg.Sheets[0].Pages)
	if err != nil {
		return nil, "", err
	}
	total := 0
	for _, sheet := range pkg.Sheets {
		total += len(sheet.Pages)
		if total > maxPrintPackagePages || sheet.StudentID == "" || sheet.SheetSerial == "" || sheet.Ordinal <= 0 || len(sheet.Pages) == 0 {
			return nil, "", ErrInvalidInput
		}
		for _, page := range sheet.Pages {
			src, ok := pages[page.PageNo]
			if !ok {
				return nil, "", ErrInvalidInput
			}
			if err := addStudentPrintBackgroundPage(writer, src, printPageWithConfirmedQR(page, pkg.QRLayout)); err != nil {
				return nil, "", err
			}
		}
	}
	if err := writer.Finish(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(result.Bytes())
	return result.Bytes(), "sha256:" + hex.EncodeToString(sum[:]), nil
}

func addStudentPrintBackgroundPage(writer *printpdf.PDF, src printBackgroundPage, page PrintPackagePage) error {
	if page.PageNo <= 0 || page.TemplateWidth <= 0 || page.TemplateHeight <= 0 || page.BarcodeValue == "" {
		return ErrInvalidInput
	}
	qr, err := printQRRegion(page, src.width/pdfPointsPerMM, src.height/pdfPointsPerMM)
	if err != nil {
		if err == ErrPrintLayoutConfirmationRequired {
			return err
		}
		return printPageError(page.PageNo, "二维码区过小、与题区重叠，或没有可放置二维码的空间。")
	}
	code, err := qrcode.New(page.BarcodeValue, qrcode.High)
	if err != nil {
		return printPageError(page.PageNo, "条码内容无法生成二维码。")
	}
	stream := writer.NewObject()
	stream.Data = new(bytes.Buffer)
	stream.SetCompression(6)
	outputPage := writer.AddPage(stream, 0)
	outputPage.Width, outputPage.Height = src.width, src.height
	if src.image != nil {
		outputPage.Images = []*printpdf.Imagefile{src.image}
		fmt.Fprintf(stream.Data, "q %.5f 0 0 %.5f 0 0 cm %s Do Q\n", src.width, src.height, src.image.InternalName())
	} else {
		outputPage.Dict = printpdf.Dict{"Resources": printpdf.Dict{"XObject": printpdf.Dict{"SheetBackground": printpdf.Objectnumber(src.form).Ref()}}}
		m := src.matrix
		fmt.Fprintf(stream.Data, "q %.5f %.5f %.5f %.5f %.5f %.5f cm /SheetBackground Do Q\n", m[0], m[1], m[2], m[3], m[4], m[5])
	}
	// 模板坐标从左上测量，PDF 指令从左下测量。白色底包含二维码的静区。
	x, y, size := qr.X*src.width, (1-qr.Y-qr.Height)*src.height, qr.Width*src.width
	fmt.Fprintf(stream.Data, "q 1 g %.5f %.5f %.5f %.5f re f 0 g\n", x, y, size, size)
	bitmap := code.Bitmap()
	unit := size / float64(len(bitmap))
	for rowIndex, row := range bitmap {
		for column := 0; column < len(row); {
			begin := column
			for column < len(row) && row[column] {
				column++
			}
			if column == begin {
				column++
				continue
			}
			fmt.Fprintf(stream.Data, "%.5f %.5f %.5f %.5f re f\n", x+float64(begin)*unit, y+size-float64(rowIndex+1)*unit, float64(column-begin)*unit, unit)
		}
	}
	stream.Data.WriteString("Q\n")
	return nil
}
