// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../../api/papers";
import type { AnswerSheetTemplate, LayoutRegion } from "../../../api/configuration";
import { answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { useTemplateEditor } from "./useTemplateEditor";
import type { SheetWord } from "./answerSheetRegionMatching";
import type { RemotePageAnalyzer } from "./analyzeTemplatePage";

const mocks = vi.hoisted(() => ({ render: vi.fn(), frames: vi.fn(), createOCR: vi.fn(), recognize: vi.fn(), terminate: vi.fn(),
  message: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message, modal: { confirm: vi.fn() } }) } }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
// 本组只组合本地扫描与编辑器状态；生命周期的网络模块不参与场景，避免初始化真实SDK客户端。
vi.mock("../../../api/configuration", () => ({
  cloneAnswerSheetTemplate: vi.fn(), createAnswerSheetTemplate: vi.fn(), lockAnswerSheetTemplate: vi.fn(), updateAnswerSheetTemplate: vi.fn(),
}));
vi.mock("./answerSheetOCR", () => ({ renderAnswerSheetPage: mocks.render, createAnswerSheetOCR: mocks.createOCR }));
vi.mock("./answerSheetImageRegions", () => ({ detectAnswerSheetFrames: mocks.frames }));

const questions = [{ id: "q1", question_no: "1" }, { id: "q2", question_no: "2" }] as Question[];
const frames = [{ x: .1, y: .1, width: .8, height: .6, confidence: .98 }];
const template: AnswerSheetTemplate = { ...answerTemplate, layout: { pages: [
  { ...answerTemplate.layout.pages[0], question_regions: [], width: 600, height: 800 },
  { ...answerTemplate.layout.pages[0], page_no: 2, question_regions: [], width: 1200, height: 800 }
] } };
const setPageNo = vi.fn();
const words = (pageNo: number): SheetWord[] => [{ text: `${pageNo}.`, x: .12, y: .12, width: .03, height: .03, confidence: 95 }];
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe("template editor continuous scan across preview-page changes", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useTemplateEditor>;
  let pdfRef: { current: PDFDocumentProxy | undefined };
  const canvasPages = new WeakMap<HTMLCanvasElement, number>();
  function Probe({ loading = false, pageNo = 1, canManage = true, selected = template, analyzePage }: { loading?: boolean; pageNo?: number; canManage?: boolean; selected?: typeof template; analyzePage?: RemotePageAnalyzer }) {
    current = useTemplateEditor({ examId: "exam", canManage, selectedPaper: answerSource, selectedTemplate: selected,
      questions, setTemplates: vi.fn(), setSelectedTemplateId: vi.fn(), pdfRef, pageNo, setPageNo, analyzePage,
      preview: { sourceId: answerSource.id, loading, pageCount: 2, width: pageNo === 1 ? 600 : 1200, height: 800,
        pageSizes: [{ width: 600, height: 800 }, { width: 1200, height: 800 }] } });
    return null;
  }
  async function render(props: Parameters<typeof Probe>[0] = {}) { await act(async () => root.render(<Probe {...props} />)); }
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const pages = [1, 2].map(pageNo => ({ pageNo, getViewport: () => ({ width: pageNo === 1 ? 600 : 1200, height: 800 }),
      getTextContent: async () => ({ items: [] }) }));
    pdfRef = { current: { numPages: 2, getPage: vi.fn(async (number: number) => pages[number - 1]) } as unknown as PDFDocumentProxy };
    mocks.frames.mockReturnValue(frames);
    mocks.render.mockImplementation(async (page: { pageNo: number }) => {
      const canvas = { width: 600, height: 800, getContext: () => ({ getImageData: () => ({}) }) } as unknown as HTMLCanvasElement;
      canvasPages.set(canvas, page.pageNo);
      return canvas;
    });
    mocks.recognize.mockImplementation(async (canvas: HTMLCanvasElement) => words(canvasPages.get(canvas)!));
    mocks.terminate.mockResolvedValue(undefined);
    mocks.createOCR.mockResolvedValue({ recognize: mocks.recognize, terminate: mocks.terminate });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("finishes all-page recognition even when another preview page starts rendering", async () => {
    const waiting = deferred<SheetWord[]>();
    mocks.recognize.mockReturnValueOnce(waiting.promise);
    await render();
    let pending!: Promise<void>;
    await act(async () => { pending = current.suggestQuestionRegions(); });
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledOnce());
    await render({ loading: true, pageNo: 2 });
    expect(current.suggestingRegions).toBe(true);
    expect(current.readonly).toBe(true);
    expect(mocks.terminate).not.toHaveBeenCalled();
    await act(async () => { waiting.resolve(words(1)); await pending; });
    expect(current.layout.pages.map(page => [page.width, page.question_regions.map(region => region.question_id)]))
      .toEqual([[600, ["q1"]], [1200, ["q2"]]]);
    expect(current.suggestionProgress).toEqual({ completed: 2, total: 2 });
    expect(current.suggestingRegions).toBe(false);
    expect(current.readonly).toBe(true);
    await render({ pageNo: 2 });
    expect(current.readonly).toBe(false);
  });

  it("retains a manual region restored while an all-page scan is pending", async () => {
    const waiting = deferred<SheetWord[]>();
    mocks.recognize.mockReturnValueOnce(waiting.promise);
    await render();
    let pending!: Promise<void>;
    await act(async () => { pending = current.suggestQuestionRegions(); });
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledOnce());
    const manual: LayoutRegion = { id: "restored-manual", question_id: "q1", x: .3, y: .2, width: .5, height: .4 };
    await act(async () => current.setLayout(layout => ({ ...layout, pages: layout.pages.map(page => page.page_no === 1
      ? { ...page, question_regions: [manual] } : page) })));
    await act(async () => { waiting.resolve(words(1)); await pending; });
    expect(current.layout.pages[0].question_regions).toEqual([manual]);
    expect(current.layout.pages[1].question_regions.map(region => region.question_id)).toEqual(["q2"]);
  });

  it("restores a missing reverse layout page from the source without changing front-side manual regions", async () => {
    const manual = { id: "front-manual", question_id: "q1", x: .1, y: .1, width: .5, height: .3 };
    const oldDraft = { ...template, page_count: 1, layout: { pages: [{ ...template.layout.pages[0], question_regions: [manual] }] } };
    await render({ selected: oldDraft });
    expect(current.layout.pages).toHaveLength(2);
    expect(current.layout.pages[0].question_regions).toEqual([manual]);
    expect(current.layout.pages[1]).toMatchObject({ page_no: 2, width: 1200, height: 800, question_regions: [] });
    expect(mocks.render).not.toHaveBeenCalled();
    expect(mocks.createOCR).not.toHaveBeenCalled();
  });

  it("does not alter a locked template even when its source has an additional reverse page", async () => {
    const locked = { ...template, status: "locked" as const, page_count: 1, layout: { pages: [template.layout.pages[0]] } };
    await render({ selected: locked });
    expect(current.layout.pages).toHaveLength(1);
    expect(current.readonly).toBe(true);
    expect(mocks.render).not.toHaveBeenCalled();
  });

  it("fills locally unassociated frames with backend semantics across all pages", async () => {
    mocks.recognize.mockResolvedValue([]);
    const analyzePage = vi.fn(async ({ pageNo }: { pageNo: number }) => ({ regions: [{ id: `remote-${pageNo}`,
      question_id: `q${pageNo}`, x: .1, y: .1, width: .8, height: .6 }], unassigned: [] }));
    await render({ analyzePage });
    await act(async () => { await current.suggestQuestionRegions(); });
    expect(analyzePage).toHaveBeenCalledTimes(2);
    expect(current.layout.pages.map(page => page.question_regions.map(region => region.question_id))).toEqual([["q1"], ["q2"]]);
  });

  it("keeps the draft unchanged when all remote pages fail and reports failure without semantic success", async () => {
    mocks.recognize.mockResolvedValue([]);
    const analyzePage = vi.fn().mockRejectedValue(new Error("not configured"));
    await render({ analyzePage });
    await act(async () => { await current.suggestQuestionRegions(); });
    expect(current.unassignedRegions.size).toBe(0);
    expect(current.layout.pages.every(page => !page.question_regions.length)).toBe(true);
    expect(current.suggestionError).toContain("识别未完成，原有区域已保留");
    expect(current.suggestionTone).toBe("warning");
  });

  it("keeps a second backend-only region for the same question pending instead of silently dropping it", async () => {
    mocks.frames.mockReturnValue([]); mocks.recognize.mockResolvedValue([]);
    const analyzePage = vi.fn(async ({ pageNo }: { pageNo: number }) => ({ regions: [{ id: `remote-${pageNo}`,
      question_id: "q1", x: .1, y: .1, width: .8, height: .6 }], unassigned: [] }));
    await render({ analyzePage }); await act(async () => { await current.suggestQuestionRegions(); });
    expect(current.layout.pages[0].question_regions.map(region => region.question_id)).toEqual(["q1"]);
    expect(current.layout.pages[1].question_regions).toEqual([]);
    expect(current.unassignedRegions.get(2)).toHaveLength(1);
  });

  it("replaces wrong choice and identity suggestions with a corrected retry through the real editor hook", async () => {
    mocks.frames.mockReturnValue([]);
    const box = { x: .1, y: .1, width: .3, height: .3 };
    const analyzePage = vi.fn<RemotePageAnalyzer>().mockImplementation(async ({ pageNo }) => pageNo === 1
      ? { regions: [], unassigned: [], objectiveGroups: [{ id: "wrong-choice", ...box }],
        identityRegions: [{ id: "wrong-name", label: "姓名", ...box, x: .6 }] }
      : { regions: [], unassigned: [] });
    await render({ analyzePage }); await act(async () => current.suggestQuestionRegions());
    expect(current.objectiveGroups.get(1)?.[0].id).toBe("wrong-choice");
    analyzePage.mockImplementation(async ({ pageNo }) => pageNo === 1
      ? { regions: [{ id: "correct-answer", question_id: "q1", ...box }], unassigned: [],
        identityRegions: [{ id: "correct-name", label: "姓名", ...box, x: .6, y: .6 }] }
      : { regions: [], unassigned: [] });
    await act(async () => current.suggestQuestionRegions());
    expect(current.objectiveGroups.size).toBe(0);
    expect(current.layout.pages[0].question_regions.map(region => region.id)).toEqual(["correct-answer"]);
    expect(current.layout.pages[0].identity_regions.map(region => region.id)).toEqual(["correct-name"]);
    expect(JSON.stringify(current.layout)).not.toContain("wrong-");
  });

  it("preserves a human edit on retry and rejects a late response after another edit", async () => {
    mocks.frames.mockReturnValue([]);
    const box = { x: .1, y: .1, width: .3, height: .3 };
    const analyzePage = vi.fn<RemotePageAnalyzer>().mockResolvedValue({ regions: [{ id: "first", question_id: "q1", ...box }], unassigned: [] });
    await render({ analyzePage }); await act(async () => current.suggestQuestionRegions());
    await act(async () => current.updateRegion("first", { x: .2 }));
    analyzePage.mockResolvedValue({ regions: [{ id: "second", question_id: "q1", ...box }], unassigned: [] });
    await act(async () => current.suggestQuestionRegions());
    expect(current.layout.pages[0].question_regions).toMatchObject([{ id: "first", x: .2, review_state: "edited" }]);
    const delayed = deferred<Awaited<ReturnType<RemotePageAnalyzer>>>();
    analyzePage.mockReturnValueOnce(delayed.promise);
    let pending!: Promise<void>;
    await act(async () => { pending = current.suggestQuestionRegions(); });
    await act(async () => current.updateRegion("first", { y: .4 }));
    await act(async () => { delayed.resolve({ regions: [], unassigned: [] }); await pending; });
    expect(current.layout.pages[0].question_regions).toMatchObject([{ id: "first", x: .2, y: .4 }]);
    expect(current.suggestionError).toContain("本次结果未应用");
  });

  it("clears automatic regions on empty success but retains the failed page unchanged", async () => {
    mocks.frames.mockReturnValue([]);
    const analyzePage = vi.fn<RemotePageAnalyzer>().mockImplementation(async ({ pageNo }) => ({ regions: [{ id: `first-${pageNo}`,
      question_id: `q${pageNo}`, x: .1, y: .1, width: .3, height: .3 }], unassigned: [] }));
    await render({ analyzePage }); await act(async () => current.suggestQuestionRegions());
    const second = current.layout.pages[1];
    analyzePage.mockImplementation(async ({ pageNo }) => {
      // 版面校验整批拒绝也会返回 HTTP 成功；processingSucceeded=false 不能被当成合法空页。
      if (pageNo === 2) return { regions: [], unassigned: [], processingSucceeded: false, retainLocalFallback: false };
      return { regions: [], unassigned: [], processingSucceeded: true };
    });
    await act(async () => current.suggestQuestionRegions());
    expect(current.layout.pages[0].question_regions).toEqual([]);
    expect(current.layout.pages[1]).toBe(second);
    expect(current.suggestionError).toContain("第 2 页识别未完成");
  });

  it("accepts the teacher's corrected identity immediately while retaining its model origin and diagnostics", async () => {
    mocks.frames.mockReturnValue([]);
    const analyzePage = vi.fn<RemotePageAnalyzer>().mockImplementation(async ({ pageNo }) => ({ regions: [],
      unassigned: pageNo === 1 ? [{ id: "unknown", x: .1, y: .1, width: .3, height: .1, origin: "model",
        semantic_check: "needs_review", reason_codes: ["SEMANTIC_CONTENT_CONFLICT"] }] : [] }));
    await render({ analyzePage }); await act(async () => current.suggestQuestionRegions());
    await act(async () => current.assignSuggestedIdentity("unknown", "姓名"));
    expect(current.unassignedRegions.size).toBe(0);
    expect(current.layout.pages[0].identity_regions).toMatchObject([{ id: "unknown", label: "姓名", origin: "model", review_state: "edited" }]);
    expect(current.reviewMessage).toBe("");
    expect(current.layout.pages[0].identity_regions[0].reason_codes).toEqual(["SEMANTIC_CONTENT_CONFLICT"]);
    await act(async () => current.confirmPageRegions());
    expect(current.layout.pages[0].identity_regions[0].review_state).toBe("confirmed");
    expect(current.reviewMessage).toBe("");
    await act(async () => current.undo());
    expect(current.layout.pages[0].identity_regions[0].review_state).toBe("edited");
    await act(async () => current.redo());
    expect(current.layout.pages[0].identity_regions[0].review_state).toBe("confirmed");
  });
});
