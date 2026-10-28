// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderPdfAnswerPages } from "./pdfAnswerPages";
const pdf = vi.hoisted(() => ({ getDocument: vi.fn(), GlobalWorkerOptions: { workerSrc: "" }, getPage: vi.fn(), render: vi.fn(), cleanup: vi.fn(), destroy: vi.fn() }));
vi.mock("pdfjs-dist", () => ({ getDocument: pdf.getDocument, GlobalWorkerOptions: pdf.GlobalWorkerOptions }));
beforeEach(() => {
  vi.clearAllMocks(); pdf.destroy.mockResolvedValue(undefined);
  pdf.getDocument.mockReturnValue({ promise: Promise.resolve({ numPages: 2, getPage: pdf.getPage }), destroy: pdf.destroy });
  pdf.render.mockReturnValue({ promise: Promise.resolve() });
  pdf.getPage.mockResolvedValue({ getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }), render: pdf.render, cleanup: pdf.cleanup });
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({} as never);
  vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue("data:image/png;base64,YQ==");
});
afterEach(() => vi.restoreAllMocks());
describe("controlled local PDF pages", () => {
  it("renders each local original page, uses no evaluation, and cleans the decoding task", async () => {
    const onPage = vi.fn(); await renderPdfAnswerPages("blob:original", new AbortController().signal, onPage);
    expect(pdf.getDocument).toHaveBeenCalledWith({ url: "blob:original", isEvalSupported: false });
    expect(pdf.getPage.mock.calls.map(call => call[0])).toEqual([1, 2]); expect(onPage).toHaveBeenCalledTimes(2); expect(onPage).toHaveBeenLastCalledWith("data:image/png;base64,YQ==", 2);
    expect(pdf.cleanup).toHaveBeenCalledTimes(2); expect(pdf.destroy).toHaveBeenCalledOnce();
  });
  it("stops on a failed page and still releases its canvas and task", async () => {
    pdf.render.mockReturnValueOnce({ promise: Promise.resolve() }).mockReturnValueOnce({ promise: Promise.reject(new Error("broken page")) });
    const onPage = vi.fn(); await expect(renderPdfAnswerPages("blob:original", new AbortController().signal, onPage)).rejects.toThrow("broken page");
    expect(onPage).toHaveBeenCalledTimes(1); expect(pdf.cleanup).toHaveBeenCalledTimes(2); expect(pdf.destroy).toHaveBeenCalledOnce();
  });
  it("rejects remote sources, excessive page counts and cancelled requests", async () => {
    await expect(renderPdfAnswerPages("https://example.com/answer.pdf", new AbortController().signal, vi.fn())).rejects.toThrow("尚未下载"); expect(pdf.getDocument).not.toHaveBeenCalled();
    pdf.getDocument.mockReturnValueOnce({ promise: Promise.resolve({ numPages: 51, getPage: pdf.getPage }), destroy: pdf.destroy });
    await expect(renderPdfAnswerPages("blob:large", new AbortController().signal, vi.fn())).rejects.toThrow("50 页");
    const abort = new AbortController(); abort.abort(); await expect(renderPdfAnswerPages("blob:cancel", abort.signal, vi.fn())).rejects.toThrow("取消"); expect(pdf.getPage).not.toHaveBeenCalled();
  });
});
