import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";

export async function renderPdfAnswerPages(url: string, signal: AbortSignal, onPage: (image: string, count: number) => void) {
  const { getDocument, GlobalWorkerOptions } = await import("pdfjs-dist");
  GlobalWorkerOptions.workerSrc = pdfWorker;
  // 只使用任务已下载的本地 Blob，不另行请求文件库或执行 PDF 脚本、表单与外部链接。
  if (!url.startsWith("blob:")) throw new Error("答卷 PDF 尚未下载到本机，请重新读取原件。");
  const task = getDocument({ url, isEvalSupported: false });
  const abort = () => { void task.destroy().catch(() => undefined); };
  signal.addEventListener("abort", abort, { once: true });
  try {
    if (signal.aborted) throw new DOMException("答卷读取已取消", "AbortError");
    const pdf = await task.promise;
    if (pdf.numPages < 1 || pdf.numPages > 50) throw new Error("此 PDF 超过本机逐页核对的 50 页范围，请联系负责人按题拆分材料。");
    let renderedPixels = 0; let renderedBytes = 0;
    for (let pageNo = 1; pageNo <= pdf.numPages; pageNo++) {
      if (signal.aborted) throw new DOMException("答卷读取已取消", "AbortError");
      const page = await pdf.getPage(pageNo); const base = page.getViewport({ scale: 1 });
      if (!Number.isFinite(base.width) || !Number.isFinite(base.height) || base.width <= 0 || base.height <= 0) throw new Error("PDF 页面尺寸无效，请恢复原件后重试。");
      const scale = Math.min(2, 4096 / Math.max(base.width, base.height), Math.sqrt(8_000_000 / (base.width * base.height)));
      const viewport = page.getViewport({ scale }); const canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.ceil(viewport.width)); canvas.height = Math.max(1, Math.ceil(viewport.height));
      renderedPixels += canvas.width * canvas.height;
      if (renderedPixels > 64_000_000) throw new Error("PDF 页面总尺寸超出本机核对范围，请联系负责人按题拆分材料。");
      const context = canvas.getContext("2d"); if (!context) throw new Error("当前查看器无法显示 PDF 页面，请联系负责人补充图片材料。");
      try {
        await page.render({ canvas, canvasContext: context, viewport, background: "white" }).promise;
        if (signal.aborted) throw new DOMException("答卷读取已取消", "AbortError");
        const image = canvas.toDataURL("image/png");
        renderedBytes += image.length;
        if (renderedBytes > 64 * 1024 * 1024) throw new Error("PDF 页面内容超出本机核对容量，请联系负责人按题拆分材料。");
        if (!image.startsWith("data:image/png")) throw new Error("PDF 页面未能生成可核对图片，请恢复原件后重试。");
        onPage(image, pdf.numPages);
      } finally { page.cleanup(); canvas.width = 0; canvas.height = 0; }
    }
  } finally { signal.removeEventListener("abort", abort); await task.destroy().catch(() => undefined); }
}
