import { useCallback, useEffect, useRef, useState } from "react";
import { getUserErrorMessage } from "../../api/userError";
import { archiveDurableScanQueueItems, hasDurableDesktopStore, listDurableScanQueue, persistDurableScanQueueItem, spoolScanAsset } from "../../lib/durableStore";
import { isTauriRuntime } from "../../lib/localRuntime";
import { listOfflineDraftEnvelopes, readOfflineDraftEnvelopes } from "../../lib/offlineStore";
import { inferContentType, inspectScanFile, persistScanQueue, previewUrlForFile, readPersistedScanQueue } from "../../lib/scanFiles";
import { inspectScannerSample, type ScannerPreflightResult } from "../../lib/scannerProfile";
import type { CaptureUploadSource } from "../../api/captureUploads";
import type { Exam, LocalLogEntry, SyncQueueItem } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useScanQueue({
  exams,
  selectedExamId,
  captureBatchId,
  scanSubmissionId,
  scanStartPage,
  scannerPreflight,
  setDiagnosticError,
  logEvent
}: {
  exams: Exam[];
  selectedExamId: string;
  captureBatchId: string;
  scanSubmissionId: string;
  scanStartPage: number;
  scannerPreflight: ScannerPreflightResult | null;
  setDiagnosticError: (message: string | null) => void;
  logEvent: LogEvent;
}) {
  const [queue, setQueue] = useState<SyncQueueItem[]>(() => hasDurableDesktopStore() ? [] : readPersistedScanQueue());
  const [offlineDraftCount, setOfflineDraftCount] = useState(() => readOfflineDraftEnvelopes().length);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const fileBufferRef = useRef(new Map<string, File | CaptureUploadSource>());
  const uploadInFlightRef = useRef(new Set<string>());
  const queueRef = useRef(queue);
  const durablePersistenceRef = useRef<Promise<void>>(Promise.resolve());

  const updateQueue = useCallback((updater: (current: SyncQueueItem[]) => SyncQueueItem[]) => {
    const next = updater(queueRef.current);
    queueRef.current = next;
    setQueue(next);
    if (hasDurableDesktopStore()) {
      durablePersistenceRef.current = durablePersistenceRef.current
        .catch(() => undefined)
        .then(() => Promise.all(next.filter((item) => item.kind === "scan_upload").map(persistDurableScanQueueItem)))
        .then(() => undefined)
        .catch((error) => console.warn("durable scan queue persistence failed", error));
    } else {
      persistScanQueue(next);
    }
  }, []);

  useEffect(() => {
    if (hasDurableDesktopStore()) {
      void listDurableScanQueue()
        .then((items) => { queueRef.current = items; setQueue(items); })
        .catch((error) => setDiagnosticError(getUserErrorMessage(error, "本地耐久队列无法恢复")));
    }
    void listOfflineDraftEnvelopes().then((drafts) => setOfflineDraftCount(drafts.length)).catch(() => undefined);
  }, []);

  useEffect(() => { queueRef.current = queue; }, [queue]);
  useEffect(() => () => {
    for (const item of queueRef.current) if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    fileBufferRef.current.clear();
    uploadInFlightRef.current.clear();
  }, []);

  const handleFileSelection = async (files: FileList | null) => {
    const selected = files ? Array.from(files) : [];
    if (!selected.length) return;
    if (isTauriRuntime() && !scannerPreflight?.readyToScan) {
      setDiagnosticError("请先完成通过的扫描前检查，再将答题卡加入本地耐久队列。");
      return;
    }
    const selectedExam = exams.find((exam) => exam.id === selectedExamId);
    const submissionId = scanSubmissionId.trim();
    const startPage = scanStartPage || 1;
    const nextItems: SyncQueueItem[] = [];
    for (const [index, file] of selected.entries()) {
      const scannerChecks = isTauriRuntime() && scannerPreflight
        ? await inspectScannerSample(file, scannerPreflight.profile)
        : [];
      const qualityChecks = [...await inspectScanFile(file), ...scannerChecks];
      const failedQuality = qualityChecks.some((check) => check.status === "failed");
      if (hasDurableDesktopStore()) {
        try {
          const durableItem = await spoolScanAsset({
            file,
            examId: selectedExamId || undefined,
            captureBatchId: captureBatchId.trim() || undefined,
            submissionId: submissionId || undefined,
            pageNo: startPage + index,
            qualityChecks
          });
          fileBufferRef.current.set(durableItem.id, file);
          nextItems.push({
            ...durableItem,
            status: failedQuality ? "failed" : durableItem.status,
            detail: failedQuality ? "本地质量检查未通过，原件已安全保留" : durableItem.detail,
            previewUrl: previewUrlForFile(file),
            qualityChecks
          });
        } catch (error) {
          await logEvent("error", "scan asset durable spool failed", error instanceof Error ? error.message : String(error));
          setDiagnosticError(getUserErrorMessage(error, "扫描原件无法写入耐久本地存储，已停止加入上传队列"));
        }
        continue;
      }
      const recovered = queueRef.current.find((item) => item.requiresReselect && item.fileName === file.name && item.fileSize === file.size && item.kind === "scan_upload");
      if (recovered) {
        if (recovered.previewUrl) URL.revokeObjectURL(recovered.previewUrl);
        fileBufferRef.current.set(recovered.id, file);
        updateQueue((current) => current.map((item) => item.id === recovered.id ? {
          ...item,
          status: failedQuality ? "failed" : "pending",
          progress: 0,
          detail: failedQuality ? "本地质量检查未通过" : "文件句柄已恢复，等待上传",
          requiresReselect: false,
          qualityChecks,
          previewUrl: previewUrlForFile(file),
          updatedAt: new Date().toISOString()
        } : item));
        continue;
      }
      const id = crypto.randomUUID();
      fileBufferRef.current.set(id, file);
      nextItems.push({
        id,
        title: file.name,
        kind: "scan_upload",
        status: failedQuality ? "failed" : "pending",
        progress: 0,
        detail: failedQuality ? "本地质量检查未通过" : "已通过本地基础检查，等待上传",
        updatedAt: new Date().toISOString(),
        examId: selectedExamId || undefined,
        examName: selectedExam?.name,
        submissionId: submissionId || undefined,
        pageNo: startPage + index,
        fileName: file.name,
        fileSize: file.size,
        contentType: file.type || inferContentType(file.name),
        previewUrl: previewUrlForFile(file),
        requiresReselect: false,
        qualityChecks
      });
    }
    if (nextItems.length) updateQueue((current) => [...nextItems, ...current]);
    await logEvent("info", "scan files queued", `${selected.length} files`);
  };

  const clearSucceededQueueItems = async () => {
    const succeededIds = queueRef.current
      .filter((item) => item.kind === "scan_upload" && item.status === "succeeded")
      .map((item) => item.localAssetId ?? item.id);
    if (hasDurableDesktopStore() && succeededIds.length) {
      try {
        await archiveDurableScanQueueItems(succeededIds);
      } catch (error) {
        const message = getUserErrorMessage(error, "本地已确认扫描件无法归档");
        setDiagnosticError(message);
        await logEvent("warning", "durable scan archive failed", message);
        return;
      }
    }
    updateQueue((current) => {
      for (const item of current) {
        if (item.status !== "succeeded") continue;
        if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
        fileBufferRef.current.delete(item.id);
      }
      return current.filter((item) => item.status !== "succeeded");
    });
  };

  return {
    queue, offlineDraftCount, setOfflineDraftCount, fileInputRef, fileBufferRef,
    uploadInFlightRef, queueRef, durablePersistenceRef, updateQueue,
    handleFileSelection, clearSucceededQueueItems
  };
}
