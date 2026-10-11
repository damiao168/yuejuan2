import { apiClient, ApiClientError } from "./client";
import type { BusinessCommandReceipt } from "@edugrade/sdk";

export type BusinessCommandSession = Readonly<{ scope: string; generation: number }>;
let session: BusinessCommandSession = { scope: "", generation: 0 };

// 同一账号退出后重登也属于新会话；旧回执查询不能借用后来会话的 Cookie 继续提交。
export function setBusinessCommandScope(tenant: string, actor: string) {
  session = { scope: tenant && actor ? `${tenant}:${actor}` : "", generation: session.generation + 1 };
  if (typeof window !== "undefined") window.dispatchEvent(new Event("business-command-scope-changed"));
}

export function getBusinessCommandSession(): BusinessCommandSession { return session; }

export function isBusinessCommandSessionCurrent(expected: BusinessCommandSession): boolean {
  return Boolean(expected.scope) && expected.scope === session.scope && expected.generation === session.generation;
}

export class BusinessCommandSessionChangedError extends Error {
  constructor() {
    super("登录状态已变化，请在当前账号下重新确认操作结果");
    this.name = "BusinessCommandSessionChangedError";
  }
}

export function assertBusinessCommandSession(expected: BusinessCommandSession): void {
  if (!isBusinessCommandSessionCurrent(expected)) throw new BusinessCommandSessionChangedError();
}

export function captureBusinessCommandSession(tenant?: string, actor?: string): BusinessCommandSession {
  const expected = session;
  assertBusinessCommandSession(expected);
  if (tenant !== undefined && actor !== undefined && expected.scope !== `${tenant}:${actor}`) {
    throw new BusinessCommandSessionChangedError();
  }
  return expected;
}

const active = new Map<string, { session: BusinessCommandSession; promise: Promise<unknown> }>();

// 浏览器只持久化不透明的命令 ID，
// 不可变载荷保存在服务端的幂等预留记录中。
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
    // 对旧客户端写入的记录进行一次性迁移：本次恢复只在内存中
    // 保留旧载荷，并立即从磁盘记录中清除它。
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

// 全局入口先只读回执供用户核对。查看不能顺带重新发送冻结载荷。
export async function inspectPersistedBusinessCommand(operation: string, target: string, expectedSession: BusinessCommandSession) {
  assertBusinessCommandSession(expectedSession);
  const key = `business-command:${expectedSession.scope}:${operation}:${target}`;
  const raw = localStorage.getItem(key);
  if (!raw) throw new Error("待确认操作记录已不存在");
  const { command } = loadPersistedBusinessCommand(localStorage, key, raw);
  const receipt = await apiClient.request<BusinessCommandReceipt>(`/api/v1/${operation.split(".")[0]}-commands/${encodeURIComponent(command.id)}`);
  assertBusinessCommandSession(expectedSession);
  if (receipt.command_id !== command.id || (receipt.operation && receipt.operation !== operation) || (receipt.target_id && receipt.target_id !== target)) throw new Error("操作恢复结果不匹配");
  return receipt;
}

// 原始页面和全局待处理命令入口共用这个决策点。
// 过期预留只能通过 POST 冻结的命令 ID 和载荷恢复，
// 由服务端按条件获取该预留。
export async function recoverBusinessCommand<T>(
  operation: string,
  command: PersistedBusinessCommand,
  send: (id: string, original: unknown) => Promise<T>,
  recover: (result: unknown) => T,
  onRejected?: () => void,
  fallback?: RecoveryFallback,
  expectedSession: BusinessCommandSession = captureBusinessCommandSession(),
): Promise<T> {
  assertBusinessCommandSession(expectedSession);
  const domain = operation.split(".")[0];
  const receipt = await apiClient.request<BusinessCommandReceipt>(`/api/v1/${domain}-commands/${encodeURIComponent(command.id)}`);
  assertBusinessCommandSession(expectedSession);
  if (receipt.command_id !== command.id) throw new Error("操作恢复结果不匹配");
  if (receipt.status === "succeeded") return recover(receipt.result);
  const resend = async (payload: unknown) => {
    // 回执查询是异步的；新写请求前和返回后都检查，已发送的旧请求不能被当成本地撤销。
    assertBusinessCommandSession(expectedSession);
    const result = await send(command.id, payload);
    assertBusinessCommandSession(expectedSession);
    return result;
  };
  if (receipt.status === "not_accepted") {
    if (fallback) return resend(fallback.payload);
    onRejected?.();
    throw new ApiClientError(409, "command_not_accepted", "原操作未被服务器接收，请返回业务页面重新提交");
  }
  if (receipt.status === "takeover_ready") {
    if ("payload" in receipt) return resend(receipt.payload);
    if (fallback) return resend(fallback.payload);
    throw new ApiClientError(409, "business_command_payload_missing", "原操作缺少可恢复输入，请联系管理员核对，暂勿重复提交");
  }
  if (receipt.status === "rejected") onRejected?.();
  throw businessCommandRecoveryError(receipt);
}

// 请求结果明确前，以持久化请求为准。编辑后的界面草稿
// 不能替换可能已经提交成功的请求。
export function executeBusinessCommand<T>(operation: string, target: string, payload: unknown, send: (id: string, original: unknown) => Promise<T>, recover: (result: unknown) => T): Promise<T> {
  return runBusinessCommand(operation, target, payload, send, recover, false);
}

// 全局恢复入口与原业务页面共用进行中的 Promise，避免同一个命令同时触发两次续发。
export function resumePersistedBusinessCommand<T>(operation: string, target: string, send: (id: string, original: unknown) => Promise<T>, recover: (result: unknown) => T, expectedSession: BusinessCommandSession): Promise<T> {
  return runBusinessCommand(operation, target, undefined, send, recover, true, expectedSession);
}

function runBusinessCommand<T>(operation: string, target: string, payload: unknown, send: (id: string, original: unknown) => Promise<T>, recover: (result: unknown) => T, requirePersisted: boolean, expectedSession = getBusinessCommandSession()): Promise<T> {
  try { assertBusinessCommandSession(expectedSession); } catch (error) { return Promise.reject(error); }
  const key = `business-command:${expectedSession.scope}:${operation}:${target}`;
  const existing = active.get(key);
  if (existing?.session.generation === expectedSession.generation) return existing.promise as Promise<T>;
  const work = (async () => {
    const raw = localStorage.getItem(key);
    if (!raw && requirePersisted) throw new Error("待确认操作记录已不存在");
    const loaded = raw ? loadPersistedBusinessCommand(localStorage, key, raw) : undefined;
    const command = loaded?.command ?? { id: crypto.randomUUID() };
    const initialPayload = raw ? undefined : { payload: structuredClone(payload) };
    if (!raw) localStorage.setItem(key, JSON.stringify(command));
    const removeOriginalRecord = () => {
      assertBusinessCommandSession(expectedSession);
      // 同一持久化键可能已经被另一页或后来的操作替换，只能清除本次命令 ID。
      const current = localStorage.getItem(key);
      if (current && (JSON.parse(current) as PersistedBusinessCommand).id === command.id) localStorage.removeItem(key);
    };
    try {
      let result: T;
      if (raw) {
        result = await recoverBusinessCommand(operation, command, send, recover, removeOriginalRecord, loaded?.fallback, expectedSession);
      } else {
        assertBusinessCommandSession(expectedSession);
        result = await send(command.id, initialPayload!.payload);
      }
      assertBusinessCommandSession(expectedSession);
      try { removeOriginalRecord(); } catch { /* 保留已接受的回执，以便恢复。 */ }
      if (typeof window !== "undefined") window.dispatchEvent(new Event("business-command-changed"));
      return result;
    } catch (error) {
      // 身份变更后的结果只保留原记录，不清缓存、不发新请求，也不更新后来会话的恢复入口。
      assertBusinessCommandSession(expectedSession);
      // 输入或修订号被明确拒绝时，可以编辑为新命令；
      // 传输失败、限流或与其他命令冲突时则不能。
      if (error instanceof ApiClientError && (
        error.status === 400
        || error.code === "recent_auth_required"
        || ["review_revision_conflict", "review_task_revision_conflict", "arbitration_revision_conflict"].includes(error.code)
      )) removeOriginalRecord();
      if (typeof window !== "undefined") window.dispatchEvent(new Event("business-command-changed"));
      throw error;
    }
  })();
  active.set(key, { session: expectedSession, promise: work });
  void work.finally(() => { if (active.get(key)?.promise === work) active.delete(key); }).catch(() => undefined);
  return work;
}
