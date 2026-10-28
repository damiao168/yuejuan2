import type { StudentImportSheet } from "./studentImportSheet";
export type { StudentImportSheet } from "./studentImportSheet";

export async function readStudentImportWorkbook(file: File, signal: AbortSignal): Promise<StudentImportSheet[]> {
  if (!/\.xlsx$/i.test(file.name)) throw new Error("请选择 .xlsx 或 CSV 文件；旧版 .xls 请另存为 .xlsx");
  if (file.size > 10 * 1024 * 1024) throw new Error("Excel名单不能超过10MB，请分批导入");
  if (signal.aborted) throw new DOMException("读取已取消", "AbortError");
  const worker = new Worker(new URL("./studentImportWorkbook.worker.ts", import.meta.url), { type: "module" });
  return new Promise((resolve, reject) => {
    const finish = () => { worker.terminate(); window.clearTimeout(timer); signal.removeEventListener("abort", abort); };
    const abort = () => { finish(); reject(new DOMException("读取已取消", "AbortError")); };
    const timer = window.setTimeout(() => { finish(); reject(new Error("Excel读取超时，请缩小文件后重试")); }, 30000);
    signal.addEventListener("abort", abort, { once: true });
    worker.onmessage = (event: MessageEvent<{ sheets?: StudentImportSheet[]; error?: string }>) => {
      finish();
      if (event.data.error || !event.data.sheets) reject(new Error(event.data.error || "Excel名单读取失败"));
      else resolve(event.data.sheets);
    };
    worker.onerror = () => { finish(); reject(new Error("Excel名单读取失败，请检查文件或另存为 .xlsx 后重试")); };
    // 解析在专用 Worker 中执行；移除文件、关闭窗口或换文件会终止旧任务，旧结果不能替换新名单。
    try { worker.postMessage(file); }
    catch { finish(); reject(new Error("Excel读取未能启动，请重新选择文件")); }
  });
}
