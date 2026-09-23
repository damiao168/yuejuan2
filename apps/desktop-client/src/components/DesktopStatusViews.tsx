import { Button, Empty, Progress, Table, Tag, Tooltip, type TableColumnsType } from "antd";
import { RotateCcw } from "lucide-react";
import { queueStatusLabels } from "../statusLabels";
import type { ScanQualityCheck, SyncQueueItem } from "../types";
import { formatBytes } from "../lib/scanFiles";

export function SectionHead(props: { icon: React.ReactNode; title: string; description: string; action?: React.ReactNode }) {
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

export function StatusLine(props: { label: string; value: string; tone: "ready" | "warning" | "idle" }) {
  return (
    <div className="status-line">
      <span>{props.label}</span>
      <strong>{props.value}</strong>
      <i className={props.tone} />
    </div>
  );
}

export function CapabilityTag(props: { status: string }) {
  if (props.status === "ready") return <Tag color="success">已配置</Tag>;
  if (props.status === "legacy_data") return <Tag color="warning">旧数据待核对</Tag>;
  if (props.status === "browser_fallback") return <Tag color="processing">开发模式</Tag>;
  if (props.status === "unavailable") return <Tag color="error">不可用</Tag>;
  return <Tag color="warning">未配置/待接入</Tag>;
}

export function QueueList(props: { items: SyncQueueItem[] }) {
  if (!props.items.length) return <Empty description="暂无队列项目" />;
  return (
    <div className="queue-list">
      {props.items.map((item) => (
        <div className="queue-row" key={item.id}>
          <div>
            <strong>{item.title}</strong>
            <p>{item.detail}</p>
          </div>
          <div className="queue-progress">
            <CapabilityTag status={item.status === "not_configured" ? "not_configured" : item.status === "failed" ? "unavailable" : item.status === "succeeded" ? "ready" : "browser_fallback"} />
            <Progress percent={item.progress} size="small" status={item.status === "failed" ? "exception" : undefined} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function ScanQueueTable(props: { items: SyncQueueItem[]; onRetry: (id: string) => void }) {
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
    {
      title: "本地质量检查",
      dataIndex: "qualityChecks",
      render: (checks?: ScanQualityCheck[]) => (
        <div className="quality-checks">
          {(checks ?? []).map((check) => (
            <Tooltip key={check.key} title={check.detail}>
              <Tag color={check.status === "passed" ? "success" : check.status === "failed" ? "error" : "warning"}>
                {check.label}: {check.status === "passed" ? "通过" : check.status === "warning" ? "请关注" : check.status === "failed" ? "失败" : "未配置/待接入"}
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
      render: (status: SyncQueueItem["status"]) => <Tag color={status === "succeeded" ? "success" : status === "failed" ? "error" : status === "uploading" ? "processing" : "default"}>{queueStatusLabels[status]}</Tag>
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
      width: 92,
      render: (_value, item) => (
        <Button size="small" icon={<RotateCcw size={14} />} disabled={item.status === "uploading" || item.status === "succeeded"} onClick={() => props.onRetry(item.id)}>
          重试
        </Button>
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
      locale={{ emptyText: <Empty description="尚未选择文件" /> }}
    />
  );
}
