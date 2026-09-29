import { invoke } from "@tauri-apps/api/core";
import { sha256ForFile, type CaptureUploadSource } from "../api/captureUploads";
import type { OfflineDraftRecord, OfflineSyncStatus, ScanQualityCheck, SyncQueueItem } from "../types";
import { isTauriRuntime } from "./localRuntime";

/**
 * Native storage boundary for the Windows scan station.
 *
 * The Tauri implementation owns SQLite, encryption and filesystem paths. Browser
 * development is deliberately not a replacement for this store: callers must
 * choose an explicit development fallback where one is still supported.
 */
export interface DurableStoreStatus {
  ready: boolean;
  databasePath: string;
  spoolPath: string;
  legacyDataPresent: boolean;
}

export interface SpoolAssetInput {
  file: File;
  examId?: string;
  captureBatchId?: string;
  submissionId?: string;
  pageNo?: number;
  qualityChecks?: ScanQualityCheck[];
}

export interface DurableSpoolFile {
  filename: string;
  mime: string;
  size: number;
  sha256: string;
  chunkSize: number;
}

interface SpoolAssetSession {
  localAssetId: string;
  chunkSize: number;
  confirmedOffset: number;
  item?: SyncQueueItem;
}

export function hasDurableDesktopStore() {
  return isTauriRuntime();
}

let activeSessionId: string | null = null;
// 原生绑定按顺序执行，版本号只允许最后一次登录接管当前会话，避免快速切换账号时串用本地数据。
let transitionVersion = 0;
let sessionTransition: Promise<void> = Promise.resolve();

function requireSessionId(): string {
  if (!activeSessionId) throw new Error("请先登录，再访问当前账号的本地耐久数据。");
  return activeSessionId;
}

export function invokeScoped<T>(command: string, args: Record<string, unknown> = {}, sessionId = requireSessionId()): Promise<T> {
  if (activeSessionId !== sessionId) return Promise.reject(new Error("登录账号已切换，本地操作已停止。"));
  return invoke<T>(command, { ...args, sessionId });
}

export async function bindDurableSession(server: string, tenantId: string, actorId: string): Promise<string> {
  if (!isTauriRuntime()) return crypto.randomUUID();
  const version = ++transitionVersion;
  activeSessionId = null;
  let boundSessionId: string | null = null;
  sessionTransition = sessionTransition.catch(() => undefined).then(async () => {
    const sessionId = await invoke<string>("bind_durable_session", { server, tenantId, actorId });
    if (version === transitionVersion) {
      activeSessionId = sessionId;
      boundSessionId = sessionId;
    }
  });
  await sessionTransition;
  if (!boundSessionId) throw new Error("登录账号已切换，本地绑定已停止。");
  return boundSessionId;
}

export async function clearDurableSession(): Promise<void> {
  if (!isTauriRuntime()) return;
  ++transitionVersion;
  activeSessionId = null;
  sessionTransition = sessionTransition.catch(() => undefined).then(() => invoke<void>("clear_durable_session"));
  await sessionTransition;
}

export async function claimLegacyDurableStore(expectedSessionId: string): Promise<void> {
  requireDurableRuntime();
  await invokeScoped("claim_legacy_durable_store", {}, expectedSessionId);
}

export async function spoolScanAsset(input: SpoolAssetInput, expectedSessionId?: string): Promise<SyncQueueItem> {
  requireDurableRuntime();
  const sessionId = expectedSessionId ?? requireSessionId();
  const sha256 = await sha256ForFile(input.file);
  const session = await invokeScoped<SpoolAssetSession>("begin_spool_local_asset", {
    input: {
      filename: input.file.name,
      mime: input.file.type || inferContentType(input.file.name),
      size: input.file.size,
      sha256,
      examId: input.examId,
      captureBatchId: input.captureBatchId,
      submissionId: input.submissionId,
      pageNo: input.pageNo,
      qualityChecks: input.qualityChecks
    }
  }, sessionId);
  if (session.item) return session.item;
  let offset = session.confirmedOffset;
  // 续写位置以原生存储已确认的偏移为准；只有完整写入后才把扫描原件加入队列。
  while (offset < input.file.size) {
    const end = Math.min(offset + session.chunkSize, input.file.size);
    const bytes = Array.from(new Uint8Array(await input.file.slice(offset, end).arrayBuffer()));
    offset = await invokeScoped<number>("write_spool_local_asset_chunk", {
      localAssetId: session.localAssetId,
      offset,
      bytes
    }, sessionId);
  }
  return invokeScoped<SyncQueueItem>("complete_spool_local_asset", { localAssetId: session.localAssetId }, sessionId);
}

export async function listDurableScanQueue(expectedSessionId?: string): Promise<SyncQueueItem[]> {
  requireDurableRuntime();
  return invokeScoped<SyncQueueItem[]>("list_durable_scan_queue", {}, expectedSessionId);
}

export async function persistDurableScanQueueItem(item: SyncQueueItem, expectedSessionId?: string): Promise<void> {
  requireDurableRuntime();
  await invokeScoped("persist_durable_scan_queue_item", { item: withoutPreview(item) }, expectedSessionId);
}

export async function archiveDurableScanQueueItems(ids: string[], expectedSessionId?: string): Promise<void> {
  requireDurableRuntime();
  await invokeScoped("archive_durable_scan_queue_items", { ids }, expectedSessionId);
}

export async function loadDurableSpoolFile(localAssetId: string, expectedSessionId?: string): Promise<CaptureUploadSource> {
  requireDurableRuntime();
  const sessionId = expectedSessionId ?? requireSessionId();
  const stored = await invokeScoped<DurableSpoolFile>("read_durable_local_asset", { localAssetId }, sessionId);
  return {
    name: stored.filename,
    type: stored.mime,
    size: stored.size,
    sha256: stored.sha256,
    async slice(start: number, end: number) {
      if (start < 0 || end < start || end > stored.size) throw new Error("本地扫描原件读取范围无效");
      const parts: BlobPart[] = [];
      let offset = start;
      while (offset < end) {
        const length = Math.min(stored.chunkSize, end - offset);
        const bytes = await invokeScoped<number[]>("read_durable_local_asset_chunk", { localAssetId, offset, length }, sessionId);
        if (bytes.length !== length) throw new Error("本地扫描原件分块不完整");
        parts.push(new Uint8Array(bytes));
        offset += bytes.length;
      }
      return new Blob(parts, { type: stored.mime });
    }
  };
}

export async function saveDurableDraft(record: OfflineDraftRecord, expectedSessionId?: string): Promise<void> {
  requireDurableRuntime();
  await invokeScoped("save_durable_draft", { record }, expectedSessionId);
}

export async function listDurableDraftEnvelopes(expectedSessionId?: string): Promise<DurableDraftEnvelope[]> {
  requireDurableRuntime();
  return invokeScoped<DurableDraftEnvelope[]>("list_durable_drafts", {}, expectedSessionId);
}

export async function loadDurableDraft(taskId: string, expectedSessionId?: string): Promise<OfflineDraftRecord | null> {
  requireDurableRuntime();
  return invokeScoped<OfflineDraftRecord | null>("load_durable_draft", { taskId }, expectedSessionId);
}

export async function updateDurableDraftStatus(taskId: string, patch: { syncStatus: OfflineSyncStatus; syncMessage?: string }, expectedSessionId?: string): Promise<void> {
  requireDurableRuntime();
  await invokeScoped("update_durable_draft_status", { taskId, syncStatus: patch.syncStatus, syncMessage: patch.syncMessage }, expectedSessionId);
}

export async function purgeExpiredDurableDrafts(now = new Date(), expectedSessionId?: string): Promise<number> {
  requireDurableRuntime();
  return invokeScoped<number>("purge_expired_durable_drafts", { now: now.toISOString() }, expectedSessionId);
}

export interface DurableDraftEnvelope {
  taskId: string;
  anonymousCode: string;
  savedAt: string;
  expiresAt: string;
  syncStatus: OfflineSyncStatus;
  syncMessage?: string;
}

function withoutPreview(item: SyncQueueItem): SyncQueueItem {
  // Blob 预览地址只在当前 WebView 有效，不能作为重启后可恢复的数据保存。
  const { previewUrl: _previewUrl, ...stored } = item;
  return stored;
}

function requireDurableRuntime() {
  if (!isTauriRuntime()) {
    throw new Error("耐久扫描存储仅在 Windows 桌面客户端可用；浏览器开发模式不能作为生产扫描队列。");
  }
}

function inferContentType(filename: string) {
  const extension = filename.slice(filename.lastIndexOf(".")).toLowerCase();
  if (extension === ".pdf") return "application/pdf";
  if (extension === ".png") return "image/png";
  if (extension === ".jpg" || extension === ".jpeg") return "image/jpeg";
  if (extension === ".tif" || extension === ".tiff") return "image/tiff";
  return "application/octet-stream";
}
