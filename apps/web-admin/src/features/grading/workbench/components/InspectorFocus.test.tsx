// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { EvidenceInspector, type EvidenceInspectorProps } from "./EvidenceInspector";
import { ScoreEditor, type ScoreEditorProps } from "./ScoreEditor";

vi.mock("../ReviewContextInspector", () => ({ ReviewContextInspector: () => <div data-inspector="context" /> }));
vi.mock("../MathEvidenceInspector", () => ({ MathEvidenceInspector: () => <div data-inspector="formula" /> }));
vi.mock("./MathRubricMatrix", () => ({ MathRubricMatrix: () => <div data-inspector="matrix" /> }));
vi.mock("../../SharedScoreControl", () => ({ SharedScoreControl: () => <div data-inspector="score-control" /> }));

function evidenceProps(questionType: string): EvidenceInspectorProps {
  const task = { id: "task", question_id: "question", question_no: "1", answer_segment_id: "segment", source: "omr_ambiguous", status: "in_progress" };
  const context = {
    task, question: { question_type: questionType }, aiGrades: [],
    reviewContext: {
      task, question: { question_type: questionType },
      question_snapshot: { risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } },
      subject_tool_hints: { subject_code: "mathematics", archetype_code: "selected_response" },
      ai_candidates: [{ source: "omr", is_current: true, decision: "selected", evidence: { auto_confirm_eligible: false, auto_confirm_reason: "manual_only_profile" } }]
    },
    automationResult: { source: "omr", recognized_answer: "B", standard_answer: "B", confidence: 1, decision: "selected", max_score: 5 }
  } as unknown as WorkbenchContext;
  return {
    context, draft: { score: null, comments: "", privateNote: "", studentFeedback: "", reason: "", disputeReason: "", rubricSelections: {}, answerText: "B" },
    maxScore: 5, canEditDraft: false, canVerifyEvidence: false, actioning: null, onVerifyEvidence: vi.fn(async () => undefined),
    math: { enabled: true, state: { segmentId: "segment", phase: "unavailable", understanding: null, score: null, message: "" }, dirty: false, setDirty: vi.fn(), refresh: vi.fn(async () => null), correctionSaved: vi.fn() },
    onRefreshMath: vi.fn(), onSelectMathStep: vi.fn(), canRequestMath: true, requestingMath: false, onRequestMath: vi.fn(), mathRequestNotice: "旧数学请求失败"
  };
}

function parse(element: React.ReactNode) {
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(element);
  return host;
}

describe("single-question inspector focus", () => {
  it.each(["single_choice", "multiple_choice", "true_false"])("does not mount math tools for %s in a mathematics exam", (questionType) => {
    const host = parse(<EvidenceInspector {...evidenceProps(questionType)} />);
    expect(host.querySelector('[data-inspector="formula"]')).toBeNull();
    expect(host.querySelector('[data-inspector="matrix"]')).toBeNull();
    expect(host.querySelector("output.math-suggestion-note")).toBeNull();
    expect(host.querySelectorAll(".task-automation-grid strong")).toHaveLength(4);
    expect(host.textContent).toContain("只提供参考结果");
    expect(host.textContent).not.toContain("歧义");
  });

  it("preserves step evidence and scoring criteria for a subjective mathematics question", () => {
    const host = parse(<EvidenceInspector {...evidenceProps("calculation")} />);
    expect(host.querySelector('[data-inspector="formula"]')).not.toBeNull();
    expect(host.querySelector('[data-inspector="matrix"]')).not.toBeNull();
  });

  it("shows image suggestions without the obsolete rule-result card or manual evidence action", () => {
    const props = evidenceProps("calculation");
    const selectedGrade = { id: "image-grade", suggested_score: 8, max_score: 15, confidence: 0.9, needs_human_review: true,
      matched_points: [], missing_points: [], evidence: [], risk_flags: [], raw_output: { schema_version: "grading-image-v3" } } as unknown as NonNullable<EvidenceInspectorProps["selectedGrade"]>;
    const host = parse(<EvidenceInspector {...props} selectedGrade={selectedGrade} canVerifyEvidence />);
    expect(host.querySelector(".task-automation-result")).toBeNull();
    expect(host.querySelector('[data-inspector="matrix"]')).toBeNull();
    expect(host.querySelector('[data-inspector="formula"]')).toBeNull();
    expect(host.textContent).toContain("8 / 15");
    expect(host.textContent).not.toContain("校验 AI 证据");
    expect(host.textContent).not.toContain("未生效 · 转人工");
  });

  it("does not mount editors or submission actions in a read-only task even for an administrator", () => {
    const props = evidenceProps("calculation");
    const editor = { ...props, setDraft: vi.fn(), rubricPoints: [], canSubmit: false, canReturn: true, canManageTasks: true,
      onAdoptAiScore: vi.fn(), onMarkDispute: vi.fn(async () => undefined), onSubmit: vi.fn(async () => undefined) } as ScoreEditorProps;
    const readonly = parse(<ScoreEditor {...editor} />);
    expect(readonly.querySelector('[data-inspector="score-control"]')).toBeNull();
    expect(readonly.querySelector("button, input, textarea")).toBeNull();
    const editable = parse(<ScoreEditor {...editor} canEditDraft />);
    expect(editable.querySelector('[data-inspector="score-control"]')).not.toBeNull();
  });
});
