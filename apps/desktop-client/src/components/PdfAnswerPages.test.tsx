// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PdfAnswerPages } from "./PdfAnswerPages";
const renderer = vi.hoisted(() => ({ run: vi.fn() }));
vi.mock("../lib/pdfAnswerPages", () => ({ renderPdfAnswerPages: renderer.run }));
afterEach(() => vi.resetAllMocks());
describe("PDF answer completeness", () => {
  it("requires every rendered page to decode and revokes approval on any display failure or document change", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div"); const root = createRoot(host); const readable = vi.fn();
    renderer.run.mockImplementation(async (_url, _signal, onPage) => { onPage("data:image/png;base64,YQ==", 2); onPage("data:image/png;base64,Yg==", 2); });
    try {
      await act(async () => root.render(<PdfAnswerPages url="blob:a" zoom={1} onReadableChange={readable} />));
      const images = host.querySelectorAll("img"); expect(images).toHaveLength(2); expect(readable).toHaveBeenLastCalledWith(false);
      for (const image of images) Object.defineProperties(image, { naturalWidth: { value: 300 }, naturalHeight: { value: 400 } });
      await act(async () => images[0].dispatchEvent(new Event("load"))); expect(readable).toHaveBeenLastCalledWith(false);
      await act(async () => images[1].dispatchEvent(new Event("load"))); expect(readable).toHaveBeenLastCalledWith(true);
      await act(async () => images[0].dispatchEvent(new Event("error"))); expect(readable).toHaveBeenLastCalledWith(false); expect(host.textContent).toContain("第 1 页显示失败");
      renderer.run.mockImplementation(() => new Promise(() => {}));
      await act(async () => root.render(<PdfAnswerPages url="blob:b" zoom={1} onReadableChange={readable} />));
      expect(readable).toHaveBeenLastCalledWith(false); expect(host.querySelectorAll("img")).toHaveLength(0);
    } finally { await act(async () => root.unmount()); }
  });
  it("does not approve partial rendering and rejects late pages after unmount", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div"); const root = createRoot(host); const readable = vi.fn(); let signal!: AbortSignal; let page!: (image: string, count: number) => void;
    renderer.run.mockImplementation((_url, nextSignal, onPage) => { signal = nextSignal; page = onPage; onPage("data:image/png;base64,YQ==", 2); return Promise.reject(new Error("第 2 页解码失败")); });
    await act(async () => root.render(<PdfAnswerPages url="blob:a" zoom={1} onReadableChange={readable} />));
    expect(readable).toHaveBeenLastCalledWith(false); expect(host.textContent).toContain("第 2 页解码失败");
    await act(async () => root.unmount()); expect(signal.aborted).toBe(true); await act(async () => page("late", 2)); expect(readable).toHaveBeenLastCalledWith(false);
  });
});
