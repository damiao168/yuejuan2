"""生成无真实人员数据的最小 XLSX，验证工作表选择、文本学号和数字精度。"""
from pathlib import Path
from xml.sax.saxutils import escape
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

ns = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
relationships = "http://schemas.openxmlformats.org/package/2006/relationships"
def sheet(values):
    rows = []
    for index, cells in enumerate(values, 1):
        content = ""
        for column, value in enumerate(cells):
            address = f"{chr(65 + column)}{index}"
            content += f'<c r="{address}" t="inlineStr"><is><t>{escape(value)}</t></is></c>' if isinstance(value, str) else f'<c r="{address}"><v>{value}</v></c>'
        rows.append(f'<row r="{index}">{content}</row>')
    return f'<worksheet xmlns="{ns}"><sheetData>{"".join(rows)}</sheetData></worksheet>'

files = {
    "[Content_Types].xml": '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>',
    "_rels/.rels": f'<Relationships xmlns="{relationships}"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
    "xl/workbook.xml": f'<workbook xmlns="{ns}" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="高一名单" sheetId="1" r:id="rId1"/><sheet name="高二名单" sheetId="2" r:id="rId2"/></sheets></workbook>',
    "xl/_rels/workbook.xml.rels": f'<Relationships xmlns="{relationships}"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/></Relationships>',
    "xl/worksheets/sheet1.xml": sheet([["学号", "姓名"], ["001", "合成同学甲"], [12345678901234567890, "合成同学乙"]]),
    "xl/worksheets/sheet2.xml": sheet([["学号", "姓名"], ["002", "合成同学丙"]]),
}
with ZipFile(Path(__file__).with_name("roster.xlsx"), "w") as archive:
    for name, content in files.items():
        entry = ZipInfo(name, (2026, 1, 1, 0, 0, 0))
        entry.compress_type = ZIP_DEFLATED
        archive.writestr(entry, content.encode("utf-8"))
