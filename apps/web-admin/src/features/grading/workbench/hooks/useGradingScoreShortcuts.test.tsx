// @vitest-environment jsdom
import { useRef, useState } from "react";
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createInitialDraft } from "../gradingWorkbench.model";
import { useLiveScoreDraft } from "./useLiveScoreDraft";
import { useGradingScoreShortcuts } from "./useGradingScoreShortcuts";

function mount() {
  const view = renderHook(() => {
    const [draft, setDraft] = useState(() => createInitialDraft(null));
    const liveDraft = useRef(draft); liveDraft.current = draft;
    const updateDraft = useLiveScoreDraft(liveDraft, setDraft);
    const shortcuts = useGradingScoreShortcuts({ draft, setDraft: updateDraft, rubricPoints: [], onInfo: vi.fn() });
    return { draft, shortcuts };
  });
  return view;
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("未提交评分的撤销", () => {
  it("直接填分也可撤销，随后修改评语不会覆盖分数记录或误提交", async () => {
    const view = mount();
    act(() => view.result.current.shortcuts.updateDraft((current) => ({ ...current, score: 0 })));
    act(() => view.result.current.shortcuts.updateDraft((current) => ({ ...current, comments: "保留教师评语" })));
    expect(view.result.current.shortcuts.canUndo).toBe(true);
    act(() => view.result.current.shortcuts.undo());
    expect(view.result.current.draft.score).toBeNull();
    expect(view.result.current.draft.comments).toBe("保留教师评语");
    expect(view.result.current.shortcuts.canUndo).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(view.result.current.draft.score).toBeNull();
  });

  it("快捷评分和直接输入共用最后一笔记录，重置后不能撤销旧任务", () => {
    const view = mount();
    act(() => view.result.current.shortcuts.setScore(3));
    act(() => view.result.current.shortcuts.updateDraft((current) => ({ ...current, score: 8 })));
    act(() => view.result.current.shortcuts.undo());
    expect(view.result.current.draft.score).toBe(3);
    act(() => view.result.current.shortcuts.setScore(5));
    act(() => view.result.current.shortcuts.reset());
    act(() => view.result.current.shortcuts.undo());
    expect(view.result.current.draft.score).toBe(5);
    expect(view.result.current.shortcuts.canUndo).toBe(false);
  });
});
