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
 * 渲染层唯一的队列状态转换边界。原生构建以 SQLite 为准，
 * 并由其校验相同的状态转换；React 状态只表示当前投影。
 * 浏览器 localStorage 是显式启用的开发回退。
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
