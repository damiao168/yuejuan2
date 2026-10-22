// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportDraftQuestion, PaperImportJob, PaperImportSourceRef } from "../../api/papers";
import { PaperImportResults } from "./PaperImportResults";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

vi.mock("antd", () => ({
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <aside>{message}{description}</aside>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Progress: () => <progress />,
  Pagination: ({ current, pageSize, total, onChange }: { current: number; pageSize: number; total: number; onChange: (page: number) => void }) => <div><button disabled={current === 1} onClick={() => onChange(current - 1)}>上一页</button><button disabled={current * pageSize >= total} onClick={() => onChange(current + 1)}>下一页</button></div>
}));
vi.mock("./PaperImportReviewPanel", () => ({ PaperImportReviewPanel: () => <div>可编辑识别草稿</div> }));

const sourceRef: PaperImportSourceRef = { source_id: "source-1", file_asset_id: "file-1", document_index: 0, page_no: 2 };
function question(overrides: Partial<PaperImportDraftQuestion> = {}): PaperImportDraftQuestion {
  return { candidate_id: "q1", question_no: "1", question_type: "numeric", score: 5, stem: "计算 $x^2$", options: ["A. 0", "B. 4"], source_refs: [sourceRef], knowledge_points: [], confidence: .9, issues: [],
    answer_key: { standard_answer: 0, equivalent_answers: [], tolerance: {} },
    solution: { raw_text: "", steps: [{ step_no: 1, content: "代入 $x=0$ 得到 $x^2=0$" }], source_refs: [sourceRef] }, ...overrides };
}
function job(overrides: Partial<PaperImportJob> = {}): PaperImportJob {
  return { id: "import-1", exam_id: "exam-1", exam_paper_id: "paper-1", paper_file_asset_id: "file-1", answer_file_asset_id: "", generation: 7, structure_confirmed_generation: 7,
    status: "review_required", run_id: "run-1", source_revision: "r1", subject: "mathematics", created_at: "2026-10-12T00:00:00Z", sources: [{ id: "source-1", file_asset_id: "file-1", document_index: 0, role_hint: "auto", detected_role: "mixed", processing_status: "processed", role_confidence: .9, original_name: "试卷.pdf" }],
    questions: [question()], question_candidates: [], answer_candidates: [], solution_candidates: [], rubric_candidates: [], structured_issues: [], issues: [], ...overrides };
}

describe("retained paper import recognition results", () => {
  let root: Root;
  let container: HTMLDivElement;
  let workflow: ReturnType<typeof usePaperImportWorkflow>;
  const onReview = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    workflow = { reviewDrafts: [question({ stem: "未保存本地修改" })], openImportSource: vi.fn(), saveImportReview: vi.fn(), confirmPaperImport: vi.fn(), updateReviewDraft: vi.fn(), replaceImportSources: vi.fn(), retryImportParse: vi.fn() } as unknown as ReturnType<typeof usePaperImportWorkflow>;
  });
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
  async function mount(activeJob = job()) { await act(async () => root.render(<PaperImportResults job={activeJob} workflow={workflow} onReview={onReview} />)); }
  async function click(label: string) {
    const button = Array.from(container.querySelectorAll("button")).find((item) => item.textContent === label);
    expect(button, label).toBeDefined();
    await act(async () => button!.click());
  }

  it("keeps the adopted generation visible as a read-only saved recognition snapshot", async () => {
    await mount();
    expect(container.textContent).toContain("试卷结构已采用");
    expect(container.querySelector("article[aria-label='第 1 题识别结果']")).not.toBeNull();
    expect(container.textContent).toContain("5 分");
    expect(container.textContent).toContain("识别答案0");
    expect(container.textContent).toContain("代入");
    expect(container.querySelector(".katex")).not.toBeNull();
    expect(container.textContent).toContain("当前考试的答案、分值和正式评分配置以小题与分值页面为准");
    expect(container.textContent).not.toContain("未保存本地修改");
    expect(container.textContent).not.toContain("可编辑识别草稿");
    expect(container.textContent).not.toContain("保存草稿");
    await click("查看来源 · 试卷.pdf · 第 2 页");
    expect(workflow.openImportSource).toHaveBeenCalledWith("file-1", 2);
    await click("核对小题与分值");
    expect(onReview).toHaveBeenCalledOnce();
    expect(workflow.saveImportReview).not.toHaveBeenCalled();
    expect(workflow.confirmPaperImport).not.toHaveBeenCalled();
    expect(workflow.replaceImportSources).not.toHaveBeenCalled();
  });

  it.each([false, ["A", "C"], { value: 0 }])("preserves a structured or false answer after the import is applied: %j", async (answer) => {
    await mount(job({ status: "applied", questions: [question({ answer_key: { standard_answer: answer, equivalent_answers: [], tolerance: {} }, rubric: { status: "draft", max_score: 5, points: [{ id: "p1", description: "列式正确", score: 5, required: true }], deductions: [], examples: [] } })] }));
    expect(container.textContent).toContain("识别答案");
    expect(container.textContent).not.toContain("未识别到答案");
    if (answer === false) expect(container.textContent).toContain("false");
    else expect(container.querySelector("code.language-json")?.textContent).toContain(Array.isArray(answer) ? '"C"' : '"value": 0');
    expect(container.textContent).toContain("列式正确（5 分）");
    expect(container.textContent).toContain("标准满分：5");
    expect(container.textContent).not.toContain("可编辑识别草稿");
  });

  it("pages a long snapshot and resets the page when the recognition generation changes", async () => {
    const questions = Array.from({ length: 7 }, (_, index) => question({ candidate_id: `q${index + 1}`, question_no: String(index + 1), stem: `识别题干 ${index + 1}` }));
    await mount(job({ questions }));
    expect(container.querySelectorAll("article")).toHaveLength(5);
    expect(container.textContent).not.toContain("识别题干 6");
    await click("下一页");
    expect(container.querySelectorAll("article")).toHaveLength(2);
    expect(container.textContent).toContain("识别题干 6");
    await mount(job({ generation: 8, structure_confirmed_generation: 8, questions }));
    expect(container.querySelectorAll("article")).toHaveLength(5);
    expect(container.textContent).toContain("识别题干 1");
    expect(container.textContent).not.toContain("识别题干 6");
  });

  it("shows unpaired candidates separately rather than inventing associations with a question", async () => {
    await mount(job({ questions: [], answer_candidates: [{ candidate_id: "a1", standard_answer: false, equivalent_answers: [], confidence: .9, source_refs: [sourceRef], issues: [] }] }));
    expect(container.textContent).toContain("尚未形成题目草稿的识别内容");
    expect(container.textContent).toContain("答案 （题号待核对）");
    expect(container.textContent).toContain("false");
    expect(container.textContent).not.toContain("未保存本地修改");
    expect(container.querySelector("article[aria-label='第 1 题识别结果']")).toBeNull();
  });

  it("makes an empty saved snapshot explicit and leaves an unconfirmed generation editable", async () => {
    await mount(job({ questions: [] }));
    expect(container.textContent).toContain("暂无可展示的识别结果");
    await mount(job({ generation: 8 }));
    expect(container.textContent).toContain("可编辑识别草稿");
    expect(container.textContent).not.toContain("本轮识别快照");
    expect(container.querySelector("article")).toBeNull();
  });

  it("displays a failed empty parse instead of leaving an empty results section", async () => {
    workflow.reviewDrafts = [];
    await mount(job({ status: "failed", structure_confirmed_generation: undefined, questions: [], error_code: "ai_parse_failed", issues: ["识别服务暂不可用"] }));
    expect(container.textContent).toContain("暂无可展示的题目识别结果");
    expect(container.textContent).toContain("请查看失败提示和资料来源");
    expect(workflow.retryImportParse).not.toHaveBeenCalled();
  });
});
