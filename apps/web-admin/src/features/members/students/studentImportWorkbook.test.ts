import { readFile } from "node:fs/promises";
import { describe, expect, it } from "vitest";
import readExcelFile from "read-excel-file/universal";
import { studentSheetToCSV } from "./studentImportSheet";
import { prepareStudentImport } from "./studentImport";
import type { SchoolClass } from "../../../api/org";

describe("真实xlsx读取与导入边界", () => {
  it("保留多个工作表、前导0文本学号和原始数字字符串，再进入同一班级预检", async () => {
    const bytes = await readFile(new URL("./__fixtures__/roster.xlsx", import.meta.url));
    const sheets = await readExcelFile(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength), { parseNumber: value => value, trim: false });
    expect(sheets.map(sheet => sheet.sheet)).toEqual(["高一名单", "高二名单"]);
    expect(sheets[0].data[1][0]).toBe("001"); expect(sheets[0].data[2][0]).toBe("12345678901234567890");
    const prepared = prepareStudentImport(studentSheetToCSV(sheets[1].data), [{ id: "class", school_id: "school", code: "CLASS-01" } as SchoolClass], undefined, "class");
    expect(prepared.errors).toEqual([]); expect(prepared.csv).toContain("002,合成同学丙,school,class");
    expect(prepared.csv).not.toContain("001");
  });
});
