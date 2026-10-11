import type { Exam } from "../../../api/exams";

export function defaultExamWorkspaceSection(status: Exam["status"]) {
  // 默认入口跟随实际阶段，进入后续工作时不能把用户送回准备第一步。
  if (["ready", "preparation_confirmed", "collecting", "processing"].includes(status)) return "capture";
  if (["grading", "reviewing"].includes(status)) return "grading";
  if (["finalized", "published", "archived"].includes(status)) return "scores";
  return "students";
}

export function defaultExamWorkspaceRoute(exam: Exam) {
  return `/exams/${encodeURIComponent(exam.id)}/${defaultExamWorkspaceSection(exam.status)}`;
}
