// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { AnswerDocumentViewer } from "./AnswerDocumentViewer";

describe("answer document reader", () => {
  it("reports only successfully decoded images as readable and clears the result on errors or document changes", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div"); document.body.appendChild(host); const root = createRoot(host);
    const readable = vi.fn();
    try {
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:corrupt", contentType: "image/png" }} onReadableChange={readable} />));
      expect(readable).toHaveBeenLastCalledWith(false);
      await act(async () => host.querySelector("img")!.dispatchEvent(new Event("error")));
      expect(readable).toHaveBeenLastCalledWith(false); expect(host.textContent).toContain("答卷预览未能显示");

      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:image", contentType: "image/png" }} onReadableChange={readable} />));
      const image = host.querySelector("img")!;
      await act(async () => image.dispatchEvent(new Event("load")));
      expect(readable).toHaveBeenLastCalledWith(false);
      Object.defineProperties(image, { naturalWidth: { configurable: true, value: 800 }, naturalHeight: { configurable: true, value: 400 } });
      await act(async () => image.dispatchEvent(new Event("load")));
      expect(readable).toHaveBeenLastCalledWith(true);
      await act(async () => image.dispatchEvent(new Event("error")));
      expect(readable).toHaveBeenLastCalledWith(false);

      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:pdf", contentType: "application/pdf" }} onReadableChange={readable} />));
      await act(async () => host.querySelector("iframe")!.dispatchEvent(new Event("load")));
      expect(readable).toHaveBeenLastCalledWith(false); expect(host.textContent).toContain("PDF 阅读器载入不能确认内容已显示");
      expect(host.textContent).toContain("请重新读取本题可显示图片或答卷文字"); expect(host.textContent).toContain("联系负责人补核材料后提交");
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:tiff", contentType: "image/tiff" }} onReadableChange={readable} />));
      expect(readable).toHaveBeenLastCalledWith(false); expect(host.querySelector("img")).toBeNull();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("supports image zoom and rotation, shows PDF in a reader and offers the original for TIFF", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div"); document.body.appendChild(host); const root = createRoot(host);
    try {
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:image", contentType: "image/png", filename: "answer.png" }} />));
      const click = async (text: string) => act(async () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""))!.click());
      await click("放大"); expect(host.textContent).toContain("125%");
      await click("旋转 90°"); expect(host.querySelector("img")?.style.transform).toBe("rotate(90deg)");
      await click("适合窗口"); expect(host.textContent).toContain("100%");
      expect(host.querySelector("img")?.style.transform).toBe("rotate(90deg)");
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:pdf", contentType: "application/pdf", filename: "answer.pdf" }} />));
      expect(host.querySelector("img")).toBeNull(); expect(host.querySelector("iframe")?.src).toContain("blob:pdf#toolbar=1");
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:tiff", contentType: "image/tiff", filename: "answer.tiff" }} />));
      expect(host.querySelector("iframe")).toBeNull(); expect(host.querySelector("img")).toBeNull();
      expect(host.textContent).toContain("当前答卷为 TIFF"); expect(host.querySelector("a")?.getAttribute("download")).toBe("answer.tiff");
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("fits the rotated image bounds before applying zoom and keeps each edge scrollable", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div"); document.body.appendChild(host); const root = createRoot(host);
    try {
      await act(async () => root.render(<AnswerDocumentViewer document={{ url: "blob:dimensions", contentType: "image/png" }} />));
      const viewport = host.querySelector<HTMLDivElement>(".answer-document-viewport")!;
      const image = host.querySelector("img")!;
      const canvas = host.querySelector<HTMLDivElement>(".answer-document-canvas")!;
      Object.defineProperties(viewport, { clientWidth: { configurable: true, value: 300 }, clientHeight: { configurable: true, value: 200 } });
      Object.defineProperties(image, { naturalWidth: { configurable: true, value: 800 }, naturalHeight: { configurable: true, value: 400 } });
      await act(async () => { window.dispatchEvent(new Event("resize")); image.dispatchEvent(new Event("load")); });
      expect(image.style.width).toBe("300px"); expect(image.style.height).toBe("150px");
      const click = async (text: string) => act(async () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.replace(/\s/g, "") === text.replace(/\s/g, ""))!.click());
      await click("旋转 90°");
      expect(image.style.width).toBe("200px"); expect(image.style.height).toBe("100px");
      expect(canvas.style.width).toBe("100px"); expect(canvas.style.height).toBe("200px");
      expect(image.style.left).toBe("-50px"); expect(image.style.top).toBe("50px");
      await click("放大");
      expect(image.style.width).toBe("250px"); expect(canvas.style.width).toBe("125px"); expect(canvas.style.height).toBe("250px");
      viewport.scrollLeft = 25; viewport.scrollTop = 50;
      await click("适合窗口");
      expect(viewport.scrollLeft).toBe(0); expect(viewport.scrollTop).toBe(0);
      expect(canvas.style.width).toBe("100px"); expect(canvas.style.height).toBe("200px");
      expect(image.style.transform).toBe("rotate(90deg)");
    } finally { await act(async () => root.unmount()); host.remove(); }
  });
});
