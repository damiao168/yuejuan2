import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";

export async function captureFileBytes(file: File) {
  if (file.arrayBuffer) return file.arrayBuffer();
  return new Promise<ArrayBuffer>((resolve, reject) => {
    const reader = new FileReader(); reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(new Error("无法读取本地答卷文件")); reader.readAsArrayBuffer(file);
  });
}

export async function previewCapturePdf(file: File, signal: AbortSignal, onPage: (image: string, pageCount: number) => void) {
  const { getDocument, GlobalWorkerOptions } = await import("pdfjs-dist");
  GlobalWorkerOptions.workerSrc = pdfWorker;
  const task = getDocument({ data: await captureFileBytes(file) });
  const abort = () => { void task.destroy().catch(() => undefined); };
  signal.addEventListener("abort", abort, { once: true });
  try {
    if (signal.aborted) throw new DOMException("预览已取消", "AbortError");
    const pdf = await task.promise;
    if (pdf.numPages > 100) throw new Error(`此文件共 ${pdf.numPages} 页，当前最多预览和调整 100 页；可按原文件页序导入。`);
    for (let number = 1; number <= pdf.numPages; number++) {
      if (signal.aborted) throw new DOMException("预览已取消", "AbortError");
      const page = await pdf.getPage(number); const base = page.getViewport({ scale: 1 });
      const viewport = page.getViewport({ scale: Math.min(1, 400 / Math.max(base.width, base.height)) });
      const canvas = document.createElement("canvas"); canvas.width = Math.max(1, Math.round(viewport.width)); canvas.height = Math.max(1, Math.round(viewport.height));
      const context = canvas.getContext("2d"); if (!context) throw new Error("浏览器暂不支持PDF页面预览，可按原页序导入。");
      await page.render({ canvas, canvasContext: context, viewport, background: "white" }).promise;
      if (signal.aborted) throw new DOMException("预览已取消", "AbortError");
      onPage(canvas.toDataURL("image/jpeg", .65), pdf.numPages);
      page.cleanup(); canvas.width = 0; canvas.height = 0;
    }
  } finally { signal.removeEventListener("abort", abort); await task.destroy().catch(() => undefined); }
}

export async function countCapturePdfPages(file: File, signal: AbortSignal) {
  const { getDocument, GlobalWorkerOptions } = await import("pdfjs-dist");
  GlobalWorkerOptions.workerSrc = pdfWorker;
  const task = getDocument({ data: await captureFileBytes(file) });
  const abort = () => { void task.destroy().catch(() => undefined); };
  signal.addEventListener("abort", abort, { once: true });
  try {
    if (signal.aborted) throw new DOMException("页数读取已取消", "AbortError");
    const document = await task.promise;
    if (!Number.isSafeInteger(document.numPages) || document.numPages < 1) throw new Error("PDF未包含可导入页面");
    return document.numPages;
  } finally { signal.removeEventListener("abort", abort); await task.destroy().catch(() => undefined); }
}

export async function reorderCapturePdf(file: File, pageOrder: readonly number[]) {
  const { PDFDocument } = await import("pdf-lib");
  const original = await PDFDocument.load(await captureFileBytes(file));
  // 每页必须恰好保留一次；重排复制原PDF页面，不用缩略图替换实际答卷内容。
  if (pageOrder.length !== original.getPageCount() || new Set(pageOrder).size !== pageOrder.length || pageOrder.some(page => !Number.isInteger(page) || page < 0 || page >= pageOrder.length)) throw new Error("页序不完整，未生成调整后的答卷");
  const output = await PDFDocument.create();
  // 相同原文件和页序应有稳定内容哈希；写入当前时间会让重复选择绕过内容去重。
  const stableDate = new Date("2000-01-01T00:00:00.000Z"); output.setCreationDate(stableDate); output.setModificationDate(stableDate);
  for (const page of await output.copyPages(original, [...pageOrder])) output.addPage(page);
  const bytes = await output.save();
  return new File([new Uint8Array(bytes).buffer], `${file.name.replace(/\.pdf$/i, "")}.页序已调整.pdf`, { type: "application/pdf", lastModified: Date.now() });
}
