import { useEffect, useState } from "react";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
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
import { OfflineWorkbench } from "./components/OfflineWorkbench";
import { CapabilityTag, QueueList, SectionHead, StatusLine } from "./components/DesktopStatusViews";
import { hasDurableDesktopStore } from "./lib/durableStore";
import {
  dependencyLabel,
  dependencyTone,
  estimateLocalCacheBytes,
  formatBytes,
  formatDate
} from "./lib/scanFiles";
import type {
  ReviewTask,
  SyncQueueItem,
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
  const {
    capabilities, diagnostics, localCacheSecurity, serviceStatus, isCheckingServiceStatus,
    diagnosticError, setDiagnosticError, refreshCapabilities, saveServerForSession,
    handleHealthCheck, handleSystemStatusCheck
  } = useRuntimeDiagnostics(client, serverUrl, setServerUrl, logEvent);
  const [isOnline, setIsOnline] = useState(() => navigator.onLine);

  const { tasks, taskError, isLoadingTasks, handleLoadTasks } = useReviewTasks(client, logEvent);
  const capture = useExamCaptureContext({ client, token, workspace, logEvent });
  const { exams, selectedExamId, scanSubmissionId, captureBatchId, scanStartPage } = capture;
  const scanner = useScannerController({ workspace, isOnline, logEvent });
  const { scannerPreflight } = scanner;
  const scanQueue = useScanQueue({
    exams, selectedExamId, captureBatchId, scanSubmissionId, scanStartPage,
    scannerPreflight, setDiagnosticError, logEvent
  });
  const {
    queue, offlineDraftCount, fileBufferRef,
    uploadInFlightRef, queueRef, durablePersistenceRef, updateQueue,
  } = scanQueue;
  const uploadSync = useUploadSync({
    client, token, isOnline, setIsOnline, queueRef, fileBufferRef,
    uploadInFlightRef, durablePersistenceRef, updateQueue, logEvent
  });
  const quality = useSubmissionQuality(client, scanSubmissionId, logEvent);



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
