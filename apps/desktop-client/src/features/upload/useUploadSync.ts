import { useCallback, useEffect, useRef, type RefObject } from "react";
import { resumeCaptureUpload, type CaptureUploadCompleteResponse, type CaptureUploadSource } from "../../api/captureUploads";
import type { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import { loadDurableSpoolFile, hasDurableDesktopStore, persistDurableScanQueueItem } from "../../lib/durableStore";
import { transitionScanQueueItem, updateScanQueueItem } from "../../lib/scanQueueWorkflow";
import type { LocalLogEntry, SyncQueueItem } from "../../types";
import { genericStatusLabel } from "../../statusLabels";
import { applyUploadState, uploadStateFromQueueItem } from "./uploadState";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
type UpdateQueue = (updater: (current: SyncQueueItem[]) => SyncQueueItem[]) => void;
type PreparedUploadQueueItem = SyncQueueItem & Required<Pick<SyncQueueItem, "examId" | "captureBatchId" | "idempotencyKey">>;

export function useUploadSync({
  client,
  token,
  durableScopeKey,
  isOnline,
  setIsOnline,
  queueRef,
  fileBufferRef,
  uploadInFlightRef,
  durablePersistenceRef,
  updateQueue,
  logEvent,
  onUploaded
}: {
  client: DesktopApiClient;
  token: string | null;
  durableScopeKey: string;
  isOnline: boolean;
  setIsOnline: (online: boolean) => void;
  queueRef: RefObject<SyncQueueItem[]>;
  fileBufferRef: RefObject<Map<string, File | CaptureUploadSource>>;
  uploadInFlightRef: RefObject<Set<string>>;
  durablePersistenceRef: RefObject<Promise<void>>;
  updateQueue: UpdateQueue;
  logEvent: LogEvent;
  onUploaded?: (context: { examId: string; captureBatchId: string }) => Promise<void>;
}) {
  const onlineRef = useRef(isOnline);
  onlineRef.current = isOnline;
  const scopeRef = useRef(durableScopeKey);
  const scopeEpoch = useRef(0);
  const activeUploads = useRef(new Map<string, AbortController>());
  // 账号作用域变化时立即中止旧上传；异步回调还会再次核对作用域，防止迟到结果写入新账号。
  if (scopeRef.current !== durableScopeKey) {
    for (const [id, controller] of activeUploads.current) {
      controller.abort();
      uploadInFlightRef.current.delete(id);
    }
    activeUploads.current.clear();
    scopeRef.current = durableScopeKey;
    scopeEpoch.current++;
  }
  // 阻断顺序先检查账号和登录，再检查联网及采集上下文；通过后才恢复原件或发起网络请求。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 同步预检读取可变队列 ref 的最新内容，与上传回调使用相同的依赖边界。
  const prepareUploadQueueItem = useCallback((id: string): PreparedUploadQueueItem | undefined => {
    const scope = durableScopeKey;
    if (scopeRef.current !== scope || (!scope && hasDurableDesktopStore())) return;
    const item = queueRef.current.find((candidate) => candidate.id === id);
    const uploadState = item ? uploadStateFromQueueItem(item) : undefined;
    if (item?.kind !== "scan_upload" || uploadState?.status === "succeeded"
      || uploadState?.status === "uploading" || uploadInFlightRef.current.has(id)) return;
    if (!token) {
      updateQueue((current) => current.map((candidate) => candidate.id === id
        ? applyUploadState(candidate, { status: "failed", error: "未登录，无法通过后端鉴权上传" })
        : candidate));
      return;
    }
    if (!onlineRef.current) {
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
    // 上面的检查已拒绝缺少上传上下文的队列项；只补足返回类型，仍返回队列中的原对象。
    return item as PreparedUploadQueueItem;
  }, [durableScopeKey, token, updateQueue]);

  // 服务端未确认完成时保留原件；发出成功队列更新后才移除内存原件，耐久原件仍按保留策略保存。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 队列和文件缓冲由 ref 管理，完成响应读取最新成员。
  const finishUploadQueueItem = useCallback((id: string, completed: CaptureUploadCompleteResponse, controller: AbortController, scope: string) => {
    if (isUploadInterrupted(controller, scopeRef, scope)) return false;
    if (completed.status !== "completed") {
      updateQueue((current) => updateScanQueueItem(current, id, {
        status: "pending",
        detail: `服务端状态为${genericStatusLabel(completed.status)}，将在下次同步继续确认`
      }));
      return false;
    }
    updateQueue((current) => updateScanQueueItem(current, id, {
      status: "succeeded",
      progress: 100,
      fileAssetId: completed.file_asset_id ?? queueRef.current.find((candidate) => candidate.id === id)?.fileAssetId,
      remoteUploadId: completed.remote_upload_id,
      confirmedOffset: completed.confirmed_offset,
      serverStatus: completed.capture_file_id ? `采集文件 ${completed.capture_file_id}` : `上传会话 ${completed.remote_upload_id} 已确认`,
      detail: "服务端已确认采集文件；本机加密原件持续留存，归档只隐藏队列，不释放磁盘空间"
    }));
    fileBufferRef.current.delete(id);
    return true;
  }, [updateQueue]);

  // 这些 ref 是跨渲染的可变队列；上传过程中必须读取最新内容，不能把 current 成员当作渲染依赖。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 队列和上传集合由 ref 管理，依赖其 current 成员会制造不稳定回调。
  const uploadQueueItem = useCallback(async (id: string) => {
    const scope = durableScopeKey;
    const item = prepareUploadQueueItem(id);
    if (!item) return;
    // 读取加密原件前就占用队列项，否则批量上传和单项重试可以同时发出请求。
    const controller = new AbortController();
    activeUploads.current.set(id, controller);
    uploadInFlightRef.current.add(id);
    let uploadConfirmed = false;
    let restoringOriginal = false;
    try {
      let file = fileBufferRef.current.get(id);
      if (!file && item.localAssetId && hasDurableDesktopStore()) {
        restoringOriginal = true;
        file = await loadDurableSpoolFile(item.localAssetId, scope);
      }
      if (isUploadInterrupted(controller, scopeRef, scope)) return;
      const currentItem = queueRef.current.find((candidate) => candidate.id === id);
      if (!currentItem || currentItem.idempotencyKey !== item.idempotencyKey ||
        currentItem.localAssetId !== item.localAssetId || (currentItem.status !== "pending" && currentItem.status !== "failed")) return;
      if (!file) {
        updateQueue((current) => updateScanQueueItem(current, id, {
          status: "failed", requiresReselect: true,
          detail: "本地队列已恢复，但文件句柄不可用；请重新选择同名文件后重试"
        }));
        return;
      }
      if (restoringOriginal) fileBufferRef.current.set(id, file);
      restoringOriginal = false;
      if (!onlineRef.current) return;
      updateQueue((current) => updateScanQueueItem(current, id, {
        status: "uploading",
        progress: Math.max(currentItem.progress, 1),
        detail: "正在上传到后端文件 API"
      }));
      const completed = await resumeCaptureUpload(client, {
        file,
        exam: item.examId,
        batch: item.captureBatchId,
        remoteUploadId: currentItem.remoteUploadId,
        idempotency_key: item.idempotencyKey
      }, async (progress) => {
        if (isUploadInterrupted(controller, scopeRef, scope)) throw new Error("登录账号已切换，上传已停止");
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
          // 本次进度必须排在此前队列写入之后，并等待落盘成功，才能允许续传下一块。
          const persisted = durablePersistenceRef.current
            .catch(() => undefined)
            .then(() => persistDurableScanQueueItem(nextItem, scope));
          durablePersistenceRef.current = persisted.catch((error) => console.warn("durable scan progress persistence failed", error));
          await persisted;
        }
      }, controller.signal);
      if (!finishUploadQueueItem(id, completed, controller, scope)) return;
      uploadConfirmed = true;
      if (!isUploadInterrupted(controller, scopeRef, scope)) await onUploaded?.({ examId: item.examId, captureBatchId: item.captureBatchId });
      await logEvent("info", "scan queue item uploaded", `${item.fileName ?? item.title} -> ${completed.capture_file_id ?? completed.remote_upload_id}`);
    } catch (error) {
      if (isUploadInterrupted(controller, scopeRef, scope)) return;
      // 上传回执已确认后，检查或日志失败只能提示刷新，不能把成功文件改回失败后重复上传。
      if (uploadConfirmed || queueRef.current.find((candidate) => candidate.id === id)?.status === "succeeded") {
        updateQueue((current) => updateScanQueueItem(current, id, { detail: "上传已完成，处理检查暂未更新；请刷新当前批次。" }));
        return;
      }
      const message = getUserErrorMessage(error, restoringOriginal ? "本地加密扫描原件无法恢复" : "上传失败");
      updateQueue((current) => updateScanQueueItem(current, id, { status: "failed", detail: message }));
      if (restoringOriginal) await logEvent("error", "durable spool recovery failed", message);
      else await logEvent("error", "scan queue item failed", `${item.fileName ?? item.title}: ${message}`);
    } finally {
      // 账号切换后相同 ID 可以重新占用，旧请求只能释放自己的位置。
      if (activeUploads.current.get(id) === controller) {
        activeUploads.current.delete(id);
        uploadInFlightRef.current.delete(id);
      }
    }
  }, [client, durableScopeKey, finishUploadQueueItem, logEvent, onUploaded, prepareUploadQueueItem, updateQueue]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 批量上传读取可变队列 ref 的最新快照。
  const uploadQueueItems = useCallback(async (mode: "pending" | "failed" | "all", context?: { examId: string; captureBatchId: string }) => {
    const epoch = scopeEpoch.current;
    const candidates = queueRef.current.filter((item) => item.kind === "scan_upload"
      && (!context || item.examId === context.examId && item.captureBatchId === context.captureBatchId)
      && (mode === "all" ? item.status === "pending" || item.status === "failed" : item.status === mode));
    // 切换账号后，旧批量命令不可继续；即使随后切回原账号，也需要重新发起。
    for (const item of candidates) {
      if (scopeEpoch.current !== epoch) return;
      await uploadQueueItem(item.id);
    }
  }, [uploadQueueItem]);

  useEffect(() => {
    const handleOnline = () => {
      onlineRef.current = true;
      setIsOnline(true);
      // 恢复网络只更新可用性，教师先核对归属再主动上传；误导入文件不能趁联网自动发送。
      void logEvent("info", "network online", "scan queue awaits teacher confirmation");
    };
    const handleOffline = () => {
      onlineRef.current = false;
      setIsOnline(false);
      void logEvent("warning", "network offline", "scan upload paused");
    };
    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);
    return () => {
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
    };
  }, [logEvent, setIsOnline]);

  return { uploadQueueItem, uploadQueueItems };
}

function isUploadInterrupted(controller: AbortController, scopeRef: RefObject<string>, scope: string) {
  return controller.signal.aborted || scopeRef.current !== scope;
}
