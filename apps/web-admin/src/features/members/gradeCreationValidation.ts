import type { AcademicYear, Grade } from "../../api/org";
import { gradeCohortGroups, gradeCohortKey, gradeCohortLabel, gradeCurrentLevelLabel, gradeCurrentLevelNumber, gradeEntryYear } from "./gradeCohortPresentation";

export function currentGradeSchoolYear(years: readonly AcademicYear[], schoolId: string, now = new Date()) {
  const configured = years.filter(year => year.school_id === schoolId && year.is_current && Number.isInteger(year.start_year)).sort((a, b) => b.start_year - a.start_year)[0];
  if (configured) return configured.start_year;
  // 尚未配置学年时按上海九月开学兜底；提交仍由服务端按其时钟和最新配置核验。
  const parts = new Intl.DateTimeFormat("en", { timeZone: "Asia/Shanghai", year: "numeric", month: "numeric" }).formatToParts(now);
  const year = Number(parts.find(part => part.type === "year")?.value);
  return year - (Number(parts.find(part => part.type === "month")?.value) < 9 ? 1 : 0);
}

export function expectedGradeLevel(stage: Grade["education_stage"], entryYear: number, schoolYear: number) {
  const offset = schoolYear - entryYear;
  return Number.isInteger(entryYear) && offset >= 0 && offset <= 2 ? (stage === "junior" ? 7 : 10) + offset : undefined;
}

type GradeIssue = { code: string; message: string };
type GradeValidation = { schoolId: string; stage: Grade["education_stage"]; entryYear?: number; schoolYear: number; grades: readonly Grade[]; ready: boolean };

export function gradeCreationIssue(input: GradeValidation): GradeIssue | undefined {
  if (!input.ready) return { code: "grade_context_unavailable", message: "届别和学年资料尚未完整读取，请刷新后再创建。" };
  if (!input.schoolId) return { code: "school_required", message: "请先选择所属学校。" };
  if (!Number.isInteger(input.entryYear) || input.entryYear! < 1900 || input.entryYear! > 2100) return { code: "grade_entry_year_invalid", message: "请填写1900至2100之间的入学年份。" };
  const sameScope = input.grades.filter(grade => grade.school_id === input.schoolId && grade.education_stage === input.stage);
  const existing = sameScope.find(grade => gradeEntryYear(grade) === input.entryYear);
  if (existing) return { code: "grade_cohort_exists", message: `${input.entryYear}级${input.stage === "junior" ? "初中" : "高中"}已存在，请直接编辑已有届别，不要重复创建。` };
  if (input.entryYear! > input.schoolYear) return { code: "grade_entry_year_future", message: `当前是${input.schoolYear}—${input.schoolYear + 1}学年，${input.entryYear}级尚未入学，不能创建为当前在读年级。` };
  const level = expectedGradeLevel(input.stage, input.entryYear!, input.schoolYear);
  if (level === undefined) return { code: "grade_cohort_graduated", message: `${input.entryYear}级已超出当前学段的三年范围，不能新建为当前在读年级。` };
  // 只让每届的当前代表占用级数，历史学年的高一/高二不与其他届新建冲突。
  const occupied = gradeCohortGroups(sameScope).find(group => {
    const entry = gradeEntryYear(group.grade);
    return entry !== undefined && expectedGradeLevel(input.stage, entry, input.schoolYear) !== undefined && gradeCurrentLevelNumber(group.grade) === level;
  });
  if (occupied) return { code: "grade_level_conflict", message: `${gradeCurrentLevelLabel(occupied.grade)}已被${gradeCohortLabel(occupied.grade)}占用，请先核对并校正已有届别的当前年级。` };
  return undefined;
}

export function gradeEditIssue(input: GradeValidation & { grade: Grade; level?: number }): GradeIssue | undefined {
  if (!input.ready) return { code: "grade_context_unavailable", message: "届别和学年资料尚未完整读取，请刷新后再修改。" };
  if (!Number.isInteger(input.entryYear)) return { code: "grade_entry_year_invalid", message: "该记录尚未确定入学届别，请先核对组织资料。" };
  const group = gradeCohortGroups([...input.grades]).find(item => item.key === gradeCohortKey(input.grade));
  if (group && group.grade.id !== input.grade.id) return { code: "grade_historical_record_immutable", message: "这是历史学年记录，请编辑该届别当前的年级。" };
  if (input.entryYear! > input.schoolYear) return { code: "grade_entry_year_future", message: "该届别尚未入学，不能设置为当前在读年级。" };
  const expected = expectedGradeLevel(input.stage, input.entryYear!, input.schoolYear);
  if (expected === undefined) return { code: "grade_cohort_graduated", message: "该届别已超出当前学段的三年范围，不能再调整为当前在读年级。" };
  // 旧届别互相填错级数时，仍允许逐个校正到唯一正确值，避免错误占位造成修复互锁。
  if (input.level !== expected) return { code: "grade_level_year_mismatch", message: `${input.schoolYear}—${input.schoolYear + 1}学年，${input.entryYear}级应为${gradeCurrentLevelLabel({ level_no: expected, name: "" })}，请按入学年份校正当前年级。` };
  return undefined;
}

export function gradeSchoolYearHint(schoolYear: number, stage: Grade["education_stage"]) {
  const base = stage === "junior" ? 7 : 10;
  return `当前${schoolYear}—${schoolYear + 1}学年：${[0, 1, 2].map(offset => `${schoolYear - offset}级${gradeCurrentLevelLabel({ level_no: base + offset, name: "" })}`).join("、")}。同校同学段的届别只能创建一次。`;
}
