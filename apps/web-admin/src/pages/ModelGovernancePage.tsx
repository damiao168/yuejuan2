import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Drawer,
  Form,
  Input,
  InputNumber,
  Segmented,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip
} from "antd";
import { AnimatePresence, motion } from "framer-motion";
import {
  CheckCircle2,
  RefreshCw,
  Route,
  TriangleAlert
} from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getAIGradingStatus, type AIGradingRuntimeStatus } from "../api/system";
import {
  getModelPolicy,
  listModelDeployments,
  listModelEvaluationRuns,
  listModelApprovals,
  updateModelPolicy,
  type EvaluationRun,
  type ModelApproval,
  type ModelDeployment,
  type PolicyMode,
  type TenantModelPolicy,
  type UpdatePolicyInput
} from "../api/modelGovernance";
import { ErrorState, LoadingState } from "../components/PageState";
import { ModelEvaluationWorkspace } from "../components/model-governance/ModelEvaluationWorkspace";
import { ModelApprovalWorkspace } from "../components/model-governance/ModelApprovalWorkspace";
import { PromptVersionWorkspace } from "../components/model-governance/PromptVersionWorkspace";
import { ScoringAssuranceWorkspace } from "../components/model-governance/ScoringAssuranceWorkspace";

type GovernanceView = "prompts" | "evaluations" | "assurance" | "approvals" | "policy";

const policyModeLabels: Record<PolicyMode, string> = {
  local_only: "仅本地",
  shadow_compare: "影子对比",
  cloud_suggestion: "云端建议",
  hybrid_escalation: "混合升级",
  dual_provider_review: "双模型复核"
};

function errorMessage(error: unknown) {
  if (error instanceof ApiClientError) {
    return getUserErrorMessage(error, "请求失败，请稍后重试");
  }
  return getUserErrorMessage(error, "请求失败，请稍后重试");
}

function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function microsToYuan(value: number) {
  return `¥${(value / 1_000_000).toFixed(value >= 100_000 ? 2 : 4)}`;
}

export function ModelGovernancePage({
  canManagePolicy,
  canManageEvaluations,
  canReadEligibility,
  canReadDisagreements,
  canManageDisagreements
}: {
  canManagePolicy: boolean;
  canManageEvaluations: boolean;
  canReadEligibility: boolean;
  canReadDisagreements: boolean;
  canManageDisagreements: boolean;
}) {
  const { message } = App.useApp();
  const [deployments, setDeployments] = useState<ModelDeployment[]>([]);
  const [evaluationRuns, setEvaluationRuns] = useState<EvaluationRun[]>([]);
  const [nextEvaluationCursor, setNextEvaluationCursor] = useState("");
  const [hasMoreEvaluations, setHasMoreEvaluations] = useState(false);
  const [loadingMoreEvaluations, setLoadingMoreEvaluations] = useState(false);
  const [modelApprovals, setModelApprovals] = useState<ModelApproval[]>([]);
  const [policy, setPolicy] = useState<TenantModelPolicy | null>(null);
  const [activeView, setActiveView] = useState<GovernanceView>("prompts");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [policyDrawerOpen, setPolicyDrawerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [aiStatus, setAIStatus] = useState<AIGradingRuntimeStatus>();
  const [policyForm] = Form.useForm<UpdatePolicyInput>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [deploymentResponse, policyResponse, evaluationResponse, approvalResponse, aiStatusResponse] = await Promise.all([
        listModelDeployments(),
        getModelPolicy(),
        listModelEvaluationRuns({ limit: 30 }),
        listModelApprovals(),
        getAIGradingStatus()
      ]);
      setDeployments(deploymentResponse.deployments);
      setPolicy(policyResponse.policy);
      setEvaluationRuns(evaluationResponse.evaluation_runs);
      setNextEvaluationCursor(evaluationResponse.next_cursor ?? "");
      setHasMoreEvaluations(Boolean(evaluationResponse.has_more));
      setModelApprovals(approvalResponse.model_approvals);
      setAIStatus(aiStatusResponse.ai_grading);
    } catch (nextError) {
      setError(errorMessage(nextError));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadMoreEvaluations = useCallback(async () => {
    if (!hasMoreEvaluations || !nextEvaluationCursor || loadingMoreEvaluations) return;
    setLoadingMoreEvaluations(true);
    try {
      const response = await listModelEvaluationRuns({ limit: 30, cursor: nextEvaluationCursor });
      setEvaluationRuns((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        response.evaluation_runs.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setNextEvaluationCursor(response.next_cursor ?? "");
      setHasMoreEvaluations(Boolean(response.has_more));
    } catch (nextError) {
      message.error(errorMessage(nextError));
    } finally {
      setLoadingMoreEvaluations(false);
    }
  }, [hasMoreEvaluations, loadingMoreEvaluations, message, nextEvaluationCursor]);

  useEffect(() => {
    void load();
  }, [load]);

  const deployableExternal = deployments.filter(
    (item) => item.provider_key !== "local" && item.status !== "disabled"
  );

  const openPolicyEditor = () => {
    if (!policy) return;
    policyForm.setFieldsValue({
      display_name: policy.display_name,
      mode: policy.mode,
      external_enabled: policy.external_enabled,
      text_export_enabled: policy.text_export_enabled,
      image_export_enabled: policy.image_export_enabled,
      allowed_deployments: policy.allowed_deployments,
      max_cost_micros_per_question: policy.max_cost_micros_per_question,
      max_cost_micros_per_exam: policy.max_cost_micros_per_exam,
      fallback_mode: policy.fallback_mode,
      expected_version: policy.version,
      reason: ""
    });
    setPolicyDrawerOpen(true);
  };

  const submitPolicy = async () => {
    const values = await policyForm.validateFields();
    const externalEnabled = Boolean(values.external_enabled);
    setSaving(true);
    try {
      await updateModelPolicy({
        ...values,
        mode: externalEnabled ? values.mode : "local_only",
        external_enabled: externalEnabled,
        text_export_enabled: externalEnabled && Boolean(values.text_export_enabled),
        image_export_enabled: externalEnabled && Boolean(values.image_export_enabled),
        allowed_deployments: externalEnabled ? (values.allowed_deployments ?? []) : [],
        max_cost_micros_per_question: values.max_cost_micros_per_question ?? 0,
        max_cost_micros_per_exam: values.max_cost_micros_per_exam ?? 0,
        fallback_mode: values.fallback_mode ?? "manual_only",
        expected_version: policy?.version ?? values.expected_version
      });
      message.success("模型策略已更新，版本号已递增");
      setPolicyDrawerOpen(false);
      await load();
    } catch (nextError) {
      message.error(errorMessage(nextError));
    } finally {
      setSaving(false);
    }
  };

  const policyFacts = useMemo(() => {
    if (!policy) return [];
    return [
      { label: "路由模式", value: policyModeLabels[policy.mode] },
      { label: "文本外发", value: policy.text_export_enabled ? "允许" : "关闭" },
      { label: "图片外发", value: policy.image_export_enabled ? "允许" : "关闭" },
      { label: "失败回退", value: policy.fallback_mode === "manual_only" ? "仅人工" : "仅批准部署" }
    ];
  }, [policy]);

  return (
    <div className="model-governance-shell">
      <motion.section
        className="page-heading model-governance-heading"
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.22 }}
      >
        <div>
          <h1>模型质量治理</h1>
          <p>模型 API 与学校模型由“模型配置”统一管理；这里负责提示词、评测、评分保障、批准和运行策略。</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
        </Space>
      </motion.section>

      {loading && !policy ? <LoadingState label="读取模型质量治理配置" /> : null}
      {error ? <ErrorState message={error} onRetry={() => void load()} /> : null}
      {aiStatus ? (
        <Alert
          type={aiStatus.mode === "real" && aiStatus.available ? "success" : "warning"}
          showIcon
          message={aiStatus.mode === "real" && aiStatus.available ? "AI 阅卷服务已配置" : aiStatus.mode === "mock" ? "Mock 模式，仅用于开发或演示" : "AI 阅卷未启用"}
          description={`运行模式：${aiStatus.mode}；模型：${aiStatus.model_version || "未配置"}；Prompt：${aiStatus.prompt_version || "未配置"}。治理页面可打开不代表模型已具备生产评分能力。`}
        />
      ) : null}

      {!error && policy ? (
        <>
          <motion.section
            className="model-governance-summary"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.06, duration: 0.2 }}
          >
            <div>
              <span>当前策略</span>
              <strong>{policyModeLabels[policy.mode]}</strong>
              <small>版本 {policy.version}</small>
            </div>
            <div>
              <span>外部调用</span>
              <strong className={policy.external_enabled ? "warning" : "safe"}>{policy.external_enabled ? "已授权" : "默认关闭"}</strong>
              <small>{policy.external_enabled ? `${policy.allowed_deployments.length} 个允许部署` : "不会外发答题数据"}</small>
            </div>
          </motion.section>

          <Alert
            className="model-governance-boundary"
            type={policy.external_enabled ? "warning" : "success"}
            showIcon
            icon={policy.external_enabled ? <TriangleAlert size={18} /> : <CheckCircle2 size={18} />}
            message={policy.external_enabled ? "租户已授权部分外部处理" : "外部模型保持关闭"}
            description={policy.external_enabled
              ? "只有 allowlist 中且已通过验收、健康可用的原生部署才可能被路由；当前页面不会直接改变成绩。"
              : "本地部署继续提供评分建议，任何故障回退都不能绕过租户外发开关。"}
          />

          <section className="model-governance-workspace">
            <div className="model-governance-toolbar">
              <Segmented
                value={activeView}
                onChange={(value) => setActiveView(value as GovernanceView)}
                options={[
                  { value: "prompts", label: "系统提示词" },
                  { value: "evaluations", label: `评测 ${evaluationRuns.length}` },
                  { value: "assurance", label: "评分保障" },
                  { value: "approvals", label: `批准 ${modelApprovals.filter((item) => !item.revoked_at && new Date(item.expires_at).getTime() > Date.now()).length}` },
                  { value: "policy", label: "租户策略" }
                ]}
              />
              {activeView === "policy" && canManagePolicy ? (
                <Button type="primary" icon={<Route size={16} />} onClick={openPolicyEditor}>编辑策略</Button>
              ) : null}
            </div>

            <AnimatePresence mode="wait">
              <motion.div
                key={activeView}
                initial={{ opacity: 0, y: 6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -4 }}
                transition={{ duration: 0.16 }}
              >
                {activeView === "prompts" ? <PromptVersionWorkspace /> : null}
                {activeView === "evaluations" ? (
                  <Space direction="vertical" size="middle" style={{ width: "100%" }}>
                    <ModelEvaluationWorkspace
                      runs={evaluationRuns}
                      deployments={deployments}
                      loading={loading}
                      canManage={canManageEvaluations}
                      onRefresh={load}
                    />
                    {hasMoreEvaluations ? (
                      <Button block loading={loadingMoreEvaluations} onClick={() => void loadMoreEvaluations()}>
                        加载更多评测记录
                      </Button>
                    ) : null}
                  </Space>
                ) : null}
                {activeView === "assurance" ? (
                  <ScoringAssuranceWorkspace
                    canReadEligibility={canReadEligibility}
                    canManageEligibility={canManagePolicy}
                    canManageEvaluations={canManageEvaluations}
                    canReadDisagreements={canReadDisagreements}
                    canManageDisagreements={canManageDisagreements}
                  />
                ) : null}
                {activeView === "approvals" ? (
                  <ModelApprovalWorkspace
                    approvals={modelApprovals}
                    evaluationRuns={evaluationRuns}
                    canManage={canManageEvaluations}
                    onRefresh={load}
                  />
                ) : null}
                {activeView === "policy" ? (
                  <div className="model-policy-layout">
                    <div className="model-policy-primary">
                      <span className="model-policy-label">当前生效策略</span>
                      <h2>{policy.display_name}</h2>
                      <p>最近更新：{formatTime(policy.updated_at)} · 版本 {policy.version}</p>
                      <div className="model-policy-facts">
                        {policyFacts.map((fact) => (
                          <div key={fact.label}><span>{fact.label}</span><strong>{fact.value}</strong></div>
                        ))}
                      </div>
                    </div>
                    <div className="model-policy-inspector">
                      <div>
                        <span>允许部署</span>
                        {policy.allowed_deployments.length > 0
                          ? policy.allowed_deployments.map((key) => <Tag key={key}>{key}</Tag>)
                          : <strong>无</strong>}
                      </div>
                      <div>
                        <span>单题预算</span>
                        <strong>{microsToYuan(policy.max_cost_micros_per_question)}</strong>
                      </div>
                      <div>
                        <span>单考试预算</span>
                        <strong>{microsToYuan(policy.max_cost_micros_per_exam)}</strong>
                      </div>
                      <Tooltip title="未通过 promotion 的外部部署仍不能参与评分路由">
                        <div>
                          <span>可登记外部部署</span>
                          <strong>{deployableExternal.length}</strong>
                        </div>
                      </Tooltip>
                    </div>
                  </div>
                ) : null}
              </motion.div>
            </AnimatePresence>
          </section>
        </>
      ) : null}

      <Drawer
        title="编辑租户模型策略"
        width={560}
        open={policyDrawerOpen}
        onClose={() => setPolicyDrawerOpen(false)}
        extra={<Button type="primary" loading={saving} onClick={() => void submitPolicy()}>保存新版本</Button>}
      >
        <Form form={policyForm} layout="vertical" className="model-governance-form">
          <Form.Item name="display_name" label="策略名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="external_enabled" label="允许外部模型" valuePropName="checked">
            <Switch
              checkedChildren="允许"
              unCheckedChildren="关闭"
              onChange={(checked) => {
                if (checked && policyForm.getFieldValue("mode") === "local_only") {
                  policyForm.setFieldValue("mode", "shadow_compare");
                }
                if (!checked) {
                  policyForm.setFieldsValue({
                    mode: "local_only",
                    text_export_enabled: false,
                    image_export_enabled: false,
                    allowed_deployments: []
                  });
                }
              }}
            />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(before, after) => before.external_enabled !== after.external_enabled}>
            {({ getFieldValue }) => {
              const enabled = Boolean(getFieldValue("external_enabled"));
              return (
                <>
                  <Form.Item name="mode" label="路由模式" rules={[{ required: true }]}>
                    <Select
                      disabled={!enabled}
                      options={Object.entries(policyModeLabels)
                        .filter(([value]) => value !== "local_only")
                        .map(([value, label]) => ({ value, label }))}
                    />
                  </Form.Item>
                  <div className="model-governance-switch-row">
                    <Form.Item name="text_export_enabled" label="允许文本外发" valuePropName="checked"><Switch disabled={!enabled} /></Form.Item>
                    <Form.Item name="image_export_enabled" label="允许题目裁剪图外发" valuePropName="checked"><Switch disabled={!enabled} /></Form.Item>
                  </div>
                  <Form.Item name="allowed_deployments" label="Deployment allowlist">
                    <Select
                      mode="multiple"
                      disabled={!enabled}
                      options={deployableExternal.map((item) => ({ value: item.deployment_key, label: `${item.model_name} · ${item.deployment_key}` }))}
                    />
                  </Form.Item>
                </>
              );
            }}
          </Form.Item>
          <div className="model-governance-form-row two">
            <Form.Item name="max_cost_micros_per_question" label="单题预算（微元）"><InputNumber min={0} precision={0} /></Form.Item>
            <Form.Item name="max_cost_micros_per_exam" label="单考试预算（微元）"><InputNumber min={0} precision={0} /></Form.Item>
          </div>
          <Form.Item name="fallback_mode" label="失败回退" rules={[{ required: true }]}>
            <Select options={[{ value: "manual_only", label: "仅进入人工队列" }, { value: "approved_deployment_only", label: "仅回退到已批准部署" }]} />
          </Form.Item>
          <Form.Item name="reason" label="变更原因" rules={[{ required: true, min: 4 }]}><Input.TextArea rows={3} placeholder="说明授权范围、目的和复核安排" /></Form.Item>
          <Form.Item name="expected_version" hidden><InputNumber /></Form.Item>
        </Form>
      </Drawer>

    </div>
  );
}
