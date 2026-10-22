// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { SheetWord } from "./answerSheetRegionMatching";
import { templateLayoutReducer, type TemplateLayoutAction } from "./templateLayoutReducer";
import { useRegionSuggestion } from "./useRegionSuggestion";

const mocks = vi.hoisted(() => ({
  message: { warning: vi.fn(), success: vi.fn(), error: vi.fn() },
  render: vi.fn(), frames: vi.fn(), createOCR: vi.fn(), recognize: vi.fn(), terminate: vi.fn(), dispatch: vi.fn()
}));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("./answerSheetOCR", () => ({ renderAnswerSheetPage: mocks.render, createAnswerSheetOCR: mocks.createOCR }));
vi.mock("./answerSheetImageRegions", () => ({ detectAnswerSheetFrames: mocks.frames }));

type PDFPage = Awaited<ReturnType<PDFDocumentProxy["getPage"]>>;
type HookInput = Parameters<typeof useRegionSuggestion>[0];
const questions = Array.from({ length: 6 }, (_, index) => ({
  id: `q${index + 1}`, question_no: `${index + 1}`
})) as Question[];
const frames = [
  { x: .05, y: .1, width: .4, height: .7, confidence: .95 },
  { x: .55, y: .1, width: .4, height: .7, confidence: .95 }
];
function words(pageNo = 1): SheetWord[] {
  return frames.map((frame, index) => ({ text: `${(pageNo - 1) * 2 + index + 1}.`,
    x: frame.x + .01, y: frame.y + .01, width: .025, height: .025, confidence: 95 }));
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function pdf(pageCount: number) {
  const pages = Array.from({ length: pageCount }, (_, index) => ({
    scanPage: index + 1, getViewport: () => ({ width: 1000, height: 2000 }),
    getTextContent: vi.fn(async () => ({ items: [] }))
  }));
  const getPage = vi.fn(async (number: number) => pages[number - 1] as unknown as PDFPage);
  return { document: { numPages: pageCount, getPage } as unknown as PDFDocumentProxy, pages, getPage };
}

describe("whole-answer-sheet scan orchestration", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useRegionSuggestion>;
  let mounted: boolean;
  const canvasPages = new WeakMap<HTMLCanvasElement, number>();
  function Probe(props: HookInput) { current = useRegionSuggestion(props); return null; }
  async function mount(patch: Partial<HookInput> = {}) {
    await act(async () => root.render(<Probe pdfRef={{ current: undefined }} questions={questions}
      readonly={false} dispatchLayout={mocks.dispatch} sourceKey="source-a:template-a" imageUrl="blob:sheet" {...patch} />));
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
    mocks.render.mockImplementation(async (page?: PDFPage) => {
      const canvas = { width: 1000, height: 2000, getContext: () => ({
        getImageData: () => ({ width: 1000, height: 2000, data: new Uint8ClampedArray(4) })
      }) } as unknown as HTMLCanvasElement;
      canvasPages.set(canvas, (page as unknown as { scanPage?: number })?.scanPage ?? 1);
      return canvas;
    });
    mocks.recognize.mockImplementation(async (canvas: HTMLCanvasElement) => words(canvasPages.get(canvas)));
    mocks.terminate.mockResolvedValue(undefined);
    mocks.createOCR.mockResolvedValue({ recognize: mocks.recognize, terminate: mocks.terminate });
  });
  afterEach(async () => {
    if (mounted) await act(async () => root.unmount());
    host.remove();
  });

  it("starts at page one and visits every PDF page before applying all matched regions together", async () => {
    const document = pdf(3);
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(document.getPage.mock.calls.map(([number]) => number)).toEqual([1, 2, 3]);
    expect(mocks.render).toHaveBeenCalledTimes(3);
    expect(mocks.recognize).toHaveBeenCalledTimes(3);
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.terminate).toHaveBeenCalledOnce();
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect([...action().pages].map(([pageNo, regions]) => [pageNo, regions.map(region => region.question_id)]))
      .toEqual([[1, ["q1", "q2"]], [2, ["q3", "q4"]], [3, ["q5", "q6"]]]);
    expect(current.suggestionProgress).toEqual({ completed: 3, total: 3 });
    expect(current.suggestingRegions).toBe(false);
    expect(current.suggestionError).toBeUndefined();
  });

  it("recognizes a standalone handwritten image without a PDF or text layer", async () => {
    await mount({ questions: questions.slice(0, 2) });
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.render).toHaveBeenCalledWith(undefined, "blob:sheet");
    expect(action().pages.get(1)).toMatchObject([
      { question_id: "q1", x: .05, y: .1, width: .4, height: .7, suggestion_source: "ocr_layout" },
      { question_id: "q2", x: .55, y: .1, width: .4, height: .7, suggestion_source: "ocr_layout" }
    ]);
    expect(current.suggestionProgress).toEqual({ completed: 1, total: 1 });
  });

  it("updates completed-page progress but keeps every suggestion unapplied until the final page succeeds", async () => {
    const lastPage = deferred<SheetWord[]>();
    const document = pdf(2);
    mocks.recognize.mockResolvedValueOnce(words(1)).mockReturnValueOnce(lastPage.promise);
    await mount({ pdfRef: { current: document.document }, questions: questions.slice(0, 4) });
    const { pending } = await start();
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledTimes(2));
    expect(current.suggestionProgress).toEqual({ completed: 1, total: 2 });
    expect(current.suggestingRegions).toBe(true);
    expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => { lastPage.resolve(words(2)); await pending; });
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect(current.suggestionProgress).toEqual({ completed: 2, total: 2 });
  });

  it("continues after a failed middle page and applies the successful pages with an explicit warning", async () => {
    const document = pdf(3);
    const firstRender = mocks.render.getMockImplementation()!;
    mocks.render.mockImplementationOnce(firstRender).mockRejectedValueOnce(new Error("second page image unreadable"));
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(document.getPage.mock.calls.map(([number]) => number)).toEqual([1, 2, 3]);
    expect(mocks.render).toHaveBeenCalledTimes(3);
    expect(mocks.recognize).toHaveBeenCalledTimes(2);
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect([...action().pages].map(([pageNo, regions]) => [pageNo, regions.map(region => region.question_id)]))
      .toEqual([[1, ["q1", "q2"]], [3, ["q5", "q6"]]]);
    expect(current.suggestionProgress).toEqual({ completed: 3, total: 3 });
    expect(current.suggestionError).toBe("第 2 页识别未完成，其余可识别区域已处理；请重试，已配置区域会保留。");
    expect(mocks.message.warning).toHaveBeenCalledWith(current.suggestionError);
    expect(mocks.message.error).not.toHaveBeenCalled();
    expect(mocks.terminate).toHaveBeenCalledOnce();
    expect(current.suggestingRegions).toBe(false);
  });

  it("does not restart failed OCR initialization on every page and lists all unfinished pages", async () => {
    const document = pdf(3);
    mocks.createOCR.mockRejectedValue(new Error("local OCR core timed out"));
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(document.getPage.mock.calls.map(([number]) => number)).toEqual([1, 2, 3]);
    expect(mocks.render).toHaveBeenCalledTimes(3);
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.recognize).not.toHaveBeenCalled();
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(current.suggestionProgress).toEqual({ completed: 3, total: 3 });
    expect(current.suggestionError).toBe("第 1、2、3 页识别未完成；已保留 6 个待关联框，可先关联题目或重试。");
    expect(current.suggestionTone).toBe("warning");
    expect([...current.unassignedRegions.values()].flat()).toHaveLength(6);
    expect(mocks.message.warning).toHaveBeenCalledWith(current.suggestionError);
    expect(current.suggestingRegions).toBe(false);
  });

  it("allows a manual retry to initialize OCR again after a previous scan initialization failed", async () => {
    const document = pdf(3);
    mocks.createOCR.mockRejectedValueOnce(new Error("local OCR core timed out"));
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.createOCR).toHaveBeenCalledTimes(2);
    expect(mocks.recognize).toHaveBeenCalledTimes(3);
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect([...action().pages].map(([pageNo, regions]) => [pageNo, regions.map(region => region.question_id)]))
      .toEqual([[1, ["q1", "q2"]], [2, ["q3", "q4"]], [3, ["q5", "q6"]]]);
    expect(current.suggestionError).toBeUndefined();
  });

  it("continues matching later text-layer frames after OCR initialization fails on an earlier page", async () => {
    const document = pdf(2);
    mocks.createOCR.mockRejectedValue(new Error("local OCR core timed out"));
    document.pages[1].getTextContent.mockResolvedValue({ items: frames.map((frame, index) => ({
      str: `${index + 3}.`, transform: [50, 0, 0, 50, (frame.x + .01) * 1000, 1730], width: 25,
    })) } as never);
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.recognize).not.toHaveBeenCalled();
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect([...action().pages].map(([pageNo, regions]) => [pageNo, regions.map(region => region.question_id)]))
      .toEqual([[2, ["q3", "q4"]]]);
    expect(current.suggestionError).toBe("第 1 页识别未完成，其余可识别区域已处理；请重试，已配置区域会保留。");
    expect(current.suggestionProgress).toEqual({ completed: 2, total: 2 });
  });

  it("preserves an already matched text-layer frame when OCR cannot initialize for the other frame on that page", async () => {
    const document = pdf(1);
    mocks.createOCR.mockRejectedValue(new Error("local OCR core timed out"));
    document.pages[0].getTextContent.mockResolvedValue({ items: [{
      str: "1.", transform: [50, 0, 0, 50, 60, 1730], width: 25,
    }] } as never);
    await mount({ pdfRef: { current: document.document }, questions: questions.slice(0, 2) });
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect(action().pages.get(1)?.map(region => region.question_id)).toEqual(["q1"]);
    expect(current.suggestionError).toBe("第 1 页识别未完成，其余可识别区域已处理；请重试，已配置区域会保留。");
    expect(current.suggestingRegions).toBe(false);
  });

  it.each(["source", "readonly", "unmount"] as const)("discards OCR results arriving after %s changes", async boundary => {
    const delayed = deferred<SheetWord[]>();
    mocks.recognize.mockReturnValueOnce(delayed.promise);
    await mount({ questions: questions.slice(0, 2) });
    const { pending } = await start();
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledOnce());
    if (boundary === "unmount") { await act(async () => root.unmount()); mounted = false; }
    else await mount({ questions: questions.slice(0, 2), ...(boundary === "source" ? { sourceKey: "source-b:template-b" } : { readonly: true }) });
    await act(async () => { delayed.resolve(words()); await pending; });
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(mocks.message.error).not.toHaveBeenCalled();
    expect(mocks.terminate).toHaveBeenCalled();
    if (mounted) expect(current.suggestingRegions).toBe(false);
  });

  it("ignores a second click while the first OCR request is pending", async () => {
    const delayed = deferred<SheetWord[]>();
    mocks.recognize.mockReturnValueOnce(delayed.promise);
    await mount({ questions: questions.slice(0, 2) });
    const { pending } = await start();
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledOnce());
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.render).toHaveBeenCalledOnce();
    expect(mocks.createOCR).toHaveBeenCalledOnce();
    expect(mocks.recognize).toHaveBeenCalledOnce();
    await act(async () => { delayed.resolve(words()); await pending; });
    expect(mocks.dispatch).toHaveBeenCalledOnce();
  });

  it("keeps an existing manual region while adding only the remaining question", async () => {
    const manual: LayoutRegion = { id: "manual", question_id: "q1", x: .2, y: .3, width: .3, height: .4 };
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [manual] }] };
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    await mount({ questions: questions.slice(0, 2), coveredQuestionIDs: new Set(["q1"]) });
    await act(async () => current.suggestQuestionRegions());
    expect(action().pages.get(1)?.map(region => region.question_id)).toEqual(["q2"]);
    expect(layout.pages[0].question_regions[0]).toBe(manual);
    expect(layout.pages[0].question_regions.map(region => region.question_id)).toEqual(["q1", "q2"]);
  });

  it("does not replace a manual region added while OCR is still pending", async () => {
    const delayed = deferred<SheetWord[]>();
    mocks.recognize.mockReturnValueOnce(delayed.promise);
    const manual: LayoutRegion = { id: "new-manual", question_id: "q1", x: .2, y: .3, width: .3, height: .4 };
    let layout: TemplateLayout = { pages: [{ page_no: 1, width: 1000, height: 2000,
      registration_marks: [], identity_regions: [], question_regions: [] }] };
    mocks.dispatch.mockImplementation((change: TemplateLayoutAction) => { layout = templateLayoutReducer(layout, change); });
    await mount({ questions: questions.slice(0, 2), coveredQuestionIDs: new Set() });
    const { pending } = await start();
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledOnce());
    // 识别启动后用户补充人工区域；迟到建议不能用启动时的空覆盖集合覆盖刚才的调整。
    layout = { pages: [{ ...layout.pages[0], question_regions: [manual] }] };
    await mount({ questions: questions.slice(0, 2), coveredQuestionIDs: new Set(["q1"]) });
    await act(async () => { delayed.resolve(words()); await pending; });
    expect(layout.pages[0].question_regions.find(region => region.question_id === "q1")).toBe(manual);
  });

  it("keeps one matched question and the other frame without requiring all exam questions to match", async () => {
    mocks.recognize.mockResolvedValue([words()[0]]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(action().pages.get(1)?.map(region => region.question_id)).toEqual(["q1"]);
    expect(current.unassignedRegions.get(1)).toMatchObject([{ x: .55, y: .1, label: "待关联题目" }]);
    expect(current.unassignedRegions.get(1)?.[0].question_id).toBeUndefined();
    expect(current.suggestionError).toContain("不必一次全部匹配");
    expect(current.suggestionTone).toBe("info");
  });

  it("keeps all frames as separate unassigned candidates when no printed question numbers match", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(new Set(current.unassignedRegions.get(1)?.map(region => region.id)).size).toBe(2);
    expect(current.suggestionTone).toBe("info");
    expect(current.suggestionError).toBeUndefined();
    expect(mocks.message.warning).not.toHaveBeenCalled();
    expect(mocks.message.error).not.toHaveBeenCalled();
  });

  it("detects frames without OCR when exam questions are not configured yet", async () => {
    await mount({ questions: [] });
    await act(async () => current.suggestQuestionRegions());
    expect(mocks.render).toHaveBeenCalledOnce();
    expect(mocks.createOCR).not.toHaveBeenCalled();
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(current.suggestionTone).toBe("info");
  });

  it("distinguishes missing frames from frames with unrecognized question numbers", async () => {
    mocks.frames.mockReturnValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    expect(current.unassignedRegions.size).toBe(0);
    expect(current.suggestionError).toContain("暂未检测到可靠的闭合答题框");
    expect(current.suggestionTone).toBe("info");
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("deduplicates a retry geometrically while keeping independent pages and candidate identities", async () => {
    const document = pdf(2);
    mocks.recognize.mockResolvedValue([]);
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    const firstIDs = [...current.unassignedRegions.values()].flat().map(region => region.id);
    await act(async () => current.suggestQuestionRegions());
    expect([...current.unassignedRegions.values()].flat().map(region => region.id)).toEqual(firstIDs);
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(current.unassignedRegions.get(2)).toHaveLength(2);
  });

  it("does not show a previously configured frame as unassigned when retrying the rest", async () => {
    mocks.recognize.mockResolvedValue([]);
    const manual = { id: "manual", question_id: "q1", ...frames[0] };
    await mount({ coveredQuestionIDs: new Set(["q1"]), coveredRegions: new Map([[1, [manual]]]) });
    await act(async () => current.suggestQuestionRegions());
    expect(current.unassignedRegions.get(1)).toMatchObject([{ x: .55 }]);
  });

  it("associates just the chosen candidate with a valid question and keeps the rest out of persisted layout", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    const candidates = current.unassignedRegions.get(1)!;
    await act(async () => current.assignSuggestedRegion(candidates[0].id, "q3"));
    expect(mocks.dispatch).toHaveBeenCalledWith({ type: "acceptSuggestedRegion", pageNo: 1, questionId: "q3",
      region: expect.objectContaining({ id: candidates[0].id, question_id: "q3", label: "3" }) });
    expect(current.unassignedRegions.get(1)?.map(region => region.id)).toEqual([candidates[1].id]);
  });

  it("rejects associating a candidate with a question already configured on another page", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount({ coveredQuestionIDs: new Set(["q3"]) });
    await act(async () => current.suggestQuestionRegions());
    const candidate = current.unassignedRegions.get(1)![0];
    await act(async () => current.assignSuggestedRegion(candidate.id, "q3"));
    expect(mocks.dispatch).not.toHaveBeenCalled();
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(mocks.message.warning).toHaveBeenCalledWith("此题已有答题区域，请先核对或删除原区域后再关联");
  });

  it("removes empty candidate page entries after the last frame is discarded", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    const candidates = current.unassignedRegions.get(1)!;
    await act(async () => current.discardSuggestedRegion(candidates[0].id));
    expect(current.unassignedRegions.get(1)?.map(region => region.id)).toEqual([candidates[1].id]);
    await act(async () => current.discardSuggestedRegion(candidates[1].id));
    expect(current.unassignedRegions.size).toBe(0);
  });

  it("removes an empty candidate page after associating its final frame while retaining another page", async () => {
    mocks.recognize.mockResolvedValue([]);
    const document = pdf(2);
    await mount({ pdfRef: { current: document.document } });
    await act(async () => current.suggestQuestionRegions());
    const front = current.unassignedRegions.get(1)!;
    const backIDs = current.unassignedRegions.get(2)!.map(region => region.id);
    await act(async () => current.discardSuggestedRegion(front[0].id));
    await act(async () => current.assignSuggestedRegion(front[1].id, "q3"));
    expect(current.unassignedRegions.has(1)).toBe(false);
    expect(current.unassignedRegions.get(2)?.map(region => region.id)).toEqual(backIDs);
    expect(mocks.dispatch).toHaveBeenCalledWith(expect.objectContaining({ type: "acceptSuggestedRegion", pageNo: 1, questionId: "q3" }));
  });

  it("preserves unassigned frames across temporary saving readonly and disables candidate mutations", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    const candidate = current.unassignedRegions.get(1)![0];
    await mount({ readonly: true });
    await act(async () => {
      current.assignSuggestedRegion(candidate.id, "q3");
      current.discardSuggestedRegion(candidate.id);
    });
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    expect(mocks.dispatch).not.toHaveBeenCalled();
    await mount();
    expect(current.unassignedRegions.get(1)?.[0].id).toBe(candidate.id);
    await act(async () => current.assignSuggestedRegion(candidate.id, "q3"));
    expect(mocks.dispatch).toHaveBeenCalledOnce();
  });

  it("clears unassigned frames only when switching the source or exam questions", async () => {
    mocks.recognize.mockResolvedValue([]);
    await mount();
    await act(async () => current.suggestQuestionRegions());
    await mount({ sourceKey: "source-b:template-b" });
    expect(current.unassignedRegions.size).toBe(0);
    await act(async () => current.suggestQuestionRegions());
    expect(current.unassignedRegions.get(1)).toHaveLength(2);
    await mount({ sourceKey: "source-b:template-b", questions: questions.slice(0, 2) });
    expect(current.unassignedRegions.size).toBe(0);
  });
});
