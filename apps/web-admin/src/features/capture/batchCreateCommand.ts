import type { CaptureBatch } from "../../api/capture";

export interface BatchCreateCommand {
  version: 1;
  payload: { name: string; source_type: CaptureBatch["source_type"]; scanner_device?: string; idempotency_key: string };
}

export function batchCreateCommandKey(tenant: string, actor: string, exam: string) {
  return `capture-batch-command:v1:${tenant}:${actor}:${exam}`;
}

export function loadBatchCreateCommand(storage: Pick<Storage, "getItem">, key: string): BatchCreateCommand | null {
  const raw = storage.getItem(key);
  if (!raw) return null;
  // 恢复数据损坏时应报错；静默替换可能重复执行
  // 首次响应已丢失的命令。
  const value = JSON.parse(raw) as BatchCreateCommand;
  if (value.version !== 1 || !value.payload?.idempotency_key || !value.payload.name || !value.payload.source_type) throw new Error("采集批次恢复记录无效，请先核对已有批次");
  return value;
}

export function acceptBatchCreateCommand(storage: Pick<Storage, "getItem" | "setItem">, key: string,
  draft: Omit<BatchCreateCommand["payload"], "idempotency_key">): BatchCreateCommand {
  const existing = loadBatchCreateCommand(storage, key);
  if (existing) return existing;
  const command: BatchCreateCommand = { version: 1, payload: { ...structuredClone(draft), idempotency_key: crypto.randomUUID() } };
  storage.setItem(key, JSON.stringify(command));
  return command;
}
