import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, App, Button, InputNumber, Select, Space, Spin } from "antd";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { getGradingAIPlan, probeGradingAIModel, saveGradingAIPlan, type GradingAIModel, type GradingAIPlan, type GradingAIPlanResponse } from "../../../../api/gradingAIPlan";
import { formatError } from "../gradingWorkbench.model";
import "./gradingAI.css";

export const gradingAIModeLabels: Record<string, string> = {
  single: "单模型评分",
  dual: "双模型独立评分",
  dual_conditional_arbiter: "双模型评分 · 超差启用第三模型",
  unconfigured: "尚无可用评分模型"
};

export function GradingAIPlanDescription({ plan, showScope = true, showConfirmation = true }: Readonly<{ plan: GradingAIPlan; showScope?: boolean; showConfirmation?: boolean }>) {
  const names = Object.entries(plan.resolved_roles ?? {}).map(([role, model]) => `${({ primary_a: "评分 A", primary_b: "评分 B", arbiter: "第三评分" } as Record<string, string>)[role]}：${model?.display_name || model?.model_name}`).join("；");
  const primary = plan.decision_config.primary_allowed_diff_points;
  const teacher = plan.decision_config.teacher_allowed_diff_points;
  const multiModel = plan.effective_mode === "dual" || plan.effective_mode === "dual_conditional_arbiter";
  return <div className="grading-ai-plan-summary">
    <strong>{gradingAIModeLabels[plan.effective_mode] ?? "AI 评分"}</strong>
    {showScope ? <p>仅用于解答、计算、证明等大题；选择题和填空题不使用本方案。</p> : null}
    <p>{plan.effective_mode === "single" ? `使用 ${plan.single_model?.display_name || plan.single_model?.model_name || "当前可用模型"}。确认后按单模型生成建议分。` : names}</p>
    {multiModel ? validAllowedDifference(primary) && (plan.effective_mode === "dual" || validAllowedDifference(teacher)) ? <>
      <p>A、B 分差不超过 {primary} 分时取均分；超过后{plan.effective_mode === "dual" ? "转教师处理（未配置第三模型）" : "调用第三模型，按 A 25%、B 25%、第三模型 50% 汇总"}。最终向上取整，不超过满分。</p>
      {plan.effective_mode === "dual_conditional_arbiter" && teacher !== null ? <p>三评后，第三模型与 A、B 均分的差值超过 {teacher} 分，或三者极差超过 {teacher * 2} 分时，转教师处理。</p> : null}
    </> : <p>本场大题允许分差尚未填写，需要学校管理员先确认。</p> : null}
    {showConfirmation ? <small>{plan.confirmed_at ? `本场大题方案已确认 · ${new Date(plan.confirmed_at).toLocaleString()}` : "需要学校管理员先确认本场大题方案；只有一个模型也可确认单模型。"}</small> : null}
  </div>;
}

function modelIdentity(model: GradingAIModel): string {
  return JSON.stringify([model.model_name.trim().toLowerCase(), model.model_version.trim().toLowerCase()]);
}

function validAllowedDifference(value: number | null): value is number {
  return value !== null && Number.isFinite(value) && value >= 0 && value <= 1000;
}

// 每次挂载读取本场方案；方案调整后重开启动窗口，避免把旧摘要当成本次配置。
export function GradingAIPlanSummary({ examId, onConfigure }: Readonly<{ examId: string; onConfigure?: () => void }>) {
  const [data, setData] = useState<GradingAIPlanResponse | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let disposed = false;
    const session = getBusinessCommandSession();
    setData(null); setError("");
    void getGradingAIPlan(examId).then(result => { if (!disposed && session === getBusinessCommandSession()) setData(result); }).catch(reason => { if (!disposed && session === getBusinessCommandSession()) setError(formatError(reason)); });
    return () => { disposed = true; };
  }, [examId]);
  return <section aria-label="本次大题 AI 评分方式" className="grading-ai-plan-before-start">
    {data ? data.plan.confirmed_at ? <details><summary>已确认 · {gradingAIModeLabels[data.plan.effective_mode] ?? "AI 评分"} · 查看本次方案</summary><GradingAIPlanDescription plan={data.plan} /></details> : <GradingAIPlanDescription plan={data.plan} /> : error ? <Alert type="warning" message="AI 方案暂未读到" description={error} /> : <Spin size="small" />}
    {onConfigure ? <Button type="link" onClick={onConfigure}>{data?.can_configure === false ? "查看本场 AI 评分方案" : "配置本场 AI 评分"}</Button> : null}
  </section>;
}

export function GradingAIPlanSettings(props: Readonly<{ examId: string; onSaved?: () => void }>) {
  const session = getBusinessCommandSession();
  return <ScopedGradingAIPlanSettings key={`${session.scope}:${session.generation}:${props.examId}`} {...props} />;
}

function ScopedGradingAIPlanSettings({ examId, onSaved }: Readonly<{ examId: string; onSaved?: () => void }>) {
  const { message } = App.useApp();
  const [data, setData] = useState<GradingAIPlanResponse | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [probing, setProbing] = useState("");
  const [probeNotice, setProbeNotice] = useState("");
  const [probeRows, setProbeRows] = useState<Array<{ key: string; name: string; status: "running" | "passed" | "failed"; message: string }>>([]);
  const probeBusy = useRef(false); const mounted = useRef(true);
  const probeRequests = useRef(new Map<string, string>());
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const [a, setA] = useState(""); const [b, setB] = useState(""); const [c, setC] = useState("");
  const [primary, setPrimary] = useState<number | null>(null); const [teacher, setTeacher] = useState<number | null>(null);
  const owner = `${getBusinessCommandSession().scope}:${getBusinessCommandSession().generation}:${examId}`;
  const ownerRef = useRef(owner); ownerRef.current = owner;
  const apply = useCallback((result: GradingAIPlanResponse) => {
    setData(result); setA(result.plan.roles.primary_a?.model_config_id || result.plan.fallback_model_config_id || result.plan.single_model?.model_config_id || "");
    setB(result.plan.roles.primary_b?.model_config_id || ""); setC(result.plan.roles.arbiter?.model_config_id || "");
    setPrimary(result.plan.decision_config.primary_allowed_diff_points ?? null); setTeacher(result.plan.decision_config.teacher_allowed_diff_points ?? null);
  }, []);
  useEffect(() => {
    let disposed = false;
    const session = getBusinessCommandSession();
    setData(null); setError("");
    void getGradingAIPlan(examId).then(result => { if (!disposed && session === getBusinessCommandSession()) apply(result); }).catch(reason => { if (!disposed && session === getBusinessCommandSession()) setError(formatError(reason)); });
    return () => { disposed = true; };
  }, [examId, apply]);
  const modelA = data?.available_models.find(model => model.model_config_id === a);
  const modelB = data?.available_models.find(model => model.model_config_id === b);
  // 名称和版本相同的模型跨供应商仍不独立；这里只要求填写必要分差，实际模式由服务端确认。
  const requiresThresholds = Boolean(modelA && modelB && modelIdentity(modelA) !== modelIdentity(modelB));
  const modelC = data?.available_models.find(model => model.model_config_id === c);
  // 这里只切换编辑中规则的说明；已保存摘要和实际生效模式仍以服务端回包为准。
  const hasIndependentThirdModel = Boolean(requiresThresholds && modelA && modelB && modelC && modelIdentity(modelC) !== modelIdentity(modelA) && modelIdentity(modelC) !== modelIdentity(modelB));
  const thresholdsValid = validAllowedDifference(primary) && (!hasIndependentThirdModel || (validAllowedDifference(teacher) && teacher >= primary));
  const canSave = Boolean(modelA) && data?.can_configure !== false && (!requiresThresholds || thresholdsValid);
  const save = async () => {
    if (!data || saving || probeBusy.current || !canSave) return;
    const requestOwner = owner; const session = getBusinessCommandSession();
    setSaving(true); setError("");
    try {
      const result = await saveGradingAIPlan(examId, {
        expected_revision: data.plan.revision, fallback_model_config_id: a,
        roles: { primary_a: { model_config_id: a, strength_rank: 1 }, primary_b: { model_config_id: b, strength_rank: 1 }, arbiter: { model_config_id: c, strength_rank: 2 } },
        // 单模型不使用分差，不把之前多评表单里的数值当作本次生效配置。
        decision_config: { primary_allowed_diff_points: requiresThresholds ? primary : null, teacher_allowed_diff_points: hasIndependentThirdModel ? teacher : null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" }
      });
      // 退出后重登或切换考试，旧保存回包不能覆盖当前方案或关闭新窗口。
      if (ownerRef.current !== requestOwner || session !== getBusinessCommandSession()) return;
      apply(result); message.success("本场大题 AI 评分方案已确认"); onSaved?.();
    } catch (reason) { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setError(formatError(reason)); }
    finally { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setSaving(false); }
  };
  const options = data?.available_models.map(model => ({ value: model.model_config_id, label: `${model.display_name || model.model_name} · ${model.provider_key}` })) ?? [];
  const probeKey = (model: GradingAIModel) => JSON.stringify([owner, model.model_config_id, model.config_revision, model.binding_revision]);
  const probe = async (models: GradingAIModel[]) => {
    if (probeBusy.current || saving || !models.length || data?.can_configure === false) return;
    const requestOwner = owner; const session = getBusinessCommandSession();
    const current = () => mounted.current && ownerRef.current === requestOwner && session === getBusinessCommandSession();
    // 串行处理同一次点击时选定的配置；失败项保留同一请求ID，重试不能把未知结果当作新付费调用。
    const frozen = models.map(model => ({ ...model }));
    probeBusy.current = true; setProbing("batch"); setProbeNotice("");
    try {
      for (const model of frozen) {
        if (!current()) return;
        const key = probeKey(model); const name = model.display_name || model.model_name;
        let requestId = probeRequests.current.get(key);
        if (!requestId) { requestId = crypto.randomUUID(); probeRequests.current.set(key, requestId); }
        setProbeRows(rows => [...rows.filter(row => row.key !== key), { key, name, status: "running", message: "正在检测" }]);
        try {
          const result = await probeGradingAIModel(examId, model.model_config_id, { requestId, configRevision: model.config_revision, bindingRevision: model.binding_revision });
          if (!current()) return;
          setProbeRows(rows => rows.map(row => row.key === key ? { ...row, status: result.vision_probe_status === "passed" ? "passed" : "failed", message: result.vision_probe_status === "passed" ? result.cache_hit ? "通过（复用已有检测）" : "通过" : result.vision_probe_reason || "未通过，请核对连接" } : row));
        } catch (reason) {
          if (!current()) return;
          setProbeRows(rows => rows.map(row => row.key === key ? { ...row, status: "failed", message: formatError(reason) } : row));
        }
      }
      if (!current()) return;
      const fresh = await getGradingAIPlan(examId);
      if (!current()) return;
      // 探测只更新能力状态，不能重置管理员尚未保存的模型选择和分差设置。
      setData(previous => previous ? { ...previous, available_models: fresh.available_models } : previous);
      setProbeNotice("本次检测已结束；未通过的模型可单独重试。");
    } catch (reason) { if (current()) setProbeNotice(`检测回执已保留，最新模型状态暂未读到：${formatError(reason)}`); }
    finally { probeBusy.current = false; if (current()) setProbing(""); }
  };
  const selectedModels = data?.available_models.filter(model => [a, b, c].includes(model.model_config_id)) ?? [];
  const unverifiedModels = selectedModels.filter(model => model.vision_probe_status !== "passed" && !probeRows.some(row => row.key === probeKey(model) && row.status === "passed"));
  const failedModels = selectedModels.filter(model => probeRows.some(row => row.key === probeKey(model) && row.status === "failed") && model.vision_probe_status !== "passed");
  return <section className="grading-ai-plan-settings" aria-label="本场大题 AI 评分方案">
    <h2>本场大题 AI 评分方案</h2>
    <p>仅用于解答、计算、证明题；选择题和填空题不使用。可确认单模型方案。</p>
    {error ? <Alert type="warning" showIcon message={error} /> : null}
    {data ? <>
      <small>当前已保存的方案</small>
      <GradingAIPlanDescription plan={data.plan} showScope={false} showConfirmation={data.can_configure !== false} />
      {/* 服务端明确只读时，不展示保存和主动探测入口；旧响应缺少字段时保留原有交互。 */}
      {data.can_configure === false ? <p>{data.plan.confirmed_at ? "方案已确认。如需修改，请联系学校管理员。" : "方案尚未确认，请联系学校管理员。"}</p> : <>
      <details open={!data.plan.confirmed_at}><summary>{data.plan.confirmed_at ? "修改评分方案" : "设置评分方案"}</summary>
      <div className="grading-ai-plan-fields">
        <label>评分模型 A<Select disabled={Boolean(probing) || saving} aria-label="评分模型 A" value={a || undefined} options={options} onChange={setA} placeholder="选择可用模型" /></label>
        <label>评分模型 B（可选）<Select disabled={Boolean(probing) || saving} aria-label="评分模型 B（可选）" allowClear value={b || undefined} options={options} onChange={value => setB(value || "")} placeholder="未配置时采用单模型" /></label>
        <label>第三评分模型（可选）<Select disabled={Boolean(probing) || saving} aria-label="第三评分模型（可选）" allowClear value={c || undefined} options={options} onChange={value => setC(value || "")} placeholder="选择能力更强的模型" /></label>
      </div>
      <Button loading={Boolean(probing)} disabled={saving || !unverifiedModels.length} onClick={() => void probe(unverifiedModels)}>{unverifiedModels.length ? `检测所选模型图片能力（${unverifiedModels.length} 个）` : "所选模型图片能力均已通过"}</Button>
      {failedModels.length ? <Button disabled={Boolean(probing) || saving} onClick={() => void probe(failedModels)}>只重试未通过模型（{failedModels.length} 个）</Button> : null}
      {probeRows.length ? <ul aria-label="本次图片能力检测结果">{probeRows.filter(row => selectedModels.some(model => row.key === probeKey(model))).map(row => <li key={row.key}>{row.name}：{row.message}</li>)}</ul> : null}
      <details><summary>逐个模型检查</summary><div className="grading-ai-probe-list">{selectedModels.map(model => <div key={model.model_config_id}><span>{model.display_name || model.model_name} · {model.vision_probe_status === "passed" ? "图片能力已检测" : model.vision_probe_status === "failed" ? "图片能力检测未通过" : "图片能力尚未检测"}</span><Button size="small" disabled={Boolean(probing) || saving} onClick={() => void probe([model])}>检测图片能力</Button>{model.vision_probe_reason && model.vision_probe_status !== "passed" ? <small>{model.vision_probe_reason}</small> : null}</div>)}</div></details>
      {selectedModels.length ? <small>检测可能调用模型；同配置的检测结果可复用。保存方案不会调用模型。</small> : null}
      {probeNotice ? <p role="status">{probeNotice}</p> : null}
      <section aria-label="编辑中的分差设置">
        <h3>编辑中的分差设置</h3>
        <div className="grading-ai-plan-fields">
          <label>前两模型允许分差<InputNumber aria-label="前两模型允许分差" min={0} max={1000} value={primary} suffix="分" disabled={!requiresThresholds} placeholder="请手动填写" onChange={value => setPrimary(value)} /></label>
          {hasIndependentThirdModel ? <label>三评后允许分差<InputNumber aria-label="三评后允许分差" min={primary ?? 0} max={1000} value={teacher} suffix="分" placeholder="请手动填写" onChange={value => setTeacher(value)} /></label> : null}
        </div>
        {requiresThresholds ? <p>{hasIndependentThirdModel ? "两个分差均必填，可填 0；第二项不得小于第一项。A、B 分差超过第一项时才调用第三模型。" : "填写 A、B 允许分差，可填 0；分差以内取均分，超过则转教师处理。"}</p> : <p>{modelA ? "当前为单模型，无需填写分差。" : "请先选择评分模型。"}</p>}
        <details className="grading-ai-thresholds"><summary>计算规则</summary>
          {requiresThresholds ? <>
            <p>按未取整分比较；相差恰好等于第一项时，{hasIndependentThirdModel ? "不触发第三评分。" : "取 A、B 均分。"}最终向上取整，不超过满分。</p>
            {hasIndependentThirdModel ? <p>三评按 A 25%、B 25%、C 50% 汇总。C 与 A、B 均分的差值超过第二项，或三者极差超过第二项的两倍时，转教师处理。</p> : null}
          </> : null}
          <p>作答不清或模型调用失败，不直接计零分。实际生效模式以保存后的方案为准。</p>
        </details>
        <small>AI 建议分仍需教师确认。</small>
      </section>
      <Space><Button type="primary" loading={saving} disabled={!canSave || Boolean(probing)} onClick={() => void save()}>确认本场大题 AI 评分方案</Button></Space>
      <small>新方案仅用于之后启动的评分；已启动任务仍用原方案。</small>
      </details>
      </>}
    </> : !error ? <Spin /> : null}
  </section>;
}
