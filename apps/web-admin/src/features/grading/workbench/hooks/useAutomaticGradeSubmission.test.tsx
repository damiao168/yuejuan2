// @vitest-environment jsdom
import { useRef, useState } from "react";
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScoreDraft } from "../gradingWorkbench.types";
import { useAutomaticGradeSubmission, useScoringDraftIntent } from "./useAutomaticGradeSubmission";

type Submit = (taskId: string, score: number | null) => Promise<void>;

interface HarnessOptions {
  taskId: string;
  ready: boolean;
  marked: boolean;
  submit: Submit;
}

function draftWithScore(score: number | null): ScoreDraft {
  return { score, comments: "", privateNote: "", studentFeedback: "", reason: "", disputeReason: "", rubricSelections: {}, answerText: "" };
}

function mount(initialScore: number | null = null, overrides: Partial<HarnessOptions> = {}) {
  const submit = vi.fn<Submit>().mockResolvedValue(undefined);
  const initialProps: HarnessOptions = { taskId: "task-a", ready: true, marked: false, submit, ...overrides };
  const view = renderHook(({ taskId, ready, marked, submit: submitCurrent }: HarnessOptions) => {
    const [draft, setDraft] = useState(() => draftWithScore(initialScore));
    const liveDraft = useRef(draft);
    liveDraft.current = draft;
    const { edit, updateDraft } = useScoringDraftIntent(taskId, liveDraft, setDraft);
    useAutomaticGradeSubmission({ taskId, edit, ready, marked, submit: () => submitCurrent(taskId, liveDraft.current.score) });
    return { draft, edit, updateDraft, restoreDraft: setDraft };
  }, { initialProps });
  return { ...view, initialProps, submit };
}

async function advance(milliseconds: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(milliseconds); });
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("最终分编辑后的自动提交", () => {
  it("打开已有分数或恢复旧草稿时没有编辑意图，不自动提交", async () => {
    const view = mount(7);
    expect(view.result.current.edit).toBeNull();
    await advance(5000);
    // 恢复服务端或本地草稿走原始 setter，不能冒充本次教师操作。
    act(() => view.result.current.restoreDraft({ ...draftWithScore(8), rubricSelections: { point: 8 } }));
    await advance(5000);
    expect(view.result.current.draft.score).toBe(8);
    expect(view.result.current.edit).toBeNull();
    expect(view.submit).not.toHaveBeenCalled();
  });

  it.each([0, 8, 4.5])("调用方确认分数 %s 可提交后等待1200ms，同一次编辑只提交一次", async (score) => {
    const view = mount();
    act(() => view.result.current.updateDraft((current) => ({ ...current, score })));
    await advance(1199);
    expect(view.submit).not.toHaveBeenCalled();
    await advance(1);
    expect(view.submit).toHaveBeenCalledExactlyOnceWith("task-a", score);
    view.rerender({ ...view.initialProps, ready: false });
    view.rerender(view.initialProps);
    await advance(5000);
    expect(view.submit).toHaveBeenCalledTimes(1);
  });

  it.each([true, false])("标记阻止自动提交，取消标记后重新等待（编辑前已标记=%s）", async (markedBeforeEdit) => {
    const view = mount(null, { marked: markedBeforeEdit });
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 6 })));
    if (!markedBeforeEdit) {
      await advance(1000);
      view.rerender({ ...view.initialProps, marked: true });
    }
    await advance(5000);
    expect(view.submit).not.toHaveBeenCalled();
    view.rerender({ ...view.initialProps, marked: false });
    await advance(1199);
    expect(view.submit).not.toHaveBeenCalled();
    await advance(1);
    expect(view.submit).toHaveBeenCalledExactlyOnceWith("task-a", 6);
  });

  it("连续输入让旧计时器失效，只提交最后一次完整分数", async () => {
    const view = mount();
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 1 })));
    await advance(800);
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 15 })));
    await advance(400);
    expect(view.submit).not.toHaveBeenCalled();
    await advance(799);
    expect(view.submit).not.toHaveBeenCalled();
    await advance(1);
    expect(view.submit).toHaveBeenCalledExactlyOnceWith("task-a", 15);
  });

  it("切换任务后取消旧计时器，恢复新任务草稿也不继承旧编辑意图", async () => {
    const view = mount();
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 7 })));
    await advance(1000);
    const nextProps = { ...view.initialProps, taskId: "task-b" };
    view.rerender(nextProps);
    act(() => view.result.current.restoreDraft(draftWithScore(3)));
    await advance(5000);
    expect(view.submit).not.toHaveBeenCalled();
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 4 })));
    await advance(1200);
    expect(view.submit).toHaveBeenCalledExactlyOnceWith("task-b", 4);
  });

  it("未就绪时等待，就绪中断后重新累计完整1200ms", async () => {
    const view = mount(null, { ready: false });
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 5 })));
    await advance(5000);
    expect(view.submit).not.toHaveBeenCalled();
    view.rerender({ ...view.initialProps, ready: true });
    await advance(600);
    view.rerender(view.initialProps);
    await advance(5000);
    expect(view.submit).not.toHaveBeenCalled();
    view.rerender({ ...view.initialProps, ready: true });
    await advance(1199);
    expect(view.submit).not.toHaveBeenCalled();
    await advance(1);
    expect(view.submit).toHaveBeenCalledExactlyOnceWith("task-a", 5);
  });

  it("调用方处理提交失败后同一次编辑不反复重试，修改分数后允许再次提交", async () => {
    const failure = new Error("保存失败");
    const api = vi.fn<Submit>().mockResolvedValue(undefined).mockRejectedValueOnce(failure);
    const handledErrors: unknown[] = [];
    const view = mount();
    // 实际提交动作会处理错误并更新界面；返回完成不代表服务端已成功写分。
    view.submit.mockImplementation(async (taskId, score) => {
      try { await api(taskId, score); } catch (error) { handledErrors.push(error); }
    });
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 5 })));
    await advance(1200);
    expect(handledErrors).toEqual([failure]);
    view.rerender({ ...view.initialProps, ready: false });
    view.rerender(view.initialProps);
    await advance(10000);
    expect(api).toHaveBeenCalledExactlyOnceWith("task-a", 5);
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 6 })));
    await advance(1200);
    expect(api).toHaveBeenCalledTimes(2);
    expect(api).toHaveBeenLastCalledWith("task-a", 6);
  });
});

describe("分数编辑意图", () => {
  it("非分数字段和相同评分值不产生意图，最终分或评分点实际变化才递增序号", async () => {
    const view = mount(5, { ready: false });
    act(() => view.result.current.updateDraft({
      ...view.result.current.draft, comments: "教师评语", privateNote: "内部备注", studentFeedback: "学生反馈",
      reason: "原因", disputeReason: "争议", answerText: "答案", rubricSelections: {}
    }));
    await advance(5000);
    expect(view.result.current.edit).toBeNull();
    expect(view.result.current.draft.comments).toBe("教师评语");
    act(() => view.result.current.updateDraft((current) => ({ ...current, score: 6 })));
    const scoreEdit = view.result.current.edit;
    expect(scoreEdit).toEqual({ taskId: "task-a", sequence: 1 });
    act(() => view.result.current.updateDraft((current) => ({ ...current, comments: "补充评语", rubricSelections: {} })));
    expect(view.result.current.edit).toBe(scoreEdit);
    act(() => view.result.current.updateDraft((current) => ({ ...current, rubricSelections: { point: 2 } })));
    const pointEdit = view.result.current.edit;
    expect(pointEdit).toEqual({ taskId: "task-a", sequence: 2 });
    act(() => view.result.current.updateDraft((current) => ({ ...current, rubricSelections: { point: 2 } })));
    expect(view.result.current.edit).toBe(pointEdit);
    expect(view.submit).not.toHaveBeenCalled();
  });
});
