import { GlobalWorkerOptions, getDocument } from "pdfjs-dist";
import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";

GlobalWorkerOptions.workerSrc = pdfWorker;

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException("附件读取已取消", "AbortError");
}

/** 调用方已检查文件类型与大小；这里只提取文字，不执行模型请求或保存附件。 */
export async function readConversationPDF(file: File, maxCharacters: number, signal?: AbortSignal): Promise<string> {
  throwIfAborted(signal);
  const data = await file.arrayBuffer();
  throwIfAborted(signal);
  const loading = getDocument({ data });
  // 解析任务归属本次上传；离开对话时销毁 worker。即使销毁较晚，页结果也要再次核对取消状态。
  const cancel = () => { void loading.destroy().catch(() => undefined); };
  signal?.addEventListener("abort", cancel, { once: true });
  try {
    const document = await loading.promise;
    throwIfAborted(signal);
    const pages: string[] = [];
    let length = 0;
    for (let pageNumber = 1; pageNumber <= document.numPages; pageNumber += 1) {
      throwIfAborted(signal);
      const page = await document.getPage(pageNumber);
      const text = await page.getTextContent();
      throwIfAborted(signal);
      const pageText = text.items.map((item) => "str" in item ? item.str : "").join(" ").trim();
      length += Array.from(pageText).length;
      if (length > maxCharacters) throw new Error("文件文字过多，请拆分后上传");
      pages.push(`第 ${pageNumber} 页\n${pageText}`);
    }
    return pages.join("\n\n").trim();
  } finally {
    signal?.removeEventListener("abort", cancel);
    await loading.destroy();
  }
}
