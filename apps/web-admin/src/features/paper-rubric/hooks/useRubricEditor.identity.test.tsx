// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Question, RubricPayload } from "../../../api/papers";
import { useRubricEditor } from "./useRubricEditor";

const mocks = vi.hoisted(() => ({ createRubric: vi.fn(), message: { success: vi.fn(), error: vi.fn() } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../../api/papers", () => ({ createRubric: mocks.createRubric }));

function question(points: RubricPayload["points"]): Question {
  return { id: "question", tenant_id: "tenant", exam_id: "exam", question_no: "1", question_type: "short_answer", score: 6,
    options: [], knowledge_points: [], sort_order: 1, status: "draft", rubric: { id: "rubric", question_id: "question", version: "1", points, status: "draft", max_score: 6, deductions: [], examples: [] } };
}
const points = () => [1, 2, 3].map(id => ({ id: `p${id}`, description: `步骤${id}`, score: 2, required: true }));

describe("采分点身份与保存边界", () => {
  beforeEach(() => vi.clearAllMocks());
  it("删掉中间采分点后新增保持剩余证据身份，并生成两个不同的新标识", () => {
    const selectedQuestion = question(points());
    const { result } = renderHook(() => useRubricEditor({ selectedQuestion, selectedExamId: "exam", loadConfig: vi.fn() }));
    act(() => result.current.removePoint(1));
    act(() => { result.current.addPoint(); result.current.addPoint(); });
    const ids = result.current.rubricPoints.map(point => point.id);
    expect(ids.slice(0, 2)).toEqual(["p1", "p3"]);
    expect(new Set(ids).size).toBe(4);
  });
  it("历史重复标识在准备及直接保存入口都被拒绝，不发出 API 请求", async () => {
    const duplicate = points(); duplicate[1].id = "p1";
    const selectedQuestion = question(duplicate);
    const { result } = renderHook(() => useRubricEditor({ selectedQuestion, selectedExamId: "exam", loadConfig: vi.fn() }));
    expect(() => result.current.prepareRubricSave()).toThrow("采分点标识重复");
    await expect(result.current.persistRubricSave("question", { status: "draft", max_score: 6, points: duplicate, deductions: [], examples: [] })).rejects.toThrow("采分点标识重复");
    expect(mocks.createRubric).not.toHaveBeenCalled();
  });
  it("未应用分段纳入离页保护并阻止保存，应用后仍为待确认草稿", () => {
    const selectedQuestion = question(points());
    selectedQuestion.rubric!.status = "approved";
    const { result } = renderHook(() => useRubricEditor({ selectedQuestion, selectedExamId: "exam", loadConfig: vi.fn() }));
    act(() => result.current.setAssistantDirty(true));
    expect(result.current.rubricDirty).toBe(true);
    expect(() => result.current.prepareRubricSave()).toThrow("请先应用分值划分草稿");
    act(() => result.current.replaceRubricPoints([{ id: "new", description: "分段过程", score: 6, required: false }]));
    expect(result.current.prepareRubricSave()).toMatchObject({ status: "draft", deductions: [], examples: [] });
    expect(result.current.assistantDirty).toBe(false);
    expect(result.current.rubricDirty).toBe(true);
    expect(() => result.current.replaceRubricPoints([{ id: "bad", description: "错误总分", score: 2, required: false }])).toThrow("合计必须等于");
    expect(mocks.createRubric).not.toHaveBeenCalled();
  });

  // 不同学校或考试可能复用题目标识，离页保护只能保留当前完整题目身份的草稿。
  it.each([
    ["题目", { id: "another-question" }],
    ["考试", { exam_id: "another-exam" }],
    ["学校", { tenant_id: "another-tenant" }]
  ])("切换%s时不会把原题未保存采分点带入新范围", (_, identity) => {
    const selectedQuestion = question(points());
    const { result, rerender } = renderHook(({ selectedQuestion }: { selectedQuestion: Question }) => useRubricEditor({
      selectedQuestion, selectedExamId: selectedQuestion.exam_id, preserveUnsaved: true, loadConfig: vi.fn()
    }), { initialProps: { selectedQuestion } });
    act(() => result.current.updatePoint(0, { description: "原题未保存修改" }));
    const nextPoints = [{ id: "new-scope-point", description: "新范围评分规则", score: 6, required: true }];
    rerender({ selectedQuestion: { ...selectedQuestion, ...identity, rubric: { ...selectedQuestion.rubric!, points: nextPoints } } });
    expect(result.current.rubricPoints).toEqual(nextPoints);
    expect(result.current.rubricDirty).toBe(false);
    expect(result.current.assistantDirty).toBe(false);
  });
});
