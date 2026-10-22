import { useState } from "react";
import { Alert, Button } from "antd";
import type { DocumentModelSelection, ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { capabilityVerified } from "../lib/modelConfig";

export function DocumentModelForm({ config, selection, busy, loading, error, onRefresh, onSave, onDirtyChange }: Readonly<{
  config: ManagedModelAPIConfig; selection?: DocumentModelSelection; busy: boolean; loading: boolean; error?: string;
  onRefresh: () => void; onSave: (mode: DocumentModelSelection["mode"]) => void; onDirtyChange: (dirty: boolean) => void;
}>) {
  const [mode, setMode] = useState<DocumentModelSelection["mode"]>("model");
  const canSelect = config.status === "active" && config.credential_configured && capabilityVerified(config);
  return <div className="platform-document-model-form">
    <p>为当前学校设置试卷解析模型，不改变对话开放范围或阅卷角色。</p>
    <p>当前设置：{selection?.mode === "model" ? selection.model_config_id === config.id ? `独立使用 ${config.model_name}` : "已独立指定其他模型" : "跟随默认配置"}</p>
    {error ? <Alert type="error" showIcon message={error} action={<Button disabled={busy || loading} onClick={onRefresh}>刷新设置</Button>} /> : null}
    <label><input type="radio" name="document-selection-mode" checked={mode === "model"} disabled={busy || !canSelect}
      onChange={() => { setMode("model"); onDirtyChange(true); }} />独立使用 {config.model_name}</label>
    <label><input type="radio" name="document-selection-mode" checked={mode === "follow_chat"} disabled={busy}
      onChange={() => { setMode("follow_chat"); onDirtyChange(true); }} />跟随默认配置</label>
    <small>{mode === "model" ? "解析需通过当前结构化输出检测；选择后仍会在每次解析时检查模型资格。" : "默认配置需通过结构化检测；未指定时使用本地解析。聊天页切换模型不会改变此设置。"}</small>
    <Button type="primary" loading={busy} disabled={loading || Boolean(error) || !selection || mode === "model" && !canSelect} onClick={() => onSave(mode)}>保存解析用途</Button>
  </div>;
}
