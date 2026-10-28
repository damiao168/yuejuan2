import { useEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { probeGradingModelSelection, type AnswerSheetModelSelection, type ManagedModelAPIConfig } from "../../../api/modelApiConfig";

export function ImageGradingModelForm({ tenantID, config, selection, busy, loading, error, onRefresh, onSave, onDirtyChange }: Readonly<{
  tenantID: string; config: ManagedModelAPIConfig; selection?: AnswerSheetModelSelection; busy: boolean; loading: boolean; error?: string;
  onRefresh: () => void; onSave: (mode: AnswerSheetModelSelection["mode"]) => boolean | void | Promise<boolean | void>; onDirtyChange: (dirty: boolean) => void;
}>) {
  const [probeBusy, setProbeBusy] = useState(false);
  const [probeResult, setProbeResult] = useState<{ ok: boolean; text: string }>();
  const [verifySaved, setVerifySaved] = useState<string>();
  const identity = `${tenantID}:${config.id}`;
  const liveIdentity = useRef<string | undefined>(identity);
  liveIdentity.current = identity;
  useEffect(() => { liveIdentity.current = identity; return () => { liveIdentity.current = undefined; }; }, [identity]);
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
      if (current.current.generation === scope) setProbeResult({ ok: result.capability.visual_supported, text: result.capability.visual_supported ? "图片读取检测通过；正式评分仍须满足准入与确认策略。" : "模型未通过图片读取检测，请更换支持图像的模型。" });
    } catch (cause) { if (current.current.generation === scope) setProbeResult({ ok: false, text: getUserErrorMessage(cause, "图像能力检测失败，请核对接口和模型。") }); }
    finally { if (current.current.generation === scope) { probing.current = undefined; setProbeBusy(false); } }
  };
  // 保存确认后等用途状态回填，再验证；两步各自生效，检测失败不回滚已保存用途。
  // biome-ignore lint/correctness/useExhaustiveDependencies: probe 使用当前渲染快照，非稳定函数不能触发重复检测。
  useEffect(() => {
    if (verifySaved !== identity || !selected || busy || loading) return;
    setVerifySaved(undefined);
    if (available && !error) void probe();
  }, [verifySaved, identity, selected, busy, loading, available, error]);
  return <section className="platform-document-model-form">
    <h3>自动阅卷模型</h3>
    <p>生成评分建议，教师确认最终分数。本校自动阅卷独立配置 API 模型，不回退本地模型。</p>
    <p>当前设置：{selected ? config.model_name : selection?.mode === "model" ? "已选择其他 API 模型" : "尚未配置"}</p>
    {error ? <Alert type="error" message={error} action={<Button onClick={onRefresh}>刷新设置</Button>} /> : null}
    {probeResult ? <Alert type={probeResult.ok ? "success" : "error"} showIcon message={probeResult.text} /> : null}
    <Space wrap>
      <Button type="primary" loading={busy} disabled={loading || Boolean(error) || !selection || !available || probeBusy} onClick={() => {
        onDirtyChange(false);
        void Promise.resolve(onSave("model")).then(saved => { if (saved && liveIdentity.current === identity) setVerifySaved(identity); });
      }}>设置并验证图片能力</Button>
      {selected ? <Button loading={probeBusy} disabled={busy || loading || !available || Boolean(error)} onClick={() => void probe()}>检测图片能力</Button> : null}
      {selected ? <Button disabled={busy || probeBusy} onClick={() => onSave("unconfigured")}>取消用途</Button> : null}
    </Space>
    <small>先保存本校自动阅卷模型，再发送合成图片验证，会产生少量模型用量。检测失败会保留已保存用途，可重试检测；密钥停用或配置变化后需重新检测。</small>
  </section>;
}
