import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import { PDFArray, PDFDocument, PDFHexString, PDFName, PDFRawStream, concatTransformationMatrix, decodePDFRawStream, degrees, popGraphicsState, pushGraphicsState } from "pdf-lib";
import { describe, expect, it, vi } from "vitest";
import { readAnswerSheetSourceHashes } from "./answerSheetSourceManifest";
import { ANSWER_SHEET_BATCH_LIMITS, mergeAnswerSheetSources } from "./mergeAnswerSheetSources";

const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=", "base64");
// 4×2 白底左上角红点的合成 JPEG；真实编码数据用于验证合并时不做二次压缩。
const jpeg = Buffer.from("/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAIBAQEBAQIBAQECAgICAgQDAgICAgUEBAMEBgUGBgYFBgYGBwkIBgcJBwYGCAsICQoKCgoKBggLDAsKDAkKCgr/2wBDAQICAgICAgUDAwUKBwYHCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgr/wAARCAACAAQDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD98dA0DQvCmhWXhbwtotppumabaR2unadp9ssMFrBGoSOKONAFRFUBVVQAAAAMCiiiscP/AAIei/I9DNv+RriP8c//AEpn/9k=", "base64");

function imageFile(name = "page.png") { return new File([png], name, { type: "image/png" }); }
function otherImage(name = "back.jpg") { return new File([jpeg], name, { type: "image/jpeg" }); }
async function sha256(file: File) { return createHash("sha256").update(Buffer.from(await file.arrayBuffer())).digest("hex"); }
async function pdfFile(name: string, sizes: [number, number][]) {
  const pdf = await PDFDocument.create();
  for (const size of sizes) pdf.addPage(size);
  return new File([new Uint8Array(await pdf.save())], name, { type: "application/pdf" });
}
function orientedJpeg(orientation: number, little = true) {
  const header = new Uint8Array(36);
  const data = new DataView(header.buffer);
  data.setUint16(0, 0xffe1); data.setUint16(2, 34);
  header.set([69, 120, 105, 102, 0, 0], 4);
  data.setUint16(10, little ? 0x4949 : 0x4d4d); data.setUint16(12, 42, little); data.setUint32(14, 8, little);
  data.setUint16(18, 1, little); data.setUint16(20, 0x0112, little); data.setUint16(22, 3, little);
  data.setUint32(24, 1, little); data.setUint16(28, orientation, little);
  return new Uint8Array(Buffer.concat([jpeg.subarray(0, 2), header, jpeg.subarray(2)]));
}

describe("mergeAnswerSheetSources", () => {
  it("returns the original single image or PDF rather than changing its filename or bytes", async () => {
    const image = imageFile();
    const pdf = await pdfFile("original.pdf", [[120, 200]]);
    expect(await mergeAnswerSheetSources([image])).toBe(image);
    expect(await mergeAnswerSheetSources([pdf])).toBe(pdf);
  });

  it("preserves selection order, PDF page boxes and rotation when mixing PDF and PNG files", async () => {
    const original = await PDFDocument.create();
    const first = original.addPage([300, 400]); first.setRotation(degrees(90)); first.setCropBox(10, 20, 100, 200);
    original.addPage([600, 800]);
    const firstFile = new File([new Uint8Array(await original.save())], "first.pdf", { type: "application/pdf" });
    const lastFile = await pdfFile("last.pdf", [[123, 456]]);
    const progress: number[] = [];
    const merged = await mergeAnswerSheetSources([firstFile, imageFile(), lastFile], { onProgress: processed => progress.push(processed) });
    const actual = await PDFDocument.load(await merged.arrayBuffer());
    expect(actual.getPages().map(page => [page.getWidth(), page.getHeight()])).toEqual([[300, 400], [600, 800], [1, 1], [123, 456]]);
    expect(actual.getPage(0).getRotation().angle).toBe(90);
    expect(actual.getPage(0).getCropBox()).toEqual({ x: 10, y: 20, width: 100, height: 200 });
    expect(progress).toEqual([1, 2, 3]);
    expect(merged.type).toBe("application/pdf");
    expect(merged.name).toBe("first-等3份答题卡.pdf");
  });

  for (const [orientation, matrix] of [
    [1, "1 0 0 1 0 0"], [2, "-1 0 0 1 4 0"], [3, "-1 0 0 -1 4 2"], [4, "1 0 0 -1 0 2"],
    [5, "0 -1 -1 0 2 4"], [6, "0 -1 1 0 0 4"], [7, "0 1 1 0 0 0"], [8, "0 1 -1 0 2 0"]
  ] as const) {
    it(`preserves JPEG EXIF orientation ${orientation}, including mirrored captures, without re-encoding`, async () => {
      const bytes = orientedJpeg(orientation, orientation % 2 === 0);
      const merged = await mergeAnswerSheetSources([new File([bytes], "photo.jpg", { type: "image/jpeg" }), imageFile()]);
      const pdf = await PDFDocument.load(await merged.arrayBuffer());
      const page = pdf.getPage(0);
      expect(page.getSize()).toEqual(orientation >= 5 ? { width: 2, height: 4 } : { width: 4, height: 2 });
      const contents = page.node.Contents();
      if (!(contents instanceof PDFArray)) throw new Error("合并页面缺少内容流数组");
      const operations = Array.from({ length: contents.size() }, (_, index) => {
        const stream = contents.lookup(index, PDFRawStream);
        return new TextDecoder().decode(decodePDFRawStream(stream).decode());
      }).join("\n");
      expect(operations).toContain(`${matrix} cm`);
      const imageStreams = pdf.context.enumerateIndirectObjects().map(([, object]) => object).filter((object): object is PDFRawStream => object instanceof PDFRawStream && object.dict.get(PDFName.of("Filter")) === PDFName.of("DCTDecode"));
      expect(imageStreams).toHaveLength(1);
      expect(imageStreams[0].contents).toEqual(bytes);
    });
  }

  it("validates file count, total byte size, unsupported and empty files before reading", async () => {
    await expect(mergeAnswerSheetSources([])).rejects.toThrow("请先选择");
    await expect(mergeAnswerSheetSources(Array.from({ length: 51 }, () => imageFile()))).rejects.toThrow("50 个");
    await expect(mergeAnswerSheetSources([{ name: "oversize.pdf", type: "application/pdf", size: ANSWER_SHEET_BATCH_LIMITS.bytes + 1 } as File])).rejects.toThrow("100 MiB");
    await expect(mergeAnswerSheetSources([new File([], "empty.png", { type: "image/png" })])).rejects.toThrow("文件为空");
    await expect(mergeAnswerSheetSources([new File(["text"], "notes.txt", { type: "text/plain" })])).rejects.toThrow("仅支持 PDF");
  });

  it("enforces page count for a single PDF and across all merged files", async () => {
    const hundred = await pdfFile("hundred.pdf", Array.from({ length: 100 }, () => [10, 20] as [number, number]));
    expect(await mergeAnswerSheetSources([hundred])).toBe(hundred);
    await expect(mergeAnswerSheetSources([hundred, imageFile()])).rejects.toThrow("100 页");
    const oversized = await pdfFile("oversized.pdf", Array.from({ length: 101 }, () => [10, 20] as [number, number]));
    await expect(mergeAnswerSheetSources([oversized])).rejects.toThrow("100 页");
  });

  it("reports the failing file index rather than returning a partially merged PDF", async () => {
    const broken = new File(["broken PDF"], "broken.pdf", { type: "application/pdf" });
    await expect(mergeAnswerSheetSources([imageFile(), broken])).rejects.toThrow("第 2 个");
  });

  it("rejects an already uploaded original by content, including a renamed file", async () => {
    const existing = imageFile("front.png");
    const renamed = imageFile("renamed-front.png");
    await expect(mergeAnswerSheetSources([renamed], { existingHashes: [(await sha256(existing)).toUpperCase()] })).rejects.toThrow("renamed-front.png”已经上传过");
  });

  it("allows the same filename when its content is different", async () => {
    const changed = otherImage("page.png");
    const merged = await mergeAnswerSheetSources([changed], { existingHashes: [await sha256(imageFile())] });
    expect(merged).toBe(changed);
  });

  it("reorders individual pages across PDF files using the verified multimodal page order", async () => {
    const frontAndBack = await pdfFile("uploaded-second.pdf", [[300, 400], [500, 600]]);
    const middle = await pdfFile("uploaded-first.pdf", [[700, 800]]);
    const resolvePageOrder = vi.fn(async () => [2, 0, 1]);
    const output = await mergeAnswerSheetSources([frontAndBack, middle], { resolvePageOrder });
    const actual = await PDFDocument.load(await output.arrayBuffer());
    expect(actual.getPages().map(page => [page.getWidth(), page.getHeight()])).toEqual([[700, 800], [300, 400], [500, 600]]);
    expect(resolvePageOrder).toHaveBeenCalledWith([frontAndBack, middle]);
  });

  it("rejects duplicate source bytes before requesting multimodal ordering", async () => {
    const resolvePageOrder = vi.fn(async () => [1, 0]);
    const front = imageFile("front.png");
    await expect(mergeAnswerSheetSources([front, otherImage()], { existingHashes: [await sha256(front)], resolvePageOrder }))
      .rejects.toThrow("已经上传过");
    expect(resolvePageOrder).not.toHaveBeenCalled();
  });

  it.each([[0, 0], [1], [0, 3], [0, .5]])("rejects incomplete or fabricated page indices %s", async (...order) => {
    await expect(mergeAnswerSheetSources([imageFile(), otherImage()], { resolvePageOrder: async () => order }))
      .rejects.toThrow("页序不完整");
  });

  it("rejects a repeated side inside the batch rather than silently producing one page", async () => {
    const progress = vi.fn();
    await expect(mergeAnswerSheetSources([imageFile("front.png"), imageFile("back.png")], { onProgress: progress })).rejects.toThrow("back.png”与本批“front.png”内容重复");
    expect(progress).not.toHaveBeenCalled();
  });

  it("does not upload just the other side when one original was already uploaded", async () => {
    await expect(mergeAnswerSheetSources([imageFile("front.png"), otherImage()], { existingHashes: [await sha256(imageFile())] })).rejects.toThrow("front.png”已经上传过");
  });

  it("retains both sides and produces stable bytes when the same batch is selected later", async () => {
    const files = [imageFile("front.png"), otherImage()];
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-10-16T00:00:00Z"));
      const first = await mergeAnswerSheetSources(files);
      vi.setSystemTime(new Date("2026-10-17T05:00:00Z"));
      const second = await mergeAnswerSheetSources(files);
      expect(await sha256(second)).toBe(await sha256(first));
      const checkedAgainstHistory = await mergeAnswerSheetSources(files, { existingHashes: ["a".repeat(64)] });
      expect(await sha256(checkedAgainstHistory)).toBe(await sha256(first));
      const renamed = await mergeAnswerSheetSources([imageFile("renamed.png"), otherImage("renamed.jpg")]);
      expect(await sha256(renamed)).toBe(await sha256(first));
      const pdf = await PDFDocument.load(await second.arrayBuffer());
      expect(pdf.getPages().map(page => page.getSize())).toEqual([{ width: 1, height: 1 }, { width: 4, height: 2 }]);
    } finally { vi.useRealTimers(); }
  });

  it("persists the ordered original fingerprints inside the merged PDF and detects a renamed single side later", async () => {
    const files = [imageFile("front.png"), otherImage()];
    const merged = await mergeAnswerSheetSources(files);
    const sourceHashes = await readAnswerSheetSourceHashes(merged);
    expect(sourceHashes).toEqual(await Promise.all(files.map(sha256)));
    await expect(mergeAnswerSheetSources([imageFile("renamed-front.png")], { existingSourceHashes: sourceHashes }))
      .rejects.toThrow("renamed-front.png”已经上传过");
    expect(await mergeAnswerSheetSources([otherImage("front.png")], { existingSourceHashes: [await sha256(files[0])] }))
      .toHaveProperty("name", "front.png");
  });

  it("filters malformed fingerprint values and normalizes valid ones without executing PDF metadata", async () => {
    const document = await PDFDocument.create(); document.addPage([100, 200]);
    const validHash = await sha256(imageFile());
    const manifest = { version: 1, hashes: [validHash.toUpperCase(), "not-a-hash", 42, null, validHash, "b".repeat(64)] };
    document.catalog.set(PDFName.of("EduGradeAnswerSheetSourceManifest"), PDFHexString.fromText(JSON.stringify(manifest)));
    const file = new File([new Uint8Array(await document.save())], "external.pdf", { type: "application/pdf" });
    expect(await readAnswerSheetSourceHashes(file)).toEqual([validHash, "b".repeat(64)]);
  });

  it.each([
    "invalid-json", JSON.stringify({ version: 2, hashes: ["a".repeat(64)] }),
    JSON.stringify({ version: 1, hashes: "a".repeat(64) }),
    JSON.stringify({ version: 1, hashes: Array.from({ length: 51 }, () => "a".repeat(64)) })
  ])("ignores an unsupported or malformed source manifest: %s", async text => {
    const document = await PDFDocument.create(); document.addPage([100, 200]);
    document.catalog.set(PDFName.of("EduGradeAnswerSheetSourceManifest"), PDFHexString.fromText(text));
    const file = new File([new Uint8Array(await document.save())], "external.pdf", { type: "application/pdf" });
    expect(await readAnswerSheetSourceHashes(file)).toEqual([]);
  });

  it("treats legacy PDFs without a manifest and unreadable files as having no original fingerprint list", async () => {
    expect(await readAnswerSheetSourceHashes(await pdfFile("legacy.pdf", [[100, 200]]))).toEqual([]);
    expect(await readAnswerSheetSourceHashes(new Blob(["invalid PDF"]))).toEqual([]);
    expect(await readAnswerSheetSourceHashes(new Blob())).toEqual([]);
  });

  it("does not compare the new merged output against fingerprints that describe only original inputs", async () => {
    const files = [imageFile("front.png"), otherImage()];
    const merged = await mergeAnswerSheetSources(files);
    const next = await mergeAnswerSheetSources(files, { existingSourceHashes: [await sha256(merged)] });
    expect(await sha256(next)).toBe(await sha256(merged));
  });

  it("still detects an old merged whole sheet created before source manifests were added", async () => {
    // 模拟已部署旧版的双面合成规则；新增清单不能让旧整套文件被再次登记。
    const legacy = await PDFDocument.create();
    const stableDate = new Date("2000-01-01T00:00:00.000Z");
    legacy.setCreationDate(stableDate); legacy.setModificationDate(stableDate);
    for (const [index, bytes] of [png, jpeg].entries()) {
      const input = new Uint8Array(bytes);
      const image = index === 0 ? await legacy.embedPng(input) : await legacy.embedJpg(input);
      const page = legacy.addPage([image.width, image.height]);
      page.pushOperators(pushGraphicsState(), concatTransformationMatrix(1, 0, 0, 1, 0, 0));
      page.drawImage(image, { x: 0, y: 0, width: image.width, height: image.height });
      page.pushOperators(popGraphicsState());
    }
    const previous = new File([new Uint8Array(await legacy.save())], "legacy-sheet.pdf", { type: "application/pdf" });
    await expect(mergeAnswerSheetSources([imageFile("front.png"), otherImage()], { existingHashes: [await sha256(previous)] }))
      .rejects.toThrow("这套答题卡已经上传过");
  });

  it("rejects a previously merged whole sheet without requiring original-image hashes", async () => {
    const files = [imageFile("front.png"), otherImage()];
    const previous = await mergeAnswerSheetSources(files);
    await expect(mergeAnswerSheetSources(files, { existingHashes: [await sha256(previous)] })).rejects.toThrow("这套答题卡已经上传过");
  });

  it("allows explicitly combining old sources but still rejects a duplicated whole sheet", async () => {
    const files = [imageFile("front.png"), otherImage()];
    const originalHashes = await Promise.all(files.map(sha256));
    const merged = await mergeAnswerSheetSources(files, { existingHashes: originalHashes, existingSourceHashes: originalHashes, allowExistingSourceFiles: true });
    expect((await PDFDocument.load(await merged.arrayBuffer())).getPageCount()).toBe(2);
    await expect(mergeAnswerSheetSources(files, { existingHashes: [...originalHashes, await sha256(merged)], existingSourceHashes: originalHashes, allowExistingSourceFiles: true })).rejects.toThrow("这套答题卡已经上传过");
  });

  it("cancels after a completed page when the source scope changes", async () => {
    let current = true;
    const progress: number[] = [];
    await expect(mergeAnswerSheetSources([imageFile(), otherImage()], { isCurrent: () => current, onProgress: count => { progress.push(count); current = false; } })).rejects.toThrow("已取消");
    expect(progress).toEqual([1]);
  });
});
