import type { PDFDocumentProxy } from "pdfjs-dist";
import type { Page, PSM, WorkerParams } from "tesseract.js";
import workerURL from "tesseract.js/dist/worker.min.js?url";
import coreURL from "tesseract.js-core/tesseract-core-lstm.wasm.js?url";
import trainedDataURL from "@tesseract.js-data/eng/4.0.0_best_int/eng.traineddata.gz?url";
import type { SheetFrame, SheetWord } from "./answerSheetRegionMatching";
import { createLocalAnswerSheetOCRWorker } from "./localAnswerSheetOCRWorker";

type PDFPage = Awaited<ReturnType<PDFDocumentProxy["getPage"]>>;

/** 按原页比例生成有界图像；识别坐标保持0～1，独立于编辑器当前翻到哪一页。 */
export async function renderAnswerSheetPage(page: PDFPage | undefined, imageURL?: string) {
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d", { willReadFrequently: true });
  if (!context) throw new Error("浏览器无法读取答题卡图像");
  if (page) {
    const base = page.getViewport({ scale: 1 });
    const viewport = page.getViewport({ scale: Math.min(2.5, 1800 / Math.max(base.width, base.height)) });
    canvas.width = Math.ceil(viewport.width); canvas.height = Math.ceil(viewport.height);
    await page.render({ canvas, canvasContext: context, viewport }).promise;
  } else {
    if (!imageURL) throw new Error("答题卡图片尚未加载完成");
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const node = new Image();
      node.onload = () => resolve(node);
      node.onerror = () => reject(new Error("答题卡图片无法读取，请重新上传"));
      node.src = imageURL;
    });
    const scale = Math.min(1, 1800 / Math.max(image.naturalWidth, image.naturalHeight));
    canvas.width = Math.max(1, Math.round(image.naturalWidth * scale));
    canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
    context.fillStyle = "white"; context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
  }
  return canvas;
}

const PAGE_PARAMETERS: Partial<WorkerParams> = {
  tessedit_pageseg_mode: "11" as PSM,
  tessedit_char_whitelist: "0123456789().、ABCDEFGH[]",
  user_defined_dpi: "200",
};
const HEADER_PARAMETERS: Partial<WorkerParams> = {
  tessedit_pageseg_mode: "7" as PSM,
  tessedit_char_whitelist: "",
};
const normalizedToken = (value: string) => value.normalize("NFKC").replace(/\s+/g, "");
const explicitNumber = (text: string) => /^(?:\((\d+)\)|(\d+)[.、:])$/.exec(normalizedToken(text));

function normalizeWords(page: Page, width: number, height: number, offsetX = 0, offsetY = 0, prefix = ""): SheetWord[] {
  return (page.blocks ?? []).flatMap((block, blockIndex) => block.paragraphs.flatMap((paragraph, paragraphIndex) =>
    paragraph.lines.flatMap((line, lineIndex) => line.words.map(word => ({ text: word.text, confidence: word.confidence,
      x: (word.bbox.x0 + offsetX) / width, y: (word.bbox.y0 + offsetY) / height,
      width: (word.bbox.x1 - word.bbox.x0) / width, height: (word.bbox.y1 - word.bbox.y0) / height,
      source: "ocr" as const, lineId: `${prefix}${blockIndex}:${paragraphIndex}:${lineIndex}` })))));
}

function headerRetries(frames: SheetFrame[], words: SheetWord[]) {
  return frames.flatMap((frame, index) => {
    if (![frame.x, frame.y, frame.width, frame.height].every(Number.isFinite)
      || frame.x < 0 || frame.y < 0 || frame.width <= 0 || frame.height <= 0
      || frame.x + frame.width > 1 || frame.y + frame.height > 1) return [];
    const header = words.filter(word => word.confidence >= 55 && word.width > 0 && word.height > 0
      && word.x >= frame.x - .006 && word.x + word.width <= frame.x + frame.width + .006
      && word.x < frame.x + frame.width * .38 && word.y >= frame.y - .012
      && word.y + word.height <= frame.y + frame.height + .006
      && word.y < frame.y + Math.min(.055, frame.height * .3));
    const first = [...header].sort((a, b) => a.y - b.y || a.x - b.x)[0];
    if (!first) return [];
    const firstLine = header.filter(word => Math.abs(word.y - first.y)
      < Math.max(.006, Math.min(word.height, first.height) * .7));
    const explicit = new Set(firstLine.flatMap(word => {
      const marker = explicitNumber(word.text);
      return marker ? [marker[1] ?? marker[2]] : [];
    }));
    const bare = firstLine.filter(word => /^\d+$/.test(normalizedToken(word.text)) && !explicit.has(normalizedToken(word.text)));
    return bare.length ? [{ frame, index, bare }] : [];
  });
}

function cropHeader(canvas: HTMLCanvasElement, frame: SheetFrame, bare: SheetWord[]) {
  const first = [...bare].sort((a, b) => a.y - b.y || a.x - b.x)[0];
  const lineHeight = first.height * canvas.height;
  const left = Math.max(0, Math.floor(frame.x * canvas.width - 2));
  const top = Math.max(0, Math.floor(first.y * canvas.height - Math.max(5, lineHeight * .6)));
  const right = Math.min(canvas.width, Math.ceil((frame.x + frame.width * .45) * canvas.width));
  const bottom = Math.min(canvas.height, Math.ceil(Math.min((frame.y + frame.height) * canvas.height,
    (first.y + first.height) * canvas.height + Math.max(5, lineHeight * .5))));
  if (right <= left || bottom <= top) return;
  const cropped = document.createElement("canvas");
  cropped.width = right - left; cropped.height = bottom - top;
  const context = cropped.getContext("2d");
  if (!context) throw new Error("浏览器无法读取答题卡题号图像");
  context.drawImage(canvas, left, top, cropped.width, cropped.height, 0, 0, cropped.width, cropped.height);
  return { canvas: cropped, left, top };
}

/** OCR资源随应用打包并在本地Worker执行，学生图像不发给CDN或外部模型。 */
export async function createAnswerSheetOCR(signal?: AbortSignal) {
  const languageURL = new URL(trainedDataURL, window.location.href);
  const worker = await createLocalAnswerSheetOCRWorker({
    workerPath: workerURL, corePath: coreURL,
    langPath: new URL(".", languageURL).href, signal,
  });
  let terminated = false;
  let queue = Promise.resolve();
  const assertActive = () => {
    if (terminated || signal?.aborted) throw new DOMException("已取消识别", "AbortError");
  };
  const terminate = async () => {
    if (terminated) return;
    terminated = true;
    await worker.terminate();
  };
  try {
    assertActive();
    await worker.setParameters(PAGE_PARAMETERS);
  } catch (error) {
    await terminate();
    throw error;
  }
  return {
    recognize(canvas: HTMLCanvasElement, frames: SheetFrame[] = []): Promise<SheetWord[]> {
      // 二扫会临时修改同一Worker参数；把完整页面操作串行，不能让另一页插入PSM7窗口。
      const result = queue.then(async () => {
        assertActive();
        const words = normalizeWords((await worker.recognize(canvas)).data, canvas.width, canvas.height);
        assertActive();
        const retries = headerRetries(frames, words);
        if (!retries.length) return words;
        let failed = false;
        try {
          await worker.setParameters(HEADER_PARAMETERS);
          for (const retry of retries) {
            assertActive();
            const crop = cropHeader(canvas, retry.frame, retry.bare);
            if (!crop) continue;
            try {
              const page = (await worker.recognize(crop.canvas)).data;
              assertActive();
              const numbers = new Set(retry.bare.map(word => normalizedToken(word.text)));
              // 整页白名单会丢掉题号标点；只重看框顶部首行，必须真实读回相同数字的标点形式才追加。
              const verified = normalizeWords(page, canvas.width, canvas.height, crop.left, crop.top, `header:${retry.index}:`)
                .filter(word => {
                  const marker = explicitNumber(word.text);
                  return word.confidence >= 55 && marker && numbers.has(marker[1] ?? marker[2])
                    && retry.bare.some(bare => normalizedToken(bare.text) === (marker[1] ?? marker[2])
                      && Math.abs(word.x - bare.x) <= Math.max(.012, bare.width)
                      && Math.abs(word.y - bare.y) <= Math.max(.006, bare.height));
                });
              words.push(...verified);
            } finally {
              crop.canvas.width = 0; crop.canvas.height = 0;
            }
          }
        } catch (error) {
          failed = true;
          throw error;
        } finally {
          if (!terminated && !signal?.aborted) {
            try { await worker.setParameters(PAGE_PARAMETERS); }
            catch (restoreError) {
              // 无法恢复参数后不再复用Worker；保留原识别错误，避免恢复失败覆盖最初原因。
              await terminate();
              if (!failed) throw restoreError;
            }
          }
        }
        return words;
      });
      queue = result.then(() => undefined, () => undefined);
      return result;
    },
    terminate,
  };
}
