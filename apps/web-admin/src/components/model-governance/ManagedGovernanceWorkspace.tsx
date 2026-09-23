import { useCallback, useEffect, useState } from "react";
import { Alert, App, Button, Empty, Form, Input, InputNumber, Segmented, Select, Space, Switch, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { getUserErrorMessage } from "../../api/client";
import {
  getModelPolicy, listModelApprovals, listModelEvaluationRuns, updateModelPolicy,
  type EvaluationRun, type ModelApproval, type TenantModelPolicy, type UpdatePolicyInput
} from "../../api/modelGovernance";
import { ModelEvaluationWorkspace } from "./ModelEvaluationWorkspace";
import { ModelApprovalWorkspace } from "./ModelApprovalWorkspace";
import { ScoringAssuranceWorkspace } from "./ScoringAssuranceWorkspace";
import { PromptVersionWorkspace } from "./PromptVersionWorkspace";

type View = "evaluations" | "approvals" | "assurance" | "policy" | "prompts";
const modeOptions = [
  { value: "local_only", label: "仅人工 / 本地" },
  { value: "shadow_compare", label: "仅后台评测" },
  { value: "cloud_suggestion", label: "AI 提供评分建议" },
  { value: "hybrid_escalation", label: "高风险题启用 AI" },
  { value: "dual_provider_review", label: "双模型交叉复核" }
];

export function ManagedGovernanceWorkspace({ tenantID, configs }: { tenantID: string; configs: ManagedModelAPIConfig[] }) {
  const { message } = App.useApp();
  const [view, setView] = useState<View>("evaluations");
  const [runs, setRuns] = useState<EvaluationRun[]>([]);
  const [approvals, setApprovals] = useState<ModelApproval[]>([]);
  const [policy, setPolicy] = useState<TenantModelPolicy>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [form] = Form.useForm<UpdatePolicyInput>();

  const load = useCallback(async () => {
    if (!tenantID) return;
    setLoading(true);
    setError("");
    try {
      const [runResult, approvalResult, policyResult] = await Promise.all([
        listModelEvaluationRuns(tenantID, { limit: 100 }),
        listModelApprovals(tenantID),
        getModelPolicy(tenantID)
      ]);
      setRuns(runResult.evaluation_runs);
      setApprovals(approvalResult.model_approvals);
      setPolicy(policyResult.policy);
      form.setFieldsValue({
        display_name: policyResult.policy.display_name,
        mode: policyResult.policy.mode,
        external_enabled: policyResult.policy.external_enabled,
        text_export_enabled: policyResult.policy.text_export_enabled,
        image_export_enabled: policyResult.policy.image_export_enabled,
        allowed_model_config_ids: policyResult.policy.allowed_model_config_ids ?? [],
        max_cost_micros_per_question: policyResult.policy.max_cost_micros_per_question,
        max_cost_micros_per_exam: policyResult.policy.max_cost_micros_per_exam,
        fallback_mode: policyResult.policy.fallback_mode,
        reason: ""
      });
    } catch (nextError) {
      setError(getUserErrorMessage(nextError, "无法读取学校模型治理数据"));
    } finally {
      setLoading(false);
    }
  }, [form, tenantID]);

  useEffect(() => { void load(); }, [load]);

  const savePolicy = async () => {
    if (!policy) return;
    const values = await form.validateFields();
    setSaving(true);
    try {
      await updateModelPolicy(tenantID, {
        ...values,
        allowed_model_config_ids: values.external_enabled ? values.allowed_model_config_ids ?? [] : [],
        expected_version: policy.version
      });
      message.success("使用策略已更新");
      await load();
    } catch (nextError) {
      message.error(getUserErrorMessage(nextError, "更新使用策略失败"));
    } finally {
      setSaving(false);
    }
  };

  if (!tenantID) return <Empty description="请先选择学校" />;

  return <section className="model-governance-workspace">
    <Space wrap style={{ marginBottom: 18 }}>
      <Segmented value={view} onChange={(value) => setView(value as View)} options={[
        { value: "evaluations", label: "模型评测" },
        { value: "approvals", label: "模型批准" },
        { value: "assurance", label: "评分保障" },
        { value: "policy", label: "使用策略" },
        { value: "prompts", label: "系统提示词" }
      ]} />
      <Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button>
    </Space>
    {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} /> : null}
    {view === "evaluations" ? <ModelEvaluationWorkspace tenantID={tenantID} configs={configs} runs={runs} loading={loading} canManage onRefresh={load} /> : null}
    {view === "approvals" ? <ModelApprovalWorkspace tenantID={tenantID} approvals={approvals} evaluationRuns={runs} canManage onRefresh={load} /> : null}
    {view === "assurance" ? <ScoringAssuranceWorkspace tenantID={tenantID} canReadEligibility canManageEligibility canManageEvaluations canReadDisagreements canManageDisagreements /> : null}
    {view === "prompts" ? <PromptVersionWorkspace /> : null}
    {view === "policy" && policy ? <div className="model-policy-layout">
      <div className="model-policy-primary">
        <span className="model-policy-label">当前生效策略 · 版本 {policy.version}</span>
        <h2>{policy.display_name}</h2>
        <p>已允许 {policy.allowed_model_config_ids?.length ?? 0} 个学校模型</p>
        {(policy.allowed_model_config_ids ?? []).map((id) => <Tag key={id}>{configs.find((item) => item.id === id)?.model_name ?? "历史模型配置（已停用）"}</Tag>)}
      </div>
      <div className="model-policy-inspector">
        <Form form={form} layout="vertical" onFinish={() => void savePolicy()}>
          <Form.Item name="display_name" label="策略名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="mode" label="使用方式" rules={[{ required: true }]}><Select options={modeOptions} /></Form.Item>
          <Form.Item name="external_enabled" label="允许外部模型" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="text_export_enabled" label="允许发送文本" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="image_export_enabled" label="允许发送图片" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="allowed_model_config_ids" label="允许使用的学校模型">
            <Select mode="multiple" options={configs.filter((item) => item.status === "active" && item.last_capability_status === "success" && item.last_capability_probe_version === "structured-json-v3").map((item) => ({ value: item.id, label: `${item.display_name} · ${item.model_name}` }))} />
          </Form.Item>
          <Form.Item name="max_cost_micros_per_question" label="单题预算（微元）"><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="max_cost_micros_per_exam" label="单考试预算（微元）"><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="fallback_mode" label="故障回退" rules={[{ required: true }]}><Select options={[{ value: "manual_only", label: "转人工" }, { value: "approved_deployment_only", label: "仅用已批准模型" }]} /></Form.Item>
          <Form.Item name="reason" label="修改原因" rules={[{ required: true, min: 4 }]}><Input.TextArea rows={2} /></Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>保存策略</Button>
        </Form>
      </div>
    </div> : null}
  </section>;
}
