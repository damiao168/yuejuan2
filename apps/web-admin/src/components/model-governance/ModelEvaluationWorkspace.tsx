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
import { useEffect, useMemo, useState } from "react";
import { getUserErrorMessage } from "../../api/client";
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
  ModelDeployment
} from "../../api/modelGovernance";
import { ResponsiveTable } from "../ResponsiveTable";

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
  return value === 0 ? "¥0" : `¥${(value / 1_000_000).toFixed(6)}`;
}

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "评测操作失败，请检查输入后重试");
}

type TransitionKind = "complete" | "invalidate";

export function ModelEvaluationWorkspace({
  runs,
  deployments,
  loading,
  canManage,
  onRefresh
}: {
  runs: EvaluationRun[];
  deployments: ModelDeployment[];
  loading: boolean;
  canManage: boolean;
  onRefresh: () => Promise<void>;
}) {
  const { message } = App.useApp();
  const [keyword, setKeyword] = useState("");
  const [status, setStatus] = useState<EvaluationRunStatus | "all">("all");
  const [evidenceClass, setEvidenceClass] = useState<EvaluationEvidenceClass | "all">("all");
  const [selectedRunID, setSelectedRunID] = useState<string>();
  const [createDrawerOpen, setCreateDrawerOpen] = useState(false);
  const [candidateDrawerOpen, setCandidateDrawerOpen] = useState(false);
  const [transitionKind, setTransitionKind] = useState<TransitionKind>();
  const [saving, setSaving] = useState(false);
  const [createForm] = Form.useForm<CreateEvaluationRunInput>();
  const [candidateForm] = Form.useForm<AddEvaluationCandidateInput>();
  const [transitionForm] = Form.useForm<{ reason: string }>();

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
  const availableDeployments = selectedRun
    ? deployments.filter((deployment) => !selectedRun.candidates.some((candidate) => candidate.deployment_id === deployment.id))
    : [];
  const canCompleteSelected = Boolean(
    selectedRun
    && selectedRun.candidates.length >= 2
    && selectedRun.candidates.some((candidate) => candidate.provider_key === "local")
  );

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
            setSelectedRunID(run.id);
          }}
        >
          对比
        </Button>
      )
    }
  ];

  const candidateColumns: TableColumnsType<EvaluationCandidate> = [
    {
      title: "候选部署",
      dataIndex: "deployment_key",
      render: (_value, candidate) => (
        <div className="model-governance-stack">
          <span>{candidate.deployment_key}</span>
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
        <strong className={value === bestMetrics?.seriousError ? "model-evaluation-best" : value > 0 ? "model-evaluation-risk" : ""}>
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
    if (!selectedRun) return;
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
    const values = await createForm.validateFields();
    setSaving(true);
    try {
      const response = await createModelEvaluationRun({
        ...values,
        authorization_reference: values.evidence_class === "authorized_frozen_set"
          ? values.authorization_reference
          : undefined
      });
      setCreateDrawerOpen(false);
      setSelectedRunID(response.evaluation_run.id);
      message.success("评测批次已创建，可开始录入候选结果");
      await onRefresh();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const submitCandidate = async () => {
    if (!selectedRun) return;
    const values = await candidateForm.validateFields();
    setSaving(true);
    try {
      await addModelEvaluationCandidate(selectedRun.id, {
        ...values,
        evaluated_samples: selectedRun.sample_count,
        teacher_reviewed_samples: selectedRun.evidence_class === "authorized_frozen_set"
          ? selectedRun.sample_count
          : 0,
        teacher_accepted_samples: selectedRun.evidence_class === "protocol_fixture"
          ? 0
          : values.teacher_accepted_samples
      });
      setCandidateDrawerOpen(false);
      message.success("候选评测证据已加入批次");
      await onRefresh();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const submitTransition = async () => {
    if (!selectedRun || !transitionKind) return;
    const { reason } = await transitionForm.validateFields();
    setSaving(true);
    try {
      if (transitionKind === "complete") {
        await completeModelEvaluationRun(selectedRun.id, reason);
        message.success("评测批次已冻结，候选证据不再接受修改");
      } else {
        await invalidateModelEvaluationRun(selectedRun.id, reason);
        message.success("评测证据已标记为失效");
      }
      setTransitionKind(undefined);
      transitionForm.resetFields();
      await onRefresh();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const openTransition = (kind: TransitionKind) => {
    transitionForm.resetFields();
    setTransitionKind(kind);
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
              新建评测
            </Button>
          ) : null}
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
          onRow={(run) => ({ onClick: () => setSelectedRunID(run.id) })}
          rowClassName={(run) => run.id === selectedRunID ? "model-evaluation-selected-row" : ""}
        />
      </div>
      {selectedRun ? (
        <section className="model-evaluation-detail" aria-label="评测候选对比">
          <header>
            <div>
              <span className="model-evaluation-detail-kicker"><BarChart3 size={15} /> 候选并排对比</span>
              <h2>{selectedRun.display_name}</h2>
              <p>
                {selectedRun.subject} · {selectedRun.grade} · {selectedRun.question_type}
                <span>数据集 {selectedRun.dataset_reference}</span>
              </p>
            </div>
            <Space wrap>
              <Tag color={statusColor(selectedRun.status)}>{statusLabels[selectedRun.status]}</Tag>
              {canManage && selectedRun.status === "draft" ? (
                <Button icon={<Plus size={15} />} disabled={availableDeployments.length === 0} onClick={openCandidateDrawer}>
                  添加候选
                </Button>
              ) : null}
              {canManage && selectedRun.status === "draft" ? (
                <Tooltip
                  title={canCompleteSelected ? undefined : "至少需要两个候选，并且必须包含本地基线"}
                >
                  <span>
                    <Button
                      type="primary"
                      icon={<CheckCircle2 size={15} />}
                      disabled={!canCompleteSelected}
                      onClick={() => openTransition("complete")}
                    >
                      冻结完成
                    </Button>
                  </span>
                </Tooltip>
              ) : null}
              {canManage && selectedRun.status !== "invalidated" ? (
                <Button danger icon={<Ban size={15} />} onClick={() => openTransition("invalidate")}>
                  标记失效
                </Button>
              ) : null}
            </Space>
          </header>

          <div className="model-evaluation-evidence">
            {selectedRun.status === "invalidated" ? (
              <Alert
                type="error"
                showIcon
                icon={<TriangleAlert size={17} />}
                message="这组评测证据已失效"
                description={`失效时间：${formatTime(selectedRun.invalidated_at)}。历史结果保留用于审计，不应再用于质量判断。`}
              />
            ) : selectedRun.evidence_class === "authorized_frozen_set" ? (
              <Alert
                type="success"
                showIcon
                icon={<ShieldCheck size={17} />}
                message="授权冻结集证据"
                description={`授权引用：${selectedRun.authorization_reference}；SHA-256：${selectedRun.dataset_sha256}。这类证据可进入后续质量评审，但不等于模型已批准。`}
              />
            ) : (
              <Alert
                type="info"
                showIcon
                message="协议样例证据"
                description={`SHA-256：${selectedRun.dataset_sha256}。只验证协议、结构和产品流程，不代表真实教师接受率或生产评分效果。`}
              />
            )}
          </div>

          <div className="model-evaluation-candidate-table">
            <ResponsiveTable
              rowKey="id"
              columns={candidateColumns}
              dataSource={selectedRun.candidates}
              pagination={false}
              mobilePrimaryCount={4}
              locale={{
                emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该批次还没有候选结果" />
              }}
            />
          </div>
          <footer>
            <span><i className="model-evaluation-legend-best" /> 同批次内的相对最优值</span>
            <small>不同数据集、题型或 Rubric 版本之间不可直接横向比较。</small>
          </footer>
        </section>
      ) : null}

      <Drawer
        title="新建离线评测批次"
        width={620}
        open={createDrawerOpen}
        onClose={() => setCreateDrawerOpen(false)}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCreate()}>创建批次</Button>}
      >
        <Alert
          type="info"
          showIcon
          message="评测记录不等于模型批准"
          description="这里只登记离线运行证据；不会调用外部模型、启用部署或改变评分路由。"
        />
        <Form form={createForm} layout="vertical" className="model-evaluation-form">
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
        title={`添加候选结果${selectedRun ? ` · ${selectedRun.display_name}` : ""}`}
        width={620}
        open={candidateDrawerOpen}
        onClose={() => setCandidateDrawerOpen(false)}
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
            <Form form={candidateForm} layout="vertical" className="model-evaluation-form">
              <Form.Item name="deployment_id" label="候选部署" rules={[{ required: true }]}>
                <Select
                  options={availableDeployments.map((deployment) => ({
                    value: deployment.id,
                    label: `${deployment.model_name} · ${deployment.deployment_key}`
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
                <Form.Item name="total_cost_micros" label="总成本（微元）" rules={[{ required: true }]}>
                  <InputNumber min={0} precision={0} />
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
        onCancel={() => setTransitionKind(undefined)}
      >
        <Alert
          type={transitionKind === "complete" ? "warning" : "error"}
          showIcon
          message={transitionKind === "complete" ? "冻结后不能再添加候选" : "失效后只能保留为历史审计证据"}
          description={transitionKind === "complete"
            ? "本操作仍不会批准模型或改变评分路由。"
            : "若数据授权撤回、版本不一致或证据被替代，应立即标记失效。"}
        />
        <Form form={transitionForm} layout="vertical" className="model-evaluation-transition-form">
          <Form.Item name="reason" label="操作原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="填写可审计的操作原因" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
