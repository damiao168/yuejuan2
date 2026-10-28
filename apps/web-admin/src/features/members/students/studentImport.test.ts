import { describe, expect, it } from "vitest";
import type { SchoolClass } from "../../../api/org";
import { prepareStudentImport, remapStudentImportError } from "./studentImport";

const classes = [
  { id: "class-1", school_id: "school-1", code: "G10-01" }
] as SchoolClass[];

describe("student CSV import preparation", () => {
  it("resolves the downloadable class-code template to backend IDs", () => {
    const prepared = prepareStudentImport("student_no,name,class_code\n20260001,张同学,G10-01\n", classes);

    expect(prepared.errors).toEqual([]);
    expect(prepared.sourceRows).toEqual([2]);
    expect(prepared.csv).toContain("student_no,name,school_id,class_id");
    expect(prepared.csv).toContain("20260001,张同学,school-1,class-1");
  });

  it("keeps the existing ID-based import format compatible", () => {
    const prepared = prepareStudentImport("student_no,name,school_id,class_id\nS001,张三,school-1,class-1\n", classes);

    expect(prepared.errors).toEqual([]);
    expect(prepared.csv).toContain("S001,张三,school-1,class-1");
  });

  it("reports unknown and ambiguous class codes without importing those rows", () => {
    const ambiguousClasses = [...classes, { ...classes[0], id: "class-2", school_id: "school-2" }];
    const prepared = prepareStudentImport(
      "student_no,name,class_code\nS001,张三,missing\nS002,李四,G10-01\n",
      ambiguousClasses
    );

    expect(prepared.csv).toBe("");
    expect(prepared.errors.map((error) => error.row)).toEqual([2, 3]);
    expect(prepared.errors[0].message).toContain("不存在");
    expect(prepared.errors[1].message).toContain("多个班级");
  });

  it("maps backend row numbers back to the original uploaded row", () => {
    expect(remapStudentImportError({ row: 2, message: "该学号已存在" }, [4])).toEqual({
      row: 4,
      message: "该学号已存在"
    });
  });

  it("识别学校中文表头，并允许显式映射其他列名", () => {
    expect(prepareStudentImport("学号,姓名,班级\n001,张三,G10-01", classes).csv).toContain("001,张三,school-1,class-1");
    const mapped = prepareStudentImport("编号,学生,教学班\n002,李四,G10-01", classes, { studentNo: "编号", name: "学生", classCode: "教学班", schoolID: "", classID: "" });
    expect(mapped.errors).toEqual([]);
    expect(mapped.csv).toContain("002,李四,school-1,class-1");
  });

  it("整班导入可省略班级列，显式班级范围不接受其他班或文件中的冲突编号", () => {
    const mapped = { studentNo: "学号", name: "姓名", classCode: "", schoolID: "", classID: "" };
    expect(prepareStudentImport("学号,姓名\n001,张三", classes, mapped, "class-1").csv).toContain("001,张三,school-1,class-1");
    const conflict = prepareStudentImport("student_no,name,school_id,class_id\n001,张三,school-2,class-2", classes, undefined, "class-1");
    expect(conflict.csv).toBe("");
    expect(conflict.errors[0].message).toContain("目标班级不一致");
  });

  it("does not submit malformed CSV rows", () => {
    const prepared = prepareStudentImport(
      "student_no,name,class_code\nS001,张三,G10-01,unexpected\nS002,李四,G10-01\n",
      classes
    );

    expect(prepared.errors).toEqual([{ row: 2, message: "该行 CSV 格式有误，请检查列数和引号" }]);
    expect(prepared.sourceRows).toEqual([3]);
    expect(prepared.csv).not.toContain("S001");
    expect(prepared.csv).toContain("S002,李四,school-1,class-1");
  });
  it("跳过空白行但保留原表行号，服务端错误仍能定位原行", () => {
    const prepared = prepareStudentImport("学号,姓名,班级\n001,合成甲,G10-01\n,,\n002,合成乙,G10-01\n", classes);
    expect(prepared.errors).toEqual([]); expect(prepared.sourceRows).toEqual([2, 4]);
    expect(remapStudentImportError({ row: 3, message: "重复学号" }, prepared.sourceRows).row).toBe(4);
  });
});
