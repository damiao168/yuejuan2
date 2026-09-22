import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App as AntApp, Button, Input, InputNumber, Select, Switch } from "antd";
import { RefreshCw } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import {
  listPanelModelRoleBindings,
  savePanelModelRoleBinding,
  type ManagedModelAPIConfig,
  type PanelAgentRole,
  type PanelEducationStage,
  type PanelModelRoleBinding
} from "../api/modelApiConfig";

const roles: { key: PanelAgentRole; title: string; description: string; rank: number }[] = [
  { key: "primary_a", title: "主评 A", description: "独立盲评", rank: 1 },
  { key: "primary_b", title: "主评 B", description: "独立盲评，不读取 A 的结果", rank: 1 },
  { key: "arbiter", title: "仲裁 C", description: "更强的独立盲评，不读取 A/B 分数", rank: 2 }
];

const subjects = [
  ["chinese", "语文"], ["mathematics", "数学"], ["english", "英语"],
  ["physics", "物理"], ["chemistry", "化学"], ["biology", "生物"],
  ["history", "历史"], ["geography", "地理"], ["ethics_politics", "思想政治"]
].map(([value, label]) => ({ value, label }));

type RoleDraft = Pick<PanelModelRoleBinding, "managed_model_api_config_id" | "prompt_version" | "strength_rank" | "status">;
type RoleDrafts = Record<PanelAgentRole, RoleDraft>;

function emptyDrafts(): RoleDrafts {
  return {
    primary_a: { managed_model_api_config_id: "", prompt_version: "", strength_rank: 1, status: "active" },
    primary_b: { managed_model_api_config_id: "", prompt_version: "", strength_rank: 1, status: "active" },
    arbiter: { managed_model_api_config_id: "", prompt_version: "", strength_rank: 2, status: "active" }
  };
}

function capabilityVerified(config: ManagedModelAPIConfig) {
  return config.status === "active" && config.last_capability_status === "success" &&
    config.last_capability_probe_version === "structured-json-v3";
}

export function PanelModelBindingsSection({ tenantID, configs, loadingConfigs = false }: { tenantID: string; configs: ManagedModelAPIConfig[]; loadingConfigs?: boolean }) {
  const { message } = AntApp.useApp();
  const [stage, setStage] = useState<PanelEducationStage>("senior");
  const [subject, setSubject] = useState("mathematics");
  const [archetypeInput, setArchetypeInput] = useState("*");
  const [archetype, setArchetype] = useState("*");
  const [bindings, setBindings] = useState<PanelModelRoleBinding[]>([]);
  const [drafts, setDrafts] = useState<RoleDrafts>(emptyDrafts);
  const [loading, setLoading] = useState(false);
  const [savingRole, setSavingRole] = useState<PanelAgentRole | null>(null);
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const requestID = ++requestRef.current;
    setBindings([]);
    setDrafts(emptyDrafts());
    if (!tenantID) return;
    setLoading(true);
    try {
      const response = await listPanelModelRoleBindings(tenantID, stage, subject, archetype);
      if (requestID !== requestRef.current) return;
      const next = emptyDrafts();
      for (const binding of response.bindings) {
        next[binding.agent_role] = {
          managed_model_api_config_id: binding.managed_model_api_config_id,
          prompt_version: binding.prompt_version,
          strength_rank: binding.strength_rank,
          status: binding.status
        };
      }
      setBindings(response.bindings);
      setDrafts(next);
    } catch (error) {
      if (requestID === requestRef.current) message.error(getUserErrorMessage(error, "三智能体配置加载失败"));
    } finally {
      if (requestID === requestRef.current) setLoading(false);
    }
  }, [archetype, message, stage, subject, tenantID]);

  useEffect(() => {
    void load();
    return () => { requestRef.current += 1; };
  }, [load]);

  const setRoleDraft = (role: PanelAgentRole, patch: Partial<RoleDraft>) => {
    setDrafts((current) => ({ ...current, [role]: { ...current[role], ...patch } }));
  };

  const save = async (role: PanelAgentRole) => {
    const requestID = requestRef.current;
    const draft = drafts[role];
    const config = configs.find((item) => item.id === draft.managed_model_api_config_id);
    const existing = bindings.find((item) => item.agent_role === role);
    const canDisableMissing = draft.status === "disabled" && existing?.managed_model_api_config_id === draft.managed_model_api_config_id;
    if (!tenantID || loadingConfigs || (!config && !canDisableMissing) || !draft.prompt_version.trim() || !draft.strength_rank ||
      (draft.status === "active" && (!config || !capabilityVerified(config)))) {
      message.error("请选择本校已通过完整能力检测的模型，并填写实际部署的 Prompt 版本");
      return;
    }
    setSavingRole(role);
    try {
      const response = await savePanelModelRoleBinding({
        tenant_id: tenantID, education_stage: stage, subject_code: subject,
        archetype_code: archetype, agent_role: role,
        managed_model_api_config_id: draft.managed_model_api_config_id,
        prompt_version: draft.prompt_version.trim(), strength_rank: draft.strength_rank,
        status: draft.status
      });
      if (requestID === requestRef.current) {
        setBindings((current) => [...current.filter((item) => item.agent_role !== role), response.binding]);
        message.success(`${roles.find((item) => item.key === role)?.title} 已保存；不会改变日常对话模型`);
      }
    } catch (error) {
      if (requestID === requestRef.current) message.error(getUserErrorMessage(error, "角色模型保存失败"));
    } finally {
      setSavingRole(null);
    }
  };

  const modelOptions = useMemo(() => [...configs.map((config) => ({
    value: config.id,
    label: `${config.display_name} · ${config.model_name}${config.is_default ? " · 日常对话当前使用" : ""}${capabilityVerified(config) ? "" : " · 不可启用"}`,
    disabled: !capabilityVerified(config) && !bindings.some((binding) => binding.managed_model_api_config_id === config.id)
  })), ...bindings.filter((binding) => !configs.some((config) => config.id === binding.managed_model_api_config_id))
    .map((binding) => ({ value: binding.managed_model_api_config_id, label: `已移除模型 · ${binding.managed_model_api_config_id}`, disabled: true }))], [bindings, configs]);
  const active = roles.every(({ key }) => bindings.some((item) => item.agent_role === key && item.status === "active"));
  const arbiter = bindings.find((item) => item.agent_role === "arbiter");
  const primaries = bindings.filter((item) => item.agent_role !== "arbiter");
  const arbiterModel = configs.find((item) => item.id === arbiter?.managed_model_api_config_id)?.model_version;
  const roleModelsVerified = bindings.every((item) => {
    const config = configs.find((candidate) => candidate.id === item.managed_model_api_config_id);
    return config != null && capabilityVerified(config);
  });
  const rankValid = active && arbiter != null && primaries.every((item) => arbiter.strength_rank > item.strength_rank &&
    arbiter.managed_model_api_config_id !== item.managed_model_api_config_id) && roleModelsVerified && Boolean(arbiterModel) &&
    primaries.every((item) => configs.find((candidate) => candidate.id === item.managed_model_api_config_id)?.model_version !== arbiterModel);

  return (
    <section className="platform-panel-bindings" aria-label="三智能体评分模型配置">
      <div className="platform-panel-bindings-heading">
        <div>
          <h2>三智能体评分模型</h2>
          <p>按学校、学段、学科和题型分别绑定 A/B/C。这里的角色选择与日常对话“当前使用”互不影响。</p>
        </div>
        <Button icon={<RefreshCw size={15} />} loading={loading} disabled={!tenantID} onClick={() => void load()}>刷新绑定</Button>
      </div>
      <div className="platform-panel-bindings-scope">
        <label>学段<Select value={stage} options={[{ value: "senior", label: "高中" }, { value: "junior", label: "初中" }]} onChange={setStage} disabled={savingRole !== null} /></label>
        <label>学科<Select value={subject} options={subjects} onChange={setSubject} disabled={savingRole !== null} showSearch optionFilterProp="label" /></label>
        <label>题型编码<Input value={archetypeInput} maxLength={128} onChange={(event) => setArchetypeInput(event.target.value)} onPressEnter={() => setArchetype(archetypeInput.trim() || "*")} disabled={savingRole !== null} /></label>
        <Button onClick={() => setArchetype(archetypeInput.trim() || "*")} disabled={savingRole !== null}>载入题型</Button>
      </div>
      <p className="platform-panel-bindings-hint">当前范围：{stage === "senior" ? "高中" : "初中"} / {subjects.find((item) => item.value === subject)?.label} / {archetype === "*" ? "全部题型（默认）" : archetype}。特定题型优先于默认绑定。</p>
      <Alert type={rankValid ? "success" : "warning"} showIcon message={rankValid
        ? "A/B/C 角色绑定完整；仍须通过评分准入与真实评测，才可用于正式评分。"
        : "角色绑定尚不完整或 C 未高于 A/B；不会自动启用三智能体评分。"} />
      <div className="platform-panel-bindings-grid">
        {roles.map((role) => {
          const draft = drafts[role.key];
          const existing = bindings.find((item) => item.agent_role === role.key);
          return <div className="platform-panel-role" key={role.key}>
            <div><h3>{role.title}</h3><small>{role.description}</small></div>
            <label>评分模型<Select aria-label={`${role.title}评分模型`} value={draft.managed_model_api_config_id || undefined} options={modelOptions} placeholder="选择已验证的学校模型" showSearch optionFilterProp="label" loading={loadingConfigs} disabled={!tenantID || loading || loadingConfigs || savingRole !== null} onChange={(value) => setRoleDraft(role.key, { managed_model_api_config_id: value })} /></label>
            <label>Prompt 版本<Input aria-label={`${role.title} Prompt 版本`} value={draft.prompt_version} maxLength={128} placeholder="与评分服务实际部署版本一致" disabled={!tenantID || loading || savingRole !== null} onChange={(event) => setRoleDraft(role.key, { prompt_version: event.target.value })} /></label>
            <div className="platform-panel-role-footer">
              <label>强度等级<InputNumber aria-label={`${role.title}强度等级`} min={1} max={100} value={draft.strength_rank} disabled={!tenantID || loading || savingRole !== null} onChange={(value) => setRoleDraft(role.key, { strength_rank: value ?? role.rank })} /></label>
              <label>启用<Switch aria-label={`${role.title}启用`} checked={draft.status === "active"} disabled={!existing || loading || savingRole !== null} onChange={(checked) => setRoleDraft(role.key, { status: checked ? "active" : "disabled" })} /></label>
            </div>
            <Button type="primary" loading={savingRole === role.key} disabled={!tenantID || loading || loadingConfigs || savingRole !== null} onClick={() => void save(role.key)}>保存{role.title}</Button>
          </div>;
        })}
      </div>
      <p className="platform-panel-bindings-hint">模型需先在上方模型库完成结构化能力检测。A/B 独立盲评；C 必须是不同模型且强度等级高于 A/B。强度等级只是配置门槛，不代替真实评测。</p>
    </section>
  );
}
