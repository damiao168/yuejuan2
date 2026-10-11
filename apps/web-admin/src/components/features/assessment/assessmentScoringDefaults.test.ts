import { describe, expect, it } from "vitest";
import type { AssessmentScoringMode, QuestionArchetype } from "../../../api/assessment";
import { assessmentScoringGuidance, defaultAssessmentScoringMode, isObjectiveAIModeUnsupported } from "./assessmentScoringDefaults";

const archetypes: QuestionArchetype[] = [
  { code: "selected_response", default_scoring_mode: "RULE_AUTO", evidence_types: [], response_schema: {} },
  { code: "exact_text", default_scoring_mode: "RULE_AUTO", evidence_types: [], response_schema: {} },
  { code: "numeric_expression", default_scoring_mode: "RULE_AUTO", evidence_types: [], response_schema: {} },
  { code: "structured_steps", default_scoring_mode: "AI_ASSIST", evidence_types: [], response_schema: {} },
  { code: "short_constructed", default_scoring_mode: "AI_ASSIST", evidence_types: [], response_schema: {} },
  { code: "extended_response", default_scoring_mode: "HUMAN_PRIMARY", evidence_types: [], response_schema: {} },
  { code: "diagram_graph", default_scoring_mode: "HUMAN_PRIMARY", evidence_types: [], response_schema: {} },
  { code: "table_experiment", default_scoring_mode: "HUMAN_PRIMARY", evidence_types: [], response_schema: {} }
];

describe("未保存学科评分策略的默认模式", () => {
  it.each(archetypes)("普通考试按 $code 的原型默认评分", (archetype) => {
    expect(defaultAssessmentScoringMode(archetype, "single")).toBe(archetype.default_scoring_mode);
    expect(defaultAssessmentScoringMode(archetype)).toBe(archetype.default_scoring_mode);
  });

  it.each(["double_mark", "blind_double_mark"])("%s 对所有内置原型采用双人独立评分", (gradingMode) => {
    for (const archetype of archetypes) expect(defaultAssessmentScoringMode(archetype, gradingMode)).toBe("DUAL_HUMAN");
  });

  it("选择题的默认规则路径不受学科模板的 AI 默认污染", () => {
    const subjectTemplate = { scoring_default: { mode: "AI_ASSIST" } };
    const selectedResponse = { ...archetypes[0], ...subjectTemplate };
    expect(defaultAssessmentScoringMode(selectedResponse, "ai_assist")).toBe("RULE_AUTO");
  });

  it("有效自定义原型模式优先，不把人工配置推测成自动评分", () => {
    const definition = { ...archetypes[0], default_scoring_mode: "MANUAL_ONLY" as const };
    expect(defaultAssessmentScoringMode(definition, "single")).toBe("MANUAL_ONLY");
    expect(defaultAssessmentScoringMode(definition, "future_grading_mode")).toBe("MANUAL_ONLY");
  });

  it("缺失或非法原型模式保守显示人工主评，双评上下文保持双评", () => {
    const invalid = { ...archetypes[0], default_scoring_mode: "FUTURE_MODE" as AssessmentScoringMode };
    expect(defaultAssessmentScoringMode()).toBe("HUMAN_PRIMARY");
    expect(defaultAssessmentScoringMode(invalid, "single")).toBe("HUMAN_PRIMARY");
    expect(defaultAssessmentScoringMode(undefined, "double_mark")).toBe("DUAL_HUMAN");
  });
});

describe("客观题 AI 模式的实际路由提示", () => {
  it.each(["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric", "formula"])("%s 的 AI 策略应说明转人工", (questionType) => {
    expect(isObjectiveAIModeUnsupported(questionType, "AI_ASSIST")).toBe(true);
    expect(isObjectiveAIModeUnsupported(questionType, "AI_FAST_CONFIRM")).toBe(true);
    expect(isObjectiveAIModeUnsupported(questionType, "RULE_AUTO")).toBe(false);
    expect(isObjectiveAIModeUnsupported(questionType, "DUAL_HUMAN")).toBe(false);
  });

  it.each(["short_answer", "calculation", "essay", "discussion"])("%s 的 AI 策略不显示客观题限制", (questionType) => {
    expect(isObjectiveAIModeUnsupported(questionType, "AI_ASSIST")).toBe(false);
  });

  it("缺失题型或策略不能推测成客观 AI 能力限制", () => {
    expect(isObjectiveAIModeUnsupported(undefined, "AI_ASSIST")).toBe(false);
    expect(isObjectiveAIModeUnsupported("single_choice")).toBe(false);
  });
});

describe("评分提示与真实题型能力一致", () => {
  const scoringModes: AssessmentScoringMode[] = ["RULE_AUTO", "AI_ASSIST", "AI_FAST_CONFIRM", "HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY"];

  it.each(["formula", "coding"])("%s 在任何评分模式下都说明人工要求，不推荐改模式取得自动评分", (questionType) => {
    // 后端接受这些题型的多种策略，但评分运行始终转人工；提示不能把可选模式写成已有评分能力。
    for (const scoringMode of [...scoringModes, undefined]) {
      const guidance = assessmentScoringGuidance(questionType, scoringMode);
      expect(guidance).toMatchObject({ type: "info", message: "本题需要教师人工评分" });
      expect(guidance?.description).toContain(questionType === "formula" ? "公式题" : "编程题");
      expect(guidance?.description).toContain("也不能自动判分");
      expect(guidance?.description).toContain("请安排教师阅卷");
      expect(guidance?.description).not.toContain("请选择规则自动评分");
      expect(guidance?.description).not.toContain("如需自动判分");
    }
  });

  it.each(["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"])("仅规则引擎支持的 %s 在 AI 模式下推荐核对规则路径", (questionType) => {
    for (const scoringMode of ["AI_ASSIST", "AI_FAST_CONFIRM"] as const) {
      const guidance = assessmentScoringGuidance(questionType, scoringMode);
      expect(guidance).toMatchObject({ type: "warning", message: "本题不能使用该 AI 模式自动判分" });
      expect(guidance?.description).toContain("会转入人工评分");
      expect(guidance?.description).toContain("请选择规则自动评分");
      expect(guidance?.description).toContain("核对固定规则、作答或选项区域配置");
    }
    for (const scoringMode of ["RULE_AUTO", "HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY", undefined] as const) {
      expect(assessmentScoringGuidance(questionType, scoringMode)).toBeUndefined();
    }
  });

  // 表中同时记录真实路由类别；新增题型或模式时必须同步核对 Go 的冻结策略路由，不能只测试提示函数自己的分支。
  // scoring_capability_matrix_contract_test.go 将同一组 11 × 6 组合保存、冻结后交给实际路由验证。
  type GuidanceOutcome = "supported_rule" | "unsupported_ai" | "unsupported_rule" | "ai_teacher_confirmation" | "human_policy" | "manual_only_type";
  const capabilityMatrix: { questionType: string; outcomes: [GuidanceOutcome, GuidanceOutcome, GuidanceOutcome, GuidanceOutcome, GuidanceOutcome, GuidanceOutcome] }[] = [
    { questionType: "single_choice", outcomes: ["supported_rule", "unsupported_ai", "unsupported_ai", "human_policy", "human_policy", "human_policy"] },
    { questionType: "multiple_choice", outcomes: ["supported_rule", "unsupported_ai", "unsupported_ai", "human_policy", "human_policy", "human_policy"] },
    { questionType: "true_false", outcomes: ["supported_rule", "unsupported_ai", "unsupported_ai", "human_policy", "human_policy", "human_policy"] },
    { questionType: "fill_blank", outcomes: ["supported_rule", "unsupported_ai", "unsupported_ai", "human_policy", "human_policy", "human_policy"] },
    { questionType: "numeric", outcomes: ["supported_rule", "unsupported_ai", "unsupported_ai", "human_policy", "human_policy", "human_policy"] },
    { questionType: "short_answer", outcomes: ["unsupported_rule", "ai_teacher_confirmation", "ai_teacher_confirmation", "human_policy", "human_policy", "human_policy"] },
    { questionType: "calculation", outcomes: ["unsupported_rule", "ai_teacher_confirmation", "ai_teacher_confirmation", "human_policy", "human_policy", "human_policy"] },
    { questionType: "essay", outcomes: ["unsupported_rule", "ai_teacher_confirmation", "ai_teacher_confirmation", "human_policy", "human_policy", "human_policy"] },
    { questionType: "discussion", outcomes: ["unsupported_rule", "ai_teacher_confirmation", "ai_teacher_confirmation", "human_policy", "human_policy", "human_policy"] },
    { questionType: "formula", outcomes: ["manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type"] },
    { questionType: "coding", outcomes: ["manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type", "manual_only_type"] }
  ];
  const combinations = capabilityMatrix.flatMap(({ questionType, outcomes }) => scoringModes.map((scoringMode, index) => ({ questionType, scoringMode, outcome: outcomes[index] })));

  it.each(combinations)("$questionType / $scoringMode 的提示对应 $outcome", ({ questionType, scoringMode, outcome }) => {
    const guidance = assessmentScoringGuidance(questionType, scoringMode);
    switch (outcome) {
      case "unsupported_rule":
        expect(guidance).toMatchObject({ type: "warning", message: "本题不能使用规则自动评分" });
        expect(guidance?.description).toContain("会转入人工评分");
        expect(guidance?.description).toContain("固定判分规则也不会自动执行");
        expect(guidance?.description).toContain("请选择 AI 辅助人工评分");
        expect(guidance?.description).toContain("仍须教师确认");
        expect(guidance?.description).toContain("证据与模型准入要求");
        expect(guidance?.description).toContain("人工主评、双人独立评分或仅人工评分");
        expect(guidance?.description).not.toContain("请选择规则自动评分");
        break;
      case "ai_teacher_confirmation":
        expect(guidance).toMatchObject({ type: "info", message: "AI 建议分数仍须教师确认" });
        expect(guidance?.description).toContain("都须由教师确认最终分数");
        expect(guidance?.description).toContain("证据与模型准入要求");
        expect(guidance?.description).not.toContain("自动判分");
        break;
      case "unsupported_ai":
        expect(guidance).toMatchObject({ type: "warning", message: "本题不能使用该 AI 模式自动判分" });
        expect(guidance?.description).toContain("会转入人工评分");
        expect(guidance?.description).toContain("核对固定规则、作答或选项区域配置");
        break;
      case "manual_only_type":
        expect(guidance).toMatchObject({ type: "info", message: "本题需要教师人工评分" });
        expect(guidance?.description).toContain("也不能自动判分");
        break;
      case "supported_rule":
      case "human_policy":
        // 支持规则或明确要求人工的模式没有能力冲突；界面不应提示切换到 AI 或改写原模式。
        expect(guidance).toBeUndefined();
    }
  });

  it("主界面没有明确评分模式时不推测主观题的规则或 AI 能力", () => {
    for (const questionType of ["short_answer", "calculation", "essay", "discussion"]) {
      expect(assessmentScoringGuidance(questionType)).toBeUndefined();
    }
  });

  it("未知或缺失题型不会被推断为支持规则自动评分", () => {
    for (const questionType of [undefined, "", "future_question_type"]) {
      for (const scoringMode of scoringModes) expect(assessmentScoringGuidance(questionType, scoringMode)).toBeUndefined();
    }
  });
});
