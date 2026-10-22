// @vitest-environment jsdom
import { createHash } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ANSWER_SHEET_ORDER_LIMITS, orderAnswerSheetSourcePages } from "./orderAnswerSheetSourcePages";

const mocks = vi.hoisted(() => ({ order: vi.fn(), getDocument: vi.fn(), destroy: vi.fn(), render: vi.fn() }));
vi.mock("../../../api/answerSheetPageOrder", () => ({ orderAnswerSheetPages: mocks.order }));
vi.mock("pdfjs-dist", () => ({ GlobalWorkerOptions: {}, getDocument: mocks.getDocument }));
const files = [new File(["first-pdf"], "z.pdf", { type: "application/pdf" }), new File(["second-pdf"], "a.pdf", { type: "application/pdf" })];
const run = (patch: Partial<Parameters<typeof orderAnswerSheetSourcePages>[1]> = {}) => orderAnswerSheetSourcePages(files, {
  examId: "exam-1", signal: new AbortController().signal, isCurrent: () => true, ...patch
});
describe("multimodal answer-sheet page ordering", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.destroy.mockResolvedValue(undefined);
    mocks.render.mockReturnValue({ promise: Promise.resolve() });
    const page = { getViewport: ({ scale }: { scale: number }) => ({ width: 2000 * scale, height: 3000 * scale }), render: mocks.render };
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve({ numPages: 1, getPage: async () => page }), destroy: mocks.destroy });
    mocks.order.mockResolvedValue({ status: "ordered", page_order: ["page-2", "page-1"], reason_codes: [] });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ fillRect: vi.fn(), drawImage: vi.fn() } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue(`data:image/jpeg;base64,${btoa("page-thumbnail")}`);
    Object.defineProperty(crypto, "subtle", { configurable: true, value: { digest: async (_algorithm: string, data: Uint8Array) =>
      new Uint8Array(createHash("sha256").update(data).digest()).buffer } });
  });
  afterEach(() => vi.restoreAllMocks());

  it("sends bounded real-page images and maps returned IDs to original flat page indices", async () => {
    expect(await run()).toEqual([1, 0]);
    const [examId, pages] = mocks.order.mock.calls[0];
    expect(examId).toBe("exam-1"); expect(pages.map((page: { page_id: string }) => page.page_id)).toEqual(["page-1", "page-2"]);
    expect(pages[0]).toMatchObject({ width: 667, height: 1000, media_type: "image/jpeg",
      sha256: createHash("sha256").update("page-thumbnail").digest("hex") });
    expect(JSON.stringify(pages)).not.toContain("z.pdf"); expect(JSON.stringify(pages)).not.toContain("a.pdf");
    expect(mocks.destroy).toHaveBeenCalledTimes(2);
  });

  it("flattens all PDF pages rather than asking the model to sort only whole files", async () => {
    const page = { getViewport: ({ scale }: { scale: number }) => ({ width: 200 * scale, height: 300 * scale }), render: mocks.render };
    mocks.getDocument.mockReturnValueOnce({ promise: Promise.resolve({ numPages: 2, getPage: async () => page }), destroy: mocks.destroy });
    mocks.order.mockResolvedValue({ status: "ordered", page_order: ["page-3", "page-1", "page-2"], reason_codes: [] });
    expect(await run()).toEqual([2, 0, 1]);
    expect(mocks.order.mock.calls[0][1]).toHaveLength(3);
  });

  it.each([
    { status: "uncertain", page_order: [] },
    { status: "ordered", page_order: ["page-1"] },
    { status: "ordered", page_order: ["page-1", "page-1"] },
    { status: "ordered", page_order: ["page-1", "invented"] }
  ])("rejects uncertain, missing, repeated or fabricated page order %j", async result => {
    mocks.order.mockResolvedValue(result);
    await expect(run()).rejects.toThrow("页序");
  });

  it.each([
    ["MODEL_OUTPUT_INVALID", "页序格式不完整"],
    ["MODEL_RATE_LIMITED", "额度不足"],
    ["DUPLICATE_PRINTED_PAGE_NUMBER", "重复页码"],
    ["PAGE_ORDER_EVIDENCE_INSUFFICIENT", "跨页内容仍不足"]
  ])("explains the bounded reason %s rather than blaming all multi-file uploads", async (code, message) => {
    mocks.order.mockResolvedValue({ status: "uncertain", page_order: [], reason_codes: [code] });
    await expect(run()).rejects.toThrow(message);
  });

  it("does not send oversized page images after all compression attempts", async () => {
    vi.mocked(HTMLCanvasElement.prototype.toDataURL).mockReturnValue(`data:image/jpeg;base64,${"A".repeat(ANSWER_SHEET_ORDER_LIMITS.imageBytes * 2)}`);
    await expect(run()).rejects.toThrow("超过大小限制");
    expect(mocks.order).not.toHaveBeenCalled(); expect(mocks.destroy).toHaveBeenCalledOnce();
  });

  it("does not send a model request after the source scope changes during rendering", async () => {
    let active = true;
    mocks.render.mockImplementation(() => { active = false; return { promise: Promise.resolve() }; });
    await expect(run({ isCurrent: () => active })).rejects.toThrow("已取消");
    expect(mocks.order).not.toHaveBeenCalled(); expect(mocks.destroy).toHaveBeenCalledOnce();
  });

  it("discards a late model result after cancellation", async () => {
    const controller = new AbortController();
    mocks.order.mockImplementation(async () => { controller.abort(); return { status: "ordered", page_order: ["page-2", "page-1"] }; });
    await expect(run({ signal: controller.signal })).rejects.toThrow("已取消");
  });
});
