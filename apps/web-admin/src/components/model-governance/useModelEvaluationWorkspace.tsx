import { Alert, App, Button, Form, Tag, type TableColumnsType } from "antd";
import { Database, Eye, ShieldCheck, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { getUserErrorMessage } from "../../api/client";
import type { ManagedModelAPIConfig } from "../../api/modelApiConfig";
import { addModelEvaluationCandidate, completeModelEvaluationRun, createModelEvaluationRun, EvaluationCandidate, EvaluationEvidenceClass, EvaluationRun, EvaluationRunStatus, invalidateModelEvaluationRun, type AddEvaluationCandidateInput, type CreateEvaluationRunInput, } from "../../api/modelGovernance";
import { capabilityVerified } from "../../features/platform-model-config/lib/modelConfig";
import { costToMicros, formatUSDCost } from "./costUnits";
import { evaluationMaterialMatchesRun, type EvaluationRunMaterial } from "./evaluationReportImport";
import type { MaterialDelivery } from "./materialIntake";


export const statusLabels: Record<EvaluationRunStatus, string> = {
  draft: "采集中",
  completed: "已冻结",
  invalidated: "已失效"
};

export const evidenceLabels: Record<EvaluationEvidenceClass, string> = {
  protocol_fixture: "协议样例",
  authorized_frozen_set: "授权冻结集"
};

export function statusColor(status: EvaluationRunStatus) {
  if (status === "completed") return "success";
  if (status === "invalidated") return "default";
  return "processing";
}

export function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

export function formatAverageCost(value: number) {
  return formatUSDCost(value);
}

export function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "评测操作失败，请检查输入后重试");
}

export function seriousErrorClass(value: number, bestValue?: number) {
  // 即使本批最小错误率仍大于零，也保留相对最优标记，不据此表示没有错误。
  if (value === bestValue) return "model-evaluation-best";
  if (value > 0) return "model-evaluation-risk";
  return "";
}

export function renderRunEvidence(run: EvaluationRun) {
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

export type TransitionKind = "complete" | "invalidate";
export type CandidateFormValues = Omit<AddEvaluationCandidateInput, "total_cost_micros"> & { total_cost_micros: number | string };
// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function useModelEvaluationWorkspace({
  runs,
  tenantID,
  configs,
  loading,
  canManage,
  onRefresh,
  onDirtyChange,
  onSavingChange,
  materialDelivery,
  onMaterialReceived
}: Readonly<{
  runs: EvaluationRun[];
  tenantID: string;
  configs: ManagedModelAPIConfig[];
  loading: boolean;
  canManage: boolean;
  onRefresh: () => Promise<void>;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
  materialDelivery?: MaterialDelivery<EvaluationRunMaterial>;
  onMaterialReceived?: () => void;
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
  const [importNotice, setImportNotice] = useState("");
  const [importedMaterial, setImportedMaterial] = useState<{ tenantID: string; material: EvaluationRunMaterial }>();
  const [candidateImportNotice, setCandidateImportNotice] = useState("");
  const [candidateForm] = Form.useForm<CandidateFormValues>();
  const [transitionForm] = Form.useForm<{ reason: string }>();
  const receivedMaterial = useRef(0);
  useEffect(() => {
    if (!materialDelivery || materialDelivery.sequence === receivedMaterial.current || materialDelivery.tenantID !== tenantID || !canManage) return;
    receivedMaterial.current = materialDelivery.sequence;
    interactionVersion.current += 1;
    createScope.current = tenantID;
    createForm.resetFields(); createForm.setFieldsValue(materialDelivery.material.run);
    setImportedMaterial({ tenantID, material: materialDelivery.material });
    setImportNotice(`已带入 ${materialDelivery.material.candidateCount} 个候选及来源版本，创建后可选择对应候选。`);
    setCreateDrawerOpen(true); onMaterialReceived?.();
  }, [materialDelivery, tenantID, canManage, createForm, onMaterialReceived]);
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
  const importedCandidates = importedMaterial?.tenantID === tenantID && selectedRun && evaluationMaterialMatchesRun(importedMaterial.material, selectedRun)
    ? importedMaterial.material.candidates.filter(candidate => availableConfigs.some(config => config.id === candidate.model_config_id)) : [];
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
    setImportNotice("");
    setImportedMaterial(undefined);
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
    setCandidateImportNotice("");
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
  return {
    runs, tenantID, loading, canManage,
    message, keyword, setKeyword, status,
    setStatus, evidenceClass, setEvidenceClass, selectedRunID,
    createDrawerOpen, setCreateDrawerOpen, candidateDrawerOpen, setCandidateDrawerOpen,
    transitionKind, setTransitionKind, saving, interactionVersion,
    mounted, createScope, candidateScope, createForm,
    importNotice, setImportNotice, setImportedMaterial, candidateImportNotice,
    setCandidateImportNotice, candidateForm, transitionForm, filteredRuns,
    selectedRun, currentContext, selectRun, canMutateRun,
    availableConfigs, importedCandidates, canCompleteSelected, columns,
    candidateColumns, openCreateDrawer, openCandidateDrawer, submitCreate,
    submitCandidate, submitTransition, openTransition, candidateDrawerTitle,
  };
}
