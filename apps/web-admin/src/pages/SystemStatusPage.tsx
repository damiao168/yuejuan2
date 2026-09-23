import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Descriptions, Space, Tag, Tooltip, type TableColumnsType } from "antd";
import { Activity, Database, RefreshCw, ScanText, ShieldCheck, Signal } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getSystemStatus, type DependencyStatus, type SystemStatus } from "../api/system";
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

      {status ? (
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

          {ocrWorker ? (
            <section className={`system-worker-status ${ocrWorker.availability}`}>
              <div className="system-worker-heading">
                <div>
                  <Space>
                    <ScanText size={18} />
                    <h2>文字识别服务</h2>
                  </Space>
                  <Tooltip title={`服务：${ocrWorker.worker_service} · 队列：${ocrWorker.queue_name}`}>
                    <p>负责自动识别答卷内容；超过 {ocrWorker.stale_after_sec} 秒没有心跳即判定为失联</p>
                  </Tooltip>
                </div>
                <Tag color={ocrWorker.automation_available ? "success" : ocrWorker.availability === "not_configured" ? "default" : ocrWorker.availability === "stale" ? "warning" : "error"}>
                  {ocrWorker.automation_available ? "在线" : ocrWorker.availability === "not_configured" ? "未启用" : ocrWorker.availability === "stale" ? "心跳失联" : "不可用"}
                </Tag>
              </div>
              <div className="system-worker-metrics">
                <span>在线实例<strong>{ocrWorker.fresh_instances}</strong></span>
                <span>失联实例<strong>{ocrWorker.stale_instances}</strong></span>
                <span>待处理<strong>{ocrWorker.queued_tasks}</strong></span>
                <span>处理中<strong>{ocrWorker.in_flight_tasks}</strong></span>
                <span>近一小时失败<strong>{ocrWorker.failed_last_hour}</strong></span>
                <Tooltip title="重试多次仍失败、需人工介入的任务数"><span>多次失败已搁置<strong>{ocrWorker.dead_letter_tasks}</strong></span></Tooltip>
              </div>
              <Alert
                type={ocrWorker.automation_available ? "success" : ocrWorker.availability === "not_configured" ? "info" : ocrWorker.availability === "stale" ? "warning" : "error"}
                showIcon
                message={ocrWorker.impact}
                description={`处理建议：${ocrWorker.action}${ocrWorker.last_seen_at ? ` 最近心跳：${formatTime(ocrWorker.last_seen_at)}` : ""}`}
                action={!ocrWorker.automation_available && ocrWorker.availability !== "not_configured" ? <Button size="small" href="#/capture">进入答卷处理</Button> : undefined}
              />
            </section>
          ) : (
            <Alert type="warning" showIcon message="暂未获取到文字识别服务状态" description="识别服务可能未部署或未连通，请联系平台技术支持处理。" />
          )}

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
              <ResponsiveTable rowKey="name" columns={columns} dataSource={status.dependencies} pagination={false} />
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
      ) : null}
    </div>
  );
}
