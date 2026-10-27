// @vitest-environment jsdom
import type { ComponentProps, ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { listAiGrades, type AiGrade } from "../../../../api/review";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";
import { WorkbenchAiRecord } from "./EvidenceInspector";
import { WorkbenchTaskContent } from "./WorkbenchTaskContent";

const answerPane = vi.hoisted(() => vi.fn());

vi.mock("antd", () => {
  const container = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (item: unknown) => ReactNode }) => <div>{dataSource.map((item, index) => <div key={index}>{renderItem(item)}</div>)}</div>, { Item: container });
  return {
    Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <div>{message}{description}</div>,
    Drawer: ({ title, open, onClose, children }: { title: string; open: boolean; onClose: () => void; children: ReactNode }) => open ? <section role="dialog" aria-label={title}><button onClick={onClose}>关闭记录</button>{children}</section> : null,
    Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
    Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    List, Space: container, Tooltip: container, Spin: () => null,
    Tabs: ({ items }: { items: { key: string; label: string; children: ReactNode }[] }) => <div>{items.map(item => <div key={item.key}><h3>{item.label}</h3>{item.children}</div>)}</div>
  };
});
vi.mock("../../../../api/review", () => ({ listAiGrades: vi.fn() }));
vi.mock("../../../../components/StatusTag", () => ({ StatusTag: ({ tone, children }: { tone: string; children: ReactNode }) => <span data-tone={tone}>{children}</span> }));
vi.mock("./AnswerEvidencePane", () => ({ AnswerEvidencePane: (props: unknown) => { answerPane(props); return <section>学生作答</section>; } }));
vi.mock("./ScoreEditor", () => ({ ScoreEditor: ({ onShowAiRecord }: { onShowAiRecord?: () => void }) => <section>{onShowAiRecord ? <button onClick={onShowAiRecord}>评分依据</button> : null}</section> }));
vi.mock("../ReviewContextInspector", () => ({ ReviewContextInspector: () => <div>复核上下文</div> }));
vi.mock("../MathEvidenceInspector", () => ({ MathEvidenceInspector: () => null }));
vi.mock("./MathRubricMatrix", () => ({ MathRubricMatrix: () => null }));
vi.mock("./GradingAIPlanSettings", () => ({ gradingAIModeLabels: { single: "单模型评分" } }));

afterEach(() => { cleanup(); vi.clearAllMocks(); });

function imageGrade(id = "grade-1", answer = String.raw`x_1=\frac13`): AiGrade {
  return {
    id, question_id: "question-1", answer_segment_id: "segment-1", suggested_score: 0, max_score: 10,
    confidence: 0.9, needs_human_review: true, matched_points: [], missing_points: [], evidence: [], risk_flags: [],
    model_version: "已保存模型", raw_output: {
      schema_version: "grading-image-v3", reasoning_content: "不应展示的供应商私有内容",
      scoring_basis: { standard_answer: String.raw`x_1=\frac13`, solution_text: String.raw`由 3x_1=1 得解`, rubric_points: [{ id: "p", description: String.raw`求出 x_1=\frac13`, score: 2 }] },
      observation: { members: [{ question_id: "question-1", presence: "present", answer: { value: answer }, evidence: [{ evidence_id: "e", text: String.raw`写出 3x_1=1` }] }] },
      judgments: [{ criterion_id: "p", status: "supported", reason: String.raw`代入 x_1=\frac13 成立`, evidence_ids: ["e"] }]
    }
  } as unknown as AiGrade;
}

function props(taskId = "task-1", selectedGrade: AiGrade | undefined = imageGrade()): ComponentProps<typeof WorkbenchTaskContent> {
  const task = { id: taskId, answer_segment_id: "segment-1", question_id: "question-1", status: "submitted", source: "manual_review" };
  const context = {
    task, warnings: [], question: { question_type: "calculation" }, aiGrades: selectedGrade ? [selectedGrade] : [],
    reviewContext: { task, question: { question_type: "calculation" }, question_snapshot: { risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } }, subject_tool_hints: { subject_code: "mathematics" } }
  } as unknown as WorkbenchContext;
  const draft: ScoreDraft = { score: null, comments: "", privateNote: "", studentFeedback: "", reason: "", disputeReason: "", rubricSelections: {}, answerText: "" };
  return {
    context, loading: false, error: null, hasTasks: true, canManageTasks: false, onRetry: vi.fn(), selectedGrade, subjectCode: "mathematics", canAdoptAiScore: false,
    answer: {} as ComponentProps<typeof WorkbenchTaskContent>["answer"],
    evidence: {
      draft, selectedGrade, maxScore: 10, canEditDraft: false, canVerifyEvidence: false, actioning: null, onVerifyEvidence: vi.fn(async () => undefined),
      math: { enabled: false, state: { segmentId: "segment-1", phase: "unavailable", understanding: null, score: null, message: "" }, dirty: false, setDirty: vi.fn(), refresh: vi.fn(async () => null), correctionSaved: vi.fn() },
      onRefreshMath: vi.fn(), onSelectMathStep: vi.fn(), canRequestMath: false, requestingMath: false, onRequestMath: vi.fn(), mathRequestNotice: ""
    },
    editor: { draft, selectedGrade, maxScore: 10, rubricPoints: [], canEditDraft: false, canReturn: false, actioning: null, setDraft: vi.fn(), onAdoptAiScore: vi.fn(), onAdoptAiFeedback: vi.fn(), canAdoptAiScore: false, onMarkDispute: vi.fn(async () => undefined) }
  };
}

describe("顶部查看 AI 评分记录", () => {
  it("本题评语只写 studentFeedback，保留得分和历史教师评语及内部备注", () => {
    const initial = props();
    const value = { ...initial, editor: { ...initial.editor, canEditDraft: true, draft: { ...initial.editor.draft, score: 7, comments: "历史教师评语", privateNote: "历史内部备注", studentFeedback: "原学生反馈" } } };
    render(<WorkbenchTaskContent {...value} />);
    const pane = answerPane.mock.lastCall![0];
    expect(pane.studentFeedback).toBe("原学生反馈");
    expect(pane.historicalComments).toBe("历史教师评语");
    expect(pane.historicalPrivateNote).toBe("历史内部备注");
    pane.onStudentFeedbackChange("新本题评语");
    const update = vi.mocked(value.editor.setDraft).mock.calls[0][0] as (draft: ScoreDraft) => ScoreDraft;
    expect(update(value.editor.draft)).toEqual({ ...value.editor.draft, studentFeedback: "新本题评语" });
    expect(value.editor.onAdoptAiScore).not.toHaveBeenCalled();
  });

  it("只读或建议不可采纳时仍能打开完整记录，保留公式且不请求评分或修改草稿", () => {
    const value = props();
    const { container } = render(<WorkbenchTaskContent {...value} />);
    expect(container.querySelector(".grading-ai-record")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "评分依据" }));
    const dialog = screen.getByRole("dialog", { name: "AI评分记录" });
    for (const text of ["建议分 0 / 10", "使用模型：已保存模型", "本次冻结的标准答案", "标准解答", "模型识别的作答", "逐项评分依据", "代入", "写出"]) expect(dialog.textContent).toContain(text);
    expect(dialog.querySelectorAll(".katex").length).toBeGreaterThanOrEqual(6);
    expect(dialog.textContent).not.toContain("不应展示的供应商私有内容");
    expect(value.editor.onAdoptAiScore).not.toHaveBeenCalled();
    expect(value.editor.onAdoptAiFeedback).not.toHaveBeenCalled();
    expect(value.editor.setDraft).not.toHaveBeenCalled();
    expect(value.evidence.onRequestMath).not.toHaveBeenCalled();
    expect(listAiGrades).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "关闭记录" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("切题后关闭抽屉，返回原题也不恢复上一份记录的打开状态", () => {
    const first = props();
    const second = props("task-2", imageGrade("grade-2", "第二份作答"));
    const { rerender } = render(<WorkbenchTaskContent {...first} />);
    fireEvent.click(screen.getByRole("button", { name: "评分依据" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    rerender(<WorkbenchTaskContent {...second} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "评分依据" }));
    expect(screen.getByRole("dialog").textContent).toContain("第二份作答");
    rerender(<WorkbenchTaskContent {...first} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it.each([{ loading: true }, { error: "加载失败" }])("上下文不可用时清理记录，恢复后保持关闭 %j", (state) => {
    const value = props();
    const { rerender } = render(<WorkbenchTaskContent {...value} />);
    fireEvent.click(screen.getByRole("button", { name: "评分依据" }));
    rerender(<WorkbenchTaskContent {...value} {...state} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    rerender(<WorkbenchTaskContent {...value} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("独立评分策略不提供入口，记录组件也不能绕过策略或展示影子结果", () => {
    const value = props();
    value.context!.reviewContext.question_snapshot.risk_tier = "R3";
    value.context!.reviewContext.question_snapshot.scoring_policy_snapshot.mode = "HUMAN_PRIMARY";
    const { rerender, container } = render(<WorkbenchTaskContent {...value} />);
    expect(screen.queryByRole("button", { name: "评分依据" })).toBeNull();
    rerender(<WorkbenchAiRecord context={value.context!} selectedGrade={value.selectedGrade} math={value.evidence.math} />);
    expect(container.textContent).toBe("");
    rerender(<WorkbenchAiRecord context={props().context!} selectedGrade={{ ...imageGrade(), delivery_mode: "shadow_only" }} math={value.evidence.math} />);
    expect(container.textContent).toBe("");
  });

  it("没有记录时显示空态并保持只读", () => {
    const current = props();
    const value = { ...current, selectedGrade: undefined, evidence: { ...current.evidence, selectedGrade: undefined }, editor: { ...current.editor, selectedGrade: undefined } };
    render(<WorkbenchTaskContent {...value} />);
    fireEvent.click(screen.getByRole("button", { name: "评分依据" }));
    expect(screen.getByRole("dialog").textContent).toContain("暂无 AI 评分记录");
    expect(listAiGrades).not.toHaveBeenCalled();
    expect(value.evidence.onRequestMath).not.toHaveBeenCalled();
  });
});
