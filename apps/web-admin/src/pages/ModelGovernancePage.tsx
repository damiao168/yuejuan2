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
  Tooltip,
  type TableColumnsType
} from "antd";
import { AnimatePresence, motion } from "framer-motion";
import {
  Bot,
  CheckCircle2,
  CloudCog,
  KeyRound,
  Plus,
  RefreshCw,
  Route,
  Server,
  TriangleAlert
} from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getAIGradingStatus, type AIGradingRuntimeStatus } from "../api/system";
import {
  createModelDeployment,
  createModelProvider,
  getModelPolicy,
  listModelDeployments,
  listModelEvaluationRuns,
  listModelApprovals,
  listModelProviders,
  updateModelPolicy,
  type CreateDeploymentInput,
  type CreateProviderInput,
  type EvaluationRun,
  type ModelApproval,
  type ModelDeployment,
  type ModelProvider,
  type PolicyMode,
  type TenantModelPolicy,
  type UpdatePolicyInput
} from "../api/modelGovernance";
import { ErrorState, LoadingState } from "../components/PageState";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { ModelEvaluationWorkspace } from "../components/model-governance/ModelEvaluationWorkspace";
import { ModelApprovalWorkspace } from "../components/model-governance/ModelApprovalWorkspace";
import { PromptVersionWorkspace } from "../components/model-governance/PromptVersionWorkspace";
import { ScoringAssuranceWorkspace } from "../components/model-governance/ScoringAssuranceWorkspace";

type GovernanceView = "providers" | "deployments" | "prompts" | "evaluations" | "assurance" | "approvals" | "policy";

const policyModeLabels: Record<PolicyMode, string> = {
  local_only: "仅本地",
  shadow_compare: "影子对比",
  cloud_suggestion: "云端建议",
  hybrid_escalation: "混合升级",
  dual_provider_review: "双模型复核"
};

const statusLabels: Record<string, string> = {
  unverified: "未验收",
  active: "已启用",
  degraded: "降级",
  rate_limited: "限流",
  disabled: "已停用",
  shadow_only: "仅影子",
  available: "可用",
  unavailable: "不可用"
};

function errorMessage(error: unknown) {
  if (error instanceof ApiClientError) {
    return getUserErrorMessage(error, "请求失败，请稍后重试");
  }
  return getUserErrorMessage(error, "请求失败，请稍后重试");
}

function statusColor(status: string) {
  if (status === "active" || status === "available") return "success";
  if (status === "disabled" || status === "unavailable") return "default";
  if (status === "degraded" || status === "rate_limited") return "warning";
  return "processing";
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
  canManageProviders,
  canManagePolicy,
  canManageEvaluations,
  canReadEligibility,
  canReadDisagreements,
  canManageDisagreements
}: {
  canManageProviders: boolean;
  canManagePolicy: boolean;
  canManageEvaluations: boolean;
  canReadEligibility: boolean;
  canReadDisagreements: boolean;
  canManageDisagreements: boolean;
}) {
  const { message } = App.useApp();
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [deployments, setDeployments] = useState<ModelDeployment[]>([]);
  const [evaluationRuns, setEvaluationRuns] = useState<EvaluationRun[]>([]);
  const [nextEvaluationCursor, setNextEvaluationCursor] = useState("");
  const [hasMoreEvaluations, setHasMoreEvaluations] = useState(false);
  const [loadingMoreEvaluations, setLoadingMoreEvaluations] = useState(false);
  const [modelApprovals, setModelApprovals] = useState<ModelApproval[]>([]);
  const [policy, setPolicy] = useState<TenantModelPolicy | null>(null);
  const [activeView, setActiveView] = useState<GovernanceView>("providers");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [providerDrawerOpen, setProviderDrawerOpen] = useState(false);
  const [deploymentDrawerOpen, setDeploymentDrawerOpen] = useState(false);
  const [policyDrawerOpen, setPolicyDrawerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [aiStatus, setAIStatus] = useState<AIGradingRuntimeStatus>();
  const [providerForm] = Form.useForm<CreateProviderInput>();
  const [deploymentForm] = Form.useForm<CreateDeploymentInput & { meter: string; input_micros?: number; output_micros?: number }>();
  const [policyForm] = Form.useForm<UpdatePolicyInput>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [providerResponse, deploymentResponse, policyResponse, evaluationResponse, approvalResponse, aiStatusResponse] = await Promise.all([
        listModelProviders(),
        listModelDeployments(),
        getModelPolicy(),
        listModelEvaluationRuns({ limit: 30 }),
        listModelApprovals(),
        getAIGradingStatus()
      ]);
      setProviders(providerResponse.providers);
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

  const externalProviders = providers.filter((item) => item.provider_kind === "external");
  const localDeployments = deployments.filter((item) => item.provider_key === "local");
  const pendingExternalDeployments = deployments.filter(
    (item) => item.provider_key !== "local" && item.status === "unverified"
  );
  const deployableExternal = deployments.filter(
    (item) => item.provider_key !== "local" && item.status !== "disabled"
  );

  const providerColumns: TableColumnsType<ModelProvider> = [
    {
      title: "供应商",
      dataIndex: "display_name",
      render: (_value, record) => (
        <div className="model-governance-identity">
          <span className={record.provider_kind === "local" ? "local" : "external"}>
            {record.provider_kind === "local" ? <Server size={16} /> : <CloudCog size={16} />}
          </span>
          <div>
            <strong>{record.display_name}</strong>
            <small>{record.provider_key}</small>
          </div>
        </div>
      )
    },
    {
      title: "协议边界",
      dataIndex: "adapter_type",
      render: (value: string, record) => (
        <div className="model-governance-stack">
          <span>{value}</span>
          <small>{record.provider_kind === "external" ? "厂商原生 Adapter" : "本地运行时"}</small>
        </div>
      )
    },
    {
      title: "区域",
      dataIndex: "region",
      width: 140
    },
    {
      title: "Secret",
      dataIndex: "credential_reference_set",
      width: 160,
      render: (configured: boolean, record) => record.provider_kind === "local"
        ? <span className="muted">无需凭据</span>
        : configured
          ? <Tag icon={<KeyRound size={12} />} color="success">{record.credential_scheme} 引用</Tag>
          : <Tag color="error">未设置</Tag>
    },
    {
      title: "数据策略",
      dataIndex: "data_policy",
      width: 170,
      render: (_value, record) => (
        <div className="model-governance-stack">
          <span>禁止训练</span>
          <small>{record.data_policy.retention_mode === "no_store" ? "不保留" : "合同约定保留"}</small>
        </div>
      )
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 120,
      render: (value: string) => <Tag color={statusColor(value)}>{statusLabels[value] ?? "未知状态"}</Tag>
    }
  ];

  const deploymentColumns: TableColumnsType<ModelDeployment> = [
    {
      title: "部署",
      dataIndex: "model_name",
      render: (_value, record) => (
        <div className="model-governance-identity">
          <span className={record.provider_key === "local" ? "local" : "external"}><Bot size={16} /></span>
          <div>
            <strong>{record.model_name}</strong>
            <small>{record.deployment_key}</small>
          </div>
        </div>
      )
    },
    {
      title: "模型版本",
      dataIndex: "model_version",
      ellipsis: true
    },
    {
      title: "能力",
      dataIndex: "capability_profile",
      render: (value: string, record) => (
        <div className="model-governance-stack">
          <span>{value}</span>
          <small>{record.modalities.join(" / ")}</small>
        </div>
      )
    },
    {
      title: "计量",
      dataIndex: "pricing_policy",
      width: 130,
      render: (value?: Record<string, unknown>) => String(value?.meter ?? "未配置")
    },
    {
      title: "部署状态",
      dataIndex: "status",
      width: 120,
      render: (value: string) => <Tag color={statusColor(value)}>{statusLabels[value] ?? "未知状态"}</Tag>
    },
    {
      title: "健康",
      dataIndex: "health_state",
      width: 120,
      render: (value: string) => <Tag color={statusColor(value)}>{statusLabels[value] ?? "未知状态"}</Tag>
    }
  ];

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

  const submitProvider = async () => {
    const values = await providerForm.validateFields();
    setSaving(true);
    try {
      await createModelProvider({
        ...values,
        data_policy: {
          training_allowed: false,
          retention_mode: values.data_policy?.retention_mode ?? "no_store"
        },
        status: "unverified"
      });
      message.success("供应商元数据已登记，仍保持未验收状态");
      setProviderDrawerOpen(false);
      providerForm.resetFields();
      await load();
    } catch (nextError) {
      message.error(errorMessage(nextError));
    } finally {
      setSaving(false);
    }
  };

  const submitDeployment = async () => {
    const values = await deploymentForm.validateFields();
    setSaving(true);
    try {
      await createModelDeployment({
        provider_id: values.provider_id,
        deployment_key: values.deployment_key,
        model_name: values.model_name,
        model_version: values.model_version,
        region: values.region,
        capability_profile: values.capability_profile,
        modalities: values.modalities,
        capability_policy: {},
        pricing_policy: {
          meter: values.meter,
          input_micros: values.input_micros ?? 0,
          output_micros: values.output_micros ?? 0
        },
        status: "unverified",
        health_state: "unverified"
      });
      message.success("部署已登记，需通过原生协议验收后才能进入影子路由");
      setDeploymentDrawerOpen(false);
      deploymentForm.resetFields();
      await load();
    } catch (nextError) {
      message.error(errorMessage(nextError));
    } finally {
      setSaving(false);
    }
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
          <h1>模型治理</h1>
          <p>登记本地与外部部署，控制数据外发范围；未验收模型不会进入评分路由。</p>
        </div>
        <Space wrap>
          <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
        </Space>
      </motion.section>

      {loading && providers.length === 0 ? <LoadingState label="读取模型治理配置" /> : null}
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
            <div>
              <span>本地部署</span>
              <strong>{localDeployments.length}</strong>
              <small>{localDeployments.filter((item) => item.health_state === "available").length} 个状态可用</small>
            </div>
            <div>
              <span>待验收外部部署</span>
              <strong>{pendingExternalDeployments.length}</strong>
              <small>{externalProviders.length} 个外部供应商</small>
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
                  { value: "providers", label: `供应商 ${providers.length}` },
                  { value: "deployments", label: `部署 ${deployments.length}` },
                  { value: "prompts", label: "系统提示词" },
                  { value: "evaluations", label: `评测 ${evaluationRuns.length}` },
                  { value: "assurance", label: "评分保障" },
                  { value: "approvals", label: `批准 ${modelApprovals.filter((item) => !item.revoked_at && new Date(item.expires_at).getTime() > Date.now()).length}` },
                  { value: "policy", label: "租户策略" }
                ]}
              />
              {activeView === "providers" && canManageProviders ? (
                <Button type="primary" icon={<Plus size={16} />} onClick={() => setProviderDrawerOpen(true)}>登记供应商</Button>
              ) : null}
              {activeView === "deployments" && canManageProviders ? (
                <Button type="primary" icon={<Plus size={16} />} disabled={providers.length === 0} onClick={() => setDeploymentDrawerOpen(true)}>登记部署</Button>
              ) : null}
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
                {activeView === "providers" ? (
                  <div className="model-governance-table">
                    <ResponsiveTable className="dense-data-table" rowKey="id" columns={providerColumns} dataSource={providers} pagination={false} />
                  </div>
                ) : null}
                {activeView === "deployments" ? (
                  <div className="model-governance-table">
                    <ResponsiveTable className="dense-data-table" rowKey="id" columns={deploymentColumns} dataSource={deployments} pagination={false} />
                  </div>
                ) : null}
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
        title="登记供应商"
        width={520}
        open={providerDrawerOpen}
        onClose={() => setProviderDrawerOpen(false)}
        extra={<Button type="primary" loading={saving} onClick={() => void submitProvider()}>保存为未验收</Button>}
      >
        <Alert type="info" showIcon message="只登记元数据和 Secret 引用" description="不接收明文 API Key；外部供应商在原生协议验收前不能激活。" />
        <Form
          form={providerForm}
          layout="vertical"
          className="model-governance-form"
          initialValues={{ provider_kind: "external", adapter_type: "dashscope_native", status: "unverified", data_policy: { retention_mode: "no_store" } }}
        >
          <Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input placeholder="例如：厂商 A" /></Form.Item>
          <Form.Item name="provider_key" label="供应商标识" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="vendor-a" /></Form.Item>
          <Form.Item name="provider_kind" label="类型" rules={[{ required: true }]}>
            <Select disabled options={[{ value: "external", label: "外部原生供应商" }]} />
          </Form.Item>
          <Form.Item name="adapter_type" label="Adapter 类型" extra="必须是厂商原生 Adapter，OpenAI-compatible 会被服务端拒绝。" rules={[{ required: true }]}>
            <Select options={[{ value: "dashscope_native", label: "阿里云百炼（DashScope 原生）" }]} />
          </Form.Item>
          <Form.Item
            name="credential_ref"
            label="Secret 引用"
            extra="例如 env://DASHSCOPE_API_KEY、vault://edugrade/dashscope"
            rules={[{ required: true, message: "请输入外部供应商的 Secret 引用" }]}
          >
            <Input prefix={<KeyRound size={15} />} placeholder="vault://edugrade/dashscope" />
          </Form.Item>
          <Form.Item name="region" label="处理区域" rules={[{ required: true }]}><Input placeholder="cn-east" /></Form.Item>
          <Form.Item name={["data_policy", "retention_mode"]} label="数据保留策略" rules={[{ required: true }]}>
            <Select options={[{ value: "no_store", label: "不保留" }, { value: "contractual", label: "按合同约定" }]} />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        title="登记模型部署"
        width={560}
        open={deploymentDrawerOpen}
        onClose={() => setDeploymentDrawerOpen(false)}
        extra={<Button type="primary" loading={saving} onClick={() => void submitDeployment()}>保存为未验收</Button>}
      >
        <Alert type="warning" showIcon message="登记不代表可用于评分" description="部署需经过原生协议联调、冻结集评测和 promotion，才能进入影子或建议路由。" />
        <Form
          form={deploymentForm}
          layout="vertical"
          className="model-governance-form"
          initialValues={{ modalities: ["text"], meter: "token", status: "unverified", health_state: "unverified" }}
        >
          <Form.Item name="provider_id" label="供应商" rules={[{ required: true }]}>
            <Select options={providers.map((item) => ({ value: item.id, label: `${item.display_name} · ${item.provider_key}` }))} />
          </Form.Item>
          <Form.Item name="deployment_key" label="部署标识" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="vendor-a-text-v1" /></Form.Item>
          <Form.Item name="model_name" label="模型名称" rules={[{ required: true }]}><Input placeholder="Vendor A Text" /></Form.Item>
          <Form.Item name="model_version" label="固定版本" rules={[{ required: true }]}><Input placeholder="vendor-a-model-v1" /></Form.Item>
          <Form.Item name="capability_profile" label="能力配置" rules={[{ required: true }]}><Input placeholder="subjective-shadow-v1" /></Form.Item>
          <Form.Item name="region" label="部署区域" rules={[{ required: true }]}><Input placeholder="cn-east" /></Form.Item>
          <Form.Item name="modalities" label="输入模态" rules={[{ required: true }]}>
            <Select mode="multiple" options={[{ value: "text", label: "文本" }, { value: "image", label: "题目裁剪图" }]} />
          </Form.Item>
          <div className="model-governance-form-row">
            <Form.Item name="meter" label="计量方式" rules={[{ required: true }]}><Select options={[{ value: "token", label: "Token" }, { value: "request", label: "请求次数" }]} /></Form.Item>
            <Form.Item name="input_micros" label="输入单价（微元）"><InputNumber min={0} precision={0} /></Form.Item>
            <Form.Item name="output_micros" label="输出单价（微元）"><InputNumber min={0} precision={0} /></Form.Item>
          </div>
        </Form>
      </Drawer>

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
