import { useCallback, useEffect, type MutableRefObject } from "react";
import { resumeCaptureUpload, type CaptureUploadSource } from "../../api/captureUploads";
import type { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { loadDurableSpoolFile, hasDurableDesktopStore, persistDurableScanQueueItem } from "../../lib/durableStore";
import { transitionScanQueueItem, updateScanQueueItem } from "../../lib/scanQueueWorkflow";
import type { LocalLogEntry, SyncQueueItem } from "../../types";
import { genericStatusLabel } from "../../statusLabels";
import { applyUploadState, uploadStateFromQueueItem } from "./uploadState";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
type UpdateQueue = (updater: (current: SyncQueueItem[]) => SyncQueueItem[]) => void;

export function useUploadSync({
  client,
  token,
  isOnline,
  setIsOnline,
  queueRef,
  fileBufferRef,
  uploadInFlightRef,
  durablePersistenceRef,
  updateQueue,
  logEvent
}: {
  client: DesktopApiClient;
  token: string | null;
  isOnline: boolean;
  setIsOnline: (online: boolean) => void;
  queueRef: MutableRefObject<SyncQueueItem[]>;
  fileBufferRef: MutableRefObject<Map<string, File | CaptureUploadSource>>;
  uploadInFlightRef: MutableRefObject<Set<string>>;
  durablePersistenceRef: MutableRefObject<Promise<void>>;
  updateQueue: UpdateQueue;
  logEvent: LogEvent;
}) {
  const uploadQueueItem = useCallback(async (id: string) => {
    const item = queueRef.current.find((candidate) => candidate.id === id);
    const uploadState = item ? uploadStateFromQueueItem(item) : undefined;
    if (!item || item.kind !== "scan_upload" || uploadState?.status === "succeeded"
      || uploadState?.status === "uploading" || uploadInFlightRef.current.has(id)) return;
    if (!token) {
      updateQueue((current) => current.map((candidate) => candidate.id === id
        ? applyUploadState(candidate, { status: "failed", error: "未登录，无法通过后端鉴权上传" })
        : candidate));
      return;
    }
    if (!isOnline) {
      updateQueue((current) => updateScanQueueItem(current, id, { status: "pending", detail: "当前离线，等待联网后继续上传" }));
      return;
    }
    if (!item.examId || !item.captureBatchId || !item.idempotencyKey) {
      updateQueue((current) => current.map((candidate) => candidate.id === id
        ? applyUploadState(candidate, { status: "failed", error: "缺少考试、采集批次或幂等键，不能启动可恢复上传" })
        : candidate));
      return;
    }
    if (item.qualityChecks?.some((check) => check.status === "failed")) {
      updateQueue((current) => current.map((candidate) => candidate.id === id
        ? applyUploadState(candidate, { status: "failed", error: "本地质量检查未通过，未上传" })
        : candidate));
      return;
    }
    let file = fileBufferRef.current.get(id);
    if (!file && item.localAssetId && hasDurableDesktopStore()) {
      try {
        file = await loadDurableSpoolFile(item.localAssetId);
        fileBufferRef.current.set(id, file);
      } catch (error) {
        const message = getUserErrorMessage(error, "本地加密扫描原件无法恢复");
        updateQueue((current) => updateScanQueueItem(current, id, { status: "failed", detail: message }));
        await logEvent("error", "durable spool recovery failed", message);
        return;
      }
    }
    if (!file) {
      updateQueue((current) => updateScanQueueItem(current, id, {
        status: "failed",
        requiresReselect: true,
        detail: "本地队列已恢复，但文件句柄不可用；请重新选择同名文件后重试"
      }));
      return;
    }
    uploadInFlightRef.current.add(id);
    updateQueue((current) => updateScanQueueItem(current, id, {
      status: "uploading",
      progress: Math.max(item.progress, 1),
      detail: "正在上传到后端文件 API"
    }));
    try {
      const completed = await resumeCaptureUpload(client, {
        file,
        exam: item.examId,
        batch: item.captureBatchId,
        remoteUploadId: item.remoteUploadId,
        idempotency_key: item.idempotencyKey
      }, async (progress) => {
        const currentItem = queueRef.current.find((candidate) => candidate.id === id);
        if (!currentItem) throw new Error("本地耐久队列记录已丢失，已停止继续上传");
        const nextItem = transitionScanQueueItem(currentItem, {
          status: progress.status === "completed" ? "succeeded" : "uploading",
          progress: progress.totalBytes ? Math.round((progress.confirmedOffset / progress.totalBytes) * 100) : 0,
          confirmedOffset: progress.confirmedOffset,
          remoteUploadId: progress.remoteUploadId,
          fileAssetId: progress.fileAssetId ?? currentItem.fileAssetId,
          serverStatus: progress.captureFileId ? `采集文件 ${progress.captureFileId}` : `上传会话 ${progress.remoteUploadId}`,
          detail: progress.status === "completed" ? "服务端已确认采集文件" : `已确认 ${progress.confirmedOffset} / ${progress.totalBytes} 字节`
        });
        updateQueue((current) => current.map((candidate) => candidate.id === id ? nextItem : candidate));
        if (hasDurableDesktopStore()) {
          const persisted = durablePersistenceRef.current
            .catch(() => undefined)
            .then(() => persistDurableScanQueueItem(nextItem));
          durablePersistenceRef.current = persisted.catch((error) => console.warn("durable scan progress persistence failed", error));
          await persisted;
        }
      });
      if (completed.status !== "completed") {
        updateQueue((current) => updateScanQueueItem(current, id, {
          status: "pending",
          detail: `服务端状态为${genericStatusLabel(completed.status)}，将在下次同步继续确认`
        }));
        return;
      }
      updateQueue((current) => updateScanQueueItem(current, id, {
        status: "succeeded",
        progress: 100,
        fileAssetId: completed.file_asset_id ?? queueRef.current.find((candidate) => candidate.id === id)?.fileAssetId,
        remoteUploadId: completed.remote_upload_id,
        confirmedOffset: completed.confirmed_offset,
        serverStatus: completed.capture_file_id ? `采集文件 ${completed.capture_file_id}` : `上传会话 ${completed.remote_upload_id} 已确认`,
        detail: "服务端已确认采集文件；本地加密原件按保留策略继续保存"
      }));
      fileBufferRef.current.delete(id);
      await logEvent("info", "scan queue item uploaded", `${item.fileName ?? item.title} -> ${completed.capture_file_id ?? completed.remote_upload_id}`);
    } catch (error) {
      const message = getUserErrorMessage(error, "上传失败");
      updateQueue((current) => updateScanQueueItem(current, id, { status: "failed", detail: message }));
      await logEvent("error", "scan queue item failed", `${item.fileName ?? item.title}: ${message}`);
    } finally {
      uploadInFlightRef.current.delete(id);
    }
  }, [client, isOnline, logEvent, token, updateQueue]);

  const uploadQueueItems = useCallback(async (mode: "pending" | "failed" | "all") => {
    const candidates = queueRef.current.filter((item) => item.kind === "scan_upload"
      && (mode === "all" ? item.status === "pending" || item.status === "failed" : item.status === mode));
    for (const item of candidates) await uploadQueueItem(item.id);
  }, [uploadQueueItem]);

  useEffect(() => {
    const handleOnline = () => {
      setIsOnline(true);
      void logEvent("info", "network online", "scan queue auto resume");
      void uploadQueueItems("all");
    };
    const handleOffline = () => {
      setIsOnline(false);
      void logEvent("warning", "network offline", "scan upload paused");
    };
    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);
    return () => {
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
    };
  }, [logEvent, uploadQueueItems]);

  return { uploadQueueItem, uploadQueueItems };
}
