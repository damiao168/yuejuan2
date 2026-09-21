import type { Exam } from "../../../api/exams";

export function defaultExamWorkspaceSection(status: Exam["status"]) {
  if (["draft", "configured", "ready"].includes(status)) return "settings";
  if (status === "collecting") return "capture";
  if (["grading", "reviewing"].includes(status)) return "grading";
  return "overview";
}

export function defaultExamWorkspaceRoute(exam: Exam) {
  return `/exams/${encodeURIComponent(exam.id)}/${defaultExamWorkspaceSection(exam.status)}`;
}
