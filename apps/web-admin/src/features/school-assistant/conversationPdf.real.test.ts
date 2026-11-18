// @vitest-environment node
import { PDFDocument, StandardFonts } from "pdf-lib";
import { describe, expect, it, vi } from "vitest";
import { readChatAttachment } from "./conversationAttachments";

// Node 使用 PDF.js 的兼容入口与本地 worker；仍解析实际 PDF 字节，不替换文字提取结果。
vi.mock("pdfjs-dist", () => import("pdfjs-dist/legacy/build/pdf.mjs"));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", async () => {
  const { createRequire } = await import("node:module");
  const { pathToFileURL } = await import("node:url");
  return { default: pathToFileURL(createRequire(import.meta.url).resolve("pdfjs-dist/legacy/build/pdf.worker.mjs")).href };
});

async function pdfFile(pages: ("text" | "scan" | "blank")[]) {
  const document = await PDFDocument.create();
  const font = await document.embedFont(StandardFonts.Helvetica);
  const image = await document.embedPng(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l1sAAAAASUVORK5CYII=", "base64"));
  for (const [index, kind] of pages.entries()) {
    const page = document.addPage([300, 200]);
    if (kind === "text") page.drawText(`Page ${index + 1} content`, { x: 20, y: 100, font });
    if (kind === "scan") page.drawImage(image, { x: 20, y: 20, width: 200, height: 150 });
  }
  const data = await document.save();
  return new File([new Uint8Array(data).buffer], "attachment.pdf", { type: "application/pdf" });
}

describe("PDF attachment extraction with actual document bytes", () => {
  it("accepts a multi-page PDF with extractable text", async () => {
    const attachment = await readChatAttachment(await pdfFile(["text", "text"]));
    expect(attachment.content).toBe("第 1 页\nPage 1 content\n\n第 2 页\nPage 2 content");
  });

  it.each([
    { pages: ["scan", "scan"] as const, missingPage: 1 },
    { pages: ["text", "scan", "text"] as const, missingPage: 2 },
    { pages: ["text", "blank"] as const, missingPage: 2 },
  ])("rejects $pages without accepting a partial attachment", async ({ pages, missingPage }) => {
    await expect(readChatAttachment(await pdfFile([...pages]))).rejects.toThrow(`PDF 第 ${missingPage} 页未提取到文字`);
  });
});
