import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Input, Select } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { rotateManagedModelAPIConfigCredential, type ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import type { ModelOperationQueue } from "../lib/modelOperationQueue";
import { isSameModelAPIOrigin } from "../lib/modelConfig";
import { ModelConfigDrawer } from "./ModelConfigDrawer";

type RotationResult = { id: string; name: string; state: "pending" | "saved" | "unconfirmed"; message?: string };

export function credentialRotationTargets(configs: ManagedModelAPIConfig[], tenantID: string, source: ManagedModelAPIConfig) {
  return configs.filter(config => config.tenant_id === tenantID && config.provider_key === source.provider_key && isSameModelAPIOrigin(source.base_url, config.base_url));
}

export function CredentialRotation({ tenantID, configs, disabled, queue, onConfigSaved, onStateChange }: Readonly<{
  tenantID: string;
  configs: ManagedModelAPIConfig[];
  disabled: boolean;
  queue: ModelOperationQueue;
  onConfigSaved: (tenantID: string, config: ManagedModelAPIConfig) => void;
  onStateChange: (open: boolean, busy: boolean) => void;
}>) {
  const [open, setOpen] = useState(false);
  const [sourceID, setSourceID] = useState("");
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const [apiKey, setAPIKey] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [results, setResults] = useState<RotationResult[]>([]);
  const current = useRef({ tenantID, configs, generation: 0 });
  current.current = { tenantID, configs, generation: current.current.generation + (current.current.tenantID === tenantID ? 0 : 1) };
  const mounted = useRef(true);
  const saving = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { setOpen(false); setAPIKey(""); setBusy(false); setSelectedIDs([]); setResults([]); setConfirmed(false); setError(""); }, [tenantID]);
  useEffect(() => { onStateChange(open, busy); }, [onStateChange, open, busy]);
  useEffect(() => () => onStateChange(false, false), [onStateChange]);
  const source = configs.find(config => config.id === sourceID && config.tenant_id === tenantID);
  const targets = source ? credentialRotationTargets(configs, tenantID, source) : [];
  const close = () => {
    if (saving.current) return;
    setOpen(false); setAPIKey(""); setConfirmed(false); setResults([]); setError("");
  };
  const submit = async () => {
    if (saving.current || disabled || !source || !confirmed || apiKey.trim().length < 16 || apiKey.length > 1024 || /[\r\n\0]/.test(apiKey) || !selectedIDs.length) return;
    const selected = targets.filter(config => selectedIDs.includes(config.id));
    if (selected.length !== selectedIDs.length || selected.some(config => !config.updated_at)) { setError("所选配置已变化，请关闭后重新选择。"); return; }
    const owner = tenantID;
    const generation = current.current.generation;
    const secret = apiKey.trim();
    const releases: (() => void)[] = [];
    // 先取得整批本页写锁；一项忙碌就整批等待，不能边检测边切换同一配置的地址或默认状态。
    for (const config of selected) {
      const release = queue.acquireWrite(owner, config.id);
      if (!release) { releases.forEach(unlock => unlock()); setError("所选模型仍在检测或更新，请完成后重试。"); return; }
      releases.push(release);
    }
    saving.current = true; setBusy(true); onStateChange(true, true); setError(""); setAPIKey(""); setConfirmed(false);
    const next: RotationResult[] = selected.map(config => ({ id: config.id, name: config.display_name, state: "pending" }));
    setResults([...next]);
    // A→B→A 也属于另一轮学校上下文；旧轮换的结果不能回填新向导或继续提交剩余模型。
    const active = () => mounted.current && current.current.tenantID === owner && current.current.generation === generation;
    try {
      for (let index = 0; index < selected.length && active(); index += 1) {
        const config = selected[index];
        try {
          const latest = current.current.configs.find(item => item.id === config.id);
          if (!latest || latest.updated_at !== config.updated_at || !credentialRotationTargets([latest], owner, source).length) throw new Error("配置已变化，请重新核对后再轮换");
          // 只发送密钥和冻结版本；服务端补全其他字段，并在连接检测后的写锁内再次核对版本。
          const response = await rotateManagedModelAPIConfigCredential(config.id, owner, secret, config.updated_at);
          if (!active()) return;
          if (response.config.id !== config.id || response.config.tenant_id !== owner) throw new Error("返回的配置与本次学校或模型不一致，请刷新核对");
          onConfigSaved(owner, response.config);
          next[index] = { ...next[index], state: "saved" };
        } catch (cause) {
          if (!active()) return;
          next[index] = { ...next[index], state: "unconfirmed", message: getUserErrorMessage(cause, "更新未确认，请重新读取配置后核对") };
          if (cause instanceof ApiClientError && (cause.status === 401 || cause.status === 403)) {
            setError("当前账号无法继续更新，其余模型未提交。请核对登录和权限。");
            setResults([...next]);
            break;
          }
        }
        if (active()) setResults([...next]);
      }
    } finally {
      releases.forEach(unlock => unlock()); saving.current = false;
      if (active()) { setBusy(false); onStateChange(true, false); }
    }
  };
  return <>
    <Button disabled={disabled || !tenantID || !configs.length} onClick={() => { setSourceID(""); setSelectedIDs([]); setOpen(true); }}>批量更换密钥</Button>
    {open ? <ModelConfigDrawer title="批量更换学校模型密钥" width={620} open closable={!busy} maskClosable={!busy} keyboard={!busy} onClose={close} extra={<Button type="primary" loading={busy} disabled={!confirmed || !selectedIDs.length || apiKey.trim().length < 16 || disabled} onClick={() => void submit()}>确认更换所选密钥</Button>}>
      <p>选择同一供应商、同一 API 域名下共用新密钥的模型。每项分别检查连接并保存，结果逐项显示。当前默认对话模型会验证文本回复并产生少量模型用量；其他配置只检查连接。</p>
      <label>供应商连接<Select aria-label="供应商连接" value={sourceID || undefined} disabled={busy} placeholder="选择一个模型的供应商连接" options={configs.filter(config => config.tenant_id === tenantID && isSameModelAPIOrigin(config.base_url, config.base_url)).map(config => ({ value: config.id, label: `${config.display_name} · ${config.provider_key} · ${new URL(config.base_url).origin}` }))} onChange={id => { setSourceID(id); setSelectedIDs([]); setConfirmed(false); setResults([]); setError(""); }} /></label>
      {source ? <div><p>仅当前学校 · {source.provider_key} · {new URL(source.base_url).origin}</p><Checkbox.Group value={selectedIDs} disabled={busy} onChange={values => { setSelectedIDs(values.map(String)); setConfirmed(false); }}>{targets.map(config => <div key={config.id}><Checkbox value={config.id}>{config.display_name} · {config.model_name} · {config.base_url}</Checkbox></div>)}</Checkbox.Group></div> : null}
      <label>新 API Key<Input.Password aria-label="新 API Key" autoComplete="new-password" maxLength={1024} value={apiKey} disabled={busy} onChange={event => { setAPIKey(event.target.value); setConfirmed(false); }} /></label>
      <Checkbox checked={confirmed} disabled={busy} onChange={event => setConfirmed(event.target.checked)}>我已核对所选模型使用同一份新密钥</Checkbox>
      {error ? <Alert type="error" showIcon message={error} /> : null}
      {results.length ? <section aria-label="密钥更新结果"><p>已确认更新 {results.filter(result => result.state === "saved").length} / {results.length} 项。</p>{results.map(result => <p key={result.id}>{result.name}：{result.state === "saved" ? "已更新，其他用途能力需重新验证" : result.state === "unconfirmed" ? `未确认：${result.message}` : busy ? "等待处理" : "未提交"}</p>)}</section> : null}
      <p>新密钥仅用于本次请求，不保存为浏览器草稿；关闭后清除输入。未确认项请刷新核对，再重新选择需要更新的模型。</p>
    </ModelConfigDrawer> : null}
  </>;
}
