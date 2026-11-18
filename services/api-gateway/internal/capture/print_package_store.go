package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type printPackagePageLayout struct {
	width, height           int
	regions                 []PrintPackageRegion
	marks, identity, groups []PrintPackageRegion
}

func (s *PostgresStore) GetStudentPrintPackage(ctx context.Context, tenantID, printBatchID string) (StudentPrintPackage, error) {
	var out StudentPrintPackage
	var layoutRaw, printLayoutRaw []byte
	var expectedSheetCount, expectedPageCount int
	if err := s.db.QueryRowContext(ctx, `
SELECT
  b.id::text,b.exam_id::text,e.name,b.template_id::text,
  b.template_content_hash,b.key_id,b.issued_at,b.sheet_count,b.page_count,t.layout,b.print_layout
FROM answer_sheet_print_batch b
JOIN exam e
  ON e.tenant_id=b.tenant_id AND e.id=b.exam_id AND e.deleted_at IS NULL
JOIN answer_sheet_template t
  ON t.tenant_id=b.tenant_id AND t.id=b.template_id AND t.exam_id=b.exam_id
  AND t.content_hash=b.template_content_hash AND t.status='locked' AND t.deleted_at IS NULL
WHERE b.tenant_id=$1 AND b.id=$2::uuid
`, tenantID, printBatchID).Scan(
		&out.PrintBatchID, &out.ExamID, &out.ExamName, &out.TemplateID,
		&out.TemplateContentHash, &out.KeyID, &out.IssuedAt,
		&expectedSheetCount, &expectedPageCount, &layoutRaw, &printLayoutRaw,
	); err != nil {
		return StudentPrintPackage{}, mapNotFound(err)
	}
	pagesByNumber, err := printPackagePages(layoutRaw, expectedSheetCount, expectedPageCount)
	if err != nil {
		return StudentPrintPackage{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
  sh.student_id::text,sh.id::text,sh.ordinal,sh.status,
  p.page_no,p.barcode_value
FROM answer_sheet_print_sheet sh
JOIN answer_sheet_print_page p
  ON p.tenant_id=sh.tenant_id AND p.print_sheet_id=sh.id
WHERE sh.tenant_id=$1 AND sh.print_batch_id=$2::uuid
ORDER BY sh.ordinal,p.page_no
`, tenantID, printBatchID)
	if err != nil {
		return StudentPrintPackage{}, err
	}
	defer rows.Close()

	out.IssuedAt = out.IssuedAt.UTC()
	out.Sheets = []PrintPackageSheet{}
	currentSerial := ""
	for rows.Next() {
		var studentID, sheetSerial, status, barcodeValue string
		var ordinal, pageNo int
		if err = rows.Scan(&studentID, &sheetSerial, &ordinal, &status, &pageNo, &barcodeValue); err != nil {
			return StudentPrintPackage{}, err
		}
		// 序列号已被观察到或作废后，再次下载旧打印包可能生成无法区分的第二份实体副本；替换必须经过显式撤销和重印生命周期。
		if status != "issued" {
			return StudentPrintPackage{}, ErrInvalidTransition
		}
		layoutPage, ok := pagesByNumber[pageNo]
		if !ok {
			return StudentPrintPackage{}, ErrInvalidTransition
		}
		if sheetSerial != currentSerial {
			out.Sheets = append(out.Sheets, PrintPackageSheet{
				StudentID: studentID, SheetSerial: sheetSerial, Ordinal: ordinal, Pages: []PrintPackagePage{},
			})
			currentSerial = sheetSerial
		}
		last := len(out.Sheets) - 1
		out.Sheets[last].Pages = append(out.Sheets[last].Pages, PrintPackagePage{
			PageNo: pageNo, TemplateWidth: layoutPage.width, TemplateHeight: layoutPage.height,
			BarcodeValue: barcodeValue, QuestionRegions: layoutPage.regions,
			RegistrationMarks: layoutPage.marks, IdentityRegions: layoutPage.identity, GradingGroups: layoutPage.groups,
		})
	}
	if err = rows.Err(); err != nil {
		return StudentPrintPackage{}, err
	}
	if err := validatePrintPackageSheetCounts(out.Sheets, expectedSheetCount, expectedPageCount); err != nil {
		return StudentPrintPackage{}, err
	}
	out.background, err = s.readStudentPrintBackground(ctx, s.db, tenantID, out.TemplateID, out.TemplateContentHash, layoutRaw)
	if err != nil {
		return StudentPrintPackage{}, err
	}
	storedLayout, err := decodeStudentPrintLayout(printLayoutRaw)
	if err != nil {
		return StudentPrintPackage{}, err
	}
	out.QRLayout, err = normalizedStudentPrintLayout(pagesByNumber, out.background.sha256, storedLayout)
	if err != nil {
		return StudentPrintPackage{}, err
	}
	return out, nil
}

// 台账数量和模板页数必须先通过边界校验，后面才允许逐页组装可下载包。
func printPackagePages(layoutRaw []byte, sheetCount, pageCount int) (map[int]printPackagePageLayout, error) {
	if sheetCount <= 0 || pageCount <= 0 || sheetCount > maxPrintPackagePages/pageCount {
		return nil, printPackageInputError{fmt.Sprintf("每批最多打印 %d 页，请减少学生人数后分批签发。", maxPrintPackagePages)}
	}
	var layout paper.TemplateLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil || len(layout.Pages) == 0 || len(layout.Pages) != pageCount {
		return nil, ErrInvalidTransition
	}
	pagesByNumber := make(map[int]printPackagePageLayout, len(layout.Pages))
	for _, page := range layout.Pages {
		if page.PageNo <= 0 || page.Width <= 0 || page.Height <= 0 {
			return nil, ErrInvalidTransition
		}
		resolvedGroups, err := paper.ResolveTemplateGradingGroups(page)
		if err != nil {
			return nil, ErrInvalidTransition
		}
		groups := make([]PrintPackageRegion, 0, len(resolvedGroups))
		for _, group := range resolvedGroups {
			groups = append(groups, PrintPackageRegion{ID: group.GroupID, X: group.BBox.X, Y: group.BBox.Y, Width: group.BBox.Width, Height: group.BBox.Height})
		}
		pagesByNumber[page.PageNo] = printPackagePageLayout{width: page.Width, height: page.Height, regions: printRegions(page.QuestionRegions), marks: printRegions(page.RegistrationMarks), identity: printRegions(page.IdentityRegions), groups: groups}
	}
	return pagesByNumber, nil
}

type printPackageInputError struct{ message string }

func (err printPackageInputError) Error() string { return err.message }
func (err printPackageInputError) Unwrap() error { return ErrInvalidTransition }

func (s *PostgresStore) prepareStudentPrintPackageTx(ctx context.Context, tx *sql.Tx, tenantID, templateID, templateHash string, layoutRaw []byte, sheetCount, pageCount int, input *StudentPrintLayout) (*StudentPrintLayout, error) {
	pages, err := printPackagePages(layoutRaw, sheetCount, pageCount)
	if err != nil {
		return nil, err
	}
	background, err := s.readStudentPrintBackground(ctx, tx, tenantID, templateID, templateHash, layoutRaw)
	if err != nil {
		return nil, err
	}
	layout, err := normalizedStudentPrintLayout(pages, background.sha256, input)
	if err != nil {
		return nil, err
	}
	// 用和下载相同的背景与页面几何做签发预检。失败时整笔签发或补打尚未入账。
	if err := validatePrintPackageBackgroundAndGeometry(pages, background, layout); err != nil {
		return nil, err
	}
	return layout, nil
}

func printRegions(regions []paper.LayoutRegion) []PrintPackageRegion {
	out := make([]PrintPackageRegion, 0, len(regions))
	for _, region := range regions {
		item := PrintPackageRegion{ID: region.ID, QuestionID: region.QuestionID, Label: region.Label, X: region.X, Y: region.Y, Width: region.Width, Height: region.Height, OptionCoordinateSpace: region.OptionCoordinateSpace}
		for _, option := range region.OptionRegions {
			item.OptionRegions = append(item.OptionRegions, PrintPackageRegion{ID: option.ID, Label: option.Label, X: option.X, Y: option.Y, Width: option.Width, Height: option.Height})
		}
		out = append(out, item)
	}
	return out
}

func validatePrintPackageSheetCounts(sheets []PrintPackageSheet, sheetCount, pageCount int) error {
	if len(sheets) == 0 {
		return mapNotFound(sql.ErrNoRows)
	}
	if len(sheets) != sheetCount {
		return ErrInvalidTransition
	}
	for _, sheet := range sheets {
		if len(sheet.Pages) != pageCount {
			return ErrInvalidTransition
		}
	}
	return nil
}
