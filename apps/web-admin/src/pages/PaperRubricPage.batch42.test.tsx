// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { Question } from "../api/papers";
import type { PaperRubricViewContext } from "../features/paper-rubric/paperRubricViewTypes";
import { PaperRubricPage } from "./PaperRubricPage";

const mocks = vi.hoisted(() => ({
  config: {} as Record<string, unknown>, importState: {} as Record<string, unknown>,
  viewContext: null as PaperRubricViewContext | null,
  loadConfig: vi.fn(), loadExams: vi.fn(), updateQuestion: vi.fn(), createQuestion: vi.fn(), deleteQuestion: vi.fn(),
  createRubric: vi.fn(), listScoringRules: vi.fn(), listQuestionRubricDraftJobs: vi.fn(), fillRecognizedContent: vi.fn(),
  createScoringRule: vi.fn(), updateScoringRule: vi.fn(), publishScoringRule: vi.fn(),
  warning: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn(), confirm: vi.fn(),
}));

vi.mock("antd", async importOriginal => {
  const original = await importOriginal<typeof import("antd")>();
  const { withFixedTextAreaLayout } = await import("../test-utils/antdTextAreaLayout");
  const app = { message: { warning: mocks.warning, success: mocks.success, error: mocks.error, info: mocks.info }, modal: { confirm: mocks.confirm } };
  return { ...withFixedTextAreaLayout(original), App: { useApp: () => app } };
});
vi.mock("../api/papers", () => ({
  validatePaperConfig: vi.fn(), updateQuestion: mocks.updateQuestion, createQuestion: mocks.createQuestion,
  deleteQuestion: mocks.deleteQuestion, createRubric: mocks.createRubric, listScoringRules: mocks.listScoringRules,
  listQuestionRubricDraftJobs: mocks.listQuestionRubricDraftJobs, createQuestionRubricDraftJob: vi.fn(),
  getQuestionRubricDraftJob: vi.fn(), cancelQuestionRubricDraftJob: vi.fn(),
  createScoringRule: mocks.createScoringRule, updateScoringRule: mocks.updateScoringRule, publishScoringRule: mocks.publishScoringRule,
}));
vi.mock("../features/paper-import/usePaperConfigData", () => ({ usePaperConfigData: () => {
  const [selectedQuestionId, setSelectedQuestionId] = useState<string | null>("question-1");
  const [editorMode, setEditorMode] = useState<"create" | "edit">("edit");
  return { ...mocks.config, selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode };
} }));
vi.mock("../features/paper-import/usePaperImportWorkflow", () => ({ usePaperImportWorkflow: () => mocks.importState }));
vi.mock("../features/paper-import/PaperImportWorkspace", () => ({ PaperImportWorkspace: () => null }));
vi.mock("../features/paper-import/PaperStructurePanel", () => ({ PaperStructurePanel: () => null }));
vi.mock("../features/paper-import/PaperImportReviewPanel", () => ({ PaperImportReviewPanel: () => null }));
vi.mock("../components/features/assessment/AssessmentProfileEditor", () => ({ AssessmentProfileEditor: () => null }));
vi.mock("../features/question-bank/BankQuestionPicker", () => ({ BankQuestionPicker: () => null }));
vi.mock("../features/question-bank/HistoryQuestionImporter", () => ({ HistoryQuestionImporter: () => null }));
vi.mock("../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: string }>) => <div>{children}</div> }));
vi.mock("../features/paper-rubric/PaperRubricView", async importOriginal => {
  const original = await importOriginal<typeof import("../features/paper-rubric/PaperRubricView")>();
  return { ...original, PaperRubricView: ({ context }: Readonly<{ context: PaperRubricViewContext }>) => {
    mocks.viewContext = context;
    return <original.PaperRubricView context={context} />;
  } };
});

function question(type = "short_answer", id = "question-1", score = 5): Question {
  return {
    id, tenant_id: "synthetic-school", exam_id: "exam-1", question_no: "1", question_type: type,
    score, stem: "synthetic stem", options: ["甲", "乙"], knowledge_points: [], sort_order: 1, status: "draft",
    answer_key: { id: `answer-${id}`, question_id: id, answer_version: "1", standard_answer: type === "multiple_choice" ? ["A"] : "synthetic answer", equivalent_answers: [], tolerance: {} },
    rubric: { id: `rubric-${id}`, question_id: id, version: "1", status: "draft", max_score: score, points: [{ id: "point-1", description: "synthetic point", score, required: true }], deductions: [], examples: [] },
  };
}

// V8 插桩下，jsdom 计算真实 AntD 自动高度样式耗时较长；仅覆盖率模式沿用同页测试的 60 秒上限。
// 普通回归仍限 20 秒，保存顺序、部分成功回执和评分配置的断言保持不变。
const testTimeout = process.env.EDUGRADE_NATIVE_COVERAGE === "1" ? 60_000 : 20_000;
describe("paper rubric page batch42 retry and changed scoring type", { timeout: testTimeout }, () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.resetAllMocks();
    mocks.viewContext = null;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })) });
    mocks.config = {
      exams: [{ id: "exam-1", subject: "math", status: "draft", total_score: 10, name: "synthetic exam" } as Exam],
      selectedExamId: "exam-1", setSelectedExamId: vi.fn(), papers: [], paperImports: [], questions: [question()],
      loading: false, configLoading: false, error: null, configError: null, validation: null,
      setValidation: vi.fn(), loadConfig: mocks.loadConfig, loadExams: mocks.loadExams,
    };
    mocks.importState = { reviewDrafts: [], reviewDirty: false, fillingImportContent: false, contentFillResult: null,
      fillRecognizedContent: mocks.fillRecognizedContent, savingImportReview: false };
    mocks.loadConfig.mockResolvedValue(undefined);
    mocks.updateQuestion.mockResolvedValue({});
    mocks.createQuestion.mockResolvedValue({ question: { id: "new-question" } });
    mocks.createRubric.mockResolvedValue({});
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [] });
    mocks.listQuestionRubricDraftJobs.mockResolvedValue({ jobs: [], concurrency: 5 });
    mocks.createScoringRule.mockResolvedValue({ scoring_rule: { id: "new-rule", status: "draft", revision: 1, config: {} } });
    mocks.fillRecognizedContent.mockResolvedValue(undefined);
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  async function render(canManage = true) {
    await act(async () => root.render(<PaperRubricPage canManage={canManage} canManageAssessment={false} mode="embedded" initialView="questions" fixedExamId="exam-1" />));
  }

  async function click(label: string) {
    const button = [...host.querySelectorAll("button")].find(item => item.textContent?.includes(label));
    expect(button).toBeDefined();
    await act(async () => button!.click());
  }

  async function edit(selector: string, value: string) {
    const input = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector);
    expect(input).not.toBeNull();
    await act(async () => {
      const prototype = input!.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
      input!.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("updates the already created question when content changes after a scoring failure", async () => {
    await render();
    await click("新建");
    await edit("input#question_no", "2");
    await edit("textarea#stem", "first created content");
    await edit('textarea[aria-label="标准答案"]', "new answer");
    mocks.createRubric.mockRejectedValueOnce(new Error("synthetic scoring failure"));
    await click("保存本题全部修改");
    expect(mocks.createQuestion).toHaveBeenCalledTimes(1);
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(host.textContent).toContain("题目与答案已保存；评分配置尚未全部保存");
    // 回执只允许同快照跳过写入；修改快照后仍须复用首次创建的编号来更新。
    await edit("textarea#stem", "changed retry content");
    mocks.loadConfig.mockImplementation(async () => { mocks.config.questions = [question(), question("short_answer", "new-question", 10)]; });
    await click("保存本题全部修改");
    expect(mocks.createQuestion).toHaveBeenCalledTimes(1);
    expect(mocks.updateQuestion).toHaveBeenCalledExactlyOnceWith("new-question", expect.objectContaining({ stem: "changed retry content" }));
    expect(mocks.createRubric).toHaveBeenCalledTimes(2);
    expect(mocks.createRubric).toHaveBeenLastCalledWith("new-question", expect.objectContaining({ max_score: 10 }));
    expect(mocks.updateQuestion.mock.invocationCallOrder[0]).toBeLessThan(mocks.createRubric.mock.invocationCallOrder[1]);
    expect(mocks.loadConfig).toHaveBeenCalledExactlyOnceWith("exam-1", { silent: true, propagateError: true });
    expect(host.textContent).toContain("本题题目与答案、已修改的评分配置全部保存");
  });

  it("does not finish an old whole-save receipt after the externally selected question changes during reread", async () => {
    mocks.config.questions = [question(), question("short_answer", "question-2")];
    let finishRead!: () => void;
    mocks.loadConfig.mockImplementationOnce(() => new Promise<void>(resolve => { finishRead = resolve; }));
    await render();
    await edit("textarea#stem", "saved first question content");
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).toHaveBeenCalledExactlyOnceWith("question-1", expect.objectContaining({ stem: "saved first question content" }));
    expect(mocks.loadConfig).toHaveBeenCalledExactlyOnceWith("exam-1", { silent: true, propagateError: true });
    expect(mocks.viewContext!.state.saveBusy).toBe(true);
    expect(mocks.viewContext!.state.saveRefreshPending).toBe(true);
    // 模拟配置在回读期间外部换题，不能通过被保存锁拦住的普通换题按钮构造此边界。
    await act(async () => { mocks.viewContext!.config.setSelectedQuestionId("question-2"); });
    await act(async () => { finishRead(); });
    expect(mocks.viewContext!.selectedQuestion?.id).toBe("question-2");
    expect(mocks.viewContext!.state.allSaveCompleted).toBe(false);
    expect(mocks.viewContext!.state.allSaveMessage).toBeNull();
    expect(mocks.viewContext!.state.saveRefreshPending).toBe(false);
    expect(mocks.viewContext!.state.saveBusy).toBe(false);
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(1);
    expect(mocks.createQuestion).not.toHaveBeenCalled();
    expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.publishScoringRule).not.toHaveBeenCalled();
  });

  it.each(["评分草稿变化", "题目答案草稿变化", "切换题目", "权限变为只读", "题目来源更新", "页面卸载"])("锁定确认期间%s，迟到确认不能写入", async boundary => {
    mocks.config.questions = [question(), question("short_answer", "question-2")];
    await render(); await click("评分配置"); await click("确认并锁定细则");
    const confirmation = mocks.confirm.mock.lastCall![0];
    if (boundary === "评分草稿变化") await edit(".paper-rubric-description textarea", "确认期间修改的条件");
    if (boundary === "题目答案草稿变化") await edit("textarea#stem", "确认期间修改题目");
    if (boundary === "切换题目") await act(async () => { mocks.viewContext!.config.setSelectedQuestionId("question-2"); });
    if (boundary === "权限变为只读") await render(false);
    if (boundary === "题目来源更新") {
      mocks.config.questions = (mocks.config.questions as Question[]).map(item => item.id === "question-1" ? { ...item, stem: "新版本题干" } : item);
      await render();
    }
    if (boundary === "页面卸载") {
      await act(async () => root.unmount());
      root = createRoot(host);
    }
    await act(async () => { await confirmation.onOk(); });
    expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it.each([
    { original: "short_answer", next: "multiple_choice", region: "rubric" },
    { original: "multiple_choice", next: "short_answer", region: "scoring" },
  ])("rejects changed type $original to $next while the original $region draft is dirty", async ({ original, next, region }) => {
    mocks.config.questions = [question(original)];
    await render();
    // 保留真实表单、评分 hooks 与保存按钮；直接调用已有编辑动作只用于构造跨题型草稿。
    await act(async () => {
      const context = mocks.viewContext!;
      if (region === "rubric") context.rubricWorkflow.updatePoint(0, { description: "retained subjective draft" });
      else context.scoringRuleWorkflow.setRuleConfig("score_per_correct_option", 1);
      context.questionWorkflow.form.setFieldsValue({ question_type: next });
      context.actions.markQuestionDirty();
    });
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(mocks.createQuestion).not.toHaveBeenCalled();
    expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.createScoringRule).not.toHaveBeenCalled();
    expect(mocks.updateScoringRule).not.toHaveBeenCalled();
    expect(mocks.publishScoringRule).not.toHaveBeenCalled();
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    expect(host.textContent).toContain("本题尚未保存");
    expect(mocks.viewContext!.questionWorkflow.form.getFieldValue("question_type")).toBe(next);
    if (region === "rubric") {
      expect(mocks.viewContext!.rubricWorkflow.rubricDirty).toBe(true);
      expect(mocks.viewContext!.rubricWorkflow.rubricPoints[0].description).toBe("retained subjective draft");
    } else {
      expect(mocks.viewContext!.scoringRuleWorkflow.scoringRuleDirty).toBe(true);
      expect(mocks.viewContext!.scoringRuleWorkflow.scoringRuleConfig.score_per_correct_option).toBe(1);
    }
  });
});
