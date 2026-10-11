import { Button, Dropdown, Space, Tooltip, type TableColumnsType } from "antd";
import { KeyRound, MoreHorizontal, Pencil, Zap } from "lucide-react";
import type { ManagedAPIProbeMode, ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { StatusTag } from "../../../components/StatusTag";
import type { StatusTone } from "../../../types";
import { capabilityVerified, connectionStatusLabel, formatDateTime } from "../lib/modelConfig";

type ColumnOptions = Readonly<{
  configLoading: boolean;
  modelBusy: boolean;
  probingID: string;
  updatingID: string;
  openCreateWithKey: (config: ManagedModelAPIConfig) => void;
  openEdit: (config: ManagedModelAPIConfig) => void;
  probe: (config: ManagedModelAPIConfig, mode: ManagedAPIProbeMode) => Promise<void>;
  removeConfig: (config: ManagedModelAPIConfig) => void;
  showCapabilityDetails: (config: ManagedModelAPIConfig) => void;
  updateUsage: (config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) => Promise<void>;
}>;

function connectionTone(config: ManagedModelAPIConfig): StatusTone {
  if (config.last_test_status === "success") return "success";
  if (config.last_test_status === "temporary_unavailable") return "warning";
  if (config.last_test_status === "failed") return "danger";
  return "neutral";
}

function capabilityLabel(config: ManagedModelAPIConfig): string {
  if (config.last_capability_status === "success") {
    if (config.last_capability_probe_version === "structured-json-v3") {
      return `已验证 · ${formatDateTime(config.last_capability_tested_at)}`;
    }
    return "待按新策略复验";
  }
  return config.last_capability_status === "failed" ? "验证失败" : "未验证";
}

function ConnectionCell({ config, onShowDetails }: Readonly<{
  config: ManagedModelAPIConfig;
  onShowDetails: (config: ManagedModelAPIConfig) => void;
}>) {
  const hasFailureMessage = (config.last_test_status === "failed" || config.last_test_status === "temporary_unavailable") && config.last_test_message;
  return (
    <div className="platform-model-probe">
      <StatusTag tone={connectionTone(config)}>
        {config.last_test_status === "success" ? "连接正常" : connectionStatusLabel(config)}
      </StatusTag>
      <small title={config.last_test_message}>
        {hasFailureMessage ? config.last_test_message : <>{config.last_test_latency_ms ? `${config.last_test_latency_ms}ms · ` : ""}{formatDateTime(config.last_tested_at)}</>}
      </small>
      {config.last_test_status === "temporary_unavailable" ? <small>上次连接正常：{config.last_successful_tested_at ? formatDateTime(config.last_successful_tested_at) : "暂无记录"}</small> : null}
      <small title={config.last_capability_message}>结构化能力：{capabilityLabel(config)}</small>
      {config.last_capability_status === "failed" ? <Button className="platform-model-detail-link" type="link" size="small" onClick={() => onShowDetails(config)}>查看原因</Button> : null}
    </div>
  );
}

function UsageCell({ config }: Readonly<{ config: ManagedModelAPIConfig }>) {
  if (config.is_default) return <StatusTag tone="info">日常对话</StatusTag>;
  const active = config.status === "active";
  return <StatusTag tone={active ? "success" : "neutral"}>{active ? "备用" : "已停用"}</StatusTag>;
}

function ModelActions({ config, options }: Readonly<{ config: ManagedModelAPIConfig; options: ColumnOptions }>) {
  const active = config.status === "active";
  const busy = options.modelBusy || options.configLoading;
  const canUseForConversation = capabilityVerified(config);
  const toggleHint = active && config.is_default ? "停用后日常对话切回本地模型，恢复后作为备用模型" : undefined;
  const conversationLabel = canUseForConversation ? "设为日常对话模型" : "设为日常对话模型（需先完整检测）";

  async function selectAction(key: string) {
    if (key === "same_key") {
      options.openCreateWithKey(config);
      return;
    }
    if (key === "capability") {
      await options.probe(config, "capability");
      return;
    }
    if (key === "current" || key === "local") await options.updateUsage(config, "active", key === "current");
  }

  return (
    <Space size={6} wrap>
      <Tooltip title="只检查接口、密钥和模型权限，不发送模型生成请求">
        <Button size="small" icon={<Zap size={14} />} disabled={busy} loading={options.probingID === config.id} onClick={() => void options.probe(config, "quick")}>检测连接</Button>
      </Tooltip>
      <Tooltip title="编辑配置或更换密钥">
        <Button size="small" type="text" disabled={busy} icon={<Pencil size={14} />} aria-label={`编辑${config.display_name}`} onClick={() => options.openEdit(config)} />
      </Tooltip>
      <Dropdown
        disabled={busy}
        trigger={["click"]}
        menu={{
          items: [
            ...(active ? [{ key: "same_key", label: "添加同 Key 模型" }] : []),
            { key: "capability", label: "完整能力检测" },
            ...(active && !config.is_default ? [{ key: "current", label: conversationLabel, disabled: !canUseForConversation }] : []),
            ...(config.is_default ? [{ key: "local", label: "日常对话切回本地模型" }] : []),
          ],
          onClick: ({ key }) => selectAction(key),
        }}
      >
        <Button size="small" type="text" disabled={busy} icon={<MoreHorizontal size={15} />} aria-label={`${config.display_name}更多操作`} />
      </Dropdown>
      <Tooltip title={toggleHint}>
        <Button size="small" danger={active} disabled={busy} loading={options.updatingID === config.id}
          onClick={() => {
            // 停用取消默认选择；恢复只启用配置，不自动成为日常对话模型。
            void options.updateUsage(config, active ? "disabled" : "active", false);
          }}>{active ? "停用" : "恢复"}</Button>
      </Tooltip>
      <Tooltip title={config.is_default ? "请先停用或切换日常对话模型后再删除" : "删除模型配置"}>
        <Button size="small" danger disabled={busy || config.is_default} onClick={() => options.removeConfig(config)}>删除</Button>
      </Tooltip>
    </Space>
  );
}

// 列组件只展示状态并发出回调；请求互斥、学校切换和失败恢复仍由页面处理。
export function modelConfigColumns(options: ColumnOptions): TableColumnsType<ManagedModelAPIConfig> {
  return [
    {
      title: "模型", key: "identity", width: 195, fixed: "left",
      render: (_value, config) => (
        <div className="platform-model-identity">
          <span className={config.status === "active" ? "active" : "disabled"}><Zap size={16} /></span>
          <div><strong>{config.display_name}</strong><small>{config.model_name}</small><small>配置 {config.id.slice(0, 8)}</small></div>
        </div>
      ),
    },
    {
      title: "密钥", key: "credential", width: 130,
      render: (_value, config) => (
        <div className="platform-model-secret"><KeyRound size={14} /><span>已配置 · {config.credential_hint || "已加密"}</span></div>
      ),
    },
    {
      title: "连接状态", key: "probe", width: 210,
      render: (_value, config) => <ConnectionCell config={config} onShowDetails={options.showCapabilityDetails} />,
    },
    {
      title: "用途", key: "assignment", width: 120,
      render: (_value, config) => <UsageCell config={config} />,
    },
    {
      title: "操作", key: "actions", width: 285, fixed: "right",
      render: (_value, config) => <ModelActions config={config} options={options} />,
    },
  ];
}
