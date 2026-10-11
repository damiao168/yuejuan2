export const assuranceErrorSourceOptions = [
  ["none", "无错误"], ["image_quality", "图像质量"], ["page_matching", "页面匹配"], ["answer_crop", "答题区域裁切"],
  ["handwriting_ocr", "手写识别"], ["formula_recognition", "公式识别"], ["answer_structuring", "答案结构化"],
  ["rubric", "Rubric"], ["model_scoring", "模型评分"], ["score_calculation", "分值计算"], ["system", "系统错误"],
  ["unattributed", "待归因"]
].map(([value, label]) => ({ value, label }));

export function assuranceEvaluationStatusColor(status: string): string {
  if (status === "completed") return "success";
  if (status === "invalidated") return "default";
  return "processing";
}

export function assuranceEvaluationStatusLabel(status: string): string {
  if (status === "completed") return "已冻结";
  if (status === "invalidated") return "已失效";
  return "采集中";
}

export function assuranceCalibrationStatusColor(status: string): string {
  if (status === "approved") return "success";
  if (status === "invalidated") return "default";
  if (status === "completed") return "processing";
  return "warning";
}

export function assuranceCalibrationStatusLabel(status: string): string {
  if (status === "approved") return "已批准";
  if (status === "completed") return "待批准";
  if (status === "invalidated") return "已失效";
  return "草稿";
}

export function assuranceCalibrationTransitionMessage(kind: "complete" | "approve" | "invalidate"): string {
  if (kind === "complete") return "校准工件已完成。";
  if (kind === "approve") return "校准工件已批准；仅精确版本轴可引用。";
  return "校准工件已失效。";
}

export function assuranceDisagreementStatusLabel(status: string): string {
  if (status === "needs_review") return "待审阅";
  if (status === "classified") return "已分类";
  return "已关联任务";
}
