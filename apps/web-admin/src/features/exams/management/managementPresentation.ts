import type { ManagementExam, ManagementExamUpdate } from "../../../api/exams";
import { managementExamVersion } from "../../../api/exams";
import { defaultExamWorkspaceRoute } from "../workspace/defaultExamWorkspaceRoute";

export interface ManagementFilters {
  search: string;
  schoolId: string;
  gradeId: string;
  status: string;
}

export const managementStatusOptions = [
  { label: "全部进度", value: "" }, { label: "正在进行", value: "active" }, { label: "阅卷结束", value: "completed" }
];

export function managementProgressFilter(status: string) {
  // 旧书签继续落到对应的大类，不能在精简后的下拉框里留下无法选择的细分值。
  if (status === "completed" || status === "finalized" || status === "published") return "completed";
  return status && status !== "archived" ? "active" : "";
}

export function managementGradingComplete(exam: ManagementExam) {
  const { total_count: total, grading_complete_count: complete, published_count: published } = exam.progress;
  if (total <= 0) return false;
  if (complete !== undefined) return complete === total;
  // 旧 API 没有核对评分完整性的计数，只有全部正式发布才可确认结束；手动定稿标签不足以证明。
  return published === total;
}

export function managementProgressLabel(exam: ManagementExam) {
  if (isManagementExamArchived(exam)) return "已归档";
  if (managementGradingComplete(exam)) return "阅卷结束";
  return exam.progress.status === "unavailable" ? "进度待核查" : "正在进行";
}

export function isManagementExamArchived(exam: ManagementExam) {
  return Boolean(exam.archived_at) || exam.progress.status === "archived" || Boolean(exam.legacy_archived && exam.archived_subject_ids?.length === exam.exams.length);
}

export function managementExamRoute(exam: Pick<ManagementExam, "kind" | "id">) {
  return `/exams/overview/${exam.kind}/${encodeURIComponent(exam.id)}`;
}

export function managementExamWorkspaceRoute(exam: Pick<ManagementExam, "kind" | "id" | "exams">) {
  // 只对真实独立单科省去总览；多科场次即使只授权一科，也仍保留原来的整场入口。
  return exam.kind === "standalone" && exam.exams.length === 1
    ? defaultExamWorkspaceRoute(exam.exams[0]) : managementExamRoute(exam);
}

export function managementExamKey(exam: Pick<ManagementExam, "kind" | "id">) {
  return `${exam.kind}:${exam.id}`;
}

/** 筛选只控制整场是否显示，返回原对象，不能从学科数组中删掉不匹配的科目。 */
export function filterManagementExams(exams: ManagementExam[], filters: ManagementFilters, classGrade: Map<string, string>) {
  const keyword = filters.search.trim().toLowerCase();
  return exams.filter((exam) => {
    const complete = managementGradingComplete(exam);
    const statusMatched = !filters.status || (filters.status === "completed" ? complete : !complete);
    return !isManagementExamArchived(exam) && statusMatched && (!keyword || exam.name.toLowerCase().includes(keyword))
      && (!filters.gradeId || exam.grade_id === filters.gradeId || exam.class_ids.some((id) => classGrade.get(id) === filters.gradeId));
  });
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
  const current = [...(exam.default_class_ids ?? exam.class_ids)].sort(compareClassIds);
  if (JSON.stringify([...values.class_ids].sort(compareClassIds)) !== JSON.stringify(current)) payload.class_ids = values.class_ids;
  return payload;
}

export function formatExamTime(value?: string) {
  if (!value) return "暂无记录";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "暂无记录" : parsed.toLocaleString("zh-CN", { hour12: false });
}
