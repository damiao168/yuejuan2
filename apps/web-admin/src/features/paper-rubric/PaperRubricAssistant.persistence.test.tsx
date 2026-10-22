// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question, RubricPayload } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";
import { useRubricEditor } from "./hooks/useRubricEditor";

const mocks = vi.hoisted(() => ({
  generate: vi.fn(), createRubric: vi.fn(), loadConfig: vi.fn(), confirm: vi.fn(),
  message: { success: vi.fn(), error: vi.fn() }
}));
vi.mock("../../api/papers", async (original) => ({
  ...await original<typeof import("../../api/papers")>(),
  generateQuestionRubricDraft: mocks.generate, createRubric: mocks.createRubric
}));
vi.mock("antd", async (original) => ({
  ...await original<typeof import("antd")>(),
  App: { useApp: () => ({ modal: { confirm: mocks.confirm }, message: mocks.message }) }
}));
vi.mock("../../components/MathMarkdown", () => ({
  MathMarkdown: ({ children }: { children: string }) => <div>{children}</div>
}));

const question: Question = {
  id: "q1", tenant_id: "tenant", exam_id: "exam", question_no: "15", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", stem: "解方程",
  solution: { id: "solution", question_id: "q1", solution_version: "v1", raw_text: "", source_refs: [],
    steps: [{ step_no: 1, content: "2x=4" }, { step_no: 2, content: "x=2" }], verification_status: "human_confirmed" },
  rubric: { id: "rubric", question_id: "q1", version: "1", status: "draft", max_score: 6,
    points: [{ id: "saved-point", description: "原评分规则", score: 6, required: true }], deductions: [], examples: [] }
};
const suggestion = {
  suggested_rubric_candidates: [{ candidate_id: "q1", max_score: 6, origin: "ai_suggestion_from_solution", status: "review_required", issues: [], points: [
    { id: "a", description: "正确移项", suggested_score: 4, evidence_step_ids: ["step-1"], review_note: "" },
    { id: "b", description: "求出结果", suggested_score: 2, evidence_step_ids: ["step-2"], review_note: "" }
  ] }]
};

// 使用页面真实的重挂载方式和编辑器存储，保护“采用后重挂”和“保存后回读”这两个状态边界。
function EditorHarness({ selectedQuestion }: { selectedQuestion: Question }) {
  const editor = useRubricEditor({ selectedQuestion, selectedExamId: selectedQuestion.exam_id, loadConfig: mocks.loadConfig });
  return <>
    <PaperRubricAssistant key={JSON.stringify([selectedQuestion, editor.assistantResetVersion])}
      question={selectedQuestion} disabled={false} contentDirty={false} hasExistingPoints={editor.rubricPoints.length > 0}
      dirty={editor.assistantDirty} onDirtyChange={editor.setAssistantDirty} onApply={editor.replaceRubricPoints}
      draftStore={editor.assistantDraftStore} />
    <output aria-label="细则修改状态">{String(editor.rubricDirty)}</output>
    <output aria-label="助手预览状态">{String(editor.assistantDirty)}</output>
    <output aria-label="评分细则内容">{JSON.stringify(editor.rubricPoints)}</output>
    <button onClick={() => void editor.saveRubric()}>保存测试细则</button>
  </>;
}

function expectMethod(method: "steps" | "ai") {
  const radio = screen.getByRole("radio", { name: method === "ai" ? /AI/ : /按解答步骤配分/ }) as HTMLInputElement;
  expect(radio.checked).toBe(true);
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.generate.mockResolvedValue(suggestion);
  mocks.createRubric.mockResolvedValue({ ...question.rubric, version: "2" });
  mocks.loadConfig.mockResolvedValue(undefined);
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({
    matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}
  }) });
});
afterEach(cleanup);

describe("分值建议方式在采用与保存后的恢复", () => {
  it("选择 AI 不产生未保存草稿；采用并保存后保留 AI 方式，清除预览且保存教师采用的采分点", async () => {
    const view = render(<EditorHarness selectedQuestion={question} />);
    fireEvent.click(screen.getByRole("radio", { name: /AI/ }));
    expectMethod("ai");
    expect(screen.getByLabelText("细则修改状态").textContent).toBe("false");
    expect(mocks.generate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "生成分值建议" }));
    await screen.findByText("AI 建议 · 待教师核对");
    fireEvent.click(screen.getByRole("button", { name: "保存测试细则" }));
    await waitFor(() => expect(mocks.message.error).toHaveBeenCalledOnce());
    expect(mocks.createRubric).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "采用建议到评分细则" }));
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expectMethod("ai");
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(screen.getByLabelText("助手预览状态").textContent).toBe("false");
    expect(screen.getByLabelText("细则修改状态").textContent).toBe("true");
    expect(screen.getByLabelText("评分细则内容").textContent).toContain("正确移项");

    fireEvent.click(screen.getByRole("button", { name: "保存测试细则" }));
    await waitFor(() => expect(mocks.createRubric).toHaveBeenCalledOnce());
    const payload = mocks.createRubric.mock.calls[0][1] as RubricPayload;
    expect(payload.points).toEqual([
      expect.objectContaining({ description: "正确移项\n参考解答：\n2x=4", score: 4 }),
      expect.objectContaining({ description: "求出结果\n参考解答：\nx=2", score: 2 })
    ]);
    await waitFor(() => expect(mocks.loadConfig).toHaveBeenCalledOnce());
    view.rerender(<EditorHarness selectedQuestion={{ ...question, rubric: { ...question.rubric!, ...payload, version: "2" } }} />);
    expectMethod("ai");
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(screen.getByLabelText("评分细则内容").textContent).toContain("正确移项");
    expect(screen.getByLabelText("细则修改状态").textContent).toBe("false");
    expect(mocks.generate).toHaveBeenCalledOnce();
  });

  it.each([
    ["题目", { id: "q2", question_no: "16" }],
    ["考试", { exam_id: "another-exam" }],
    ["学校", { tenant_id: "another-tenant" }]
  ])("切换%s后不沿用上一题的 AI 方式", (_, identity) => {
    const view = render(<EditorHarness selectedQuestion={question} />);
    fireEvent.click(screen.getByRole("radio", { name: /AI/ }));
    view.rerender(<EditorHarness selectedQuestion={{ ...question, ...identity }} />);
    expectMethod("steps");
    expect(screen.getByLabelText("细则修改状态").textContent).toBe("false");
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it("同题参考解答更新后保留 AI 方式，但旧建议不能继续应用", async () => {
    const view = render(<EditorHarness selectedQuestion={question} />);
    fireEvent.click(screen.getByRole("radio", { name: /AI/ }));
    fireEvent.click(screen.getByRole("button", { name: "生成分值建议" }));
    await screen.findByText("AI 建议 · 待教师核对");
    view.rerender(<EditorHarness selectedQuestion={{ ...question, solution: { ...question.solution!, solution_version: "v2",
      steps: [{ step_no: 1, content: "3x=6" }, { step_no: 2, content: "x=2" }] } }} />);
    expectMethod("ai");
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(screen.queryByRole("button", { name: "采用建议到评分细则" })).toBeNull();
    expect(screen.getByLabelText("助手预览状态").textContent).toBe("false");
    expect(mocks.generate).toHaveBeenCalledOnce();
  });

  it("同题来源更新取消旧请求，迟到结果不能恢复旧预览或重置方式", async () => {
    let complete!: (value: typeof suggestion) => void;
    mocks.generate.mockImplementation(() => new Promise((resolve) => { complete = resolve; }));
    const view = render(<EditorHarness selectedQuestion={question} />);
    fireEvent.click(screen.getByRole("radio", { name: /AI/ }));
    fireEvent.click(screen.getByRole("button", { name: "生成分值建议" }));
    const signal = mocks.generate.mock.calls[0][1] as AbortSignal;
    view.rerender(<EditorHarness selectedQuestion={{ ...question, solution: { ...question.solution!, solution_version: "v2", raw_text: "新解答" } }} />);
    expect(signal.aborted).toBe(true);
    await act(async () => complete(suggestion));
    expectMethod("ai");
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(screen.getByLabelText("助手预览状态").textContent).toBe("false");
    expect(mocks.generate).toHaveBeenCalledOnce();
  });

  it("同题解答来源更新清除原来的手动配分", () => {
    const view = render(<EditorHarness selectedQuestion={question} />);
    fireEvent.click(screen.getByRole("button", { name: "在第 1 行结束采分段" }));
    fireEvent.change(screen.getByLabelText("本段分值"), { target: { value: "4" } });
    fireEvent.blur(screen.getByLabelText("本段分值"));
    fireEvent.click(screen.getByRole("button", { name: "添加为采分点" }));
    expect(screen.getByLabelText("采分点 1 分值")).toBeDefined();
    view.rerender(<EditorHarness selectedQuestion={{ ...question, solution: { ...question.solution!, solution_version: "v2",
      steps: [{ step_no: 1, content: "3x=6" }, { step_no: 2, content: "x=2" }] } }} />);
    expect(screen.queryByLabelText("采分点 1 分值")).toBeNull();
    expect(screen.getByLabelText("助手预览状态").textContent).toBe("false");
    expect(screen.getByText("已分配 0 / 6 分")).toBeDefined();
  });
});
