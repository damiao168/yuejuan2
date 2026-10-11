import type { OfflineDraftRecord, OfflineSyncStatus } from "../types";
import {
  hasDurableDesktopStore,
  listDurableDraftEnvelopes,
  loadDurableDraft,
  purgeExpiredDurableDrafts,
  saveDurableDraft,
  updateDurableDraftStatus
} from "./durableStore";

const offlineStoreKey = "edugrade.desktop.offline_drafts";
const encoder = new TextEncoder();
const decoder = new TextDecoder();

interface EncryptedPayload {
  version: 1;
  salt: string;
  iv: string;
  data: string;
}

export interface OfflineDraftEnvelope {
  taskId: string;
  anonymousCode: string;
  savedAt: string;
  expiresAt: string;
  syncStatus: OfflineSyncStatus;
  syncMessage?: string;
  /** 仅供浏览器开发回退使用；原生封装不会向 WebView 暴露密文。 */
  encrypted?: EncryptedPayload;
}

/** 加载持久化的 Tauri 元数据，或显式启用的加密浏览器开发回退数据。 */
export async function listOfflineDraftEnvelopes(expectedSessionId?: string): Promise<OfflineDraftEnvelope[]> {
  if (hasDurableDesktopStore()) {
    return listDurableDraftEnvelopes(expectedSessionId);
  }
  return readOfflineDraftEnvelopes();
}

export function readOfflineDraftEnvelopes(): OfflineDraftEnvelope[] {
  try {
    const raw = window.localStorage.getItem(offlineStoreKey);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as OfflineDraftEnvelope[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export async function saveOfflineDraft(record: OfflineDraftRecord, passphrase?: string, expectedSessionId?: string) {
  if (hasDurableDesktopStore()) {
    await saveDurableDraft(record, expectedSessionId);
    return;
  }
  requireBrowserPassphrase(passphrase);
  const encrypted = await encryptJson(record, passphrase);
  const envelopes = readOfflineDraftEnvelopes().filter((item) => item.taskId !== record.taskId);
  const next: OfflineDraftEnvelope = {
    taskId: record.taskId,
    anonymousCode: record.anonymousCode,
    savedAt: record.savedAt,
    expiresAt: record.expiresAt,
    syncStatus: record.syncStatus,
    syncMessage: record.syncMessage,
    encrypted
  };
  // 容量不足时拒绝这次保存；不能静默淘汰旧记录中的待确认命令。
  writeOfflineDraftEnvelopes([next, ...envelopes]);
}

export async function loadOfflineDraft(taskId: string, passphrase?: string, expectedSessionId?: string) {
  if (hasDurableDesktopStore()) {
    return loadDurableDraft(taskId, expectedSessionId);
  }
  requireBrowserPassphrase(passphrase);
  const envelope = readOfflineDraftEnvelopes().find((item) => item.taskId === taskId);
  if (!envelope?.encrypted) {
    return null;
  }
  return decryptJson<OfflineDraftRecord>(envelope.encrypted, passphrase);
}

export async function updateOfflineDraftStatus(taskId: string, patch: { syncStatus: OfflineSyncStatus; syncMessage?: string }, expectedSessionId?: string) {
  // 同步状态单独保存在外层元数据中；这里不重写加密正文，读取时不能只相信正文里的旧状态。
  if (hasDurableDesktopStore()) {
    await updateDurableDraftStatus(taskId, patch, expectedSessionId);
    return;
  }
  const envelopes = readOfflineDraftEnvelopes().map((item) =>
    item.taskId === taskId
      ? {
          ...item,
          syncStatus: patch.syncStatus,
          syncMessage: patch.syncMessage,
          savedAt: new Date().toISOString()
        }
      : item
  );
  writeOfflineDraftEnvelopes(envelopes);
}

export async function purgeExpiredOfflineDrafts(now = new Date(), expectedSessionId?: string, passphrase?: string) {
  if (hasDurableDesktopStore()) {
    return purgeExpiredDurableDrafts(now, expectedSessionId);
  }
  const before = readOfflineDraftEnvelopes();
  const removable = new Map<string, string>();
  // 评分响应可能丢失；保留期到了也不能删除尚未确认的命令 ID 和原载荷。
  // 先完成全部候选的解密检查，密钥错误或密文损坏时不执行部分清理。
  for (const item of before) {
    const expiresAt = new Date(item.expiresAt);
    if (Number.isNaN(expiresAt.getTime()) || expiresAt > now) continue;
    requireBrowserPassphrase(passphrase);
    if (!item.encrypted) throw new Error("过期草稿缺少加密正文，已停止清理并保留本地记录。");
    const record = await decryptJson<OfflineDraftRecord>(item.encrypted, passphrase);
    if (!record || typeof record !== "object" || Array.isArray(record)) {
      throw new Error("过期草稿正文无法识别，已停止清理并保留本地记录。");
    }
    // 外层状态是最新持久化的服务端确认结果，正文可能仍是网络发送前的 syncing。
    // 普通未提交草稿可能是教师唯一的评分副本；自动清理只删除服务端已确认的已同步记录。
    if (item.syncStatus !== "synced") continue;
    removable.set(item.taskId, JSON.stringify(item));
  }
  // 解密期间可能有新保存；只删除仍与检查时完全相同的记录，不能覆盖新正文或新回执。
  const current = readOfflineDraftEnvelopes();
  const after = current.filter((item) => removable.get(item.taskId) !== JSON.stringify(item));
  writeOfflineDraftEnvelopes(after);
  return current.length - after.length;
}

function requireBrowserPassphrase(passphrase: string | undefined): asserts passphrase is string {
  if (!passphrase || passphrase.trim().length < 8) {
    throw new Error("浏览器开发模式需要至少 8 位离线密钥；生产桌面端使用 Windows 受保护主密钥。");
  }
}

async function encryptJson(value: unknown, passphrase: string): Promise<EncryptedPayload> {
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const key = await deriveKey(passphrase, salt);
  const plaintext = encoder.encode(JSON.stringify(value));
  const encrypted = await crypto.subtle.encrypt({ name: "AES-GCM", iv: toArrayBuffer(iv) }, key, plaintext);
  return {
    version: 1,
    salt: base64(salt),
    iv: base64(iv),
    data: base64(new Uint8Array(encrypted))
  };
}

async function decryptJson<T>(payload: EncryptedPayload, passphrase: string): Promise<T> {
  const salt = fromBase64(payload.salt);
  const iv = fromBase64(payload.iv);
  const data = fromBase64(payload.data);
  const key = await deriveKey(passphrase, salt);
  const decrypted = await crypto.subtle.decrypt({ name: "AES-GCM", iv: toArrayBuffer(iv) }, key, toArrayBuffer(data));
  return JSON.parse(decoder.decode(decrypted)) as T;
}

async function deriveKey(passphrase: string, salt: Uint8Array) {
  // 解密旧草稿也使用这些参数；修改迭代次数或算法时，需要同时设计旧版本的读取方式。
  const material = await crypto.subtle.importKey("raw", encoder.encode(passphrase), "PBKDF2", false, ["deriveKey"]);
  return crypto.subtle.deriveKey(
    {
      name: "PBKDF2",
      salt: toArrayBuffer(salt),
      iterations: 120000,
      hash: "SHA-256"
    },
    material,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"]
  );
}

function writeOfflineDraftEnvelopes(envelopes: OfflineDraftEnvelope[]) {
  window.localStorage.setItem(offlineStoreKey, JSON.stringify(envelopes));
}

function base64(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCodePoint(byte);
  }
  return window.btoa(binary);
}

function fromBase64(value: string) {
  const binary = window.atob(value);
  const bytes = new Uint8Array(binary.length);
  // atob 返回的每个字符都是 0..255 的单字节值，不涉及 UTF-16 代理对；旧加密载荷保持兼容。
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.codePointAt(index) ?? 0;
  }
  return bytes;
}

function toArrayBuffer(bytes: Uint8Array) {
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  return buffer;
}
