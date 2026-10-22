import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { orderAnswerSheetPages, type AnswerSheetPageOrderImage } from "../../../api/answerSheetPageOrder";

export const ANSWER_SHEET_ORDER_LIMITS = { pages: 100, longEdge: 1000, imageBytes: 128 * 1024, totalBytes: 20 * 1024 * 1024 } as const;

async function readFile(file: File) {
  if (file.arrayBuffer) return file.arrayBuffer();
  return new Promise<ArrayBuffer>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(new Error("无法读取答题卡文件，请重新选择。"));
    reader.readAsArrayBuffer(file);
  });
}

/** 用页码、题号及版面衔接排列页面；单个 PDF 的原页序由上传流程保留。 */
export async function orderAnswerSheetSourcePages(files: readonly File[], input: {
  examId: string; signal: AbortSignal; isCurrent: () => boolean;
  onProgress?: (processed: number, total: number) => void;
}): Promise<number[]> {
  const assertCurrent = () => {
    if (input.signal.aborted || !input.isCurrent()) throw new DOMException("答题卡页序识别已取消", "AbortError");
  };
  const pages: AnswerSheetPageOrderImage[] = [];
  let totalBytes = 0;
  const append = async (canvas: HTMLCanvasElement) => {
    assertCurrent();
    if (pages.length >= ANSWER_SHEET_ORDER_LIMITS.pages) throw new Error("本批答题卡最多支持 100 页。");
    let data = "";
    for (const quality of [.82, .68, .52, .38, .25]) {
      const encoded = canvas.toDataURL("image/jpeg", quality);
      if (!encoded.startsWith("data:image/jpeg;base64,")) throw new Error("浏览器无法准备答题卡页序图像，请改为上传整套 PDF。");
      data = encoded.split(",")[1] ?? "";
      if (data.length * .75 <= ANSWER_SHEET_ORDER_LIMITS.imageBytes) break;
    }
    if (!data || data.length * .75 > ANSWER_SHEET_ORDER_LIMITS.imageBytes) throw new Error("答题卡页序图像超过大小限制，请上传按正确页序排列的整套 PDF。");
    const bytes = Uint8Array.from(atob(data), character => character.charCodeAt(0));
    totalBytes += data.length;
    if (totalBytes > ANSWER_SHEET_ORDER_LIMITS.totalBytes - 65536) throw new Error("答题卡页序请求过大，请上传按正确页序排列的整套 PDF。");
    const hash = await crypto.subtle.digest("SHA-256", bytes);
    assertCurrent();
    pages.push({ page_id: `page-${pages.length + 1}`, width: canvas.width, height: canvas.height, media_type: "image/jpeg",
      data_base64: data, sha256: Array.from(new Uint8Array(hash), value => value.toString(16).padStart(2, "0")).join("") });
  };
  for (let index = 0; index < files.length; index++) {
    assertCurrent();
    const file = files[index];
    const pdf = file.type.toLowerCase() === "application/pdf" || (!file.type && /\.pdf$/i.test(file.name));
    if (pdf) {
      const { getDocument, GlobalWorkerOptions } = await import("pdfjs-dist");
      GlobalWorkerOptions.workerSrc = pdfWorker;
      const bytes = await readFile(file);
      assertCurrent();
      const task = getDocument({ data: bytes });
      const abort = () => { void task.destroy().catch(() => undefined); };
      input.signal.addEventListener("abort", abort, { once: true });
      try {
        const document = await task.promise;
        assertCurrent();
        if (pages.length + document.numPages > ANSWER_SHEET_ORDER_LIMITS.pages) throw new Error("本批答题卡最多支持 100 页。");
        for (let number = 1; number <= document.numPages; number++) {
          const page = await document.getPage(number); assertCurrent();
          const base = page.getViewport({ scale: 1 });
          const viewport = page.getViewport({ scale: Math.min(1, ANSWER_SHEET_ORDER_LIMITS.longEdge / Math.max(base.width, base.height)) });
          const canvas = window.document.createElement("canvas");
          canvas.width = Math.max(1, Math.round(viewport.width)); canvas.height = Math.max(1, Math.round(viewport.height));
          const context = canvas.getContext("2d");
          if (!context) throw new Error("浏览器无法读取答题卡页序图像。");
          await page.render({ canvas, canvasContext: context, viewport, background: "white" }).promise;
          await append(canvas);
        }
      } finally {
        input.signal.removeEventListener("abort", abort);
        await task.destroy().catch(() => undefined);
      }
    } else {
      const url = URL.createObjectURL(file);
      try {
        const image = await new Promise<HTMLImageElement>((resolve, reject) => {
          const node = new Image();
          const abort = () => { node.src = ""; reject(new DOMException("答题卡页序识别已取消", "AbortError")); };
          const release = () => input.signal.removeEventListener("abort", abort);
          node.onload = () => { release(); resolve(node); };
          node.onerror = () => { release(); reject(new Error("答题卡图片无法读取，请改为上传整套 PDF。")); };
          input.signal.addEventListener("abort", abort, { once: true }); node.src = url;
        });
        assertCurrent();
        const scale = Math.min(1, ANSWER_SHEET_ORDER_LIMITS.longEdge / Math.max(image.naturalWidth, image.naturalHeight));
        const canvas = document.createElement("canvas");
        canvas.width = Math.max(1, Math.round(image.naturalWidth * scale)); canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
        const context = canvas.getContext("2d");
        if (!context) throw new Error("浏览器无法读取答题卡页序图像。");
        context.fillStyle = "white"; context.fillRect(0, 0, canvas.width, canvas.height);
        context.drawImage(image, 0, 0, canvas.width, canvas.height);
        await append(canvas);
      } finally { URL.revokeObjectURL(url); }
    }
    input.onProgress?.(index + 1, files.length);
  }
  assertCurrent();
  const result = await orderAnswerSheetPages(input.examId, pages, input.signal);
  assertCurrent();
  if (result.status === "uncertain") throw new Error(pageOrderFailureMessage(result.reason_codes));
  // 必须一一覆盖真实页 ID；缺页、重复页或模型不确定均停止，不能退回上传顺序冒充识别成功。
  const order = result.page_order?.map(id => pages.findIndex(page => page.page_id === id));
  if (result.status !== "ordered" || !order || order.length !== pages.length || new Set(order).size !== pages.length || order.some(index => index < 0))
    throw new Error("模型返回的页序缺页、重复或引用了未知页面，本批未上传，请重试自动识别。");
  return order;
}

function pageOrderFailureMessage(codes: readonly string[] = []): string {
  // 只展示受控原因，不将供应商响应或学生填写内容直接拼入错误提示。
  const messages: Record<string, string> = {
    PAGE_IMAGE_DUPLICATED: "这批文件包含内容相同的页面，请去掉重复页后重新上传。",
    MULTIPLE_DOCUMENTS_SUSPECTED: "这批图片疑似包含不同答题卡，请仅选择同一套答题卡后上传。",
    MODEL_AUTH_FAILED: "答题卡模型的访问凭据不可用，请管理员检查模型配置后重试。",
    MODEL_RATE_LIMITED: "答题卡模型额度不足或请求过于频繁，请稍后重试。",
    MODEL_TIMEOUT: "答题卡模型响应超时，请重试自动识别。",
    PAGE_ORDER_TIME_BUDGET_EXCEEDED: "答题卡页序分析超时，请减少本批文件后重试。",
    MODEL_OUTPUT_INVALID: "答题卡模型返回的页序格式不完整，请重试自动识别。",
    MODEL_REQUEST_REJECTED: "模型接口拒绝了页序图片请求，请管理员检查答题卡模型配置。",
    MODEL_ANALYSIS_FAILED: "答题卡模型本次调用未完成，请重试自动识别。",
    SEMANTIC_MODEL_NOT_CONFIGURED: "尚未配置可用的答题卡多模态模型，请管理员在模型管理中分配。",
    DUPLICATE_PRINTED_PAGE_NUMBER: "不同页面识别出了重复页码，请检查是否混入了另一套答题卡后重试。",
    PRINTED_TOTAL_PAGE_COUNT_CONFLICT: "识别到的页码或总页数相互矛盾，请检查页面是否属于同一套答题卡后重试。",
    PAGE_ORDER_EVIDENCE_CONFLICT: "页面上的页码与题号顺序存在冲突，请检查是否混入其他答题卡后重试。",
    PAGE_ORDER_EVIDENCE_INSUFFICIENT: "已分析图片，但页码、题号和跨页内容仍不足以确定顺序。请换用更清晰的图片，或上传页序正确的单个 PDF。"
  };
  return codes.map(code => messages[code]).find(Boolean)
    ?? "本次页序分析没有给出明确顺序，请重试或上传页序正确的单个 PDF；本批文件尚未上传。";
}
