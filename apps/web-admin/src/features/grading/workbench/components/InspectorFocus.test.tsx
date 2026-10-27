// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { cleanup, fireEvent, render } from "@testing-library/react";
import { Modal } from "antd";
import { describe, expect, it, vi } from "vitest";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { EvidenceInspector, type EvidenceInspectorProps } from "./EvidenceInspector";
import { ScoreEditor, type ScoreEditorProps } from "./ScoreEditor";

vi.mock("../ReviewContextInspector", () => ({ ReviewContextInspector: () => <div data-inspector="context" /> }));
vi.mock("../MathEvidenceInspector", () => ({ MathEvidenceInspector: () => <div data-inspector="formula" /> }));
vi.mock("./MathRubricMatrix", () => ({ MathRubricMatrix: () => <div data-inspector="matrix" /> }));
vi.mock("./GradingMathMarkdown", () => ({ GradingMathMarkdown: ({ children, className }: { children: string; className?: string }) => <div className={className} data-grading-math="true">{children}</div> }));
vi.mock("../../SharedScoreControl", () => ({ SharedScoreControl: ({ showRubric }: { showRubric?: boolean }) => <div data-inspector="score-control" data-show-rubric={String(showRubric)} /> }));

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
    expect(host.querySelectorAll(".task-automation-grid > div")).toHaveLength(4);
    expect(host.querySelectorAll(".task-automation-answer[data-grading-math]")).toHaveLength(2);
    expect(host.textContent).toContain("只提供参考结果");
    expect(host.textContent).not.toContain("歧义");
  });

  it("preserves step evidence and scoring criteria for a subjective mathematics question", () => {
    const host = parse(<EvidenceInspector {...evidenceProps("calculation")} />);
    expect(host.querySelector('[data-inspector="formula"]')).not.toBeNull();
    expect(host.querySelector('[data-inspector="matrix"]')).not.toBeNull();
  });

  it("识别摘要与规则结果中的作答和标准答案都使用公式展示", () => {
    const props = evidenceProps("calculation");
    const recognized = String.raw`P=p_1p_2...p_n`;
    const standard = String.raw`\frac13`;
    props.context.automationResult = { ...props.context.automationResult!, recognized_answer: recognized, standard_answer: standard };
    const result = parse(<EvidenceInspector {...props} />);
    expect(Array.from(result.querySelectorAll(".task-automation-answer[data-grading-math]")).map((node) => node.textContent)).toEqual([recognized, standard]);
    props.context.automationResult = undefined;
    props.draft.answerText = recognized;
    const summary = parse(<EvidenceInspector {...props} />);
    expect(summary.querySelector(".ocr-snippet [data-grading-math]")?.textContent).toBe(recognized);
  });

  it("图像 AI 评分记录不再占据底部面板，原有无关工具也不展示", () => {
    const props = evidenceProps("calculation");
    const selectedGrade = { id: "image-grade", suggested_score: 8, max_score: 15, confidence: 0.9, needs_human_review: true,
      matched_points: [], missing_points: [], evidence: [], risk_flags: [], raw_output: { schema_version: "grading-image-v3" } } as unknown as NonNullable<EvidenceInspectorProps["selectedGrade"]>;
    const host = parse(<EvidenceInspector {...props} selectedGrade={selectedGrade} canVerifyEvidence />);
    expect(host.querySelector(".task-automation-result")).toBeNull();
    expect(host.querySelector('[data-inspector="matrix"]')).toBeNull();
    expect(host.querySelector('[data-inspector="formula"]')).toBeNull();
    expect(host.textContent).not.toContain("8 / 15");
    expect(host.querySelector(".ai-evidence-details")).toBeNull();
    expect(host.textContent).not.toContain("校验 AI 证据");
    expect(host.textContent).not.toContain("未生效 · 转人工");
  });

  it("does not mount editors or submission actions in a read-only task even for an administrator", () => {
    const props = evidenceProps("calculation");
    const editor = { ...props, setDraft: vi.fn(), rubricPoints: [], canReturn: true,
      onAdoptAiScore: vi.fn(), onMarkDispute: vi.fn(async () => undefined) } as ScoreEditorProps;
    const readonly = parse(<ScoreEditor {...editor} />);
    expect(readonly.querySelector('[data-inspector="score-control"]')).toBeNull();
    expect(readonly.querySelector("button, input, textarea")).toBeNull();
    const editable = parse(<ScoreEditor {...editor} canEditDraft />);
    expect(editable.querySelector('[data-inspector="score-control"]')).not.toBeNull();
    expect(editable.querySelector('[data-inspector="score-control"]')?.getAttribute("data-show-rubric")).toBe("false");
    for (const obsolete of ["提交并下一份", "提交并提名标准卷", "连续阅卷", "Ctrl / ⌘ + Enter"]) expect(editable.textContent).not.toContain(obsolete);
  });

  it.each([0, 5])("评分栏首行显示真实 AI 建议 %s 分，并保留采纳禁用条件", (score) => {
    const editor = { ...evidenceProps("calculation"), setDraft: vi.fn(), rubricPoints: [], canEditDraft: true, canReturn: false,
      maxScore: 17, selectedGrade: { suggested_score: score, student_feedback: "检查推导" }, canAdoptAiScore: false,
      onAdoptAiScore: vi.fn(), onAdoptAiFeedback: vi.fn(), onShowAiRecord: vi.fn(), onMarkDispute: vi.fn(async () => undefined) } as unknown as ScoreEditorProps;
    const host = parse(<ScoreEditor {...editor} />);
    expect(host.querySelector(".score-ai-summary")?.textContent).toBe(`AI 建议 ${score} / 17 分采纳评分依据`);
    expect(host.querySelector('[aria-label="采纳 AI 建议分"]')?.hasAttribute("disabled")).toBe(true);
    expect(host.querySelector(".score-ai-basis")?.hasAttribute("disabled")).toBe(false);
    expect(host.querySelector(".grading-more-fields .grading-ai-feedback-adoption")?.hasAttribute("disabled")).toBe(true);
    expect(host.querySelector("h2")).toBeNull();
    expect(host.textContent).not.toContain("审读 AI 反馈");
  });

  it.each([true, false])("完整 AI 记录没有反馈文字时仍可查看，可编辑状态为 %s", (canEditDraft) => {
    const onShowAiRecord = vi.fn();
    const confirm = vi.spyOn(Modal, "confirm");
    const editor = { ...evidenceProps("calculation"), setDraft: vi.fn(), rubricPoints: [], canEditDraft, canReturn: false,
      selectedGrade: { suggested_score: 5 }, canAdoptAiScore: false,
      onAdoptAiScore: vi.fn(), onAdoptAiFeedback: vi.fn(), onShowAiRecord, onMarkDispute: vi.fn(async () => undefined) } as unknown as ScoreEditorProps;
    const view = render(<ScoreEditor {...editor} />);
    try {
      fireEvent.click(view.getByRole("button", { name: "评分依据" }));
      expect(onShowAiRecord).toHaveBeenCalledOnce();
      expect(confirm).not.toHaveBeenCalled();
      expect(view.container.querySelector(".grading-ai-feedback-adoption")).toBeNull();
    } finally {
      cleanup();
      confirm.mockRestore();
    }
  });

  it("没有 AI 建议时不展示分数或采纳入口，独立评分继续隐藏 AI 摘要", () => {
    const editor = { ...evidenceProps("calculation"), setDraft: vi.fn(), rubricPoints: [], canEditDraft: true, canReturn: false,
      onAdoptAiScore: vi.fn(), onMarkDispute: vi.fn(async () => undefined) } as ScoreEditorProps;
    const empty = parse(<ScoreEditor {...editor} />);
    expect(empty.querySelector(".score-ai-summary")?.textContent).toBe("暂无 AI 建议");
    expect(empty.querySelector('[aria-label="采纳 AI 建议分"]')).toBeNull();
    editor.onShowAiRecord = vi.fn();
    const recordOnly = parse(<ScoreEditor {...editor} />);
    expect(recordOnly.querySelector(".score-ai-basis")).not.toBeNull();
    expect(recordOnly.querySelector('[aria-label="采纳 AI 建议分"]')).toBeNull();
    editor.context.reviewContext.question_snapshot.risk_tier = "R3";
    editor.context.reviewContext.question_snapshot.scoring_policy_snapshot.mode = "HUMAN_PRIMARY";
    editor.selectedGrade = { suggested_score: 5 } as ScoreEditorProps["selectedGrade"];
    const independent = parse(<ScoreEditor {...editor} />);
    expect(independent.querySelector(".score-ai-summary")).toBeNull();
    expect(independent.querySelector('[aria-label="采纳 AI 建议分"]')).toBeNull();
  });
});
