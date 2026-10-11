import type { AssessmentScoringMode, QuestionArchetype } from "../../../api/assessment";

/** 未保存的策略与后端默认物化一致：双评使用人工双评，其他考试读取题型默认，学科默认不覆盖题型。
 * 缺失或非法原型模式只保守展示人工主评；调用方仍须阻止缺模板的配置保存，不能视为已核实策略。
 */
export function defaultAssessmentScoringMode(archetype?: QuestionArchetype, gradingMode?: string): AssessmentScoringMode {
  if (gradingMode === "double_mark" || gradingMode === "blind_double_mark") return "DUAL_HUMAN";
  switch (archetype?.default_scoring_mode) {
    case "RULE_AUTO":
    case "AI_ASSIST":
    case "AI_FAST_CONFIRM":
    case "HUMAN_PRIMARY":
    case "DUAL_HUMAN":
    case "MANUAL_ONLY":
      return archetype.default_scoring_mode;
    default:
      return "HUMAN_PRIMARY";
  }
}

// 此判断只说明 AI 不支持，不能据此推断规则引擎支持；用户操作建议应使用 assessmentScoringGuidance。
export function isObjectiveAIModeUnsupported(questionType?: string, scoringMode?: AssessmentScoringMode): boolean {
  return (scoringMode === "AI_ASSIST" || scoringMode === "AI_FAST_CONFIRM")
    && ["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric", "formula"].includes(questionType ?? "");
}

export interface AssessmentScoringGuidance {
  type: "warning" | "info";
  message: string;
  description: string;
}

/** 按试卷真实题型说明人工要求；更换原型或评分模式不会增加题型本身的自动评分能力。
 * 支持范围对应 paper/scoring_coverage.go 和 grading/scoring_run_routing.go；未知题型不推荐自动评分。
 */
export function assessmentScoringGuidance(questionType?: string, scoringMode?: AssessmentScoringMode): AssessmentScoringGuidance | undefined {
  // 公式和编程题在所有评分模式下都由人工处理，不能向它们推荐当前尚无能力的规则自动评分。
  let manualQuestionLabel: string | undefined;
  if (questionType === "formula") manualQuestionLabel = "公式题";
  else if (questionType === "coding") manualQuestionLabel = "编程题";
  if (manualQuestionLabel) {
    return {
      type: "info",
      message: "本题需要教师人工评分",
      description: `${manualQuestionLabel}目前需要教师人工评分。切换为规则自动评分、AI 辅助或快速确认，也不能自动判分。请安排教师阅卷。`
    };
  }
  if (["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"].includes(questionType ?? "")
    && isObjectiveAIModeUnsupported(questionType, scoringMode)) {
    return {
      type: "warning",
      message: "本题不能使用该 AI 模式自动判分",
      description: "该题型使用 AI 辅助或快速确认时会转入人工评分，已发布的固定判分规则也不会自动执行。如需自动判分，请选择规则自动评分并核对固定规则、作答或选项区域配置。"
    };
  }
  if (["short_answer", "calculation", "essay", "discussion"].includes(questionType ?? "")) {
    // 规则模式不会调度主观题 AI 批次；即使配置合法且规则已发布，冻结策略仍只产生人工任务。
    if (scoringMode === "RULE_AUTO") {
      return {
        type: "warning",
        message: "本题不能使用规则自动评分",
        description: "该主观题使用规则自动评分时会转入人工评分，已发布的固定判分规则也不会自动执行。如需 AI 建议分数，请选择 AI 辅助人工评分；仍须教师确认，并满足证据与模型准入要求。也可选择人工主评、双人独立评分或仅人工评分。"
      };
    }
    // 两种 AI 模式仅提供建议分数；快速确认也必须保留教师确认，不承诺已满足模型准入。
    if (scoringMode === "AI_ASSIST" || scoringMode === "AI_FAST_CONFIRM") {
      return {
        type: "info",
        message: "AI 建议分数仍须教师确认",
        description: "该题型可使用 AI 辅助生成建议分数，但须满足证据与模型准入要求。AI 辅助人工评分和 AI 快速确认都须由教师确认最终分数；未满足准入要求时请安排人工评分。"
      };
    }
  }
  return undefined;
}
