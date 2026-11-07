import { Select } from "antd";
import { ChevronDown } from "lucide-react";
import type { SchoolChatModel } from "../../api/schoolAiChat";
import { conversationModelStatus } from "./conversationPresentation";

interface ModelOption {
  value: string;
  label: string;
  disabled: boolean;
  model: SchoolChatModel;
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
  const provider = (item: SchoolChatModel) => item.display_name || item.provider_key || "";
  const sameName = (item: SchoolChatModel) => (item.model_name || item.display_name || "").trim().toLowerCase() === name.trim().toLowerCase();
  // 同名模型可能走不同供应商或密钥，数量按配置编号计算，不能按模型名称合并。
  const currentLabel = current && models.filter(sameName).length > 1 ? `${name} · ${provider(current)}` : name;
  const options: ModelOption[] = models.flatMap(item => item.id ? [{
    value: item.id,
    label: `${item.model_name || item.display_name || "对话模型"}${item.display_name && item.display_name !== item.model_name ? ` · ${item.display_name}` : ""}${item.available ? "" : "（不可用）"}`,
    disabled: !item.available,
    model: item,
  }] : []);
  const availableCount = options.filter(item => !item.disabled).length;
  const unavailableCount = options.length - availableCount;
  const unavailableReason = (item: SchoolChatModel) => item.message || "模型暂不可用，请联系平台管理员";

  return <div className="school-chat-model-status" title={loading ? "正在读取模型" : `${currentLabel}${current?.available ? "" : `：${current ? unavailableReason(current) : "模型未就绪"}`}`}>
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
      popupRender={menu => <><div className="school-chat-model-summary">已开放 {options.length} 项 · 可用 {availableCount} 项{unavailableCount ? ` · 不可用 ${unavailableCount} 项` : ""}</div>{menu}</>}
      labelRender={() => <span title={currentLabel}>{currentLabel}{current?.available ? "" : "（不可用）"}</span>}
      optionRender={option => <div className="school-chat-model-option">
        <strong>{option.data.model.model_name || option.data.model.display_name || "对话模型"}</strong>
        <span>{provider(option.data.model)}{option.data.disabled ? `${provider(option.data.model) ? " · " : ""}不可用` : ""}</span>
        {option.data.disabled ? <span>{unavailableReason(option.data.model)}</span> : null}
      </div>}
      onChange={onChange}
    /> : <span className="school-chat-model-label" title={current?.available ? name : undefined}>
      {loading || !current?.available ? conversationModelStatus(current, loading) : name}
    </span>}
  </div>;
}
