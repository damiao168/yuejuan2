// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { RemotePageAnalyzer } from "./analyzeTemplatePage";
import { templateLayoutReducer, type TemplateLayoutAction } from "./templateLayoutReducer";
import { useRegionSuggestion } from "./useRegionSuggestion";

const mocks = vi.hoisted(() => ({
  message: { warning: vi.fn(), success: vi.fn(), error: vi.fn() },
  render: vi.fn(), frames: vi.fn(), createOCR: vi.fn(), dispatch: vi.fn()
}));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("./answerSheetOCR", () => ({ renderAnswerSheetPage: mocks.render, createAnswerSheetOCR: mocks.createOCR }));
vi.mock("./answerSheetImageRegions", () => ({ detectAnswerSheetFrames: mocks.frames }));

type HookInput = Parameters<typeof useRegionSuggestion>[0];
type RemoteResult = Awaited<ReturnType<RemotePageAnalyzer>>;
const questions = [{ id: "q1", question_no: "1" }, { id: "q2", question_no: "2" }] as Question[];
const frames = [
  { x: .05, y: .1, width: .4, height: .7, confidence: .95 },
  { x: .55, y: .1, width: .4, height: .7, confidence: .95 }
];
const region = (questionId: string, index = 0): LayoutRegion => ({ id: `model-${questionId}-${index}`,
  question_id: questionId, label: questionId.slice(1), ...frames[index] });
const result = (regions: LayoutRegion[] = []): RemoteResult => ({ regions, unassigned: [], modelName: "vision-model" });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function pdf(pageCount = 1) {
  const pages = Array.from({ length: pageCount }, () => ({
    getViewport: () => ({ width: 1000, height: 2000 }),
    getTextContent: vi.fn(async () => ({ items: frames.map((frame, index) => ({
      str: `${index + 1}.`, transform: [50, 0, 0, 50, (frame.x + .01) * 1000, 1730], width: 25
    })) }))
  }));
  return { document: { numPages: pageCount, getPage: async (number: number) => pages[number - 1] } as unknown as PDFDocumentProxy, pages };
}

describe("multimodal-first answer-sheet suggestions", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useRegionSuggestion>;
  let mounted: boolean;
  const analyze = vi.fn<RemotePageAnalyzer>();
  function Probe(props: HookInput) { current = useRegionSuggestion(props); return null; }
  async function mount(patch: Partial<HookInput> = {}) {
    await act(async () => root.render(<Probe pdfRef={{ current: undefined }} questions={questions}
      readonly={false} dispatchLayout={mocks.dispatch} sourceKey="account-a:source-a:template-a"
      imageUrl="blob:sheet" analyzePage={analyze} {...patch} />));
  }
  async function start() {
    let pending!: Promise<void>;
    await act(async () => { pending = current.suggestQuestionRegions(); });
    return { pending };
  }
  function action() {
    return mocks.dispatch.mock.calls[0][0] as Extract<TemplateLayoutAction, { type: "suggestRegions" }>;
  }
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host); mounted = true;
    mocks.frames.mockReturnValue(frames);
    mocks.render.mockResolvedValue({ width: 1000, height: 2000, getContext: () => ({ getImageData: vi.fn() }) });
    analyze.mockResolvedValue(result([region("q1"), region("q2", 1)]));
  });
  afterEach(async () => {
    if (mounted) await act(async () => root.unmount());
    host.remove();
  });

  it("analyzes every page even when PDF text and frames would match every question locally", async () => {
    const source = pdf(2);
    analyze.mockResolvedValueOnce(result([region("q1"), region("q2", 1)])).mockResolvedValueOnce(result());
    await mount({ pdfRef: { current: source.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(analyze.mock.calls.map(([input]) => input.pageNo)).toEqual([1, 2]);
    expect(analyze.mock.calls[0][0].frames).toEqual(frames);
    expect(source.pages[0].getTextContent).not.toHaveBeenCalled();
    expect(mocks.createOCR).not.toHaveBeenCalled();
    expect(action().pages.get(1)?.map(item => item.question_id)).toEqual(["q1", "q2"]);
    expect(current.suggestionProgress).toEqual({ completed: 2, total: 2 });
    expect(current.suggestionModel).toBe("vision-model");
    expect(mocks.message.success).toHaveBeenCalledWith(expect.stringContaining("多模态模型 vision-model"));
  });

  it("uses model question associations instead of the conflicting local printed-number positions", async () => {
    const source = pdf();
    analyze.mockResolvedValue(result([region("q2"), region("q1", 1)]));
    await mount({ pdfRef: { current: source.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(action().pages.get(1)).toMatchObject([{ question_id: "q2", x: .05 }, { question_id: "q1", x: .55 }]);
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("keeps only unassociated geometry when the model fails rather than applying perfect-looking local associations", async () => {
    const source = pdf();
    analyze.mockRejectedValue(new Error("vision provider unavailable"));
    await mount({ pdfRef: { current: source.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(analyze).toHaveBeenCalledOnce();
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(mocks.createOCR).not.toHaveBeenCalled();
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(current.unassignedRegions.get(1)?.every(item => !item.question_id)).toBe(true);
    expect(current.suggestionError).toContain("未使用本地 OCR 自动关联");
    expect(current.suggestionTone).toBe("warning");
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(current.suggestionModel).toBeUndefined();
  });

  it("retains a failed page warning alongside the model and regions from a completed later page", async () => {
    const source = pdf(2);
    analyze.mockRejectedValueOnce(new Error("答题卡分析请求格式未通过校验，请刷新页面后重试"))
      .mockResolvedValueOnce(result([region("q2", 1)]));
    await mount({ pdfRef: { current: source.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(analyze.mock.calls.map(([input]) => input.pageNo)).toEqual([1, 2]);
    expect(current.suggestionError).toContain("第 1 页多模态后台分析未完成");
    expect(current.suggestionTone).toBe("warning");
    expect(current.suggestionModel).toBe("vision-model");
    expect(action().pages.has(1)).toBe(false);
    expect(action().pages.get(2)?.map(item => item.question_id)).toEqual(["q2"]);
    expect(current.unassignedRegions.get(1)?.every(item => !item.question_id)).toBe(true);
  });

  it("analyzes a page with no local frames and accepts model-discovered answer regions", async () => {
    mocks.frames.mockReturnValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(analyze).toHaveBeenCalledWith(expect.objectContaining({ frames: [], pageNo: 1 }));
    expect(action().pages.get(1)).toHaveLength(2);
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("retains identity-only recognition without exposing those frames as unassociated questions", async () => {
    const identity: LayoutRegion = { id: "student-number", label: "学号", ...frames[0] };
    mocks.frames.mockReturnValue([frames[0]]);
    analyze.mockResolvedValue({ regions: [], unassigned: [], identityRegions: [identity], modelName: "vision-model" });
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect(mocks.dispatch).toHaveBeenCalledWith({ type: "suggestIdentityRegions", pages: new Map([[1, [identity]]]) });
    expect(current.unassignedRegions.size).toBe(0);
    expect(mocks.message.success).toHaveBeenCalledWith(expect.stringContaining("已自动对应 0 道题及 1 个身份信息区域"));
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("retains identity geometry alongside questions without changing question coverage", async () => {
    const identity: LayoutRegion = { id: "student-name", label: "姓名", ...frames[0] };
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [] }] };
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    analyze.mockResolvedValue({ ...result([region("q2", 1)]), identityRegions: [identity] });
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(layout.pages[0].question_regions.map(item => item.question_id)).toEqual(["q2"]);
    expect(layout.pages[0].identity_regions).toMatchObject([{ id: "student-name", label: "姓名" }]);
    expect(layout.pages[0].identity_regions[0].question_id).toBeUndefined();
    expect(current.unassignedRegions.size).toBe(0);
  });

  it("keeps a complete choice block separate and suppresses row fragments inside it", async () => {
    const group: LayoutRegion = { id: "choice-block", label: "选择题区域", x: .05, y: .1, width: .4, height: .7 };
    const rows = [.1, .3, .5].map(y => ({ ...frames[0], y, height: .15 }));
    mocks.frames.mockReturnValue([...rows, frames[1]]);
    analyze.mockResolvedValue({ ...result([region("q2", 1)]), objectiveGroups: [group],
      unassigned: rows.map((row, index) => ({ id: `row-${index}`, ...row })) });
    await mount(); await act(async () => current.suggestQuestionRegions());
    expect(current.objectiveGroups.get(1)).toEqual([{ ...group, question_id: undefined }]);
    expect(current.unassignedRegions.size).toBe(0);
    expect(action().pages.get(1)?.map(item => item.question_id)).toEqual(["q2"]);
    expect(mocks.dispatch.mock.calls.every(([change]) => ![...change.pages.values()].flat().some((item: LayoutRegion) => item.id === group.id))).toBe(true);
    expect(mocks.message.success).toHaveBeenCalledWith(expect.stringContaining("1 个选择题区域"));
  });

  it("retains choice-only suggestions without creating any per-question region and prevents generic assignment", async () => {
    const group: LayoutRegion = { id: "choice-block", label: "选择题区域", ...frames[0] };
    mocks.frames.mockReturnValue([frames[0]]);
    analyze.mockResolvedValue({ ...result(), objectiveGroups: [group] });
    await mount(); await act(async () => current.suggestQuestionRegions());
    await act(async () => current.assignSuggestedRegion(group.id, "q1"));
    expect(current.objectiveGroups.get(1)).toHaveLength(1);
    expect(current.unassignedRegions.size).toBe(0); expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.discardObjectiveGroup(group.id));
    expect(current.objectiveGroups.size).toBe(0);
  });

  it("deduplicates choice blocks on retry and preserves them when later analysis fails", async () => {
    const group: LayoutRegion = { id: "choice-block", ...frames[0] };
    mocks.frames.mockReturnValue([frames[0]]);
    analyze.mockResolvedValue({ ...result(), objectiveGroups: [group] });
    await mount(); await act(async () => current.suggestQuestionRegions());
    analyze.mockResolvedValueOnce({ ...result(), objectiveGroups: [{ ...group, id: "retry", x: .051 }] });
    await act(async () => current.suggestQuestionRegions());
    expect(current.objectiveGroups.get(1)?.map(item => item.id)).toEqual(["choice-block"]);
    analyze.mockRejectedValueOnce(new Error("provider unavailable"));
    await act(async () => current.suggestQuestionRegions());
    expect(current.objectiveGroups.get(1)?.map(item => item.id)).toEqual(["choice-block"]);
    expect(current.unassignedRegions.size).toBe(0);
  });

  it("commits successful model pages while preserving only the page whose local fill recovery failed", async () => {
    mocks.frames.mockReturnValue([]);
    const source = pdf(2);
    const fill: LayoutRegion = { id: "old-fill", x: .1, y: .1, width: .2, height: .2, origin: "local_detector",
      review_state: "suggested", semantic_check: "needs_review", reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"], label: "填空第 12 题 · 待核对" };
    let layout: TemplateLayout = { pages: [1, 2].map(page_no => ({ page_no, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [] })),
      editor_state: { version: 1, source_sha256: "source", candidates: [
        { id: fill.id, page_no: 1, role: "uncertain", region: fill },
        { id: "wrong-model", page_no: 1, role: "uncertain", region: { ...fill, id: "wrong-model", origin: "model", x: .6 } },
        { id: "other-page", page_no: 2, role: "uncertain", region: { ...fill, id: "other-page" } }
      ] } };
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    analyze.mockImplementation(async ({ pageNo }) => pageNo === 1 ? { ...result([region("q2", 1)]),
      processingSucceeded: true, retainLocalFallback: false, retainLocalFillCandidates: true,
      notice: "第 1 页局部填空文字识别超时；已有填空候选仍需核对。" }
      : { ...result(), processingSucceeded: true, retainLocalFallback: false });
    await mount({ pdfRef: { current: source.document }, editorLayout: layout, sourceHash: "source" });
    await act(async () => current.suggestQuestionRegions());
    expect(layout.pages[0].question_regions.map(item => item.question_id)).toEqual(["q2"]);
    expect(layout.editor_state!.candidates.map(item => item.id)).toEqual(["old-fill"]);
    expect(current.unassignedRegions.get(1)?.[0]).toBe(fill);
    expect(current.suggestionError).toContain("局部填空文字识别超时");
    expect(current.suggestionTone).toBe("warning");
    expect(mocks.message.warning).toHaveBeenCalled();
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(mocks.dispatch.mock.calls[0][0]).toMatchObject({ type: "commitAnalysisPages", commit: { successfulPages: [1, 2], retainLocalFillPages: [1] } });
    // 随后的正常局部结果替换被保留的旧框；页面警告消失后不继续保留历史失败事实。
    analyze.mockImplementation(async ({ pageNo }) => ({ ...result(), retainLocalFallback: false, unassigned: pageNo === 1
      ? [{ ...fill, id: "fresh-fill", x: .5 }] : [] }));
    await mount({ pdfRef: { current: source.document }, editorLayout: layout, sourceHash: "source" });
    await act(async () => current.suggestQuestionRegions());
    expect(layout.editor_state!.candidates.map(item => item.id)).toEqual(["fresh-fill"]);
    expect(layout.editor_state!.candidates[0].region.question_id).toBeUndefined();
    expect(current.suggestionError).not.toContain("超时");
  });

  it("persists unresolved candidate roles and printed labels instead of reducing them all to unknown answer areas", async () => {
    mocks.frames.mockReturnValue([]);
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [] }],
      editor_state: { version: 1, source_sha256: "source", candidates: [] } };
    const candidates = (["identity", "objective_group", "answer"] as const).map((role, index) => ({ id: `pending-${role}`, page_no: 1, role,
      question_no: `${12 + index}`, region: { id: `pending-${role}`, x: .1 + index * .25, y: .1, width: .2, height: .2,
        origin: "model" as const, review_state: "suggested" as const, semantic_check: "needs_review" as const } }));
    analyze.mockResolvedValue({ ...result(), retainLocalFallback: false, unassigned: candidates.map(candidate => candidate.region),
      unassignedCandidates: candidates });
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    await mount({ editorLayout: layout, sourceHash: "source" });
    await act(async () => current.suggestQuestionRegions());
    expect(layout.editor_state?.candidates).toEqual(candidates);
    expect(current.objectiveGroups.get(1)?.map(candidate => candidate.id)).toEqual(["pending-objective_group"]);
    expect(current.unassignedRegions.get(1)?.map(candidate => candidate.id)).toEqual(["pending-identity", "pending-answer"]);
  });

  it("commits measured registration marks with the successful page and never retains old geometry after complete recognition", async () => {
    const old = { id: "old-candidate", ...frames[0], origin: "local_detector" as const,
      review_state: "suggested" as const, semantic_check: "not_evaluated" as const };
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [] }],
      editor_state: { version: 1, source_sha256: "source", candidates: [{ id: old.id, page_no: 1, role: "uncertain", region: old }] } };
    const mark: LayoutRegion = { id: "measured-corner", x: .01, y: .02, width: .01, height: .01,
      origin: "local_detector", review_state: "suggested", semantic_check: "supported" };
    analyze.mockResolvedValue({ ...result([region("q1"), region("q2", 1)]), retainLocalFallback: false,
      registrationMarks: [mark], processingSucceeded: true });
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    await mount({ editorLayout: layout, sourceHash: "source" });
    await act(async () => current.suggestQuestionRegions());
    expect(layout.pages[0].registration_marks).toEqual([mark]);
    expect(layout.pages[0].question_regions.map(item => item.question_id)).toEqual(["q1", "q2"]);
    expect(layout.editor_state?.candidates).toEqual([]);
    expect(current.unassignedRegions.size).toBe(0);
    expect(current.objectiveGroups.size).toBe(0);
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("preserves pending choice blocks during temporary readonly but invalidates them with question types and source versions", async () => {
    analyze.mockResolvedValue({ ...result(), objectiveGroups: [{ id: "choice-block", ...frames[0] }] });
    await mount(); await act(async () => current.suggestQuestionRegions());
    await mount({ readonly: true });
    expect(current.objectiveGroups.size).toBe(1);
    await act(async () => current.discardObjectiveGroup("choice-block"));
    expect(current.objectiveGroups.size).toBe(1);
    await mount({ questions: questions.map(q => ({ ...q, question_type: "single_choice" })) });
    expect(current.objectiveGroups.size).toBe(0);
    await act(async () => current.suggestQuestionRegions());
    await mount({ sourceKey: "account-a:source-a:template-a:revision-2" });
    expect(current.objectiveGroups.size).toBe(0);
  });

  it("preserves ordinary pending frames across template revisions while discarding old choice blocks", async () => {
    analyze.mockResolvedValue({ ...result(), objectiveGroups: [{ id: "choice-block", ...frames[0] }] });
    await mount({ analysisRevision: 1 }); await act(async () => current.suggestQuestionRegions());
    const pending = current.unassignedRegions.get(1)!;
    expect(pending.map(region => region.x)).toEqual([.55]);
    expect(current.objectiveGroups.size).toBe(1);
    await mount({ analysisRevision: 2 });
    expect(current.objectiveGroups.size).toBe(0);
    expect(current.unassignedRegions.get(1)).toEqual(pending);
  });

  it("aborts old model work after a revision change without applying late choice blocks", async () => {
    const delayed = deferred<RemoteResult>(); analyze.mockReturnValueOnce(delayed.promise);
    await mount({ analysisRevision: 1 });
    const { pending } = await start();
    await vi.waitFor(() => expect(analyze).toHaveBeenCalledOnce());
    const signal = analyze.mock.calls[0][0].signal;
    await mount({ analysisRevision: 2 }); expect(signal.aborted).toBe(true);
    await act(async () => { delayed.resolve({ ...result(), objectiveGroups: [{ id: "late-block", ...frames[0] }] }); await pending; });
    expect(current.objectiveGroups.size).toBe(0); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("still sends the rendered page when the local frame detector fails", async () => {
    mocks.frames.mockImplementation(() => { throw new Error("invalid local pixels"); });
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(analyze).toHaveBeenCalledWith(expect.objectContaining({ frames: [] }));
    expect(action().pages.get(1)).toHaveLength(2);
  });

  it("keeps geometric candidates unassociated when semantic analysis is unavailable", async () => {
    analyze.mockResolvedValue({ regions: [], unassigned: [], notice: "第 1 页多模态语义分析尚未配置，几何候选仍需人工关联和核对。" });
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(current.suggestionError).toContain("尚未配置");
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("retains an existing manual region and keeps a second model ROI for that question unassigned", async () => {
    const manual: LayoutRegion = { id: "manual", question_id: "q1", ...frames[0] };
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [manual] }] };
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    analyze.mockResolvedValue(result([region("q1", 1)]));
    await mount({ coveredQuestionIDs: new Set(["q1"]), coveredRegions: new Map([[1, [manual]]]) });
    await act(async () => current.suggestQuestionRegions());
    expect(analyze).toHaveBeenCalledOnce();
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(layout.pages[0].question_regions[0]).toBe(manual);
    expect(current.unassignedRegions.get(1)).toMatchObject([{ x: .55 }]);
    expect(current.unassignedRegions.get(1)?.[0].question_id).toBeUndefined();
  });

  it("does not drop the second ROI when the same question spans pages", async () => {
    const source = pdf(2);
    analyze.mockResolvedValue(result([region("q1")]));
    await mount({ pdfRef: { current: source.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(analyze).toHaveBeenCalledTimes(2);
    expect(action().pages.get(1)?.map(item => item.question_id)).toEqual(["q1"]);
    expect(action().pages.has(2)).toBe(false);
    expect(current.unassignedRegions.get(2)?.some(item => item.x === frames[0].x && !item.question_id)).toBe(true);
  });

  it.each(["account", "source", "readonly", "unmount"] as const)("discards and aborts model results after %s changes", async boundary => {
    const delayed = deferred<RemoteResult>();
    analyze.mockReturnValue(delayed.promise);
    await mount();
    const { pending } = await start();
    await vi.waitFor(() => expect(analyze).toHaveBeenCalledOnce());
    const signal = analyze.mock.calls[0][0].signal;
    if (boundary === "unmount") { await act(async () => root.unmount()); mounted = false; }
    else await mount(boundary === "readonly" ? { readonly: true } : {
      sourceKey: boundary === "account" ? "account-b:source-a:template-a" : "account-a:source-b:template-b"
    });
    expect(signal.aborted).toBe(true);
    await act(async () => { delayed.resolve({ ...result([region("q1")]),
      identityRegions: [{ id: "late-identity", label: "姓名", ...frames[0] }] }); await pending; });
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(mocks.message.error).not.toHaveBeenCalled();
  });

  it("preserves manual work added while a model request is pending", async () => {
    const delayed = deferred<RemoteResult>();
    analyze.mockReturnValue(delayed.promise);
    await mount({ coveredQuestionIDs: new Set() });
    const { pending } = await start();
    await vi.waitFor(() => expect(analyze).toHaveBeenCalledOnce());
    await mount({ coveredQuestionIDs: new Set(["q1"]) });
    await act(async () => { delayed.resolve(result([region("q1"), region("q2", 1)])); await pending; });
    expect(action().preserveExisting).toBe(true);
    expect(action().pages.get(1)?.map(item => item.question_id)).toEqual(["q2"]);
  });

  it("clears the actual model on a new run and source change without inferring one from failed responses", async () => {
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(current.suggestionModel).toBe("vision-model");
    const delayed = deferred<RemoteResult>();
    analyze.mockReturnValueOnce(delayed.promise);
    const { pending } = await start();
    expect(current.suggestionModel).toBeUndefined();
    await act(async () => { delayed.resolve({ regions: [], unassigned: [], notice: "模型调用失败" }); await pending; });
    expect(current.suggestionModel).toBeUndefined();
    await act(async () => current.suggestQuestionRegions());
    expect(current.suggestionModel).toBe("vision-model");
    await mount({ sourceKey: "account-b:source-b:template-b" });
    expect(current.suggestionModel).toBeUndefined();
  });
});
