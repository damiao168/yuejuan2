// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import type { Question } from "../../../api/papers";
import { useGroupedQuestionScores } from "./useGroupedQuestionScores";

const mocks = vi.hoisted(() => ({ confirm: vi.fn(), warning: vi.fn(), success: vi.fn(),
  listQuestions: vi.fn(), listScoringRules: vi.fn(), updateQuestion: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ modal: { confirm: mocks.confirm }, message: { warning: mocks.warning, success: mocks.success } }) } }));
vi.mock("../../../api/papers", () => ({ listQuestions: mocks.listQuestions, listScoringRules: mocks.listScoringRules, updateQuestion: mocks.updateQuestion }));

function question(id: string, score = 2): Question {
  return { id, exam_id: "exam", tenant_id: "tenant", question_no: id, question_type: "single_choice", score,
    options: ["A", "B"], knowledge_points: [], sort_order: 1, status: "draft" };
}

describe("统一分值保存保护与失败恢复", () => {
  let root: Root;
  let host: HTMLDivElement;
  let workflow: ReturnType<typeof useGroupedQuestionScores>;
  let options: Parameters<typeof useGroupedQuestionScores>[0];
  const loadConfig = vi.fn();
  const changed = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const questions = [question("1"), question("2")];
    options = { selectedExam: { id: "exam", status: "draft" } as Exam, questions, canManage: true, hasUnsavedWork: false,
      isMutationBlocked: () => false, runMutation: async (action) => { await action(); }, loadConfig, onChanged: changed };
    mocks.listQuestions.mockResolvedValue({ questions });
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [] });
    mocks.updateQuestion.mockResolvedValue({});
    loadConfig.mockResolvedValue(undefined);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  function Harness() { workflow = useGroupedQuestionScores(options); return null; }
  async function render() { await act(async () => root.render(<Harness />)); }
  async function propose() {
    await render();
    await act(async () => workflow.setScore("single_choice", 4));
    await act(async () => workflow.applyUniformScore(workflow.groups[0]));
  }
  const confirmation = () => mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0];
  async function confirm() { await act(async () => { await confirmation().onOk(); }); }

  it("显式确认后只更新分值，未改答案；回读后通知工作区并保留逐题输入建议", async () => {
    await propose();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    await confirm();
    expect(mocks.updateQuestion.mock.calls).toEqual([["1", { score: 4 }], ["2", { score: 4 }]]);
    expect(loadConfig).toHaveBeenCalledWith("exam", { silent: true, propagateError: true });
    expect(changed).toHaveBeenCalledOnce();
    expect(workflow.result?.text).toContain("已保存 2 题，未保存 0 题");
    options = { ...options, questions: [question("1", 4), question("2", 5)] };
    await render();
    expect(workflow.scoreForGroup(workflow.groups[0])).toBe(4);
    expect(options.questions[1].score).toBe(5);
  });

  it("逐题非原子保存遇错停止并明确已成功部分，读取失败不重放PATCH", async () => {
    options.questions = [...options.questions, question("3")];
    mocks.listQuestions.mockResolvedValue({ questions: options.questions });
    mocks.updateQuestion.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("第二题失败"));
    loadConfig.mockRejectedValue(new Error("读取失败"));
    await propose(); await confirm();
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    expect(workflow.result?.text).toContain("已确认保存 1 题，剩余 2 题保存状态");
    expect(workflow.result?.text).toContain("回读失败");
    expect(changed).toHaveBeenCalledOnce();
    expect(workflow.result?.refreshPending).toBe(true);
    await act(async () => workflow.applyUniformScore(workflow.groups[0]));
    expect(mocks.confirm).toHaveBeenCalledOnce();
    loadConfig.mockResolvedValue(undefined);
    await act(async () => workflow.rereadSavedResults());
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    expect(workflow.result?.refreshPending).toBe(false);
  });

  it.each(["readonly", "frozen", "unsaved", "saving"])("%s 时不能打开批量确认", async (reason) => {
    if (reason === "readonly") options.canManage = false;
    if (reason === "frozen") options.selectedExam = { id: "exam", status: "ready" } as Exam;
    if (reason === "unsaved") options.hasUnsavedWork = true;
    if (reason === "saving") options.isMutationBlocked = () => true;
    await propose();
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
  });

  it("已发布规则或预览后分值变化时在任何PATCH前阻断", async () => {
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [{ status: "published" }] });
    await propose(); await confirm();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(workflow.result?.text).toContain("已有发布的评分规则");
    mocks.listScoringRules.mockResolvedValue({ scoring_rules: [] });
    mocks.listQuestions.mockResolvedValue({ questions: [question("1", 7), question("2")] });
    await act(async () => workflow.applyUniformScore(workflow.groups[0]));
    await confirm();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(workflow.result?.text).toContain("题目或分值已变化");
  });

  it("确认窗口打开后换考试不得保存旧题；请求中换考试不得回读或提示新考试", async () => {
    await propose();
    options = { ...options, selectedExam: { id: "other", status: "draft" } as Exam };
    await render(); await confirm();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    options = { ...options, selectedExam: { id: "exam", status: "draft" } as Exam };
    await render(); await act(async () => workflow.applyUniformScore(workflow.groups[0]));
    let resolve: (value: { questions: Question[] }) => void;
    mocks.listQuestions.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    let pending: Promise<void>;
    await act(async () => { pending = confirmation().onOk(); });
    options = { ...options, selectedExam: { id: "other", status: "draft" } as Exam };
    await render();
    await act(async () => { resolve({ questions: [question("1"), question("2")] }); await pending; });
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(loadConfig).not.toHaveBeenCalled();
    expect(workflow.result).toBeNull();
  });

  it("切到其他考试再返回后，旧确认窗口仍不能恢复提交", async () => {
    await propose();
    options = { ...options, selectedExam: { id: "other", status: "draft" } as Exam };
    await render();
    options = { ...options, selectedExam: { id: "exam", status: "draft" } as Exam };
    await render(); await confirm();
    expect(mocks.listQuestions).not.toHaveBeenCalled();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
  });

  async function proposeMultipleGroups() {
    options.questions = [question("1"), { ...question("2", 3), question_type: "multiple_choice" }];
    mocks.listQuestions.mockResolvedValue({ questions: options.questions });
    await render();
    await act(async () => { workflow.setScore("single_choice", 4); workflow.setScore("multiple_choice", 6); });
    await act(async () => workflow.applyAllScores());
  }

  it("多题型一次确认，只写已修改分值且整批只回读一次", async () => {
    await proposeMultipleGroups();
    expect(workflow.dirty).toBe(true);
    expect(mocks.confirm).toHaveBeenCalledOnce();
    await confirm();
    expect(mocks.updateQuestion.mock.calls).toEqual([["1", { score: 4 }], ["2", { score: 6 }]]);
    expect(loadConfig).toHaveBeenCalledOnce();
  });

  it("后面的题型已有发布规则时，前面的题型也不能提前写入", async () => {
    mocks.listScoringRules.mockImplementation(async id => ({ scoring_rules: id === "2" ? [{ status: "published" }] : [] }));
    await proposeMultipleGroups(); await confirm();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    expect(workflow.result?.text).toContain("已有发布的评分规则");
  });

  it("跨题型部分失败保留成功计数，恢复读取不重放已提交分值", async () => {
    mocks.updateQuestion.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("多选题保存失败"));
    loadConfig.mockRejectedValueOnce(new Error("回读失败"));
    await proposeMultipleGroups(); await confirm();
    expect(workflow.result?.text).toContain("已确认保存 1 题，剩余 1 题");
    expect(workflow.result?.refreshPending).toBe(true);
    await act(async () => workflow.rereadSavedResults());
    expect(mocks.updateQuestion).toHaveBeenCalledTimes(2);
    expect(workflow.result?.refreshPending).toBe(false);
  });

  it("确认后改动目标分值会使旧确认失效，恢复可清除所有题型草稿", async () => {
    await proposeMultipleGroups();
    await act(async () => workflow.setScore("multiple_choice", 7));
    await confirm();
    expect(mocks.updateQuestion).not.toHaveBeenCalled();
    await act(async () => workflow.discardChanges());
    expect(workflow.dirty).toBe(false);
    expect(workflow.scoreForGroup(workflow.groups[1])).toBe(3);
  });

  it("任一已编辑题型缺失分值时，集中保存不能默默跳过它", async () => {
    await proposeMultipleGroups(); mocks.confirm.mockClear();
    await act(async () => workflow.setScore("multiple_choice", null));
    expect(workflow.hasInvalidDraft).toBe(true);
    await act(async () => workflow.applyAllScores());
    expect(mocks.confirm).not.toHaveBeenCalled();
  });
});
