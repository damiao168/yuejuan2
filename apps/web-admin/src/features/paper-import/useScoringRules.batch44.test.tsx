// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Question } from "../../api/papers";
import { useScoringRules } from "./useScoringRules";

const api = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), publish: vi.fn(),
  message: { success: vi.fn(), error: vi.fn() }
}));
vi.mock("../../api/papers", () => ({ listScoringRules: api.list, createScoringRule: api.create, updateScoringRule: api.update, publishScoringRule: api.publish }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: api.message }) } }));

const question: Question = {
  id: "question-44", tenant_id: "synthetic-school", exam_id: "exam-44", question_no: "1", question_type: "numeric",
  score: 5, stem: "synthetic numeric question", options: [], knowledge_points: [], sort_order: 1, status: "draft"
};

beforeEach(() => {
  vi.resetAllMocks();
  api.list.mockResolvedValue({ scoring_rules: [{ id: "published-44", question_id: question.id, status: "published", revision: 1, config: { loaded: true } }] });
});
afterEach(cleanup);

it("新多选规则默认少选与错选均为0，编辑后的固定分真正写入评分规则", async () => {
  api.list.mockResolvedValue({ scoring_rules: [] });
  const multiple = { ...question, question_type: "multiple_choice" };
  const { result } = renderHook(() => useScoringRules({ selectedQuestion: multiple, objectiveRuleType: "multiple_choice" }));
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  expect(result.current.scoringRuleConfig).toEqual({ partial_score: 0, wrong_answer_score: 0 });
  act(() => { result.current.setRuleConfig("partial_score", 2); result.current.setRuleConfig("wrong_answer_score", 1); });
  const prepared = result.current.prepareScoringRuleSave();
  api.create.mockResolvedValue({ scoring_rule: { id: "fixed-rule", question_id: multiple.id, status: "draft", revision: 1, config: prepared.config } });
  await act(async () => { await result.current.persistScoringRuleSave(multiple.id, prepared, false, { notify: false, reload: false }); });
  expect(api.create).toHaveBeenCalledWith(multiple.id, "multiple_choice", { max_score: 5, partial_score: 2, wrong_answer_score: 1 });
});

it("仅答案容差有历史多选政策时保留旧语义，明确切换后才保存固定得分", async () => {
  api.list.mockResolvedValue({ scoring_rules: [] });
  const legacy = { allow_partial: true, score_per_correct_option: 1, wrong_option_penalty: 2, minimum_score: 0 };
  const multiple = { ...question, question_type: "multiple_choice", answer_key: { id: "answer", question_id: question.id, answer_version: "1", standard_answer: ["A", "B"], equivalent_answers: [], tolerance: legacy } };
  const { result } = renderHook(() => useScoringRules({ selectedQuestion: multiple, objectiveRuleType: "multiple_choice" }));
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  expect(result.current.scoringRuleConfig).toEqual(legacy);
  expect(result.current.prepareScoringRuleSave().config).toEqual({ ...legacy, max_score: 5 });
  act(() => { result.current.setRuleConfig("partial_score", 2); result.current.setRuleConfig("wrong_answer_score", 0); });
  expect(result.current.prepareScoringRuleSave().config).toEqual({ ...legacy, max_score: 5, partial_score: 2, wrong_answer_score: 0 });
  expect(multiple.answer_key.tolerance).toEqual(legacy);
});

it.each(["partial_score", "wrong_answer_score"])("多选固定分%s不能超出当前题目分值", async (key) => {
  api.list.mockResolvedValue({ scoring_rules: [] });
  const multiple = { ...question, question_type: "multiple_choice" };
  const { result } = renderHook(() => useScoringRules({ selectedQuestion: multiple, objectiveRuleType: "multiple_choice" }));
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  for (const invalid of [-1, 5.01, Number.NaN, null, "2"]) {
    act(() => result.current.setRuleConfig(key, invalid));
    expect(() => result.current.prepareScoringRuleSave()).toThrow("不在本次题目分值允许的范围内");
  }
  expect(api.create).not.toHaveBeenCalled();
});

it("numeric 只校验误差字段，保留其他评分配置并拒绝超界相对误差", async () => {
  const { result } = renderHook(() => useScoringRules({ selectedQuestion: question, objectiveRuleType: "numeric" }));
  await waitFor(() => expect(result.current.scoringRuleConfig.loaded).toBe(true));
  act(() => {
    result.current.setRuleConfig("wrong_option_penalty", 999);
    result.current.setRuleConfig("absolute", 0.5);
    result.current.setRuleConfig("relative", 1);
  });
  const prepared = result.current.prepareScoringRuleSave();
  expect(prepared.config).toMatchObject({ max_score: 5, wrong_option_penalty: 999, absolute: 0.5, relative: 1 });
  expect(prepared.draft).toBeUndefined();
  act(() => result.current.setRuleConfig("relative", 1.1));
  expect(() => result.current.prepareScoringRuleSave()).toThrow("相对误差不在本次题目分值允许的范围内，请核对评分配置");
  expect(api.create).not.toHaveBeenCalled();
});

it("评分写入失败保留原异常并释放保存锁，再次保存能完成草稿写入", async () => {
  const { result } = renderHook(() => useScoringRules({ selectedQuestion: question, objectiveRuleType: "numeric" }));
  await waitFor(() => expect(result.current.scoringRuleConfig.loaded).toBe(true));
  const prepared = result.current.prepareScoringRuleSave();
  const cause = new Error("synthetic scoring write failure");
  let rejectWrite!: (cause: unknown) => void;
  api.create.mockReturnValueOnce(new Promise((_resolve, reject) => { rejectWrite = reject; }));
  let pending!: Promise<boolean>;
  act(() => { pending = result.current.persistScoringRuleSave(question.id, prepared, false, { notify: false, reload: false }); });
  expect(result.current.savingScoringRule).toBe(true);
  await act(async () => {
    expect(await result.current.persistScoringRuleSave(question.id, prepared, false, { notify: false, reload: false })).toBe(false);
  });
  expect(api.create).toHaveBeenCalledTimes(1);

  // 首次请求未完成时阻止重入；收到拒绝后，同一真实入口必须解除锁并允许重试。
  const rejected = expect(pending).rejects.toBe(cause);
  await act(async () => { rejectWrite(cause); await rejected; });
  expect(result.current.savingScoringRule).toBe(false);
  api.create.mockResolvedValueOnce({ scoring_rule: { id: "retry-44", question_id: question.id, status: "draft", revision: 1, config: prepared.config } });
  await act(async () => {
    expect(await result.current.persistScoringRuleSave(question.id, prepared, false, { notify: false, reload: false })).toBe(true);
  });
  expect(api.create).toHaveBeenCalledTimes(2);
  expect(result.current.draftScoringRule?.id).toBe("retry-44");
  expect(result.current.savingScoringRule).toBe(false);
  expect(result.current.scoringRuleDirty).toBe(false);
  expect(api.publish).not.toHaveBeenCalled();
});


it("换题读取失败不能保存上一题草稿，成功重读后只更新当前题规则", async () => {
  const other = { ...question, id: "other-question", score: 9 };
  api.list.mockResolvedValueOnce({ scoring_rules: [{ id: "rule-A", question_id: question.id, status: "draft", revision: 2, config: { absolute: .1 } }] });
  const { result, rerender } = renderHook(({ selected }) => useScoringRules({ selectedQuestion: selected, objectiveRuleType: "numeric" }), { initialProps: { selected: question } });
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  const previous = result.current.prepareScoringRuleSave();
  api.list.mockRejectedValueOnce(new Error("B read failed")); rerender({ selected: other });
  await waitFor(() => expect(result.current.scoringRulesError).toContain("评分规则读取失败"));
  expect(result.current.scoringRules).toEqual([]); expect(result.current.scoringRuleConfig).toEqual({});
  expect(() => result.current.prepareScoringRuleSave()).toThrow("成功读取");
  await expect(result.current.persistScoringRuleSave(other.id, previous, false)).rejects.toThrow("所属题目已变化");
  expect(api.update).not.toHaveBeenCalled(); expect(api.create).not.toHaveBeenCalled();
  const target = { id: "rule-B", question_id: other.id, status: "draft", revision: 4, config: { absolute: .25 } };
  api.list.mockResolvedValue({ scoring_rules: [target] }); act(() => result.current.reloadScoringRules());
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  api.update.mockResolvedValue({ scoring_rule: { ...target, revision: 5 } });
  await act(async () => { await result.current.persistScoringRuleSave(other.id, result.current.prepareScoringRuleSave(), false, { reload: false }); });
  expect(api.update).toHaveBeenCalledWith("rule-B", { absolute: .25, max_score: 9 }, 4);
});

it("切题后迟到的规则写入回执不能回填到当前题", async () => {
  api.list.mockResolvedValue({ scoring_rules: [] });
  const { result, rerender } = renderHook(({ selected }) => useScoringRules({ selectedQuestion: selected, objectiveRuleType: "numeric" }), { initialProps: { selected: question } });
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  let release!: (value: unknown) => void; api.create.mockReturnValue(new Promise(resolve => { release = resolve; }));
  let saving!: Promise<boolean>; act(() => { saving = result.current.persistScoringRuleSave(question.id, result.current.prepareScoringRuleSave(), false); });
  const other = { ...question, id: "other-question" }; rerender({ selected: other });
  await waitFor(() => expect(result.current.scoringRulesReadable).toBe(true));
  await act(async () => { release({ scoring_rule: { id: "rule-A", question_id: question.id, status: "draft", revision: 1, config: {} } }); await saving; });
  expect(result.current.scoringRules).toEqual([]); expect(api.publish).not.toHaveBeenCalled();
});
