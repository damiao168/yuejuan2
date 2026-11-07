import type { Grade, SchoolClass } from "../../api/org";

const levelLabels: Record<number, string> = { 7: "初一", 8: "初二", 9: "初三", 10: "高一", 11: "高二", 12: "高三" };

export function gradeCurrentLevelNumber(grade: Pick<Grade, "level_no" | "name">): number | undefined {
  if (levelLabels[grade.level_no]) return grade.level_no;
  // 早期记录可能只有“高一年级”等名称；只识别完整的标准名称，不从自定义名称猜年级。
  const legacy = /^(初|高)([一二三123])(?:年级)?$/.exec(grade.name.trim());
  if (!legacy) return undefined;
  const offset = ({ 一: 0, 二: 1, 三: 2, "1": 0, "2": 1, "3": 2 } as Record<string, number>)[legacy[2]];
  return (legacy[1] === "初" ? 7 : 10) + offset;
}

export function gradeCurrentLevelLabel(grade: Pick<Grade, "level_no" | "name">) {
  const level = gradeCurrentLevelNumber(grade);
  return (level && levelLabels[level]) || "年级未设置";
}

export function gradeEntryYear(grade: Grade): number | undefined {
  if (Number.isInteger(grade.entry_year) && grade.entry_year! >= 1900 && grade.entry_year! <= 2200) return grade.entry_year;
  // 旧记录仅按存档学年推导，不读取当前日期；后端回填固定届别后，升年级也不会让届别跟着改变。
  const match = /^(\d{4})-\d{4}$/.exec(grade.academic_year);
  const firstLevel = grade.education_stage === "junior" ? 7 : 10;
  const offset = (gradeCurrentLevelNumber(grade) ?? 0) - firstLevel;
  if (!match || !Number.isInteger(offset) || offset < 0 || offset > 2) return undefined;
  const year = Number(match[1]) - offset;
  return year >= 1900 && year <= 2200 ? year : undefined;
}

export function gradeCohortLabel(grade?: Grade) {
  if (!grade) return "未选择年级";
  const year = gradeEntryYear(grade);
  return year ? `${year}级 · ${gradeCurrentLevelLabel(grade)}` : `${gradeCurrentLevelLabel(grade)} · 届别未设置`;
}

export function gradeCohortKey(grade: Grade) {
  if (grade.grade_cohort_id) return grade.grade_cohort_id;
  const year = gradeEntryYear(grade);
  return year ? `${grade.school_id}:${grade.education_stage}:${year}` : grade.id;
}

export function gradeLevelOptions(stage: Grade["education_stage"]) {
  return (stage === "junior" ? [7, 8, 9] : [10, 11, 12]).map(value => ({ value, label: levelLabels[value] }));
}

export function gradeCohortGroups(grades: Grade[]) {
  const groups = new Map<string, Grade[]>();
  for (const grade of grades) {
    const key = gradeCohortKey(grade);
    groups.set(key, [...(groups.get(key) ?? []), grade]);
  }
  // 同届可能留有多个历史学年记录；显示以最新记录为准，筛选时仍包含旧记录的班级和学生。
  return [...groups].map(([key, records]) => {
    const ordered = [...records].sort((left, right) => right.academic_year.localeCompare(left.academic_year) || left.id.localeCompare(right.id));
    return { key, grade: ordered[0], grades: ordered, gradeIds: ordered.map(grade => grade.id) };
  }).sort((left, right) => (gradeEntryYear(right.grade) ?? 0) - (gradeEntryYear(left.grade) ?? 0) || left.key.localeCompare(right.key));
}

export function classLocalName(schoolClass: Pick<SchoolClass, "name" | "class_no">) {
  const name = schoolClass.name.trim();
  const conventional = /^(?:(?:初[一二三123]|高[一二三123])(?:年级)?\s*)?[（(]?(\d+|[一二三四五六七八九十]+)[）)]?\s*班?$/.exec(name);
  // 表单只编辑班级本身；标准旧名称去掉年级前缀，自定义班名原样保留，不改身份和代码。
  if (conventional) return `${conventional[1]}班`;
  if (name) return name;
  return schoolClass.class_no ? `${schoolClass.class_no}班` : "未命名班级";
}

export function classCohortLabel(schoolClass: Pick<SchoolClass, "name" | "class_no">, grade?: Grade) {
  if (!grade) return schoolClass.name.trim() || "未命名班级";
  const localName = classLocalName(schoolClass);
  const conventional = /^(\d+|[一二三四五六七八九十]+)班$/.exec(localName);
  return conventional ? `${gradeCurrentLevelLabel(grade)}（${conventional[1]}）班` : localName;
}
