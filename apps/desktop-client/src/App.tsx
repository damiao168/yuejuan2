import { useCallback, useEffect, useRef, useState } from "react";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import { subscribeBeforeUnload } from "@edugrade/sdk";
import {
  Alert,
  Button,
  Checkbox,
  ConfigProvider,
  Empty,
  Form,
  Input,
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
  ScrollText,
  ServerCog,
  Settings,
  ShieldAlert,
  Stethoscope,
  Wifi
} from "lucide-react";
import { motion } from "framer-motion";
import { getUserErrorMessage } from "./api/userError";
import { OfflineWorkbench, type OfflineLeaveGuard } from "./components/OfflineWorkbench";
import { CapabilityTag, QueueList, ScanQueueRecovery, SectionHead, StatusLine } from "./components/DesktopStatusViews";
import { claimLegacyDurableStore, hasDurableDesktopStore } from "./lib/durableStore";
import { listOfflineDraftEnvelopes, type OfflineDraftEnvelope } from "./lib/offlineStore";
import {
  dependencyLabel,
  dependencyTone,
  estimateLocalCacheBytes,
  formatBytes,
  formatDate
} from "./lib/scanFiles";
import type {
  LocalLogEntry,
  ReviewTask,
  SyncQueueItem,
  SystemStatus,
  WorkspaceKey
} from "./types";
import { useDesktopSession } from "./features/session/useDesktopSession";
import { useLocalLogs } from "./features/runtime/useLocalLogs";
import { useRuntimeDiagnostics } from "./features/runtime/useRuntimeDiagnostics";
import { useReviewTasks } from "./features/tasks/useReviewTasks";
import { useExamCaptureContext } from "./features/capture/useExamCaptureContext";
import { useScannerController } from "./features/scanner/useScannerController";
import { useScanQueue } from "./features/scan-queue/useScanQueue";
import { useUploadSync } from "./features/upload/useUploadSync";
import { useSubmissionQuality } from "./features/quality/useSubmissionQuality";
import { ScanWorkspace } from "./features/scan/ScanWorkspace";
import { reviewTaskState } from "./statusLabels";
import { NativeStoragePanel } from "./components/NativeStoragePanel";

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

function serviceStatusTone(status: SystemStatus | null): "idle" | "ready" | "warning" {
  if (!status) return "idle";
  if (status.status === "healthy") return "ready";
  return "warning";
}

function logTagColor(level: LocalLogEntry["level"]) {
  if (level === "error") return "error";
  if (level === "warning") return "warning";
  return "blue";
}

function App() {
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
    queue, offlineDraftCount, fileBufferRef,
    uploadInFlightRef, queueRef, durablePersistenceRef, updateQueue,
  } = scanQueue;
  const uploadSync = useUploadSync({
    client, token, durableScopeKey, isOnline, setIsOnline, queueRef, fileBufferRef,
    uploadInFlightRef, durablePersistenceRef, updateQueue, logEvent
  });
  const quality = useSubmissionQuality({ client, token, scanSubmissionId, durableScopeKey, selectedExamId, captureBatchId,
    linkedSubmissionIds: capture.linkedSubmissionIds ?? [], logEvent });
  useEffect(() => { setOfflineTaskId(""); setOfflineDrafts([]); }, [durableScopeKey]);
  useEffect(() => {
    if (workspace !== "sync" || hasDurableDesktopStore() && !durableScopeKey) return;
    let current = true;
    setOfflineQueueError(null);
    void listOfflineDraftEnvelopes(durableScopeKey).then((drafts) => {
      if (current) { setOfflineDrafts(drafts); scanQueue.setOfflineDraftCount(drafts.length); }
    }).catch((error) => { if (current) setOfflineQueueError(getUserErrorMessage(error, "本机草稿同步状态读取失败，请重试。")); });
    return () => { current = false; };
  }, [durableScopeKey, workspace]);



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
                onClick={() => { if (workspace !== item.key) void leaveThen(() => setWorkspace(item.key)); }}
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
              <Tag color={sessionStatusColor}>{sessionStatusLabel}</Tag>
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
            {workspace === "scan" && <ScanWorkspace
              token={token}
              isOnline={isOnline}
              capture={capture}
              scanner={scanner}
              scanQueue={scanQueue}
              uploadSync={uploadSync}
              quality={quality}
            />}
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
          <SectionHead icon={<ServerCog size={20} />} title="连接学校服务器" description="请向学校教务处或技术老师索取服务器地址和学校代码。地址先检查并保存，再填写账号登录。" />
          <Form layout="vertical">
            <Form.Item label="学校服务器地址">
              <Input value={serverUrl} disabled={Boolean(token) || offlineUnlocked} onChange={(event) => setServerUrl(event.target.value)} placeholder="例如：https://grade.school.edu" />
            </Form.Item>
            <Space wrap>
              <Button type="primary" icon={<Settings size={16} />} disabled={Boolean(token)} onClick={saveServerForSession}>
                保存服务器地址
              </Button>
              <Button icon={<Wifi size={16} />} loading={isCheckingHealth} disabled={offlineUnlocked || !isOnline} onClick={handleHealthCheck}>
                检查连接
              </Button>
            </Space>
          </Form>
          {healthResult && <Alert className="section-alert" showIcon type={healthResult.ok ? "success" : "error"} message={healthResult.message} description={`${healthResult.server} · ${healthResult.elapsedMs} 毫秒`} />}
          {diagnosticError && <Alert className="section-alert" type="warning" message={diagnosticError} showIcon />}
        </section>

        <section className="panel">
          <SectionHead icon={<LogIn size={20} />} title="学校账号登录" description="使用学校分配的账号。勾选自动登录后，密码仅保存到当前 Windows 用户的系统凭据库。" />
          <Form layout="vertical">
            <Form.Item label="学校代码">
              <Input value={tenantCode} onChange={(event) => setTenantCode(event.target.value)} />
            </Form.Item>
            <Form.Item label="账号">
              <Input value={username} onChange={(event) => setUsername(event.target.value)} />
            </Form.Item>
            <Form.Item label="密码">
              <Input.Password value={password} onChange={(event) => setPassword(event.target.value)} onPressEnter={() => void leaveThen(handleLogin)} />
            </Form.Item>
            <Form.Item>
              <Checkbox checked={rememberLogin} disabled={!credentialStoreReady} onChange={(event) => setRememberLogin(event.target.checked)}>
                保存到 Windows 凭据库，下次自动登录
              </Checkbox>
            </Form.Item>
            {hasDurableDesktopStore() && <Form.Item><Checkbox checked={enableOfflineAccess} onChange={(event) => setEnableOfflineAccess(event.target.checked)}>允许当前 Windows 用户在有效期内离线解锁本账号草稿</Checkbox><p className="muted">联网登录后建立授权，有效期不超过这次登录且最多七天。仅解锁本机加密数据；退出登录或清除保存信息会撤销授权。</p></Form.Item>}
            <Space wrap>
              <Button type="primary" icon={<LogIn size={16} />} loading={isLoggingIn} onClick={() => void leaveThen(handleLogin)}>
                登录
              </Button>
              <Button icon={<ShieldAlert size={16} />} disabled={!token} onClick={handleCheckSession}>
                查看登录状态
              </Button>
              <Button disabled={!token && !durableScopeKey} loading={isLoggingOut} onClick={() => void leaveThen(handleLogout)}>
                退出登录
              </Button>
              <Button disabled={!credentialStoreReady} onClick={() => void leaveThen(handleForgetStoredLogin)}>
                清除已保存登录
              </Button>
            </Space>
          </Form>
          {offlineAccess && !token && <Button loading={isLoggingIn} onClick={() => void leaveThen(async () => { if (await handleUnlockOffline()) setWorkspace("offline"); })}>解锁 {offlineAccess.username}（{offlineAccess.tenantCode}）的本机草稿</Button>}
          {offlineAccessMessage && <Alert className="section-alert" type="info" message={offlineAccessMessage} showIcon />}
          {credentialStoreMessage && <Alert className="section-alert" type={credentialStoreReady ? "info" : "warning"} message={credentialStoreMessage} showIcon />}
          {authError && <Alert className="section-alert" type="error" message={authError} showIcon />}
          {token && <Space wrap className="section-alert"><span>登录成功，接下来：</span><Button type="primary" onClick={() => void leaveThen(() => setWorkspace("tasks"))}>查看我的任务</Button><Button onClick={() => void leaveThen(() => setWorkspace("scan"))}>导入答卷</Button></Space>}
          {legacyDataPresent && token && <div className="legacy-claim-panel">
            <Alert type="warning" showIcon message="检测到旧版未绑定账号的本地数据" description="请先由管理员核对该工作站旧扫描队列和离线草稿确属当前登录账号。认领会迁入当前账号的加密存储；若当前账号已有本地记录，操作会拒绝。" />
            <Checkbox checked={legacyClaimConfirmed} onChange={(event) => setLegacyClaimConfirmed(event.target.checked)}>我已核对旧数据归属当前账号</Checkbox>
            <Button disabled={!legacyClaimConfirmed} loading={isClaimingLegacy} onClick={() => void handleClaimLegacy()}>认领并迁移旧数据</Button>
            {legacyClaimError && <Alert type="error" showIcon message={legacyClaimError} />}
          </div>}
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
        render: (value: string) => <Tooltip title={reviewTaskState(value).detail}><Tag>{reviewTaskState(value).label}</Tag></Tooltip>
      },
      { title: "优先级", dataIndex: "priority", width: 90 },
      { title: "创建时间", dataIndex: "created_at", render: formatDate },
      { title: "考试", dataIndex: "exam_id", render: (value: string) => <Tooltip title={value}>{examNames[value] ?? "考试名称暂不可读"}</Tooltip> },
      { title: "截止时间", dataIndex: "due_at", render: (value?: string) => value ? formatDate(value) : "未设置" },
      { title: "操作", key: "open", render: (_value, task) => <Button disabled={!token || !reviewTaskState(task.status).canGrade} onClick={() => void leaveThen(() => { setOfflineTaskId(task.id); setWorkspace("offline"); })}>开始阅卷</Button> }
    ];
    return (
      <section className="panel full">
        <SectionHead
          icon={<ListChecks size={20} />}
          title="复核任务列表"
          description={`当前列表 ${tasks.length} 项。选择开始阅卷会打开已有本机草稿，或下载所选任务材料。`}
          action={
            <Button icon={<RefreshCw size={16} />} loading={isLoadingTasks} disabled={!token} onClick={handleLoadTasks}>
              刷新任务
            </Button>
          }
        />
        {!token && <Alert type="warning" message="请先在线登录，再读取分配给你的任务。已保存草稿可在离线阅卷中加载。" showIcon />}
        {taskError && <Alert className="section-alert" type="error" message={taskError} showIcon />}
        <Table rowKey="id" size="middle" columns={columns} dataSource={tasks} loading={isLoadingTasks} scroll={{ x: 760 }} locale={{ emptyText: <Empty description="暂无真实任务" /> }} />
      </section>
    );
  }


  function renderOffline() {
    const canRecoverAfterRestart = offlineAccess?.actorId === user?.id && offlineAccess?.tenantId === user?.tenant_id
      && offlineAccess?.server === serverUrl && new Date(offlineAccess.expiresAt).getTime() > Date.now();
    let offlineRecoveryDescription: string;
    if (canRecoverAfterRestart) {
      offlineRecoveryDescription = "请先加密保存草稿；重启后可在连接登录页解锁本账号草稿。答卷图片仍需联网恢复，上传和下载任务需在线登录。";
    } else if (offlineAccess && user) {
      offlineRecoveryDescription = "当前未保存评分仍保留。请在本页联网验证同一账号后续期，再加密保存；续期失败也可以留在本页重试。";
    } else {
      offlineRecoveryDescription = "跨重启继续阅卷前，请到连接登录页勾选本机离线授权并再次在线登录。当前编辑内容仍须加密保存。";
    }
    return (
      <>
      {hasDurableDesktopStore() && <Alert className="section-alert" type={canRecoverAfterRestart ? "info" : "warning"} showIcon
        message={canRecoverAfterRestart ? `本机离线恢复有效至 ${new Date(offlineAccess!.expiresAt).toLocaleString("zh-CN")}` : "本机离线恢复尚未启用或已过期"}
        description={offlineRecoveryDescription} />}
      {hasDurableDesktopStore() && offlineAccess && user && (offlineUnlocked || !canRecoverAfterRestart) && <section className="panel full">
        <p>续期当前账号：{offlineAccess.username}（{offlineAccess.tenantCode}） · {offlineAccess.server}</p>
        <Form layout="vertical"><Form.Item label="当前账号密码"><Input.Password value={password} disabled={isLoggingIn} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></Form.Item>
          <Button loading={isLoggingIn} disabled={!isOnline || !password || isLoggingIn || isLoggingOut} onClick={() => void handleRenewOfflineAccess(password)}>联网续期并保留当前评分</Button></Form>
      </section>}
      {offlineAccessMessage && <Alert className="section-alert" type="info" message={offlineAccessMessage} showIcon />}
      {authError && <Alert className="section-alert" type="error" message={authError} showIcon />}
      {/* 同账号续期不离开工作区；新租约传给原编辑器，未保存输入继续由统一离开保护负责。 */}
      <OfflineWorkbench key={editorAccountKey} durableScopeKey={durableScopeKey} client={client} token={token} user={user} isOnline={isOnline} onLog={logEvent} registerLeaveGuard={registerLeaveGuard} initialTaskId={offlineTaskId} />
      </>
    );
  }

  function renderSync() {
    const draftItems: SyncQueueItem[] = offlineDrafts.map((draft) => ({ id: draft.taskId, title: `评分草稿 · ${draft.anonymousCode}`, kind: "offline_grade",
      status: ({ draft: "pending", syncing: "uploading", synced: "succeeded", failed: "failed", conflict: "conflict" } as const)[draft.syncStatus],
      progress: draft.syncStatus === "synced" ? 100 : 0, updatedAt: draft.savedAt,
      detail: `${draft.syncMessage ?? "已保存到本机，尚未提交"} / 保存 ${formatDate(draft.savedAt)}` }));
    return (
      <section className="panel full">
        <SectionHead icon={<CloudUpload size={20} />} title="同步队列" description="查看答卷上传和本机评分草稿的真实状态；评分草稿可打开继续同步或核对回执。" />
        {offlineQueueError && <Alert type="error" message={offlineQueueError} showIcon />}
        <ScanQueueRecovery status={scanQueue.queueRestoreStatus} error={scanQueue.queueRestoreError} onRetry={() => void scanQueue.restoreQueue()} onDiagnostics={() => setWorkspace("diagnostics")} />
        {scanQueue.queueContextError && <Alert type="warning" showIcon message={scanQueue.queueContextError} />}
        <Button disabled={!token || !isOnline || !queue.length} loading={scanQueue.loadingQueueContext} onClick={() => void scanQueue.refreshQueueContext()}>重新读取考试与批次名称</Button>
        <QueueList items={[...queue, ...draftItems]} emptyText={scanQueue.queueRestoreStatus === "ready" ? undefined : false} onOpen={(item) => void leaveThen(() => {
          if (item.kind === "offline_grade") { setOfflineTaskId(item.id); setWorkspace("offline"); }
          else if (item.examId && item.captureBatchId) { capture.handleOpenQueueContext(item.examId, item.captureBatchId); setWorkspace("scan"); }
          else setOfflineQueueError("该文件缺少原考试或采集批次，请联系学校管理员核对上传记录后再定位。");
        })} />
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
          <Button className="section-button" icon={<Wifi size={16} />} loading={isCheckingHealth} disabled={offlineUnlocked || !isOnline} onClick={handleHealthCheck}>
            检查连接
          </Button>
          {healthResult && <Alert className="section-alert" showIcon type={healthResult.ok ? "success" : "error"} message={healthResult.message} description={`${healthResult.server} · ${healthResult.elapsedMs} 毫秒`} />}
          {diagnosticError && <Alert className="section-alert" type="warning" message={diagnosticError} showIcon />}
        </section>

        <section className="panel">
          <SectionHead
            icon={<ServerCog size={20} />}
            title="服务端连接"
            description="调用真实 /api/v1/system/status；依赖未配置会显示未配置/待接入。"
            action={
              <Button icon={<RefreshCw size={16} />} loading={isCheckingServiceStatus} disabled={!token || !isOnline} onClick={handleSystemStatusCheck}>
                检查状态
              </Button>
            }
          />
          <div className="diagnostic-list">
            <StatusLine label="Server" value={serverUrl} tone={serviceStatusTone(serviceStatus)} />
            <StatusLine label="Status" value={serviceStatus?.status ?? "未检查"} tone={serviceStatusTone(serviceStatus)} />
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
              <span>界面缓存大小</span>
              <strong>{formatBytes(localCacheBytes)}</strong>
              <small>localStorage 估算，不含原件</small>
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
          <NativeStoragePanel scope={durableScopeKey} />
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
                <Tag color={logTagColor(entry.level)}>{entry.level}</Tag>
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
