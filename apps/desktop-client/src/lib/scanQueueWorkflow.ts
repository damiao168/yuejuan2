import type { QueueStatus, SyncQueueItem } from "../types";

const transitions: Record<QueueStatus, ReadonlySet<QueueStatus>> = {
  // 成功是终态；冲突必须先回到待处理或失败，不能绕过人工处理直接上传。
  pending: new Set(["pending", "uploading", "failed", "conflict"]),
  uploading: new Set(["uploading", "pending", "failed", "succeeded", "conflict"]),
  failed: new Set(["failed", "pending", "uploading", "conflict"]),
  conflict: new Set(["conflict", "pending", "failed"]),
  succeeded: new Set(["succeeded"]),
  not_configured: new Set(["not_configured", "pending", "failed"])
};

/**
 * The renderer's single queue-transition boundary. In native builds SQLite is
 * authoritative and validates the same transitions; React state is only its
 * current projection. Browser localStorage is an explicit development fallback.
 */
export function transitionScanQueueItem(
  item: SyncQueueItem,
  patch: Partial<Omit<SyncQueueItem, "id" | "kind" | "updatedAt">> & { status?: QueueStatus },
  now = new Date()
) {
  const nextStatus = patch.status ?? item.status;
  if (!transitions[item.status].has(nextStatus)) {
    throw new Error(`invalid scan queue transition: ${item.status} -> ${nextStatus}`);
  }
  return { ...item, ...patch, id: item.id, kind: item.kind, updatedAt: now.toISOString() };
}

export function updateScanQueueItem(
  items: SyncQueueItem[],
  id: string,
  patch: Partial<Omit<SyncQueueItem, "id" | "kind" | "updatedAt">> & { status?: QueueStatus },
  now = new Date()
) {
  return items.map((item) => item.id === id ? transitionScanQueueItem(item, patch, now) : item);
}
