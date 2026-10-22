import { useState } from "react";
import { Alert, Button } from "antd";
import type { AnswerSheetModelSelection, ManagedModelAPIConfig } from "../../../api/modelApiConfig";

export function AnswerSheetModelForm({ config, selection, busy, loading, error, onRefresh, onSave, onDirtyChange }: Readonly<{
  config: ManagedModelAPIConfig; selection?: AnswerSheetModelSelection; busy: boolean; loading: boolean; error?: string;
  onRefresh: () => void; onSave: (mode: AnswerSheetModelSelection["mode"]) => void; onDirtyChange: (dirty: boolean) => void;
}>) {
  const [mode, setMode] = useState<AnswerSheetModelSelection["mode"]>("model");
  const canSelect = config.status === "active" && config.credential_configured;
  const current = selection?.mode === "model"
    ? selection.model_config_id === config.id ? `使用 ${config.model_name}` : "已指定其他图像理解模型"
    : "尚未配置多模态模型";
  return <div className="platform-document-model-form">
    <p>为当前学校独立设置答题卡识别模型，用于未知模板的题号理解与答题区域分析。</p>
    <p>当前设置：{current}</p>
    <Alert type="info" showIcon message="由管理员指定多模态模型"
      description="模型将直接分析答题卡图像，识别结果仍需人工核对。实际调用失败会显示原因。" />
    {error ? <Alert type="error" showIcon message={error} action={<Button disabled={busy || loading} onClick={onRefresh}>刷新设置</Button>} /> : null}
    {!canSelect ? <Alert type="warning" showIcon message="此模型须先启用并配置密钥。" /> : null}
    {!selection && !loading && !error ? <Alert type="warning" showIcon message="尚未读取到答题卡用途，请更新 API 服务后刷新设置。" /> : null}
    <label><input type="radio" name="answer-sheet-selection-mode" checked={mode === "model"} disabled={busy || !canSelect}
      onChange={() => { setMode("model"); onDirtyChange(true); }} />使用 {config.model_name} 识别答题卡</label>
    <label><input type="radio" name="answer-sheet-selection-mode" checked={mode === "unconfigured"} disabled={busy}
      onChange={() => { setMode("unconfigured"); onDirtyChange(true); }} />取消答题卡模型配置</label>
    <small>{mode === "model" ? "每次识别仍会检查模型资格；模型输出需人工核对后才能发布模板。此选择不改变日常对话、试卷解析或阅卷角色。" : "取消后未知模板将提示尚未配置多模态模型，不会静默改用 OCR 自动关联题目。"}</small>
    <Button type="primary" loading={busy} disabled={loading || Boolean(error) || !selection || mode === "model" && !canSelect}
      onClick={() => onSave(mode)}>保存答题卡用途</Button>
  </div>;
}
