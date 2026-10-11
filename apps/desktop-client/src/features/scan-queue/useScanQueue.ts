import { useCallback, useEffect, useRef, useState } from "react";
import { Modal } from "antd";
import { getUserErrorMessage } from "../../api/userError";
import { archiveDurableScanQueueItems, hasDurableDesktopStore, listDurableScanQueue, persistDurableScanQueueItem, spoolScanAsset, removeUnstartedScanQueueItem } from "../../lib/durableStore";
import { isTauriRuntime } from "../../lib/localRuntime";
import { listOfflineDraftEnvelopes, readOfflineDraftEnvelopes } from "../../lib/offlineStore";
import { inferContentType, inspectScanFile, persistScanQueue, previewUrlForFile, readPersistedScanQueue } from "../../lib/scanFiles";
import { inspectScannerSample, type ScannerProfile } from "../../lib/scannerProfile";
import { getCaptureScanContext, type CaptureBatch, type CaptureScanContext } from "../../api/exams";
import type { DesktopApiClient } from "../../api/client";
import type { CaptureUploadSource } from "../../api/captureUploads";
import type { Exam, LocalLogEntry, SyncQueueItem } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;
type FileImportResult = { filename: string; saved: boolean; detail: string };
type FileImportStep = "stop" | "continue";

export function useScanQueue({
  durableScopeKey,
  exams,
  selectedExamId,
  captureBatchId,
  scanSubmissionId,
  scanStartPage,
  captureBatches,
  scanContext,
  client,
  token,
  fileImportTemplate,
  setDiagnosticError,
  logEvent
}: {
  durableScopeKey: string;
  exams: Exam[];
  selectedExamId: string;
  captureBatchId: string;
  scanSubmissionId: string;
  scanStartPage: number;
  captureBatches: CaptureBatch[];
  scanContext: CaptureScanContext | null;
  client?: DesktopApiClient;
  token?: string | null;
  fileImportTemplate: Pick<ScannerProfile, "templatePreset" | "paperSize" | "dpi" | "duplex">;
  setDiagnosticError: (message: string | null) => void;
  logEvent: LogEvent;
}) {
  const [queue, setQueue] = useState<SyncQueueItem[]>(() => hasDurableDesktopStore() ? [] : readPersistedScanQueue());
  const [offlineDraftCount, setOfflineDraftCount] = useState(() => readOfflineDraftEnvelopes().length);
  const [fileImportError, setFileImportError] = useState<string | null>(null);
  const [importResults, setImportResults] = useState<FileImportResult[]>([]);
  const [queueRestoreStatus, setQueueRestoreStatus] = useState<"loading" | "ready" | "error" | "locked">(() => hasDurableDesktopStore() ? "loading" : "ready");
  const [queueRestoreError, setQueueRestoreError] = useState<string | null>(null);
  const [queueContextError, setQueueContextError] = useState<string | null>(null);
  const [loadingQueueContext, setLoadingQueueContext] = useState(false);
  const restoreRequestRef = useRef(0);
  const queueContextRequestRef = useRef(0);
  const restoredScopeRef = useRef("");
  const queueRestoreStatusRef = useRef(queueRestoreStatus);
  queueRestoreStatusRef.current = queueRestoreStatus;
  const replacementId = useRef<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const fileBufferRef = useRef(new Map<string, File | CaptureUploadSource>());
  const uploadInFlightRef = useRef(new Set<string>());
  const queueRef = useRef(queue);
  const durablePersistenceRef = useRef<Promise<void>>(Promise.resolve());
  const scopeRef = useRef(durableScopeKey);
  scopeRef.current = durableScopeKey;

  const updateQueue = useCallback((updater: (current: SyncQueueItem[]) => SyncQueueItem[]) => {
    const next = updater(queueRef.current);
    queueRef.current = next;
    setQueue(next);
    if (hasDurableDesktopStore()) {
      if (!scopeRef.current) return;
      const scope = scopeRef.current;
      durablePersistenceRef.current = durablePersistenceRef.current
        .catch(() => undefined)
        .then(() => scopeRef.current === scope ? Promise.all(next.filter((item) => item.kind === "scan_upload")
          .map((item) => persistDurableScanQueueItem(item, scope))) : undefined)
        .then(() => undefined)
        .catch((error) => console.warn("durable scan queue persistence failed", error));
    } else {
      persistScanQueue(next);
    }
  }, []);

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
  }, [setDiagnosticError]);

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
  }, [client, token, updateQueue]);

  useEffect(() => {
    setFileImportError(null);
    setImportResults([]);
    replacementId.current = null;
    restoredScopeRef.current = "";
    restoreRequestRef.current += 1; queueContextRequestRef.current += 1;
    setQueueRestoreError(null); setQueueContextError(null); setLoadingQueueContext(false);
    if (!hasDurableDesktopStore()) return;
    for (const item of queueRef.current) if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    fileBufferRef.current.clear();
    uploadInFlightRef.current.clear();
    queueRef.current = [];
    setQueue([]);
    setOfflineDraftCount(0);
    void restoreQueue();
    if (durableScopeKey) {
      void listOfflineDraftEnvelopes(durableScopeKey)
        .then((drafts) => { if (scopeRef.current === durableScopeKey) setOfflineDraftCount(drafts.length); })
        .catch(() => undefined);
    }
  }, [durableScopeKey, restoreQueue]);

  useEffect(() => {
    if (queueRestoreStatus === "ready" && client && token && durableScopeKey && restoredScopeRef.current !== durableScopeKey) {
      restoredScopeRef.current = durableScopeKey;
      void refreshQueueContext();
    }
  }, [client, durableScopeKey, queueRestoreStatus, refreshQueueContext, token]);

  useEffect(() => { queueRef.current = queue; }, [queue]);
  useEffect(() => () => {
    restoreRequestRef.current += 1; queueContextRequestRef.current += 1;
    for (const item of queueRef.current) if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    fileBufferRef.current.clear();
    uploadInFlightRef.current.clear();
  }, []);

  const prepareFileSelection = (files: FileList | null) => {
    if (hasDurableDesktopStore() && queueRestoreStatusRef.current !== "ready") {
      setFileImportError("本机原队列尚未核实，请先重新读取队列，再决定是否导入，避免重复选择同一批答卷。"); return;
    }
    if (hasDurableDesktopStore() && !durableScopeKey) {
      setDiagnosticError("请先登录，再保存扫描原件。");
      setFileImportError("请先在线登录或解锁本账号的有效本机授权，再保存原件。");
      return;
    }
    const selected = files ? Array.from(files) : [];
    if (!selected.length) { replacementId.current = null; return; }
    if (replacementId.current && selected.length !== 1) { setFileImportError("更换原件请只选择一个文件；原队列仍保留。"); replacementId.current = null; return; }
    const selectedExam = exams.find((exam) => exam.id === selectedExamId);
    const batch = captureBatches.find((item) => item.id === captureBatchId.trim());
    // 已扫描文件不依赖本机 WIA 设备；只使用真实考试/可用批次、模板采样参数和文件检查放行。
    if (!selectedExam || batch?.exam_id !== selectedExamId || ["completed", "cancelled"].includes(batch.status)) {
      setFileImportError("请选择已加载的真实考试和该考试可继续上传的采集批次，再导入文件。"); return;
    }
    if (scanContext?.batch.id !== batch.id || scanContext.batch.exam_id !== selectedExamId || scanContext.template_status !== "ready" || !scanContext.template?.id) {
      setFileImportError("当前批次尚无已核实的答题卡模板。请重新读取模板；未绑定时请学校管理员为本次考试绑定已锁定模板。"); return;
    }
    if (!["A3", "A4", "A5", "Letter", "Legal"].includes(fileImportTemplate.paperSize)
      || !Number.isInteger(fileImportTemplate.dpi) || fileImportTemplate.dpi < 150 || fileImportTemplate.dpi > 1200) {
      setFileImportError("请确认实际纸张和 150 到 1200 DPI 的扫描清晰度。"); return;
    }
    const sampleProfile: ScannerProfile = { ...fileImportTemplate, templatePreset: scanContext.template.id,
      id: "file-import", name: "已有文件导入", deviceFingerprint: "", colorMode: "grayscale", autoRotate: false, compression: "pdf", createdAt: "", updatedAt: "" };
    const submissionId = scanSubmissionId.trim();
    const startPage = scanStartPage || 1;
    return { selected, selectedExam, batch, template: scanContext.template, sampleProfile, submissionId, startPage };
  };
  type PreparedFileSelection = NonNullable<ReturnType<typeof prepareFileSelection>>;

  const spoolSelectedScanFile = async (file: File, index: number, context: PreparedFileSelection, qualityChecks: NonNullable<SyncQueueItem["qualityChecks"]>, nextItems: SyncQueueItem[], results: FileImportResult[]): Promise<FileImportStep> => {
    const { selectedExam, batch, submissionId, startPage } = context;
    const failedQuality = qualityChecks.some((check) => check.status === "failed");
    try {
      // 原件写盘成功后才保留文件句柄和队列项；写盘失败不能移出旧件。
      const durableItem = await spoolScanAsset({
        file,
        examId: selectedExamId || undefined,
        captureBatchId: captureBatchId.trim() || undefined,
        examName: selectedExam.name, captureBatchName: batch.name, captureBatchCreatedAt: batch.created_at,
        submissionId: submissionId || undefined,
        pageNo: startPage + index,
        qualityChecks
      }, durableScopeKey);
      if (scopeRef.current !== durableScopeKey) return "stop";
      fileBufferRef.current.set(durableItem.id, file);
      nextItems.push({
        ...durableItem,
        examName: selectedExam.name,
        captureBatchName: batch.name, captureBatchCreatedAt: batch.created_at,
        status: failedQuality ? "failed" : durableItem.status,
        detail: failedQuality ? "本地质量检查未通过，原件已安全保留" : durableItem.detail,
        previewUrl: previewUrlForFile(file),
        qualityChecks
      });
      results.push({ filename: file.name, saved: true, detail: failedQuality ? "原件已保留，质量检查失败；请修复后更换" : "已加密保留并加入队列" });
    } catch (error) {
      if (scopeRef.current !== durableScopeKey) return "stop";
      await logEvent("error", "scan asset durable spool failed", error instanceof Error ? error.message : String(error));
      setDiagnosticError(getUserErrorMessage(error, "扫描原件无法写入耐久本地存储，已停止加入上传队列"));
      setFileImportError(getUserErrorMessage(error, "部分原件无法写入本机加密存储；已入队文件保留，请检查磁盘后重选失败文件。"));
      results.push({ filename: file.name, saved: false, detail: getUserErrorMessage(error, "写入本机失败，请检查磁盘、账号授权后重选该文件") });
      setImportResults([...results]);
    }
    return "continue";
  };

  const queueBrowserScanFile = (file: File, index: number, context: PreparedFileSelection, qualityChecks: NonNullable<SyncQueueItem["qualityChecks"]>, nextItems: SyncQueueItem[], results: FileImportResult[]) => {
    const { selectedExam, batch, submissionId, startPage } = context;
    const failedQuality = qualityChecks.some((check) => check.status === "failed");
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
      return;
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
      captureBatchId: captureBatchId.trim(),
      examName: selectedExam?.name,
      captureBatchName: batch.name, captureBatchCreatedAt: batch.created_at,
      submissionId: submissionId || undefined,
      pageNo: startPage + index,
      fileName: file.name,
      fileSize: file.size,
      contentType: file.type || inferContentType(file.name),
      previewUrl: previewUrlForFile(file),
      requiresReselect: false,
      qualityChecks
    });
    results.push({ filename: file.name, saved: true, detail: "已加入开发队列；浏览器重启需重选原件" });
  };

  const processSelectedScanFile = async (file: File, index: number, context: PreparedFileSelection, nextItems: SyncQueueItem[], results: FileImportResult[]): Promise<FileImportStep> => {
    if (hasDurableDesktopStore() && scopeRef.current !== durableScopeKey) return "stop";
    let qualityChecks: NonNullable<SyncQueueItem["qualityChecks"]>;
    try {
      const scannerChecks = isTauriRuntime() ? await inspectScannerSample(file, context.sampleProfile) : [];
      qualityChecks = [...await inspectScanFile(file), ...scannerChecks,
        { key: "template_preset", label: "答题卡模板", status: "not_configured" as const, detail: `已选 ${context.template.name} 第 ${context.template.version_no} 版；原件是否匹配仍由服务端上传后核对。` }];
    } catch (error) {
      if (scopeRef.current !== durableScopeKey) return "stop";
      results.push({ filename: file.name, saved: false, detail: getUserErrorMessage(error, "原件检查失败，请核对格式并重新选择该文件") });
      setImportResults([...results]);
      setFileImportError("部分文件检查失败；已入队文件保留，请核对失败文件后重新选择。");
      return "continue";
    }
    if (hasDurableDesktopStore() && scopeRef.current !== durableScopeKey) return "stop";
    if (hasDurableDesktopStore()) return spoolSelectedScanFile(file, index, context, qualityChecks, nextItems, results);
    queueBrowserScanFile(file, index, context, qualityChecks, nextItems, results);
    return "continue";
  };

  const handleFileSelection = async (files: FileList | null) => {
    setFileImportError(null);
    setImportResults([]);
    const context = prepareFileSelection(files);
    if (!context) return;
    const nextItems: SyncQueueItem[] = [];
    const results: FileImportResult[] = [];
    for (const [index, file] of context.selected.entries()) {
      // 耐久队列模式下，账户切换终止整次导入；单个文件失败只跳过该文件。
      if (await processSelectedScanFile(file, index, context, nextItems, results) === "stop") return;
    }
    if (hasDurableDesktopStore() && scopeRef.current !== durableScopeKey) return;
    if (nextItems.length) updateQueue((current) => {
      const nextIDs = new Set(nextItems.map((item) => item.id));
      return [...nextItems, ...current.filter((item) => !nextIDs.has(item.id))];
    });
    if (replacementId.current && nextItems.length) {
      const oldId = replacementId.current; replacementId.current = null;
      // 新原件已入队后才尝试移出旧条目；移出失败时仍保留原队列记录。
      if (!nextItems.some((item) => item.id === oldId)) await removeQueueItem(oldId, true);
      else setFileImportError("所选文件与已有原件相同，原队列已保留；如需修复请更换修正后的文件。");
    }
    // 取消或导入失败后结束本次更换，避免下一次普通批量选择意外移出旧原件。
    replacementId.current = null;
    setImportResults(results);
    await logEvent("info", "scan files queued", `${context.selected.length} files`);
  };

  const removableItem = (id: string) => queueRef.current.find((item) => item.id === id && !item.remoteUploadId && !item.fileAssetId && item.status !== "uploading" && item.status !== "succeeded" && !uploadInFlightRef.current.has(id));
  const removeQueueItem = async (id: string, replacement = false) => {
    const scope = scopeRef.current;
    const item = removableItem(id); if (!item) { setFileImportError("已启动上传的文件不能移出或改属，请核对服务端状态。"); return; }
    const accepted = replacement || await new Promise<boolean>((resolve) => Modal.confirm({ title: `移出 ${item.fileName ?? item.title}？`, content: "该文件将停止等待上传；本机加密原件继续留存，不删除磁盘文件。更换考试或批次时请重新导入并核对归属。", okText: "确认移出队列", cancelText: "保留", onOk: () => resolve(true), onCancel: () => resolve(false) }));
    if (!accepted || scopeRef.current !== scope || !removableItem(id)) return;
    try {
      await durablePersistenceRef.current;
      if (hasDurableDesktopStore()) await removeUnstartedScanQueueItem(item.localAssetId ?? id, scope);
      if (scopeRef.current !== scope) return;
      if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
      fileBufferRef.current.delete(id); updateQueue((items) => items.filter((entry) => entry.id !== id));
    } catch (error) { setFileImportError(getUserErrorMessage(error, "移出失败，原件与队列已保留。")); }
  };
  const chooseReplacement = (id: string) => {
    const item = removableItem(id);
    if (item?.examId !== selectedExamId || item.captureBatchId !== captureBatchId.trim()) { setFileImportError("请先切换到该文件所属考试与批次，再更换原件。已启动上传的原件不能更换。"); return; }
    replacementId.current = id; fileInputRef.current?.click();
  };
  const reinspectQueueItem = async (id: string) => {
    const item = removableItem(id); const file = fileBufferRef.current.get(id);
    if (!item || !(file instanceof File)) { setFileImportError("请重新选择原件进行检查；已启动上传的文件不能改写。"); return; }
    if (item.examId !== selectedExamId || item.captureBatchId !== captureBatchId.trim() || scanContext?.batch.id !== item.captureBatchId || scanContext.template_status !== "ready" || !scanContext.template) { setFileImportError("请切换到该文件所属考试和批次，并重新读取已锁定模板后检查。"); return; }
    const scope = scopeRef.current;
    // 重新检查也保留模板采样门禁；不能只跑扩展名检查就把原来的模糊、裁边或 DPI 失败清掉。
    const profile: ScannerProfile = { ...fileImportTemplate, templatePreset: scanContext.template.id, id: "file-import", name: "已有文件导入", deviceFingerprint: "", colorMode: "grayscale", autoRotate: false, compression: "pdf", createdAt: "", updatedAt: "" };
    const checks = [...await inspectScanFile(file), ...(isTauriRuntime() ? await inspectScannerSample(file, profile) : [])];
    if (scopeRef.current !== scope || !removableItem(id)) return;
    updateQueue((items) => items.map((entry) => entry.id === id ? { ...entry, qualityChecks: checks, status: checks.some((check) => check.status === "failed") ? "failed" : "pending", detail: "已重新进行文件检查；请核对检查结果后主动上传" } : entry));
  };

  const clearSucceededQueueItems = async () => {
    const scope = scopeRef.current;
    if (hasDurableDesktopStore() && !scope) return;
    const succeededIds = queueRef.current
      .filter((item) => item.kind === "scan_upload" && item.status === "succeeded")
      .map((item) => item.localAssetId ?? item.id);
    if (hasDurableDesktopStore() && succeededIds.length) {
      try {
        await archiveDurableScanQueueItems(succeededIds, scope);
        if (scopeRef.current !== scope) return;
      } catch (error) {
        if (scopeRef.current !== scope) return;
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
    handleFileSelection, clearSucceededQueueItems, fileImportError, importResults, removeQueueItem, reinspectQueueItem, chooseReplacement,
    queueRestoreStatus, queueRestoreError, restoreQueue, queueContextError, loadingQueueContext, refreshQueueContext
  };
}
