import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App as AntApp, Button, Input, InputNumber, Select, Switch } from "antd";
import { RefreshCw } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import {
  listPanelModelRoleBindings,
  savePanelModelRoleBinding,
  type ManagedModelAPIConfig,
  type PanelAgentRole,
  type PanelEducationStage,
  type PanelModelRoleBinding,
  type GradingModelUse
} from "../api/modelApiConfig";
import { capabilityVerified as structuredVerified, modelConfigOptionLabel } from "../features/platform-model-config/lib/modelConfig";
import { getCurrentRuntimePrompt, type RuntimePrompt } from "../api/modelGovernance";
import { ScoringSetupChecklist } from "../components/model-governance/ScoringSetupChecklist";

const roles: { key: PanelAgentRole; title: string; description: string; rank: number }[] = [
  { key: "primary_a", title: "主评 A", description: "第一位独立评分；独立盲评", rank: 1 },
  { key: "primary_b", title: "主评 B", description: "第二位独立评分；独立盲评，不读取 A 的结果", rank: 1 },
  { key: "arbiter", title: "仲裁 C", description: "分歧复核；更强的独立盲评，不读取 A/B 分数", rank: 2 }
];

const subjects = [
  ["chinese", "语文"], ["mathematics", "数学"], ["english", "英语"],
  ["physics", "物理"], ["chemistry", "化学"], ["biology", "生物"],
  ["history", "历史"], ["geography", "地理"], ["ethics_politics", "思想政治"]
].map(([value, label]) => ({ value, label }));

const questionTypeLabels: Record<string, string> = { short_answer: "简答题", calculation: "计算题", essay: "作文", discussion: "论述题" };

export function deployedQuestionTypes(prompt: RuntimePrompt | null, subject: string) {
  return [...new Set((prompt?.components ?? []).map((item) => item.key.split(".")).filter((parts) => parts.length === 3 && parts[0] === "subject" && parts[1] === subject).map((parts) => parts[2]))]
    .map((value) => ({ value, label: questionTypeLabels[value] ?? value }));
}

function questionTypeOptions(prompt: RuntimePrompt | null, subject: string, archetype: string) {
  const options = [{ value: "*", label: "全部题型（默认）" }, ...deployedQuestionTypes(prompt, subject)];
  // 自定义范围可能不在当前部署清单中，仍保留选项以便管理员核对已有绑定。
  if (!options.some((item) => item.value === archetype)) {
    options.push({ value: archetype, label: `自定义题型 · ${archetype}` });
  }
  return options;
}

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
  return config.status === "active" && structuredVerified(config);
}

export function PanelModelBindingsSection({ tenantID, configs, loadingConfigs = false, suggestedModelID, savedUses = [], onSaved, onDirtyChange, onSavingChange, onOpenGovernance }: Readonly<{ tenantID: string; configs: ManagedModelAPIConfig[]; loadingConfigs?: boolean; suggestedModelID?: string; savedUses?: GradingModelUse[]; onSaved?: () => void; onDirtyChange?: (dirty: boolean) => void; onSavingChange?: (saving: boolean) => void; onOpenGovernance?: (view: string, scope?: { stage: string; subject: string; archetype: string }) => void }>) {
  const { message, modal } = AntApp.useApp();
  const [stage, setStage] = useState<PanelEducationStage>("senior");
  const [subject, setSubject] = useState("mathematics");
  const [archetypeInput, setArchetypeInput] = useState("*");
  const [archetype, setArchetype] = useState("*");
  const [bindings, setBindings] = useState<PanelModelRoleBinding[]>([]);
  const [drafts, setDrafts] = useState<RoleDrafts>(emptyDrafts);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [bindingConflict, setBindingConflict] = useState("");
  const conflictRef = useRef(false);
  const [loadedScope, setLoadedScope] = useState("");
  const [savingRole, setSavingRole] = useState<PanelAgentRole | null>(null);
  const [runtimePrompt, setRuntimePrompt] = useState<RuntimePrompt | null>(null);
  const [promptLoading, setPromptLoading] = useState(true);
  const [promptError, setPromptError] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const requestRef = useRef(0);
  const savingRef = useRef(false);
  const loadingRef = useRef(false);
  const loadedScopeRef = useRef("");
  const scopeKey = JSON.stringify([tenantID, stage, subject, archetype]);
  // 成功读取空绑定与读取失败是不同事实；只有当前范围完整读取成功才允许修改。
  const bindingReady = loadedScope === scopeKey && !loading && !loadError && !bindingConflict;

  useEffect(() => {
    let active = true;
    void getCurrentRuntimePrompt().then((response) => { if (active) setRuntimePrompt(response.prompt); })
      .catch(() => { if (active) setPromptError(true); }).finally(() => { if (active) setPromptLoading(false); });
    return () => { active = false; };
  }, []);

  const load = useCallback(async () => {
    // 保存的结果尚未确认时不得刷新；ref 同步拦截重绘前已排队的点击。
    if (savingRef.current) return;
    const requestID = ++requestRef.current;
    setLoadError("");
    if (loadedScopeRef.current !== scopeKey) {
      conflictRef.current = false;
      setBindingConflict("");
      setBindings([]);
      setDrafts(emptyDrafts());
      setLoadedScope("");
    }
    if (!tenantID) { loadingRef.current = false; setLoading(false); return; }
    loadingRef.current = true;
    setLoading(true);
    try {
      const response = await listPanelModelRoleBindings(tenantID, stage, subject, archetype);
      if (requestID !== requestRef.current) return;
      if (response.bindings.some((binding) => !binding.revision)) {
        throw new Error("当前 API 未提供角色配置修订，请更新 API 服务后刷新");
      }
      const next = emptyDrafts();
      for (const binding of response.bindings) {
        next[binding.agent_role] = {
          managed_model_api_config_id: binding.managed_model_api_config_id,
          prompt_version: binding.prompt_version,
          strength_rank: binding.strength_rank,
          status: binding.status
        };
      }
      // 只有显式重新读取成功才解除冲突；保留草稿期间不能借旧修订再次提交。
      conflictRef.current = false;
      setBindingConflict("");
      setBindings(response.bindings);
      setDrafts(next);
      loadedScopeRef.current = scopeKey;
      setLoadedScope(scopeKey);
    } catch (error) {
      if (requestID === requestRef.current) {
        // 同范围刷新失败保留上次输入，但它不再代表当前服务端事实，必须重试成功才能保存。
        const notice = getUserErrorMessage(error, "三智能体配置加载失败");
        setLoadError(notice);
        message.error(notice);
      }
    } finally {
      if (requestID === requestRef.current) { loadingRef.current = false; setLoading(false); }
    }
  }, [archetype, message, scopeKey, stage, subject, tenantID]);

  useEffect(() => {
    void load();
    return () => { requestRef.current += 1; };
  }, [load]);

  const setRoleDraft = (role: PanelAgentRole, patch: Partial<RoleDraft>) => {
    setDrafts((current) => ({ ...current, [role]: { ...current[role], ...patch } }));
  };

  const save = async (role: PanelAgentRole) => {
    // 刷新刚发起时 state 尚未重绘，同步标记也要拒绝排队的保存动作。
    if (savingRef.current || loadingRef.current || conflictRef.current || !bindingReady || loadingConfigs || !tenantID) return;
    const requestID = requestRef.current;
    const draft = drafts[role];
    const config = configs.find((item) => item.id === draft.managed_model_api_config_id);
    const existing = bindings.find((item) => item.agent_role === role);
    // 已移除的模型仍允许停用原绑定，不能因为模型库缺项而让失效绑定无法关闭。
    const canDisableMissing = draft.status === "disabled" && existing?.managed_model_api_config_id === draft.managed_model_api_config_id;
    if ((!config && !canDisableMissing) || !draft.prompt_version.trim() || !draft.strength_rank ||
      (draft.status === "active" && (!config || !capabilityVerified(config)))) {
      message.error("请选择本校已通过结构化输出检测的模型，并填写实际部署的 Prompt 版本");
      return;
    }
    savingRef.current = true;
    setSavingRole(role);
    onSavingChange?.(true);
    try {
      const response = await savePanelModelRoleBinding({
        tenant_id: tenantID, education_stage: stage, subject_code: subject,
        archetype_code: archetype, agent_role: role,
        managed_model_api_config_id: draft.managed_model_api_config_id,
        prompt_version: draft.prompt_version.trim(), strength_rank: draft.strength_rank,
        status: draft.status, expected_revision: existing?.revision ?? "absent"
      });
      if (requestID === requestRef.current) {
        setBindings((current) => [...current.filter((item) => item.agent_role !== role), response.binding]);
        // 服务端会规范化版本和角色字段；仅同步刚保存的角色，不覆盖另外两项未保存输入。
        setDrafts((current) => ({ ...current, [role]: {
          managed_model_api_config_id: response.binding.managed_model_api_config_id,
          prompt_version: response.binding.prompt_version,
          strength_rank: response.binding.strength_rank,
          status: response.binding.status
        } }));
        message.success(`${roles.find((item) => item.key === role)?.title} 已保存；不会改变日常对话模型`);
        onSaved?.();
      }
    } catch (error) {
      if (requestID === requestRef.current) {
        if (error instanceof ApiClientError && error.code === "panel_model_binding_changed") {
          // 服务端拒绝旧修订时保留三个角色的输入，等待管理员刷新核对，不自动覆盖或合并。
          conflictRef.current = true;
          setBindingConflict("该角色配置已被其他管理员更新。三个角色的本页输入均已保留，请刷新绑定、核对新配置后重新操作。");
          message.error("角色配置已变化，请刷新核对后重新操作");
        } else message.error(getUserErrorMessage(error, "角色模型保存失败"));
      }
    } finally {
      savingRef.current = false;
      setSavingRole(null);
      onSavingChange?.(false);
    }
  };

  const modelOptions = useMemo(() => [...configs.map((config) => ({
    value: config.id,
    label: `${modelConfigOptionLabel(config)}${config.is_default ? " · 日常对话当前使用" : ""}${capabilityVerified(config) ? "" : " · 不可启用"}`,
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
  const dirtyRoles = roles.filter(({ key }) => {
    const saved = bindings.find((item) => item.agent_role === key) ?? emptyDrafts()[key];
    const draft = drafts[key];
    return draft.managed_model_api_config_id !== saved.managed_model_api_config_id ||
      draft.prompt_version !== saved.prompt_version || draft.strength_rank !== saved.strength_rank || draft.status !== saved.status;
  }).map(({ key }) => key);
  const hasDraftChanges = dirtyRoles.length > 0;
  useEffect(() => { onDirtyChange?.(hasDraftChanges); }, [hasDraftChanges, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
  useEffect(() => { onSavingChange?.(savingRole !== null); }, [onSavingChange, savingRole]);
  useEffect(() => () => onSavingChange?.(false), [onSavingChange]);

  // 切换范围会重新读取服务端绑定；未保存的本地输入只能在用户明确放弃后丢弃。
  const changeScope = (action: () => void) => {
    if (savingRef.current || loadingRef.current) return;
    const proceed = () => { if (!savingRef.current && !loadingRef.current) action(); };
    if (!hasDraftChanges) { proceed(); return; }
    modal.confirm({
      title: "放弃未保存的角色配置？",
      content: "切换范围或刷新后，当前输入将被服务端配置替换。",
      okText: "放弃并继续", cancelText: "继续编辑", okButtonProps: { danger: true }, onOk: proceed
    });
  };

  const typeOptions = questionTypeOptions(runtimePrompt, subject, archetype);
  const suggestedModel = configs.find((config) => config.id === suggestedModelID);
  const promptOptions = [...new Set([runtimePrompt?.prompt_version, ...bindings.map((item) => item.prompt_version)].filter((value): value is string => Boolean(value)))].map((value) => ({ value, label: `${value}${value === runtimePrompt?.prompt_version ? " · 当前部署" : " · 已保存"}` }));

  return (
    <section className="platform-panel-bindings" aria-label="三智能体评分模型配置">
      <div className="platform-panel-bindings-heading">
        <div>
          <h2>三智能体评分模型</h2>
          <p>按学校、学段、学科和题型分别绑定 A/B/C。这里的角色选择与日常对话“当前使用”互不影响。</p>
        </div>
        <Button icon={<RefreshCw size={15} />} loading={loading} disabled={!tenantID || savingRole !== null} onClick={() => changeScope(() => void load())}>刷新绑定</Button>
      </div>
      {bindingConflict ? <Alert type="warning" showIcon message="角色配置已变化，暂时不能保存" description={bindingConflict} /> : null}
      {loadError ? <Alert type="error" showIcon message="角色配置读取失败，暂时不能保存" description={`${loadError}。已保留本页输入，请刷新绑定后核对。`} /> : null}
      {suggestedModel ? <div className="platform-panel-suggested-model"><strong>当前模型：{suggestedModel.model_name}</strong>
        <p className="platform-panel-bindings-hint">选择下方范围，再将当前模型用于指定角色并保存。已保存的角色：</p>
        <div className="platform-model-saved-uses">{savedUses.length ? savedUses.map(use => <Button key={`${use.education_stage}:${use.subject_code}:${use.archetype_code}:${use.agent_role}`} size="small" disabled={savingRole !== null || loading}
          onClick={() => changeScope(() => { setStage(use.education_stage); setSubject(use.subject_code); setArchetype(use.archetype_code); setArchetypeInput(use.archetype_code); })}>
          {use.education_stage === "senior" ? "高中" : "初中"} / {subjects.find(subject => subject.value === use.subject_code)?.label ?? use.subject_code} / {use.archetype_code === "*" ? "全部题型" : questionTypeLabels[use.archetype_code] ?? use.archetype_code} / {roles.find(role => role.key === use.agent_role)?.title ?? use.agent_role}
        </Button>) : <small>暂无已启用的角色绑定</small>}</div>
      </div> : null}
      <div className="platform-panel-bindings-scope">
        <label>学段<Select value={stage} options={[{ value: "senior", label: "高中" }, { value: "junior", label: "初中" }]} onChange={(value) => changeScope(() => setStage(value))} disabled={savingRole !== null} /></label>
        <label>学科<Select value={subject} options={subjects} onChange={(value) => changeScope(() => setSubject(value))} disabled={savingRole !== null} showSearch optionFilterProp="label" /></label>
        <label>题型<Select value={archetype} options={typeOptions} loading={promptLoading} disabled={savingRole !== null} onChange={(value) => changeScope(() => { setArchetype(value); setArchetypeInput(value); })} /></label>
        <Button onClick={() => setAdvanced((value) => !value)} disabled={savingRole !== null}>{advanced ? "收起高级设置" : "高级设置"}</Button>
      </div>
      {advanced ? <div className="platform-panel-bindings-scope"><label>自定义题型编码<Input value={archetypeInput} maxLength={128} onChange={(event) => setArchetypeInput(event.target.value)} onPressEnter={() => changeScope(() => setArchetype(archetypeInput.trim() || "*"))} disabled={savingRole !== null} /></label><Button onClick={() => changeScope(() => setArchetype(archetypeInput.trim() || "*"))} disabled={savingRole !== null}>载入自定义题型</Button></div> : null}
      {promptError ? <Alert type="warning" showIcon message="当前部署版本暂时无法读取，可在高级设置填写已核实的版本；保存仍由服务器校验。" /> : <p className="platform-panel-bindings-hint">{promptLoading ? "正在读取评分服务部署版本…" : `评分服务当前部署：${runtimePrompt?.prompt_version ?? "暂未配置"}`}</p>}
      <Button disabled={!runtimePrompt || !tenantID || !bindingReady || savingRole !== null} onClick={() => {
        if (!runtimePrompt) return;
        // 套用只更新未保存草稿；三个角色仍逐项保存，不能将部分成功显示成全部已部署。
        setDrafts((current) => Object.fromEntries(roles.map(({ key }) => [key, { ...current[key], prompt_version: runtimePrompt.prompt_version }])) as RoleDrafts);
      }}>为 A/B/C 套用当前部署版本</Button>
      <p className="platform-panel-bindings-hint">当前范围：{stage === "senior" ? "高中" : "初中"} / {subjects.find((item) => item.value === subject)?.label} / {archetype === "*" ? "全部题型（默认）" : archetype}。特定题型优先于默认绑定。</p>
      <Alert type={rankValid ? "success" : "warning"} showIcon message={rankValid
        ? "A/B/C 角色绑定完整；仍须通过评分准入与真实评测，才可用于正式评分。"
        : "角色绑定尚不完整或 C 未高于 A/B；不会自动启用三智能体评分。"} />
      <div className="platform-panel-bindings-grid">
        {roles.map((role) => {
          const draft = drafts[role.key];
          const hasExisting = bindings.some((item) => item.agent_role === role.key);
          return <div className="platform-panel-role" key={role.key}>
            <div><h3>{role.title}</h3><small>{role.description}</small>{dirtyRoles.includes(role.key) ? <small className="platform-panel-role-dirty">未保存更改</small> : null}</div>
            <label>评分模型<Select aria-label={`${role.title}评分模型`} value={draft.managed_model_api_config_id || undefined} options={modelOptions} placeholder="选择已验证的学校模型" showSearch optionFilterProp="label" loading={loadingConfigs} disabled={!tenantID || !bindingReady || loadingConfigs || savingRole !== null} onChange={(value) => setRoleDraft(role.key, { managed_model_api_config_id: value })} /></label>
            {suggestedModel ? <Button size="small" disabled={!bindingReady || loadingConfigs || savingRole !== null || !capabilityVerified(suggestedModel)}
              onClick={() => setRoleDraft(role.key, { managed_model_api_config_id: suggestedModel.id })}>将 {suggestedModel.model_name} 用于{role.title}</Button> : null}
            <label>评分规则版本<Select aria-label={`${role.title} Prompt 版本`} value={draft.prompt_version || undefined} options={[...promptOptions, ...(!promptOptions.some((item) => item.value === draft.prompt_version) && draft.prompt_version ? [{ value: draft.prompt_version, label: `${draft.prompt_version} · 自定义` }] : [])]} placeholder="选择评分服务部署版本" disabled={!tenantID || !bindingReady || savingRole !== null} onChange={(value) => setRoleDraft(role.key, { prompt_version: value })} /></label>
            {advanced ? <label>自定义 Prompt 版本<Input aria-label={`${role.title}自定义 Prompt 版本`} value={draft.prompt_version} maxLength={128} placeholder="填写已核实的部署版本" disabled={!tenantID || !bindingReady || savingRole !== null} onChange={(event) => setRoleDraft(role.key, { prompt_version: event.target.value })} /></label> : null}
            <div className="platform-panel-role-footer">
              {advanced ? <label>路由强度等级<InputNumber aria-label={`${role.title}强度等级`} min={1} max={100} value={draft.strength_rank} disabled={!tenantID || !bindingReady || savingRole !== null} onChange={(value) => setRoleDraft(role.key, { strength_rank: value ?? role.rank })} /></label> : <span>路由强度等级：{draft.strength_rank}（默认 {role.rank}）</span>}
              <label>启用<Switch aria-label={`${role.title}启用`} checked={draft.status === "active"} disabled={!hasExisting || !bindingReady || savingRole !== null} onChange={(checked) => setRoleDraft(role.key, { status: checked ? "active" : "disabled" })} /></label>
            </div>
            <Button type="primary" loading={savingRole === role.key} disabled={!tenantID || !bindingReady || loadingConfigs || savingRole !== null} onClick={() => void save(role.key)}>保存{role.title}</Button>
          </div>;
        })}
      </div>
      <p className="platform-panel-bindings-hint">模型需先在上方模型库完成结构化能力检测。A/B 独立盲评；C 必须是不同模型且强度等级高于 A/B。强度等级只是配置门槛，不代替真实评测。</p>
      {tenantID && onOpenGovernance ? <ScoringSetupChecklist tenantID={tenantID} modelIDs={bindings.map((binding) => binding.managed_model_api_config_id)} capabilityReady={roleModelsVerified && active} bindingReady={rankValid} subject={subjects.find((item) => item.value === subject)?.label ?? subject} onOpen={(view) => onOpenGovernance(view, { stage, subject, archetype })} /> : null}
    </section>
  );
}
