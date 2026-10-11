import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Descriptions, Space, Tag, Tooltip, type TableColumnsType } from "antd";
import { Activity, Database, RefreshCw, ScanText, ShieldCheck, Signal } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getSystemStatus, type DependencyStatus, type SystemStatus, type WorkerServiceStatus } from "../api/system";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";

const dependencyNames: Record<string, string> = {
  postgres: "数据库",
  redis: "缓存队列",
  minio: "文件存储",
  qdrant: "检索服务",
  ai_service: "智能评分服务",
  ocr_worker: "文字识别服务"
};

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn("系统状态请求失败", error.status, error.code);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatTime(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function formatUptime(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return "-";
  }
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return `${days}天 ${hours}小时 ${minutes}分`;
}

function dependencyTone(status: DependencyStatus["status"]) {
  if (status === "ok") {
    return "success";
  }
  if (status === "not_configured") {
    return "warning";
  }
  if (status === "disabled") {
    return "default";
  }
  if (status === "mock") {
    return "processing";
  }
  return "error";
}

function dependencyText(status: DependencyStatus["status"]) {
  if (status === "ok") {
    return "正常";
  }
  if (status === "not_configured") {
    return "未配置/待接入";
  }
  if (status === "disabled") {
    return "已关闭";
  }
  if (status === "mock") {
    return "演示模式";
  }
  return "异常";
}

export function SystemStatusPage() {
  const frontendRelease = import.meta.env.VITE_RELEASE_ID?.trim() || "local";
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setStatus(await getSystemStatus());
    } catch (nextError) {
      setError(formatError(nextError));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const dependencySummary = useMemo(() => {
    const items = status?.dependencies ?? [];
    return {
      total: items.length,
      ok: items.filter((item) => item.status === "ok").length,
      warning: items.filter((item) => item.status === "not_configured").length,
      error: items.filter((item) => item.status === "error").length
    };
  }, [status]);
  const ocrWorker = useMemo(
    () => status?.worker_services?.find((item) => item.name === "ocr_worker"),
    [status?.worker_services]
  );

  return (
    <div className="system-status-shell">
      <section className="page-heading system-status-topbar">
        <div>
          <h1>系统状态</h1>
          <p>查看平台服务运行状态、各项依赖是否连通，以及日志记录说明。</p>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>
          刷新
        </Button>
      </section>

      {loading && !status ? <LoadingState label="读取系统状态" /> : null}
      {error ? <ErrorState message={error} onRetry={() => void load()} /> : null}
      {status ? <SystemStatusDetails status={status} frontendRelease={frontendRelease} dependencySummary={dependencySummary} ocrWorker={ocrWorker} /> : null}
    </div>
  );
}

// 刷新失败时仍展示上次状态；展示组件不重新请求，也不清空父页面已保存的结果。
function SystemStatusDetails({ status, frontendRelease, dependencySummary, ocrWorker }: Readonly<{
  status: SystemStatus;
  frontendRelease: string;
  dependencySummary: { total: number; ok: number; warning: number; error: number };
  ocrWorker: WorkerServiceStatus | undefined;
}>) {

  const columns: TableColumnsType<DependencyStatus> = [
    {
      title: "服务",
      dataIndex: "name",
      render: (value: string) => (
        <Space align="start">
          <Database size={16} />
          <div>
            <strong>{dependencyNames[value] ?? value}</strong>
            <div><small>{value}</small></div>
          </div>
        </Space>
      )
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 150,
      render: (value: DependencyStatus["status"]) => <Tag color={dependencyTone(value)}>{dependencyText(value)}</Tag>
    },
    {
      title: "用途",
      dataIndex: "required",
      width: 110,
      render: (value: boolean) => value ? "核心依赖" : "可选能力"
    },
    { title: "检查耗时", dataIndex: "duration_ms", width: 120, render: (value: number) => `${value} ms` },
    { title: "最近检查", dataIndex: "checked_at", width: 190, render: (value: string) => formatTime(value) },
    {
      title: "说明",
      dataIndex: "detail",
      render: (_value, record) => record.error ?? record.detail ?? "-"
    }
  ];

  return (
    <>
          <section className="system-status-summary">
            <div>
              <span>整体状态</span>
              <strong title={status.status}>{status.status === "healthy" ? "健康" : "降级"}</strong>
            </div>
            <div>
              <span>服务</span>
              <strong>{status.service}</strong>
              <small>{status.environment}</small>
            </div>
            <div>
              <span>版本</span>
              <strong>{status.version}</strong>
              <small>发布 {status.release_id} · Schema {status.schema_version}</small>
            </div>
            <div>
              <span>运行时长</span>
              <strong>{formatUptime(status.uptime_sec)}</strong>
              <small>生成 {formatTime(status.generated_at)}</small>
            </div>
          </section>

          {frontendRelease !== "local" && status.release_id !== "local" && frontendRelease !== status.release_id ? (
            <Alert
              type="warning"
              showIcon
              message="前后端发布版本暂不一致"
              description={`网页 ${frontendRelease} · API ${status.release_id}。滚动升级期间可继续使用；若长时间不一致，请联系平台运维。`}
            />
          ) : null}

          <WorkerStatusPanel worker={ocrWorker} />

          <section className="system-status-grid">
            <div className="system-status-panel">
              <div className="section-head">
                <div>
                  <Space>
                    <Signal size={18} />
                    <h2>依赖状态</h2>
                  </Space>
                  <p>
                    共 {dependencySummary.total} 项，正常 {dependencySummary.ok}，未配置 {dependencySummary.warning}，异常 {dependencySummary.error}
                  </p>
                </div>
              </div>
              <ResponsiveTable rowKey="name" columns={columns} dataSource={status.dependencies} pagination={false} mobilePrimaryKeys={["name", "status", "detail"]} scroll={{ x: 900 }} />
            </div>

            <div className="system-status-panel">
              <div className="section-head">
                <div>
                  <Space>
                    <Activity size={18} />
                    <h2>日志与追踪</h2>
                  </Space>
                  <p>系统日志用于排障，审计日志用于业务追溯，两者分离。</p>
                </div>
              </div>
              <Descriptions bordered size="small" column={1}>
                <Descriptions.Item label="日志格式">{status.observability.log_format}</Descriptions.Item>
                <Descriptions.Item label="系统日志流">{status.observability.system_log_stream}</Descriptions.Item>
                <Descriptions.Item label="审计日志流">{status.observability.audit_log_stream}</Descriptions.Item>
                <Descriptions.Item label="请求 ID">{status.observability.request_id_header}</Descriptions.Item>
                <Descriptions.Item label="Trace ID">{status.observability.trace_id_header}</Descriptions.Item>
                <Descriptions.Item label="慢请求阈值">{status.observability.slow_request_threshold_ms} ms</Descriptions.Item>
                <Descriptions.Item label="慢查询日志">{status.observability.slow_query_log}</Descriptions.Item>
              </Descriptions>
            </div>
          </section>

          <Alert
            type="info"
            showIcon
            icon={<ShieldCheck size={18} />}
            message="日志隐私边界"
            description={status.observability.sensitive_log_policy}
          />
    </>
  );
}

function workerPresentation(worker: WorkerServiceStatus) {
  // 自动处理能力优先于可用性标签，保持网关返回组合状态时的既有展示顺序。
  if (worker.automation_available) return { tone: "success", alertType: "success", text: "在线" } as const;
  if (worker.availability === "not_configured") return { tone: "default", alertType: "info", text: "未启用" } as const;
  if (worker.availability === "stale") return { tone: "warning", alertType: "warning", text: "心跳失联" } as const;
  return { tone: "error", alertType: "error", text: "不可用" } as const;
}

function WorkerStatusPanel({ worker }: Readonly<{ worker: WorkerServiceStatus | undefined }>) {
  if (!worker) return <Alert type="warning" showIcon message="暂未获取到文字识别服务状态" description="识别服务可能未部署或未连通，请联系平台技术支持处理。" />;
  const presentation = workerPresentation(worker);
  const lastHeartbeat = worker.last_seen_at ? " 最近心跳：" + formatTime(worker.last_seen_at) : "";
  return (
    <section className={`system-worker-status ${worker.availability}`}>
      <div className="system-worker-heading">
        <div>
          <Space>
            <ScanText size={18} />
            <h2>文字识别服务</h2>
          </Space>
          <Tooltip title={`服务：${worker.worker_service} · 队列：${worker.queue_name}`}>
            <p>负责自动识别答卷内容；超过 {worker.stale_after_sec} 秒没有心跳即判定为失联</p>
          </Tooltip>
        </div>
        <Tag color={presentation.tone}>{presentation.text}</Tag>
      </div>
      <div className="system-worker-metrics">
        <span>在线实例<strong>{worker.fresh_instances}</strong></span>
        <span>失联实例<strong>{worker.stale_instances}</strong></span>
        <span>待处理<strong>{worker.queued_tasks}</strong></span>
        <span>处理中<strong>{worker.in_flight_tasks}</strong></span>
        <span>近一小时失败<strong>{worker.failed_last_hour}</strong></span>
        <Tooltip title="重试多次仍失败、需人工介入的任务数"><span>多次失败已搁置<strong>{worker.dead_letter_tasks}</strong></span></Tooltip>
      </div>
      <Alert type={presentation.alertType} showIcon message={worker.impact} description={`处理建议：${worker.action}${lastHeartbeat}`} />
    </section>
  );
}
