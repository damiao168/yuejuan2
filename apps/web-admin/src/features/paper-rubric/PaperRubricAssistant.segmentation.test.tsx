// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";
import type { RubricAssistantDraft } from "./rubricAssistantDraft";

const mocks = vi.hoisted(() => ({ generate: vi.fn(), confirm: vi.fn() }));
vi.mock("../../api/papers", async (original) => ({ ...await original<typeof import("../../api/papers")>(), generateQuestionRubricDraft: mocks.generate }));
vi.mock("antd", async (original) => ({ ...await original<typeof import("antd")>(), App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));
const question = { id: "q1", tenant_id: "t", exam_id: "exam", question_no: "16", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", solution: { id: "s", question_id: "q1", solution_version: "v1", raw_text: "",
    steps: [{ step_no: 1, content: "先列方程然后求解答案" }], source_refs: [], verification_status: "human_confirmed" } } as Question;

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(cleanup);
function props(q = question) {
  return { question: q, disabled: false, contentDirty: false, hasExistingPoints: false, dirty: false, onDirtyChange: vi.fn(), onApply: vi.fn() };
}

describe("拆分、合并和 AI 共用解答单元", () => {
  it("长块支持光标拆分、合并、撤销；向AI发送调整后原文范围", async () => {
    const draftStore: { current: RubricAssistantDraft | null } = { current: null };
    const p = props(); const view = render(<PaperRubricAssistant {...p} draftStore={draftStore} />);
    // 控件已有明确标签，直接按标签定位，避免反复计算整页 Ant Design 节点的可见性。
    fireEvent.click(screen.getByLabelText("拆分解答单元 1"));
    const input = screen.getByLabelText("原解答拆分光标") as HTMLTextAreaElement;
    input.setSelectionRange(4, 4); fireEvent.select(input);
    const confirmSplit = screen.getByText("确认拆分").closest("button")!;
    await waitFor(() => expect(confirmSplit.disabled).toBe(false));
    fireEvent.click(confirmSplit);
    expect(screen.getByLabelText("拆分解答单元 2")).toBeDefined();
    fireEvent.click(screen.getByLabelText("合并解答单元 1 与下一单元"));
    expect(screen.queryByLabelText("拆分解答单元 2")).toBeNull();
    fireEvent.click(screen.getByText("撤销上次拆分或合并").closest("button")!);
    expect(screen.getByLabelText("拆分解答单元 2")).toBeDefined();
    expect(p.onDirtyChange).toHaveBeenCalledWith(true);
    view.unmount();
    render(<PaperRubricAssistant {...p} draftStore={draftStore} />);
    expect(screen.getByLabelText("拆分解答单元 2")).toBeDefined();
    fireEvent.click(screen.getByText("AI 建议分值划分"));
    mocks.generate.mockResolvedValue({ suggested_rubric_candidates: [] });
    fireEvent.click(screen.getByText("生成分值建议"));
    await waitFor(() => expect(mocks.generate).toHaveBeenCalledOnce());
    expect(mocks.generate.mock.calls[0][2]).toEqual([
      { spans: [{ source_id: "step-1", start: 0, end: 4 }] },
      { spans: [{ source_id: "step-1", start: 4, end: 10 }] }
    ]);
    expect(p.onApply).not.toHaveBeenCalled();
  });

  it("AI分段预览只调整边界，不把建议分值或采分条件直接写入评分细则", async () => {
    const p = props({ ...question, solution: { ...question.solution!, steps: [{ step_no: 1, content: "先列式。再求解。最后检验。" }] } });
    mocks.generate.mockResolvedValue({ suggested_rubric_candidates: [{ candidate_id: "q1", max_score: 6, origin: "ai_suggestion_from_solution", status: "review_required", issues: [], points: [
      { id: "a", description: "列式求解", suggested_score: 5, evidence_step_ids: ["step-1", "step-2"], review_note: "" },
      { id: "b", description: "检验", suggested_score: 1, evidence_step_ids: ["step-3"], review_note: "" }
    ] }] });
    render(<PaperRubricAssistant {...p} />);
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    fireEvent.click(screen.getByText("生成分段建议"));
    await screen.findByText("采用分段建议，不采用建议分值");
    expect(screen.getAllByLabelText(/^拆分解答单元/)).toHaveLength(3);
    fireEvent.click(screen.getByText("采用分段建议，不采用建议分值"));
    expect(screen.getAllByLabelText(/^拆分解答单元/)).toHaveLength(2);
    expect(p.onApply).not.toHaveBeenCalled();
  });

  it("旧分段建议在内容变更后被取消，不能覆盖教师新边界", async () => {
    let resolve!: (value: unknown) => void;
    mocks.generate.mockImplementation(() => new Promise((done) => { resolve = done; }));
    const p = props({ ...question, solution: { ...question.solution!, steps: [{ step_no: 1, content: "第一步。第二步。第三步。" }] } });
    render(<PaperRubricAssistant {...p} />);
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    fireEvent.click(screen.getByText("生成分段建议"));
    const signal = mocks.generate.mock.calls[0][1] as AbortSignal;
    fireEvent.click(screen.getByLabelText("合并解答单元 1 与下一单元"));
    expect(signal.aborted).toBe(true);
    await act(async () => resolve({ suggested_rubric_candidates: [] }));
    expect(screen.queryByText("采用分段建议，不采用建议分值")).toBeNull();
    expect(screen.getAllByLabelText(/^拆分解答单元/)).toHaveLength(2);
  });
});
