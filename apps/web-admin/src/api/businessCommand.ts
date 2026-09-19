import { apiClient, ApiClientError } from "./client";
import type { BusinessCommandReceipt } from "@edugrade/sdk";

let scope = "";
export function setBusinessCommandScope(tenant: string, actor: string) { scope = tenant && actor ? `${tenant}:${actor}` : ""; }
const active = new Map<string, Promise<unknown>>();

// Browser persistence is deliberately limited to an opaque command id. The
// immutable payload lives in the server-side idempotency reservation.
export type PersistedBusinessCommand = { id: string };
type RecoveryFallback = { payload: unknown };

export function loadPersistedBusinessCommand(storage: Storage, key: string, raw: string): { command: PersistedBusinessCommand; fallback?: RecoveryFallback } {
  let parsed: { id?: unknown; payload?: unknown };
  try {
    parsed = JSON.parse(raw) as { id?: unknown; payload?: unknown };
  } catch {
    storage.removeItem(key);
    throw new Error("本地待确认操作记录无效");
  }
  if (typeof parsed.id !== "string" || !parsed.id) {
    storage.removeItem(key);
    throw new Error("本地待确认操作记录无效");
  }
  const command = { id: parsed.id };
  if ("payload" in parsed) {
    // One-time migration for records written by older clients: keep the old
    // payload in memory for this recovery attempt, and scrub it from disk now.
    storage.setItem(key, JSON.stringify(command));
    return { command, fallback: { payload: parsed.payload } };
  }
  return { command };
}

export function businessCommandRecoveryError(receipt: BusinessCommandReceipt) {
  if (receipt.status === "processing") {
    return new ApiClientError(409, "operation_in_progress", "原操作仍在处理中，请稍后继续确认");
  }
  if (receipt.status === "unknown") {
    return new ApiClientError(receipt.http_status ?? 409, receipt.error_code ?? "operation_outcome_unknown", "原操作结果暂时无法确认，请保留记录并稍后重试");
  }
  return new ApiClientError(receipt.http_status ?? 400, receipt.error_code ?? "command_rejected", "原操作已被明确拒绝，请修改后重新提交");
}

// Both the originating page and the global pending-command affordance use this
// decision point. A stale reservation is resumed only by POSTing the frozen
// command ID and payload; the server conditionally acquires the reservation.
export async function recoverBusinessCommand<T>(
  operation: string,
  command: PersistedBusinessCommand,
  send: (id: string, original: unknown) => Promise<T>,
  recover: (result: unknown) => T,
  onRejected?: () => void,
  fallback?: RecoveryFallback,
): Promise<T> {
  const domain = operation.split(".")[0];
  const receipt = await apiClient.request<BusinessCommandReceipt>(`/api/v1/${domain}-commands/${encodeURIComponent(command.id)}`);
  if (receipt.command_id !== command.id) throw new Error("操作恢复结果不匹配");
  if (receipt.status === "succeeded") return recover(receipt.result);
  if (receipt.status === "not_accepted") {
    if (fallback) return send(command.id, fallback.payload);
    onRejected?.();
    throw new ApiClientError(409, "command_not_accepted", "原操作未被服务器接收，请返回业务页面重新提交");
  }
  if (receipt.status === "takeover_ready") {
    if ("payload" in receipt) return send(command.id, receipt.payload);
    if (fallback) return send(command.id, fallback.payload);
    throw new ApiClientError(409, "business_command_payload_missing", "原操作缺少可恢复输入，请联系管理员核对，暂勿重复提交");
  }
  if (receipt.status === "rejected") onRejected?.();
  throw businessCommandRecoveryError(receipt);
}

// The persisted request is authoritative until its outcome is known. An edited
// screen draft cannot replace a request that may already have committed.
export function executeBusinessCommand<T>(operation: string, target: string, payload: unknown, send: (id: string, original: unknown) => Promise<T>, recover: (result: unknown) => T): Promise<T> {
  if (!scope) return Promise.reject(new Error("登录状态尚未就绪"));
  const key = `business-command:${scope}:${operation}:${target}`;
  const existing = active.get(key);
  if (existing) return existing as Promise<T>;
  const work = (async () => {
    const raw = localStorage.getItem(key);
    const loaded = raw ? loadPersistedBusinessCommand(localStorage, key, raw) : undefined;
    const command = loaded?.command ?? { id: crypto.randomUUID() };
    const initialPayload = raw ? undefined : { payload: structuredClone(payload) };
    if (!raw) localStorage.setItem(key, JSON.stringify(command));
    try {
      let result: T;
      if (raw) {
        result = await recoverBusinessCommand(operation, command, send, recover, () => localStorage.removeItem(key), loaded?.fallback);
      } else result = await send(command.id, initialPayload!.payload);
      try { localStorage.removeItem(key); } catch { /* Keep the accepted receipt recoverable. */ }
      return result;
    } catch (error) {
      // Explicit rejected input/revision can be edited into a new command. A
      // transport failure, throttling, or conflict with another command cannot.
      if (error instanceof ApiClientError && (
        error.status === 400
        || error.code === "recent_auth_required"
        || ["review_revision_conflict", "review_task_revision_conflict", "arbitration_revision_conflict"].includes(error.code)
      )) localStorage.removeItem(key);
      if (typeof window !== "undefined") window.dispatchEvent(new Event("business-command-changed"));
      throw error;
    }
  })();
  active.set(key, work);
  void work.finally(() => { if (active.get(key) === work) active.delete(key); }).catch(() => undefined);
  return work;
}
