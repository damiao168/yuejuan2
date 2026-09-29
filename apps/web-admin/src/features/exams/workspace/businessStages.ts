import type { ExamWorkspaceProjection } from "../../../api/workspace";

export type ExamBusinessStage = "preparation" | "capture" | "grading" | "results";

const stageOrder: ExamBusinessStage[] = ["preparation", "capture", "grading", "results"];

export const sectionBusinessStage: Record<string, ExamBusinessStage> = {
  overview: "preparation",
  students: "preparation",
  paper: "preparation",
  questions: "preparation",
  template: "preparation",
  settings: "preparation",
  capture: "capture",
  processing: "capture",
  grading: "grading",
  quality: "grading",
  scores: "results",
  appeals: "results",
  reports: "results"
};

function lifecycleBusinessStage(stage: string): ExamBusinessStage {
  if (stage === "capture") return "capture";
  if (stage === "grading" || stage === "quality") return "grading";
  if (stage === "results") return "results";
  return "preparation";
}

export function examBusinessStages(data: ExamWorkspaceProjection) {
  // 导航阶段取服务端生命周期；阶段标为 completed 仅是顺序展示，不替代各环节的业务检查。
  const current = lifecycleBusinessStage(data.stage);
  const currentIndex = stageOrder.indexOf(current);
  return [
    { key: "preparation", label: "考试准备", action_route: `/exams/${encodeURIComponent(data.exam_id)}/students`, summary: `${data.subject_summary.configured_question_count}/${data.subject_summary.question_count} 题已配置` },
    { key: "capture", label: "答卷导入", action_route: `/exams/${encodeURIComponent(data.exam_id)}/capture`, summary: `${data.counts.submission_count} 份答卷` },
    { key: "grading", label: "阅卷", action_route: `/exams/${encodeURIComponent(data.exam_id)}/grading`, summary: `${data.counts.pending_review_count} 项待阅` },
    { key: "results", label: "成绩", action_route: `/exams/${encodeURIComponent(data.exam_id)}/scores`, summary: current === "results" ? "查看成绩" : "尚未开始" }
  ].map((stage, index) => ({ ...stage, state: index < currentIndex ? "completed" : index === currentIndex ? "current" : "pending" }));
}

export function currentExamBusinessStage(data: ExamWorkspaceProjection) {
  return examBusinessStages(data).find((stage) => stage.state === "current");
}
