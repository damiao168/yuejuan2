import type { ScoringRunItem } from "../../../api/review";

export const scoringStageLabels: Record<string, string> = {
  prepare: "准备评分输入", observe: "识别学生作答", verify: "核验作答证据", score: "生成评分结果", done: "处理结束"
};

export const scoringReasonLabels: Record<string, string> = {
  scoring_rule_missing: "本题尚无生效评分规则",
  rule_review_required: "本题需要核对评分依据",
  ai_assist_teacher_confirmation: "AI 建议生成后由教师确认分数",
  answer_segment_answer_missing: "尚未形成可评分的学生作答",
  waiting_observation: "正在识别学生作答",
  waiting_recognition: "正在识别学生作答",
  waiting_dependency: "等待前序处理完成",
  observation_unreadable: "学生作答看不清，请查看答题图",
  observation_uncertain: "学生作答存在歧义，请查看答题图",
  answer_observation_uncertain: "作答识别不确定，请核对原图",
  answer_association_uncertain: "本题答案归属不明确，请查看整区答题图",
  visual_model_unavailable: "尚无可用的图像阅卷模型",
  visual_model_not_configured: "请平台管理员配置本校自动阅卷 API 模型",
  visual_model_probe_required: "所选模型尚未完成图片能力检测",
  visual_model_changed: "阅卷模型配置已变化，请使用当前配置重新处理",
  observation_pending: "正在识别学生作答",
  ai_suggestion_pending: "正在生成 AI 建议分",
  ai_suggestion_ready: "建议分已生成，等待教师确认",
  ai_suggestion_failed: "AI 建议未生成，请查看本题详情",
  input_version_changed: "答题图已变化，请按新图重新处理",
  multi_blank_rule_requires_review: "本题含多个填空，请按各空答案核对得分",
  omr_possible_erasure: "检测到疑似擦改，请核对最终填涂",
  omr_unreadable: "填涂区域不清晰，请查看答题图",
  omr_uncertain_mark: "填涂较浅，暂不能确定所选项",
  omr_multiple_marks: "单选题检测到多个填涂项",
  omr_set_quality_insufficient: "选项集合的识别依据不足，请核对",
  visual_capability_required: "当前阅卷模型不支持图像",
  omr_calibration_required: "填涂结果已提取，尚不具备自动确认条件",
  omr_ambiguous: "填涂不清晰，需要核对",
  omr_multiple: "填涂数量与本题要求不符",
  omr_blank: "未检测到填涂",
  answer_low_confidence: "学生作答识别把握不足",
  low_confidence: "学生作答识别把握不足",
  auto_grade_confirmation_failed: "评分结果未通过自动确认",
  auto_grade_engine_unavailable: "规则评分暂不可用",
  human_primary: "本题采用人工评分",
  manual_only: "本题采用人工评分",
  grading_group_requires_observation: "正在识别整区作答并对应题号"
};

// 转入人工、执行失败或已有裁图都不是识别完成；新服务端的明确事实优先于旧字段推断。
export function hasObservedAnswer(item: ScoringRunItem): boolean {
  if (typeof item.recognition_completed === "boolean") return item.recognition_completed;
  return Boolean(item.recognition_source && (
    item.recognized_answer?.trim() || ["blank", "selected", "confirmed"].includes(item.recognition_decision ?? "")
  ));
}

export function hasScoringResult(item: ScoringRunItem): boolean {
  return typeof item.score === "number" && Boolean(item.grade_source);
}

export function imageRecognitionSummary(item: ScoringRunItem): { label: string; detail: string } | undefined {
  if (item.route !== "image_ai") return undefined;
  const suggestionReady = item.decision_status === "suggestion_ready";
  // 图像识别与逐字文本是两种事实；建议成功也不能替代服务端明确的识别完成标记。
  if (item.recognition_completed === true) {
    const label = item.recognition_decision === "blank" ? "未作答" : "作答图已识别";
    if (suggestionReady) return { label, detail: "建议已生成" };
    const failed = item.processing_status === "failed" || item.state === "failed";
    return { label, detail: failed ? "后续处理失败，请核对" : "图像识别已完成" };
  }
  if (suggestionReady) return { label: "建议已生成", detail: "请查看作答图" };
  if (item.processing_status === "failed" || item.state === "failed") return { label: "暂无识别结果", detail: "处理失败，请查看作答图" };
  if (item.processing_status === "cancelled" || item.state === "cancelled") return { label: "暂无识别结果", detail: "处理已取消" };
  if (item.processing_status === "blocked") return { label: "作答图待核对", detail: "需要人工处理" };
  if (["waiting_dependency", "queued", "retry_wait"].includes(item.processing_status ?? "")) {
    return { label: "等待处理", detail: item.processing_status === "retry_wait" ? "等待自动重试" : "尚无已完成的识别结果" };
  }
  if (item.processing_status === "running") return { label: "图像阅卷处理中", detail: "尚无已完成的识别结果" };
  return { label: "暂无识别结果", detail: "请查看作答图" };
}

export function scoringProgressLabel(item: ScoringRunItem): string | undefined {
  if (item.decision_status === "suggestion_ready") return "建议分待确认";
  if (item.processing_status === "blocked") return "需要处理";
  if (["waiting_dependency", "queued", "running", "retry_wait"].includes(item.processing_status ?? "")) {
    if (item.processing_status === "retry_wait") return "等待自动重试";
    return scoringStageLabels[item.stage ?? ""] ?? "处理中";
  }
  if (item.decision_status === "manual_only") return "人工评分";
  if (item.decision_status === "review_needed") return "需要核对";
  return undefined;
}
