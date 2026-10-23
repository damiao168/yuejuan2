// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { PaperImportJob, Question, RubricPayload } from "../api/papers";
import type { PaperRubricViewContext } from "../features/paper-rubric/paperRubricViewTypes";
import { PaperRubricPage } from "./PaperRubricPage";

const mocks = vi.hoisted(() => ({
  config: {} as Record<string, unknown>, importState: {} as Record<string, unknown>,
  loadConfig: vi.fn(), loadExams: vi.fn(), updateQuestion: vi.fn(), createQuestion: vi.fn(), deleteQuestion: vi.fn(),
  createRubric: vi.fn(), generateQuestionRubricDraft: vi.fn(), createQuestionRubricDraftJob: vi.fn(), listQuestionRubricDraftJobs: vi.fn(), getQuestionRubricDraftJob: vi.fn(), cancelQuestionRubricDraftJob: vi.fn(), listScoringRules: vi.fn(), fillRecognizedContent: vi.fn(), confirm: vi.fn(),
  warning: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn(),
  createScoringRule: vi.fn(), updateScoringRule: vi.fn(), publishScoringRule: vi.fn(), listQuestions: vi.fn(),
  context: null as PaperRubricViewContext | null
}));

vi.mock("antd", async (importOriginal) => {
  const original = await importOriginal<typeof import("antd")>();
  const { withFixedTextAreaLayout } = await import("../test-utils/antdTextAreaLayout");
  const app = { message: { warning: mocks.warning, success: mocks.success, error: mocks.error, info: mocks.info }, modal: { confirm: mocks.confirm } };
  return { ...withFixedTextAreaLayout(original), App: { useApp: () => app } };
});
vi.mock("../api/papers", () => ({
  validatePaperConfig: vi.fn(), updateQuestion: mocks.updateQuestion, createQuestion: mocks.createQuestion,
  deleteQuestion: mocks.deleteQuestion, createRubric: mocks.createRubric, generateQuestionRubricDraft: mocks.generateQuestionRubricDraft, listScoringRules: mocks.listScoringRules, listQuestions: mocks.listQuestions,
  createQuestionRubricDraftJob: mocks.createQuestionRubricDraftJob, listQuestionRubricDraftJobs: mocks.listQuestionRubricDraftJobs,
  getQuestionRubricDraftJob: mocks.getQuestionRubricDraftJob, cancelQuestionRubricDraftJob: mocks.cancelQuestionRubricDraftJob,
  createScoringRule: mocks.createScoringRule, publishScoringRule: mocks.publishScoringRule, updateScoringRule: mocks.updateScoringRule
}));
vi.mock("../features/paper-import/usePaperConfigData", () => ({
  usePaperConfigData: () => {
    const [selectedQuestionId, setSelectedQuestionId] = useState<string | null>("question-1");
    const [editorMode, setEditorMode] = useState<"create" | "edit">("edit");
    return { ...mocks.config, selectedQuestionId, setSelectedQuestionId, editorMode, setEditorMode };
  }
}));
vi.mock("../features/paper-import/usePaperImportWorkflow", () => ({ usePaperImportWorkflow: () => mocks.importState }));
vi.mock("../features/paper-import/PaperImportWorkspace", () => ({ PaperImportWorkspace: () => <div>synthetic material workspace</div> }));
vi.mock("../features/paper-import/PaperStructurePanel", () => ({ PaperStructurePanel: () => <div>synthetic structure panel</div> }));
vi.mock("../features/paper-import/PaperImportReviewPanel", () => ({ PaperImportReviewPanel: () => <div>synthetic candidate review</div> }));
vi.mock("../components/features/assessment/AssessmentProfileEditor", () => ({ AssessmentProfileEditor: () => <div>synthetic assessment editor</div> }));
vi.mock("../features/question-bank/BankQuestionPicker", () => ({ BankQuestionPicker: () => <button>从学校题库选题</button> }));
vi.mock("../features/question-bank/HistoryQuestionImporter", () => ({ HistoryQuestionImporter: () => <button>将本次考试题目精选入题库</button> }));
vi.mock("../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: string }>) => <div>{children}</div> }));
vi.mock("../features/paper-rubric/PaperRubricView", async (importOriginal) => {
  const original = await importOriginal<typeof import("../features/paper-rubric/PaperRubricView")>();
  return { ...original, PaperRubricView: ({ context }: Readonly<{ context: PaperRubricViewContext }>) => {
    mocks.context = context;
    return <original.PaperRubricView context={context} />;
  } };
});

function question(id: string, answer: unknown = "synthetic answer"): Question {
  return {
    id, tenant_id: "synthetic-school", exam_id: "exam-1", question_no: id === "question-1" ? "1" : "2", question_type: "short_answer",
    score: 5, stem: `synthetic stem ${id}`, options: [], knowledge_points: [], sort_order: 1, status: "draft",
    answer_key: { id: `answer-${id}`, question_id: id, answer_version: "1", standard_answer: answer, equivalent_answers: [], tolerance: {} },
    rubric: { id: `rubric-${id}`, question_id: id, version: "1", status: "draft", max_score: 5, points: [{ id: "point-1", description: "synthetic point", score: 5, required: true }], deductions: [], examples: [] }
  };
}

// 真实 AntD 表单采集覆盖率时耗时更长；普通回归仍限 20 秒，不放宽保存事实断言。
const testTimeout = process.env.EDUGRADE_NATIVE_COVERAGE === "1" ? 60_000 : 20_000;
// 顶部字段回归包含真实 Select 弹层和多轮必填校验，单独给足交互时间，保存及草稿断言不放宽。
const headerInteractionTimeout = Math.max(testTimeout, 30_000);
describe("paper rubric page state and draft boundaries", { timeout: testTimeout }, () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })) });
    mocks.config = {
      exams: [{ id: "exam-1", subject: "math", status: "draft", total_score: 10, name: "synthetic exam" } as Exam],
      selectedExamId: "exam-1", setSelectedExamId: vi.fn(), papers: [], paperImports: [],
      questions: [question("question-1"), question("question-2")], loading: false, configLoading: false,
      error: null, configError: null, validation: null, setValidation: vi.fn(), loadConfig: mocks.loadConfig, loadExams: mocks.loadExams
    };
    mocks.importState = { reviewDrafts: [], reviewDirty: false, fillingImportContent: false, contentFillResult: null,
      fillRecognizedContent: mocks.fillRecognizedContent, savingImportReview: false };
    mocks.loadConfig.mockResolvedValue(undefined);
    mocks.updateQuestion.mockResolvedValue({});
    mocks.createRubric.mockResolvedValue({});
    mocks.generateQuestionRubricDraft.mockReset();
    mocks.listQuestionRubricDraftJobs.mockResolvedValue({ jobs: [], concurrency: 5 });
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [] });
    mocks.listQuestions.mockResolvedValue({ questions: mocks.config.questions });
    mocks.createScoringRule.mockResolvedValue({ scoring_rule: { id: "new-rule", question_id: "question-1", status: "draft", revision: 1, config: {} } });
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
    const button = [...host.querySelectorAll("button"), ...document.querySelectorAll<HTMLButtonElement>(".ant-drawer button")].find((item) => item.textContent?.includes(label));
    if (!button) throw new Error(`Missing button: ${label}`);
    await act(async () => button.click());
  }

  async function editStem(value: string) {
    const field = host.querySelector<HTMLTextAreaElement>("textarea#stem");
    if (!field) throw new Error("Missing stem field");
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set?.call(field, value);
      field.dispatchEvent(new Event("input", { bubbles: true }));
    });
    return field;
  }

  async function editInput(selector: string, value: string) {
    const input = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector) ?? document.querySelector<HTMLInputElement | HTMLTextAreaElement>(`.ant-drawer ${selector}`);
    if (!input) throw new Error(`Missing field ${selector}`);
    await act(async () => {
      Object.getOwnPropertyDescriptor(input.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function chooseAI() {
    const choice = [...host.querySelectorAll("label")].find(label => label.textContent?.includes("AI 建议分值划分"));
    if (!choice) throw new Error("Missing AI scoring choice");
    await act(async () => choice.click());
  }

  function aiSelected() {
    return [...host.querySelectorAll("label")].find(label => label.textContent?.includes("AI 建议分值划分"))?.querySelector<HTMLInputElement>("input")?.checked;
  }

  function saveRubricOnRead() {
    mocks.createRubric.mockResolvedValue({ rubric: { version: "2" } });
    mocks.loadConfig.mockImplementation(async () => {
      const payload = mocks.createRubric.mock.lastCall?.[1] as RubricPayload;
      mocks.config.questions = (mocks.config.questions as Question[]).map(item => item.id === "question-1"
        ? { ...item, rubric: { ...item.rubric!, ...structuredClone(payload), version: "2" } } : item);
    });
  }

  it("顶部只保留一份题号题型分值，跨标签保持同一字段和草稿并保存最新值", async () => {
    await render();
    const heading = host.querySelector(".question-editor .paper-editor-sticky-head");
    expect(heading).not.toBeNull();
    const fields = ["question_no", "question_type", "score"].map(name => {
      const matches = host.querySelectorAll<HTMLInputElement>(`#${name}`);
      expect(matches).toHaveLength(1);
      expect(heading?.contains(matches[0])).toBe(true);
      return matches[0];
    });
    expect(fields[2].closest(".paper-question-score-target")).not.toBeNull();
    expect(host.querySelector(".paper-question-content .paper-question-basics")).toBeNull();
    await editInput("input#question_no", "15");
    await editInput("input#score", "7");
    const typeSelector = fields[1].closest(".ant-select-selector");
    expect(typeSelector).not.toBeNull();
    await act(async () => typeSelector?.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    const calculation = [...document.querySelectorAll<HTMLElement>(".ant-select-item-option")].find(option => option.textContent === "计算题");
    expect(calculation).toBeDefined();
    await act(async () => calculation?.click());
    expect(mocks.context!.state.formDirty).toBe(true);
    await click("评分配置");
    fields.forEach(field => expect(host.querySelector(`#${field.id}`)).toBe(field));
    expect(heading?.closest('[style*="display: none"]')).toBeNull();
    expect(mocks.context!.questionWorkflow.form.getFieldsValue()).toMatchObject({ question_no: "15", question_type: "calculation", score: 7 });
    expect(mocks.context!.state.formDirty).toBe(true);
    await click("题目与答案");
    fields.forEach(field => expect(host.querySelector(`#${field.id}`)).toBe(field));
    await click("保存题目与答案");
    expect(mocks.updateQuestion).toHaveBeenCalledExactlyOnceWith("question-1", expect.objectContaining({ question_no: "15", question_type: "calculation", score: 7 }));
  }, headerInteractionTimeout);

  it("新建题目的顶部字段仍执行必填校验，缺少任一字段都不创建题目", async () => {
    await render(); await click("新建");
    await editInput('textarea[aria-label="标准答案"]', "新建题目答案");
    // 字段移动后仍须注册在同一表单中；逐个留空可发现只显示输入框却丢失校验的回归。
    for (const name of ["question_no", "question_type", "score"] as const) {
      await act(async () => {
        mocks.context!.questionWorkflow.form.setFieldsValue({ question_no: "3", question_type: "short_answer", score: 10, [name]: undefined });
        mocks.context!.actions.markQuestionDirty();
      });
      await click("保存题目与答案");
      expect(mocks.context!.questionWorkflow.form.getFieldError(name).length).toBeGreaterThan(0);
      expect(mocks.createQuestion).not.toHaveBeenCalled();
      expect(mocks.updateQuestion).not.toHaveBeenCalled();
    }
  }, headerInteractionTimeout);

  it("仅选择 AI 不生成或改写采分点，保存草稿回读后仍显示 AI，且明确仍待确认", async () => {
    saveRubricOnRead();
    await render(); await click("评分配置"); await chooseAI();
    expect(mocks.generateQuestionRubricDraft).not.toHaveBeenCalled();
    expect(mocks.context!.rubricWorkflow.rubricPoints[0].description).toBe("synthetic point");
    expect(host.textContent).not.toContain("提交为");
    await click("保存草稿");
    expect(mocks.createRubric).toHaveBeenCalledExactlyOnceWith("question-1", expect.objectContaining({ status: "draft", points: [expect.objectContaining({ description: "synthetic point" })] }));
    expect(aiSelected()).toBe(true);
    expect(host.textContent).toContain("草稿已保存");
    expect(host.textContent).toContain("待确认");
    expect(host.querySelector('.question-index-cell[title^="第 1 题"]')?.classList.contains("is-rubric")).toBe(true);
    // 静默回读也可能短暂卸载评分展示区；方式选择应由页面工作流保留。
    mocks.config.configLoading = true; await render();
    mocks.config.configLoading = false; await render();
    expect(aiSelected()).toBe(true);
    expect(mocks.generateQuestionRubricDraft).not.toHaveBeenCalled();
  });

  it("AI 预览必须先采用；采用并保存后保留建议采分点和 AI 方式", async () => {
    const item = question("question-1");
    item.solution = { id: "solution-1", question_id: item.id, solution_version: "1", raw_text: "", steps: [{ step_no: 1, content: "2x=4" }, { step_no: 2, content: "x=2" }], source_refs: [], verification_status: "human_confirmed" };
    mocks.config.questions = [item];
    const result = { suggested_rubric_candidates: [{ candidate_id: item.id, max_score: 5,
      origin: "ai_suggestion_from_solution", status: "review_required", issues: [], points: [
        { id: "a", description: "正确变形", suggested_score: 3, evidence_step_ids: ["step-1"], review_note: "" },
        { id: "b", description: "算出结果", suggested_score: 2, evidence_step_ids: ["step-2"], review_note: "" }
      ] }] };
    const draftJob = { id: "synthetic-rubric-job", exam_id: item.exam_id, question_id: item.id, question_no: item.question_no, status: "succeeded",
      request_id: "synthetic-request", model_name: "synthetic-model", attempt: 1, created_at: "2026-10-16T00:00:00Z", updated_at: "2026-10-16T00:00:00Z",
      input: { expected_question: structuredClone(item) }, result };
    mocks.createQuestionRubricDraftJob.mockResolvedValue({ job: draftJob });
    mocks.getQuestionRubricDraftJob.mockResolvedValue({ job: draftJob });
    saveRubricOnRead();
    await render(); await click("评分配置"); await chooseAI(); await click("生成分值建议");
    expect(host.textContent).toContain("AI 建议 · 待教师核对");
    // 后台结果自动预览不算教师编辑，不能自动写入或替换已有正式采分点。
    expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.context!.rubricWorkflow.rubricPoints[0].description).toBe("synthetic point");
    await click("采用建议到评分细则");
    await act(async () => { await mocks.confirm.mock.lastCall![0].onOk(); });
    expect(aiSelected()).toBe(true);
    await click("保存草稿");
    expect(mocks.createRubric).toHaveBeenCalledExactlyOnceWith("question-1", expect.objectContaining({ status: "draft", points: [
      expect.objectContaining({ description: "正确变形\n参考解答：\n2x=4", score: 3 }),
      expect.objectContaining({ description: "算出结果\n参考解答：\nx=2", score: 2 })
    ] }));
    expect(aiSelected()).toBe(true);
    expect(mocks.context!.rubricWorkflow.rubricPoints).toHaveLength(2);
    expect(mocks.createQuestionRubricDraftJob).toHaveBeenCalledTimes(1);
  });

  it("确认并锁定须明确确认，继续检查不写入且保留评分草稿", async () => {
    await render(); await click("评分配置");
    await editInput(".paper-rubric-description textarea", "教师仍在核对的条件");
    await click("确认并锁定细则");
    expect(mocks.createRubric).not.toHaveBeenCalled();
    const confirmation = mocks.confirm.mock.lastCall![0];
    expect(confirmation).toEqual(expect.objectContaining({ title: "确认本题评分细则？", okText: "确认并锁定", cancelText: "继续检查" }));
    await act(async () => { await confirmation.onCancel?.(); });
    expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.context!.rubricWorkflow.rubricDirty).toBe(true);
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("教师仍在核对的条件");
  });

  it("确认并锁定保存 locked 状态，回读后题号不再待确认且题目和细则只读", async () => {
    saveRubricOnRead();
    await render(); await click("评分配置"); await click("确认并锁定细则");
    expect(mocks.createRubric).not.toHaveBeenCalled();
    await act(async () => { await mocks.confirm.mock.lastCall![0].onOk(); });
    expect(mocks.createRubric).toHaveBeenCalledExactlyOnceWith("question-1", expect.objectContaining({ status: "locked" }));
    expect(mocks.loadConfig).toHaveBeenCalledExactlyOnceWith("exam-1", { silent: true, propagateError: true });
    expect(mocks.context!.selectedQuestion?.rubric?.status).toBe("locked");
    expect(host.textContent).toContain("已确认并锁定");
    expect(host.querySelector('.question-index-cell[title^="第 1 题"]')?.classList.contains("is-rubric")).toBe(false);
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.disabled).toBe(true);
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.disabled).toBe(true);
    for (const name of ["question_no", "question_type", "score"] as const) {
      expect(host.querySelector<HTMLInputElement>(`.paper-editor-sticky-head #${name}`)?.disabled).toBe(true);
    }
  });

  it("锁定写入失败时保留可编辑草稿，不显示已锁定，重试仍需重新确认", async () => {
    await render(); await click("评分配置");
    await editInput(".paper-rubric-description textarea", "失败后保留的评分条件");
    mocks.createRubric.mockRejectedValueOnce(new Error("synthetic lock failure"));
    await click("确认并锁定细则");
    await act(async () => { await mocks.confirm.mock.lastCall![0].onOk(); });
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.context!.selectedQuestion?.rubric?.status).toBe("draft");
    const field = host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea");
    expect(field?.value).toBe("失败后保留的评分条件"); expect(field?.disabled).toBe(false);
    saveRubricOnRead();
    await click("确认并锁定细则");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    await act(async () => { await mocks.confirm.mock.lastCall![0].onOk(); });
    expect(mocks.createRubric).toHaveBeenCalledTimes(2);
    expect(mocks.createRubric).toHaveBeenLastCalledWith("question-1", expect.objectContaining({ status: "locked", points: [expect.objectContaining({ description: "失败后保留的评分条件" })] }));
  });

  it("锁定成功后回读失败仍立即只读，重读只发 GET 而不重复创建版本", async () => {
    mocks.createRubric.mockResolvedValueOnce({ rubric: { version: "2" } });
    mocks.loadConfig.mockRejectedValueOnce(new Error("synthetic read failure"));
    await render(); await click("评分配置"); await click("确认并锁定细则");
    await act(async () => { await mocks.confirm.mock.lastCall![0].onOk(); });
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("已确认并锁定");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.disabled).toBe(true);
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.disabled).toBe(true);
    await act(async () => { mocks.context!.actions.saveRubric(); });
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    const reread = [...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "重新读取保存结果");
    expect(reread?.disabled).toBe(false);
    saveRubricOnRead();
    await click("重新读取保存结果");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(mocks.loadConfig).toHaveBeenCalledTimes(2);
    expect(mocks.context!.selectedQuestion?.rubric?.status).toBe("locked");
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
  });

  it("validates both regions before writing either when the newly entered score mismatches the rubric", async () => {
    await render(); await editStem("unsaved full question"); await editInput("input#score", "8");
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled(); expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(host.textContent).toContain("本题尚未保存");
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("unsaved full question");
  });

  it("统一分值成功后回读失败时，阻止旧表单覆盖分值，重新读取只发GET", async () => {
    const questions = [question("question-1"), question("question-2")].map((item) => ({ ...item, question_type: "single_choice", rubric: undefined }));
    mocks.config.questions = questions;
    mocks.listQuestions.mockResolvedValue({ questions });
    mocks.loadConfig.mockRejectedValueOnce(new Error("synthetic read failure"));
    await render();
    await click("批量分值");
    await editInput('input[aria-label="单选题统一每题分值"]', "8");
    await click("保存分值修改");
    await act(async () => { await mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onOk(); });
    expect(mocks.updateQuestion.mock.calls).toEqual([["question-1", { score: 8 }], ["question-2", { score: 8 }]]);
    expect(host.querySelector<HTMLInputElement>("input#score")?.disabled).toBe(true);
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    mocks.loadConfig.mockImplementationOnce(async () => { mocks.config.questions = questions.map((item) => ({ ...item, score: 8 })); });
    await click("重新读取保存结果");
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    expect(Number(host.querySelector<HTMLInputElement>("input#score")?.value)).toBe(8);
    expect(host.querySelector<HTMLInputElement>("input#score")?.disabled).toBe(false);
  });

  it("统一分值回读失败后，早已打开的删除确认与旧保存回调仍被同步屏障阻止", async () => {
    const questions = [question("question-1"), question("question-2")].map((item) => ({ ...item, question_type: "single_choice", rubric: undefined }));
    mocks.config.questions = questions;
    mocks.listQuestions.mockResolvedValue({ questions });
    await render();
    const oldActions = mocks.context!.actions;
    const deleteButton = host.querySelector<HTMLButtonElement>('button[aria-label="删除当前题目"]')!;
    await act(async () => deleteButton.click());
    const oldDeleteConfirmation = mocks.confirm.mock.calls[0][0];
    mocks.loadConfig.mockRejectedValueOnce(new Error("synthetic read failure"));
    await click("批量分值");
    await editInput('input[aria-label="单选题统一每题分值"]', "8");
    await click("保存分值修改");
    await act(async () => { await mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onOk(); });
    await act(async () => {
      await oldDeleteConfirmation.onOk();
      oldActions.saveQuestion();
      oldActions.saveAllQuestionChanges();
    });
    expect(mocks.deleteQuestion).not.toHaveBeenCalled();
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    expect(mocks.info).toHaveBeenCalledWith(expect.stringContaining("重新读取分值结果"));
    expect(mocks.context!.state.saveRefreshPending).toBe(true);
  });

  it("saves content then rubric using the new score, refreshes once and offers the next question", async () => {
    await render(); await editStem("whole saved question"); await editInput("input#score", "8");
    await click("评分配置"); await editInput(".paper-rubric-point-fields input", "8");
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).toHaveBeenCalledWith("question-1", expect.objectContaining({ score: 8, stem: "whole saved question" }));
    expect(mocks.createRubric).toHaveBeenCalledWith("question-1", expect.objectContaining({ max_score: 8, points: [expect.objectContaining({ score: 8 })] }));
    expect(mocks.updateQuestion.mock.invocationCallOrder[0]).toBeLessThan(mocks.createRubric.mock.invocationCallOrder[0]);
    expect(mocks.loadConfig).toHaveBeenCalledExactlyOnceWith("exam-1", { silent: true, propagateError: true });
    expect(host.textContent).toContain("全部保存"); expect(host.textContent).toContain("保存完成，下一题");
    await click("保存完成，下一题"); expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("synthetic stem question-2");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.updateQuestion).toHaveBeenCalledTimes(1);
  });

  it("keeps the unsaved scoring region and retries without writing the already saved content twice", async () => {
    await render(); await editStem("content saved before rubric failure"); await click("评分配置");
    await editInput(".paper-rubric-description textarea", "remaining scoring input");
    mocks.createRubric.mockRejectedValueOnce(new Error("synthetic rubric write failure"));
    await click("保存本题全部修改");
    expect(host.textContent).toContain("题目与答案已保存；评分配置尚未全部保存");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("remaining scoring input");
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(1); expect(mocks.createRubric).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("全部保存");
  });

  it("does not switch questions or start a second write while the whole question is saving", async () => {
    let finish!: () => void; mocks.updateQuestion.mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
    await render(); await editStem("pending full question"); await click("保存本题全部修改");
    await click("保存本题全部修改");
    await act(async () => host.querySelector<HTMLButtonElement>('.question-index-cell[title^="第 2 题"]')!.click());
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(1); expect(mocks.info).toHaveBeenCalledWith(expect.stringContaining("等待保存结果"));
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("pending full question");
    await act(async () => finish());
  });

  it("saves fixed scoring as a draft using the new maximum without implicitly publishing", async () => {
    mocks.config.questions = [ { ...question("question-1", ["A"]), question_type: "multiple_choice", options: ["甲", "乙"] } ];
    await render(); await editInput("input#score", "8"); await click("保存本题全部修改");
    expect(mocks.createScoringRule).toHaveBeenCalledWith("question-1", "multiple_choice", expect.objectContaining({ max_score: 8 }));
    expect(mocks.publishScoringRule).not.toHaveBeenCalled(); expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(mocks.updateQuestion.mock.invocationCallOrder[0]).toBeLessThan(mocks.createScoringRule.mock.invocationCallOrder[0]);
  });

  it("keeps successful scoring receipts when only the final refresh fails", async () => {
    await render(); await editStem("saved despite failed refresh"); await editInput("input#score", "8");
    await click("评分配置"); await editInput(".paper-rubric-point-fields input", "8");
    mocks.loadConfig.mockRejectedValueOnce(new Error("refresh unavailable")); await click("保存本题全部修改");
    expect(host.textContent).toContain("题目与答案、评分配置已保存；列表刷新失败");
    await click("重新读取保存结果");
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(1); expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(mocks.loadConfig).toHaveBeenCalledTimes(2);
  });

  it("retains accepted rubric-only input through repeated failed reads and retries without creating another version", async () => {
    await render(); await click("评分配置");
    await editInput(".paper-rubric-description textarea", "仅评分修改，已被服务器接受");
    mocks.loadConfig.mockRejectedValueOnce(new Error("first read failed"));
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled(); expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("仅评分修改，已被服务器接受");
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent?.includes("保存本题全部修改"))?.disabled).toBe(true);
    mocks.loadConfig.mockRejectedValueOnce(new Error("second read failed"));
    await click("重新读取保存结果");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("仅评分修改，已被服务器接受");
    mocks.loadConfig.mockImplementationOnce(async () => {
      const items = mocks.config.questions as Question[];
      mocks.config.questions = items.map(item => item.id === "question-1" ? { ...item, rubric: { ...item.rubric!, points: [{ ...item.rubric!.points[0], description: "仅评分修改，已被服务器接受" }] } } : item);
    });
    await click("重新读取保存结果");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1); expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(mocks.loadConfig).toHaveBeenCalledTimes(3); expect(host.textContent).toContain("保存完成，下一题");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("仅评分修改，已被服务器接受");
    await click("保存完成，下一题");
    expect(host.textContent).not.toContain("重新读取保存结果");
  });

  it("keeps edits made after an accepted save when its read-only recovery succeeds", async () => {
    await render(); await click("评分配置"); await editInput(".paper-rubric-description textarea", "已保存条件");
    mocks.loadConfig.mockRejectedValueOnce(new Error("read failed")); await click("保存本题全部修改");
    await editInput(".paper-rubric-description textarea", "随后继续编辑的条件");
    mocks.loadConfig.mockImplementationOnce(async () => {
      const items = mocks.config.questions as Question[];
      mocks.config.questions = items.map(item => item.id === "question-1" ? { ...item, rubric: { ...item.rubric!, points: [{ ...item.rubric!.points[0], description: "已保存条件" }] } } : item);
    });
    await click("重新读取保存结果");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("随后继续编辑的条件");
    expect(host.textContent).toContain("之后继续编辑的修改仍保留"); expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(host.textContent).not.toContain("保存完成，下一题");
  });

  it("分区评分已保存而回读失败时保留回执、原快照和另一处草稿，只读重试不再创建版本", async () => {
    await render();
    await editStem("尚未保存的题目草稿");
    await click("评分配置");
    await editInput(".paper-rubric-description textarea", "分区保存的评分条件");
    mocks.createRubric.mockResolvedValueOnce({ rubric: { version: "2" } });
    mocks.loadConfig.mockRejectedValueOnce(new Error("read unavailable"));
    await click("保存草稿");
    expect(host.textContent).toContain("评分细则第 2 版草稿已保存");
    expect(host.textContent).toContain("无需再次保存评分细则");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("分区保存的评分条件");
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("尚未保存的题目草稿");
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true, propagateError: true });
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "保存草稿")?.disabled).toBe(true);
    mocks.loadConfig.mockRejectedValueOnce(new Error("second read unavailable"));
    await click("重新读取保存结果");
    expect(host.textContent).toContain("评分细则第 2 版草稿已保存");
    mocks.loadConfig.mockImplementationOnce(async () => {
      const items = mocks.config.questions as Question[];
      mocks.config.questions = items.map(item => item.id === "question-1" ? { ...item, rubric: { ...item.rubric!, version: "2", points: [{ ...item.rubric!.points[0], description: "分区保存的评分条件" }] } } : item);
    });
    await click("重新读取保存结果");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(mocks.loadConfig).toHaveBeenCalledTimes(3);
    expect(host.textContent).toContain("其他区域尚有未保存修改");
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("尚未保存的题目草稿");
  });

  it("分区保存后的只读恢复保留后来填写的评分条件", async () => {
    await render(); await click("评分配置");
    await editInput(".paper-rubric-description textarea", "已经保存的条件");
    mocks.loadConfig.mockRejectedValueOnce(new Error("read failed"));
    await click("保存草稿");
    await editInput(".paper-rubric-description textarea", "回读前继续编辑的条件");
    mocks.loadConfig.mockImplementationOnce(async () => {
      const items = mocks.config.questions as Question[];
      mocks.config.questions = items.map(item => item.id === "question-1" ? { ...item, rubric: { ...item.rubric!, points: [{ ...item.rubric!.points[0], description: "已经保存的条件" }] } } : item);
    });
    await click("重新读取保存结果");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("回读前继续编辑的条件");
    expect(host.textContent).toContain("之后继续编辑的修改仍保留");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
  });

  it("rechecks the shared lock when an earlier delete dialog is confirmed during saving", async () => {
    let finish!: () => void; mocks.updateQuestion.mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
    await render(); await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="删除当前题目"]')!.click());
    await editStem("saving while delete dialog remains open"); await click("保存本题全部修改");
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.deleteQuestion).not.toHaveBeenCalled(); await act(async () => finish());
  });

  it("does not rewrite unchanged question content when only scoring has changed", async () => {
    await render(); await click("评分配置"); await editInput(".paper-rubric-description textarea", "only scoring changed");
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(mocks.createRubric).toHaveBeenCalledWith("question-1", expect.objectContaining({ points: [expect.objectContaining({ description: "only scoring changed" })] }));
    expect(host.textContent).toContain("全部保存");
    expect(host.textContent).toContain("评分细则仍待确认");
  });

  it("rejects a fixed-rule value above the new maximum before updating the question", async () => {
    mocks.config.questions = [{ ...question("question-1", ["A"]), question_type: "multiple_choice", options: ["甲", "乙"] }];
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [{ id: "rule-1", question_id: "question-1", status: "draft", revision: 2, config: { wrong_option_penalty: 5 } }] });
    await render(); await editInput("input#score", "3"); await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled(); expect(mocks.updateScoringRule).not.toHaveBeenCalled();
    expect(host.textContent).toContain("本题尚未保存");
  });

  it("retries scoring for a newly created question without creating that question twice", async () => {
    mocks.createQuestion.mockResolvedValue({ question: { id: "new-question" } });
    await render(); await click("新建"); await editInput("input#question_no", "3");
    await editStem("new question with retained scoring"); await editInput('textarea[aria-label="标准答案"]', "new answer");
    mocks.createRubric.mockRejectedValueOnce(new Error("temporary scoring failure"));
    await click("保存本题全部修改");
    expect(mocks.createQuestion).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("题目与答案已保存；评分配置尚未全部保存");
    mocks.loadConfig.mockImplementation(async () => { mocks.config.questions = [...mocks.config.questions as Question[], { ...question("new-question"), score: 10 }]; });
    await click("保存本题全部修改");
    expect(mocks.createQuestion).toHaveBeenCalledTimes(1); expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(mocks.createRubric).toHaveBeenLastCalledWith("new-question", expect.objectContaining({ max_score: 10 }));
    expect(host.textContent).toContain("本题题目与答案、已修改的评分配置全部保存");
  });

  it("removes question-bank selection while retaining curated import after freezing", async () => {
    const page = <PaperRubricPage canManage canManageAssessment={false} canReadQuestionBank canImportQuestionBank mode="embedded" initialView="questions" fixedExamId="exam-1" />;
    await act(async () => root.render(page)); expect(host.textContent).not.toContain("从学校题库选题");
    expect(host.textContent).not.toContain("将本次考试题目精选入题库");
    mocks.config.exams = [{ id: "exam-1", subject: "math", status: "completed", total_score: 10, name: "synthetic exam" } as Exam];
    await act(async () => root.render(<PaperRubricPage canManage canManageAssessment={false} canReadQuestionBank canImportQuestionBank mode="embedded" initialView="questions" fixedExamId="exam-1" />));
    expect(host.querySelector("#paper-question-tools")?.textContent).toContain("将本次考试题目精选入题库"); expect(host.textContent).not.toContain("从学校题库选题");
  });

  it("缺项定位在切换题目前等待未保存确认，确认后打开正确评分区域", async () => {
    await render(); await editStem("pending issue navigation");
    await act(async () => mocks.context!.actions.openQuestionIssue((mocks.config.questions as Question[])[1], "rubric"));
    expect(mocks.context!.selectedQuestion?.id).toBe("question-1");
    expect(mocks.context!.state.detailTab).toBe("content");
    const pending = mocks.confirm.mock.lastCall![0];
    await act(async () => pending.onOk());
    expect(mocks.context!.selectedQuestion?.id).toBe("question-2");
    expect(mocks.context!.state.detailTab).toBe("scoring");
  });

  it("同题缺项定位保留题干草稿，并切到对应评分区域", async () => {
    await render(); const field = await editStem("keep same-question draft");
    await act(async () => mocks.context!.actions.openQuestionIssue((mocks.config.questions as Question[])[0], "rubric"));
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(field.value).toBe("keep same-question draft");
    expect(mocks.context!.state.formDirty).toBe(true);
    expect(mocks.context!.state.detailTab).toBe("scoring");
  });

  it("keeps the same form field and unsaved value across content/scoring tab changes", async () => {
    await render();
    const field = await editStem("unsaved synthetic stem");
    await click("评分配置");
    expect(host.querySelector("textarea#stem")).toBe(field);
    expect(field.value).toBe("unsaved synthetic stem");
    expect(host.querySelector<HTMLDivElement>(".question-editor > div[style]")?.style.display).toBe("none");
    await click("保存草稿");
    expect(mocks.createRubric).toHaveBeenCalledTimes(1);
    expect(mocks.warning).not.toHaveBeenCalled();
    await click("题目与答案");
    expect(host.querySelector("textarea#stem")).toBe(field);
    expect(field.value).toBe("unsaved synthetic stem");
  });

  it("saves both dirty regions separately without discarding the other region on refresh", async () => {
    await render();
    await editStem("new local stem");
    await click("评分配置");
    const point = host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea");
    if (!point) throw new Error("Missing rubric field");
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set?.call(point, "new local scoring condition");
      point.dispatchEvent(new Event("input", { bubbles: true }));
    });
    mocks.loadConfig.mockImplementation(async () => {
      const items = mocks.config.questions as Question[];
      mocks.config.questions = items.map((item) => item.id === "question-1" ? { ...item, rubric: { ...item.rubric!, points: [{ ...item.rubric!.points[0], description: "new local scoring condition" }] } } : item);
    });
    await click("保存草稿");
    expect(mocks.createRubric).toHaveBeenCalledWith("question-1", expect.objectContaining({ points: expect.arrayContaining([expect.objectContaining({ description: "new local scoring condition" })]) }));
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("new local stem");
    await click("题目与答案");
    await click("保存题目与答案");
    expect(mocks.updateQuestion).toHaveBeenCalledWith("question-1", expect.objectContaining({ stem: "new local stem" }));
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it("keeps the current question selected while its save request is pending", async () => {
    let finishSave: (() => void) | undefined;
    mocks.updateQuestion.mockImplementation(() => new Promise<void>((resolve) => { finishSave = resolve; }));
    await render();
    await editStem("pending stem");
    await click("保存题目与答案");
    const next = host.querySelector<HTMLButtonElement>('.question-index-cell[title^="第 2 题"]');
    await act(async () => next?.click());
    expect(mocks.info).toHaveBeenCalledWith(expect.stringContaining("等待保存结果"));
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("pending stem");
    await act(async () => finishSave?.());
  });

  it("waits for explicit discard confirmation before switching away from a dirty question", async () => {
    await render();
    await editStem("unsaved synthetic stem");
    const next = host.querySelector<HTMLButtonElement>('.question-index-cell[title^="第 2 题"]');
    if (!next) throw new Error("Missing second question");
    await act(async () => next.click());
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("unsaved synthetic stem");
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("synthetic stem question-2");
  });

  it("saves structured standard answers in their original shape while editing other fields", async () => {
    const originalAnswer = { blanks: ["a", "b"] };
    mocks.config.questions = [question("question-1", originalAnswer)];
    await render();
    const answerFields = Array.from(host.querySelectorAll<HTMLTextAreaElement>('textarea[aria-label^="各空答案"]'));
    expect(answerFields.map(field => field.value)).toEqual(["a", "b"]);
    await editStem("updated synthetic stem");
    await click("保存题目与答案");
    expect(mocks.updateQuestion).toHaveBeenCalledWith("question-1", expect.objectContaining({ stem: "updated synthetic stem", answer_key: expect.objectContaining({ standard_answer: originalAnswer }) }));
  });

  it("does not expose editing commands while configuration is loading or failed", async () => {
    mocks.config.configLoading = true;
    await render();
    expect(host.textContent).toContain("正在读取试卷与题目配置");
    expect(host.querySelector("textarea#stem")).toBeNull();
    expect(host.textContent).not.toContain("保存题目与答案");
    mocks.config.configLoading = false;
    mocks.config.configError = "synthetic configuration failure";
    await render();
    expect(host.textContent).toContain("synthetic configuration failure");
    expect(host.querySelector("textarea#stem")).toBeNull();
  });

  it("disables question mutations in a view-only session", async () => {
    await render(false);
    for (const name of ["question_no", "question_type", "score"]) {
      expect(host.querySelector<HTMLInputElement>(`.paper-editor-sticky-head #${name}`)?.disabled).toBe(true);
    }
    const save = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("保存题目与答案"));
    expect(save?.disabled).toBe(true);
    await click("保存题目与答案");
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
  });

  it("automatically fills a confirmed import once per generation without replaying rerenders", async () => {
    const job = { id: "import-1", generation: 1, structure_confirmed_generation: 1, status: "review_required", questions: [] } as unknown as PaperImportJob;
    mocks.config.paperImports = [job];
    await render();
    expect(mocks.fillRecognizedContent).toHaveBeenCalledExactlyOnceWith(job, true);
    await render();
    expect(mocks.fillRecognizedContent).toHaveBeenCalledTimes(1);
    const next = { ...job, generation: 2, structure_confirmed_generation: 2 };
    mocks.config.paperImports = [next];
    await render();
    expect(mocks.fillRecognizedContent).toHaveBeenCalledTimes(2);
    expect(mocks.fillRecognizedContent).toHaveBeenLastCalledWith(next, true);
  });

  it("waits for the local draft to be saved before automatically filling newly confirmed structure", async () => {
    const job = { id: "import-1", generation: 1, status: "review_required", questions: [] } as unknown as PaperImportJob;
    mocks.config.paperImports = [job];
    await render();
    await editStem("unsaved synthetic stem");
    const confirmed = { ...job, structure_confirmed_generation: 1 };
    mocks.config.paperImports = [confirmed];
    await render();
    expect(mocks.fillRecognizedContent).not.toHaveBeenCalled();
    await click("保存题目与答案");
    expect(mocks.fillRecognizedContent).toHaveBeenCalledExactlyOnceWith(confirmed, true);
  });
});
