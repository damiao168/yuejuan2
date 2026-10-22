// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";

const mocks = vi.hoisted(() => ({ generate: vi.fn(), confirm: vi.fn() }));
vi.mock("../../api/papers", async (original) => ({ ...await original<typeof import("../../api/papers")>(), generateQuestionRubricDraft: mocks.generate }));
vi.mock("antd", async (original) => ({ ...await original<typeof import("antd")>(), App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));

const question: Question = { id: "q1", tenant_id: "tenant", exam_id: "exam", question_no: "1", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", stem: "解方程",
  solution: { id: "solution", question_id: "q1", solution_version: "v1", raw_text: "x=2", steps: [], source_refs: [], verification_status: "human_confirmed" } };
const suggestion = { suggested_rubric_candidates: [{ candidate_id: "q1", max_score: 6, origin: "ai_suggestion_from_solution", status: "review_required", issues: [],
  points: [{ id: "point", description: "求出结果", suggested_score: 6, evidence_step_ids: ["step-1"], review_note: "" }] }] };

beforeEach(() => {
  vi.clearAllMocks();
  mocks.generate.mockResolvedValue(suggestion);
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(cleanup);

async function prepareConfirmation() {
  const props = { question, disabled: false, contentDirty: false, hasExistingPoints: false, dirty: false, onDirtyChange: vi.fn(), onApply: vi.fn() };
  const view = render(<PaperRubricAssistant key="q1" {...props} />);
  fireEvent.click(screen.getByText("AI 建议分值划分"));
  fireEvent.click(screen.getByText("生成分值建议"));
  await screen.findByText("AI 建议 · 待教师核对");
  fireEvent.click(screen.getByText("重新生成分值建议"));
  return { ...view, props, confirm: mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0] as { onOk: () => void } };
}

describe("重新生成确认的题目与编辑边界", () => {
  it("确认框打开后换题，旧确认不能清除新题草稿或发起旧题请求", async () => {
    const { rerender, props, confirm } = await prepareConfirmation();
    rerender(<PaperRubricAssistant key="q2" {...props} question={{ ...question, id: "q2" }} />);
    props.onDirtyChange.mockClear();
    // AntD 的全局确认框会比原组件活得更久，必须验证卸载之后再确认的次序。
    await act(async () => confirm.onOk());
    expect(mocks.generate).toHaveBeenCalledTimes(1);
    expect(props.onDirtyChange).not.toHaveBeenCalled();
  });

  it("确认框打开后进入只读，确认不能清空预览或继续请求", async () => {
    const { rerender, props, confirm } = await prepareConfirmation();
    rerender(<PaperRubricAssistant key="q1" {...props} disabled />);
    props.onDirtyChange.mockClear();
    await act(async () => confirm.onOk());
    expect(mocks.generate).toHaveBeenCalledTimes(1);
    expect(props.onDirtyChange).not.toHaveBeenCalled();
    expect(screen.getByText("AI 建议 · 待教师核对")).toBeDefined();
  });

  it("题目仍可编辑时确认可以重新生成", async () => {
    const { props, confirm } = await prepareConfirmation();
    await act(async () => confirm.onOk());
    expect(mocks.generate).toHaveBeenCalledTimes(2);
    expect(props.onDirtyChange).toHaveBeenCalledWith(false);
    expect(screen.getByText("AI 建议 · 待教师核对")).toBeDefined();
  });

  it("放弃建议并切换方式确认期间进入只读，旧确认不能清除预览", async () => {
    const { props, rerender } = await prepareConfirmation();
    rerender(<PaperRubricAssistant key="q1" {...props} dirty />);
    fireEvent.click(screen.getByText("按解答步骤配分"));
    const confirmation = mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0];
    rerender(<PaperRubricAssistant key="q1" {...props} dirty disabled />);
    props.onDirtyChange.mockClear();
    await act(async () => confirmation.onOk());
    expect(props.onDirtyChange).not.toHaveBeenCalled();
    expect(screen.getByText("AI 建议 · 待教师核对")).toBeDefined();
    expect(mocks.generate).toHaveBeenCalledTimes(1);
  });

  it("仍可编辑时确认放弃建议可以切换方式", async () => {
    const { props, rerender } = await prepareConfirmation();
    rerender(<PaperRubricAssistant key="q1" {...props} dirty />);
    fireEvent.click(screen.getByText("按解答步骤配分"));
    const confirmation = mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0];
    props.onDirtyChange.mockClear();
    await act(async () => confirmation.onOk());
    expect(props.onDirtyChange).toHaveBeenCalledWith(false);
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
    expect(screen.getByRole("button", { name: "采用分段结果" })).toBeDefined();
  });
});
