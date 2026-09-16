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
});
