import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Empty, Form, Input, InputNumber, Segmented, Select, Space, Switch, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { modelConfigOptionLabel } from "../../features/platform-model-config/lib/modelConfig";
import { getUserErrorMessage } from "../../api/client";
import {
  getModelPolicy, listModelApprovals, listModelEvaluationRuns, updateModelPolicy,
  type EvaluationRun, type ModelApproval, type TenantModelPolicy, type UpdatePolicyInput
} from "../../api/modelGovernance";
import { ModelEvaluationWorkspace } from "./ModelEvaluationWorkspace";
import { ModelApprovalWorkspace } from "./ModelApprovalWorkspace";
import { ScoringAssuranceWorkspace } from "./ScoringAssuranceWorkspace";
import { PromptVersionWorkspace } from "./PromptVersionWorkspace";
import { costToMicros, microsToCost } from "./costUnits";
import { hashQueryParam } from "../../router/query";
import { eligibilitySelection } from "./eligibilityScope";

type View = "evaluations" | "approvals" | "assurance" | "policy" | "prompts";
const modeOptions = [
  { value: "local_only", label: "仅人工 / 本地" },
  { value: "shadow_compare", label: "仅后台评测" },
  { value: "cloud_suggestion", label: "AI 提供评分建议" },
  { value: "hybrid_escalation", label: "高风险题启用 AI" },
  { value: "dual_provider_review", label: "双模型交叉复核" }
];

type PolicyFormValues = Omit<UpdatePolicyInput, "max_cost_micros_per_question" | "max_cost_micros_per_exam"> & { max_cost_micros_per_question: string | number; max_cost_micros_per_exam: string | number };
export function ManagedGovernanceWorkspace({ tenantID, schoolName, configs, onDirtyChange, onSavingChange }: Readonly<{ tenantID: string; schoolName?: string; configs: ManagedModelAPIConfig[]; onDirtyChange?: (dirty: boolean) => void; onSavingChange?: (busy: boolean) => void }>) {
  const { message } = App.useApp();
  const [view, setView] = useState<View>(() => { const requested = hashQueryParam("view"); return ["approvals", "assurance", "policy", "prompts"].includes(requested) ? requested as View : "evaluations"; });
  const stage = hashQueryParam("stage"), subject = hashQueryParam("subject"), archetype = hashQueryParam("archetype"), risk = hashQueryParam("risk");
  const initialEligibilityAxis = useMemo(() => eligibilitySelection({ stage, subject, archetype, risk }), [stage, subject, archetype, risk]);
  const [dirty, setDirty] = useState(false);
  const policyDirtyRef = useRef(false);
  const [evaluationDirty, setEvaluationDirty] = useState(false);
  const [assuranceDirty, setAssuranceDirty] = useState(false);
  const [evaluationSaving, setEvaluationSaving] = useState(false);
  const [assuranceSaving, setAssuranceSaving] = useState(false);
  const requestRef = useRef(0);
  const [runs, setRuns] = useState<EvaluationRun[]>([]);
  const [approvals, setApprovals] = useState<ModelApproval[]>([]);
  const [policy, setPolicy] = useState<TenantModelPolicy>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [form] = Form.useForm<PolicyFormValues>();
  const savingScopesRef = useRef({ policy: false, evaluation: false, assurance: false });
  // 保存开始时同步通知外层学校选择器；不能等重绘，否则同一轮排队的切校可卸载原范围请求。
  const markSaving = useCallback((scope: "policy" | "evaluation" | "assurance", busy: boolean) => {
    savingScopesRef.current[scope] = busy;
    onSavingChange?.(Object.values(savingScopesRef.current).some(Boolean));
  }, [onSavingChange]);
  const handleEvaluationSaving = useCallback((busy: boolean) => { setEvaluationSaving(busy); markSaving("evaluation", busy); }, [markSaving]);
  const handleAssuranceSaving = useCallback((busy: boolean) => { setAssuranceSaving(busy); markSaving("assurance", busy); }, [markSaving]);
  useEffect(() => { onDirtyChange?.(dirty || evaluationDirty || assuranceDirty); return () => onDirtyChange?.(false); }, [dirty, evaluationDirty, assuranceDirty, onDirtyChange]);
  useEffect(() => () => { requestRef.current++; onSavingChange?.(false); }, [onSavingChange]);

  const load = useCallback(async () => {
    if (!tenantID) return;
    const requestID = ++requestRef.current;
    setLoading(true);
    setError("");
    try {
      const [runResult, approvalResult, policyResult] = await Promise.all([
        listModelEvaluationRuns(tenantID, { limit: 100 }),
        listModelApprovals(tenantID),
        getModelPolicy(tenantID)
      ]);
      // 学校或组件已切换时，旧范围的策略和表单不能回填到新学校。
      if (requestID !== requestRef.current) return;
      setRuns(runResult.evaluation_runs);
      setApprovals(approvalResult.model_approvals);
      // 另一评测保存后的列表刷新不能覆盖正在编辑的策略及其原expected_version。
      if (!policyDirtyRef.current) {
      setPolicy(policyResult.policy);
      form.setFieldsValue({
        display_name: policyResult.policy.display_name,
        mode: policyResult.policy.mode,
        external_enabled: policyResult.policy.external_enabled,
        text_export_enabled: policyResult.policy.text_export_enabled,
        image_export_enabled: policyResult.policy.image_export_enabled,
        allowed_model_config_ids: policyResult.policy.allowed_model_config_ids ?? [],
        max_cost_micros_per_question: microsToCost(policyResult.policy.max_cost_micros_per_question),
        max_cost_micros_per_exam: microsToCost(policyResult.policy.max_cost_micros_per_exam),
        fallback_mode: policyResult.policy.fallback_mode,
        reason: ""
      });
      setDirty(false);
      }
    } catch (nextError) {
      if (requestID !== requestRef.current) return;
      setError(getUserErrorMessage(nextError, "无法读取学校模型治理数据"));
    } finally {
      if (requestID === requestRef.current) setLoading(false);
    }
  }, [form, tenantID]);

  useEffect(() => { void load(); }, [load]);

  const savePolicy = async () => {
    if (!policy) return;
    const values = await form.validateFields();
    let questionBudget: number, examBudget: number;
    try { questionBudget = costToMicros(values.max_cost_micros_per_question); examBudget = costToMicros(values.max_cost_micros_per_exam); } catch (reason) { message.error(getUserErrorMessage(reason, "请核对预算金额")); return; }
    setSaving(true); markSaving("policy", true);
    try {
      await updateModelPolicy(tenantID, {
        ...values,
        max_cost_micros_per_question: questionBudget,
        max_cost_micros_per_exam: examBudget,
        // 未允许外部模型时提交空白名单，不能继续带上表单中此前选中的配置。
        allowed_model_config_ids: values.external_enabled ? values.allowed_model_config_ids ?? [] : [],
        expected_version: policy.version
      });
      message.success("使用策略已更新");
      policyDirtyRef.current = false;
      await load();
    } catch (nextError) {
      message.error(getUserErrorMessage(nextError, "更新使用策略失败"));
    } finally {
      setSaving(false); markSaving("policy", false);
    }
  };

  if (!tenantID) return <Empty description="请先选择学校" />;

  return <section className="model-governance-workspace">
    <p>当前学校：{schoolName || tenantID}。模型证据与使用策略按该学校管理；系统提示词来自平台部署的只读版本。</p>
    {subject ? <p>已带入当前评分组合的学段与学科。通配绑定或作文等业务题型不能代替准入的具体题型；请在正式评分使用检查中补齐题型和题目实际风险级别。</p> : null}
    <Space wrap style={{ marginBottom: 18 }}>
      <Segmented value={view} disabled={saving || evaluationSaving || assuranceSaving} onChange={(value) => { const next = value as View; setView(next); const [path, query = ""] = window.location.hash.split("?"); const params = new URLSearchParams(query); params.set("view", next); window.history.replaceState(window.history.state, "", `${window.location.pathname}${window.location.search}${path}?${params}`); }} options={[
        { value: "evaluations", label: "对比候选模型" },
        { value: "approvals", label: "模型批准" },
        { value: "assurance", label: "正式评分使用检查" },
        { value: "policy", label: "使用策略" },
        { value: "prompts", label: "系统提示词" }
      ]} />
      <Button icon={<RefreshCw size={15} />} disabled={dirty || saving || evaluationDirty || assuranceDirty || evaluationSaving || assuranceSaving} loading={loading} onClick={() => void load()}>刷新</Button>
    </Space>
    {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} /> : null}
    <div hidden={view !== "evaluations"}><ModelEvaluationWorkspace tenantID={tenantID} configs={configs} runs={runs} loading={loading} canManage onRefresh={load} onDirtyChange={setEvaluationDirty} onSavingChange={handleEvaluationSaving} /></div>
    {view === "approvals" ? <ModelApprovalWorkspace tenantID={tenantID} approvals={approvals} evaluationRuns={runs} canManage onRefresh={load} /> : null}
    <div hidden={view !== "assurance"}><ScoringAssuranceWorkspace tenantID={tenantID} initialAxis={initialEligibilityAxis} canReadEligibility canManageEligibility canManageEvaluations canReadDisagreements canManageDisagreements onDirtyChange={setAssuranceDirty} onSavingChange={handleAssuranceSaving} /></div>
    {view === "prompts" ? <PromptVersionWorkspace /> : null}
    {view === "policy" && policy ? <div className="model-policy-layout">
      <div className="model-policy-primary">
        <span className="model-policy-label">当前生效策略 · 版本 {policy.version}</span>
        <h2>{policy.display_name}</h2>
        <p>已允许 {policy.allowed_model_config_ids?.length ?? 0} 个学校模型</p>
        {(policy.allowed_model_config_ids ?? []).map((id) => {
          const config = configs.find((item) => item.id === id);
          return <Tag key={id}>{config ? modelConfigOptionLabel(config) : "历史模型配置（已停用）"}</Tag>;
        })}
      </div>
      <div className="model-policy-inspector">
        <Form form={form} layout="vertical" disabled={saving} onValuesChange={() => { policyDirtyRef.current = true; setDirty(true); }} onFinish={() => void savePolicy()}>
          <Form.Item name="display_name" label="策略名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="mode" label="使用方式" rules={[{ required: true }]}><Select options={modeOptions} /></Form.Item>
          <Form.Item name="external_enabled" label="允许外部模型" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="text_export_enabled" label="允许发送文本" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="image_export_enabled" label="允许发送图片" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="allowed_model_config_ids" label="允许使用的学校模型">
            <Select mode="multiple" options={configs.filter((item) => item.status === "active" && item.last_capability_status === "success" && item.last_capability_probe_version === "structured-json-v3").map((item) => ({ value: item.id, label: modelConfigOptionLabel(item) }))} />
          </Form.Item>
          <Form.Item name="max_cost_micros_per_question" label="每题预算上限（美元）" extra="0 表示尚未配置预算，不能通过外部沙箱准入；不是不限额。" rules={[{ required: true }]}><InputNumber stringMode min="0" precision={6} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="max_cost_micros_per_exam" label="整场考试预算上限（美元）" extra="按平台微美元口径保存，最多六位小数；不是本月的学校总额度。" rules={[{ required: true }]}><InputNumber stringMode min="0" precision={6} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="fallback_mode" label="故障回退" rules={[{ required: true }]}><Select options={[{ value: "manual_only", label: "转人工" }, { value: "approved_deployment_only", label: "仅用已批准模型" }]} /></Form.Item>
          <Form.Item name="reason" label="修改原因" rules={[{ required: true, min: 4 }]}><Input.TextArea rows={2} /></Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>保存策略</Button>
        </Form>
      </div>
    </div> : null}
  </section>;
}
