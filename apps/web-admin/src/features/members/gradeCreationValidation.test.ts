import { describe, expect, it } from "vitest";
import type { AcademicYear, Grade } from "../../api/org";
import { currentGradeSchoolYear, expectedGradeLevel, gradeCreationIssue, gradeEditIssue, gradeSchoolYearHint } from "./gradeCreationValidation";

const grade = (entry: number, level: number, overrides: Partial<Grade> = {}): Grade => ({ id: `grade-${entry}`, tenant_id: "tenant", school_id: "school", education_stage: "senior", entry_year: entry, grade_cohort_id: `cohort-${entry}`, name: "旧名称", level_no: level, academic_year: "2026-2027", status: "active", ...overrides });
const input = { schoolId: "school", stage: "senior" as const, entryYear: 2026, schoolYear: 2026, grades: [] as Grade[], ready: true };

describe("届别创建和当前年级约束", () => {
  it("按上海九月学年兜底，配置的本校当前学年优先且不受其他学校影响", () => {
    expect(currentGradeSchoolYear([], "school", new Date("2026-08-31T15:59:59Z"))).toBe(2025);
    expect(currentGradeSchoolYear([], "school", new Date("2026-08-31T16:00:00Z"))).toBe(2026);
    const years = [{ school_id: "school", is_current: true, start_year: 2027 }, { school_id: "school", is_current: false, start_year: 2028 }, { school_id: "other", is_current: true, start_year: 2030 }] as AcademicYear[];
    expect(currentGradeSchoolYear(years, "school", new Date("2026-01-01"))).toBe(2027);
  });
  it("固定入学年份对应当前年级，初高中分开", () => {
    expect([2026, 2025, 2024].map(year => expectedGradeLevel("senior", year, 2026))).toEqual([10, 11, 12]);
    expect([2026, 2025, 2024].map(year => expectedGradeLevel("junior", year, 2026))).toEqual([7, 8, 9]);
    expect(gradeSchoolYearHint(2026, "senior")).toContain("2024级高三");
  });
  it.each([2027, 2028])("尚未入学%s不能创建为当前在读", entryYear => expect(gradeCreationIssue({ ...input, entryYear })?.code).toBe("grade_entry_year_future"));
  it.each([2023, 1900])("已超出三年范围%s不能创建为当前在读", entryYear => expect(gradeCreationIssue({ ...input, entryYear })?.code).toBe("grade_cohort_graduated"));
  it("已有同届不论当前年级和归档学年都不能追加第二份", () => {
    const issue = gradeCreationIssue({ ...input, grades: [grade(2026, 11, { academic_year: "2025-2026" })] });
    expect(issue?.code).toBe("grade_cohort_exists");
    expect(issue?.message).toContain("直接编辑");
  });
  it("同年入学跨学校或跨学段互不冲突", () => {
    expect(gradeCreationIssue({ ...input, grades: [grade(2026, 10, { school_id: "other" }), grade(2026, 7, { education_stage: "junior" })] })).toBeUndefined();
  });
  it("当前三年范围内的错误旧占级阻止新建，提示先校正", () => {
    expect(gradeCreationIssue({ ...input, grades: [grade(2025, 10)] })?.code).toBe("grade_level_conflict");
  });
  it("同届只用最新代表，历史高一不能阻止另一届正常入学", () => {
    const old = grade(2025, 10, { id: "old", academic_year: "2025-2026" });
    const current = grade(2025, 11, { id: "current", academic_year: "2026-2027" });
    expect(gradeCreationIssue({ ...input, grades: [old, current] })).toBeUndefined();
  });
  it("已毕业或尚未入学的历史记录不占用当前年级", () => {
    expect(gradeCreationIssue({ ...input, entryYear: 2024, grades: [grade(2023, 12), grade(2028, 12)] })).toBeUndefined();
  });
  it("读取失败不能当成空名单允许创建", () => expect(gradeCreationIssue({ ...input, ready: false })?.code).toBe("grade_context_unavailable"));
  it("错误旧占级之间允许逐个校正，不能互相锁住", () => {
    const current = grade(2025, 10);
    const args = { ...input, entryYear: 2025, grades: [current, grade(2026, 11)], grade: current };
    expect(gradeEditIssue({ ...args, level: 11 })).toBeUndefined();
    expect(gradeEditIssue({ ...args, level: 12 })?.code).toBe("grade_level_year_mismatch");
  });
  it("历史年度不可通过编辑改级数", () => {
    const old = grade(2025, 10, { id: "old", academic_year: "2025-2026" });
    const current = grade(2025, 11, { id: "current" });
    expect(gradeEditIssue({ ...input, entryYear: 2025, grade: old, grades: [old, current], level: 11 })?.code).toBe("grade_historical_record_immutable");
  });
});
