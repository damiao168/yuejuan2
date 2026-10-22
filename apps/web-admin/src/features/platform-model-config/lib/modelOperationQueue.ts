import type { ManagedAPIProbeMode } from "../../../api/modelApiConfig";

export type ModelOperation = Readonly<{
  tenantID: string;
  modelID: string;
  mode: ManagedAPIProbeMode | "write";
  phase: "queued" | "running";
}>;

type QueuedProbe = {
  key: string;
  group?: string;
  operation: ModelOperation;
  run: () => Promise<void>;
  cancel: () => void;
};

export function modelOperationKey(tenantID: string, modelID: string) {
  return `${tenantID}:${modelID}`;
}

// 同一配置的检测和写入互斥；不同模型共用检测并发上限，排队本身不发送供应商请求。
export class ModelOperationQueue {
  private snapshot: Readonly<Record<string, ModelOperation>> = {};
  private listeners = new Set<() => void>();
  private pending: QueuedProbe[] = [];
  private running = 0;

  constructor(private readonly concurrency = 3) {
    if (!Number.isInteger(concurrency) || concurrency < 1) throw new Error("Invalid model probe concurrency");
  }

  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  isBusy(tenantID: string, modelID: string) {
    return Boolean(this.snapshot[modelOperationKey(tenantID, modelID)]);
  }

  acquireWrite(tenantID: string, modelID: string): (() => void) | undefined {
    const key = modelOperationKey(tenantID, modelID);
    if (this.snapshot[key]) return undefined;
    const operation: ModelOperation = { tenantID, modelID, mode: "write", phase: "running" };
    this.update(key, operation);
    return () => { if (this.snapshot[key] === operation) this.update(key); };
  }

  enqueue<T>(tenantID: string, modelID: string, mode: ManagedAPIProbeMode, run: () => Promise<T>, group?: string): Promise<T | undefined> {
    const key = modelOperationKey(tenantID, modelID);
    if (this.snapshot[key]) return Promise.resolve(undefined);
    const operation: ModelOperation = { tenantID, modelID, mode, phase: "queued" };
    this.update(key, operation);
    return new Promise((resolve, reject) => {
      this.pending.push({
        key, group, operation,
        run: async () => {
          try { resolve(await run()); } catch (error) { reject(error); }
        },
        cancel: () => resolve(undefined),
      });
      this.pump();
    });
  }

  // 只取消尚未开始的任务；已经发出的生成请求仍可能产生用量，不能显示为已撤销。
  cancelQueued(group?: string) {
    const cancelled = this.pending.filter((job) => group === undefined || job.group === group);
    this.pending = this.pending.filter((job) => !cancelled.includes(job));
    for (const job of cancelled) {
      this.update(job.key);
      job.cancel();
    }
    return cancelled.length;
  }

  private update(key: string, operation?: ModelOperation) {
    const next = { ...this.snapshot };
    if (operation) next[key] = operation;
    else delete next[key];
    this.snapshot = next;
    for (const listener of this.listeners) listener();
  }

  private pump() {
    while (this.running < this.concurrency && this.pending.length) {
      const job = this.pending.shift()!;
      this.running += 1;
      this.update(job.key, { ...job.operation, phase: "running" });
      void job.run().finally(() => {
        this.running -= 1;
        this.update(job.key);
        this.pump();
      });
    }
  }
}
