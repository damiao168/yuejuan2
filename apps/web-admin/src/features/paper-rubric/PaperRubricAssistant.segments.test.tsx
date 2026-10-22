// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { PaperRubricAssistant } from "./PaperRubricAssistant";

const mocks = vi.hoisted(() => ({ generate: vi.fn(), confirm: vi.fn() }));
vi.mock("../../api/papers", async (original) => ({ ...await original<typeof import("../../api/papers")>(), generateQuestionRubricDraft: mocks.generate }));
vi.mock("antd", async (original) => ({ ...await original<typeof import("antd")>(), App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));

const question: Question = { id: "q1", tenant_id: "tenant", exam_id: "exam", question_no: "16", question_type: "calculation", score: 6,
  options: [], knowledge_points: [], sort_order: 1, status: "draft", stem: "解方程",
  solution: { id: "solution", question_id: "q1", solution_version: "v1", raw_text: "", steps: [{ step_no: 1, content: "甲😀乙" }, { step_no: 2, content: " " }, { step_no: 3, content: "最终答案" }], source_refs: [], verification_status: "human_confirmed" } };
const response = { suggested_rubric_candidates: [{ candidate_id: "q1", max_score: 6, origin: "ai_suggestion_from_solution", status: "review_required", issues: [],
  points: [{ id: "point", description: "完整过程", suggested_score: 6, evidence_step_ids: ["step-1", "step-2"], review_note: "" }] }] };

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }) });
});
afterEach(cleanup);

function setup(hasExistingPoints = false) {
  const props = { question, disabled: false, contentDirty: false, hasExistingPoints, dirty: false, onDirtyChange: vi.fn(), onApply: vi.fn() };
  return { ...render(<PaperRubricAssistant {...props} />), props };
}

describe("分段版本与AI建议边界", () => {
  it("提交完整UTF16原文范围，空步骤保持来源编号，响应step编号按当前单元解释", async () => {
    mocks.generate.mockResolvedValue(response);
    const { props } = setup();
    fireEvent.click(screen.getByText("AI 建议分值划分"));
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("AI 建议 · 待教师核对");
    expect(mocks.generate.mock.calls[0][2]).toEqual([
      { spans: [{ source_id: "step-1", start: 0, end: 4 }] }, { spans: [{ source_id: "step-3", start: 0, end: 4 }] }
    ]);
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    expect(props.onApply).toHaveBeenCalledWith([expect.objectContaining({ description: "完整过程\n参考解答：\n甲😀乙\n最终答案", score: 6 })]);
  });

  it("AI分段建议返回前人工改变边界，取消旧请求且迟到结果不能重新出现", async () => {
    let complete!: (value: typeof response) => void;
    mocks.generate.mockImplementation(() => new Promise((resolve) => { complete = resolve; }));
    const { props } = setup();
    fireEvent.click(screen.getByText("让 AI 建议采分段（可选）"));
    fireEvent.click(screen.getByText("生成分段建议"));
    const signal = mocks.generate.mock.calls[0][1] as AbortSignal;
    fireEvent.click(screen.getByRole("button", { name: "合并解答单元 1 与下一单元" }));
    expect(signal.aborted).toBe(true);
    await act(async () => complete(response));
    expect(screen.queryByText("采用分段建议，不采用建议分值")).toBeNull();
    expect(props.onApply).not.toHaveBeenCalled();
  });

  it("替换确认期间调整分段，旧评分预览不能写入新边界草稿", async () => {
    mocks.generate.mockResolvedValue(response);
    const { props } = setup(true);
    fireEvent.click(screen.getByText("AI 建议分值划分"));
    fireEvent.click(screen.getByText("生成分值建议"));
    await screen.findByText("AI 建议 · 待教师核对");
    fireEvent.click(screen.getByText("采用建议到评分细则"));
    const confirmation = mocks.confirm.mock.calls[0][0];
    fireEvent.click(screen.getByText("查看并调整解答拆分"));
    fireEvent.click(screen.getByRole("button", { name: "合并解答单元 1 与下一单元" }));
    await act(async () => confirmation.onOk());
    expect(props.onApply).not.toHaveBeenCalled();
    expect(screen.queryByText("AI 建议 · 待教师核对")).toBeNull();
  });
});
