// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../api/exams";
import type { PaperImportJob, Question } from "../api/papers";
import { PaperRubricPage } from "./PaperRubricPage";

const mocks = vi.hoisted(() => ({
  config: {} as Record<string, unknown>, importState: {} as Record<string, unknown>,
  loadConfig: vi.fn(), loadExams: vi.fn(), updateQuestion: vi.fn(), createQuestion: vi.fn(), deleteQuestion: vi.fn(),
  createRubric: vi.fn(), listScoringRules: vi.fn(), fillRecognizedContent: vi.fn(), confirm: vi.fn(),
  warning: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn(),
  createScoringRule: vi.fn(), updateScoringRule: vi.fn(), publishScoringRule: vi.fn()
}));

vi.mock("antd", async (importOriginal) => {
  const original = await importOriginal<typeof import("antd")>();
  const app = { message: { warning: mocks.warning, success: mocks.success, error: mocks.error, info: mocks.info }, modal: { confirm: mocks.confirm } };
  return { ...original, App: { useApp: () => app } };
});
vi.mock("../api/papers", () => ({
  validatePaperConfig: vi.fn(), updateQuestion: mocks.updateQuestion, createQuestion: mocks.createQuestion,
  deleteQuestion: mocks.deleteQuestion, createRubric: mocks.createRubric, listScoringRules: mocks.listScoringRules,
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
describe("paper rubric page state and draft boundaries", { timeout: testTimeout }, () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.clearAllMocks();
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
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [] });
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
    const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent?.includes(label));
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
    const input = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector)!;
    if (!input) throw new Error(`Missing field ${selector}`);
    await act(async () => {
      Object.getOwnPropertyDescriptor(input.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("validates both regions before writing either when the newly entered score mismatches the rubric", async () => {
    await render(); await editStem("unsaved full question"); await editInput("input#score", "8");
    await click("保存本题全部修改");
    expect(mocks.updateQuestion).not.toHaveBeenCalled(); expect(mocks.createRubric).not.toHaveBeenCalled();
    expect(host.textContent).toContain("本题尚未保存");
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("unsaved full question");
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
    await click("保存评分细则");
    expect(host.textContent).toContain("评分细则第 2 版已保存");
    expect(host.textContent).toContain("无需再次保存评分细则");
    expect(host.querySelector<HTMLTextAreaElement>(".paper-rubric-description textarea")?.value).toBe("分区保存的评分条件");
    expect(host.querySelector<HTMLTextAreaElement>("textarea#stem")?.value).toBe("尚未保存的题目草稿");
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true, propagateError: true });
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "保存评分细则")?.disabled).toBe(true);
    mocks.loadConfig.mockRejectedValueOnce(new Error("second read unavailable"));
    await click("重新读取保存结果");
    expect(host.textContent).toContain("评分细则第 2 版已保存");
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
    await click("保存评分细则");
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
  });

  it("rejects a fixed-rule value above the new maximum before updating the question", async () => {
    mocks.config.questions = [{ ...question("question-1", ["A"]), question_type: "multiple_choice", options: ["甲", "乙"] }];
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [{ id: "rule-1", status: "draft", revision: 2, config: { wrong_option_penalty: 5 } }] });
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

  it("mounts school selection on the embedded question page and curated import after freezing", async () => {
    const page = <PaperRubricPage canManage canManageAssessment={false} canReadQuestionBank canImportQuestionBank mode="embedded" initialView="questions" fixedExamId="exam-1" />;
    await act(async () => root.render(page)); expect(host.querySelector("#paper-question-summary")?.textContent).toContain("从学校题库选题");
    expect(host.textContent).not.toContain("将本次考试题目精选入题库");
    mocks.config.exams = [{ id: "exam-1", subject: "math", status: "completed", total_score: 10, name: "synthetic exam" } as Exam];
    await act(async () => root.render(<PaperRubricPage canManage canManageAssessment={false} canReadQuestionBank canImportQuestionBank mode="embedded" initialView="questions" fixedExamId="exam-1" />));
    expect(host.querySelector("#paper-question-summary")?.textContent).toContain("将本次考试题目精选入题库"); expect(host.textContent).not.toContain("从学校题库选题");
  });

  it("keeps the same form field and unsaved value across content/scoring tab changes", async () => {
    await render();
    const field = await editStem("unsaved synthetic stem");
    await click("评分配置");
    expect(host.querySelector("textarea#stem")).toBe(field);
    expect(field.value).toBe("unsaved synthetic stem");
    expect(host.querySelector<HTMLDivElement>(".question-editor > div[style]")?.style.display).toBe("none");
    await click("保存评分细则");
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
    await click("保存评分细则");
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
