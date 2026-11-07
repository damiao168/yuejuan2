import { describe, expect, it } from "vitest";
import type { Grade } from "../../api/org";
import { classCohortLabel, classLocalName, gradeCohortGroups, gradeCohortKey, gradeCohortLabel, gradeCurrentLevelNumber, gradeEntryYear, gradeLevelOptions } from "./gradeCohortPresentation";

const grade: Grade = { id: "grade-a", tenant_id: "tenant", school_id: "school", name: "自定义旧名称", level_no: 10, academic_year: "2026-2027", education_stage: "senior", status: "active" };

describe("固定届别年级展示", () => {
  it("固定入学年优先，调整当前年级或系统年份不会改届别", () => {
    expect(gradeCohortLabel({ ...grade, entry_year: 2026 })).toBe("2026级 · 高一");
    expect(gradeCohortLabel({ ...grade, entry_year: 2026, level_no: 11, academic_year: "2027-2028" })).toBe("2026级 · 高二");
    expect(gradeCohortKey({ ...grade, entry_year: 2026 })).toBe(gradeCohortKey({ ...grade, entry_year: 2026, level_no: 12 }));
  });
  it("旧数据按存档学年和学段兼容，未知数据不编造届别", () => {
    expect(gradeCohortLabel({ ...grade, level_no: 11, academic_year: "2027-2028" })).toBe("2026级 · 高二");
    expect(gradeCohortLabel({ ...grade, level_no: 8, education_stage: "junior", academic_year: "2027-2028" })).toBe("2026级 · 初二");
    expect(gradeEntryYear({ ...grade, academic_year: "未知" })).toBeUndefined();
    expect(gradeCohortLabel({ ...grade, academic_year: "未知" })).toBe("高一 · 届别未设置");
    expect(gradeCohortKey({ ...grade, academic_year: "未知" })).toBe(grade.id);
  });
  it("优先后端固定届别标识，初高中当前年级选项互不混用", () => {
    expect(gradeCohortKey({ ...grade, grade_cohort_id: "cohort-a" })).toBe("cohort-a");
    expect(gradeLevelOptions("junior").map(option => option.label)).toEqual(["初一", "初二", "初三"]);
    expect(gradeLevelOptions("senior").map(option => option.label)).toEqual(["高一", "高二", "高三"]);
  });
  it("同届跨学年选最新代表并保留全部年级ID，旧绑定班级不会消失", () => {
    const older = { ...grade, entry_year: 2026, grade_cohort_id: "cohort", id: "older" };
    const newer = { ...older, id: "newer", academic_year: "2027-2028", level_no: 11 };
    const groups = gradeCohortGroups([older, newer]);
    expect(groups).toHaveLength(1);
    expect(groups[0].grade.id).toBe("newer");
    expect(groups[0].gradeIds).toEqual(["newer", "older"]);
    expect(gradeCohortLabel(groups[0].grade)).toBe("2026级 · 高二");
    expect(classCohortLabel({ name: "高一（1）班" }, groups[0].grade)).toBe("高二（1）班");
    expect(classCohortLabel({ name: "实验班", class_no: 2 }, groups[0].grade)).toBe("实验班");
  });
  it("旧数据只有标准年级名称时仍显示当前年级，自定义名称不能被猜成级数", () => {
    expect(gradeCohortLabel({ ...grade, level_no: 0, name: "高一年级", entry_year: 2026 })).toBe("2026级 · 高一");
    expect(gradeCurrentLevelNumber({ level_no: 0, name: "初二年级" })).toBe(8);
    expect(gradeCurrentLevelNumber({ level_no: 0, name: "实验高一年级" })).toBeUndefined();
    expect(gradeCohortLabel({ ...grade, level_no: 11, name: "高一年级", entry_year: 2026 })).toBe("2026级 · 高二");
  });
  it("新增和编辑仅填写班级本身，标准旧前缀不再与当前年级重复，自定义班名保留", () => {
    for (const name of ["高一（1）班", "高一年级（1）班", "1", "1班"]) expect(classLocalName({ name })).toBe("1班");
    expect(classLocalName({ name: "高阶实验班" })).toBe("高阶实验班");
    expect(classCohortLabel({ name: "高一年级（1）班" }, { ...grade, level_no: 12 })).toBe("高三（1）班");
  });
});
