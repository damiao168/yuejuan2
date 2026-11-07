import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import { subscribeBeforeUnload } from "@edugrade/sdk";
import { BookOpenCheck, CloudUpload, FileUp, ListChecks, LogIn, ScrollText, Stethoscope } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { getUserErrorMessage } from "./api/userError";
import { type OfflineLeaveGuard } from "./components/OfflineWorkbench";
import { useExamCaptureContext } from "./features/capture/useExamCaptureContext";
import { useSubmissionQuality } from "./features/quality/useSubmissionQuality";
import { useLocalLogs } from "./features/runtime/useLocalLogs";
import { useRuntimeDiagnostics } from "./features/runtime/useRuntimeDiagnostics";
import { useScanQueue } from "./features/scan-queue/useScanQueue";
import { useScannerController } from "./features/scanner/useScannerController";
import { useDesktopSession } from "./features/session/useDesktopSession";
import { useReviewTasks } from "./features/tasks/useReviewTasks";
import { useUploadSync } from "./features/upload/useUploadSync";
import { claimLegacyDurableStore, hasDurableDesktopStore } from "./lib/durableStore";
import { listOfflineDraftEnvelopes, type OfflineDraftEnvelope } from "./lib/offlineStore";
import type { LocalLogEntry, SystemStatus, WorkspaceKey } from "./types";


export const defaultServer = "http://127.0.0.1:8080";

export const navItems: { key: WorkspaceKey; label: string; icon: React.ReactNode }[] = [
  { key: "connect", label: "连接登录", icon: <LogIn size={18} /> },
  { key: "tasks", label: "任务列表", icon: <ListChecks size={18} /> },
  { key: "scan", label: "扫描上传", icon: <FileUp size={18} /> },
  { key: "offline", label: "离线阅卷", icon: <BookOpenCheck size={18} /> },
  { key: "sync", label: "同步队列", icon: <CloudUpload size={18} /> },
  { key: "diagnostics", label: "系统诊断", icon: <Stethoscope size={18} /> },
  { key: "logs", label: "本地日志", icon: <ScrollText size={18} /> }
];

export const sourceLabels: Record<string, string> = {
  ai_low_confidence: "AI 低置信",
  ocr_low_confidence: "OCR 低置信",
  subjective_default_review: "主观题复核",
  evidence_verification_failed: "证据校验失败",
  double_mark_required: "双评任务",
  score_anomaly: "分数异常",
  manual_sample: "人工抽检"
};

export function serviceStatusTone(status: SystemStatus | null): "idle" | "ready" | "warning" {
  if (!status) return "idle";
  if (status.status === "healthy") return "ready";
  return "warning";
}

export function logTagColor(level: LocalLogEntry["level"]) {
  if (level === "error") return "error";
  if (level === "warning") return "warning";
  return "blue";
}
// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function useDesktopApp() {
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const [workspace, setWorkspace] = useState<WorkspaceKey>("connect");
  const [offlineTaskId, setOfflineTaskId] = useState("");
  const [offlineDrafts, setOfflineDrafts] = useState<OfflineDraftEnvelope[]>([]);
  const [offlineQueueError, setOfflineQueueError] = useState<string | null>(null);
  const leaveGuard = useRef<OfflineLeaveGuard | null>(null);
  const leaving = useRef(false);
  const registerLeaveGuard = useCallback((guard: OfflineLeaveGuard | null) => { leaveGuard.current = guard; }, []);
  const leaveThen = async (action: () => void | Promise<unknown>) => {
    if (leaving.current) return;
    leaving.current = true;
    try { if (!leaveGuard.current || await leaveGuard.current.requestLeave()) await action(); }
    finally { leaving.current = false; }
  };
  useEffect(() => {
    return subscribeBeforeUnload(() => leaveGuard.current?.shouldBlockClose() ?? false);
  }, []);
  useEffect(() => {
    if (!hasDurableDesktopStore()) return;
    let disposed = false;
    let unlisten: (() => void) | undefined;
    // Windows 标题栏关闭走 Tauri 事件；浏览器 beforeunload 不能单独保证原生窗口等待草稿写盘。
    void import("@tauri-apps/api/window").then(async ({ getCurrentWindow }) => {
      const stop = await getCurrentWindow().onCloseRequested(async (event) => {
        if (leaving.current) { event.preventDefault(); return; }
        leaving.current = true;
        try { if (leaveGuard.current && !await leaveGuard.current.requestLeave()) event.preventDefault(); }
        catch { event.preventDefault(); }
        finally { leaving.current = false; }
      });
      if (disposed) stop(); else unlisten = stop;
    });
    return () => { disposed = true; unlisten?.(); };
  }, []);
  const { logs, logEvent, clearLogs } = useLocalLogs();
  const {
    client, serverUrl, setServerUrl, confirmServerUrl, tenantCode, setTenantCode, username, setUsername,
    password, setPassword, rememberLogin, setRememberLogin, credentialStoreMessage,
    credentialStoreReady, token, expiresAt, user, authError, isLoggingIn, isLoggingOut, durableScopeKey,
    handleLogin, handleLogout, handleForgetStoredLogin, handleCheckSession,
    enableOfflineAccess, setEnableOfflineAccess, offlineAccess, offlineAccessMessage, offlineUnlocked, handleUnlockOffline,
    handleRenewOfflineAccess, editorAccountKey = durableScopeKey
  } = useDesktopSession(defaultServer, logEvent);
  const {
    capabilities, diagnostics, localCacheSecurity, serviceStatus, isCheckingServiceStatus,
    diagnosticError, setDiagnosticError, refreshCapabilities, saveServerForSession,
    handleHealthCheck, handleSystemStatusCheck, healthResult, isCheckingHealth
  } = useRuntimeDiagnostics(client, serverUrl, durableScopeKey, setServerUrl, logEvent, confirmServerUrl, !offlineUnlocked);
  const [legacyClaimConfirmed, setLegacyClaimConfirmed] = useState(false);
  const [legacyClaimError, setLegacyClaimError] = useState<string | null>(null);
  const [isClaimingLegacy, setIsClaimingLegacy] = useState(false);
  const legacyDataPresent = capabilities.some((item) => item.key === "local_cache" && item.status === "legacy_data");

  async function handleClaimLegacy() {
    if (!legacyClaimConfirmed || !durableScopeKey) return;
    setIsClaimingLegacy(true);
    setLegacyClaimError(null);
    try {
      await claimLegacyDurableStore(durableScopeKey);
      await refreshCapabilities();
      setLegacyClaimConfirmed(false);
    } catch (error) {
      setLegacyClaimError(getUserErrorMessage(error, "旧版数据认领失败，原始数据仍隔离保留"));
    } finally {
      setIsClaimingLegacy(false);
    }
  }
  const [isOnline, setIsOnline] = useState(() => navigator.onLine);
  useEffect(() => {
    if (durableScopeKey) void refreshCapabilities();
  }, [durableScopeKey, refreshCapabilities]);

  const { tasks, taskError, isLoadingTasks, handleLoadTasks, examNames = {} } = useReviewTasks(client, durableScopeKey, logEvent, user?.id);
  const capture = useExamCaptureContext({ client, token, durableScopeKey, workspace, logEvent });
  const { exams, selectedExamId, scanSubmissionId, captureBatchId, scanStartPage } = capture;
  const scanner = useScannerController({ workspace, isOnline, durableScopeKey, logEvent });
  const templatePreset = capture.scanContext?.template_status === "ready" ? capture.scanContext.template?.id ?? "" : "";
  useEffect(() => {
    scanner.setExpectedTemplatePreset(templatePreset);
    scanner.setScannerPreflight(null);
  }, [templatePreset, scanner.setExpectedTemplatePreset, scanner.setScannerPreflight]);
  const scanQueue = useScanQueue({
    durableScopeKey,
    exams, selectedExamId, captureBatchId, scanSubmissionId, scanStartPage,
    captureBatches: capture.captureBatches,
    scanContext: capture.scanContext, client, token,
    fileImportTemplate: { templatePreset, paperSize: scanner.expectedPaperSize, dpi: scanner.expectedDpi, duplex: scanner.expectedDuplex },
    setDiagnosticError, logEvent
  });
  const {
    queue, offlineDraftCount, fileBufferRef, setOfflineDraftCount,
    uploadInFlightRef, queueRef, durablePersistenceRef, updateQueue,
  } = scanQueue;
  const uploadSync = useUploadSync({
    client, token, durableScopeKey, isOnline, setIsOnline, queueRef, fileBufferRef,
    uploadInFlightRef, durablePersistenceRef, updateQueue, logEvent, onUploaded: capture.handleUploadCompleted
  });
  const quality = useSubmissionQuality({ client, token, scanSubmissionId, durableScopeKey, selectedExamId, captureBatchId,
    linkedSubmissionIds: capture.linkedSubmissionIds ?? [], logEvent, autoCheck: workspace === "scan" && !capture.loadingProcessing && !capture.processingPending });
  // biome-ignore lint/correctness/useExhaustiveDependencies: 账号作用域变化必须清空旧草稿投影，不能因 effect 不读 key 就取消重置触发。
  useEffect(() => { setOfflineTaskId(""); setOfflineDrafts([]); }, [durableScopeKey]);
  useEffect(() => {
    if (workspace !== "sync" || hasDurableDesktopStore() && !durableScopeKey) return;
    let current = true;
    setOfflineQueueError(null);
    void listOfflineDraftEnvelopes(durableScopeKey).then((drafts) => {
      if (current) { setOfflineDrafts(drafts); setOfflineDraftCount(drafts.length); }
    }).catch((error) => { if (current) setOfflineQueueError(getUserErrorMessage(error, "本机草稿同步状态读取失败，请重试。")); });
    return () => { current = false; };
  }, [durableScopeKey, workspace, setOfflineDraftCount]);



  const activeCapabilityWarnings = capabilities.filter((item) => item.status !== "ready");
  let sessionStatusColor = "default";
  let sessionStatusLabel = "未登录";
  // 在线令牌优先；仅解锁本机草稿时不能显示为已登录。
  if (token) {
    sessionStatusColor = "success";
    sessionStatusLabel = "已登录";
  } else if (offlineUnlocked) {
    sessionStatusColor = "warning";
    sessionStatusLabel = "本机草稿已解锁";
  }
  return {
    readingSize, setReadingSize, workspace, setWorkspace,
    offlineTaskId, setOfflineTaskId, offlineDrafts, offlineQueueError,
    setOfflineQueueError, registerLeaveGuard, leaveThen, logs,
    logEvent, clearLogs, client, serverUrl,
    setServerUrl, tenantCode, setTenantCode, username,
    setUsername, password, setPassword, rememberLogin,
    setRememberLogin, credentialStoreMessage, credentialStoreReady, token,
    expiresAt, user, authError, isLoggingIn,
    isLoggingOut, durableScopeKey, handleLogin, handleLogout,
    handleForgetStoredLogin, handleCheckSession, enableOfflineAccess, setEnableOfflineAccess,
    offlineAccess, offlineAccessMessage, offlineUnlocked, handleUnlockOffline,
    handleRenewOfflineAccess, editorAccountKey, capabilities, diagnostics,
    localCacheSecurity, serviceStatus, isCheckingServiceStatus, diagnosticError,
    setDiagnosticError, refreshCapabilities, saveServerForSession, handleHealthCheck,
    handleSystemStatusCheck, healthResult, isCheckingHealth, legacyClaimConfirmed,
    setLegacyClaimConfirmed, legacyClaimError, isClaimingLegacy, legacyDataPresent,
    handleClaimLegacy, isOnline, tasks, taskError,
    isLoadingTasks, handleLoadTasks, examNames, capture,
    captureBatchId, scanner, scanQueue, queue,
    offlineDraftCount, uploadSync, quality, activeCapabilityWarnings,
    sessionStatusColor, sessionStatusLabel,
  };
}
