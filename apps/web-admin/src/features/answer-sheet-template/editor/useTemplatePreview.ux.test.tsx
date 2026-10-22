// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperVersion } from "../../../api/papers";
import { answerQuestion, answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { TemplateToolbar } from "../view/TemplateWorkspaceView";
import type { TemplatePageViewState } from "../view/templateViewTypes";
import { useTemplatePreview } from "./useTemplatePreview";

const mocks = vi.hoisted(() => ({ download: vi.fn(), getDocument: vi.fn(), suggest: vi.fn(), createURL: vi.fn(), revokeURL: vi.fn() }));
vi.mock("../../../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("pdfjs-dist", () => ({ GlobalWorkerOptions: {}, getDocument: mocks.getDocument }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "mock-worker.js" }));
vi.mock("antd", async () => {
  const { MockTemplateSourceDropdown, MockTemplatePopover } = await import("../testing/MockTemplateSourceDropdown");
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    App: { useApp: () => ({ modal: { confirm: vi.fn() } }) },
    Alert: Box, Dropdown: { Button: MockTemplateSourceDropdown }, Input: Box, InputNumber: Box, List: Box, Progress: Box, Space: Box, Spin: Box, Tag: Box, Upload: Box,
    Popover: MockTemplatePopover,
    Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
    Select: () => null
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function source(id: string, contentType = "application/pdf"): PaperVersion {
  return { ...answerSource, id, file_asset_id: `asset-${id}`, file: { ...answerSource.file, content_type: contentType } };
}
function download(contentType = "application/pdf") {
  const blob = new Blob(["synthetic template"], { type: contentType });
  Object.defineProperty(blob, "arrayBuffer", { value: async () => new ArrayBuffer(8) });
  return { blob, contentType };
}
function pdf(contents: Array<unknown[] | Promise<{ items: unknown[] }>>) {
  const pages = contents.map((items) => ({
    getTextContent: vi.fn(() => Array.isArray(items) ? Promise.resolve({ items }) : items),
    getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
    render: vi.fn(() => ({ promise: Promise.resolve() }))
  }));
  const document = { numPages: pages.length, getPage: vi.fn(async (pageNo: number) => pages[pageNo - 1]), destroy: vi.fn(async () => {}) };
  return { document: document as unknown as PDFDocumentProxy, pages, destroy: document.destroy };
}

describe("R15 answer-sheet source preview and real toolbar", () => {
  let root: Root; let host: HTMLDivElement; let current: ReturnType<typeof useTemplatePreview>;
  function Probe({ paper }: { paper?: PaperVersion }) {
    current = useTemplatePreview(paper);
    const view = {
      papers: paper ? [paper] : [], templates: [answerTemplate], selectedPaperId: paper?.id ?? "", selectedTemplateId: answerTemplate.id,
      selectedTemplate: answerTemplate, canManage: true, canEditSources: true, questions: [answerQuestion], sourceUpload: { uploading: false }, uploadProps: { disabled: false },
      editor: { saving: false, suggestingRegions: false, readonly: false, coveredQuestions: new Set([answerQuestion.id]), suggestQuestionRegions: mocks.suggest,
        revision: answerTemplate.revision, layout: answerTemplate.layout, unassignedRegions: new Map(), discardSuggestedRegion: vi.fn() },
      binding: { bindingBusy: false }, printing: { printBusy: false }, previewController: current
    } as unknown as TemplatePageViewState;
    return <TemplateToolbar view={view} />;
  }
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.download.mockResolvedValue(download());
    mocks.createURL.mockImplementation(() => `blob:preview-${mocks.createURL.mock.calls.length}`);
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: mocks.createURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: mocks.revokeURL });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({} as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((callback) => callback(new Blob(["preview"], { type: "image/png" })));
    class LoadedImage {
      naturalWidth = 1000; naturalHeight = 2000; onload?: () => void; onerror?: () => void;
      set src(_url: string) { queueMicrotask(() => this.onload?.()); }
    }
    vi.stubGlobal("Image", LoadedImage);
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  async function render(paper?: PaperVersion) { await act(async () => { root.render(<Probe paper={paper} />); }); }
  const candidateButton = () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "识别区域");

  it("checks every PDF page and enables question-number assistance when the cover has no text", async () => {
    const document = pdf([[], [{ str: "2. 请作答" }], []]); mocks.getDocument.mockReturnValue({ promise: Promise.resolve(document.document) });
    await render(source("multi-page"));
    expect(current.preview).toMatchObject({ pageCount: 3, textLayerAvailable: true, textLayerChecking: false });
    for (const page of document.pages) expect(page.getTextContent).toHaveBeenCalledOnce();
    expect(candidateButton()?.disabled).toBe(false);
		expect(mocks.suggest).not.toHaveBeenCalled();
    await act(async () => candidateButton()!.click()); expect(mocks.suggest).toHaveBeenCalledOnce();
    expect(host.textContent).not.toContain("图片及扫描件需手动设置区域");
  });

  it("offers whole-document recognition for a scanned PDF without readable text", async () => {
    const document = pdf([[], [{ str: "   " }, { type: "marked-content" }]]); mocks.getDocument.mockReturnValue({ promise: Promise.resolve(document.document) });
    await render(source("scanned"));
    expect(current.preview).toMatchObject({ pageCount: 2, textLayerAvailable: false, textLayerChecking: false });
    expect(candidateButton()?.disabled).toBe(false);
    expect(host.textContent).not.toContain("按印刷边框与题号定位");
    await act(async () => candidateButton()!.click()); expect(mocks.suggest).toHaveBeenCalledOnce();
    expect(host.textContent).not.toContain("OCR");
  });

  it("offers recognition for a handwritten image and keeps its real dimensions", async () => {
    mocks.download.mockResolvedValue(download("image/png")); await render(source("image", "image/png"));
    expect(current.preview).toMatchObject({ width: 1000, height: 2000, pageCount: 1, textLayerAvailable: false, textLayerChecking: false });
    expect(mocks.getDocument).not.toHaveBeenCalled(); expect(candidateButton()?.disabled).toBe(false);
    expect(host.textContent).not.toContain("已填写答题卡");
  });

  it("shows checking feedback and ignores old text-layer results after switching to an image", async () => {
    const waiting = deferred<{ items: unknown[] }>(); const old = pdf([waiting.promise]);
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(old.document) }); await render(source("checking"));
    expect(current.preview.textLayerChecking).toBe(true);
    expect(candidateButton()?.disabled).toBe(false);
    mocks.download.mockResolvedValue(download("image/png")); await render(source("replacement", "image/png"));
    const imageUrl = current.preview.imageUrl;
    await act(async () => waiting.resolve({ items: [{ str: "旧答题卡第1题" }] }));
    expect(current.preview).toMatchObject({ imageUrl, width: 1000, height: 2000, textLayerAvailable: false, textLayerChecking: false });
    expect(candidateButton()?.disabled).toBe(false); expect(old.destroy).toHaveBeenCalled();
  });

  it("destroys a PDF parsed after a source switch without replacing the new document", async () => {
    const parsing = deferred<PDFDocumentProxy>(); const old = pdf([[{ str: "旧题号1" }]]); const next = pdf([[]]);
    mocks.getDocument.mockReturnValueOnce({ promise: parsing.promise }).mockReturnValueOnce({ promise: Promise.resolve(next.document) });
    await render(source("old-parse")); await render(source("new-parse"));
    await act(async () => parsing.resolve(old.document));
    expect(old.destroy).toHaveBeenCalledOnce(); expect(current.pdfRef.current).toBe(next.document);
    expect(current.preview.textLayerAvailable).toBe(false); expect(candidateButton()?.disabled).toBe(false);
  });

  it("ignores a download finishing after a source switch", async () => {
    const waiting = deferred<ReturnType<typeof download>>(); const next = pdf([[{ str: "第2题" }]]);
    mocks.download.mockReturnValueOnce(waiting.promise).mockResolvedValueOnce(download());
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(next.document) });
    await render(source("old-download")); await render(source("new-download"));
    const previewUrl = current.preview.imageUrl;
    await act(async () => waiting.resolve(download()));
    expect(mocks.getDocument).toHaveBeenCalledOnce(); expect(current.pdfRef.current).toBe(next.document);
    expect(current.preview).toMatchObject({ imageUrl: previewUrl, textLayerAvailable: true, textLayerChecking: false });
  });

  it("does not let an obsolete delayed PDF destruction clear the newest source", async () => {
    const old = pdf([[{ str: "第1题" }]]); mocks.getDocument.mockReturnValue({ promise: Promise.resolve(old.document) });
    await render(source("initial-pdf"));
    const olderDestroy = deferred<void>();
    // 第一次切换等待旧文档释放；再选另一张图片可立即加载，旧释放迟到不能清空新预览。
    old.destroy.mockReturnValueOnce(olderDestroy.promise);
    mocks.download.mockResolvedValue(download("image/png"));
    await render(source("obsolete-image", "image/png")); await render(source("newest-image", "image/png"));
    const latest = current.preview.imageUrl;
    expect(current.preview).toMatchObject({ loading: false, width: 1000, height: 2000, textLayerAvailable: false });
    await act(async () => olderDestroy.resolve());
    expect(current.preview).toMatchObject({ loading: false, imageUrl: latest, textLayerAvailable: false, textLayerChecking: false });
    expect(mocks.download).not.toHaveBeenCalledWith("asset-obsolete-image");
  });

  it("continues loading the replacement when obsolete PDF cleanup fails", async () => {
    const old = pdf([[{ str: "第1题" }]]); mocks.getDocument.mockReturnValue({ promise: Promise.resolve(old.document) });
    await render(source("failed-cleanup-pdf"));
    old.destroy.mockRejectedValueOnce(new Error("obsolete cleanup failure"));
    mocks.download.mockResolvedValue(download("image/png")); await render(source("replacement-image", "image/png"));
    expect(current.preview).toMatchObject({ loading: false, width: 1000, height: 2000, textLayerAvailable: false, textLayerChecking: false });
    expect(current.preview.error).toBeUndefined(); expect(candidateButton()?.disabled).toBe(false);
    expect(host.textContent).not.toContain("已填写答题卡");
  });
});
