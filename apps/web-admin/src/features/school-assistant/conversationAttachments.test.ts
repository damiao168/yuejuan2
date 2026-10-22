// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ pdf: vi.fn(), loads: 0 }));
vi.mock("./conversationPdf", () => { mocks.loads += 1; return { readConversationPDF: mocks.pdf }; });

function file(name: string, content = "正文", size = 4, type = "text/plain") {
  return { name, size, type, text: vi.fn(async () => content) } as unknown as File;
}
beforeEach(() => { vi.resetModules(); vi.clearAllMocks(); mocks.loads = 0; mocks.pdf.mockResolvedValue("第 1 页\nPDF 正文"); });

describe("attachment loading boundaries", () => {
  it("reads text without evaluating the PDF module", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    expect(await readChatAttachment(file("note.txt", "  内容  "))).toMatchObject({ content: "内容", media_type: "text/plain" });
    expect(mocks.loads).toBe(0);
  });

  it("validates type and byte limits before loading PDF code", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    await expect(readChatAttachment(file("large.pdf", "", 10 * 1024 * 1024 + 1, "application/pdf"))).rejects.toThrow("10 MB");
    await expect(readChatAttachment(file("image.webp", "", 4, "image/webp"))).rejects.toThrow("支持 PNG");
    expect(mocks.loads).toBe(0);
  });

  it("reads a pasted PNG as image input without loading PDF or extracting it as text", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    const image = new File([new Uint8Array([137, 80, 78, 71])], "截图.png", { type: "image/png" });
    expect(await readChatAttachment(image)).toEqual({ name: "截图.png", media_type: "image/png", size: 4, content: "data:image/png;base64,iVBORw==" });
    expect(mocks.loads).toBe(0);
  });

  it("rejects oversized and empty images before reading", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    await expect(readChatAttachment(file("large.png", "", 3 * 1024 * 1024 + 1, "image/png"))).rejects.toThrow("3 MB");
    await expect(readChatAttachment(new File([], "empty.jpg", { type: "image/jpeg" }))).rejects.toThrow("图片为空");
    expect(mocks.loads).toBe(0);
  });

  it("cancels an in-progress image read", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    const controller = new AbortController();
    const pending = readChatAttachment(new File(["image"], "截图.png", { type: "image/png" }), controller.signal);
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  });

  it("loads PDF on demand and passes the owning cancellation signal", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    const controller = new AbortController();
    const pdf = file("answer.pdf", "", 4, "application/pdf");
    expect(await readChatAttachment(pdf, controller.signal)).toMatchObject({ content: "第 1 页\nPDF 正文", media_type: "application/pdf" });
    expect(mocks.loads).toBe(1);
    expect(mocks.pdf).toHaveBeenCalledWith(pdf, 60_000, controller.signal);
  });

  it("never starts loading for an already cancelled upload", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    const controller = new AbortController(); controller.abort();
    await expect(readChatAttachment(file("answer.pdf"), controller.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(mocks.loads).toBe(0);
  });

  it("rejects a text result that arrives after cancellation", async () => {
    const { readChatAttachment } = await import("./conversationAttachments");
    let finish!: (text: string) => void;
    const input = file("note.txt");
    input.text = () => new Promise((resolve) => { finish = resolve; });
    const controller = new AbortController();
    const pending = readChatAttachment(input, controller.signal);
    controller.abort(); finish("旧附件内容");
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  });

  it.each(["", "binary\x00data", "字".repeat(60_001)])("keeps empty, binary and character-limit validation", async (content) => {
    const { readChatAttachment } = await import("./conversationAttachments");
    await expect(readChatAttachment(file("note.txt", content))).rejects.toThrow();
    expect(mocks.loads).toBe(0);
  });
});
