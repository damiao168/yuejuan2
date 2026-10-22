import { useCallback, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from "react";
import { getUserErrorMessage } from "../../api/userError";
import { hasDurableDesktopStore, listDurableScanQueue } from "../../lib/durableStore";
import { getCaptureScanContext } from "../../api/exams";
import type { DesktopApiClient } from "../../api/client";
import type { SyncQueueItem } from "../../types";

interface RecoveryOptions {
  client?: DesktopApiClient;
  token?: string | null;
  scopeRef: RefObject<string>;
  queueRef: RefObject<SyncQueueItem[]>;
  setQueue: Dispatch<SetStateAction<SyncQueueItem[]>>;
  updateQueue: (updater: (items: SyncQueueItem[]) => SyncQueueItem[]) => void;
  setDiagnosticError: (message: string | null) => void;
}

// 恢复快照只补缺失原件，元数据只更新显示名称；两类请求各自按账号租约和代次丢弃迟到结果。
export function useScanQueueRecovery({ client, token, scopeRef, queueRef, setQueue, updateQueue, setDiagnosticError }: RecoveryOptions) {
  const [queueRestoreStatus, setQueueRestoreStatus] = useState<"loading" | "ready" | "error" | "locked">(() => hasDurableDesktopStore() ? "loading" : "ready");
  const [queueRestoreError, setQueueRestoreError] = useState<string | null>(null);
  const [queueContextError, setQueueContextError] = useState<string | null>(null);
  const [loadingQueueContext, setLoadingQueueContext] = useState(false);
  const restoreRequestRef = useRef(0);
  const queueContextRequestRef = useRef(0);
  const restoreQueue = useCallback(async () => {
    if (!hasDurableDesktopStore()) { setQueueRestoreStatus("ready"); return; }
    const scope = scopeRef.current;
    if (!scope) { setQueueRestoreStatus("locked"); return; }
    const request = ++restoreRequestRef.current;
    setQueueRestoreStatus("loading"); setQueueRestoreError(null);
    const isCurrent = () => scopeRef.current === scope && request === restoreRequestRef.current;
    try {
      const items = await listDurableScanQueue(scope);
      if (!isCurrent()) return;
      // 同页重读不清除现有项；上传/导入产生的新状态优先，恢复快照只补回缺失原件。
      const current = queueRef.current;
      const currentIDs = new Set(current.map((item) => item.id));
      const merged = [...current, ...items.filter((item) => !currentIDs.has(item.id))];
      queueRef.current = merged; setQueue(merged); setQueueRestoreStatus("ready");
    } catch (error) {
      if (!isCurrent()) return;
      const message = getUserErrorMessage(error, "本机上传队列读取失败，请检查账号授权和磁盘后重新读取。");
      setQueueRestoreError(message); setQueueRestoreStatus("error"); setDiagnosticError(message);
    }
  }, [setDiagnosticError, queueRef, scopeRef, setQueue]);

  const refreshQueueContext = useCallback(async () => {
    if (!client || !token || !scopeRef.current) return;
    const scope = scopeRef.current;
    const request = ++queueContextRequestRef.current;
    setLoadingQueueContext(true); setQueueContextError(null);
    const isCurrent = () => scopeRef.current === scope && request === queueContextRequestRef.current;
    const batches = new Map(queueRef.current.filter((item) => item.kind === "scan_upload" && item.examId && item.captureBatchId).map((item) => [item.captureBatchId!, item.examId!]));
    const failures: string[] = [];
    for (const [batchId, examId] of batches) {
      try {
        const context = await getCaptureScanContext(client, batchId);
        if (!isCurrent()) return;
        if (context.batch.id !== batchId || context.batch.exam_id !== examId || context.exam.id !== examId) throw new Error("考试与批次归属不一致，请联系学校管理员核对。");
        // 元数据只替换显示名称，绝不修改原上传目标；旧记录也按原考试与批次重新授权读取。
        updateQueue((items) => items.map((item) => item.examId === examId && item.captureBatchId === batchId ? { ...item, examName: context.exam.name, captureBatchName: context.batch.name, captureBatchCreatedAt: context.batch.created_at } : item));
      } catch (error) {
        if (!isCurrent()) return;
        failures.push(getUserErrorMessage(error, "部分考试与批次名称未能确认，请重新读取；原上传归属保持不变。"));
      }
    }
    if (isCurrent()) { setQueueContextError(failures[0] ?? null); setLoadingQueueContext(false); }
  }, [client, token, updateQueue, queueRef, scopeRef]);

  const invalidateRecovery = useCallback(() => {
    restoreRequestRef.current += 1;
    queueContextRequestRef.current += 1;
  }, []);
  const resetRecovery = useCallback(() => {
    invalidateRecovery();
    setQueueRestoreError(null); setQueueContextError(null); setLoadingQueueContext(false);
  }, [invalidateRecovery]);
  return { queueRestoreStatus, queueRestoreError, queueContextError, loadingQueueContext,
    restoreQueue, refreshQueueContext, resetRecovery, invalidateRecovery };
}
