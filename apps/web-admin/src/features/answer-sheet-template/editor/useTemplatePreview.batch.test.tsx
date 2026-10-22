// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperVersion } from "../../../api/papers";
import { answerSource } from "../testing/answerSheetFixtures";
import { useTemplatePreview } from "./useTemplatePreview";

const mocks = vi.hoisted(() => ({ download: vi.fn(), getDocument: vi.fn(), createURL: vi.fn(), revokeURL: vi.fn() }));
vi.mock("../../../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));
vi.mock("pdfjs-dist", () => ({ GlobalWorkerOptions: {}, getDocument: mocks.getDocument }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "mock-worker.js" }));

type PDFPage = Awaited<ReturnType<PDFDocumentProxy["getPage"]>>;
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function source(id: string): PaperVersion { return { ...answerSource, id, file_asset_id: `asset-${id}` }; }
function pdf(sizes: Array<[number, number]>) {
  const pages = sizes.map(([width, height]) => ({
    getViewport: vi.fn(({ scale }: { scale: number }) => ({ width: width * scale, height: height * scale })),
    getTextContent: vi.fn(async () => ({ items: [] })),
    render: vi.fn(() => ({ promise: Promise.resolve() }))
  }));
  const document = { numPages: pages.length, getPage: vi.fn(async (pageNo: number) => pages[pageNo - 1]), destroy: vi.fn(async () => undefined) };
  return { document, pages, proxy: document as unknown as PDFDocumentProxy };
}

describe("multi-page answer-sheet preview source and page-size boundaries", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useTemplatePreview>;
  function Probe({ paper }: { paper?: PaperVersion }) { current = useTemplatePreview(paper); return null; }
  async function render(paper?: PaperVersion) { await act(async () => root.render(<Probe paper={paper} />)); }
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.download.mockResolvedValue({ contentType: "application/pdf", blob: { arrayBuffer: async () => new ArrayBuffer(8) } });
    mocks.createURL.mockImplementation(() => `blob:batch-preview-${mocks.createURL.mock.calls.length}`);
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: mocks.createURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: mocks.revokeURL });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({} as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(callback => callback(new Blob(["rendered page"])));
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });

  it("retains every page's dimensions and renders a landscape page with its own coordinate size", async () => {
    const document = pdf([[595.28, 841.89], [1190.55, 841.89], [420, 595]]);
    const paper = source("different-page-sizes");
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(document.proxy) });
    await render(paper);
    expect(current.preview).toMatchObject({ sourceId: paper.id, pageCount: 3,
      pageSizes: [{ width: 595, height: 842 }, { width: 1191, height: 842 }, { width: 420, height: 595 }],
      width: 595, height: 842, loading: false });
    await act(async () => current.setPageNo(2));
    expect(current.preview).toMatchObject({ width: 1191, height: 842, loading: false });
    expect(current.preview.pageSizes?.[0]).toEqual({ width: 595, height: 842 });
    expect(document.pages[1].render).toHaveBeenCalledOnce();
  });

  it("keeps the source unavailable until dimensions for the entire document have been read", async () => {
    const document = pdf([[600, 800], [1200, 800]]);
    const lastPage = deferred<PDFPage>();
    document.document.getPage.mockImplementation(async number => number === 2
      ? await lastPage.promise as unknown as typeof document.pages[number] : document.pages[number - 1]);
    const paper = source("waiting-last-page");
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(document.proxy) });
    await render(paper);
    expect(current.preview).toMatchObject({ loading: true });
    expect(current.preview.sourceId).toBeUndefined();
    expect(current.preview.pageSizes).toBeUndefined();
    expect(current.pdfRef.current).toBeUndefined();
    await act(async () => lastPage.resolve(document.pages[1] as unknown as PDFPage));
    expect(current.preview).toMatchObject({ loading: false, sourceId: paper.id, pageCount: 2,
      pageSizes: [{ width: 600, height: 800 }, { width: 1200, height: 800 }] });
  });

  it("discards the old page sizes when their read completes after selecting another source", async () => {
    const old = pdf([[600, 800], [1200, 800]]);
    const next = pdf([[300, 500]]);
    const oldLastPage = deferred<PDFPage>();
    old.document.getPage.mockImplementation(async number => number === 2
      ? await oldLastPage.promise as unknown as typeof old.pages[number] : old.pages[number - 1]);
    mocks.getDocument.mockReturnValueOnce({ promise: Promise.resolve(old.proxy) }).mockReturnValueOnce({ promise: Promise.resolve(next.proxy) });
    await render(source("old-sizes"));
    await render(source("new-sizes"));
    const nextURL = current.preview.imageUrl;
    await act(async () => oldLastPage.resolve(old.pages[1] as unknown as PDFPage));
    expect(old.document.destroy).toHaveBeenCalledOnce();
    expect(current.pdfRef.current).toBe(next.proxy);
    expect(current.preview).toMatchObject({ sourceId: "new-sizes", pageCount: 1, imageUrl: nextURL,
      pageSizes: [{ width: 300, height: 500 }], width: 300, height: 500, loading: false });
  });

  it("does not publish an obsolete page render after the new source has loaded", async () => {
    const old = pdf([[600, 800], [800, 600]]);
    const next = pdf([[300, 500]]);
    const oldRender = deferred<void>();
    old.pages[1].render.mockReturnValue({ promise: oldRender.promise });
    mocks.getDocument.mockReturnValueOnce({ promise: Promise.resolve(old.proxy) }).mockReturnValueOnce({ promise: Promise.resolve(next.proxy) });
    await render(source("old-render"));
    await act(async () => current.setPageNo(2));
    expect(current.preview.loading).toBe(true);
    await render(source("new-render"));
    const nextURL = current.preview.imageUrl;
    await act(async () => oldRender.resolve());
    expect(current.preview).toMatchObject({ sourceId: "new-render", imageUrl: nextURL, pageCount: 1,
      width: 300, height: 500, loading: false });
    expect(current.pageNo).toBe(1);
    expect(current.pdfRef.current).toBe(next.proxy);
  });

  it("rejects an over-limit PDF without retaining an earlier source ID, page sizes, or preview image", async () => {
    const old = pdf([[600, 800]]);
    const oversized = pdf(Array.from({ length: 101 }, () => [600, 800]));
    mocks.getDocument.mockReturnValueOnce({ promise: Promise.resolve(old.proxy) }).mockReturnValueOnce({ promise: Promise.resolve(oversized.proxy) });
    await render(source("earlier-valid"));
    expect(current.preview.imageUrl).toBeDefined();
    await render(source("too-many-pages"));
    expect(oversized.document.destroy).toHaveBeenCalledOnce();
    expect(oversized.document.getPage).not.toHaveBeenCalled();
    expect(current.preview.loading).toBe(false);
    expect(current.preview.error).toContain("最多支持100页");
    expect(current.preview.sourceId).toBeUndefined();
    expect(current.preview.pageSizes).toBeUndefined();
    expect(current.preview.imageUrl).toBeUndefined();
    expect(current.pdfRef.current).toBeUndefined();
  });

  it("destroys an unbound PDF if reading a later page's dimensions fails", async () => {
    const document = pdf([[600, 800], [1200, 800]]);
    document.document.getPage.mockResolvedValueOnce(document.pages[0]).mockRejectedValueOnce(new Error("unreadable second page"));
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(document.proxy) });
    await render(source("invalid-page"));
    expect(current.preview).toMatchObject({ loading: false, error: "unreadable second page" });
    expect(current.preview.sourceId).toBeUndefined();
    expect(current.pdfRef.current).toBeUndefined();
    expect(document.document.destroy).toHaveBeenCalledOnce();
  });
});
