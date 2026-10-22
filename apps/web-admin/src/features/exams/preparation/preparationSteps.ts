import type { ExamReadiness } from "../../../api/configuration";

export const preparationSteps = [
  { key: "students", label: "学生范围", route: "students", action: "设置范围" },
  { key: "paper", label: "考试资料", route: "paper", action: "上传资料" },
  { key: "questions", label: "小题与分值", route: "questions", action: "核对题目" },
  { key: "template", label: "答题卡设置", route: "template", action: "设置答题卡" }
] as const;

const requiredChecks = {
  students: ["student_scope"],
  paper: ["paper_file"],
  questions: ["questions", "total_score", "question_scores", "answer_keys", "rubrics"],
  template: ["locked_template", "template_coverage"]
} as const;

export type PreparationStepState = "complete" | "incomplete" | "unknown";

function checkState(hasReadiness: boolean, missing: number, failed: number): PreparationStepState {
  if (!hasReadiness || missing > 0) return "unknown";
  return failed > 0 ? "incomplete" : "complete";
}

function stepDetail(state: PreparationStepState, passedCount: number, failedLabels: string[]) {
  if (state === "unknown") return "检查状态待核对，请重新检查";
  if (state === "complete") return `${passedCount} 项检查已通过`;
  return `尚需处理：${failedLabels.join("、")}`;
}

function confirmationDetail(readiness: ExamReadiness | undefined, preparationConfirmedAt?: string) {
  if (!readiness) return "检查状态待核对，请重新检查";
  if (readiness.confirmed) return readiness.confirmed_at
    ? `准备已于 ${new Date(readiness.confirmed_at).toLocaleString("zh-CN", { hour12: false })} 确认`
    : "准备已核对并确认";
  if (preparationConfirmedAt) return `准备已于 ${new Date(preparationConfirmedAt).toLocaleString("zh-CN", { hour12: false })} 确认（此前确认记录）；当前配置请重新核对`;
  return readiness.ready ? "检查已通过，仍需核对并确认准备" : "完成准备检查后，再核对并确认";
}

export function preparationNavigationSteps(readiness?: ExamReadiness) {
  const checks = readiness?.checks ?? [];
  const steps = preparationSteps.map((step) => {
    // 上传文件后仍须人工采用资料结构；这条检查同时约束资料和题目两步。
    const stepChecks = checks.filter((check) => check.section === step.key || (step.key === "paper" && check.code === "material_structure"));
    const missingChecks = requiredChecks[step.key].filter((code) => !stepChecks.some((check) => check.code === code));
    const failedChecks = stepChecks.filter((check) => !check.passed);
    // 检查列表缺项不能证明完成，访问过页面或其他步骤通过也不能替代本步检查。
    const state = checkState(Boolean(readiness), missingChecks.length, failedChecks.length);
    const detail = stepDetail(state, stepChecks.length, failedChecks.map(check => check.label));
    return { ...step, state, detail };
  });
  // 后端只在后续考试阶段返回独立历史字段；历史确认不代表当前配置仍被确认。
  const preparationConfirmedAt = readiness?.preparation_snapshot_id ? readiness.preparation_confirmed_at : undefined;
  const currentSnapshotConfirmedAt = readiness?.snapshot_id ? readiness.confirmed_at : undefined;
  const confirmed = Boolean(readiness?.confirmed || currentSnapshotConfirmedAt || preparationConfirmedAt);
  const confirmationState = checkState(Boolean(readiness), 0, confirmed ? 0 : 1);
  return [...steps, {
    key: "confirmation", route: "settings", label: "确认准备完成", state: confirmationState,
    detail: confirmationDetail(readiness, preparationConfirmedAt ?? currentSnapshotConfirmedAt)
  }];
}
