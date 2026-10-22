// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { fireEvent } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../api/exams";
import type { Question } from "../../api/papers";
import { PaperScoringConfiguration } from "./PaperScoringConfiguration";
import { PaperQuestionSaveActions } from "./PaperQuestionDetail";
import { questionEditingAccess } from "./paperQuestionPresentation";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";

// 渲染真实主界面与 AntD，只替换会读取远端配置的高级编辑器；抽屉保存和后端路由另有专项验收。
vi.mock("../../components/features/assessment/AssessmentProfileEditor", () => ({
  AssessmentProfileEditor: ({ question }: Readonly<{ question: Question | null }>) => <div data-testid="advanced-editor" data-question-type={question?.question_type}>高级配置编辑器</div>
}));

function makeContext(subject: string, questionType: string, canManage = true, locked = false): PaperRubricViewContext {
  const exam: Exam = {
    id: "exam-synthetic", tenant_id: "tenant-synthetic", school_id: "school-synthetic", name: "synthetic exam", subject,
    exam_type: "unit", total_score: 5, status: "draft", grading_mode: "ai_assisted", appeal_enabled: false,
    publish_policy: "manual", created_by: "teacher-synthetic", class_ids: [], revision: 1
  };
  const question: Question = {
    id: "question-synthetic", tenant_id: "tenant-synthetic", exam_id: exam.id, question_no: "1", question_type: questionType,
    score: 5, stem: "synthetic question", options: [], knowledge_points: [], sort_order: 1, status: "draft",
    answer_key: { id: "answer-synthetic", question_id: "question-synthetic", answer_version: "1", standard_answer: "1", equivalent_answers: [], tolerance: {} },
    rubric: { id: "rubric-synthetic", question_id: "question-synthetic", version: "1", status: locked ? "locked" : "draft", max_score: 5,
      points: [{ id: "point-synthetic", description: "synthetic scoring point", score: 5, required: true }], deductions: [], examples: [] }
  };
  // 其余页面工作流不被本展示组件读取；只构造它实际使用的上下文，不启动保存或接口流程。
  return {
    selectedExam: exam, selectedQuestion: question, options: { canManageAssessment: canManage },
    state: { detailTab: "scoring", saveBusy: false, saveRefreshPending: false, formDirty: false },
    actions: { saveRubric: vi.fn() },
    access: questionEditingAccess(canManage, questionType, question, exam),
    rubricWorkflow: {
      rubricStatus: question.rubric?.status, rubricPoints: question.rubric?.points, pointTotal: 5, scoreMismatch: false,
      deductionsJson: "", examplesJson: "", setRubricStatus: vi.fn(), addPoint: vi.fn(), removePoint: vi.fn(), updatePoint: vi.fn(),
      addEvidenceRequirement: vi.fn(), removeEvidenceRequirement: vi.fn(), updateEvidenceRequirement: vi.fn(),
      setDeductionsJson: vi.fn(), setExamplesJson: vi.fn(), setAssistantDirty: vi.fn(), replaceRubricPoints: vi.fn(),
      rubricDirty: false, savingRubric: false, assistantDirty: false, assistantResetVersion: 0, assistantDraftStore: { current: null }
    },
    scoringRuleWorkflow: { scoringRuleConfig: {}, setRuleConfig: vi.fn() }
  } as unknown as PaperRubricViewContext;
}

describe("paper scoring capability presentation", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })) });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  async function render(context: PaperRubricViewContext) {
    await act(async () => root.render(<PaperScoringConfiguration context={context} />));
  }

  function manualNotice() {
    return Array.from(host.querySelectorAll(".ant-alert")).find((alert) => alert.querySelector(".ant-alert-message")?.textContent === "本题需要教师人工评分");
  }

  it.each(["math", "physics", "chemistry"])("keeps %s formula manual scoring visible while advanced configuration is closed", async (subject) => {
    await render(makeContext(subject, "formula"));
    const notice = manualNotice();
    expect(notice).toBeDefined();
    expect(notice?.closest("details")).toBeNull();
    expect(notice?.textContent).toContain("公式题目前需要教师人工评分");
    expect(notice?.textContent).toContain("也不能自动判分");
    expect(host.querySelector(".rubric-editor")?.textContent).toContain("证据本身不会自动给分");
    expect(host.textContent).not.toContain("最终分数仍由规则或教师确认");

    const advanced = host.querySelector<HTMLDetailsElement>("details.paper-advanced-settings");
    expect(advanced?.open).toBe(false);
    await act(async () => advanced?.querySelector("summary")?.click());
    expect(advanced?.open).toBe(true);
    expect(manualNotice()).toBe(notice);
    await act(async () => advanced?.querySelector("summary")?.click());
    expect(advanced?.open).toBe(false);
    expect(manualNotice()).toBe(notice);
  });

  it.each([true, false])("shows coding manual scoring even without formula evidence and canManage=%s", async (canManage) => {
    await render(makeContext("math", "coding", canManage));
    expect(manualNotice()?.textContent).toContain("编程题目前需要教师人工评分");
    expect(manualNotice()?.closest("details")).toBeNull();
    expect(host.textContent).not.toContain("公式与步骤证据只作阅卷提示");
    const add = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("添加采分点"));
    expect(add?.disabled).toBe(!canManage);
  });

  it("preserves manual requirements for a locked formula rubric", async () => {
    await render(makeContext("physics", "formula", true, true));
    expect(manualNotice()?.textContent).toContain("公式题目前需要教师人工评分");
    expect(Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("添加采分点"))?.disabled).toBe(true);
  });

  it("shows formula manual scoring for other subjects without implying formula evidence availability", async () => {
    await render(makeContext("english", "formula"));
    expect(manualNotice()).toBeDefined();
    expect(host.textContent).not.toContain("公式与步骤证据只作阅卷提示");
  });

  it.each(["math", "physics", "chemistry"])("keeps %s calculation evidence separate from the unknown saved scoring mode", async (subject) => {
    await render(makeContext(subject, "calculation"));
    expect(manualNotice()).toBeUndefined();
    expect(host.textContent).toContain("公式与步骤证据只作阅卷提示");
    expect(host.textContent).toContain("证据本身不会自动给分");
    expect(host.textContent).not.toContain("最终分数仍由规则或教师确认");
    expect(host.textContent).not.toContain("本题不能使用规则自动评分");
    expect(host.textContent).not.toContain("AI 建议分数仍须教师确认");
  });

  it("keeps numeric questions on fixed scoring without the subjective formula evidence notice", async () => {
    const context = makeContext("math", "numeric");
    expect(context.access.showFormulaEvidence).toBe(true);
    await render(context);
    expect(host.textContent).toContain("固定判分规则");
    expect(host.querySelector(".rubric-editor")).toBeNull();
    expect(host.textContent).not.toContain("公式与步骤证据只作阅卷提示");
    expect(manualNotice()).toBeUndefined();
  });

  it("shows fixed scores for incomplete and wrong multiple choice answers and submits numeric edits", async () => {
    const context = makeContext("math", "multiple_choice");
    context.scoringRuleWorkflow.scoringRulesReadable = true;
    await render(context);
    const partial = host.querySelector<HTMLInputElement>('input[aria-label="少选且无错选得分"]');
    const wrong = host.querySelector<HTMLInputElement>('input[aria-label="有错选得分"]');
    expect(Number(partial?.value)).toBe(0);
    expect(Number(wrong?.value)).toBe(0);
    expect(partial?.getAttribute("aria-valuemax")).toBe("5");
    expect(wrong?.getAttribute("aria-valuemin")).toBe("0");
    expect(host.textContent).toContain("全对得满分");
    expect(host.textContent).toContain("空白得 0 分");
    expect(host.textContent).not.toContain("错误得 0 分");
    await act(async () => { fireEvent.change(partial!, { target: { value: "2" } }); });
    expect(context.scoringRuleWorkflow.setRuleConfig).toHaveBeenLastCalledWith("partial_score", 2);
    await act(async () => { fireEvent.change(wrong!, { target: { value: "1" } }); });
    expect(context.scoringRuleWorkflow.setRuleConfig).toHaveBeenLastCalledWith("wrong_answer_score", 1);
  });

  it("keeps an existing per-option policy until the teacher explicitly switches to fixed scores", async () => {
    const context = makeContext("math", "multiple_choice");
    context.scoringRuleWorkflow.scoringRulesReadable = true;
    const legacy = { allow_partial: true, score_per_correct_option: 1, wrong_option_penalty: 2, minimum_score: 0 };
    context.scoringRuleWorkflow.scoringRuleConfig = legacy;
    await render(context);
    expect(host.textContent).toContain("当前按正确选项数计分");
    expect(host.querySelector('input[aria-label="少选且无错选得分"]')).toBeNull();
    expect(context.scoringRuleWorkflow.setRuleConfig).not.toHaveBeenCalled();
    const convert = Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "改为固定得分");
    await act(async () => convert?.click());
    expect(context.scoringRuleWorkflow.setRuleConfig).toHaveBeenCalledWith("partial_score", 0);
    expect(context.scoringRuleWorkflow.setRuleConfig).toHaveBeenCalledWith("wrong_answer_score", 0);
    context.scoringRuleWorkflow.scoringRuleConfig = { ...legacy, partial_score: 2, wrong_answer_score: 1 };
    await render({ ...context });
    expect(Number(host.querySelector<HTMLInputElement>('input[aria-label="少选且无错选得分"]')?.value)).toBe(2);
    expect(Number(host.querySelector<HTMLInputElement>('input[aria-label="有错选得分"]')?.value)).toBe(1);
    expect(host.textContent).not.toContain("每个正确选项分值");
  });

  it.each(["saveBusy", "saveRefreshPending"])("blocks rubric assistance while %s even before access flags refresh", async (flag) => {
    const context = makeContext("math", "calculation");
    context.state = { [flag]: true } as unknown as PaperRubricViewContext["state"];
    await render(context);
    const selector = host.querySelector<HTMLInputElement>('.paper-rubric-assistant input[type="radio"]');
    expect(selector?.disabled).toBe(true);
    expect(host.querySelector<HTMLInputElement>('.paper-rubric-assistant input[type="checkbox"]')?.disabled).toBe(true);
  });

  it("removes formula capability text after selecting a short answer without inferring its scoring mode", async () => {
    await render(makeContext("math", "formula"));
    expect(manualNotice()).toBeDefined();
    await render(makeContext("math", "short_answer"));
    expect(manualNotice()).toBeUndefined();
    expect(host.textContent).not.toContain("公式与步骤证据只作阅卷提示");
    expect(host.textContent).not.toContain("本题不能使用规则自动评分");
  });

  it("shows a saved draft as awaiting confirmation instead of asking users to choose an internal status", async () => {
    await render(makeContext("math", "short_answer"));
    expect(host.querySelector('[role="status"]')?.textContent).toBe("草稿已保存，待确认");
    expect(host.textContent).toContain("确认并锁定细则");
    expect(host.textContent).not.toContain("提交为");
    expect(host.querySelector(".rubric-status-select")).toBeNull();
  });

  it("keeps unsaved edits and missing saved points distinct from a persisted rubric", async () => {
    const context = makeContext("math", "short_answer");
    context.rubricWorkflow.rubricDirty = true;
    await render(context);
    expect(host.querySelector('[role="status"]')?.textContent).toBe("有未保存修改");
    context.rubricWorkflow.rubricDirty = false;
    if (context.selectedQuestion) context.selectedQuestion.rubric = undefined;
    await render({ ...context });
    expect(host.querySelector('[role="status"]')?.textContent).toBe("尚未配置细则");
  });

  it("exposes separate draft and confirmation actions with explicit save targets", async () => {
    const context = makeContext("math", "short_answer");
    await act(async () => root.render(<PaperQuestionSaveActions context={context} />));
    const draft = Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "保存草稿");
    const confirm = Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "确认并锁定细则");
    expect(draft?.classList.contains("ant-btn-primary")).toBe(false);
    expect(confirm?.classList.contains("ant-btn-primary")).toBe(true);
    await act(async () => draft?.click());
    expect(context.actions.saveRubric).toHaveBeenLastCalledWith("draft");
    await act(async () => confirm?.click());
    expect(context.actions.saveRubric).toHaveBeenLastCalledWith("locked");
  });

  it.each(["saveBusy", "saveRefreshPending", "scoreMismatch", "assistantDirty"])("blocks both rubric save targets while %s", async (flag) => {
    const context = makeContext("math", "short_answer");
    if (flag === "saveBusy" || flag === "saveRefreshPending") context.state[flag] = true;
    else if (flag === "scoreMismatch") context.rubricWorkflow.scoreMismatch = true;
    else context.rubricWorkflow.assistantDirty = true;
    await act(async () => root.render(<PaperQuestionSaveActions context={context} />));
    const buttons = Array.from(host.querySelectorAll("button"));
    expect(buttons).toHaveLength(2);
    expect(buttons.every((button) => button.disabled)).toBe(true);
  });

  it("requires saving the edited question before confirmation while still allowing a rubric draft", async () => {
    const context = makeContext("math", "short_answer");
    context.state.formDirty = true;
    await act(async () => root.render(<><PaperQuestionSaveActions context={context} /><PaperScoringConfiguration context={context} /></>));
    const buttons = Array.from(host.querySelectorAll("button"));
    expect(buttons.find((button) => button.textContent === "保存草稿")?.disabled).toBe(false);
    expect(buttons.find((button) => button.textContent === "确认并锁定细则")?.disabled).toBe(true);
    expect(host.textContent).toContain("题目与答案有未保存修改，请先保存，再确认细则");
  });

  it("replaces save actions with a persistent confirmed state after locking", async () => {
    const context = makeContext("math", "short_answer", true, true);
    await act(async () => root.render(<><PaperQuestionSaveActions context={context} /><PaperScoringConfiguration context={context} /></>));
    expect(host.querySelector('[role="status"]')?.textContent).toBe("已确认并锁定");
    expect(host.textContent).toContain("本题内容和评分细则已锁定，不可编辑。");
    expect(Array.from(host.querySelectorAll("button")).some((button) => button.textContent === "保存草稿" || button.textContent === "确认并锁定细则")).toBe(false);
  });
});
