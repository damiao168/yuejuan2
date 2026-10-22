// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";
import type { RubricAssistantDraft } from "./rubricAssistantDraft";

const mocks = vi.hoisted(() => ({ generate: vi.fn(), confirm: vi.fn() }));
vi.mock("../../api/papers", async (original) => ({ ...await original<typeof import("../../api/papers")>(), generateQuestionRubricDraft: mocks.generate }));
vi.mock("antd", async (original) => ({ ...await original<typeof import("antd")>(), App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));

const question: Question = { id: "q1", tenant_id: "tenant", exam_id: "exam", question_no: "16", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", stem: "解方程",
  solution: { id: "solution", question_id: "q1", solution_version: "v1", raw_text: "", steps: [{ step_no: 1, content: "2x=4" }, { step_no: 2, content: "x=2" }], source_refs: [], verification_status: "human_confirmed" } };

function suggestion(id = "q1", score: number | null = 4) {
  return { suggested_rubric_candidates: [{ candidate_id: id, max_score: 6, origin: "ai_suggestion_from_solution", status: "review_required", issues: [], points: [
    { id: "a", description: "正确移项", suggested_score: score, evidence_step_ids: ["step-1"], review_note: "允许等价解法" },
    { id: "b", description: "求出结果", suggested_score: 2, evidence_step_ids: ["step-2"], review_note: "" }
  ] }] };
}

function setup(extra: Partial<React.ComponentProps<typeof PaperRubricAssistant>> = {}) {
  const props = { question, disabled: false, contentDirty: false, hasExistingPoints: false, dirty: false, onDirtyChange: vi.fn(), onApply: vi.fn(), ...extra };
  const view = render(<PaperRubricAssistant {...props} />);
  fireEvent.click(screen.getByText("AI 建议分值划分"));
  return { ...view, props };
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(cleanup);

describe("正式题目 AI 分值建议", () => {
  it("分值不完整时先修改再应用，保留对应解答且不直接保存", async () => {
    mocks.generate.mockResolvedValue(suggestion("q1", null));
    const { props } = setup();
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("AI 建议 · 待教师核对");
    expect((screen.getByText("采用建议到评分细则").closest("button") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("建议采分点 1 分值"), { target: { value: "4" } });
    fireEvent.blur(screen.getByLabelText("建议采分点 1 分值"));
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    expect(props.onApply).toHaveBeenCalledWith([
      expect.objectContaining({ description: "正确移项\n参考解答：\n2x=4", score: 4, required: false }),
      expect.objectContaining({ description: "求出结果\n参考解答：\nx=2", score: 2 })
    ]);
    expect(props.onDirtyChange).toHaveBeenCalledWith(true);
  });

  it("取消替换保留 AI 预览，不改变已有采分点", async () => {
    mocks.generate.mockResolvedValue(suggestion());
    const { props } = setup({ hasExistingPoints: true });
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("AI 建议 · 待教师核对");
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    expect(mocks.confirm).toHaveBeenCalled();
    expect(props.onApply).not.toHaveBeenCalled();
    expect(screen.getByDisplayValue("正确移项")).toBeDefined();
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(props.onApply).toHaveBeenCalledOnce();
  });

  it("换题取消请求并丢弃旧响应，其他编辑操作保持可用", async () => {
    let complete!: (value: ReturnType<typeof suggestion>) => void;
    mocks.generate.mockImplementation(() => new Promise((resolve) => { complete = resolve; }));
    const { props, rerender } = setup();
    fireEvent.click(screen.getByText("生成分值建议"));
    const signal = mocks.generate.mock.calls[0][1] as AbortSignal;
    expect((screen.getByText("按解答步骤配分").closest("label")?.querySelector("input") as HTMLInputElement).disabled).toBe(false);
    rerender(<PaperRubricAssistant {...props} question={{ ...question, id: "q2" }} />);
    expect(signal.aborted).toBe(true);
    await act(async () => complete(suggestion()));
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(props.onApply).not.toHaveBeenCalled();
  });

  it("配置回读卸载展示区后恢复 AI 预览和教师改分，旧弹窗不能触发请求", async () => {
    const draftStore: { current: RubricAssistantDraft | null } = { current: null };
    mocks.generate.mockResolvedValue(suggestion());
    const first = setup({ draftStore });
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("AI 建议 · 待教师核对");
    fireEvent.change(screen.getByLabelText("建议采分点 1 分值"), { target: { value: "3" } });
    fireEvent.blur(screen.getByLabelText("建议采分点 1 分值"));
    expect(draftStore.current?.ai.points[0].score).toBe(3);
    first.unmount();
    const second = setup({ draftStore });
    expect(Number((screen.getByLabelText("建议采分点 1 分值") as HTMLInputElement).value)).toBe(3);
    expect(screen.getByDisplayValue("正确移项")).toBeDefined();
    fireEvent.click(screen.getByText("重新生成分值建议"));
    second.unmount();
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.generate).toHaveBeenCalledOnce();
  });

  it("拒绝错题建议并可重试，未保存题目内容时禁止生成", async () => {
    mocks.generate.mockResolvedValue(suggestion("wrong-question"));
    const { props, rerender } = setup();
    fireEvent.click(screen.getByText("生成分值建议"));
    await waitFor(() => expect(screen.getByRole("alert")).toBeDefined());
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    rerender(<PaperRubricAssistant {...props} contentDirty />);
    expect((screen.getByText("生成分值建议").closest("button") as HTMLButtonElement).disabled).toBe(true);
  });
});
