import {
  Alert,
  App,
  Button,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Tag,
  Tooltip,
  type TableColumnsType
} from "antd";
import {
  BarChart3,
  Ban,
  CheckCircle2,
  Database,
  Eye,
  Plus,
  Search,
  ShieldCheck,
  TriangleAlert
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { getUserErrorMessage } from "../../api/client";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { capabilityVerified, modelConfigOptionLabel } from "../../features/platform-model-config/lib/modelConfig";
import {
  addModelEvaluationCandidate,
  completeModelEvaluationRun,
  createModelEvaluationRun,
  invalidateModelEvaluationRun,
  type AddEvaluationCandidateInput,
  type CreateEvaluationRunInput,
  EvaluationCandidate,
  EvaluationEvidenceClass,
  EvaluationRun,
  EvaluationRunStatus,
} from "../../api/modelGovernance";
import { ResponsiveTable } from "../ResponsiveTable";
import { downloadGovernanceMaterial, evaluationReportExample } from "./governanceMaterials";
import { costToMicros, formatUSDCost } from "./costUnits";

const statusLabels: Record<EvaluationRunStatus, string> = {
  draft: "采集中",
  completed: "已冻结",
  invalidated: "已失效"
};

const evidenceLabels: Record<EvaluationEvidenceClass, string> = {
  protocol_fixture: "协议样例",
  authorized_frozen_set: "授权冻结集"
};

function statusColor(status: EvaluationRunStatus) {
  if (status === "completed") return "success";
  if (status === "invalidated") return "default";
  return "processing";
}

function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatAverageCost(value: number) {
  return formatUSDCost(value);
}

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "评测操作失败，请检查输入后重试");
}

function seriousErrorClass(value: number, bestValue?: number) {
  // 即使本批最小错误率仍大于零，也保留相对最优标记，不据此表示没有错误。
  if (value === bestValue) return "model-evaluation-best";
  if (value > 0) return "model-evaluation-risk";
  return "";
}

function renderRunEvidence(run: EvaluationRun) {
  // 失效状态优先于证据类别；历史结果不能继续作为质量判断依据。
  if (run.status === "invalidated") {
    return <Alert
      type="error"
      showIcon
      icon={<TriangleAlert size={17} />}
      message="这组评测证据已失效"
      description={`失效时间：${formatTime(run.invalidated_at)}。历史结果保留用于审计，不应再用于质量判断。`}
    />;
  }
  if (run.evidence_class === "authorized_frozen_set") {
    return <Alert
      type="success"
      showIcon
      icon={<ShieldCheck size={17} />}
      message="授权冻结集证据"
      description={`授权引用：${run.authorization_reference}；SHA-256：${run.dataset_sha256}。这类证据可进入后续质量评审，但不等于模型已批准。`}
    />;
  }
  return <Alert
    type="info"
    showIcon
    message="协议样例证据"
    description={`SHA-256：${run.dataset_sha256}。只验证协议、结构和产品流程，不代表真实教师接受率或生产评分效果。`}
  />;
}

type TransitionKind = "complete" | "invalidate";

export function ModelEvaluationWorkspace({
  runs,
  tenantID,
  configs,
  loading,
  canManage,
  onRefresh,
  onDirtyChange,
  onSavingChange
}: Readonly<{
  runs: EvaluationRun[];
  tenantID: string;
  configs: ManagedModelAPIConfig[];
  loading: boolean;
  canManage: boolean;
  onRefresh: () => Promise<void>;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
}>) {
  const { message } = App.useApp();
  const [keyword, setKeyword] = useState("");
  const [status, setStatus] = useState<EvaluationRunStatus | "all">("all");
  const [evidenceClass, setEvidenceClass] = useState<EvaluationEvidenceClass | "all">("all");
  const [selectedRunID, setSelectedRunID] = useState<string>();
  const [createDrawerOpen, setCreateDrawerOpen] = useState(false);
  const [candidateDrawerOpen, setCandidateDrawerOpen] = useState(false);
  const [transitionKind, setTransitionKind] = useState<TransitionKind>();
  const [saving, setSaving] = useState(false);
  const interactionVersion = useRef(0);
  const mounted = useRef(true);
  const pendingOperation = useRef<{ version: number; tenantID: string } | null>(null);
  const createScope = useRef<string | undefined>(undefined);
  const candidateScope = useRef<{ tenantID: string; runID: string } | undefined>(undefined);
  const transitionScope = useRef<{ tenantID: string; runID: string } | undefined>(undefined);
  const [createForm] = Form.useForm<CreateEvaluationRunInput>();
  const [candidateForm] = Form.useForm<AddEvaluationCandidateInput>();
  const [transitionForm] = Form.useForm<{ reason: string }>();
  // 打开登记草稿后离开学校须确认；工作区切视图时保持挂载，失败输入仍留在原学校。
  useEffect(() => { onDirtyChange?.(createDrawerOpen || candidateDrawerOpen || Boolean(transitionKind)); return () => onDirtyChange?.(false); }, [createDrawerOpen, candidateDrawerOpen, transitionKind, onDirtyChange]);
  useEffect(() => { onSavingChange?.(saving); return () => onSavingChange?.(false); }, [saving, onSavingChange]);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; interactionVersion.current += 1; };
  }, []);

  const filteredRuns = useMemo(() => {
    const normalizedKeyword = keyword.trim().toLocaleLowerCase();
    return runs.filter((run) => {
      const matchesKeyword = !normalizedKeyword || [
        run.display_name,
        run.run_key,
        run.dataset_reference,
        run.subject,
        run.grade,
        run.question_type
      ].some((value) => value.toLocaleLowerCase().includes(normalizedKeyword));
      return matchesKeyword
        && (status === "all" || run.status === status)
        && (evidenceClass === "all" || run.evidence_class === evidenceClass);
    });
  }, [evidenceClass, keyword, runs, status]);

  useEffect(() => {
    if (selectedRunID && runs.some((run) => run.id === selectedRunID)) return;
    setSelectedRunID(runs[0]?.id);
  }, [runs, selectedRunID]);

  const selectedRun = runs.find((run) => run.id === selectedRunID);
  // 弹窗或异步表单校验可能跨过权限、学校或冻结状态更新；提交前只认最新范围，不能沿用打开时的条件。
  const currentContext = useRef({ tenantID, canManage, run: selectedRun });
  if (currentContext.current.tenantID !== tenantID || currentContext.current.canManage !== canManage || currentContext.current.run?.id !== selectedRun?.id) interactionVersion.current += 1;
  currentContext.current = { tenantID, canManage, run: selectedRun };
  // 打开、关闭或切换批次都会淘汰旧弹窗；网络成功只完成原操作，不能关闭后来打开的新草稿。
  const liveScope = (operation: { tenantID: string }) => mounted.current && currentContext.current.canManage && currentContext.current.tenantID === operation.tenantID;
  const currentOperation = (operation: { version: number; tenantID: string }) => liveScope(operation) && interactionVersion.current === operation.version;
  const beginOperation = () => {
    if (pendingOperation.current || !mounted.current) return null;
    const operation = { version: interactionVersion.current, tenantID };
    pendingOperation.current = operation;
    // 表单校验也属于保存等待期；先同步锁住外层切校和重复提交，再等待校验。
    setSaving(true); onSavingChange?.(true);
    return operation;
  };
  const finishOperation = (operation: { version: number; tenantID: string }) => {
    if (pendingOperation.current !== operation) return;
    pendingOperation.current = null;
    if (mounted.current) { setSaving(false); onSavingChange?.(false); }
  };
  const selectRun = (id: string) => { interactionVersion.current += 1; setSelectedRunID(id); };
  const canMutateRun = (runID: string, kind: "candidate" | TransitionKind) => {
    const current = currentContext.current;
    return current.canManage && current.tenantID === tenantID && current.run?.id === runID
      && (kind === "invalidate" ? current.run.status !== "invalidated" : current.run.status === "draft");
  };
  const availableConfigs = selectedRun
    ? configs.filter((config) => config.status === "active"
      && capabilityVerified(config)
      && !selectedRun.candidates.some((candidate) => candidate.model_config_id === config.id))
    : [];
  const canCompleteSelected = Boolean(
    selectedRun
    && selectedRun.candidates.length >= 2
    && (selectedRun.candidates.some((candidate) => candidate.provider_key === "local")
      || selectedRun.candidates.filter((candidate) => candidate.model_config_id).length >= 2)
  );
  // 每项指标分别取同批次的最好值用于高亮；这些值可能来自不同候选，不能据此宣称某个模型整体最好。
  const bestMetrics = useMemo(() => {
    if (!selectedRun?.candidates.length) return undefined;
    const metrics = selectedRun.candidates.map((candidate) => candidate.metrics);
    return {
      teacherAcceptance: Math.max(...metrics.map((item) => item.teacher_acceptance_rate)),
      seriousError: Math.min(...metrics.map((item) => item.serious_error_rate)),
      evidenceValidity: Math.max(...metrics.map((item) => item.evidence_validity_rate)),
      stability: Math.max(...metrics.map((item) => item.stability_rate)),
      latency: Math.min(...selectedRun.candidates.map((item) => item.p95_latency_ms)),
      cost: Math.min(...metrics.map((item) => item.average_cost_micros))
    };
  }, [selectedRun]);

  const columns: TableColumnsType<EvaluationRun> = [
    {
      title: "评测批次",
      dataIndex: "display_name",
      render: (_value, run) => (
        <div className="model-evaluation-run">
          <span><Database size={15} /></span>
          <div>
            <strong>{run.display_name}</strong>
            <small>{run.run_key}</small>
          </div>
        </div>
      )
    },
    {
      title: "适用范围",
      key: "scope",
      render: (_value, run) => (
        <div className="model-governance-stack">
          <span>{run.subject} · {run.grade}</span>
          <small>{run.question_type} · {run.modality === "image" ? "图片" : "文本"}</small>
        </div>
      )
    },
    {
      title: "证据",
      dataIndex: "evidence_class",
      render: (value: EvaluationEvidenceClass, run) => (
        <div className="model-governance-stack">
          <span>{evidenceLabels[value]}</span>
          <small>{run.dataset_reference}</small>
        </div>
      )
    },
    {
      title: "样本 / 重复",
      key: "samples",
      width: 130,
      render: (_value, run) => `${run.sample_count} / ${run.repeat_count}`
    },
    {
      title: "候选",
      dataIndex: "candidates",
      width: 88,
      render: (candidates: EvaluationRun["candidates"]) => candidates.length
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 105,
      render: (value: EvaluationRunStatus) => <Tag color={statusColor(value)}>{statusLabels[value]}</Tag>
    },
    {
      title: "创建时间",
      dataIndex: "created_at",
      width: 178,
      render: (value: string) => formatTime(value)
    },
    {
      title: "操作",
      key: "action",
      width: 90,
      render: (_value, run) => (
        <Button
          type={selectedRunID === run.id ? "primary" : "link"}
          size="small"
          icon={<Eye size={14} />}
          onClick={(event) => {
            event.stopPropagation();
            selectRun(run.id);
          }}
        >
          对比
        </Button>
      )
    }
  ];

  const candidateColumns: TableColumnsType<EvaluationCandidate> = [
    {
      title: "评测模型",
      dataIndex: "model_name",
      render: (_value, candidate) => (
        <div className="model-governance-stack">
          <span>{candidate.model_name || "历史模型配置（已停用）"}</span>
          <small>{candidate.provider_key} · {candidate.model_version}</small>
        </div>
      )
    },
    {
      title: "提示词 / Rubric",
      key: "versions",
      render: (_value, candidate) => (
        <div className="model-governance-stack">
          <span>{candidate.prompt_version}</span>
          <small>{candidate.rubric_version}</small>
        </div>
      )
    },
    {
      title: "教师接受率",
      dataIndex: ["metrics", "teacher_acceptance_rate"],
      width: 120,
      render: (value: number) => selectedRun?.evidence_class === "protocol_fixture"
        ? <span className="muted">不适用</span>
        : <strong className={value === bestMetrics?.teacherAcceptance ? "model-evaluation-best" : ""}>{formatPercent(value)}</strong>
    },
    {
      title: "严重错误率",
      dataIndex: ["metrics", "serious_error_rate"],
      width: 118,
      render: (value: number) => (
        <strong className={seriousErrorClass(value, bestMetrics?.seriousError)}>
          {formatPercent(value)}
        </strong>
      )
    },
    {
      title: "证据有效率",
      dataIndex: ["metrics", "evidence_validity_rate"],
      width: 118,
      render: (value: number) => <strong className={value === bestMetrics?.evidenceValidity ? "model-evaluation-best" : ""}>{formatPercent(value)}</strong>
    },
    {
      title: "稳定性",
      dataIndex: ["metrics", "stability_rate"],
      width: 105,
      render: (value: number, candidate) => candidate.repeat_comparisons === 0
        ? <span className="muted">未重复</span>
        : <strong className={value === bestMetrics?.stability ? "model-evaluation-best" : ""}>{formatPercent(value)}</strong>
    },
    {
      title: "P95 时延",
      dataIndex: "p95_latency_ms",
      width: 105,
      render: (value: number) => <span className={value === bestMetrics?.latency ? "model-evaluation-best" : ""}>{value} ms</span>
    },
    {
      title: "平均成本",
      dataIndex: ["metrics", "average_cost_micros"],
      width: 116,
      render: (value: number) => <span className={value === bestMetrics?.cost ? "model-evaluation-best" : ""}>{formatAverageCost(value)}</span>
    }
  ];

  const openCreateDrawer = () => {
    if (!canManage) return;
    interactionVersion.current += 1;
    createScope.current = tenantID;
    createForm.resetFields();
    createForm.setFieldsValue({
      evidence_class: "protocol_fixture",
      modality: "text",
      sample_count: 10,
      repeat_count: 2
    } as Partial<CreateEvaluationRunInput>);
    setCreateDrawerOpen(true);
  };

  const openCandidateDrawer = () => {
    if (!selectedRun || !canMutateRun(selectedRun.id, "candidate")) return;
    interactionVersion.current += 1;
    candidateScope.current = { tenantID, runID: selectedRun.id };
    const repeatComparisons = selectedRun.sample_count * Math.max(0, selectedRun.repeat_count - 1);
    candidateForm.resetFields();
    candidateForm.setFieldsValue({
      evaluated_samples: selectedRun.sample_count,
      teacher_reviewed_samples: selectedRun.evidence_class === "authorized_frozen_set" ? selectedRun.sample_count : 0,
      teacher_accepted_samples: 0,
      serious_error_samples: 0,
      evidence_valid_samples: selectedRun.sample_count,
      repeat_comparisons: repeatComparisons,
      stable_repeat_samples: repeatComparisons,
      p95_latency_ms: 0,
      total_cost_micros: 0
    } as Partial<AddEvaluationCandidateInput>);
    setCandidateDrawerOpen(true);
  };

  const submitCreate = async () => {
    if (!currentContext.current.canManage || createScope.current !== tenantID) return;
    const operation = beginOperation();
    if (!operation) return;
    let submitted = false;
    try {
      const values = await createForm.validateFields();
      if (!currentOperation(operation) || createScope.current !== tenantID) return;
      submitted = true;
      const response = await createModelEvaluationRun({
        ...values,
        tenant_id: tenantID,
        authorization_reference: values.evidence_class === "authorized_frozen_set"
          ? values.authorization_reference
          : undefined
      });
      if (!currentOperation(operation)) { if (liveScope(operation)) await onRefresh(); return; }
      setCreateDrawerOpen(false);
      setSelectedRunID(response.evaluation_run.id);
      message.success("评测批次已创建，可开始录入候选结果");
      await onRefresh();
    } catch (error) {
      if (submitted && currentOperation(operation)) message.error(errorMessage(error));
    } finally {
      finishOperation(operation);
    }
  };

  const submitCandidate = async () => {
    if (!selectedRun || !canMutateRun(selectedRun.id, "candidate") || candidateScope.current?.tenantID !== tenantID || candidateScope.current.runID !== selectedRun.id) return;
    const operation = beginOperation();
    if (!operation) return;
    let submitted = false;
    try {
      const values = await candidateForm.validateFields();
      if (!currentOperation(operation) || !canMutateRun(selectedRun.id, "candidate")) return;
      submitted = true;
      await addModelEvaluationCandidate(tenantID, selectedRun.id, {
        ...values,
        total_cost_micros: costToMicros(values.total_cost_micros),
        evaluated_samples: selectedRun.sample_count,
        teacher_reviewed_samples: selectedRun.evidence_class === "authorized_frozen_set"
          ? selectedRun.sample_count
          : 0,
        teacher_accepted_samples: selectedRun.evidence_class === "protocol_fixture"
          ? 0
          : values.teacher_accepted_samples
      });
      if (!currentOperation(operation)) { if (liveScope(operation)) await onRefresh(); return; }
      setCandidateDrawerOpen(false);
      message.success("候选评测证据已加入批次");
      await onRefresh();
    } catch (error) {
      if (submitted && currentOperation(operation)) message.error(errorMessage(error));
    } finally {
      finishOperation(operation);
    }
  };

  const submitTransition = async () => {
    if (!selectedRun || !transitionKind || !canMutateRun(selectedRun.id, transitionKind) || transitionScope.current?.tenantID !== tenantID || transitionScope.current.runID !== selectedRun.id) return;
    const operation = beginOperation();
    if (!operation) return;
    let submitted = false;
    try {
      const { reason } = await transitionForm.validateFields();
      if (!currentOperation(operation) || !canMutateRun(selectedRun.id, transitionKind)) return;
      submitted = true;
      if (transitionKind === "complete") {
        await completeModelEvaluationRun(tenantID, selectedRun.id, reason);
      } else {
        await invalidateModelEvaluationRun(tenantID, selectedRun.id, reason);
      }
      if (!currentOperation(operation)) { if (liveScope(operation)) await onRefresh(); return; }
      message.success(transitionKind === "complete" ? "评测批次已冻结，候选证据不再接受修改" : "评测证据已标记为失效");
      setTransitionKind(undefined);
      transitionForm.resetFields();
      await onRefresh();
    } catch (error) {
      if (submitted && currentOperation(operation)) message.error(errorMessage(error));
    } finally {
      finishOperation(operation);
    }
  };

  const openTransition = (kind: TransitionKind) => {
    if (!selectedRun || !canMutateRun(selectedRun.id, kind)) return;
    interactionVersion.current += 1;
    transitionScope.current = { tenantID, runID: selectedRun.id };
    transitionForm.resetFields();
    setTransitionKind(kind);
  };

  const candidateDrawerTitle = selectedRun ? `添加候选结果 · ${selectedRun.display_name}` : "添加候选结果";
  const renderSelectedRun = (run: EvaluationRun) => {
    // 这里只复用已有展示条件；冻结后不再显示录入和再次冻结入口。
    const canEditRun = canManage && run.status === "draft";
    return (
      <section className="model-evaluation-detail" aria-label="评测候选对比">
        <header>
          <div>
            <span className="model-evaluation-detail-kicker"><BarChart3 size={15} /> 候选并排对比</span>
            <h2>{run.display_name}</h2>
            <p>
              {run.subject} · {run.grade} · {run.question_type}
              <span>数据集 {run.dataset_reference}</span>
            </p>
          </div>
          <Space wrap>
            <Tag color={statusColor(run.status)}>{statusLabels[run.status]}</Tag>
            {canEditRun ? (
              <Button icon={<Plus size={15} />} disabled={availableConfigs.length === 0} onClick={openCandidateDrawer}>
                添加候选
              </Button>
            ) : null}
            {canEditRun ? (
              <Tooltip title={canCompleteSelected ? undefined : "至少需要两个候选；新评测请选择两个学校模型"}>
                <span>
                  <Button type="primary" icon={<CheckCircle2 size={15} />} disabled={!canCompleteSelected} onClick={() => openTransition("complete")}>
                    冻结完成
                  </Button>
                </span>
              </Tooltip>
            ) : null}
            {canManage && run.status !== "invalidated" ? (
              <Button danger icon={<Ban size={15} />} onClick={() => openTransition("invalidate")}>
                标记失效
              </Button>
            ) : null}
          </Space>
        </header>
        <div className="model-evaluation-evidence">{renderRunEvidence(run)}</div>
        <div className="model-evaluation-candidate-table">
          <ResponsiveTable
            rowKey="id"
            columns={candidateColumns}
            dataSource={run.candidates}
            pagination={false}
            mobilePrimaryCount={4}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该批次还没有候选结果" /> }}
          />
        </div>
        <footer>
          <span><i className="model-evaluation-legend-best" /> 同批次内的相对最优值</span>
          <small>不同数据集、题型或 Rubric 版本之间不可直接横向比较。</small>
        </footer>
      </section>
    );
  };

  return (
    <div className="model-evaluation-workspace">
      <div className="model-evaluation-filterbar">
        <Input
          allowClear
          prefix={<Search size={15} />}
          placeholder="搜索批次、数据集或适用范围"
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />
        <Space wrap>
          <Select
            aria-label="评测状态"
            value={status}
            onChange={setStatus}
            options={[
              { value: "all", label: "全部状态" },
              { value: "draft", label: statusLabels.draft },
              { value: "completed", label: statusLabels.completed },
              { value: "invalidated", label: statusLabels.invalidated }
            ]}
          />
          <Select
            aria-label="证据类别"
            value={evidenceClass}
            onChange={setEvidenceClass}
            options={[
              { value: "all", label: "全部证据" },
              { value: "protocol_fixture", label: evidenceLabels.protocol_fixture },
              { value: "authorized_frozen_set", label: evidenceLabels.authorized_frozen_set }
            ]}
          />
          {canManage ? (
            <Button type="primary" icon={<Plus size={15} />} onClick={openCreateDrawer}>
              登记已有评测报告
            </Button>
          ) : null}
          <Button onClick={() => downloadGovernanceMaterial("候选模型评测报告示例.json", evaluationReportExample)}>下载报告材料模板</Button>
        </Space>
      </div>
      <div className="model-evaluation-count">
        <span>显示 {filteredRuns.length} / {runs.length} 个批次</span>
        <small>这里只记录离线对比证据，不改变当前评分路由。</small>
      </div>
      <div className="model-governance-table">
        <ResponsiveTable
          className="dense-data-table"
          rowKey="id"
          columns={columns}
          dataSource={filteredRuns}
          loading={loading}
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无符合条件的评测批次" /> }}
          onRow={(run) => ({ onClick: () => selectRun(run.id) })}
          rowClassName={(run) => run.id === selectedRunID ? "model-evaluation-selected-row" : ""}
        />
      </div>
      {selectedRun ? renderSelectedRun(selectedRun) : null}

      <Drawer
        title="登记已有离线评测报告"
        width={620}
        open={createDrawerOpen}
        onClose={() => { interactionVersion.current += 1; setCreateDrawerOpen(false); }}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCreate()}>创建批次</Button>}
      >
        <Alert
          type="info"
          showIcon
          message="登记已有报告，不运行模型"
          description="由评测负责人先完成授权数据的离线评分和人工核验，再登记数据集引用、版本与候选汇总。登记后仍须冻结和人工批准；不会调用外部模型或改变评分路由。"
        />
        <Form name="model-evaluation-create" form={createForm} layout="vertical" className="model-evaluation-form">
          <div className="model-governance-form-row two">
            <Form.Item
              name="run_key"
              label="批次标识"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/, message: "仅支持小写字母、数字、点、下划线和连字符" }]}
            >
              <Input placeholder="math-grade9-fixture-v1" />
            </Form.Item>
            <Form.Item name="display_name" label="批次名称" rules={[{ required: true, max: 128 }]}>
              <Input placeholder="九年级数学协议样例对比" />
            </Form.Item>
          </div>
          <div className="model-governance-form-row two">
            <Form.Item
              name="dataset_reference"
              label="数据集引用"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
            >
              <Input placeholder="math-grade9-fixture-v1" />
            </Form.Item>
            <Form.Item
              name="dataset_sha256"
              label="数据集 SHA-256"
              rules={[{ required: true }, { pattern: /^[a-f0-9]{64}$/, message: "请输入 64 位小写 SHA-256" }]}
            >
              <Input placeholder="64 位小写哈希" />
            </Form.Item>
          </div>
          <Form.Item name="evidence_class" label="证据类别" rules={[{ required: true }]}>
            <Select
              options={[
                { value: "protocol_fixture", label: "协议样例（不代表模型效果）" },
                { value: "authorized_frozen_set", label: "授权冻结集（含教师真值）" }
              ]}
            />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(before, after) => before.evidence_class !== after.evidence_class}>
            {({ getFieldValue }) => getFieldValue("evidence_class") === "authorized_frozen_set" ? (
              <Form.Item
                name="authorization_reference"
                label="授权引用"
                rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
              >
                <Input placeholder="frozen-set-approval-2026-01" />
              </Form.Item>
            ) : null}
          </Form.Item>
          <div className="model-governance-form-row">
            <Form.Item name="subject" label="学科" rules={[{ required: true, max: 128 }]}><Input placeholder="数学" /></Form.Item>
            <Form.Item name="grade" label="年级" rules={[{ required: true, max: 128 }]}><Input placeholder="九年级" /></Form.Item>
            <Form.Item
              name="question_type"
              label="题型标识"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
            >
              <Input placeholder="short_answer" />
            </Form.Item>
          </div>
          <div className="model-governance-form-row">
            <Form.Item name="modality" label="输入模态" rules={[{ required: true }]}>
              <Select options={[{ value: "text", label: "文本" }, { value: "image", label: "图片" }]} />
            </Form.Item>
            <Form.Item name="sample_count" label="样本数" rules={[{ required: true }]}>
              <InputNumber min={1} max={100000} precision={0} />
            </Form.Item>
            <Form.Item name="repeat_count" label="重复评分次数" rules={[{ required: true }]}>
              <InputNumber min={1} max={20} precision={0} />
            </Form.Item>
          </div>
          <Form.Item name="reason" label="创建原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="说明评测目的、证据来源和复核安排" />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        title={candidateDrawerTitle}
        width={620}
        open={candidateDrawerOpen}
        onClose={() => { interactionVersion.current += 1; setCandidateDrawerOpen(false); }}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCandidate()}>保存候选</Button>}
      >
        {selectedRun ? (
          <>
            <Alert
              type={selectedRun.evidence_class === "protocol_fixture" ? "info" : "success"}
              showIcon
              message={selectedRun.evidence_class === "protocol_fixture" ? "协议样例不录入教师接受数据" : "教师复核数固定为全部样本"}
              description={`批次固定 ${selectedRun.sample_count} 个样本、重复 ${selectedRun.repeat_count} 次；候选版本写入后不可修改。`}
            />
            {/* 抽屉关闭后仍保留表单；独立名称避免与批准表单的同名字段争用 DOM id 和标签关联。 */}
            <Form name="model-evaluation-candidate" form={candidateForm} layout="vertical" className="model-evaluation-form">
              <Form.Item name="model_config_id" label="评测模型" rules={[{ required: true }]}>
                <Select
                  options={availableConfigs.map((config) => ({
                    value: config.id,
                    label: modelConfigOptionLabel(config)
                  }))}
                />
              </Form.Item>
              <div className="model-governance-form-row two">
                <Form.Item name="prompt_version" label="提示词版本" rules={[{ required: true, max: 128 }]}><Input placeholder="prompt-v1" /></Form.Item>
                <Form.Item name="rubric_version" label="Rubric 版本" rules={[{ required: true, max: 128 }]}><Input placeholder="rubric-v1" /></Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="evaluated_samples" label="已评样本"><InputNumber disabled /></Form.Item>
                <Form.Item name="teacher_reviewed_samples" label="教师复核数"><InputNumber disabled /></Form.Item>
                <Form.Item
                  name="teacher_accepted_samples"
                  label="教师直接接受数"
                  rules={[{ required: true }]}
                >
                  <InputNumber
                    disabled={selectedRun.evidence_class === "protocol_fixture"}
                    min={0}
                    max={selectedRun.sample_count}
                    precision={0}
                  />
                </Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="serious_error_samples" label="严重错误样本" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count} precision={0} />
                </Form.Item>
                <Form.Item name="evidence_valid_samples" label="证据有效样本" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count} precision={0} />
                </Form.Item>
                <Form.Item name="p95_latency_ms" label="P95 时延（ms）" rules={[{ required: true }]}>
                  <InputNumber min={0} precision={0} />
                </Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="repeat_comparisons" label="重复对比数" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count * Math.max(0, selectedRun.repeat_count - 1)} precision={0} />
                </Form.Item>
                <Form.Item name="stable_repeat_samples" label="稳定重复数" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count * Math.max(0, selectedRun.repeat_count - 1)} precision={0} />
                </Form.Item>
                <Form.Item name="total_cost_micros" label="总预估成本（美元）" extra="按微美元口径登记已有报告成本，不是供应商账单。" rules={[{ required: true }]}>
                  <InputNumber stringMode min="0" precision={6} />
                </Form.Item>
              </div>
              <Form.Item name="reason" label="录入原因" rules={[{ required: true, min: 4, max: 256 }]}>
                <Input.TextArea rows={3} placeholder="说明离线运行来源和数据核验情况" />
              </Form.Item>
            </Form>
          </>
        ) : null}
      </Drawer>

      <Modal
        title={transitionKind === "complete" ? "冻结完成评测" : "标记评测证据失效"}
        open={Boolean(transitionKind)}
        okText={transitionKind === "complete" ? "确认冻结" : "确认失效"}
        okButtonProps={{ danger: transitionKind === "invalidate" }}
        confirmLoading={saving}
        onOk={() => void submitTransition()}
        onCancel={() => { interactionVersion.current += 1; setTransitionKind(undefined); }}
      >
        <Alert
          type={transitionKind === "complete" ? "warning" : "error"}
          showIcon
          message={transitionKind === "complete" ? "冻结后不能再添加候选" : "失效后只能保留为历史审计证据"}
          description={transitionKind === "complete"
            ? "本操作仍不会批准模型或改变评分路由。"
            : "若数据授权撤回、版本不一致或证据被替代，应立即标记失效。"}
        />
        <Form name="model-evaluation-transition" form={transitionForm} layout="vertical" className="model-evaluation-transition-form">
          <Form.Item name="reason" label="操作原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="填写可审计的操作原因" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
