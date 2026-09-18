import { useCallback, useEffect, useRef, useState } from "react";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import {
  Alert,
  Button,
  Checkbox,
  ConfigProvider,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  type TableColumnsType
} from "antd";
import zhCN from "antd/locale/zh_CN";
import {
  BookOpenCheck,
  CloudUpload,
  FileUp,
  HardDrive,
  ListChecks,
  LogIn,
  RefreshCw,
  RotateCcw,
  ScrollText,
  ServerCog,
  Settings,
  ShieldAlert,
  Stethoscope,
  UploadCloud,
  Wifi
} from "lucide-react";
import { motion } from "framer-motion";
import { getSafeUserText, getUserErrorMessage } from "./api/userError";
import { listCaptureBatches, listExams, type CaptureBatch } from "./api/exams";
import { listReviewTasks } from "./api/review";
import { runSubmissionQualityCheck } from "./api/submissions";
import { resumeCaptureUpload, type CaptureUploadSource } from "./api/captureUploads";
import { OfflineWorkbench } from "./components/OfflineWorkbench";
import { CapabilityTag, QueueList, ScanQueueTable, SectionHead, StatusLine } from "./components/DesktopStatusViews";
import {
  isTauriRuntime,
} from "./lib/localRuntime";
import { listOfflineDraftEnvelopes, readOfflineDraftEnvelopes } from "./lib/offlineStore";
import {
  archiveDurableScanQueueItems,
  hasDurableDesktopStore,
  listDurableScanQueue,
  loadDurableSpoolFile,
  persistDurableScanQueueItem,
  spoolScanAsset
} from "./lib/durableStore";
import { transitionScanQueueItem, updateScanQueueItem } from "./lib/scanQueueWorkflow";
import { applyUploadState, uploadStateFromQueueItem } from "./features/upload/uploadState";
import {
  dependencyLabel,
  dependencyTone,
  estimateLocalCacheBytes,
  formatBytes,
  formatDate,
  inferContentType,
  inspectScanFile,
  persistScanQueue,
  previewUrlForFile,
  readPersistedScanQueue
} from "./lib/scanFiles";
import {
  inspectScannerSample,
  listScannerDevices,
  listScannerProfiles,
  runScannerPreflight,
  saveScannerProfile,
  scannerIntegrationStatus,
  type ScannerDevice,
  type ScannerIntegrationStatus,
  type ScannerPreflightResult,
  type ScannerProfile
} from "./lib/scannerProfile";
import type {
  Exam,
  ReviewTask,
  SubmissionQualityResult,
  SyncQueueItem,
  WorkspaceKey
} from "./types";
import { genericStatusLabel, subjectLabels } from "./statusLabels";
import { useDesktopSession } from "./features/session/useDesktopSession";
import { useLocalLogs } from "./features/runtime/useLocalLogs";
import { useRuntimeDiagnostics } from "./features/runtime/useRuntimeDiagnostics";

const defaultServer = "http://127.0.0.1:8080";

const navItems: { key: WorkspaceKey; label: string; icon: React.ReactNode }[] = [
  { key: "connect", label: "连接登录", icon: <LogIn size={18} /> },
  { key: "tasks", label: "任务列表", icon: <ListChecks size={18} /> },
  { key: "scan", label: "扫描上传", icon: <FileUp size={18} /> },
  { key: "offline", label: "离线阅卷", icon: <BookOpenCheck size={18} /> },
  { key: "sync", label: "同步队列", icon: <CloudUpload size={18} /> },
  { key: "diagnostics", label: "系统诊断", icon: <Stethoscope size={18} /> },
  { key: "logs", label: "本地日志", icon: <ScrollText size={18} /> }
];

const sourceLabels: Record<string, string> = {
  ai_low_confidence: "AI 低置信",
  ocr_low_confidence: "OCR 低置信",
  subjective_default_review: "主观题复核",
  evidence_verification_failed: "证据校验失败",
  double_mark_required: "双评任务",
  score_anomaly: "分数异常",
  manual_sample: "人工抽检"
};

function App() {
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const [workspace, setWorkspace] = useState<WorkspaceKey>("connect");
  const { logs, logEvent, clearLogs } = useLocalLogs();
  const {
    client, serverUrl, setServerUrl, tenantCode, setTenantCode, username, setUsername,
    password, setPassword, rememberLogin, setRememberLogin, credentialStoreMessage,
    credentialStoreReady, token, expiresAt, user, authError, isLoggingIn,
    handleLogin, handleForgetStoredLogin, handleCheckSession
  } = useDesktopSession(defaultServer, logEvent);
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [taskError, setTaskError] = useState<string | null>(null);
  const [isLoadingTasks, setIsLoadingTasks] = useState(false);
  const {
    capabilities, diagnostics, localCacheSecurity, serviceStatus, isCheckingServiceStatus,
    diagnosticError, setDiagnosticError, refreshCapabilities, saveServerForSession,
    handleHealthCheck, handleSystemStatusCheck
  } = useRuntimeDiagnostics(client, serverUrl, setServerUrl, logEvent);
  const [queue, setQueue] = useState<SyncQueueItem[]>(() => (hasDurableDesktopStore() ? [] : readPersistedScanQueue()));
  const [offlineDraftCount, setOfflineDraftCount] = useState(() => readOfflineDraftEnvelopes().length);
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState("");
  const [examError, setExamError] = useState<string | null>(null);
  const [isLoadingExams, setIsLoadingExams] = useState(false);
  const [scanSubmissionId, setScanSubmissionId] = useState("");
  const [captureBatchId, setCaptureBatchId] = useState("");
  const [captureBatches, setCaptureBatches] = useState<CaptureBatch[]>([]);
  const [isLoadingCaptureBatches, setIsLoadingCaptureBatches] = useState(false);
  const [scanStartPage, setScanStartPage] = useState(1);
  const [scannerProfiles, setScannerProfiles] = useState<ScannerProfile[]>([]);
  const [selectedScannerProfileId, setSelectedScannerProfileId] = useState("");
  const [scannerPreflight, setScannerPreflight] = useState<ScannerPreflightResult | null>(null);
  const [scannerIntegration, setScannerIntegration] = useState<ScannerIntegrationStatus | null>(null);
  const [scannerPreflightError, setScannerPreflightError] = useState<string | null>(null);
  const [isCheckingScannerPreflight, setIsCheckingScannerPreflight] = useState(false);
  const [expectedPaperSize, setExpectedPaperSize] = useState<ScannerProfile["paperSize"]>("A4");
  const [expectedTemplatePreset, setExpectedTemplatePreset] = useState("");
  const [expectedDuplex, setExpectedDuplex] = useState(false);
  const [expectedDpi, setExpectedDpi] = useState<number>(300);
  const [isScannerProfileModalOpen, setIsScannerProfileModalOpen] = useState(false);
  const [scannerDevices, setScannerDevices] = useState<ScannerDevice[]>([]);
  const [isLoadingScannerDevices, setIsLoadingScannerDevices] = useState(false);
  const [isSavingScannerProfile, setIsSavingScannerProfile] = useState(false);
  const [scannerProfileName, setScannerProfileName] = useState("");
  const [scannerDeviceFingerprint, setScannerDeviceFingerprint] = useState("");
  const [isOnline, setIsOnline] = useState(() => navigator.onLine);
  const [qualityResult, setQualityResult] = useState<SubmissionQualityResult | null>(null);
  const [qualityError, setQualityError] = useState<string | null>(null);
  const [isCheckingQuality, setIsCheckingQuality] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const fileBufferRef = useRef(new Map<string, File | CaptureUploadSource>());
  const uploadInFlightRef = useRef(new Set<string>());
  const queueRef = useRef(queue);
  const durablePersistenceRef = useRef<Promise<void>>(Promise.resolve());
  const captureBatchLoadRef = useRef(0);

  const updateQueue = useCallback((updater: (current: SyncQueueItem[]) => SyncQueueItem[]) => {
    const next = updater(queueRef.current);
    queueRef.current = next;
    setQueue(next);
    if (hasDurableDesktopStore()) {
      durablePersistenceRef.current = durablePersistenceRef.current
        .catch(() => undefined)
        .then(() => Promise.all(next.filter((item) => item.kind === "scan_upload").map((item) => persistDurableScanQueueItem(item))))
        .then(() => undefined)
        .catch((error) => console.warn("durable scan queue persistence failed", error));
    } else {
      persistScanQueue(next);
    }
  }, []);

  useEffect(() => {
    if (hasDurableDesktopStore()) {
      void listDurableScanQueue()
        .then((items) => {
          queueRef.current = items;
          setQueue(items);
        })
        .catch((error) => setDiagnosticError(getUserErrorMessage(error, "本地耐久队列无法恢复")));
    }
    void listOfflineDraftEnvelopes().then((drafts) => setOfflineDraftCount(drafts.length)).catch(() => undefined);
  }, []);

  useEffect(() => {
    queueRef.current = queue;
  }, [queue]);

  useEffect(() => {
    if (workspace === "scan" && isTauriRuntime()) {
      void handleLoadScannerProfiles();
    }
  }, [workspace]);

  // `handleLoadExams` may select the first accessible exam automatically.
  // Load its real, resumable capture batches as well, so the operator never
  // has to discover or copy an internal UUID in the normal scan workflow.
  useEffect(() => {
    if (workspace === "scan" && token && selectedExamId) {
      void handleLoadCaptureBatches(selectedExamId);
    }
  }, [workspace, token, selectedExamId]);

  useEffect(() => () => {
    for (const item of queueRef.current) {
      if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    }
    fileBufferRef.current.clear();
    uploadInFlightRef.current.clear();
  }, []);

  const handleLoadTasks = async () => {
    setTaskError(null);
    setIsLoadingTasks(true);
    try {
      const result = await listReviewTasks(client, {});
      setTasks(result.tasks);
      await logEvent("info", "review tasks loaded", `${result.tasks.length} tasks`);
    } catch (error) {
      const message = getUserErrorMessage(error, "任务列表读取失败");
      setTaskError(message);
      setTasks([]);
      await logEvent("warning", "review task load failed", message);
    } finally {
      setIsLoadingTasks(false);
    }
  };

  const handleLoadExams = async () => {
    setExamError(null);
    setIsLoadingExams(true);
    try {
      const result = await listExams(client, {});
      setExams(result.exams);
      if (!selectedExamId && result.exams[0]) {
        setSelectedExamId(result.exams[0].id);
      }
      await logEvent("info", "exam list loaded for scan workstation", `${result.exams.length} exams`);
    } catch (error) {
      const message = getUserErrorMessage(error, "考试列表读取失败");
      setExamError(message);
      setExams([]);
      await logEvent("warning", "exam list load failed", message);
    } finally {
      setIsLoadingExams(false);
    }
  };

  const handleLoadCaptureBatches = async (examID = selectedExamId) => {
    const requestID = ++captureBatchLoadRef.current;
    if (!examID) {
      setCaptureBatches([]);
      return;
    }
    setIsLoadingCaptureBatches(true);
    try {
      const result = await listCaptureBatches(client, examID);
      if (requestID !== captureBatchLoadRef.current) return;
      const usable = result.batches.filter((batch) => batch.status !== "completed" && batch.status !== "cancelled");
      setCaptureBatches(usable);
      if (usable.some((batch) => batch.id === captureBatchId)) {
        return;
      }
      setCaptureBatchId(usable[0]?.id ?? "");
      await logEvent("info", "capture batches loaded for scan workstation", `${usable.length} usable batches`);
    } catch (error) {
      if (requestID !== captureBatchLoadRef.current) return;
      const message = getUserErrorMessage(error, "采集批次读取失败");
      setCaptureBatches([]);
      setExamError(message);
      await logEvent("warning", "capture batch list load failed", message);
    } finally {
      if (requestID === captureBatchLoadRef.current) {
        setIsLoadingCaptureBatches(false);
      }
    }
  };

  const handleLoadScannerProfiles = async () => {
    if (!isTauriRuntime()) return;
    try {
      const [profiles, integration] = await Promise.all([listScannerProfiles(), scannerIntegrationStatus()]);
      setScannerProfiles(profiles);
      setScannerIntegration(integration);
      if (!selectedScannerProfileId && profiles[0]) {
        setSelectedScannerProfileId(profiles[0].id);
      }
    } catch (error) {
      setScannerPreflightError(getUserErrorMessage(error, "扫描设备配置无法读取"));
    }
  };

  const handleOpenScannerProfileSetup = async () => {
    if (!isTauriRuntime()) return;
    setScannerPreflightError(null);
    setScannerProfileName(scannerProfileName || `扫描站 ${expectedPaperSize} ${expectedDpi} DPI`);
    setIsLoadingScannerDevices(true);
    try {
      const devices = await listScannerDevices();
      setScannerDevices(devices);
      if (!scannerDeviceFingerprint && devices[0]) {
        setScannerDeviceFingerprint(devices[0].fingerprint);
      }
      setIsScannerProfileModalOpen(true);
    } catch (error) {
      const message = getUserErrorMessage(error, "无法读取 Windows 扫描设备。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner device inventory failed", message);
    } finally {
      setIsLoadingScannerDevices(false);
    }
  };

  const handleSaveScannerProfile = async () => {
    const name = scannerProfileName.trim();
    const templatePreset = expectedTemplatePreset.trim();
    if (!name || !scannerDeviceFingerprint || !templatePreset) {
      setScannerPreflightError("请填写档案名称，选择可用扫描设备，并填写锁定答题卡模板标识。");
      return;
    }
    setIsSavingScannerProfile(true);
    setScannerPreflightError(null);
    try {
      const profile = await saveScannerProfile({
        name,
        deviceFingerprint: scannerDeviceFingerprint,
        dpi: expectedDpi,
        duplex: expectedDuplex,
        colorMode: "grayscale",
        paperSize: expectedPaperSize,
        autoRotate: true,
        compression: "jpeg",
        templatePreset
      });
      setScannerProfiles((current) => [profile, ...current.filter((item) => item.id !== profile.id)]);
      setSelectedScannerProfileId(profile.id);
      setScannerPreflight(null);
      setIsScannerProfileModalOpen(false);
      await logEvent("info", "scanner profile saved", profile.name);
    } catch (error) {
      const message = getUserErrorMessage(error, "扫描设备档案保存失败。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner profile save failed", message);
    } finally {
      setIsSavingScannerProfile(false);
    }
  };

  const handleScannerPreflight = async () => {
    if (!isTauriRuntime()) return;
    if (!selectedScannerProfileId || !expectedTemplatePreset.trim()) {
      setScannerPreflightError("请选择扫描设备 Profile，并填写锁定答题卡模板的 Profile 标识。");
      return;
    }
    setScannerPreflightError(null);
    setIsCheckingScannerPreflight(true);
    try {
      const result = await runScannerPreflight({
        profileId: selectedScannerProfileId,
        expectedPaperSize,
        expectedTemplatePreset: expectedTemplatePreset.trim(),
        expectedDuplex,
        expectedDpi,
        networkAvailable: isOnline
      });
      setScannerPreflight(result);
      await logEvent("info", "scanner preflight completed", result.readyToScan ? "ready" : "blocked");
    } catch (error) {
      const message = getUserErrorMessage(error, "扫描前检查失败");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner preflight failed", message);
    } finally {
      setIsCheckingScannerPreflight(false);
    }
  };

  const handleFileSelection = async (files: FileList | null) => {
    const selected = files ? Array.from(files) : [];
    if (!selected.length) {
      return;
    }
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
      const qualityChecks = [
        ...(await inspectScanFile(file)),
        ...scannerChecks
      ];
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
      const recovered = queueRef.current.find(
        (item) => item.requiresReselect && item.fileName === file.name && item.fileSize === file.size && item.kind === "scan_upload"
      );
      if (recovered) {
        if (recovered.previewUrl) URL.revokeObjectURL(recovered.previewUrl);
        fileBufferRef.current.set(recovered.id, file);
        updateQueue((current) =>
          current.map((item) =>
            item.id === recovered.id
              ? {
                  ...item,
                  status: failedQuality ? "failed" : "pending",
                  progress: 0,
                  detail: failedQuality ? "本地质量检查未通过" : "文件句柄已恢复，等待上传",
                  requiresReselect: false,
                  qualityChecks,
                  previewUrl: previewUrlForFile(file),
                  updatedAt: new Date().toISOString()
                }
              : item
          )
        );
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
    if (nextItems.length) {
      updateQueue((current) => [...nextItems, ...current]);
    }
    await logEvent("info", "scan files queued", `${selected.length} files`);
  };

  const uploadQueueItem = useCallback(
    async (id: string) => {
      const item = queueRef.current.find((candidate) => candidate.id === id);
      const uploadState = item ? uploadStateFromQueueItem(item) : undefined;
      if (
        !item ||
        item.kind !== "scan_upload" ||
        uploadState?.status === "succeeded" ||
        uploadState?.status === "uploading" ||
        uploadInFlightRef.current.has(id)
      ) {
        return;
      }
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
          status: "failed", requiresReselect: true,
          detail: "本地队列已恢复，但文件句柄不可用；请重新选择同名文件后重试"
        }));
        return;
      }
      uploadInFlightRef.current.add(id);
      updateQueue((current) => updateScanQueueItem(current, id, {
        status: "uploading", progress: Math.max(item.progress, 1), detail: "正在上传到后端文件 API"
      }));
      try {
        const completed = await resumeCaptureUpload(
          client,
          {
            file,
            exam: item.examId,
            batch: item.captureBatchId,
            remoteUploadId: item.remoteUploadId,
            idempotency_key: item.idempotencyKey
          },
          async (progress) => {
            const currentItem = queueRef.current.find((candidate) => candidate.id === id);
            if (!currentItem) {
              throw new Error("本地耐久队列记录已丢失，已停止继续上传");
            }
            const nextItem: SyncQueueItem = transitionScanQueueItem(currentItem, {
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
          }
        );
        if (completed.status !== "completed") {
          updateQueue((current) => updateScanQueueItem(current, id, {
            status: "pending",
            detail: `服务端状态为${genericStatusLabel(completed.status)}，将在下次同步继续确认`
          }));
          return;
        }
        updateQueue((current) => updateScanQueueItem(current, id, {
          status: "succeeded", progress: 100,
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
    },
    [client, isOnline, logEvent, token, updateQueue]
  );

  const uploadQueueItems = useCallback(
    async (mode: "pending" | "failed" | "all") => {
      const candidates = queueRef.current.filter((item) => {
        if (item.kind !== "scan_upload") {
          return false;
        }
        if (mode === "all") {
          return item.status === "pending" || item.status === "failed";
        }
        return item.status === mode;
      });
      for (const item of candidates) {
        await uploadQueueItem(item.id);
      }
    },
    [uploadQueueItem]
  );

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

  const handleRunQualityCheck = async () => {
    const submissionId = scanSubmissionId.trim();
    if (!submissionId) {
      setQualityError("缺少 submission_id，无法运行服务端质量门禁");
      return;
    }
    setQualityError(null);
    setIsCheckingQuality(true);
    try {
      const result = await runSubmissionQualityCheck(client, submissionId);
      setQualityResult(result.result);
      await logEvent("info", "submission quality check completed", JSON.stringify(result.result));
    } catch (error) {
      const message = getUserErrorMessage(error, "服务端质量门禁失败");
      setQualityError(message);
      await logEvent("warning", "submission quality check failed", message);
    } finally {
      setIsCheckingQuality(false);
    }
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

  const activeCapabilityWarnings = capabilities.filter((item) => item.status !== "ready");

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
          token: {
            fontSize: readingSize === "large" ? 16 : 14,
            fontSizeSM: readingSize === "large" ? 14 : 13,
            controlHeight: readingSize === "large" ? 44 : 40,
            controlHeightSM: 36,
            lineHeight: 1.6,
          colorPrimary: "#1677ff",
          colorSuccess: "#52c41a",
          colorWarning: "#faad14",
          colorError: "#ff4d4f",
          colorInfo: "#13c2c2",
          borderRadius: 6,
          fontFamily: 'Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
        }
      }}
    >
      <div className="desktop-shell">
        <aside className="desktop-sidebar">
          <div className="desktop-brand">
            <div className="desktop-brand-mark">E</div>
            <div>
              <h1>EduGrade EXE</h1>
              <p>扫描与离线工作站</p>
            </div>
          </div>
          <nav className="desktop-nav" aria-label="桌面工作区">
            {navItems.map((item) => (
              <button
                className={workspace === item.key ? "active" : ""}
                key={item.key}
                type="button"
                onClick={() => setWorkspace(item.key)}
              >
                {item.icon}
                <span>{item.label}</span>
              </button>
            ))}
          </nav>
          <div className="desktop-sidebar-footer">
            <StatusLine label="后端" value={serverUrl} tone={token ? "ready" : "idle"} />
            <StatusLine label="安全存储" value={credentialStoreReady ? "Windows 凭据库" : "不可用"} tone={credentialStoreReady ? "ready" : "warning"} />
          </div>
        </aside>

        <main className="desktop-main">
          <header className="desktop-topbar">
            <div>
              <p className="eyebrow">Windows 桌面客户端</p>
              <h2>{navItems.find((item) => item.key === workspace)?.label}</h2>
            </div>
              <div className="topbar-actions">
                <Button aria-pressed={readingSize === "large"} onClick={() => setReadingSize((size) => size === "large" ? "standard" : "large")}>{readingSize === "large" ? "标准字号" : "大字阅读"}</Button>
              <Tag color={token ? "success" : "default"}>{token ? "已登录" : "未登录"}</Tag>
              <Tag color={activeCapabilityWarnings.length ? "warning" : "success"}>
                {activeCapabilityWarnings.length ? "存在待接入能力" : "本地能力就绪"}
              </Tag>
              <Tooltip title="刷新本地能力与运行时诊断">
                <Button icon={<RefreshCw size={16} />} onClick={refreshCapabilities} />
              </Tooltip>
            </div>
          </header>

          <motion.div
            key={workspace}
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.18 }}
            className="workspace"
          >
            {workspace === "connect" && renderConnect()}
            {workspace === "tasks" && renderTasks()}
            {workspace === "scan" && renderScan()}
            {workspace === "offline" && renderOffline()}
            {workspace === "sync" && renderSync()}
            {workspace === "diagnostics" && renderDiagnostics()}
            {workspace === "logs" && renderLogs()}
          </motion.div>
        </main>
      </div>
    </ConfigProvider>
  );

  function renderConnect() {
    return (
      <div className="workspace-grid two">
        <section className="panel">
          <SectionHead icon={<ServerCog size={20} />} title="服务端地址配置" description="当前 Story 不启用安全落盘；地址只保存到本次会话。" />
          <Form layout="vertical">
            <Form.Item label="API 服务端地址">
              <Input value={serverUrl} onChange={(event) => setServerUrl(event.target.value)} placeholder={defaultServer} />
            </Form.Item>
            <Space wrap>
              <Button type="primary" icon={<Settings size={16} />} onClick={saveServerForSession}>
                保存到当前会话
              </Button>
              <Button icon={<Wifi size={16} />} onClick={handleHealthCheck}>
                检查 /health
              </Button>
            </Space>
          </Form>
          {diagnosticError && <Alert className="section-alert" type="warning" message={diagnosticError} showIcon />}
        </section>

        <section className="panel">
          <SectionHead icon={<LogIn size={20} />} title="登录" description="访问令牌只保存在内存中；勾选保存后，密码仅写入当前 Windows 用户的系统凭据库。" />
          <Form layout="vertical">
            <Form.Item label="租户代码">
              <Input value={tenantCode} onChange={(event) => setTenantCode(event.target.value)} />
            </Form.Item>
            <Form.Item label="用户名">
              <Input value={username} onChange={(event) => setUsername(event.target.value)} />
            </Form.Item>
            <Form.Item label="密码">
              <Input.Password value={password} onChange={(event) => setPassword(event.target.value)} onPressEnter={() => void handleLogin()} />
            </Form.Item>
            <Form.Item>
              <Checkbox checked={rememberLogin} disabled={!credentialStoreReady} onChange={(event) => setRememberLogin(event.target.checked)}>
                保存到 Windows 凭据库，下次自动登录
              </Checkbox>
            </Form.Item>
            <Space wrap>
              <Button type="primary" icon={<LogIn size={16} />} loading={isLoggingIn} onClick={() => void handleLogin()}>
                登录后端
              </Button>
              <Button icon={<ShieldAlert size={16} />} disabled={!token} onClick={handleCheckSession}>
                校验 session
              </Button>
              <Button disabled={!credentialStoreReady} onClick={() => void handleForgetStoredLogin()}>
                清除已保存登录
              </Button>
            </Space>
          </Form>
          {credentialStoreMessage && <Alert className="section-alert" type={credentialStoreReady ? "info" : "warning"} message={credentialStoreMessage} showIcon />}
          {authError && <Alert className="section-alert" type="error" message={authError} showIcon />}
          {user && (
            <div className="identity-strip">
              <span>{user.display_name || user.username}</span>
              <Tag color="blue">{user.tenant_code}</Tag>
              <Tag>{user.roles.join(", ") || "无角色"}</Tag>
              {expiresAt && <span className="muted">过期：{formatDate(expiresAt)}</span>}
            </div>
          )}
        </section>
      </div>
    );
  }

  function renderTasks() {
    const columns: TableColumnsType<ReviewTask> = [
      { title: "匿名号", dataIndex: "anonymous_code", width: 130 },
      { title: "题号", dataIndex: "question_no", width: 90 },
      {
        title: "来源",
        dataIndex: "source",
        render: (value: string) => sourceLabels[value] ?? "其他来源"
      },
      {
        title: "状态",
        dataIndex: "status",
        width: 110,
        render: (value: string) => <Tag>{value}</Tag>
      },
      { title: "优先级", dataIndex: "priority", width: 90 },
      { title: "创建时间", dataIndex: "created_at", render: formatDate }
    ];
    return (
      <section className="panel full">
        <SectionHead
          icon={<ListChecks size={20} />}
          title="复核任务列表"
          description="调用真实 GET /api/v1/review-tasks；无权限或无 token 时显示后端错误，不填充假任务。"
          action={
            <Button icon={<RefreshCw size={16} />} loading={isLoadingTasks} disabled={!token} onClick={handleLoadTasks}>
              刷新任务
            </Button>
          }
        />
        {!token && <Alert type="warning" message="未登录：任务列表不会读取，也不会回退到 mock 数据。" showIcon />}
        {taskError && <Alert className="section-alert" type="error" message={taskError} showIcon />}
        <Table rowKey="id" size="middle" columns={columns} dataSource={tasks} loading={isLoadingTasks} scroll={{ x: 760 }} locale={{ emptyText: <Empty description="暂无真实任务" /> }} />
      </section>
    );
  }

  function renderScan() {
    const selectedExam = exams.find((exam) => exam.id === selectedExamId);
    const scanItems = queue.filter((item) => item.kind === "scan_upload");
    const readyCount = scanItems.filter((item) => item.status === "pending").length;
    const failedCount = scanItems.filter((item) => item.status === "failed").length;
    const scannerReady = !isTauriRuntime() || scannerPreflight?.readyToScan === true;
    return (
      <div className="workspace-grid scan-workstation">
        <section className="panel full">
          <SectionHead
            icon={<UploadCloud size={20} />}
            title="扫描工作站"
            description="先完成扫描设备预检，再把 PDF/图片写入本地加密队列；断网不影响继续采集。"
            action={<Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线，上传暂停"}</Tag>}
          />
          {isTauriRuntime() ? (
            <div className="scanner-preflight">
              <Alert
                type={scannerPreflight?.readyToScan ? "success" : "info"}
                showIcon
                message={scannerPreflight?.readyToScan ? "扫描前检查已通过" : "请先确认扫描设备与锁定答题卡模板"}
                description={scannerIntegration?.detail ?? "WIA 仅用于发现 Windows 已安装的扫描设备；直接采集需完成现场设备验证。"}
              />
              <Form layout="vertical" className="scan-context-form">
                <Form.Item label="扫描设备 Profile">
                  <Select
                    value={selectedScannerProfileId || undefined}
                    placeholder="选择已保存的扫描设备 Profile"
                    onChange={(value) => {
                      const profile = scannerProfiles.find((item) => item.id === value);
                      setSelectedScannerProfileId(value);
                      if (profile) {
                        setExpectedPaperSize(profile.paperSize);
                        setExpectedTemplatePreset(profile.templatePreset);
                        setExpectedDuplex(profile.duplex);
                        setExpectedDpi(profile.dpi);
                      }
                      setScannerPreflight(null);
                    }}
                    options={scannerProfiles.map((profile) => ({ value: profile.id, label: `${profile.name} · ${profile.dpi} DPI · ${profile.paperSize}` }))}
                  />
                </Form.Item>
                <Form.Item label="锁定答题卡模板 Profile">
                  <Input value={expectedTemplatePreset} onChange={(event) => { setExpectedTemplatePreset(event.target.value); setScannerPreflight(null); }} placeholder="填写本批答题卡模板的 Profile 标识" />
                </Form.Item>
                <Form.Item label="模板纸张">
                  <Select value={expectedPaperSize} onChange={(value) => { setExpectedPaperSize(value); setScannerPreflight(null); }} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} />
                </Form.Item>
                <Form.Item label="模板 DPI">
                  <InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => { setExpectedDpi(value ?? 300); setScannerPreflight(null); }} />
                </Form.Item>
                <Form.Item label="模板双面">
                  <Checkbox checked={expectedDuplex} onChange={(event) => { setExpectedDuplex(event.target.checked); setScannerPreflight(null); }}>双面扫描</Checkbox>
                </Form.Item>
              </Form>
              <Space wrap>
                <Button icon={<Stethoscope size={16} />} loading={isCheckingScannerPreflight} onClick={() => void handleScannerPreflight()}>运行扫描前检查</Button>
                <Button loading={isLoadingScannerDevices} onClick={() => void handleOpenScannerProfileSetup()}>新建扫描档案</Button>
                <Button icon={<RefreshCw size={16} />} onClick={() => void handleLoadScannerProfiles()}>刷新设备 Profile</Button>
              </Space>
              {scannerPreflightError && <Alert className="section-alert" type="error" showIcon message={scannerPreflightError} />}
              {scannerPreflight && (
                <div className="quality-checks">
                  {scannerPreflight.checks.map((check) => <Tooltip key={check.key} title={check.detail}><Tag color={check.status === "passed" ? "success" : check.status === "failed" ? "error" : "warning"}>{check.label}</Tag></Tooltip>)}
                </div>
              )}
            </div>
          ) : (
            <Alert type="info" showIcon message="浏览器开发模式不提供扫描设备能力；请使用 Windows 桌面扫描站完成现场采集。" />
          )}
          <Modal
            title="新建扫描档案"
            open={isScannerProfileModalOpen}
            confirmLoading={isSavingScannerProfile}
            okText="保存并使用"
            cancelText="取消"
            onCancel={() => setIsScannerProfileModalOpen(false)}
            onOk={() => void handleSaveScannerProfile()}
          >
            <p className="muted">档案只保存本机设备指纹与采集参数；不上传设备序列号，也不代表已通过真实设备验收。</p>
            <Form layout="vertical">
              <Form.Item label="档案名称" required>
                <Input value={scannerProfileName} onChange={(event) => setScannerProfileName(event.target.value)} placeholder="例如：教务处扫描站 A4 双面" />
              </Form.Item>
              <Form.Item label="Windows 已发现的扫描设备" required>
                <Select
                  value={scannerDeviceFingerprint || undefined}
                  placeholder="选择设备"
                  options={scannerDevices.map((device) => ({ value: device.fingerprint, label: `${device.displayName} / ${device.driverStatus}` }))}
                  onChange={setScannerDeviceFingerprint}
                  notFoundContent="未发现可用设备；请确认驱动、连接和 Windows WIA 服务。"
                />
              </Form.Item>
              <Form.Item label="答题卡模板标识" required>
                <Input value={expectedTemplatePreset} onChange={(event) => setExpectedTemplatePreset(event.target.value)} placeholder="与本次采集批次的模板一致" />
              </Form.Item>
              <Space wrap>
                <Form.Item label="纸张"><Select value={expectedPaperSize} onChange={setExpectedPaperSize} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} /></Form.Item>
                <Form.Item label="DPI"><InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => setExpectedDpi(value ?? 300)} /></Form.Item>
                <Form.Item label="双面"><Checkbox checked={expectedDuplex} onChange={(event) => setExpectedDuplex(event.target.checked)}>双面扫描</Checkbox></Form.Item>
              </Space>
            </Form>
          </Modal>
          <div className="scan-toolbar">
            <Form layout="vertical" className="scan-context-form">
              <Form.Item label="考试">
                <Space.Compact block>
                  <Select
                    value={selectedExamId || undefined}
                    placeholder={token ? "选择真实考试" : "登录后加载考试"}
                    loading={isLoadingExams}
                    disabled={!token}
                    onChange={(examID) => {
                      setSelectedExamId(examID);
                      setCaptureBatchId("");
                      setCaptureBatches([]);
                    }}
                    options={exams.map((exam) => ({
                      value: exam.id,
                      label: `${exam.name} / ${subjectLabels[exam.subject] ?? "其他学科"} / ${genericStatusLabel(exam.status)}`
                    }))}
                  />
                  <Button icon={<RefreshCw size={16} />} disabled={!token} loading={isLoadingExams} onClick={handleLoadExams}>
                    刷新
                  </Button>
                </Space.Compact>
              </Form.Item>
              <Form.Item label="Submission ID（可选，仅用于旧流程的人工关联）">
                <Input value={scanSubmissionId} onChange={(event) => setScanSubmissionId(event.target.value)} placeholder="无需逐份填写；服务端按采集批次处理" />
              </Form.Item>
              <Form.Item label="采集批次">
                <Space.Compact block>
                  <Select
                    value={captureBatchId || undefined}
                    placeholder={selectedExamId ? "选择可继续上传的采集批次" : "请先选择考试"}
                    loading={isLoadingCaptureBatches}
                    disabled={!selectedExamId}
                    onChange={setCaptureBatchId}
                    options={captureBatches.map((batch) => ({
                      value: batch.id,
                      label: `${batch.name} · ${genericStatusLabel(batch.status)} · ${batch.file_count} 个文件`
                    }))}
                  />
                  <Button
                    icon={<RefreshCw size={16} />}
                    disabled={!selectedExamId}
                    loading={isLoadingCaptureBatches}
                    onClick={() => void handleLoadCaptureBatches()}
                  >
                    刷新
                  </Button>
                </Space.Compact>
                <Input
                  className="scan-batch-manual-input"
                  value={captureBatchId}
                  onChange={(event) => setCaptureBatchId(event.target.value.trim())}
                  placeholder="列表不可用时可手工输入 capture batch UUID（兼容旧流程）"
                  aria-label="手工输入采集批次 UUID"
                />
              </Form.Item>
              <Form.Item label="起始页码">
                <InputNumber min={1} value={scanStartPage} onChange={(value) => setScanStartPage(value ?? 1)} />
              </Form.Item>
            </Form>
            <div className="scan-actions">
              <input
                ref={fileInputRef}
                type="file"
                multiple
                accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff"
                onChange={(event) => {
                  void handleFileSelection(event.target.files);
                  event.target.value = "";
                }}
              />
              <Button icon={<FileUp size={16} />} disabled={!selectedExamId || !captureBatchId.trim() || !scannerReady} onClick={() => fileInputRef.current?.click()}>
                批量选择 PDF/图片（含 TIFF）
              </Button>
              <Button type="primary" icon={<UploadCloud size={16} />} disabled={!token || !isOnline || readyCount === 0} onClick={() => void uploadQueueItems("pending")}>
                上传等待中的项目
              </Button>
              <Button icon={<RotateCcw size={16} />} disabled={!token || !isOnline || failedCount === 0} onClick={() => void uploadQueueItems("failed")}>
                重试失败项目
              </Button>
              <Button danger disabled={!scanItems.some((item) => item.status === "succeeded")} onClick={() => void clearSucceededQueueItems()}>
                归档已确认项
              </Button>
            </div>
          </div>
          {examError && <Alert className="section-alert" type="error" message={examError} showIcon />}
          {!token && <Alert className="section-alert" type="warning" message="未登录：不会读取考试，也不会上传文件。" showIcon />}
          {(!selectedExamId || !captureBatchId.trim()) && token && <p className="muted">请先选择真实考试和可继续上传的采集批次；客户端不会创建假考试上下文。</p>}
          {selectedExam && <p className="muted">当前考试：{selectedExam.name} / {subjectLabels[selectedExam.subject] ?? "其他学科"} / {genericStatusLabel(selectedExam.status)}</p>}
        </section>

        <section className="panel full">
          <SectionHead icon={<ListChecks size={20} />} title="上传前预览与本地质量检查" description="文件进入队列后先展示检查结果；PDF 页数与复杂分辨率检测明确预留。" />
          <ScanQueueTable items={scanItems} onRetry={(id) => void uploadQueueItem(id)} />
        </section>

        <section className="panel full">
          <SectionHead
            icon={<CloudUpload size={20} />}
            title="服务端处理状态"
            description="文件上传后显示 file_asset 和 submission page 关联结果；质量门禁调用真实 submission API。"
            action={
              <Button icon={<RefreshCw size={16} />} loading={isCheckingQuality} disabled={!token || !scanSubmissionId.trim()} onClick={handleRunQualityCheck}>
                运行服务端质量门禁
              </Button>
            }
          />
          {qualityError && <Alert type="error" message={qualityError} showIcon />}
          {qualityResult ? (
            <div className="quality-result">
              <Tag color={qualityResult.valid ? "success" : "warning"}>{qualityResult.valid ? "valid" : "issues"}</Tag>
              {qualityResult.issues.length ? (
                qualityResult.issues.map((issue) => (
                  <p key={`${issue.code}-${issue.message}`}>
                    {getSafeUserText(issue.message, "扫描质量检查未通过")}
                  </p>
                ))
              ) : (
                <p>服务端质量门禁未返回问题。</p>
              )}
            </div>
          ) : (
            <Empty description="尚未运行服务端质量门禁" />
          )}
        </section>
      </div>
    );
  }

  function renderOffline() {
    return (
      <OfflineWorkbench client={client} token={token} user={user} isOnline={isOnline} onLog={logEvent} />
    );
  }

  function renderSync() {
    const offlinePlaceholder: SyncQueueItem = {
      id: "offline-sync-not-configured",
      title: "离线阅卷同步",
      kind: "offline_grade",
      status: "not_configured",
      progress: 0,
      detail: "未配置/待接入：离线草稿同步 API 尚未实现。",
      updatedAt: new Date().toISOString()
    };
    return (
      <section className="panel full">
        <SectionHead icon={<CloudUpload size={20} />} title="同步队列" description="显示真实上传操作和未接入离线同步能力；不伪造同步成功。" />
        <QueueList items={[...queue, offlinePlaceholder]} />
      </section>
    );
  }

  function renderDiagnostics() {
    const scanItems = queue.filter((item) => item.kind === "scan_upload");
    const recentErrors = logs.filter((entry) => entry.level === "error").slice(0, 5);
    const pendingUploads = scanItems.filter((item) => item.status === "pending" || item.status === "uploading").length;
    const failedUploads = scanItems.filter((item) => item.status === "failed").length;
    const localCacheBytes = estimateLocalCacheBytes();
    return (
      <div className="workspace-grid two">
        <section className="panel">
          <SectionHead
            icon={<Stethoscope size={20} />}
            title="运行时诊断"
            description="读取 Tauri 运行时信息；浏览器开发模式会明确标注。"
            action={
              <Button icon={<RefreshCw size={16} />} onClick={refreshCapabilities}>
                刷新
              </Button>
            }
          />
          {diagnostics ? (
            <div className="diagnostic-list">
              <StatusLine label="Runtime" value={diagnostics.runtime} tone="ready" />
              <StatusLine label="Platform" value={diagnostics.platform} tone="idle" />
              <StatusLine label="Version" value={diagnostics.appVersion} tone="idle" />
              <StatusLine label="Log path" value={diagnostics.logPath ?? "未返回"} tone="idle" />
            </div>
          ) : (
            <Empty description="诊断信息读取中" />
          )}
          <Button className="section-button" icon={<Wifi size={16} />} onClick={handleHealthCheck}>
            检查后端 /health
          </Button>
          {diagnosticError && <Alert className="section-alert" type="warning" message={diagnosticError} showIcon />}
        </section>

        <section className="panel">
          <SectionHead
            icon={<ServerCog size={20} />}
            title="服务端连接"
            description="调用真实 /api/v1/system/status；依赖未配置会显示未配置/待接入。"
            action={
              <Button icon={<RefreshCw size={16} />} loading={isCheckingServiceStatus} onClick={handleSystemStatusCheck}>
                检查状态
              </Button>
            }
          />
          <div className="diagnostic-list">
            <StatusLine label="Server" value={serverUrl} tone={serviceStatus ? (serviceStatus.status === "healthy" ? "ready" : "warning") : "idle"} />
            <StatusLine label="Status" value={serviceStatus?.status ?? "未检查"} tone={serviceStatus ? (serviceStatus.status === "healthy" ? "ready" : "warning") : "idle"} />
            <StatusLine label="Service" value={serviceStatus ? `${serviceStatus.service} / ${serviceStatus.environment}` : "未返回"} tone="idle" />
            <StatusLine label="Generated" value={serviceStatus ? formatDate(serviceStatus.generated_at) : "未返回"} tone="idle" />
          </div>
          {serviceStatus && (
            <div className="dependency-chip-list">
              {serviceStatus.dependencies.map((dependency) => (
                <Tooltip key={dependency.name} title={dependency.error ?? dependency.detail ?? `${dependency.duration_ms} ms`}>
                  <Tag color={dependencyTone(dependency.status)}>
                    {dependency.name}: {dependencyLabel(dependency.status)}
                  </Tag>
                </Tooltip>
              ))}
            </div>
          )}
        </section>

        <section className="panel">
          <SectionHead icon={<ShieldAlert size={20} />} title="本地能力状态" description="未接入能力必须明确显示，不作为真实可用能力。" />
          <div className="capability-list">
            {capabilities.map((capability) => (
              <div className="capability-row" key={capability.key}>
                <div>
                  <strong>{capability.name}</strong>
                  <p>{capability.detail}</p>
                </div>
                <CapabilityTag status={capability.status} />
              </div>
            ))}
          </div>
        </section>

        <section className="panel">
          <SectionHead icon={<HardDrive size={20} />} title="本地缓存与上传队列" description="基于当前客户端本地缓存和真实上传队列统计。" />
          <div className="diagnostic-summary-grid">
            <div>
              <span>缓存大小</span>
              <strong>{formatBytes(localCacheBytes)}</strong>
              <small>localStorage 估算</small>
            </div>
            <div>
              <span>离线草稿</span>
              <strong>{offlineDraftCount}</strong>
              <small>{hasDurableDesktopStore() ? "加密 SQLite 草稿" : "浏览器开发草稿"}</small>
            </div>
            <div>
              <span>待上传</span>
              <strong>{pendingUploads}</strong>
              <small>等待上传/上传中</small>
            </div>
            <div>
              <span>失败队列</span>
              <strong>{failedUploads}</strong>
              <small>需人工处理</small>
            </div>
          </div>
          <div className="local-cache-security">
            <StatusLine
              label="缓存安全"
              value={localCacheSecurity.status === "passed" ? "未发现敏感缓存" : `${localCacheSecurity.issues.length} 个风险`}
              tone={localCacheSecurity.status === "passed" ? "ready" : "warning"}
            />
            <p>
              已扫描 {localCacheSecurity.scannedKeys} 个本地缓存键，最近检查 {formatDate(localCacheSecurity.checkedAt)}
            </p>
            {localCacheSecurity.issues.length ? (
              <div className="dependency-chip-list">
                {localCacheSecurity.issues.map((issue) => (
                  <Tooltip key={`${issue.key}-${issue.message}`} title={issue.message}>
                    <Tag color={issue.severity === "critical" ? "error" : "warning"}>{issue.key}</Tag>
                  </Tooltip>
                ))}
              </div>
            ) : null}
          </div>
        </section>

        <section className="panel full">
          <SectionHead icon={<ScrollText size={20} />} title="最近错误日志" description="只展示本客户端本地 error 级别日志；不是后端 audit_log。" />
          <div className="log-list">
            {recentErrors.length ? (
              recentErrors.map((entry) => (
                <div className="log-row" key={entry.id}>
                  <Tag color="error">{entry.level}</Tag>
                  <span>{formatDate(entry.at)}</span>
                  <strong>{entry.message}</strong>
                  {entry.context && <p>{entry.context}</p>}
                </div>
              ))
            ) : (
              <Empty description="暂无 error 日志" />
            )}
          </div>
        </section>
      </div>
    );
  }

  function renderLogs() {
    return (
      <section className="panel full">
        <SectionHead
          icon={<ScrollText size={20} />}
          title="本地日志"
          description="记录客户端侧关键操作；这不是后端 audit_log。"
          action={
            <Button
              danger
              onClick={() => void clearLogs()
                .catch((error) => setDiagnosticError(getUserErrorMessage(error, "本地日志清除失败")))}
            >
              清空本地日志
            </Button>
          }
        />
        <div className="log-list">
          {logs.length ? (
            logs.map((entry) => (
              <div className="log-row" key={entry.id}>
                <Tag color={entry.level === "error" ? "error" : entry.level === "warning" ? "warning" : "blue"}>{entry.level}</Tag>
                <span>{formatDate(entry.at)}</span>
                <strong>{entry.message}</strong>
                {entry.context && <p>{entry.context}</p>}
              </div>
            ))
          ) : (
            <Empty description="暂无本地日志" />
          )}
        </div>
      </section>
    );
  }
}

export default App;
