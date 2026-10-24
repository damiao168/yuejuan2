// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import type { ReviewTaskContext } from "../../../api/review";
import { GradingToolbarContext } from "../../../components/GradingToolbarContext";
import { AIContextPanel } from "./AIContextPanel";
import { WorkbenchHeader, type WorkbenchHeaderProps } from "./components/WorkbenchHeader";
import { createInitialDraft, fallbackSnapshot } from "./gradingWorkbench.model";
import type { ScoreDraft } from "./gradingWorkbench.types";

vi.mock("../../../components/BackmarkQueue", () => ({ BackmarkQueue: () => <span>退回队列</span> }));
vi.mock("../../../components/RegradeQueue", () => ({ RegradeQueue: () => <span>重评队列</span> }));

// jsdom 不提供媒体查询；这里仅补组件挂载接口，不验证真实响应式布局。
beforeAll(() => {
  vi.stubGlobal("matchMedia", vi.fn((media: string) => ({
    matches: false, media, onchange: null,
    addListener: vi.fn(), removeListener: vi.fn(),
    addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(() => false)
  })));
});
afterEach(cleanup);
afterAll(() => vi.unstubAllGlobals());

function panelContext(explicit = false): ReviewTaskContext {
  return {
    question_snapshot: { risk_tier: explicit ? "R3" : "R2", scoring_policy_snapshot: { mode: "HUMAN_PRIMARY" } },
    ai_second_opinion: {
      available: true,
      presentation: explicit ? "explicit_second_opinion" : "inline",
      score_prefill_allowed: !explicit,
      metadata: { model_version: "MODEL_SECRET", confidence: 0, risks: ["low_confidence"] }
    },
    ai_candidates: [{
      id: "candidate", source: "ai", engine_version: "ENGINE_SECRET", profile_version: "profile",
      display_text: "候选秘密", confidence: 0, is_current: true, evidence: { fact: "evidence" }
    }],
    scoring_evidence: [{ id: "evidence", quality: 0.85 }]
  } as unknown as ReviewTaskContext;
}

describe("batch30 candidate presentation boundaries", () => {
  it("keeps the entire candidate and evidence panel hidden until explicit reveal", () => {
    const context = panelContext(true);
    const onReveal = vi.fn();
    const view = render(<AIContextPanel context={context} visible={false} onReveal={onReveal} />);
    expect(screen.queryByText("MODEL_SECRET")).toBeNull();
    expect(screen.queryByText(/ENGINE_SECRET/)).toBeNull();
    expect(screen.queryByText(/评分证据/)).toBeNull();
    expect(screen.queryByText(/个候选/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看第二意见" }));
    expect(onReveal).toHaveBeenCalledTimes(1);
    view.rerender(<AIContextPanel context={context} visible onReveal={onReveal} />);
    expect(screen.getByText("MODEL_SECRET")).toBeTruthy();
    fireEvent.click(screen.getByText(/ENGINE_SECRET/));
    expect(screen.getByText("候选秘密")).toBeTruthy();
    expect(screen.getByText("置信度较低")).toBeTruthy();
  });

  it("does not offer reveal when the explicit second opinion is unavailable", () => {
    const context = panelContext(true);
    context.ai_second_opinion!.available = false;
    render(<AIContextPanel context={context} visible={false} onReveal={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "查看第二意见" })).toBeNull();
    expect(screen.queryByText(/ENGINE_SECRET/)).toBeNull();
  });

  it.each([false, true])("does not expose old metadata or candidates after a current failure (revealed=%s)", (visible) => {
    const context = panelContext(true);
    context.ai_failure = { failure_kind: "output_truncated" };
    const onReveal = vi.fn();
    render(<AIContextPanel context={context} visible={visible} onReveal={onReveal} />);
    expect(screen.queryByText("MODEL_SECRET")).toBeNull();
    expect(screen.queryByText(/ENGINE_SECRET/)).toBeNull();
    expect(screen.queryByText(/评分证据/)).toBeNull();
    expect(screen.queryByRole("button", { name: "查看第二意见" })).toBeNull();
    expect(onReveal).not.toHaveBeenCalled();
  });

  it.each([[false, false], [false, true], [true, false], [true, true]])(
    "keeps candidate/metadata presence distinct: candidates=%s metadata=%s", (hasCandidates, hasMetadata) => {
      const context = panelContext();
      if (!hasCandidates) context.ai_candidates = [];
      context.ai_second_opinion!.available = hasMetadata;
      render(<AIContextPanel context={context} visible={false} onReveal={vi.fn()} />);
      expect(Boolean(screen.queryByText(/ENGINE_SECRET/))).toBe(hasCandidates);
      expect(Boolean(screen.queryByText("MODEL_SECRET"))).toBe(hasMetadata);
      expect(Boolean(screen.queryByText("暂无 AI 候选，按评分细则人工评分"))).toBe(!hasCandidates && !hasMetadata);
    }
  );
});

function headerProps(): WorkbenchHeaderProps {
  return {
    canWork: true, canManageTasks: false, queueScope: "mine", hasContext: true, ownsSelectedTask: true,
    reviewerProgress: [], remainingCount: 1, progressComplete: true, draftSaveStatus: "saved", loading: false, actioning: null,
    onRefresh: vi.fn(async () => undefined), onNext: vi.fn(async () => undefined),
    onClaim: vi.fn(async () => undefined), onRelease: vi.fn(async () => undefined),
    onReloadConflict: vi.fn(async () => undefined), onResolveConflict: vi.fn(async () => undefined)
  };
}

describe("batch30 header permission and callback boundaries", () => {
  it("将简洁题目和操作移到外壳顶栏，告警仍留在评分正文且操作回调正常", () => {
    const target = document.createElement("div"); document.body.append(target);
    const props = headerProps();
    const view = render(<GradingToolbarContext.Provider value={target}><WorkbenchHeader {...props} taskLabel="18" scoreLabel="未评分" draftSaveStatus="offline" /></GradingToolbarContext.Provider>);
    try {
      expect(view.container.querySelector(".grading-topbar")).toBeNull();
      expect(target.querySelector(".grading-current-task")?.textContent).toBe("第 18 题");
      expect(target.textContent).toContain("· 未评分");
      expect(target.querySelector("h1")).toBeNull();
      expect(view.container.textContent).toContain("当前离线");
      fireEvent.click(screen.getByRole("button", { name: "下一份" }));
      expect(props.onNext).toHaveBeenCalledOnce();
    } finally { view.unmount(); target.remove(); }
  });
  it.each([
    [false, "all", true],
    [true, "all", false],
    [true, "mine", true]
  ] as const)("keeps reviewer controls scoped: manager=%s queue=%s", (canManageTasks, queueScope, actions) => {
    render(<WorkbenchHeader {...headerProps()} canManageTasks={canManageTasks} queueScope={queueScope} />);
    expect(Boolean(screen.queryByRole("button", { name: "下一份" }))).toBe(actions);
    expect(screen.queryByRole("button", { name: "开始处理" })).toBeNull();
    expect(screen.queryByRole("button", { name: "领取任务" })).toBeNull();
    expect(screen.queryByRole("button", { name: "暂放" })).toBeNull();
    expect(screen.queryByText("退回队列")).toBeNull();
    expect(screen.queryByText("重评队列")).toBeNull();
    expect(Boolean(screen.queryByRole("region", { name: "阅卷员进度" }))).toBe(canManageTasks && queueScope === "all");
  });

  it("preserves conflict actions and toolbar callbacks across the status notice extraction", () => {
    const props = headerProps();
    const view = render(<WorkbenchHeader {...props} draftSaveStatus="conflict" />);
    fireEvent.click(screen.getByRole("button", { name: "逐项合并" }));
    fireEvent.click(screen.getByRole("button", { name: "放弃本页修改" }));
    expect(props.onResolveConflict).toHaveBeenCalledTimes(1);
    expect(props.onReloadConflict).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "暂放" })).toBeNull();
    const onSubmit = vi.fn(), onBookmark = vi.fn();
    view.rerender(<WorkbenchHeader {...props} canSubmit onSubmit={onSubmit} personalActions={<button onClick={onBookmark}>标记试卷</button>} />);
    expect(screen.getByText("已保存到服务端")).toBeTruthy();
    for (const label of ["刷新", "下一份", "提交并下一份", "标记试卷"]) fireEvent.click(screen.getByRole("button", { name: label }));
    for (const callback of [props.onRefresh, props.onNext, onSubmit, onBookmark]) expect(callback).toHaveBeenCalledTimes(1);
    expect(props.onRelease).not.toHaveBeenCalled();
    expect(props.onClaim).not.toHaveBeenCalled();
    view.rerender(<WorkbenchHeader {...props} hasContext={false} />);
    fireEvent.click(screen.getByRole("button", { name: "领取任务" }));
    expect(props.onClaim).toHaveBeenCalledOnce();
    view.rerender(<WorkbenchHeader {...props} hasContext={false} remainingCount={0} />);
    expect(screen.queryByRole("button", { name: "领取任务" })).toBeNull();
    view.rerender(<WorkbenchHeader {...props} ownsSelectedTask={false} />);
    expect(screen.queryByRole("button", { name: "暂放" })).toBeNull();
    view.rerender(<WorkbenchHeader {...props} canWork={false} />);
    expect(screen.getByRole("button", { name: "下一份" })).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "开始处理" })).toBeNull();
  });
});

function initialDraft(): ScoreDraft {
  return {
    ...createInitialDraft(null), comments: "初始评语", privateNote: "初始备注", studentFeedback: "初始反馈",
    reason: "初始原因", disputeReason: "初始争议", answerText: "初始答案", rubricSelections: { criterion: 2 }
  };
}

describe("batch30 fallback snapshot value and reference boundaries", () => {
  it.each([null, "invalid", { draft: {}, viewer: null }, { draft: false, viewer: {} }])("rejects missing object structure: %j", (value) => {
    expect(fallbackSnapshot(value, initialDraft())).toBeNull();
  });

  it("keeps zero scores, empty text and valid finite values without mutating initial values", () => {
    const initial = initialDraft();
    const selections = { criterion: 0, invalid: Number.NaN, infinite: Number.POSITIVE_INFINITY };
    const restored = fallbackSnapshot({
      draft: { score: 0, comments: "", rubricSelections: selections },
      viewer: { mode: "ocr", scale: 0.25, rotation: 0, offset: { x: 0, y: -2 }, fit: false }
    }, initial)!;
    expect(restored.draft).toEqual({ ...initial, score: 0, comments: "", rubricSelections: { criterion: 0 } });
    expect(restored.viewer).toEqual({ mode: "ocr", scale: 0.25, rotation: 0, offset: { x: 0, y: -2 }, fit: false });
    expect(restored.draft).not.toBe(initial);
    expect(restored.draft.rubricSelections).not.toBe(selections);
    expect(initial.rubricSelections).toEqual({ criterion: 2 });
  });

  it("restores initial text and default viewer fields for invalid cache values", () => {
    const initial = initialDraft();
    const restored = fallbackSnapshot({
      draft: { score: Number.POSITIVE_INFINITY, comments: false, privateNote: 1, studentFeedback: null, reason: [], disputeReason: {}, answerText: 2 },
      viewer: { mode: "unsupported", scale: Number.NaN, rotation: Number.NEGATIVE_INFINITY, offset: { x: "1", y: Number.NaN }, fit: "false" }
    }, initial)!;
    expect(restored.draft).toEqual(initial);
    expect(restored.viewer).toEqual({ mode: "segment", scale: 1, rotation: 0, offset: { x: 0, y: 0 }, fit: true });
    expect(restored.draft.rubricSelections).toBe(initial.rubricSelections);
  });

  it.each([undefined, null, [], "invalid"])("reuses initial rubric object for an invalid selection table: %j", (rubricSelections) => {
    const initial = initialDraft();
    expect(fallbackSnapshot({ draft: { rubricSelections }, viewer: {} }, initial)!.draft.rubricSelections).toBe(initial.rubricSelections);
  });

  it("accepts an empty selection object independently and keeps existing array root compatibility", () => {
    const initial = initialDraft();
    const empty = fallbackSnapshot({ draft: { rubricSelections: {} }, viewer: {} }, initial)!;
    expect(empty.draft.rubricSelections).toEqual({});
    expect(empty.draft.rubricSelections).not.toBe(initial.rubricSelections);
    // 顶层只要求 draft/viewer 为非空对象；数组仍按缺失字段恢复，不能在提取中新增拒绝规则。
    expect(fallbackSnapshot({ draft: [], viewer: [] }, initial)!.draft).toEqual(initial);
  });

  it.each([[0, 0.25], [0.25, 0.25], [4, 4], [8, 4]] as const)("keeps scale bounds for %s", (scale, expected) => {
    expect(fallbackSnapshot({ draft: {}, viewer: { scale } }, initialDraft())!.viewer.scale).toBe(expected);
  });

  it.each([[false, false], [0, true], [null, true]] as const)("only literal false disables fit: %s", (fit, expected) => {
    expect(fallbackSnapshot({ draft: {}, viewer: { fit } }, initialDraft())!.viewer.fit).toBe(expected);
  });
});
