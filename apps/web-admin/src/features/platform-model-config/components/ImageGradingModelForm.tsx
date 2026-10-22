import { useEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { probeGradingModelSelection, type AnswerSheetModelSelection, type ManagedModelAPIConfig } from "../../../api/modelApiConfig";

export function ImageGradingModelForm({ tenantID, config, selection, busy, loading, error, onRefresh, onSave, onDirtyChange }: Readonly<{
  tenantID: string; config: ManagedModelAPIConfig; selection?: AnswerSheetModelSelection; busy: boolean; loading: boolean; error?: string;
  onRefresh: () => void; onSave: (mode: AnswerSheetModelSelection["mode"]) => void; onDirtyChange: (dirty: boolean) => void;
}>) {
  const [probeBusy, setProbeBusy] = useState(false);
  const [probeResult, setProbeResult] = useState<{ ok: boolean; text: string }>();
  // 同一配置换密钥、地址或模型后也属于新检测；旧请求不能给新连接显示通过或继续锁住按钮。
  const owner = JSON.stringify([tenantID, config.id, config.updated_at, config.adapter_type, config.base_url, config.model_name, config.model_version, config.credential_hint, config.credential_configured, config.status, selection?.revision]);
  const current = useRef({ owner, generation: 0 });
  const probing = useRef<number | undefined>(undefined);
  if (current.current.owner !== owner) current.current = { owner, generation: current.current.generation + 1 };
  useEffect(() => { setProbeBusy(false); setProbeResult(undefined); }, [owner]);
  useEffect(() => () => { current.current.generation += 1; }, []);
  const selected = selection?.mode === "model" && selection.model_config_id === config.id;
  const available = config.status === "active" && config.credential_configured;
  const probe = async () => {
    const scope = current.current.generation;
    if (probing.current === scope || !selected || !available) return;
    probing.current = scope; setProbeBusy(true); setProbeResult(undefined);
    try {
      const result = await probeGradingModelSelection(tenantID);
      if (current.current.generation === scope) setProbeResult({ ok: result.capability.visual_supported, text: result.capability.visual_supported ? "图片读取检测通过，可用于自动阅卷。" : "模型未通过图片读取检测，请更换支持图像的模型。" });
    } catch (cause) { if (current.current.generation === scope) setProbeResult({ ok: false, text: getUserErrorMessage(cause, "图像能力检测失败，请核对接口和模型。") }); }
    finally { if (current.current.generation === scope) { probing.current = undefined; setProbeBusy(false); } }
  };
  return <section className="platform-document-model-form">
    <h3>自动阅卷模型</h3>
    <p>识别学生答题图并生成评分建议，教师确认最终分数。此用途单独保存，不使用本地模型。</p>
    <p>当前可先使用一个 API 模型测试；多模型评审角色为可选配置，不影响单模型自动阅卷。</p>
    <p>当前设置：{selected ? config.model_name : selection?.mode === "model" ? "已选择其他 API 模型" : "尚未配置"}</p>
    {error ? <Alert type="error" message={error} action={<Button onClick={onRefresh}>刷新设置</Button>} /> : null}
    {probeResult ? <Alert type={probeResult.ok ? "success" : "error"} showIcon message={probeResult.text} /> : null}
    <Space wrap>
      <Button type="primary" loading={busy} disabled={loading || Boolean(error) || !selection || !available || probeBusy} onClick={() => { onDirtyChange(false); onSave("model"); }}>使用此模型自动阅卷</Button>
      {selected ? <Button loading={probeBusy} disabled={busy || loading || !available || Boolean(error)} onClick={() => void probe()}>检测图片能力</Button> : null}
      {selected ? <Button disabled={busy || probeBusy} onClick={() => onSave("unconfigured")}>取消用途</Button> : null}
    </Space>
    <small>先保存，再检测图片能力。检测发送合成图片，会产生少量模型用量；密钥停用或配置变化后需要重新检测。</small>
  </section>;
}
