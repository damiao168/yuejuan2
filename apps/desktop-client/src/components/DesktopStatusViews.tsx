import { Alert, Button, Empty, Progress, Space, Table, Tag, Tooltip, type TableColumnsType } from "antd";
import { RotateCcw } from "lucide-react";
import { queueStatusLabels } from "../statusLabels";
import type { ScanQualityCheck, SyncQueueItem } from "../types";
import { formatBytes, formatDate } from "../lib/scanFiles";

export function SectionHead(props: Readonly<{ icon: React.ReactNode; title: string; description: string; action?: React.ReactNode }>) {
  return (
    <div className="section-head">
      <div className="section-title">
        <span>{props.icon}</span>
        <div>
          <h3>{props.title}</h3>
          <p>{props.description}</p>
        </div>
      </div>
      {props.action}
    </div>
  );
}

export function StatusLine(props: Readonly<{ label: string; value: string; tone: "ready" | "warning" | "idle" }>) {
  return (
    <div className="status-line">
      <span>{props.label}</span>
      <strong>{props.value}</strong>
      <i className={props.tone} />
    </div>
  );
}

export function CapabilityTag(props: Readonly<{ status: string }>) {
  if (props.status === "ready") return <Tag color="success">已配置</Tag>;
  if (props.status === "legacy_data") return <Tag color="warning">旧数据待核对</Tag>;
  if (props.status === "browser_fallback") return <Tag color="processing">开发模式</Tag>;
  if (props.status === "unavailable") return <Tag color="error">不可用</Tag>;
  return <Tag color="warning">未配置/待接入</Tag>;
}

function qualityCheckColor(status: ScanQualityCheck["status"]): string {
  if (status === "passed") return "success";
  if (status === "failed") return "error";
  return "warning";
}

function qualityCheckLabel(status: ScanQualityCheck["status"]): string {
  if (status === "passed") return "通过";
  if (status === "warning") return "请关注";
  if (status === "failed") return "失败";
  return "未配置/待接入";
}

function queueStatusColor(status: SyncQueueItem["status"]): string {
  if (status === "succeeded") return "success";
  if (status === "failed") return "error";
  if (status === "uploading") return "processing";
  return "default";
}

export function ScanQueueRecovery(props: Readonly<{ status: "loading" | "ready" | "error" | "locked"; error?: string | null; onRetry: () => void; onDiagnostics?: () => void }>) {
  if (props.status === "ready") return null;
  if (props.status === "locked") return <Alert type="info" showIcon message="请先登录或解锁本账号，再读取本机上传队列。" />;
  return <Alert className="section-alert" type={props.status === "error" ? "error" : "info"} showIcon
    message={props.status === "error" ? "本机上传队列读取失败" : "正在读取本机上传队列"}
    description={<div><p>{props.error ?? "正在核对本账号之前保留的原件，请稍候。"}</p><p>原队列尚未核实；已显示的项目保留。请先读取成功，再决定是否重新导入，避免重复答卷。读取失败时请检查账号授权和磁盘。</p>
      {props.status === "error" && <Space><Button onClick={props.onRetry}>重新读取本机队列</Button>{props.onDiagnostics && <Button onClick={props.onDiagnostics}>打开诊断</Button>}</Space>}</div>} />;
}

function QueueContext({ item }: Readonly<{ item: SyncQueueItem }>) {
  return <div>考试：{item.examName || "考试名称未确认"}<p>采集批次：{item.captureBatchName || "批次名称未确认"}</p><p>批次创建：{item.captureBatchCreatedAt ? formatDate(item.captureBatchCreatedAt) : "时间未确认"}</p>
    <details><summary>内部关联信息</summary><p>考试编号：{item.examId ?? "未关联"}</p><p>批次编号：{item.captureBatchId ?? "未关联"}</p></details></div>;
}

export function QueueList(props: Readonly<{ items: SyncQueueItem[]; onOpen?: (item: SyncQueueItem) => void; emptyText?: string | false }>) {
  if (!props.items.length) return props.emptyText === false ? null : <Empty description={props.emptyText ?? "暂无队列项目"} />;
  return (
    <div className="queue-list">
      {props.items.map((item) => (
        <div className="queue-row" key={item.id}>
          <div>
            <strong>{item.title}</strong>
            <p>{item.detail}</p>
            {item.kind === "scan_upload" && <QueueContext item={item} />}
            {props.onOpen && <Button size="small" onClick={() => props.onOpen!(item)}>{item.kind === "offline_grade" ? "打开草稿查看详情" : "查看答卷与处理状态"}</Button>}
          </div>
          <div className="queue-progress">
            <Tag color={queueStatusColor(item.status)}>{item.kind === "offline_grade" ? ({ pending: "本地草稿", uploading: "提交待确认", succeeded: "已同步", failed: "同步失败", conflict: "存在冲突", not_configured: "未配置" }[item.status]) : queueStatusLabels[item.status]}</Tag>
            <Progress percent={item.progress} size="small" status={item.status === "failed" ? "exception" : undefined} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function ScanQueueTable(props: Readonly<{ items: SyncQueueItem[]; onRetry: (id: string) => void; canRetry?: (item: SyncQueueItem) => boolean; onRemove?: (id: string) => void; onReinspect?: (id: string) => void; onReplace?: (id: string) => void; emptyText?: string }>) {
  const columns: TableColumnsType<SyncQueueItem> = [
    {
      title: "预览",
      dataIndex: "previewUrl",
      width: 82,
      render: (_value, item) => item.previewUrl
        ? <img className="scan-preview" src={item.previewUrl} alt={item.fileName ?? item.title} />
        : <span className="scan-preview placeholder">PDF</span>
    },
    {
      title: "文件",
      dataIndex: "fileName",
      render: (_value, item) => (
        <div className="scan-file-cell">
          <strong>{item.fileName ?? item.title}</strong>
          <span>{item.contentType ?? "unknown"} / {formatBytes(item.fileSize)}</span>
          {item.requiresReselect && <Tag color="warning">需重新选择文件</Tag>}
        </div>
      )
    },
    { title: "页码", dataIndex: "pageNo", width: 80, render: (value?: number) => value ?? "未设置" },
    { title: "考试 / 采集批次", key: "context", render: (_value, item) => <QueueContext item={item} /> },
    {
      title: "本地质量检查",
      dataIndex: "qualityChecks",
      render: (checks?: ScanQualityCheck[]) => (
        <div className="quality-checks">
          {(checks ?? []).map((check) => (
            <Tooltip key={check.key} title={check.detail}>
              <Tag color={qualityCheckColor(check.status)}>
                {check.label}: {qualityCheckLabel(check.status)}
              </Tag>
            </Tooltip>
          ))}
        </div>
      )
    },
    {
      title: "队列状态",
      dataIndex: "status",
      width: 130,
      render: (status: SyncQueueItem["status"]) => <Tag color={queueStatusColor(status)}>{queueStatusLabels[status]}</Tag>
    },
    {
      title: "进度",
      dataIndex: "progress",
      width: 150,
      render: (_value, item) => <Progress percent={item.progress} size="small" status={item.status === "failed" ? "exception" : undefined} />
    },
    { title: "服务端状态", dataIndex: "serverStatus", render: (_value, item) => item.serverStatus ?? item.detail },
    {
      title: "操作",
      key: "action",
      width: 220,
      render: (_value, item) => (
        <div><Button size="small" icon={<RotateCcw size={14} />} disabled={item.status === "uploading" || item.status === "succeeded" || props.canRetry?.(item) === false} onClick={() => props.onRetry(item.id)}>
          重试
        </Button>
        {props.onReinspect && <Button size="small" disabled={item.status === "uploading" || item.status === "succeeded" || Boolean(item.remoteUploadId)} onClick={() => props.onReinspect!(item.id)}>重新检查</Button>}
        {props.onReplace && <Button size="small" disabled={item.status === "uploading" || item.status === "succeeded" || Boolean(item.remoteUploadId)} onClick={() => props.onReplace!(item.id)}>更换原件</Button>}
        {props.onRemove && <Button danger size="small" disabled={item.status === "uploading" || item.status === "succeeded" || Boolean(item.remoteUploadId)} onClick={() => props.onRemove!(item.id)}>移出队列</Button>}
        {item.qualityChecks?.some((check) => check.status === "failed") && <p>请修复原件后更换或重新检查；重试上传不会修复质量问题。</p>}</div>
      )
    }
  ];
  return (
    <Table
      rowKey="id"
      size="middle"
      columns={columns}
      dataSource={props.items}
      scroll={{ x: 1080 }}
      pagination={{ pageSize: 8, showSizeChanger: false }}
      locale={{ emptyText: <Empty description={props.emptyText ?? "尚未选择文件"} /> }}
    />
  );
}
