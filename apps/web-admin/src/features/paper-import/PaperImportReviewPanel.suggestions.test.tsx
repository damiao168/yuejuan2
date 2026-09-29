// @vitest-environment jsdom
import { act, type ButtonHTMLAttributes } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportDraftQuestion, PaperImportJob, SuggestedRubricCandidate } from "../../api/papers";
import { PaperImportReviewPanel } from "./PaperImportReviewPanel";

const mocks = vi.hoisted(() => ({ generate: vi.fn(), warning: vi.fn(), error: vi.fn(), confirm: vi.fn() }));
vi.mock("../../api/papers", () => ({ generatePaperImportRubricDraft: mocks.generate }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <div>{children}</div> }));
vi.mock("./RubricReviewEditor", () => ({ RubricReviewEditor: () => <div>评分细则编辑器</div> }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { warning: mocks.warning, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
  Button: ({ loading: _loading, type: _type, size: _size, icon, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { loading?: boolean; size?: string; icon?: unknown }) => <button {...props}>{children}</button>,
  Input: Object.assign(() => null, { TextArea: () => null }),
  InputNumber: () => null, Pagination: () => null, Select: () => null,
  Tag: ({ children }: { children: string }) => <span>{children}</span>
}));

const draft: PaperImportDraftQuestion = {
  candidate_id: "q1", question_no: "15", question_type: "calculation", score: 5, stem: "求导", source_refs: [], knowledge_points: [], confidence: 1, issues: [],
  solution: { raw_text: "教师解析", steps: [{ step_no: 1, content: "正确求导" }], source_refs: [{ source_id: "s1", file_asset_id: "f1", document_index: 0 }] }
};
const suggestion: SuggestedRubricCandidate = {
  candidate_id: "q1", max_score: 5, status: "review_required", origin: "ai_suggestion_from_solution", issues: [],
  points: [{ id: "p1", description: "正确求导", suggested_score: 5, evidence_step_ids: ["step-1"], source_refs: [], review_note: "需教师核对" }]
};
const job = { id: "import-1", generation: 7, updated_at: "2026-09-26T08:00:00Z", subject: "mathematics", questions: [draft], question_candidates: [], rubric_candidates: [] } as unknown as PaperImportJob;

describe("math rubric suggestion review", () => {
  let root: Root;
  let container: HTMLDivElement;
  let onChange: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    onChange = vi.fn();
    mocks.generate.mockResolvedValue({ suggested_rubric_candidates: [suggestion] });
  });
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
  async function render(importJob = job, reviewDraft = draft) {
    await act(async () => { root.render(<PaperImportReviewPanel job={importJob} drafts={[reviewDraft]} onChange={onChange} onOpenSource={() => undefined} />); });
  }
  function button(text: string) {
    const element = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
    expect(element).toBeDefined();
    return element!;
  }
  async function click(text: string) { await act(async () => { button(text).click(); }); }

  it("requires an explicit copy before AI suggestions alter the import draft", async () => {
    await render();
    await click("根据教师解析生成评分点建议");
    expect(mocks.generate).toHaveBeenCalledWith("import-1", "q1", 7, "2026-09-26T08:00:00Z");
    expect(onChange).not.toHaveBeenCalled();
    expect(container.textContent).toContain("AI 建议 · 待教师核对");
    await click("复制建议到评分细则");
    expect(onChange).toHaveBeenCalledWith(0, "rubric", expect.objectContaining({ rubric: expect.objectContaining({ status: "draft", max_score: 5 }) }));
  });

  it("keeps points without a score uncopyable", async () => {
    mocks.generate.mockResolvedValueOnce({ suggested_rubric_candidates: [{ ...suggestion, points: [{ ...suggestion.points[0], suggested_score: null }] }] });
    await render();
    await click("根据教师解析生成评分点建议");
    expect(button("复制建议到评分细则").disabled).toBe(true);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("ignores a suggestion completed after the import generation changes", async () => {
    let resolve!: (result: unknown) => void;
    mocks.generate.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    await render();
    await click("根据教师解析生成评分点建议");
    await render({ ...job, generation: 8 });
    await act(async () => { resolve({ suggested_rubric_candidates: [suggestion] }); });
    expect(container.textContent).not.toContain("AI 建议 · 待教师核对");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("ignores a suggestion completed after a review save in the same generation", async () => {
    let resolve!: (result: unknown) => void;
    mocks.generate.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    await render();
    await click("根据教师解析生成评分点建议");
    await render({ ...job, updated_at: "2026-09-26T08:01:00Z" });
    await act(async () => { resolve({ suggested_rubric_candidates: [suggestion] }); });
    expect(container.textContent).not.toContain("AI 建议 · 待教师核对");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("rejects a late replacement confirmation from the previous generation", async () => {
    const existingRubricDraft = { ...draft, rubric: { status: "draft", max_score: 5, points: [{ id: "old", description: "原评分点", score: 5, required: true }], deductions: [], examples: [] } };
    await render({ ...job, questions: [existingRubricDraft] }, existingRubricDraft);
    await click("根据教师解析生成评分点建议");
    await click("复制建议到评分细则");
    const confirmation = mocks.confirm.mock.calls[0][0] as { onOk: () => void };
    await render({ ...job, generation: 8 });
    act(() => confirmation.onOk());
    expect(onChange).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalled();
  });

  it("requires saving a changed teacher solution before generating from it", async () => {
    await render(job, { ...draft, solution: { ...draft.solution!, raw_text: "更正后的解析" } });
    expect(button("根据教师解析生成评分点建议").disabled).toBe(true);
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it("does not display a suggestion belonging to another candidate", async () => {
    mocks.generate.mockResolvedValueOnce({ suggested_rubric_candidates: [{ ...suggestion, candidate_id: "q2" }] });
    await render();
    await click("根据教师解析生成评分点建议");
    expect(container.textContent).not.toContain("AI 建议 · 待教师核对");
    expect(mocks.warning).toHaveBeenCalled();
  });
});
