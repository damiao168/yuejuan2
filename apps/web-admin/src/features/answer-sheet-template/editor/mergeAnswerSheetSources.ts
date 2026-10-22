// 多个上传文件先合成同一来源，模板的 page_no 才能连续对应整套答题卡。
// PDF 保留原页面；图片原像素直接嵌入，JPEG 的 EXIF 方向转换为页面坐标。
import { writeAnswerSheetSourceHashes } from "./answerSheetSourceManifest";

export const ANSWER_SHEET_BATCH_LIMITS = { files: 50, pages: 100, bytes: 100 * 1024 * 1024 } as const;

export interface MergeAnswerSheetSourcesOptions {
  onProgress?: (processed: number, total: number) => void;
  isCurrent?: () => boolean;
  existingHashes?: readonly string[];
  existingSourceHashes?: readonly string[];
  allowExistingSourceFiles?: boolean;
  resolvePageOrder?: (files: readonly File[]) => Promise<readonly number[]>;
}

function fileKind(file: File): "pdf" | "png" | "jpeg" {
  const type = file.type.toLowerCase();
  if (type === "application/pdf" || (!type && /\.pdf$/i.test(file.name))) return "pdf";
  if (type === "image/png" || (!type && /\.png$/i.test(file.name))) return "png";
  if (type === "image/jpeg" || (!type && /\.jpe?g$/i.test(file.name))) return "jpeg";
  throw new Error("答题卡仅支持 PDF、PNG、JPG/JPEG 文件。");
}

function readBytes(file: File): Promise<ArrayBuffer> {
  if (file.arrayBuffer) return file.arrayBuffer();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(new Error("无法读取答题卡文件，请重新选择。"));
    reader.onabort = () => reject(new Error("答题卡读取已取消。"));
    reader.readAsArrayBuffer(file);
  });
}

async function contentHash(file: File): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error("当前浏览器无法校验答题卡是否重复，请刷新页面后重试。");
  const digest = await crypto.subtle.digest("SHA-256", await readBytes(file));
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
}

function jpegOrientation(bytes: ArrayBuffer): number {
  const data = new DataView(bytes);
  if (data.byteLength < 4 || data.getUint16(0) !== 0xffd8) return 1;
  let offset = 2;
  while (offset + 4 <= data.byteLength) {
    const marker = data.getUint16(offset); offset += 2;
    if (marker === 0xffda || marker === 0xffd9) break;
    const length = data.getUint16(offset);
    if (length < 2 || offset + length > data.byteLength) break;
    if (marker === 0xffe1 && length >= 16 && data.getUint32(offset + 2) === 0x45786966 && data.getUint16(offset + 6) === 0) {
      const tiff = offset + 8;
      const little = data.getUint16(tiff) === 0x4949;
      if ((!little && data.getUint16(tiff) !== 0x4d4d) || data.getUint16(tiff + 2, little) !== 42) return 1;
      const directory = tiff + data.getUint32(tiff + 4, little);
      if (directory < tiff + 8 || directory + 2 > offset + length) return 1;
      const entries = data.getUint16(directory, little);
      for (let index = 0; index < entries; index++) {
        const entry = directory + 2 + index * 12;
        if (entry + 12 > offset + length) break;
        if (data.getUint16(entry, little) === 0x0112 && data.getUint16(entry + 2, little) === 3 && data.getUint32(entry + 4, little) === 1) {
          const orientation = data.getUint16(entry + 8, little);
          return orientation >= 1 && orientation <= 8 ? orientation : 1;
        }
      }
    }
    offset += length;
  }
  return 1;
}

function orientationMatrix(orientation: number, width: number, height: number): [number, number, number, number, number, number] {
  switch (orientation) {
    case 2: return [-1, 0, 0, 1, width, 0];
    case 3: return [-1, 0, 0, -1, width, height];
    case 4: return [1, 0, 0, -1, 0, height];
    case 5: return [0, -1, -1, 0, height, width];
    case 6: return [0, -1, 1, 0, 0, width];
    case 7: return [0, 1, 1, 0, 0, 0];
    case 8: return [0, 1, -1, 0, height, 0];
    default: return [1, 0, 0, 1, 0, 0];
  }
}

/** 合并整批文件，任一文件失败就停止；不登记只包含部分页面的来源。 */
export async function mergeAnswerSheetSources(files: readonly File[], options: MergeAnswerSheetSourcesOptions = {}): Promise<File> {
  if (!files.length) throw new Error("请先选择答题卡文件。");
  if (files.length > ANSWER_SHEET_BATCH_LIMITS.files) throw new Error("一次最多上传 50 个答题卡文件。");
  if (files.some(file => file.size <= 0)) throw new Error("答题卡文件为空，请重新选择。");
  if (files.reduce((total, file) => total + file.size, 0) > ANSWER_SHEET_BATCH_LIMITS.bytes) throw new Error("本批答题卡总大小不能超过 100 MiB。");
  const kinds = files.map(fileKind);
  const assertCurrent = () => {
    if (options.isCurrent && !options.isCurrent()) throw new Error("答题卡上传已取消。");
  };
  assertCurrent();
  const existingHashes = new Set((options.existingHashes ?? []).filter(hash => /^[a-f\d]{64}$/i.test(hash)).map(hash => hash.toLowerCase()));
  const existingSourceHashes = new Set((options.existingSourceHashes ?? []).filter(hash => /^[a-f\d]{64}$/i.test(hash)).map(hash => hash.toLowerCase()));
  // 逐个校验原文件，整批遇到重复就停止；静默跳过会把正反面错变成单页来源。
  const selectedHashes = new Map<string, string>();
  if (files.length > 1 || existingHashes.size || existingSourceHashes.size) {
    for (const file of files) {
      const hash = await contentHash(file);
      assertCurrent();
      if (selectedHashes.has(hash)) throw new Error(`“${file.name}”与本批“${selectedHashes.get(hash)}”内容重复，请每面只选择一次。`);
      if (!options.allowExistingSourceFiles && (existingHashes.has(hash) || existingSourceHashes.has(hash))) throw new Error(`“${file.name}”已经上传过，请勿重复上传。`);
      selectedHashes.set(hash, file.name);
    }
  }
  // 单张图片沿用原文件，避免重复编码；多文件和 PDF 页数校验才加载合并库。
  if (files.length === 1 && kinds[0] !== "pdf") return files[0];
  const { PDFDocument, concatTransformationMatrix, popGraphicsState, pushGraphicsState } = await import("pdf-lib");
  assertCurrent();
  const output = files.length > 1 ? await PDFDocument.create() : undefined;
  if (output) {
    // 合成文件不能包含本次上传时间；相同正反面再次上传才有稳定的整套内容哈希。
    const stableDate = new Date("2000-01-01T00:00:00.000Z");
    output.setCreationDate(stableDate);
    output.setModificationDate(stableDate);
  }
  let pageCount = 0;
  for (let index = 0; index < files.length; index++) {
    assertCurrent();
    const file = files[index];
    const bytes = await readBytes(file);
    assertCurrent();
    try {
      if (kinds[index] === "pdf") {
        const source = await PDFDocument.load(bytes);
        pageCount += source.getPageCount();
        if (!source.getPageCount()) throw new Error("PDF 中没有页面。");
        if (pageCount > ANSWER_SHEET_BATCH_LIMITS.pages) throw new Error("本批答题卡最多支持 100 页。");
        if (output) {
          const pages = await output.copyPages(source, source.getPageIndices());
          for (const page of pages) output.addPage(page);
        }
      } else if (output) {
        pageCount++;
        if (pageCount > ANSWER_SHEET_BATCH_LIMITS.pages) throw new Error("本批答题卡最多支持 100 页。");
        const image = kinds[index] === "png" ? await output.embedPng(bytes) : await output.embedJpg(bytes);
        const orientation = kinds[index] === "jpeg" ? jpegOrientation(bytes) : 1;
        const swapped = orientation >= 5;
        const page = output.addPage(swapped ? [image.height, image.width] : [image.width, image.height]);
        page.pushOperators(pushGraphicsState(), concatTransformationMatrix(...orientationMatrix(orientation, image.width, image.height)));
        page.drawImage(image, { x: 0, y: 0, width: image.width, height: image.height });
        page.pushOperators(popGraphicsState());
      }
    } catch (error) {
      if (error instanceof Error && /100 页|没有页面/.test(error.message)) throw error;
      throw new Error(`第 ${index + 1} 个答题卡文件无法读取，请检查文件是否损坏或 PDF 是否加密。`);
    }
    assertCurrent();
    options.onProgress?.(index + 1, files.length);
  }
  if (!output) return files[0];
  if (existingHashes.size) {
    // 加清单前的序列化仍与旧版合成规则一致，避免已上传的旧双面 PDF 因新增清单而漏报重复。
    const legacyBytes = await output.save();
    assertCurrent();
    const legacyHash = await contentHash(new File([new Uint8Array(legacyBytes)], "legacy-answer-sheet.pdf", { type: "application/pdf" }));
    assertCurrent();
    if (existingHashes.has(legacyHash)) throw new Error("这套答题卡已经上传过，请直接选择已有答题卡文件。");
  }
  if (options.resolvePageOrder) {
    // 去重和完整解码均先于模型请求，避免重复文件付费；返回的是原始平铺页索引，支持跨 PDF 重排。
    const order = await options.resolvePageOrder(files);
    assertCurrent();
    const original = output.getPages();
    if (order.length !== original.length || new Set(order).size !== original.length
      || order.some(index => !Number.isInteger(index) || index < 0 || index >= original.length))
      throw new Error("答题卡自动页序不完整，请上传按正确页序排列的单个 PDF。");
    for (let index = original.length - 1; index >= 0; index--) output.removePage(index);
    for (const index of order) output.addPage(original[index]);
  }
  await writeAnswerSheetSourceHashes(output, [...selectedHashes.keys()]);
  assertCurrent();
  const bytes = await output.save();
  assertCurrent();
  if (bytes.byteLength > ANSWER_SHEET_BATCH_LIMITS.bytes) throw new Error("合并后的答题卡超过 100 MiB，请减少文件后重试。");
  const merged = new File([new Uint8Array(bytes)], `${files[0].name.replace(/\.[^.]+$/, "")}-等${files.length}份答题卡.pdf`, { type: "application/pdf" });
  if (existingHashes.size) {
    const hash = await contentHash(merged);
    assertCurrent();
    if (existingHashes.has(hash)) throw new Error("这套答题卡已经上传过，请直接选择已有答题卡文件。");
  }
  return merged;
}
