import { captureFileBytes } from "./capturePdfPreview";
import type { IFD } from "utif";
import { captureTiffDirectories, readCaptureTiffTags, writeCaptureTiffPointer, type TiffDirectories } from "./captureTiffDirectories";
import { getUserErrorMessage } from "../../api/userError";

const compressions = new Set([1, 3, 4, 5, 6, 7, 8, 32773, 32809]);
const localReadLimit = 256 * 1024 * 1024;

function captureTiffBytes(file: File) {
  // 本地解码会保留输入缓冲区；超大原件不先整读进浏览器，改走用户明确核对后的原件处理。
  if (file.size > localReadLimit) throw new Error("TIFF超过本地256MiB读取范围，请核对原件后按原页序导入。");
  return captureFileBytes(file);
}

export function captureTiffOrientation(width: number, height: number, orientation: number) {
  const transforms: Record<number, [number, number, number, number, number, number]> = {
    1: [1, 0, 0, 1, 0, 0], 2: [-1, 0, 0, 1, width, 0],
    3: [-1, 0, 0, -1, width, height], 4: [1, 0, 0, -1, 0, height],
    5: [0, 1, 1, 0, 0, 0], 6: [0, 1, -1, 0, height, 0],
    7: [0, -1, -1, 0, height, width], 8: [0, -1, 1, 0, 0, width],
  };
  if (!transforms[orientation]) throw new Error("TIFF页面方向无法识别，原文件仍保留。");
  // 方向标签只作用于缩略图坐标；改序原件保留原标签和像素，不把浏览器旋转重编码进答卷。
  return { width: orientation >= 5 ? height : width, height: orientation >= 5 ? width : height, transform: transforms[orientation] };
}

export async function countCaptureTiffPages(file: File) {
  return captureTiffDirectories(await captureTiffBytes(file)).directories.length;
}

// 保留旧调用的格式约束；通用计数、预览与改序使用同一个classic/BigTIFF主目录解析器。
export function classicTiffDirectories(bytes: ArrayBuffer) {
  if (bytes.byteLength >= 8) {
    const view = new DataView(bytes), little = view.getUint16(0) === 0x4949;
    if (view.getUint16(2, little) === 43) throw new Error("这是BigTIFF，请使用完整TIFF主页面链读取");
  }
  const chain = captureTiffDirectories(bytes);
  return chain;
}

function readPages(bytes: ArrayBuffer, chain: TiffDirectories) {
  if (chain.directories.length > 100) throw new Error(`此文件共${chain.directories.length}页，本地最多预览和调整100页；请核对原件后按原页序导入。`);
  // UTIF的像素解码不依赖目录宽度；只把校验后的主页面标签交给它，原BigTIFF字节和64位偏移保持原样。
  const pages: IFD[] = readCaptureTiffTags(bytes, chain).map(tags => ({ ...tags, width: 0, height: 0, data: new Uint8Array() }));
  let pixels = 0;
  for (const page of pages) {
    const width = Number((page.t256 as number[] | undefined)?.[0]); const height = Number((page.t257 as number[] | undefined)?.[0]);
    const compression = Number((page.t259 as number[] | undefined)?.[0] ?? 1);
    const orientation = Number((page.t274 as number[] | undefined)?.[0] ?? 1);
    pixels += width * height;
    if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width <= 0 || height <= 0 || width * height > 32_000_000 || pixels > 128_000_000) throw new Error("TIFF 页面尺寸超出本地预览范围，请按原页序导入。");
    if (!compressions.has(compression) || !Number.isInteger(orientation) || orientation < 1 || orientation > 8 || page.t330 || Number((page.t284 as number[] | undefined)?.[0] ?? 1) !== 1) throw new Error("此TIFF编码、分通道或子页面暂不支持本地预览，请核对原件后按原页序导入。");
    const bits = Array.from((page.t258 ?? [1]) as ArrayLike<number>), samples = Number((page.t277 as number[] | undefined)?.[0] ?? 1);
    const photo = Number((page.t262 as number[] | undefined)?.[0] ?? 2), depth = bits[0];
    const supportedColor = (photo === 0 && [1, 4, 8].includes(depth) && samples === 1)
      || (photo === 1 && [1, 2, 8, 16].includes(depth) && samples === 1)
      || (photo === 2 && [8, 16].includes(depth) && [3, 4].includes(samples) && bits.length === samples)
      || (photo === 3 && depth === 8 && samples === 1 && Number((page.t320 as number[] | undefined)?.length) >= 768)
      || (photo === 5 && depth === 8 && [4, 5].includes(samples) && bits.length === samples);
    if (!supportedColor || bits.some(value => value !== depth) || (page.t339 && Array.from(page.t339 as ArrayLike<number>).some(value => value !== 1))) throw new Error("此TIFF色彩或位深暂不支持本地预览，请核对原件后按原页序导入。");
    const offsets = (page.t273 ?? page.t324) as number[] | undefined; const lengths = (page.t279 ?? page.t325) as number[] | undefined;
    if (!offsets?.length || offsets.length !== lengths?.length || offsets.some((start, index) => !Number.isSafeInteger(start) || start < 8 || !Number.isSafeInteger(lengths[index]) || lengths[index] <= 0 || start + lengths[index] > bytes.byteLength)) throw new Error("TIFF 图像数据不完整，未调整原文件。");
    // 指针字节不能同时被声明为像素；否则重连目录也会改变答卷图像。
    const pointers = [chain.first, ...chain.directories.map(directory => directory.next)];
    if (offsets.some((start, index) => pointers.some(at => start < at + chain.pointerSize && start + lengths[index] > at))) throw new Error("TIFF像素与目录指针重叠，未调整原件。");
  }
  return pages;
}

export async function inspectCaptureTiff(file: File) {
  const bytes = await captureTiffBytes(file), chain = captureTiffDirectories(bytes);
  let previewUnavailable = "";
  try { readPages(bytes, chain); }
  catch (cause) { previewUnavailable = getUserErrorMessage(cause, "TIFF暂不能本地预览"); }
  return { pageCount: chain.directories.length, previewUnavailable };
}

async function decodeTiff(file: File) {
  const bytes = await captureTiffBytes(file), chain = captureTiffDirectories(bytes);
  const pages = readPages(bytes, chain), UTIF = await import("utif");
  return { bytes, chain, pages, UTIF };
}

export async function previewCaptureTiff(file: File, signal: AbortSignal, onPage: (image: string, pageCount: number) => void) {
  const { bytes, UTIF, pages } = await decodeTiff(file);
  for (const page of pages) {
    if (signal.aborted) throw new DOMException("预览已取消", "AbortError");
    UTIF.decodeImage(bytes, page); const rgba = UTIF.toRGBA8(page);
    if (!page.data?.length || rgba.byteLength !== page.width * page.height * 4) throw new Error("TIFF 页面解码失败，原文件仍保留。");
    const source = document.createElement("canvas"); source.width = page.width; source.height = page.height;
    const context = source.getContext("2d"); if (!context) throw new Error("浏览器不支持 TIFF 页面预览。");
    const data = context.createImageData(page.width, page.height); data.data.set(rgba); context.putImageData(data, 0, 0);
    const orientation = captureTiffOrientation(page.width, page.height, Number((page.t274 as number[] | undefined)?.[0] ?? 1));
    const preview = document.createElement("canvas"); const scale = Math.min(1, 400 / Math.max(orientation.width, orientation.height));
    preview.width = Math.max(1, Math.round(orientation.width * scale)); preview.height = Math.max(1, Math.round(orientation.height * scale));
    const target = preview.getContext("2d"); if (!target) throw new Error("浏览器不支持 TIFF 页面预览。");
    target.setTransform(...orientation.transform.map(value => value * scale) as [number, number, number, number, number, number]);
    target.drawImage(source, 0, 0);
    if (!signal.aborted) onPage(preview.toDataURL("image/jpeg", .65), pages.length);
    source.width = 0; source.height = 0; preview.width = 0; preview.height = 0; page.data = new Uint8Array();
    // 页间让出事件循环，使取消或换文件能及时撤销后续解码。
    await new Promise<void>(resolve => setTimeout(resolve, 0));
  }
}

export async function reorderCaptureTiff(file: File, pageOrder: readonly number[]) {
  const { bytes, chain } = await decodeTiff(file);
  const count = chain.directories.length;
  if (pageOrder.length !== count || new Set(pageOrder).size !== count || pageOrder.some(page => !Number.isInteger(page) || page < 0 || page >= count)) throw new Error("页序不完整，未生成调整后的答卷。");
  const output = bytes.slice(0); const view = new DataView(output);
  // 只重连目录指针，所有压缩像素、标签与原始字节均保留；缩略图绝不用于生成上传原件。
  writeCaptureTiffPointer(view, chain.first, chain.directories[pageOrder[0]].offset, chain);
  pageOrder.forEach((page, index) => writeCaptureTiffPointer(view, chain.directories[page].next, index + 1 < count ? chain.directories[pageOrder[index + 1]].offset : 0, chain));
  return new File([output], `${file.name.replace(/\.tiff?$/i, "")}.页序已调整.tiff`, { type: "image/tiff", lastModified: file.lastModified });
}
