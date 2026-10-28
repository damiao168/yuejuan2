import { describe, expect, it } from "vitest";
import { studentSheetToCSV } from "./studentImportSheet";
import { prepareStudentImport } from "./studentImport";
import type { SchoolClass } from "../../../api/org";

describe("Excel名单转入同一导入预检", () => {
  it("保留文本学号、逗号和换行姓名，中文表头与整班范围复用原校验", () => {
    const csv = studentSheetToCSV([["学号", "姓名"], ["001", "张,同学"], ["0002", "李\n同学"]]);
    const prepared = prepareStudentImport(csv, [{ id: "class", school_id: "school", code: "CLASS-01" } as SchoolClass], undefined, "class");
    expect(prepared.errors).toEqual([]); expect(prepared.sourceRows).toEqual([2, 3]);
    expect(prepared.csv).toContain("001"); expect(prepared.csv).toContain('"张,同学"'); expect(prepared.csv).toContain('"李\n同学"');
  });
  it("拒绝重名表头与过大的名单，空列仍有独立映射名称", () => {
    expect(() => studentSheetToCSV([["学号", "学号"], ["1", "2"]])).toThrow("重名列");
    expect(() => studentSheetToCSV(Array.from({ length: 10002 }, () => ["合成"]))).toThrow("10000");
    expect(studentSheetToCSV([["学号", "", "姓名"], ["001", null, "王同学"]])).toContain("学号,未命名列2,姓名");
  });
});
