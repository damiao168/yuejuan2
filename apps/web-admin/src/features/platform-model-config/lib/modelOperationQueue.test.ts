import { describe, expect, it, vi } from "vitest";
import { ModelOperationQueue, modelOperationKey } from "./modelOperationQueue";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("学校模型操作队列", () => {
  it("手动与批量检测共用三个并发槽，第四项只在前一请求结束后发送", async () => {
    const queue = new ModelOperationQueue();
    const pending = Array.from({ length: 4 }, () => deferred<string>());
    let active = 0;
    let peak = 0;
    const start = vi.fn(async (index: number) => {
      active += 1;
      peak = Math.max(peak, active);
      try { return await pending[index].promise; }
      finally { active -= 1; }
    });
    const requests = pending.map((_, index) => queue.enqueue("school", `model-${index}`, index === 0 ? "text" : "quick", () => start(index), index === 0 ? undefined : "batch"));
    expect(start).toHaveBeenCalledTimes(3);
    expect(queue.getSnapshot()[modelOperationKey("school", "model-3")].phase).toBe("queued");
    pending[0].resolve("first");
    await vi.waitFor(() => expect(start).toHaveBeenCalledTimes(4));
    pending.slice(1).forEach((item, index) => item.resolve(`next-${index}`));
    expect(await Promise.all(requests)).toEqual(["first", "next-0", "next-1", "next-2"]);
    await vi.waitFor(() => expect(queue.getSnapshot()).toEqual({}));
    expect(peak).toBe(3);
  });

  it("同一学校模型的排队、检测与写入互斥，其他模型和其他学校不受影响", async () => {
    const queue = new ModelOperationQueue(1);
    const pending = deferred<string>();
    const request = queue.enqueue("school-a", "model-a", "quick", () => pending.promise);
    const duplicate = vi.fn(async () => "duplicate");
    expect(await queue.enqueue("school-a", "model-a", "structured", duplicate)).toBeUndefined();
    expect(queue.acquireWrite("school-a", "model-a")).toBeUndefined();
    expect(duplicate).not.toHaveBeenCalled();
    const releaseOtherModel = queue.acquireWrite("school-a", "model-b");
    const releaseOtherSchool = queue.acquireWrite("school-b", "model-a");
    expect(releaseOtherModel).toBeTypeOf("function");
    expect(releaseOtherSchool).toBeTypeOf("function");
    const blockedByWrite = vi.fn(async () => "must-not-send");
    expect(await queue.enqueue("school-a", "model-b", "text", blockedByWrite)).toBeUndefined();
    expect(blockedByWrite).not.toHaveBeenCalled();
    releaseOtherModel!();
    releaseOtherModel!();
    releaseOtherSchool!();
    pending.resolve("complete");
    expect(await request).toBe("complete");
    await vi.waitFor(() => expect(queue.isBusy("school-a", "model-a")).toBe(false));
  });

  it("失败释放槽位并继续排队任务，失败模型也可重试", async () => {
    const queue = new ModelOperationQueue(1);
    const pending = deferred<string>();
    const error = new Error("供应商暂不可用");
    const request = queue.enqueue("school", "failed-model", "text", () => pending.promise);
    const rejected = expect(request).rejects.toBe(error);
    const next = vi.fn(async () => "next-completed");
    const queued = queue.enqueue("school", "next-model", "quick", next);
    expect(next).not.toHaveBeenCalled();
    pending.reject(error);
    await rejected;
    expect(await queued).toBe("next-completed");
    await vi.waitFor(() => expect(queue.isBusy("school", "failed-model")).toBe(false));
    expect(await queue.enqueue("school", "failed-model", "text", async () => "retry-success")).toBe("retry-success");
  });

  it("停止某批后续任务不撤销已发请求，也不取消另一个组的任务", async () => {
    const queue = new ModelOperationQueue(1);
    const pending = deferred<string>();
    const running = queue.enqueue("school", "running", "quick", () => pending.promise, "batch-a");
    const cancelledRun = vi.fn(async () => "must-not-send");
    const cancelled = queue.enqueue("school", "cancelled", "quick", cancelledRun, "batch-a");
    const keptRun = vi.fn(async () => "manual-completed");
    const kept = queue.enqueue("school", "manual", "text", keptRun);
    expect(queue.cancelQueued("batch-a")).toBe(1);
    expect(await cancelled).toBeUndefined();
    expect(queue.isBusy("school", "running")).toBe(true);
    expect(queue.isBusy("school", "cancelled")).toBe(false);
    expect(cancelledRun).not.toHaveBeenCalled();
    expect(keptRun).not.toHaveBeenCalled();
    pending.resolve("running-completed");
    expect(await running).toBe("running-completed");
    expect(await kept).toBe("manual-completed");
    expect(cancelledRun).not.toHaveBeenCalled();
  });
});
