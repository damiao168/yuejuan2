package capture

import (
	"context"
	"database/sql"
	"encoding/json"
)

type printPackageLayout struct {
	Pages []struct {
		PageNo          int                  `json:"page_no"`
		Width           int                  `json:"width"`
		Height          int                  `json:"height"`
		QuestionRegions []PrintPackageRegion `json:"question_regions"`
	} `json:"pages"`
}

func (s *PostgresStore) GetStudentPrintPackage(ctx context.Context, tenantID, printBatchID string) (StudentPrintPackage, error) {
	var out StudentPrintPackage
	var layoutRaw []byte
	var expectedSheetCount, expectedPageCount int
	if err := s.db.QueryRowContext(ctx, `
SELECT
  b.id::text,b.exam_id::text,e.name,b.template_id::text,
  b.template_content_hash,b.key_id,b.issued_at,b.sheet_count,b.page_count,t.layout
FROM answer_sheet_print_batch b
JOIN exam e
  ON e.tenant_id=b.tenant_id AND e.id=b.exam_id AND e.deleted_at IS NULL
JOIN answer_sheet_template t
  ON t.tenant_id=b.tenant_id AND t.id=b.template_id AND t.deleted_at IS NULL
WHERE b.tenant_id=$1 AND b.id=$2::uuid
`, tenantID, printBatchID).Scan(
		&out.PrintBatchID, &out.ExamID, &out.ExamName, &out.TemplateID,
		&out.TemplateContentHash, &out.KeyID, &out.IssuedAt,
		&expectedSheetCount, &expectedPageCount, &layoutRaw,
	); err != nil {
		return StudentPrintPackage{}, mapNotFound(err)
	}
	// 台账数量和模板页数必须先通过边界校验，后面才允许逐页组装可下载包。
	if expectedSheetCount <= 0 || expectedPageCount <= 0 ||
		expectedSheetCount > maxPrintPackagePages/expectedPageCount {
		return StudentPrintPackage{}, ErrInvalidTransition
	}
	var layout printPackageLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil ||
		len(layout.Pages) == 0 || len(layout.Pages) != expectedPageCount {
		return StudentPrintPackage{}, ErrInvalidTransition
	}
	pagesByNumber := make(map[int]struct {
		width, height int
		regions       []PrintPackageRegion
	}, len(layout.Pages))
	for _, page := range layout.Pages {
		if page.PageNo <= 0 || page.Width <= 0 || page.Height <= 0 {
			return StudentPrintPackage{}, ErrInvalidTransition
		}
		pagesByNumber[page.PageNo] = struct {
			width, height int
			regions       []PrintPackageRegion
		}{page.Width, page.Height, page.QuestionRegions}
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
		// Once a serial has been observed or invalidated, downloading its old
		// package again could create an indistinguishable second physical copy.
		// Replacement must go through the explicit revoke/reprint lifecycle.
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
		})
	}
	if err = rows.Err(); err != nil {
		return StudentPrintPackage{}, err
	}
	if len(out.Sheets) == 0 {
		return StudentPrintPackage{}, mapNotFound(sql.ErrNoRows)
	}
	if len(out.Sheets) != expectedSheetCount {
		return StudentPrintPackage{}, ErrInvalidTransition
	}
	for _, sheet := range out.Sheets {
		if len(sheet.Pages) != expectedPageCount {
			return StudentPrintPackage{}, ErrInvalidTransition
		}
	}
	return out, nil
}
