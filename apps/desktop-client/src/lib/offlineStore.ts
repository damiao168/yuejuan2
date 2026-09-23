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
  /** Browser-development fallback only; native envelopes never expose ciphertext to WebView. */
  encrypted?: EncryptedPayload;
}

/** Loads durable Tauri metadata, or the explicit encrypted browser-dev fallback. */
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
  writeOfflineDraftEnvelopes([next, ...envelopes].slice(0, 200));
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

export async function purgeExpiredOfflineDrafts(now = new Date(), expectedSessionId?: string) {
  if (hasDurableDesktopStore()) {
    return purgeExpiredDurableDrafts(now, expectedSessionId);
  }
  const before = readOfflineDraftEnvelopes();
  const after = before.filter((item) => {
    const expiresAt = new Date(item.expiresAt);
    return Number.isNaN(expiresAt.getTime()) || expiresAt > now;
  });
  writeOfflineDraftEnvelopes(after);
  return before.length - after.length;
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
    binary += String.fromCharCode(byte);
  }
  return window.btoa(binary);
}

function fromBase64(value: string) {
  const binary = window.atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function toArrayBuffer(bytes: Uint8Array) {
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  return buffer;
}
