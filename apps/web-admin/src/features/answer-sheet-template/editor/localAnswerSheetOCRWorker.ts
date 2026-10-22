import type { Page, RecognizeOptions, WorkerParams } from "tesseract.js";

type WorkerOptions = { workerPath: string; corePath: string; langPath: string; signal?: AbortSignal };
type Action = "load" | "loadLanguage" | "initialize" | "setParameters" | "recognize" | "encode";
type PendingOperation = { action: Action; resolve: (data: unknown) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> };
type WorkerReply = { workerId?: string; jobId?: string; action?: string; status?: string; data?: unknown };

const OPERATION_TIMEOUT_MS = 60_000;
let workerSequence = 0;
const aborted = () => new DOMException("已取消答题卡识别", "AbortError");

function failure(reason: unknown) {
  if (reason instanceof Error || reason instanceof DOMException) return reason;
  // 依赖错误可能包含整段模型数据，限制展示长度，避免错误信息再次拖垮页面。
  return new Error(`答题卡识别失败：${String(reason ?? "未知错误").slice(0, 300)}`);
}

function localURL(path: string, base: URL) {
  const url = new URL(path, base);
  if (url.origin !== base.origin || !["http:", "https:"].includes(url.protocol)) {
    throw new Error("答题卡识别资源必须由当前站点提供");
  }
  return url;
}

/** 持有Tesseract 7原生Worker，绕过其启动错误可能悬空的createWorker封装。
 * 协议依据依赖中的worker-script；升级Tesseract时需同时验证真实浏览器启动、失败和取消路径。
 */
export async function createLocalAnswerSheetOCRWorker(options: WorkerOptions) {
  if (options.signal?.aborted) throw aborted();
  const base = new URL(window.location.href);
  const workerURL = localURL(options.workerPath, base);
  const coreURL = localURL(options.corePath, base);
  const languageURL = localURL(options.langPath, base);
  languageURL.pathname = `${languageURL.pathname.replace(/\/$/, "")}/`;
  if (!coreURL.pathname.endsWith(".js")) throw new Error("答题卡识别核心资源地址不完整");

  const worker = new Worker(workerURL.href, { type: "classic", name: "answer-sheet-ocr" });
  const workerId = `answer-sheet-ocr-${++workerSequence}`;
  const pending = new Map<string, PendingOperation>();
  let sequence = 0;
  let stopped: Error | undefined;
  let queue = Promise.resolve();

  function stop(reason: unknown) {
    if (stopped) return;
    stopped = failure(reason);
    options.signal?.removeEventListener("abort", onAbort);
    worker.removeEventListener("message", onMessage);
    worker.removeEventListener("error", onError);
    worker.removeEventListener("messageerror", onMessageError);
    // 原生terminate没有回包；由持有者主动结算启动、编码和识别等待。
    try { worker.terminate(); } catch { /* 终止失败也必须结算等待，保留触发关闭的原始错误。 */ } finally {
      for (const operation of pending.values()) {
        clearTimeout(operation.timer);
        operation.reject(stopped);
      }
      pending.clear();
    }
  }

  function onAbort() { stop(aborted()); }
  function onError(event: ErrorEvent) {
    event.preventDefault();
    stop(event.error ?? event.message ?? "识别进程异常退出");
  }
  function onMessageError() { stop(new Error("答题卡识别结果无法读取，请重试")); }
  function onMessage(event: MessageEvent<WorkerReply>) {
    const packet = event.data;
    if (!packet || packet.workerId !== workerId || typeof packet.jobId !== "string") return;
    const operation = pending.get(packet.jobId);
    if (!operation || packet.action !== operation.action || packet.status === "progress") return;
    if (packet.status === "reject") { stop(packet.data); return; }
    if (packet.status !== "resolve") return;
    clearTimeout(operation.timer);
    pending.delete(packet.jobId);
    operation.resolve(packet.data);
  }

  worker.addEventListener("message", onMessage);
  worker.addEventListener("error", onError);
  worker.addEventListener("messageerror", onMessageError);
  options.signal?.addEventListener("abort", onAbort, { once: true });
  // AbortSignal可能在Worker构造时已取消；挂上监听后再次检查，不漏掉此窗口。
  if (options.signal?.aborted) onAbort();

  function operation<T>(action: Action, start: (jobId: string, complete: (data: T) => void) => void): Promise<T> {
    if (stopped) return Promise.reject(stopped);
    return new Promise<T>((resolve, reject) => {
      const jobId = `${workerId}-${++sequence}`;
      const complete = (data: T) => {
        const item = pending.get(jobId);
        if (!item) return;
        clearTimeout(item.timer);
        pending.delete(jobId);
        resolve(data);
      };
      const timer = setTimeout(() => stop(new Error("答题卡识别处理超时，请重试")), OPERATION_TIMEOUT_MS);
      pending.set(jobId, { action, resolve: (data) => resolve(data as T), reject, timer });
      try { start(jobId, complete); } catch (error) { stop(error); }
    });
  }

  function request<T>(action: Exclude<Action, "encode">, payload: unknown) {
    return operation<T>(action, jobId => worker.postMessage({ workerId, jobId, action, payload }));
  }

  function enqueue<T>(task: () => Promise<T>) {
    // Tesseract在同一Worker中共享api状态，参数修改与识别必须按调用顺序完成。
    const result = queue.then(() => {
      if (stopped) throw stopped;
      return task();
    });
    queue = result.then(() => undefined, () => undefined);
    return result;
  }

  function encode(canvas: HTMLCanvasElement) {
    return operation<Uint8Array>("encode", (_, complete) => {
      if (canvas.width <= 0 || canvas.height <= 0) throw new Error("答题卡图像尺寸无效");
      canvas.toBlob(blob => {
        if (stopped) return;
        if (!blob) { stop(new Error("答题卡图像无法编码，请重新上传")); return; }
        try {
          blob.arrayBuffer().then(buffer => complete(new Uint8Array(buffer)), stop);
        } catch (error) { stop(error); }
      }, "image/png");
    });
  }

  try {
    // corePath是明确的本地JS文件，langPath是本地目录；不允许依赖选择默认CDN。
    await request("load", { options: { lstmOnly: true, corePath: coreURL.href, logging: false } });
    await request("loadLanguage", { langs: "eng", options: {
      langPath: languageURL.href, cacheMethod: "none", gzip: true, lstmOnly: true,
    } });
    await request("initialize", { langs: "eng", oem: 1, config: {} });
  } catch (error) {
    stop(error);
    throw error;
  }

  return {
    setParameters(params: Partial<WorkerParams>) {
      return enqueue(async () => { await request("setParameters", { params }); });
    },
    recognize(canvas: HTMLCanvasElement, recognizeOptions: Partial<RecognizeOptions> = {}) {
      return enqueue(async (): Promise<{ data: Page }> => {
        const image = await encode(canvas);
        const data = await request<Page>("recognize", {
          image, options: recognizeOptions, output: { blocks: true, text: false },
        });
        return { data };
      });
    },
    async terminate() { stop(aborted()); },
  };
}
