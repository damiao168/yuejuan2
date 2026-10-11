// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { QuestionBankVersion } from "@edugrade/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportJob, Question } from "../api/papers";
import { ScoringEditor } from "../features/question-bank/ScoringEditor";
import { PaperScoringConfiguration } from "../features/paper-rubric/PaperScoringConfiguration";
import { useRubricEditor } from "../features/paper-rubric/hooks/useRubricEditor";
import type { PaperRubricViewContext } from "../features/paper-rubric/paperRubricViewTypes";
import { RubricReviewEditor } from "../features/paper-import/RubricReviewEditor";
import { usePaperImportWorkflow } from "../features/paper-import/usePaperImportWorkflow";
import { createScoringExample, StructuredListEditor } from "./StructuredTeacherFields";

const mocks = vi.hoisted(() => ({
  updateScoring: vi.fn(), createRubric: vi.fn(), saveReview: vi.fn(), loadConfig: vi.fn(),
  error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn(), saved: vi.fn(), dirty: vi.fn()
}));
vi.mock("antd", async (importOriginal) => ({
  ...await importOriginal<typeof import("antd")>(),
  App: { useApp: () => ({ message: { error: mocks.error, success: mocks.success, warning: mocks.warning, info: mocks.info }, modal: { confirm: vi.fn() } }) }
}));
vi.mock("../api/questionBank", () => ({ questionBankApi: {
  getQuestionBankItem: vi.fn(async () => ({ item: { bank_id: "bank-1" } })),
  getQuestionBank: vi.fn(async () => ({ bank: { school_id: "school-1" } })),
  listQuestionBankRubricTemplates: vi.fn(async () => ({ items: [] })),
  updateQuestionBankScoring: mocks.updateScoring
} }));
vi.mock("../api/papers", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/papers")>(),
  createRubric: mocks.createRubric, savePaperImportReview: mocks.saveReview
}));
vi.mock("./features/assessment/AssessmentProfileEditor", () => ({ AssessmentProfileEditor: () => null }));

const historicalExamples = ["历史字符串示例", { id: "example-old", content: "历史结构示例", legacy_extension: { source: "retained" } }];
const example = { student_answer: "只完成第一步", expected_score: 3.5, reason: "第一步正确，缺少最后结论" };
const points = [{ id: "p1", description: "完成推导", score: 10, required: true }];
const question: Question = {
  id: "question-1", tenant_id: "tenant-1", exam_id: "exam-1", question_no: "1", question_type: "short_answer", score: 10,
  stem: "合成题目", options: [], knowledge_points: [], sort_order: 1, status: "draft",
  rubric: { id: "rubric-1", question_id: "question-1", version: "1", status: "draft", max_score: 10, points, deductions: [], examples: historicalExamples }
};
const version = {
  id: "version-1", tenant_id: "tenant-1", item_id: "item-1", revision: 4, question_type: "short_answer", options: [], default_score: 10,
  scoring: { answer: { standard_answer: "标准结论", equivalent_answers: [], tolerance: {} }, solution: null, assets: [], template_version_id: null, use_policy: "practice_only",
    rubric: { status: "approved", max_score: 10, points, deductions: [], examples: historicalExamples } }
} as unknown as QuestionBankVersion;
const reviewJob = {
  id: "import-1", exam_id: "exam-1", generation: 7, status: "review_required", sources: [],
  updated_at: "2026-10-09T03:00:00.123456Z",
  questions: [{ candidate_id: "candidate-1", question_no: "1", question_type: "short_answer", score: 10, stem: "合成题目",
    rubric: { status: "draft", max_score: 10, points, deductions: [], examples: historicalExamples } }]
} as unknown as PaperImportJob;

// 真实 AntD 表单在全量并行时可能超过默认 5 秒；保留业务断言，并用有限期限发现挂起。
describe("scoring examples use independent Chinese fields and retain historical values", { timeout: 20_000 }, () => {
  let root: Root; let host: HTMLDivElement; let client: QueryClient;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    mocks.updateScoring.mockResolvedValue({ version }); mocks.createRubric.mockResolvedValue({});
    mocks.saveReview.mockImplementation(async (_id: string, _generation: number, questions: PaperImportJob["questions"]) => ({ import: { ...reviewJob, questions, updated_at: "2026-10-09T03:00:00.123457Z" } }));
    mocks.loadConfig.mockResolvedValue(undefined);
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); client.clear(); vi.unstubAllGlobals(); });

  async function click(text: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""));
    expect(button).toBeDefined(); await act(async () => button!.click());
  }
  async function edit(label: string, text: string, index = 0) {
    const element = host.querySelectorAll<HTMLTextAreaElement | HTMLInputElement>(`[aria-label="${label}"]`)[index];
    expect(element).toBeDefined();
    await act(async () => {
      Object.getOwnPropertyDescriptor(element.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(element, text);
      element.dispatchEvent(new Event("input", { bubbles: true })); element.dispatchEvent(new Event("change", { bubbles: true })); element.dispatchEvent(new FocusEvent("blur", { bubbles: true }));
    });
  }
  async function fillExample() {
    await click("添加评分示例"); await edit("学生作答", example.student_answer); await edit("示例得分", String(example.expected_score)); await edit("原因", example.reason);
  }

  // 使用真实表单与保存控制器，只在 API 边界截取请求；不能用测试自己的解析器替代业务序列化。
  it("saves a question-bank example as a numeric score with answer and reason", async () => {
    await act(async () => root.render(<QueryClientProvider client={client}><ScoringEditor version={version} disabled={false} onSaved={mocks.saved} onDirty={mocks.dirty} canManageFiles={false} actorId="teacher-1" /></QueryClientProvider>));
    await fillExample(); await click("保存评分标准");
    expect(mocks.error).not.toHaveBeenCalled(); expect(mocks.updateScoring).toHaveBeenCalledTimes(1);
    expect(mocks.updateScoring).toHaveBeenCalledWith(expect.objectContaining({
      path: { versionId: "version-1" }, body: expect.objectContaining({ expected_revision: 4, scoring: expect.objectContaining({ rubric: expect.objectContaining({ examples: [...historicalExamples, example] }) }) })
    }));
    expect(mocks.saved).toHaveBeenCalledWith(version);
  });

  it("saves paper scoring examples through the real rubric workflow", async () => {
    function PaperEditor() {
      const rubricWorkflow = useRubricEditor({ selectedQuestion: question, selectedExamId: "exam-1", loadConfig: mocks.loadConfig });
      const context = { selectedExam: { id: "exam-1", subject: "math", status: "draft", grading_mode: "ai_assisted" }, selectedQuestion: question,
        options: { canManageAssessment: false }, access: { questionDisabled: false, editorUsesFixedScoring: false, showFormulaEvidence: false }, rubricWorkflow
      } as unknown as PaperRubricViewContext;
      return <><PaperScoringConfiguration context={context} /><button onClick={() => void rubricWorkflow.saveRubric()}>保存测试细则</button></>;
    }
    await act(async () => root.render(<PaperEditor />));
    await act(async () => host.querySelector("details.paper-advanced-settings summary")!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    await fillExample(); await click("保存测试细则");
    expect(mocks.error).not.toHaveBeenCalled();
    expect(mocks.createRubric).toHaveBeenCalledWith("question-1", expect.objectContaining({ examples: [...historicalExamples, example] }));
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1");
  });

  it("saves reviewed import examples without dropping legacy strings or extension fields", async () => {
    function ReviewEditor() {
      const workflow = usePaperImportWorkflow({ canManage: true, selectedExam: { id: "exam-1" } as never, papers: [], paperImports: [reviewJob], loadConfig: mocks.loadConfig });
      const draft = workflow.reviewDrafts[0];
      return <>{draft && <RubricReviewEditor value={draft.rubric} questionScore={draft.score}
        jsonTexts={{ examples: workflow.reviewJsonTexts["0:examples"], deductions: workflow.reviewJsonTexts["0:deductions"] }}
        onJsonTextChange={(field, text, error) => workflow.setReviewJsonText(`0:${field}`, text, error)}
        onChange={rubric => workflow.updateReviewDraft(0, "rubric", { rubric })} />}
        <button onClick={() => void workflow.saveImportReview(reviewJob, 0)}>确认并保存测试题</button></>;
    }
    await act(async () => root.render(<ReviewEditor />)); await fillExample(); await click("确认并保存测试题");
    expect(mocks.error).not.toHaveBeenCalled(); expect(mocks.saveReview).toHaveBeenCalledTimes(1);
    expect(mocks.saveReview).toHaveBeenCalledWith("import-1", 7, [expect.objectContaining({ candidate_id: "candidate-1", rubric: expect.objectContaining({ examples: [...historicalExamples, example] }) })], reviewJob.updated_at);
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true, propagateError: true });
  });

  it("keeps two newly added examples independent and preserves untouched historic items", async () => {
    let serialized = "";
    function List() {
      const [value, setValue] = useState(JSON.stringify(historicalExamples)); serialized = value;
      return <StructuredListEditor value={value} itemLabel="评分示例" newItem={createScoringExample()} onChange={setValue} />;
    }
    await act(async () => root.render(<List />)); await fillExample(); await click("添加评分示例");
    await edit("学生作答", "第二份作答", 1); await edit("示例得分", "7", 1); await edit("原因", "第二份说明", 1);
    expect(JSON.parse(serialized)).toEqual([...historicalExamples, example, { student_answer: "第二份作答", expected_score: 7, reason: "第二份说明" }]);
    expect(host.textContent).not.toContain("student_answer"); expect(host.textContent).not.toContain("expected_score");
  });
});
