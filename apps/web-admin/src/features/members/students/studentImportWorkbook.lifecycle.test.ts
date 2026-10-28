// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { readStudentImportWorkbook } from "./studentImportWorkbook";

class TestWorker {
  static all: TestWorker[] = [];
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  terminate = vi.fn();
  postMessage = vi.fn();
  constructor() { TestWorker.all.push(this); }
}
beforeEach(() => { TestWorker.all = []; vi.stubGlobal("Worker", TestWorker); });
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
const file = () => new File(["synthetic"], "合成名单.xlsx");
it("移除文件立即终止解析，迟到的结果不再兑现已取消Promise", async () => {
  const controller = new AbortController();
  const promise = readStudentImportWorkbook(file(), controller.signal);
  const result = expect(promise).rejects.toMatchObject({ name: "AbortError" });
  const worker = TestWorker.all[0];
  controller.abort(); worker.onmessage?.({ data: { sheets: [{ name: "旧名单", csv: "旧名单" }] } } as MessageEvent);
  await result; expect(worker.terminate).toHaveBeenCalled();
});
it("解析超过时限会结束Worker并允许用户重试", async () => {
  vi.useFakeTimers();
  const promise = readStudentImportWorkbook(file(), new AbortController().signal);
  const result = expect(promise).rejects.toThrow("读取超时");
  await vi.advanceTimersByTimeAsync(30000); await result;
  expect(TestWorker.all[0].terminate).toHaveBeenCalledOnce(); expect(vi.getTimerCount()).toBe(0);
});
it("postMessage同步失败也关闭Worker与计时器", async () => {
  vi.useFakeTimers();
  class BrokenWorker extends TestWorker { postMessage = vi.fn(() => { throw new Error("DataCloneError"); }); }
  vi.stubGlobal("Worker", BrokenWorker);
  await expect(readStudentImportWorkbook(file(), new AbortController().signal)).rejects.toThrow("未能启动");
  expect(TestWorker.all[0].terminate).toHaveBeenCalledOnce(); expect(vi.getTimerCount()).toBe(0);
});
it("取消状态和超过大小的文件在创建Worker前拒绝", async () => {
  const controller = new AbortController(); controller.abort();
  await expect(readStudentImportWorkbook(file(), controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  const oversized = file(); Object.defineProperty(oversized, "size", { value: 10 * 1024 * 1024 + 1 });
  await expect(readStudentImportWorkbook(oversized, new AbortController().signal)).rejects.toThrow("10MB");
  expect(TestWorker.all).toHaveLength(0);
});
