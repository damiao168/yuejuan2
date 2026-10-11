import type { ManagementExam, ManagementExamUpdate } from "../../../api/exams";
import { managementExamVersion } from "../../../api/exams";

export interface ManagementFilters {
  search: string;
  schoolId: string;
  gradeId: string;
  status: string;
  examType: string;
  archive: "active" | "archived" | "all";
}

export const managementStatusOptions = [
  { label: "草稿", value: "draft" }, { label: "准备中", value: "configured" },
  { label: "准备完成", value: "ready" }, { label: "答卷导入中", value: "collecting" },
  { label: "阅卷中", value: "grading" }, { label: "复核中", value: "reviewing" },
  { label: "已定稿", value: "finalized" }, { label: "部分已发布", value: "partially_published" },
  { label: "已发布", value: "published" }, { label: "多个阶段进行中", value: "ongoing" }
];

export function isManagementExamArchived(exam: ManagementExam) {
  return Boolean(exam.archived_at) || exam.progress.status === "archived" || Boolean(exam.legacy_archived && exam.archived_subject_ids?.length === exam.exams.length);
}

export function managementExamRoute(exam: Pick<ManagementExam, "kind" | "id">) {
  return `/exams/overview/${exam.kind}/${encodeURIComponent(exam.id)}`;
}

export function managementExamKey(exam: Pick<ManagementExam, "kind" | "id">) {
  return `${exam.kind}:${exam.id}`;
}

/** 筛选只控制整场是否显示，返回原对象，不能从学科数组中删掉不匹配的科目。 */
export function filterManagementExams(exams: ManagementExam[], filters: ManagementFilters, classGrade: Map<string, string>) {
  const keyword = filters.search.trim().toLowerCase();
  return exams.filter((exam) => {
    const archived = isManagementExamArchived(exam);
    const archiveMatched = filters.archive === "all" || (filters.archive === "archived" ? archived : !archived);
    const statusMatched = !filters.status || (filters.status === "active"
      ? !archived && exam.progress.published_count < exam.progress.total_count
      : exam.progress.status === filters.status);
    return archiveMatched && statusMatched && (!keyword || exam.name.toLowerCase().includes(keyword))
      && (!filters.examType || exam.exam_type === filters.examType)
      && (!filters.gradeId || exam.grade_id === filters.gradeId || exam.class_ids.some((id) => classGrade.get(id) === filters.gradeId));
  });
}

export function canEditManagementScope(exam: ManagementExam) {
  return !exam.archived_at && exam.exams.every((subject) => ["draft", "configured"].includes(subject.status));
}

export function canEditManagementDefaultClasses(exam: ManagementExam) {
  const affected = exam.exams.filter((subject) => exam.kind === "standalone" || exam.inherited_subject_ids?.includes(subject.id));
  return !isManagementExamArchived(exam) && affected.length > 0 && affected.every((subject) => ["draft", "configured"].includes(subject.status));
}

export function managementDefaultClassesHelp(hasAffectedSubjects: boolean, classesEditable: boolean) {
  // 没有继承默认范围的学科时，先引导到学科设置，不用编辑能力解释无效的整场操作。
  if (!hasAffectedSubjects) return "全部学科使用独立参考范围，请进入对应学科修改。";
  if (!classesEditable) return "受影响学科进入后续阶段后，参考范围不能修改；冻结名册也会阻止保存。";
  return "仅更新继承默认范围的学科，冻结名册会阻止保存。";
}

export interface ManagementEditValues {
  name: string;
  exam_type: string;
  class_ids: string[];
}

// 班级 ID 只用于范围相等判断，按 UTF-16 规范排序；语言排序可能把不同 ID 视作同序。
function compareClassIds(left: string, right: string) {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

export function managementEditPayload(exam: ManagementExam, values: ManagementEditValues): ManagementExamUpdate {
  const payload: ManagementExamUpdate = { ...managementExamVersion(exam) };
  if (values.name.trim() !== exam.name) payload.name = values.name.trim();
  if (values.exam_type !== exam.exam_type) payload.exam_type = values.exam_type;
  const current = [...(exam.default_class_ids ?? exam.class_ids)].sort(compareClassIds);
  if (JSON.stringify([...values.class_ids].sort(compareClassIds)) !== JSON.stringify(current)) payload.class_ids = values.class_ids;
  return payload;
}

export function formatExamTime(value?: string) {
  if (!value) return "暂无记录";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "暂无记录" : parsed.toLocaleString("zh-CN", { hour12: false });
}
