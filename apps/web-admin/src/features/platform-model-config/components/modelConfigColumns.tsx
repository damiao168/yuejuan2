import { Button, Tooltip, type TableColumnsType } from "antd";
import { Zap } from "lucide-react";
import type { ManagedAPIProbeMode, ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { StatusTag } from "../../../components/StatusTag";
import type { StatusTone } from "../../../types";
import { capabilityVerified, formatDateTime, textVerified } from "../lib/modelConfig";
import { modelOperationKey, type ModelOperation } from "../lib/modelOperationQueue";

type ColumnOptions = Readonly<{

  chatModelIDs?: readonly string[];
  chatAccessBusy?: boolean;
  toggleChatAccess?: (id: string) => Promise<void>;
  tenantID: string;
  operations: Readonly<Record<string, ModelOperation>>;
  credentialHintIDs: ReadonlySet<string>;
  applicationsReady: boolean;
  gradingModelIDs: ReadonlySet<string>;
  documentModelID?: string;
  documentFollowsChat: boolean;
  answerSheetReady: boolean;
  answerSheetModelID?: string;
  openApplication: (config: ManagedModelAPIConfig, type: "grading" | "document" | "answer_sheet") => void;
  openEdit: (config: ManagedModelAPIConfig) => void;
  probe: (config: ManagedModelAPIConfig, mode: ManagedAPIProbeMode) => Promise<void>;
  removeConfig: (config: ManagedModelAPIConfig) => void;
  showConnectionDetails: (config: ManagedModelAPIConfig) => void;
  showTextDetails: (config: ManagedModelAPIConfig) => void;
  showCapabilityDetails: (config: ManagedModelAPIConfig) => void;
  updateUsage: (config: ManagedModelAPIConfig, status: "active" | "disabled", isDefault: boolean) => Promise<void>;
}>;

type ProbeColumnMode = "quick" | "text" | "structured";
const probeActionLabels: Record<ProbeColumnMode, string> = {
  quick: "检测连接", text: "检测普通回复", structured: "检测结构化输出",
};

function probePresentation(config: ManagedModelAPIConfig, mode: ProbeColumnMode): { label: string; tone: StatusTone } {
  const status = mode === "quick" ? config.last_test_status : mode === "text" ? config.last_text_status : config.last_capability_status;
  if (status === "success") {
    if (mode === "structured" && !capabilityVerified(config)) return { label: "待复验", tone: "warning" };
    return { label: mode === "quick" ? "连接正常" : "通过", tone: "success" };
  }
  if (status === "temporary_unavailable") {
    const timedOut = mode === "quick" && /超时|timeout|timed out/i.test(config.last_test_message ?? "");
    return { label: timedOut ? "连接超时" : "未完成", tone: "warning" };
  }
  if (status === "failed") return { label: mode === "quick" ? "连接失败" : "未通过", tone: "danger" };
  return { label: mode === "quick" ? "未检测" : "未验证", tone: "neutral" };
}

function ProbeCell({ config, mode, options }: Readonly<{
  config: ManagedModelAPIConfig;
  mode: ProbeColumnMode;
  options: ColumnOptions;
}>) {
  const operation = options.operations[modelOperationKey(options.tenantID, config.id)];
  // 每列只显示本模式的进行状态；同一配置的其他检测和写入仍按队列互斥。
  const currentOperation = operation && (operation.mode === mode || mode === "structured" && operation.mode === "capability") ? operation : undefined;
  const presentation = currentOperation
    ? { label: currentOperation.phase === "queued" ? "排队中" : "检测中", tone: "info" as const }
    : probePresentation(config, mode);
  const message = mode === "quick" ? config.last_test_message : mode === "text" ? config.last_text_message : config.last_capability_message;
  const testedAt = mode === "quick" ? config.last_tested_at : mode === "text" ? config.last_text_tested_at : config.last_capability_tested_at;
  const status = mode === "quick" ? config.last_test_status : mode === "text" ? config.last_text_status : config.last_capability_status;
  // 兼容旧响应只在连接记录中标记结构化请求中断的情况，仍保留其诊断入口。
  const interruptedStructured = mode === "structured" && config.last_test_status === "temporary_unavailable" && (config.last_probe_mode === "structured" || config.last_probe_mode === "capability");
  const hasDetails = Boolean(status && status !== "untested" || testedAt || message || interruptedStructured);
  const actionLabel = probeActionLabels[mode];
  const detailLabel = `查看${mode === "quick" ? "连接" : mode === "text" ? "普通回复" : "结构化"}详情`;
  const showDetails = () => {
    if (mode === "quick") options.showConnectionDetails(config);
    else if (mode === "text") options.showTextDetails(config);
    else options.showCapabilityDetails(config);
  };
  return (
    <div className="platform-model-check-cell">
      <Tooltip title={<div>{message || "暂无检测结果"}<br />最近检测：{formatDateTime(testedAt)}{mode === "quick" && config.last_test_latency_ms !== undefined ? <><br />耗时：{config.last_test_latency_ms}ms</> : null}</div>}>
        {hasDetails && !currentOperation ? <button type="button" className="platform-model-status-button"
          aria-label={`${config.model_name}：${detailLabel}`} onClick={showDetails}>
          <StatusTag tone={presentation.tone}>{presentation.label}</StatusTag>
        </button> : <span><StatusTag tone={presentation.tone}>{presentation.label}</StatusTag></span>}
      </Tooltip>
      <Button className="platform-model-check-action" type="link" size="small" disabled={Boolean(operation)}
        loading={currentOperation?.phase === "running"} aria-label={`${config.model_name}：${actionLabel}`}
        title={mode === "quick" ? "只检查接口、密钥和模型权限，不生成回复" : "发送合成测试内容，会产生少量模型用量"}
        onClick={() => void options.probe(config, mode)}>检测</Button>
    </div>
  );
}

function ConversationCell({ config, options }: Readonly<{ config: ManagedModelAPIConfig; options: ColumnOptions }>) {
  if (options.toggleChatAccess) {
    const selected = Boolean(options.chatModelIDs?.includes(config.id));
    const busy = options.chatAccessBusy || !options.chatModelIDs || Boolean(options.operations[modelOperationKey(options.tenantID, config.id)]);
    return <Tooltip title={selected ? "已开放，学校管理员可在聊天页选择；点击取消开放" : "开放给学校对话使用，可同时选择多个供应商"}><span>
      <label className={`platform-model-purpose platform-model-dialogue${selected ? " is-selected" : ""}`}>
        <input type="checkbox" checked={selected} disabled={busy || !selected && (config.status !== "active" || !config.credential_configured || !textVerified(config))}
          aria-label={`${config.model_name}：允许学校对话`} onChange={() => void options.toggleChatAccess?.(config.id)} />对话{selected ? " ✓" : ""}
      </label>
    </span></Tooltip>;
  }
  return <span className="platform-model-purpose" aria-disabled="true">对话</span>;
}

function ApplicationCell({ config, options }: Readonly<{ config: ManagedModelAPIConfig; options: ColumnOptions }>) {
  const verified = config.status === "active" && config.credential_configured && capabilityVerified(config);
  const grading = options.gradingModelIDs.has(config.id);
  const parsing = options.documentModelID === config.id;
  const answerSheet = options.answerSheetModelID === config.id;
  const canUseAnswerSheet = config.status === "active" && config.credential_configured;
  // 自动阅卷另有合成图片检测，不能用文本结构化探测作为打开用途设置的前提。
  const canUseGrading = config.status === "active" && config.credential_configured;
  const busy = Boolean(options.operations[modelOperationKey(options.tenantID, config.id)] || options.operations[modelOperationKey(options.tenantID, "__document__")]);
  const hint = !options.applicationsReady ? "用途配置尚未读取成功，请刷新" : !verified ? "请先启用模型并通过结构化输出检测" : undefined;
  return <div className="platform-model-applications">
    <ConversationCell config={config} options={options} />
    <Tooltip title={grading ? "当前自动阅卷模型，点击可修改或取消" : !options.applicationsReady ? "用途配置尚未读取成功，请刷新" : !canUseGrading ? "请先启用模型并配置密钥" : "选择自动阅卷模型并检测图片能力"}><span>
      <button type="button" className={`platform-model-purpose${grading ? " is-selected" : ""}`} disabled={!options.applicationsReady || busy || !canUseGrading && !grading}
        aria-label={`${config.model_name}：阅卷配置`} onClick={() => options.openApplication(config, "grading")}>{grading ? "阅卷·已配置" : "阅卷配置"}</button>
    </span></Tooltip>
    <Tooltip title={answerSheet ? "当前答题卡识别模型，点击可修改或取消" : !options.answerSheetReady ? "答题卡用途尚未读取成功，请刷新" : !canUseAnswerSheet ? "请先启用模型并配置密钥" : "用于未知答题卡模板的多模态分析"}><span>
      <button type="button" className={`platform-model-purpose${answerSheet ? " is-selected" : ""}`}
        disabled={!options.applicationsReady || !options.answerSheetReady || busy || Boolean(options.operations[modelOperationKey(options.tenantID, "__answer_sheet__")]) || !canUseAnswerSheet && !answerSheet}
        aria-label={`${config.model_name}：答题卡识别`} onClick={() => options.openApplication(config, "answer_sheet")}>{answerSheet ? "答题卡 ✓" : "答题卡识别"}</button>
    </span></Tooltip>
    <Tooltip title={parsing ? options.documentFollowsChat ? "解析跟随默认配置，点击可独立指定模型" : "当前独立解析模型，点击修改" : hint ?? "设置试卷解析模型"}><span>
      <button type="button" className={`platform-model-purpose${parsing ? " is-selected" : ""}`} disabled={!options.applicationsReady || busy || !verified && !parsing}
        aria-label={`${config.model_name}：试卷解析`} onClick={() => options.openApplication(config, "document")}>{parsing ? options.documentFollowsChat ? "解析·跟随" : "解析 ✓" : "试卷解析"}</button>
    </span></Tooltip>
  </div>;
}

function ModelActions({ config, options }: Readonly<{ config: ManagedModelAPIConfig; options: ColumnOptions }>) {
  const active = config.status === "active";
  const operation = options.operations[modelOperationKey(options.tenantID, config.id)];
  const busy = Boolean(operation);
  const defaultBusy = Boolean(options.operations[modelOperationKey(options.tenantID, "__default__")]);
  const toggleHint = active && config.is_default ? "停用后取消默认配置；恢复后不会自动设为默认" : undefined;

  return (
    <div className="platform-model-row-actions">
      <Tooltip title="编辑配置或更换密钥">
        <Button size="small" type="link" aria-label={`编辑${config.display_name}`} onClick={() => options.openEdit(config)}>编辑</Button>
      </Tooltip>
      <Tooltip title={toggleHint}>
        <Button size="small" type="link" className={active ? "platform-model-disable" : "platform-model-enable"}
          disabled={busy || config.is_default && defaultBusy} loading={operation?.mode === "write"}
          onClick={() => {
            // 停用取消默认配置；对话开放范围独立保留，恢复后仍按开放范围检查可用资格。
            void options.updateUsage(config, active ? "disabled" : "active", false);
          }}>{active ? "停用" : "启用"}</Button>
      </Tooltip>
      <Tooltip title={config.is_default ? "请先清除默认配置或停用后再删除" : "删除模型配置"}>
        <Button size="small" type="link" danger disabled={busy || config.is_default} onClick={() => options.removeConfig(config)}>删除</Button>
      </Tooltip>
    </div>
  );
}

// 只有同一模型的检测和写入互斥；查看及打开编辑始终可用，保存按钮在侧栏单独校验。
export function modelConfigColumns(options: ColumnOptions): TableColumnsType<ManagedModelAPIConfig> {
  return [
    {
      title: "模型名称", key: "model-name", width: 230, fixed: "left",
      render: (_value, config) => (
        <div className="platform-model-identity">
          <span className={config.status === "active" ? "active" : "disabled"}><Zap size={16} /></span>
          <div><strong>{config.model_name}</strong>{options.credentialHintIDs.has(config.id) ? <small title="同名模型的密钥尾号，辅助区分配置">密钥 {config.credential_hint || "已加密"}</small> : null}</div>
        </div>
      ),
    },
    {
      title: "供应商", key: "provider", width: 130,
      render: (_value, config) => <span className="platform-model-provider">{config.display_name}</span>,
    },
    {
      title: "连接结果", key: "connection", width: 145,
      render: (_value, config) => <ProbeCell config={config} mode="quick" options={options} />,
    },
    {
      title: "普通回复", key: "text", width: 130,
      render: (_value, config) => <ProbeCell config={config} mode="text" options={options} />,
    },
    {
      title: "结构化输出", key: "structured", width: 130,
      render: (_value, config) => <ProbeCell config={config} mode="structured" options={options} />,
    },
    {
      title: "应用用途", key: "assignment", width: 320,
      render: (_value, config) => <ApplicationCell config={config} options={options} />,
    },
    {
      title: "操作", key: "actions", width: 200, fixed: "right",
      render: (_value, config) => <ModelActions config={config} options={options} />,
    },
  ];
}
