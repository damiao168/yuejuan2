import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import type { MathRubricScoreResponse, MathUnderstandingResponse } from "../../../api/mathUnderstanding";
import type { AiGrade } from "../../../api/review";
import { MathRubricMatrix, type MathRubricMatrixProps } from "./components/MathRubricMatrix";
import { mathDecisionSteps, mathScoreMatchesUnderstanding, mathSuggestionState, selectMathStep, type MathWorkbenchEvidence } from "./mathWorkbenchEvidence";

function fixture() {
  const understanding = { artifact: { id: "a1", answer_segment_id: "s1", exam_question_snapshot_id: "snapshot1", version: 3, stage: "verified", correction_revision: 2,
    blocks: [{ id: "b1", status: "active", bbox: { x: .1, y: .2, width: .4, height: .1 } }],
    formulas: [], solution_graph: { steps: [{ id: "step1", block_ids: ["b1"], formula_ids: ["f1"], normalized_text: "x = 2" }], edges: [] },
    verifications: [{ id: "v1", step_id: "step1", formula_id: "f1", status: "verified", engine: "sympy" }] }, correction_revision: 0 } as unknown as MathUnderstandingResponse;
  const score = { schema_version: "math-rubric-score-v1", scope: "teacher_suggestion_only", artifact_id: "a1", artifact_version: 3, correction_revision: 0, verified_correction_revision: 2,
    exam_question_snapshot_id: "snapshot1", rubric_version: "r1", max_score: 2, verified_score: 2, unresolved_score: 0, suggested_score: 2, score_range: { min: 2, max: 2 },
    criterion_decisions: [{ rubric_point_id: "p1", status: "supported", max_score: 2, awarded_score: 2, evidence_ids: ["e1"], verification_ids: ["v1"], decision_source: "symbolic", reason_code: "equivalent" }],
    rubric_evidence: [{ id: "e1", source_artifact_ids: ["f1", "b1"] }] } as unknown as MathRubricScoreResponse;
  const grade = { id: "g1", answer_segment_id: "s1", math_artifact_id: "a1", math_artifact_version: 3, math_correction_revision: 2, math_scoring_version: "math-rubric-score-v1", rubric_version: "r1",
    delivery_mode: "teacher_suggestion", needs_human_review: true, status: "succeeded", mock: false, suggested_score: 2, max_score: 2, created_at: "2026-09-14T00:00:00Z" } as AiGrade;
  const evidence: MathWorkbenchEvidence = { segmentId: "s1", phase: "ready", understanding, score, message: "" };
  return { understanding, score, grade, evidence };
}

function matrixHTML(evidence: MathWorkbenchEvidence, overrides: Partial<MathRubricMatrixProps> = {}) {
  return renderToStaticMarkup(createElement(MathRubricMatrix, {
    evidence, grades: [], points: [], dirty: false, requesting: false, canRequest: true,
    onRequest: () => {}, onSelectStep: () => {}, ...overrides,
  }));
}

describe("mathematical suggestion freshness", () => {
  it("accepts only the exact verified artifact, absorbed-plus-local correction and server scorer", () => {
    const { grade, evidence } = fixture();
    expect(mathSuggestionState(grade, evidence).current).toBe(true);
    expect(mathSuggestionState(grade, evidence, true).current).toBe(false);
    evidence.understanding!.correction_revision = 1;
    expect(mathSuggestionState(grade, evidence).label).toContain("已过期");
  });
  it.each(["loading", "pending", "unavailable", "conflict"] as const)("fails closed while %s", (phase) => {
    const { grade, evidence } = fixture();
    evidence.phase = phase;
    expect(mathSuggestionState(grade, evidence).current).toBe(false);
  });
  it.each([
    { math_artifact_id: "a2" }, { math_artifact_version: 4 }, { math_correction_revision: 0 }, { math_scoring_version: "future" },
    { rubric_version: "r2" }, { answer_segment_id: "other" }, { mock: true }, { status: "failed" }, { delivery_mode: "shadow_only" },
    { suggested_score: 999 }, { suggested_score: Number.NaN }, { needs_human_review: false }, { math_artifact_id: undefined }
  ])("rejects old, unbound or unsafe material: %j", (change) => {
    const { grade, evidence } = fixture();
    expect(mathSuggestionState({ ...grade, ...change } as AiGrade, evidence).current).toBe(false);
  });
  it("does not turn a nullable total or unresolved point into a current zero", () => {
    const { grade, evidence } = fixture();
    evidence.score!.suggested_score = null;
    evidence.score!.unresolved_score = 2;
    expect(mathSuggestionState({ ...grade, suggested_score: 0 }, evidence).current).toBe(false);
  });
  it("rejects raced score/projection and verified lineage reads", () => {
    const { understanding, score } = fixture();
    expect(mathScoreMatchesUnderstanding(score, understanding)).toBe(true);
    expect(mathScoreMatchesUnderstanding({ ...score, artifact_version: 4 }, understanding)).toBe(false);
    expect(mathScoreMatchesUnderstanding({ ...score, verified_correction_revision: 0 }, understanding)).toBe(false);
    expect(mathScoreMatchesUnderstanding({ ...score, exam_question_snapshot_id: "other" }, understanding)).toBe(false);
  });
});

describe("rubric × step provenance", () => {
  it.each([
    { dirty: true, phase: "pending", disabledReason: "当前身份无重新生成数学建议的权限；请联系管理员。" },
    { dirty: true, phase: "pending", disabledReason: "" },
    { dirty: false, phase: "ready", disabledReason: undefined },
  ] as const)("无生成权限时隐藏入口，保留数学证据：$phase/$dirty", ({ dirty, phase, disabledReason }) => {
    const { evidence } = fixture();
    evidence.phase = phase;
    const html = matrixHTML(evidence, { canRequest: false, dirty, disabledReason });
    expect(html).not.toContain("重新生成数学建议");
    expect(html).not.toContain('class="math-suggestion-note"');
    expect(html).toContain("定位数学步骤 step1");
    expect(html).toContain("服务端建议");
  });
  it.each([
    { name: "dirty before pending", canRequest: true, dirty: true, phase: "pending", disabledReason: undefined, expected: "请先保存证据校正并完成验证，再重新生成建议。", disabled: true },
    { name: "pending clean evidence", canRequest: true, dirty: false, phase: "pending", disabledReason: undefined, expected: "数学证据尚未验证就绪；请刷新证据状态，或继续人工评分。", disabled: true },
    { name: "ready clean evidence", canRequest: true, dirty: false, phase: "ready", disabledReason: undefined, expected: "点击后请求受治理模型；建议仍需人工核对。", disabled: false },
  ] as const)("preserves regeneration status and disabled state for $name", ({ canRequest, dirty, phase, disabledReason, expected, disabled }) => {
    const { evidence } = fixture();
    evidence.phase = phase;
    const html = matrixHTML(evidence, { canRequest, dirty, disabledReason });
    expect(html).toContain(`<output class="math-suggestion-note" aria-live="polite">${expected}</output>`);
    const requestButton = [...html.matchAll(/<button\b[^>]*>[\s\S]*?<\/button>/g)].find(([button]) => button.includes("重新生成数学建议"))?.[0];
    expect(requestButton).toBeDefined();
    expect(requestButton?.includes('disabled=""')).toBe(disabled);
  });
  it("resolves bound formulas/blocks/verification to a single step and crop-relative box", () => {
    const { understanding, score } = fixture();
    expect(mathDecisionSteps(score.criterion_decisions[0], score, understanding).map((step) => step.id)).toEqual(["step1"]);
    expect(selectMathStep(understanding, "step1")?.bbox).toEqual({ x: .1, y: .2, width: .4, height: .10000000000000003 });
    expect(selectMathStep(understanding, "unknown")).toBeNull();
  });
  it("uses effective corrected coordinates, rejects crossed-out/invalid geometry", () => {
    const { understanding } = fixture();
    understanding.effective_artifact = { ...understanding.artifact, solution_graph: { ...understanding.artifact.solution_graph,
      steps: [{ ...understanding.artifact.solution_graph.steps[0], bbox: { x: .3, y: .4, width: .1, height: .2 } }] } } as unknown as NonNullable<MathUnderstandingResponse["effective_artifact"]>;
    expect(selectMathStep(understanding, "step1")?.bbox.x).toBe(.3);
    understanding.effective_artifact.solution_graph.steps[0].bbox!.x = 5;
    understanding.effective_artifact.blocks[0].status = "crossed_out";
    expect(selectMathStep(understanding, "step1")).toBeNull();
  });
  it("renders uncertain rows as pending, with a range and preserved stale history", () => {
    const { evidence, grade } = fixture();
    evidence.score!.suggested_score = null; evidence.score!.unresolved_score = 2; evidence.score!.verified_score = 0;
    evidence.score!.score_range = { min: 0, max: 2 };
    evidence.score!.criterion_decisions[0].awarded_score = null; evidence.score!.criterion_decisions[0].status = "uncertain";
    const html = renderToStaticMarkup(createElement(MathRubricMatrix, { evidence, grades: [{ ...grade, math_artifact_version: 2 }], points: [], dirty: false,
      requesting: false, canRequest: false, onRequest: () => {}, onSelectStep: () => {} }));
    expect(html).toContain("建议区间"); expect(html).toContain("0–2"); expect(html).toContain("待确认"); expect(html).toContain("已过期");
    expect(html).toContain("定位数学步骤 step1");
  });
  it("preserves fractional server values and zero scores while uncertain rows remain pending", () => {
    const { evidence, score } = fixture();
    score.verified_score = .25; score.unresolved_score = .5; score.max_score = 2.75; score.suggested_score = 0;
    score.criterion_decisions[0].max_score = 2.75; score.criterion_decisions[0].awarded_score = 0;
    const html = matrixHTML(evidence);
    expect(html).toContain("<strong>0.25</strong>");
    expect(html).toContain("<strong>0.5</strong>");
    expect(html).toContain("<strong>0<small> / 2.75</small></strong>");
    expect(html).toContain("<td>0 / 2.75</td>");
    score.suggested_score = null; score.score_range = { min: .25, max: 1.75 };
    score.criterion_decisions[0].status = "uncertain";
    const pending = matrixHTML(evidence);
    expect(pending).toContain("0.25–1.75");
    expect(pending).toContain("<td>待确认 / 2.75</td>");
  });
  it("preserves effective verification order and the empty verification source fallback", () => {
    const { evidence, understanding, score } = fixture();
    const verification = understanding.artifact.verifications![0];
    const effectiveArtifact: NonNullable<MathUnderstandingResponse["effective_artifact"]> = { ...understanding.artifact, input_hash: "test-input", relations: understanding.artifact.relations ?? null, rubric_evidence: understanding.artifact.rubric_evidence ?? null, verifications: [
      { ...verification, id: "v1", engine: "sympy", status: "contradicted" },
      { ...verification, id: "v2", engine: "rule", status: "verified" },
      { ...verification, id: "v3", engine: "rule", status: "not_applicable" },
    ] };
    understanding.effective_artifact = effectiveArtifact;
    score.criterion_decisions[0].verification_ids = ["v1", "v2", "v3"];
    expect(matrixHTML(evidence)).toContain("SymPy · 不成立；规则 · 已验证；规则 · 待确认");
    effectiveArtifact.verifications = [];
    score.criterion_decisions[0].decision_source = "model_candidate";
    expect(matrixHTML(evidence)).toContain("语义候选 · 人工确认");
    score.criterion_decisions[0].decision_source = "rule";
    expect(matrixHTML(evidence)).toContain("规则 / 语义待确认");
  });
  it("keeps dirty and conflicting evidence read-only and other phases limited to step navigation", () => {
    const { evidence } = fixture();
    const disabledButtons = (dirty = false, canRequest = true) =>
      (matrixHTML(evidence, { dirty, canRequest }).match(/<button\b[^>]*\bdisabled=""/g) ?? []).length;
    expect(disabledButtons()).toBe(0);
    expect(disabledButtons(false, false)).toBe(0);
    expect(disabledButtons(true)).toBe(2);
    evidence.phase = "conflict";
    expect(disabledButtons()).toBe(2);
    evidence.phase = "pending";
    expect(disabledButtons()).toBe(1);
  });
});
