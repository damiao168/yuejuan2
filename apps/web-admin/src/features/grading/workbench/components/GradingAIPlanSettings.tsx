import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, InputNumber, Select, Space, Spin } from "antd";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { getGradingAIPlan, probeGradingAIModel, saveGradingAIPlan, type GradingAIModel, type GradingAIPlan, type GradingAIPlanResponse } from "../../../../api/gradingAIPlan";
import { formatError } from "../gradingWorkbench.model";
import "./gradingAI.css";

export const gradingAIModeLabels: Record<string, string> = {
  single: "单模型评分 · 非三智能体",
  dual: "双模型独立评分",
  dual_conditional_arbiter: "双模型评分 · 超差启用第三模型",
  unconfigured: "尚无可用评分模型"
};

export function GradingAIPlanDescription({ plan }: Readonly<{ plan: GradingAIPlan }>) {
  const names = Object.entries(plan.resolved_roles ?? {}).map(([role, model]) => `${({ primary_a: "评分 A", primary_b: "评分 B", arbiter: "第三评分" } as Record<string, string>)[role]}：${model?.display_name || model?.model_name}`).join("；");
  const primary = plan.decision_config.primary_allowed_diff_points;
  const teacher = plan.decision_config.teacher_allowed_diff_points;
  const multiModel = plan.effective_mode === "dual" || plan.effective_mode === "dual_conditional_arbiter";
  return <div className="grading-ai-plan-summary">
    <strong>{gradingAIModeLabels[plan.effective_mode] ?? "AI 评分"}</strong>
    <p>仅用于解答、计算、证明等大题；选择题和填空题不使用本方案。</p>
    <p>{plan.effective_mode === "single" ? `使用 ${plan.single_model?.display_name || plan.single_model?.model_name || "当前可用模型"}。确认后按单模型生成建议分。` : names}</p>
    {multiModel ? validAllowedDifference(primary) && validAllowedDifference(teacher) ? <>
      <p>A、B 分差不超过 {primary} 分时取均分；超过后{plan.effective_mode === "dual" ? "转教师处理（未配置第三模型）" : "调用第三模型，按 A 25%、B 25%、第三模型 50% 汇总"}。最终向上取整，不超过满分。</p>
      {plan.effective_mode === "dual_conditional_arbiter" ? <p>三评后，第三模型与 A、B 均分的差值超过 {teacher} 分，或三者极差超过 {teacher * 2} 分时，转教师处理。</p> : null}
    </> : <p>本场大题允许分差尚未填写，需要学校管理员先确认。</p> : null}
    <small>{plan.confirmed_at ? `本场大题方案已确认 · ${new Date(plan.confirmed_at).toLocaleString()}` : "需要学校管理员先确认本场大题方案；只有一个模型也可确认单模型。"}</small>
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
    {data ? <GradingAIPlanDescription plan={data.plan} /> : error ? <Alert type="warning" message="AI 方案暂未读到" description={error} /> : <Spin size="small" />}
    {onConfigure ? <Button type="link" onClick={onConfigure}>{data?.can_configure === false ? "查看本场 AI 评分方案" : "配置本场 AI 评分"}</Button> : null}
  </section>;
}

export function GradingAIPlanSettings({ examId, onSaved }: Readonly<{ examId: string; onSaved?: () => void }>) {
  const { message } = App.useApp();
  const [data, setData] = useState<GradingAIPlanResponse | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [probing, setProbing] = useState("");
  const [probeNotice, setProbeNotice] = useState("");
  const [a, setA] = useState(""); const [b, setB] = useState(""); const [c, setC] = useState("");
  const [primary, setPrimary] = useState<number | null>(null); const [teacher, setTeacher] = useState<number | null>(null);
  const owner = `${getBusinessCommandSession().scope}:${getBusinessCommandSession().generation}:${examId}`;
  const ownerRef = useRef(owner); ownerRef.current = owner;
  const apply = (result: GradingAIPlanResponse) => {
    setData(result); setA(result.plan.roles.primary_a?.model_config_id || result.plan.fallback_model_config_id || result.plan.single_model?.model_config_id || "");
    setB(result.plan.roles.primary_b?.model_config_id || ""); setC(result.plan.roles.arbiter?.model_config_id || "");
    setPrimary(result.plan.decision_config.primary_allowed_diff_points ?? null); setTeacher(result.plan.decision_config.teacher_allowed_diff_points ?? null);
  };
  useEffect(() => {
    let disposed = false;
    const session = getBusinessCommandSession();
    setData(null); setError("");
    void getGradingAIPlan(examId).then(result => { if (!disposed && session === getBusinessCommandSession()) apply(result); }).catch(reason => { if (!disposed && session === getBusinessCommandSession()) setError(formatError(reason)); });
    return () => { disposed = true; };
  }, [examId]);
  const modelA = data?.available_models.find(model => model.model_config_id === a);
  const modelB = data?.available_models.find(model => model.model_config_id === b);
  // 名称和版本相同的模型跨供应商仍不独立；这里只要求填写必要分差，实际模式由服务端确认。
  const requiresThresholds = Boolean(modelA && modelB && modelIdentity(modelA) !== modelIdentity(modelB));
  const thresholdsValid = validAllowedDifference(primary) && validAllowedDifference(teacher) && teacher >= primary;
  const canSave = Boolean(modelA) && data?.can_configure !== false && (!requiresThresholds || thresholdsValid);
  const save = async () => {
    if (!data || saving || !canSave) return;
    const requestOwner = owner; const session = getBusinessCommandSession();
    setSaving(true); setError("");
    try {
      const result = await saveGradingAIPlan(examId, {
        expected_revision: data.plan.revision, fallback_model_config_id: a,
        roles: { primary_a: { model_config_id: a, strength_rank: 1 }, primary_b: { model_config_id: b, strength_rank: 1 }, arbiter: { model_config_id: c, strength_rank: 2 } },
        // 单模型不使用分差，不把之前多评表单里的数值当作本次生效配置。
        decision_config: { primary_allowed_diff_points: requiresThresholds ? primary : null, teacher_allowed_diff_points: requiresThresholds ? teacher : null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" }
      });
      // 退出后重登或切换考试，旧保存回包不能覆盖当前方案或关闭新窗口。
      if (ownerRef.current !== requestOwner || session !== getBusinessCommandSession()) return;
      apply(result); message.success("本场大题 AI 评分方案已确认"); onSaved?.();
    } catch (reason) { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setError(formatError(reason)); }
    finally { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setSaving(false); }
  };
  const options = data?.available_models.map(model => ({ value: model.model_config_id, label: `${model.display_name || model.model_name} · ${model.provider_key}` })) ?? [];
  const probe = async (modelId: string) => {
    if (probing) return;
    const requestOwner = owner; const session = getBusinessCommandSession();
    setProbing(modelId); setProbeNotice("");
    try {
      const result = await probeGradingAIModel(examId, modelId);
      if (ownerRef.current !== requestOwner || session !== getBusinessCommandSession()) return;
      const fresh = await getGradingAIPlan(examId);
      if (ownerRef.current !== requestOwner || session !== getBusinessCommandSession()) return;
      // 探测只更新能力状态，不能重置管理员尚未保存的模型选择和分差设置。
      setData(previous => previous ? { ...previous, available_models: fresh.available_models } : previous);
      setProbeNotice(result.vision_probe_status === "passed" ? `图片能力检测通过${result.cache_hit ? "（复用已有检测）" : ""}` : result.vision_probe_reason || "图片能力检测未通过");
    } catch (reason) { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setProbeNotice(formatError(reason)); }
    finally { if (ownerRef.current === requestOwner && session === getBusinessCommandSession()) setProbing(""); }
  };
  const selectedModels = data?.available_models.filter(model => [a, b, c].includes(model.model_config_id)) ?? [];
  return <section className="grading-ai-plan-settings" aria-label="本场大题 AI 评分方案">
    <h2>本场大题 AI 评分方案</h2>
    <p>学校管理员在阅卷前确认。仅用于解答、计算、证明等大题；选择题和填空题不使用。只有一个独立模型也可确认单模型方案。</p>
    {error ? <Alert type="warning" showIcon message={error} /> : null}
    {data ? <>
      <small>当前已保存的方案</small>
      <GradingAIPlanDescription plan={data.plan} />
      {/* 服务端明确只读时，不展示保存和主动探测入口；旧响应缺少字段时保留原有交互。 */}
      {data.can_configure === false ? <p>请学校管理员确认本场大题 AI 评分方案。</p> : <>
      <div className="grading-ai-plan-fields">
        <label>评分模型 A<Select aria-label="评分模型 A" value={a || undefined} options={options} onChange={setA} placeholder="选择可用模型" /></label>
        <label>评分模型 B（可选）<Select aria-label="评分模型 B" allowClear value={b || undefined} options={options} onChange={value => setB(value || "")} placeholder="未配置时采用单模型" /></label>
        <label>第三评分模型（可选）<Select aria-label="第三评分模型" allowClear value={c || undefined} options={options} onChange={value => setC(value || "")} placeholder="选择能力更强的模型" /></label>
      </div>
      <div className="grading-ai-probe-list">{selectedModels.map(model => <div key={model.model_config_id}><span>{model.display_name || model.model_name} · {model.vision_probe_status === "passed" ? "图片能力已检测" : model.vision_probe_status === "failed" ? "图片能力检测未通过" : "图片能力尚未检测"}</span><Button size="small" loading={probing === model.model_config_id} disabled={Boolean(probing) && probing !== model.model_config_id} onClick={() => void probe(model.model_config_id)}>检测图片能力</Button>{model.vision_probe_reason && model.vision_probe_status !== "passed" ? <small>{model.vision_probe_reason}</small> : null}</div>)}</div>
      <small>点击检测可能产生一次模型调用；已有同配置版本的检测可复用。保存方案不会自动调用模型。</small>
      {probeNotice ? <p role="status">{probeNotice}</p> : null}
      <details className="grading-ai-thresholds" open><summary>大题分差与异常处理规则</summary>
        <div className="grading-ai-plan-fields">
          <label>前两模型允许分差<InputNumber aria-label="前两模型允许分差" min={0} max={1000} value={primary} suffix="分" disabled={!requiresThresholds} placeholder="请手动填写" onChange={value => setPrimary(value)} /></label>
          <label>三评后允许分差<InputNumber aria-label="三评后允许分差" min={primary ?? 0} max={1000} value={teacher} suffix="分" disabled={!requiresThresholds} placeholder="请手动填写" onChange={value => setTeacher(value)} /></label>
        </div>
        {requiresThresholds ? <p>两个允许分差均须手动填写，可填 0 分；三评后允许分差不得小于前两模型允许分差。</p> : <p>当前不足两个独立评分模型，可直接确认单模型方案，不需要填写分差。</p>}
        <p>A、B 差值不超过第一项时取均分，超过才启用第三评分；没有第三模型则转教师处理。按未取整分比较，三评后 C 与 A、B 均分的差值超过第二项，或三者极差超过第二项的两倍，才作为分差异常转教师处理。作答不清或模型调用失败，也不直接计零分。</p>
        <p>若设置 2 分，差 2 分不调用第三模型，超过 2 分才调用。</p>
        <small>建议分仍需按本场既有规则由教师确认。</small>
      </details>
      <Space><Button type="primary" loading={saving} disabled={!canSave} onClick={() => void save()}>确认本场大题 AI 评分方案</Button></Space>
      <small>保存用于之后启动的评分，已启动任务使用其冻结方案。</small>
      </>}
    </> : !error ? <Spin /> : null}
  </section>;
}
