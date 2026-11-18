import { Select } from "antd";
import { ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
import type { SchoolChatModel } from "../../api/schoolAiChat";
import { conversationModelStatus } from "./conversationPresentation";

interface ModelOption {
  value: string;
  label: string;
  disabled: boolean;
  model: SchoolChatModel;
}

function modelProvider(model: SchoolChatModel) {
  return model.display_name || model.provider_key || "";
}

function modelUnavailableReason(model: SchoolChatModel) {
  return model.message || "模型暂不可用，请联系平台管理员";
}

function ConversationOption({ option }: Readonly<{
  option: { data: ModelOption };
}>) {
  const model = option.data.model;
  const unavailableLabel = modelProvider(model) ? " · 不可用" : "不可用";
  return <div className="school-chat-model-option">
    <strong>{model.model_name || model.display_name || "对话模型"}</strong>
    <span>{modelProvider(model)}{option.data.disabled ? unavailableLabel : ""}</span>
    {option.data.disabled ? <span>{modelUnavailableReason(model)}</span> : null}
  </div>;
}

function renderConversationOption(option: { data: ModelOption }) {
  return <ConversationOption option={option} />;
}

function createPopupRenderer(total: number, available: number, unavailable: number) {
  return (menu: ReactNode) => <>
    <div className="school-chat-model-summary">已开放 {total} 项 · 可用 {available} 项{unavailable ? ` · 不可用 ${unavailable} 项` : ""}</div>
    {menu}
  </>;
}

function createLabelRenderer(label: string, available: boolean) {
  return () => <span title={label}>{label}{available ? "" : "（不可用）"}</span>;
}

export function ConversationModelSelector({ models, model, value, loading, pending, onChange }: Readonly<{
  models: SchoolChatModel[];
  model: SchoolChatModel | null;
  value?: string;
  loading: boolean;
  pending: boolean;
  onChange: (id: string) => void;
}>) {
  // 当前回复沿用发送时固定的模型；不能用新对话的偏好覆盖在途对话的显示。
  const current = models.find(item => item.id === value) ?? model;
  const name = current?.model_name || current?.display_name || "对话模型";
  const sameName = (item: SchoolChatModel) => (item.model_name || item.display_name || "").trim().toLowerCase() === name.trim().toLowerCase();
  // 同名模型可能走不同供应商或密钥，数量按配置编号计算，不能按模型名称合并。
  const currentLabel = current && models.filter(sameName).length > 1 ? `${name} · ${modelProvider(current)}` : name;
  const options: ModelOption[] = models.flatMap(item => {
    const displaySuffix = item.display_name && item.display_name !== item.model_name ? ` · ${item.display_name}` : "";
    return item.id ? [{
    value: item.id,
    label: `${item.model_name || item.display_name || "对话模型"}${displaySuffix}${item.available ? "" : "（不可用）"}`,
    disabled: !item.available,
    model: item,
    }] : [];
  });
  const availableCount = options.filter(item => !item.disabled).length;
  const unavailableCount = options.length - availableCount;
  let title = "正在读取模型";
  if (!loading) {
    const reason = current ? modelUnavailableReason(current) : "模型未就绪";
    const reasonSuffix = current?.available ? "" : `：${reason}`;
    title = `${currentLabel}${reasonSuffix}`;
  }

  return <div className="school-chat-model-status" title={title}>
    <i aria-hidden="true" className={current?.available && !loading ? "is-online" : ""} />
    {options.length > 1 ? <Select<string, ModelOption>
      aria-label="切换对话模型"
      className="school-chat-model-selector"
      classNames={{ popup: { root: "school-chat-model-menu" } }}
      variant="borderless"
      size="small"
      placement="topLeft"
      value={value}
      loading={loading}
      disabled={loading || pending}
      options={options}
      suffixIcon={<ChevronDown size={13} />}
      popupMatchSelectWidth={false}
       popupRender={createPopupRenderer(options.length, availableCount, unavailableCount)}
       labelRender={createLabelRenderer(currentLabel, current?.available ?? false)}
       optionRender={renderConversationOption}
      onChange={onChange}
    /> : <span className="school-chat-model-label" title={current?.available ? name : undefined}>
      {loading || !current?.available ? conversationModelStatus(current, loading) : name}
    </span>}
  </div>;
}
