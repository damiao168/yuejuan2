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
  api.list.mockResolvedValue({ scoring_rules: [{ id: "published-44", status: "published", revision: 1, config: { loaded: true } }] });
});
afterEach(cleanup);

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
  api.create.mockResolvedValueOnce({ scoring_rule: { id: "retry-44", status: "draft", revision: 1, config: prepared.config } });
  await act(async () => {
    expect(await result.current.persistScoringRuleSave(question.id, prepared, false, { notify: false, reload: false })).toBe(true);
  });
  expect(api.create).toHaveBeenCalledTimes(2);
  expect(result.current.draftScoringRule?.id).toBe("retry-44");
  expect(result.current.savingScoringRule).toBe(false);
  expect(result.current.scoringRuleDirty).toBe(false);
  expect(api.publish).not.toHaveBeenCalled();
});
