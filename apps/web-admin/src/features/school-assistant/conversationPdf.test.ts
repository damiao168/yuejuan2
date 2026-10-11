// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readConversationPDF } from "./conversationPdf";

const mocks = vi.hoisted(() => ({ getDocument: vi.fn() }));
vi.mock("pdfjs-dist", () => ({ getDocument: mocks.getDocument, GlobalWorkerOptions: {} }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "local-worker.mjs" }));

const file = { arrayBuffer: async () => new ArrayBuffer(1) } as File;
function fixture(pages = ["第一页", "第二页"]) {
  const getPage = vi.fn(async (number: number) => ({ getTextContent: async () => ({ items: [{ str: pages[number - 1] }, {}] }) }));
  const destroy = vi.fn(async () => undefined);
  const loading = { promise: Promise.resolve({ numPages: pages.length, getPage }), destroy };
  mocks.getDocument.mockReturnValue(loading);
  return { getPage, destroy, loading };
}
beforeEach(() => vi.clearAllMocks());

describe("PDF extraction lifecycle", () => {
  it("extracts each page and releases the loading task", async () => {
    const { destroy } = fixture();
    expect(await readConversationPDF(file, 60_000)).toBe("第 1 页\n第一页\n\n第 2 页\n第二页");
    expect(destroy).toHaveBeenCalledTimes(1);
  });

  it("destroys the task after a character-limit failure", async () => {
    const { destroy } = fixture(["过长的文字"]);
    await expect(readConversationPDF(file, 1)).rejects.toThrow("文件文字过多");
    expect(destroy).toHaveBeenCalledTimes(1);
  });

  it("does not create a PDF task when cancellation wins the file read", async () => {
    const controller = new AbortController();
    const pending = readConversationPDF(file, 60_000, controller.signal);
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(mocks.getDocument).not.toHaveBeenCalled();
  });

  it("destroys a pending worker and ignores a document that resolves after cancellation", async () => {
    const { loading, getPage, destroy } = fixture();
    let finish!: (document: { numPages: number; getPage: typeof getPage }) => void;
    loading.promise = new Promise((resolve) => { finish = resolve; });
    const controller = new AbortController();
    const pending = readConversationPDF(file, 60_000, controller.signal);
    await Promise.resolve();
    controller.abort();
    expect(destroy).toHaveBeenCalledTimes(1);
    finish({ numPages: 1, getPage });
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(getPage).not.toHaveBeenCalled();
  });
});
