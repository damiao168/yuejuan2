package scorerelease

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type scoreSheet struct {
	Name, Title, Subtitle string
	Header                []string
	Rows                  [][]any
	Widths                []float64
	FreezeColumns         int
	Footer                bool
}

func spreadsheetColumn(index int) string {
	name := ""
	for index++; index > 0; index = (index - 1) / 26 {
		name = string(rune('A'+(index-1)%26)) + name
	}
	return name
}

func spreadsheetText(value string) string {
	// XML 1.0 不接受控制字符；保留换行和制表符，其余非法字符移除，避免整份工作簿无法打开。
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xfffe && r != 0xffff {
			return r
		}
		return -1
	}, value)
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

func spreadsheetCell(ref string, value any, style int) string {
	// 样式编号对应下方cellXfs的顺序；文本不套数字格式，整数和小数分别显示，避免学号丢零或出现多余小数点。
	if _, text := value.(string); text && style == 3 {
		style = 0
	}
	if number, numeric := value.(float64); numeric && number != math.Trunc(number) {
		if style == 3 {
			style = 6
		}
		if style == 5 {
			style = 7
		}
	}
	prefix := fmt.Sprintf(`<c r="%s" s="%d"`, ref, style)
	switch number := value.(type) {
	case float64:
		return prefix + `><v>` + strconv.FormatFloat(number, 'f', -1, 64) + `</v></c>`
	case int:
		return prefix + `><v>` + strconv.Itoa(number) + `</v></c>`
	case nil:
		return prefix + `/>`
	default:
		// 学号和可能以 =、+ 开头的姓名始终写作文本，既保留前导零，也不会被 Excel 当成公式。
		return prefix + ` t="inlineStr"><is><t xml:space="preserve">` + spreadsheetText(fmt.Sprint(value)) + `</t></is></c>`
	}
}

func scoreSheetXML(sheet scoreSheet) string {
	var out strings.Builder
	last := spreadsheetColumn(len(sheet.Header) - 1)
	out.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetPr><pageSetUpPr fitToPage="1"/></sheetPr><sheetViews><sheetView workbookViewId="0">`)
	frozen := sheet.FreezeColumns
	if frozen > 0 {
		fmt.Fprintf(&out, `<pane xSplit="%d" ySplit="3" topLeftCell="%s4" activePane="bottomRight" state="frozen"/>`, frozen, spreadsheetColumn(frozen))
	} else {
		out.WriteString(`<pane ySplit="3" topLeftCell="A4" activePane="bottomLeft" state="frozen"/>`)
	}
	out.WriteString(`</sheetView></sheetViews><sheetFormatPr defaultRowHeight="21"/><cols>`)
	for index := range sheet.Header {
		width := 16.0
		if index < len(sheet.Widths) {
			width = sheet.Widths[index]
		}
		fmt.Fprintf(&out, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, index+1, index+1, width)
	}
	out.WriteString(`</cols><sheetData><row r="1" ht="30" customHeight="1">`)
	out.WriteString(spreadsheetCell("A1", sheet.Title, 1))
	out.WriteString(`</row><row r="2" ht="30" customHeight="1">`)
	out.WriteString(spreadsheetCell("A2", sheet.Subtitle, 4))
	out.WriteString(`</row><row r="3" ht="26" customHeight="1">`)
	for index, heading := range sheet.Header {
		out.WriteString(spreadsheetCell(spreadsheetColumn(index)+"3", heading, 2))
	}
	out.WriteString(`</row>`)
	for index, row := range sheet.Rows {
		fmt.Fprintf(&out, `<row r="%d">`, index+4)
		style := 3
		if sheet.Footer && index == len(sheet.Rows)-1 {
			style = 5
		}
		for column, value := range row {
			out.WriteString(spreadsheetCell(spreadsheetColumn(column)+strconv.Itoa(index+4), value, style))
		}
		out.WriteString(`</row>`)
	}
	out.WriteString(`</sheetData>`)
	filterEnd := len(sheet.Rows) + 3
	if sheet.Footer {
		filterEnd--
	}
	fmt.Fprintf(&out, `<autoFilter ref="A3:%s%d"/><mergeCells count="2"><mergeCell ref="A1:%s1"/><mergeCell ref="A2:%s2"/></mergeCells>`, last, filterEnd, last, last)
	out.WriteString(`<printOptions horizontalCentered="1"/><pageMargins left="0.25" right="0.25" top="0.4" bottom="0.4" header="0.2" footer="0.2"/><pageSetup paperSize="9" orientation="landscape" fitToWidth="1" fitToHeight="0"/></worksheet>`)
	return out.String()
}

// buildScoreWorkbook 按冻结班级生成固定结构的成绩表，不解析外部模板或执行公式。
// 文件写入内存完成后再交给审计和 HTTP 响应，避免失败时交付半个 ZIP。
func buildScoreWorkbook(sheets []scoreSheet, properties map[string]string) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	write := func(name, content string) error {
		file, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = file.Write([]byte(content))
		return err
	}
	var types, book, relations, names strings.Builder
	types.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	book.WriteString(`<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView activeTab="0"/></bookViews><sheets>`)
	relations.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for index, sheet := range sheets {
		id := index + 1
		fmt.Fprintf(&types, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, id)
		fmt.Fprintf(&book, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, spreadsheetText(sheet.Name), id, id)
		fmt.Fprintf(&relations, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, id, id)
		fmt.Fprintf(&names, `<definedName name="_xlnm.Print_Titles" localSheetId="%d">'%s'!$1:$3</definedName>`, index, spreadsheetText(strings.ReplaceAll(sheet.Name, "'", "''")))
		if err := write(fmt.Sprintf("xl/worksheets/sheet%d.xml", id), scoreSheetXML(sheet)); err != nil {
			return nil, err
		}
	}
	types.WriteString(`</Types>`)
	book.WriteString(`</sheets><definedNames>` + names.String() + `</definedNames></workbook>`)
	fmt.Fprintf(&relations, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1)
	files := map[string]string{
		"[Content_Types].xml": types.String(), "xl/workbook.xml": book.String(), "xl/_rels/workbook.xml.rels": relations.String(),
		"_rels/.rels":   `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/styles.xml": `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="0.00"/></numFmts><fonts count="3"><font><sz val="11"/><name val="Microsoft YaHei"/></font><font><b/><sz val="16"/><name val="Microsoft YaHei"/></font><font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Microsoft YaHei"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF245991"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="8"><xf fontId="0" fillId="0" borderId="0"/><xf fontId="1" fillId="0" borderId="0"/><xf fontId="2" fillId="2" borderId="0" applyFill="1" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="1" fontId="0" fillId="0" borderId="0" applyNumberFormat="1" applyAlignment="1"><alignment vertical="center"/></xf><xf fontId="0" fillId="0" borderId="0" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="1" fontId="2" fillId="2" borderId="0" applyFill="1" applyNumberFormat="1"><alignment vertical="center"/></xf><xf numFmtId="164" fontId="0" fillId="0" borderId="0" applyNumberFormat="1"><alignment vertical="center"/></xf><xf numFmtId="164" fontId="2" fillId="2" borderId="0" applyFill="1" applyNumberFormat="1"><alignment vertical="center"/></xf></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`,
	}
	if len(properties) > 0 {
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var custom strings.Builder
		custom.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/custom-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`)
		for index, key := range keys {
			fmt.Fprintf(&custom, `<property fmtid="{D5CDD505-2E9C-101B-9397-08002B2CF9AE}" pid="%d" name="%s"><vt:lpwstr>%s</vt:lpwstr></property>`, index+2, spreadsheetText(key), spreadsheetText(properties[key]))
		}
		custom.WriteString(`</Properties>`)
		files["docProps/custom.xml"] = custom.String()
		files["[Content_Types].xml"] = strings.Replace(files["[Content_Types].xml"], `</Types>`, `<Override PartName="/docProps/custom.xml" ContentType="application/vnd.openxmlformats-officedocument.custom-properties+xml"/></Types>`, 1)
		files["_rels/.rels"] = strings.Replace(files["_rels/.rels"], `</Relationships>`, `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/custom-properties" Target="docProps/custom.xml"/></Relationships>`, 1)
	}
	namesSorted := make([]string, 0, len(files))
	for name := range files {
		namesSorted = append(namesSorted, name)
	}
	sort.Strings(namesSorted)
	for _, name := range namesSorted {
		content := files[name]
		if err := write(name, content); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
