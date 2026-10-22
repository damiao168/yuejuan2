import type { PDFDocumentProxy } from "pdfjs-dist";

const MAX_GRID_EDGE = 2300;
const MAX_GRID_PIXELS = 4_000_000;
const MAX_GRID_IMAGE_BYTES = 1024 * 1024;

function renderSize(width: number, height: number, upscale: boolean) {
  if (![width, height].every(value => Number.isFinite(value) && value > 0)) throw new Error("当前页尺寸无效，请重新上传");
  const scale = Math.min(upscale ? Infinity : 1, MAX_GRID_EDGE / Math.max(width, height), Math.sqrt(MAX_GRID_PIXELS / width / height));
  // 向下取整，避免方形页面的两边进位后越过 4MP；坐标仍相对于本次完整显示页。
  return { scale, width: Math.max(1, Math.floor(width * scale)), height: Math.max(1, Math.floor(height * scale)) };
}

async function renderGridPage(pdf: PDFDocumentProxy | undefined, pageNo: number, imageURL?: string) {
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d", { willReadFrequently: true });
  if (!context) throw new Error("浏览器无法读取答题卡图像");
  if (pdf) {
    const page = await pdf.getPage(pageNo);
    const base = page.getViewport({ scale: 1 });
    const size = renderSize(base.width, base.height, true);
    canvas.width = size.width; canvas.height = size.height;
    await page.render({ canvas, canvasContext: context, viewport: page.getViewport({ scale: size.scale }), background: "white" }).promise;
  } else {
    if (!imageURL) throw new Error("答题卡图片尚未加载完成");
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const node = new Image();
      node.onload = () => resolve(node);
      node.onerror = () => reject(new Error("答题卡图片无法读取，请重新上传"));
      node.src = imageURL;
    });
    const size = renderSize(image.naturalWidth, image.naturalHeight, false);
    canvas.width = size.width; canvas.height = size.height;
    context.fillStyle = "white"; context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
  }
  return canvas;
}

/** 图像哈希对应本次发送的 PNG / JPEG，原 PDF 哈希由调用方另行提交，不能相互替代。 */
export async function objectiveGridPageImage(pdf: PDFDocumentProxy | undefined, pageNo: number, imageURL?: string) {
  // 网格需要保留密集的小填涂格；不复用较小的 OCR / 多模态输入，避免影响其他识别链路。
  const canvas = await renderGridPage(pdf, pageNo, imageURL);
  let data = "";
  let bytes = new Uint8Array();
  let mediaType: "image/png" | "image/jpeg" = "image/png";
  const encode = (mime: "image/png" | "image/jpeg", quality?: number) => {
    const dataURL = canvas.toDataURL(mime, quality);
    if (!dataURL.startsWith(`data:${mime};base64,`)) throw new Error("浏览器无法压缩当前页图像");
    data = dataURL.split(",")[1] ?? "";
    bytes = Uint8Array.from(atob(data), value => value.charCodeAt(0));
  };
  // 扫描答题卡的无损 PNG 往往很小；先保留细括号边缘，超过请求上限才牺牲 JPEG 质量。
  encode("image/png");
  if (bytes.byteLength > MAX_GRID_IMAGE_BYTES) {
    mediaType = "image/jpeg";
    for (const quality of [.85, .7, .55, .4]) {
      encode("image/jpeg", quality);
      if (bytes.byteLength <= MAX_GRID_IMAGE_BYTES) break;
    }
  }
  if (!bytes.byteLength || bytes.byteLength > MAX_GRID_IMAGE_BYTES) throw new Error("当前页图像过大，请压缩单页文件后重试");
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const sha256 = [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join("");
  return { canvas, width: canvas.width, height: canvas.height, data_base64: data, sha256, media_type: mediaType };
}
