// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { PDFDocument } from "pdf-lib";
import { captureFileBytes, reorderCapturePdf } from "./capturePdfPreview";

describe("答卷PDF页序重排", () => {
  async function source() { const pdf = await PDFDocument.create(); pdf.addPage([100, 200]); pdf.addPage([300, 400]); return new File([new Uint8Array(await pdf.save()).buffer], "原答卷.pdf", { type: "application/pdf" }); }
  it("复制原页面改变顺序与保留尺寸，不把缩略图作为上传答卷", async () => {
    const file = await source(); const reordered = await reorderCapturePdf(file, [1, 0]);
    const pdf = await PDFDocument.load(await captureFileBytes(reordered));
    expect(pdf.getPages().map(page => page.getSize())).toEqual([{ width: 300, height: 400 }, { width: 100, height: 200 }]);
    expect(reordered.name).toBe("原答卷.页序已调整.pdf");
    expect(pdf.getCreationDate()).toEqual(new Date("2000-01-01T00:00:00.000Z"));
    expect(new Uint8Array(await captureFileBytes(await reorderCapturePdf(file, [1, 0])))).toEqual(new Uint8Array(await captureFileBytes(reordered)));
    expect((await PDFDocument.load(await captureFileBytes(file))).getPage(0).getWidth()).toBe(100);
  });
  it.each([[0], [0, 0], [0, 2], [-1, 1], [.5, 1]].map(order => ({ order })))("缺页/重复/越界页序$order在生成前拒绝", async ({ order }) => {
    await expect(reorderCapturePdf(await source(), order)).rejects.toThrow("页序不完整");
  });
});
