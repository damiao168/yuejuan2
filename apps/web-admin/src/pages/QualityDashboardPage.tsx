import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, App, Button, Collapse, DatePicker, Descriptions, Drawer, Empty, Form, Input, InputNumber, Select, Space, Statistic, Switch, Table, Tag } from "antd";
import { RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import type { Dayjs } from "dayjs";
import type { QualityDashboard } from "../api/qualityDashboard";
import { getExamQualityDashboard } from "../api/qualityDashboard";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getSeedPolicy, listBackmarkBatches, putSeedPolicy, type BackmarkBatch, type SeedPolicy } from "../api/qualityOperations";
import { createBackmarkBatch, createBackmarkBatchRegradeJob, getBackmarkBatch, previewBackmarkBatch, previewBackmarkBatchRegrade, type BackmarkPolicy, type BackmarkPreview, type BackmarkSelector, type BackmarkSummary } from "../api/backmark";
import { listScoreReleases, type RegradePreview, type ScoreRelease } from "../api/scoreReleases";
import { listAllActiveGraders, type ManagedUser } from "../api/users";
import { listGradingQualityIncidents, recomputeGraderDrift, resolveGradingQualityIncident, type GradingQualityIncident } from "../api/graderDrift";
import { ErrorState, LoadingState } from "../components/PageState";

type Gate = QualityDashboard["gate"];

function gateLabel(gate: Gate) {
  return gate === "blocked" ? "不可发布" : gate === "ready" ? "可继续" : "需要关注";
}

function gateColor(gate: Gate) {
  return gate === "blocked" ? "error" : gate === "ready" ? "success" : "warning";
}

function percentage(value?: number) {
  return value === undefined ? "样本不足" : `${Math.round(value * 100)}%`;
}

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "质量看板暂时无法加载，请稍后重试");
}

export function QualityDashboardPage({ examId, canManage = false }: { examId: string; canManage?: boolean }) {
  const { message } = App.useApp();
  const [data, setData] = useState<QualityDashboard>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [seedOpen, setSeedOpen] = useState(false);
  const [backmarkOpen, setBackmarkOpen] = useState(false);
  const [seedQuestionId, setSeedQuestionId] = useState("");
  const [seedPolicy, setSeedPolicy] = useState<SeedPolicy>();
  const [seedLoading, setSeedLoading] = useState(false);
  const [backmarkBatches, setBackmarkBatches] = useState<BackmarkBatch[]>([]);
  const [backmarkBatchesNextCursor, setBackmarkBatchesNextCursor] = useState("");
  const [backmarkLoading, setBackmarkLoading] = useState(false);
  const [backmarkQuestionId, setBackmarkQuestionId] = useState("");
  const [backmarkPreview, setBackmarkPreview] = useState<BackmarkPreview>();
  const backmarkPreviewRevision = useRef(0);
  const [backmarkReviewers, setBackmarkReviewers] = useState<ManagedUser[]>([]);
  const [backmarkSummary, setBackmarkSummary] = useState<BackmarkSummary>();
  const [backmarkSummaryNextCursor, setBackmarkSummaryNextCursor] = useState("");
  const [backmarkSummaryOpen, setBackmarkSummaryOpen] = useState(false);
  const [backmarkRegradePreview, setBackmarkRegradePreview] = useState<RegradePreview>();
  const [backmarkRegradeAssignee, setBackmarkRegradeAssignee] = useState("");
  const [backmarkRegradeReleaseID, setBackmarkRegradeReleaseID] = useState("");
  const [backmarkRegradeReleases, setBackmarkRegradeReleases] = useState<ScoreRelease[]>([]);
  const [backmarkRegradeLoading, setBackmarkRegradeLoading] = useState(false);
  const [incidentsOpen, setIncidentsOpen] = useState(false);
  const [incidents, setIncidents] = useState<GradingQualityIncident[]>([]);
  const [incidentsLoading, setIncidentsLoading] = useState(false);
  const [backmarkForm] = Form.useForm<{
    question_id: string; source_incident_id: string; reassigned_to: string; disposition: BackmarkPolicy["disposition"]; arbitration_delta: number;
    date_range?: [Dayjs, Dayjs]; score_min?: number; score_max?: number; grader_id?: string;
  }>();
  const [policyForm] = Form.useForm<{ rate: number; min_interval: number; max_interval: number; enabled: boolean }>();

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!examId) return;
    setLoading(true);
    setError(undefined);
    try {
      const response = await getExamQualityDashboard(examId, signal);
      setData(response.dashboard);
    } catch (reason) {
      if (!signal?.aborted) setError(errorMessage(reason));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, [examId]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  if (loading && !data) return <LoadingState label="正在加载评分质量" />;
  if (error && !data) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!data) return <Empty description="暂无质量数据" />;

  const loadSeedPolicy = async (questionId: string) => {
    setSeedQuestionId(questionId);
    setSeedLoading(true);
    try {
      const response = await getSeedPolicy(examId, questionId);
      setSeedPolicy(response.policy);
      policyForm.setFieldsValue({ ...response.policy, enabled: response.policy.status === "active" });
    } catch (reason) {
      if (reason instanceof ApiClientError && reason.status === 404) {
        setSeedPolicy(undefined);
        policyForm.setFieldsValue({ rate: 0.1, min_interval: 10, max_interval: 30, enabled: false });
      } else {
        message.error(errorMessage(reason));
      }
    } finally {
      setSeedLoading(false);
    }
  };

  const openSeed = () => {
    const questionId = seedQuestionId || data.questions[0]?.question.id;
    if (!questionId) return;
    setSeedOpen(true);
    void loadSeedPolicy(questionId);
  };

  const saveSeedPolicy = async () => {
    if (!seedQuestionId) return;
    const values = await policyForm.validateFields();
    try {
      const response = await putSeedPolicy(examId, seedQuestionId, {
        rate: values.rate,
        min_interval: values.min_interval,
        max_interval: values.max_interval,
        status: values.enabled ? "active" : "paused",
        expected_revision: seedPolicy?.revision ?? 0
      });
      setSeedPolicy(response.policy);
      policyForm.setFieldsValue({ ...response.policy, enabled: response.policy.status === "active" });
      message.success("盲测策略已保存");
      void load();
    } catch (reason) {
      message.error(errorMessage(reason));
    }
  };

  const openBackmark = (initial?: Partial<ReturnType<typeof backmarkForm.getFieldsValue>>) => {
    invalidateBackmarkPreview();
    setBackmarkOpen(true);
    setBackmarkLoading(true);
    void Promise.all([listBackmarkBatches(examId), listAllActiveGraders()])
      .then(([response, users]) => {
        setBackmarkBatches(response.backmark_batches);
        setBackmarkBatchesNextCursor(response.has_more ? response.next_cursor : "");
        setBackmarkReviewers(users);
        const questionId = backmarkQuestionId || data.questions[0]?.question.id || "";
        setBackmarkQuestionId(questionId);
        backmarkForm.setFieldsValue({ question_id: questionId, disposition: "arbitrate", arbitration_delta: 1, ...initial });
      })
      .catch((reason) => message.error(errorMessage(reason)))
      .finally(() => setBackmarkLoading(false));
  };
  const loadMoreBackmarkBatches = async () => {
    if (!backmarkBatchesNextCursor) return;
    setBackmarkLoading(true);
    try {
      const response = await listBackmarkBatches(examId, backmarkBatchesNextCursor);
      setBackmarkBatches((current) => [...current, ...response.backmark_batches]);
      setBackmarkBatchesNextCursor(response.has_more ? response.next_cursor : "");
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setBackmarkLoading(false);
    }
  };
  const selectorFromForm = (values: ReturnType<typeof backmarkForm.getFieldsValue>): BackmarkSelector => ({
    time_range: values.date_range ? { from: values.date_range[0].startOf("day").toISOString(), to: values.date_range[1].endOf("day").toISOString() } : undefined,
    grader_id: values.grader_id?.trim() || undefined,
    score_band: values.score_min === undefined && values.score_max === undefined ? undefined : { min: values.score_min, max: values.score_max }
  });
  const previewBackmark = async () => {
    invalidateBackmarkPreview();
    const revision = backmarkPreviewRevision.current;
    try {
      const values = await backmarkForm.validateFields(["question_id", "date_range", "score_min", "score_max", "grader_id"]);
      if (revision !== backmarkPreviewRevision.current) return;
      const response = await previewBackmarkBatch(examId, values.question_id, selectorFromForm(values));
      if (revision !== backmarkPreviewRevision.current) return;
      setBackmarkQuestionId(values.question_id);
      setBackmarkPreview(response.preview);
      if (response.preview.affected_count === 0) message.warning("当前筛选没有匹配已完成的阅卷任务");
    } catch (reason) {
      if (reason instanceof ApiClientError) message.error(errorMessage(reason));
    }
  };
  const createBackmark = async () => {
    if (!backmarkPreview?.affected_count || !backmarkPreview.selector_hash) return;
    const preview = backmarkPreview;
    const revision = backmarkPreviewRevision.current;
    try {
      const values = await backmarkForm.validateFields();
      if (revision !== backmarkPreviewRevision.current) return;
      const response = await createBackmarkBatch(examId, values.question_id, {
        selector_hash: preview.selector_hash,
        source_incident_id: values.source_incident_id.trim(), selector: selectorFromForm(values), reassigned_to: values.reassigned_to,
        policy: { disposition: values.disposition, arbitration_delta: values.disposition === "arbitrate" ? values.arbitration_delta : 0 }
      });
      setBackmarkBatches((current) => [response.backmark.batch, ...current]);
      invalidateBackmarkPreview();
      message.success(`已创建 ${response.backmark.batch.affected_count} 份独立回标任务`);
    } catch (reason) {
      if (reason instanceof ApiClientError && reason.code === "backmark_preview_stale") invalidateBackmarkPreview();
      if (reason instanceof ApiClientError) message.error(errorMessage(reason));
    }
  };
  function invalidateBackmarkPreview() {
    backmarkPreviewRevision.current += 1;
    setBackmarkPreview(undefined);
  }
  const loadIncidents = async () => {
    setIncidentsLoading(true);
    try {
      const response = await listGradingQualityIncidents(examId);
      setIncidents(response.incidents);
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setIncidentsLoading(false);
    }
  };
  const openIncidents = () => {
    setIncidentsOpen(true);
    void loadIncidents();
  };
  const startBackmarkFromIncident = (incident: GradingQualityIncident) => {
    setIncidentsOpen(false);
    openBackmark({
      question_id: incident.question_id,
      source_incident_id: incident.id,
      grader_id: incident.grader_id,
      date_range: undefined,
      disposition: incident.severity === "critical" ? "regrade" : "arbitrate",
      arbitration_delta: 1
    });
    setBackmarkQuestionId(incident.question_id);
    setBackmarkPreview(undefined);
  };
  const resolveIncident = async (id: string) => {
    try {
      await resolveGradingQualityIncident(id);
      setIncidents((current) => current.filter((item) => item.id !== id));
      message.success("质量事件已关闭");
      void load();
    } catch (reason) {
      message.error(errorMessage(reason));
    }
  };
  const refreshQuestionDrift = async (questionId: string) => {
    try {
      await recomputeGraderDrift(examId, questionId);
      message.success("已刷新该题的阅卷质量窗口");
      void load();
    } catch (reason) {
      message.error(errorMessage(reason));
    }
  };

  const openBackmarkSummary = async (batch: BackmarkBatch) => {
    setBackmarkSummaryOpen(true);
    setBackmarkSummary(undefined);
    setBackmarkRegradePreview(undefined);
    setBackmarkRegradeAssignee("");
    setBackmarkRegradeReleaseID("");
    setBackmarkRegradeLoading(true);
    try {
      const [summaryResponse, releasesResponse, usersResponse] = await Promise.all([
        getBackmarkBatch(batch.id, { status: "regrade_required" }),
        listScoreReleases(batch.exam_id),
        listAllActiveGraders()
      ]);
      setBackmarkSummary(summaryResponse.backmark);
      setBackmarkSummaryNextCursor(summaryResponse.has_more ? summaryResponse.next_cursor : "");
      const published = releasesResponse.score_releases.filter((release) => release.status === "published");
      setBackmarkRegradeReleases(published);
      setBackmarkRegradeReleaseID(published[0]?.id ?? "");
      const graders = usersResponse;
      setBackmarkReviewers(graders);
      setBackmarkRegradeAssignee(graders[0]?.id ?? "");
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setBackmarkRegradeLoading(false);
    }
  };

  const loadMoreBackmarkSummary = async () => {
    if (!backmarkSummary || !backmarkSummaryNextCursor) return;
    setBackmarkRegradeLoading(true);
    try {
      const response = await getBackmarkBatch(backmarkSummary.batch.id, { limit: 50, cursor: backmarkSummaryNextCursor, status: "regrade_required" });
      setBackmarkSummary((current) => current ? { ...response.backmark, items: [...current.items, ...response.backmark.items] } : response.backmark);
      setBackmarkSummaryNextCursor(response.has_more ? response.next_cursor : "");
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setBackmarkRegradeLoading(false);
    }
  };

  const hasRegradeCandidates = (backmarkSummary?.regrade_required_count ?? 0) > 0;
  const previewBackmarkRegrade = async () => {
    if (!backmarkSummary || !backmarkRegradeReleaseID || !backmarkRegradeAssignee) return;
    setBackmarkRegradeLoading(true);
    try {
      const response = await previewBackmarkBatchRegrade(backmarkSummary.batch.id, {
        source_release_id: backmarkRegradeReleaseID,
        assignee_id: backmarkRegradeAssignee
      });
      setBackmarkRegradePreview(response.preview);
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setBackmarkRegradeLoading(false);
    }
  };

  const createBackmarkRegrade = async () => {
    if (!backmarkSummary || !backmarkRegradePreview || !backmarkRegradeReleaseID || !backmarkRegradeAssignee) return;
    setBackmarkRegradeLoading(true);
    try {
      const response = await createBackmarkBatchRegradeJob(backmarkSummary.batch.id, {
        source_release_id: backmarkRegradeReleaseID,
        assignee_id: backmarkRegradeAssignee
      });
      message.success(`已创建题目复评任务（${response.regrade.job.affected_count} 份），仍需审批、完成复评并发布新版本。`);
      setBackmarkSummaryOpen(false);
      void load();
    } catch (reason) {
      message.error(errorMessage(reason));
    } finally {
      setBackmarkRegradeLoading(false);
    }
  };

  return (
    <section className="page-stack">
      <div className="page-heading compact-heading">
        <div>
          <span className="eyebrow">评分质量</span>
          <h1>质量总览</h1>
          <p>基于标准卷、阅卷员标定、盲测、双评和回标的考试级质量证据。</p>
        </div>
        <Space>
          <span className="muted-text">更新于 {new Date(data.generated_at).toLocaleString("zh-CN", { hour12: false })}</span>
          {canManage ? <Button onClick={openSeed}>盲测策略</Button> : null}
          {canManage ? <Button onClick={openIncidents}>质量事件</Button> : null}
          {canManage ? <Button onClick={openBackmark}>创建回标</Button> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void load()}>刷新</Button>
        </Space>
      </div>

      <Alert
        showIcon
        type={data.gate === "blocked" ? "error" : data.gate === "ready" ? "success" : "warning"}
        icon={data.gate === "ready" ? <ShieldCheck size={19} /> : <ShieldAlert size={19} />}
        message={`发布可行性：${gateLabel(data.gate)}`}
        description={data.gate === "blocked"
          ? "存在必须先处置的质量问题，不能发布成绩。"
          : data.gate === "ready" ? "当前没有阻断性质量事件。" : "当前没有阻断项，但仍有待补齐的质量证据或处理项。"}
      />

      <div className="quality-dashboard-summary">
        <Statistic title="阻断项" value={data.blocking.length} valueStyle={{ color: data.blocking.length ? "#cf1322" : undefined }} />
        <Statistic title="需关注" value={data.warnings.length} valueStyle={{ color: data.warnings.length ? "#d48806" : undefined }} />
        <Statistic title="题目数" value={data.questions.length} />
        <Statistic title="可继续题目" value={data.questions.filter((item) => item.gate === "ready").length} valueStyle={{ color: "#389e0d" }} />
      </div>

      {error ? <Alert type="warning" showIcon message={error} /> : null}

      <div className="content-card">
        <div className="section-header"><div><h2>每题质量状态</h2><p>展开题目可查看数据依据、负责人和处理路径。</p></div></div>
        <Table
          size="middle"
          rowKey={(row) => row.question.id}
          pagination={false}
          dataSource={data.questions}
          columns={[
            { title: "题号", dataIndex: ["question", "question_no"], width: 92 },
            { title: "题型", dataIndex: ["question", "archetype_code"], render: (value: string) => value || "未配置" },
            { title: "风险", dataIndex: ["question", "risk_tier"], render: (value: string) => value ? <Tag>{value}</Tag> : "未配置" },
            { title: "质量状态", dataIndex: "gate", render: (value: Gate) => <Tag color={gateColor(value)}>{gateLabel(value)}</Tag> },
            { title: "标准卷", dataIndex: "gold", render: (value: QualityDashboard["questions"][number]["gold"]) => value.ready ? `已覆盖 ${value.active_approved} 份` : "待补充" },
            { title: "盲测一致", dataIndex: "seed", render: (value: QualityDashboard["questions"][number]["seed"]) => `${percentage(value.exact_agreement.rate)} · ${value.sample_size} 份` },
            { title: "未处理事件", dataIndex: "drift", render: (value: QualityDashboard["questions"][number]["drift"]) => value.open_critical + value.open_warnings },
            { title: "待回标", dataIndex: "backmark", render: (value: QualityDashboard["questions"][number]["backmark"]) => value.pending_items }
          ]}
          expandable={{
            expandedRowRender: (row) => (
              <Collapse
                items={[
                  {
                    key: "evidence",
                    label: "质量证据",
                    children: <><Descriptions size="small" column={{ xs: 1, sm: 2, lg: 3 }} items={[
                      { key: "calibration", label: "标定", children: row.calibration.configured ? `完成 ${row.calibration.completed}，通过 ${row.calibration.passed}` : "未配置" },
                      { key: "human", label: "双评一致", children: `${percentage(row.human_human_agreement.within_rule_agreement.rate)} · ${row.human_human_agreement.sample_size} 份` },
                      { key: "groups", label: "分组复核", children: `${row.answer_groups.group_count} 组，待抽检 ${row.answer_groups.open_sample_groups} 组` },
                      { key: "backmark", label: "回标", children: `开放批次 ${row.backmark.open_batches}，待处理 ${row.backmark.pending_items}` },
                      { key: "bands", label: "标准卷分档", children: row.gold.score_bands.map((item) => `${item.band} ${item.count}`).join("，") || "暂无" }
                    ]} />{canManage ? <Button size="small" onClick={() => void refreshQuestionDrift(row.question.id)}>重新计算本题质量</Button> : null}</>
                  },
                  {
                    key: "findings",
                    label: `待处理事项（${row.findings.length}）`,
                    children: row.findings.length ? <Table className="dense-data-table" size="small" pagination={false} rowKey={(item) => `${item.code}-${item.incident_ref ?? item.question_id}`} dataSource={row.findings} columns={[
                      { title: "状态", dataIndex: "status", render: (value: Gate) => <Tag color={gateColor(value)}>{gateLabel(value)}</Tag> },
                      { title: "原因", dataIndex: "reason" },
                      { title: "负责人", dataIndex: "owner" },
                      { title: "处理路径", dataIndex: "resolution" }
                    ]} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有待处理事项" />
                  }
                ]}
              />
            )
          }}
        />
      </div>
      <Drawer title="盲测策略" width={520} open={seedOpen} onClose={() => setSeedOpen(false)} extra={canManage ? <Button type="primary" loading={seedLoading} onClick={() => void saveSeedPolicy()}>保存</Button> : null}>
        <Form form={policyForm} layout="vertical" disabled={!canManage || seedLoading}>
          <Form.Item label="题目" required>
            <Select value={seedQuestionId} options={data.questions.map((item) => ({ value: item.question.id, label: `${item.question.question_no} · ${item.question.archetype_code || "未配置"}` }))} onChange={(value) => void loadSeedPolicy(value)} />
          </Form.Item>
          <Form.Item name="rate" label="抽样比例" rules={[{ required: true, message: "请输入抽样比例" }]}><InputNumber min={0.001} max={1} step={0.01} precision={3} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="min_interval" label="最小间隔（正常阅卷题数）" rules={[{ required: true }]}><InputNumber min={1} max={100000} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="max_interval" label="最大间隔（正常阅卷题数）" rules={[{ required: true }]}><InputNumber min={1} max={100000} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="enabled" label="启用盲测" valuePropName="checked"><Switch /></Form.Item>
        </Form>
        <Alert type="info" showIcon message="盲测题通过正常阅卷入口呈现；阅卷员不会看到标准卷答案或参考分。" />
      </Drawer>
      <Drawer title="创建回标批次" width={720} open={backmarkOpen} onClose={() => setBackmarkOpen(false)} extra={<Button icon={<RefreshCw size={15} />} loading={backmarkLoading} onClick={() => openBackmark()}>刷新</Button>}>
        <Alert type="info" showIcon message="独立回标不会直接改分" description="先确认影响范围，再分配给非原阅卷人独立评分；差异只进入后续仲裁或重评流程。" />
        <Form form={backmarkForm} layout="vertical" disabled={backmarkLoading} initialValues={{ disposition: "arbitrate", arbitration_delta: 1 }} onValuesChange={(changed) => {
          if (["question_id", "date_range", "score_min", "score_max", "grader_id"].some((field) => field in changed)) invalidateBackmarkPreview();
        }}>
          <Form.Item name="question_id" label="题目" rules={[{ required: true, message: "请选择题目" }]}><Select options={data.questions.map((item) => ({ value: item.question.id, label: `${item.question.question_no} · ${item.question.archetype_code || "未配置"}` }))} /></Form.Item>
          <Form.Item name="date_range" label="完成阅卷时间（可选）"><DatePicker.RangePicker style={{ width: "100%" }} /></Form.Item>
          <Space.Compact block>
            <Form.Item name="score_min" label="原分下限" style={{ flex: 1 }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
            <Form.Item name="score_max" label="原分上限" style={{ flex: 1 }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
          </Space.Compact>
          <Form.Item name="grader_id" label="仅原阅卷员 ID（可选）"><Input allowClear placeholder="用于按特定阅卷员筛选" /></Form.Item>
          <Button onClick={() => void previewBackmark()} loading={backmarkLoading}>预览影响范围</Button>
          {backmarkPreview ? <Descriptions className="backmark-preview" size="small" bordered column={1} items={[
            { key: "count", label: "受影响任务", children: `${backmarkPreview.affected_count} 份` },
            { key: "time", label: "完成时间", children: backmarkPreview.time_range.from && backmarkPreview.time_range.to ? `${new Date(backmarkPreview.time_range.from).toLocaleString("zh-CN", { hour12: false })} 至 ${new Date(backmarkPreview.time_range.to).toLocaleString("zh-CN", { hour12: false })}` : "无匹配记录" },
            { key: "band", label: "原分分布", children: backmarkPreview.score_bands.map((item) => `${item.score}分 · ${item.count}份`).join("，") || "无" }
          ]} /> : null}
          {backmarkPreview && backmarkPreview.affected_count > 1000 ? <Alert type="warning" showIcon message="单个回标批次最多分配 1000 份答卷，请缩小范围后重新预览。" /> : null}
          <Form.Item name="source_incident_id" label="质量事件编号" rules={[{ required: true, whitespace: true, message: "请填写触发回标的质量事件编号" }]}><Input placeholder="例如 drift-20260812-001" /></Form.Item>
          <Form.Item name="reassigned_to" label="指定回标阅卷员" rules={[{ required: true, message: "请选择非原阅卷人" }]}><Select showSearch optionFilterProp="label" options={backmarkReviewers.map((user) => ({ value: user.id, label: user.display_name || user.username }))} placeholder="选择一名阅卷员" /></Form.Item>
          <Form.Item name="disposition" label="出现差异后的去向"><Select options={[{ value: "confirm", label: "记录差异，待人工确认" }, { value: "arbitrate", label: "达到阈值进入仲裁" }, { value: "regrade", label: "进入题目重评流程" }]} /></Form.Item>
          <Form.Item noStyle shouldUpdate={(previous, current) => previous.disposition !== current.disposition}>{({ getFieldValue }) => getFieldValue("disposition") === "arbitrate" ? <Form.Item name="arbitration_delta" label="仲裁分差阈值" rules={[{ required: true }]}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item> : null}</Form.Item>
        </Form>
        <Space><Button onClick={() => setBackmarkOpen(false)}>取消</Button><Button type="primary" disabled={!backmarkPreview?.affected_count || !backmarkPreview.selector_hash} loading={backmarkLoading} onClick={() => void createBackmark()}>创建并分配回标</Button></Space>
        <div className="section-header backmark-batch-history"><div><h3>已有回标批次</h3></div></div>
        <Table className="dense-data-table" size="small" rowKey="id" loading={backmarkLoading} pagination={false} dataSource={backmarkBatches} footer={backmarkBatchesNextCursor ? () => <Button block loading={backmarkLoading} onClick={() => void loadMoreBackmarkBatches()}>加载更多批次</Button> : undefined} columns={[
          { title: "批次", dataIndex: "id", ellipsis: true },
          { title: "题目", dataIndex: "question_id", ellipsis: true },
          { title: "影响答卷", dataIndex: "affected_count" },
          { title: "状态", dataIndex: "status", render: (value: string) => <Tag>{value}</Tag> },
          { title: "创建时间", dataIndex: "created_at", render: (value: string) => new Date(value).toLocaleString("zh-CN", { hour12: false }) }
          , { title: "结果", key: "summary", render: (_, batch: BackmarkBatch) => <Button size="small" type="link" onClick={() => void openBackmarkSummary(batch)}>查看差异</Button> }
        ]} locale={{ emptyText: "当前考试没有回标批次" }} />
      </Drawer>
      <Drawer title="回标差异与复评交接" width={760} open={backmarkSummaryOpen} onClose={() => setBackmarkSummaryOpen(false)} extra={<Button loading={backmarkRegradeLoading} onClick={() => backmarkSummary && void openBackmarkSummary(backmarkSummary.batch)}>刷新</Button>}>
        {!backmarkSummary ? <LoadingState label="正在加载回标差异" /> : <>
          <Alert type="info" showIcon message="回标只记录候选差异，不会修改当前成绩" description="只有标记为“需重评”的已完成项目，才能由质量管理员明确转入题目复评。新任务仍需审批、人工复评并创建新的成绩发布版本。" />
          <Descriptions size="small" bordered column={2} items={[
            { key: "batch", label: "回标批次", children: backmarkSummary.batch.id },
            { key: "status", label: "批次状态", children: <Tag>{backmarkSummary.batch.status === "completed" ? "已完成" : backmarkSummary.batch.status === "cancelled" ? "已取消" : backmarkSummary.batch.status === "ready_for_confirmation" ? "待确认" : "处理中"}</Tag> },
            { key: "count", label: "受影响答卷", children: `${backmarkSummary.batch.affected_count} 份` },
            { key: "regrade", label: "需重评", children: `${backmarkSummary.regrade_required_count ?? 0} 份` },
            { key: "policy", label: "差异去向", children: backmarkSummary.batch.policy.disposition }
          ]} />
          <div className="section-header backmark-batch-history"><div><h3>分差分布</h3></div></div>
          {backmarkSummary.diff_histogram.length ? <Table className="dense-data-table" size="small" rowKey={(item) => `${item.delta}`} pagination={false} dataSource={backmarkSummary.diff_histogram} columns={[
            { title: "新旧分差", dataIndex: "delta", render: (value: number) => `${value > 0 ? "+" : ""}${value} 分` },
            { title: "答卷数", dataIndex: "count", render: (value: number) => `${value} 份` }
          ]} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未有完成的回标评分" />}
          <div className="section-header backmark-batch-history"><div><h3>严重变更清单</h3><p>仅列出需要进入题目复评的回标结果。</p></div></div>
          <Table className="dense-data-table" size="small" rowKey="id" pagination={false} dataSource={backmarkSummary.items.filter((item) => item.status === "regrade_required")} columns={[
            { title: "回标项", dataIndex: "id", ellipsis: true },
            { title: "原分", dataIndex: "original_score" },
            { title: "新评分", dataIndex: "new_score", render: (value?: number) => value ?? "待完成" },
            { title: "分差", dataIndex: "diff", render: (value?: number) => value === undefined ? "-" : `${value > 0 ? "+" : ""}${value}` },
            { title: "状态", dataIndex: "status", render: () => <Tag color="error">需重评</Tag> }
          ]} locale={{ emptyText: hasRegradeCandidates ? "当前页暂无需重评项，请加载更多结果" : "没有需要题目复评的严重差异" }} />
          {backmarkSummaryNextCursor ? <Button block loading={backmarkRegradeLoading} onClick={() => void loadMoreBackmarkSummary()}>加载更多回标结果</Button> : null}
          {canManage && hasRegradeCandidates ? <>
            <div className="section-header backmark-batch-history"><div><h3>转入题目复评</h3><p>系统将从已发布成绩版本重新冻结本批次对应的学生范围；不会直接采用回标候选分。</p></div></div>
            {backmarkSummary.batch.status !== "ready_for_confirmation" ? <Alert type="warning" showIcon message="回标批次尚未完成" description="请等待所有回标项结束，再创建稳定的复评范围。" /> : <>
              {!backmarkRegradeReleases.length ? <Alert type="warning" showIcon message="当前考试没有已发布的成绩版本" description="回标差异可继续查看；必须先有已发布版本才能发起正式题目复评。" /> : <>
                <Space direction="vertical" style={{ width: "100%" }}>
                  <Select value={backmarkRegradeReleaseID || undefined} placeholder="选择已发布成绩版本" options={backmarkRegradeReleases.map((release) => ({ value: release.id, label: `V${release.version} · ${release.reason || "正式成绩"}` }))} onChange={(value) => { setBackmarkRegradeReleaseID(value); setBackmarkRegradePreview(undefined); }} />
                  <Select value={backmarkRegradeAssignee || undefined} placeholder="选择复评阅卷员" options={backmarkReviewers.map((user) => ({ value: user.id, label: user.display_name || user.username }))} onChange={(value) => { setBackmarkRegradeAssignee(value); setBackmarkRegradePreview(undefined); }} />
                  <Button loading={backmarkRegradeLoading} disabled={!backmarkRegradeReleaseID || !backmarkRegradeAssignee} onClick={() => void previewBackmarkRegrade()}>预览正式复评范围</Button>
                </Space>
                {backmarkRegradePreview ? <Alert className="backmark-preview" type="info" showIcon message={`将冻结 ${backmarkRegradePreview.affected_count} 份已发布答卷`} description={`来源成绩版本 V${backmarkRegradePreview.source_release_version}；创建后仍需审批、复评、差异复核和新版本发布。`} action={<Button type="primary" size="small" loading={backmarkRegradeLoading} onClick={() => void createBackmarkRegrade()}>创建复评任务</Button>} /> : null}
              </>}
            </>}
          </> : null}
        </>}
      </Drawer>
      <Drawer title="阅卷质量事件" width={760} open={incidentsOpen} onClose={() => setIncidentsOpen(false)} extra={<Button icon={<RefreshCw size={15} />} loading={incidentsLoading} onClick={() => void loadIncidents()}>刷新</Button>}>
        <Alert type="info" showIcon message="仅展示汇总质量信号" description="不会展示标准卷答案、学生答卷或教师私密评分。可从事件创建独立回标，或在处置完成后关闭事件。" />
        <Table className="dense-data-table" size="small" rowKey="id" loading={incidentsLoading} pagination={false} dataSource={incidents} columns={[
          { title: "题目", dataIndex: "question_id", ellipsis: true },
          { title: "类型", dataIndex: "type", render: (value: string) => <Tag>{value}</Tag> },
          { title: "级别", dataIndex: "severity", render: (value: string) => <Tag color={value === "critical" ? "error" : "warning"}>{value === "critical" ? "严重" : "警告"}</Tag> },
          { title: "样本", dataIndex: "metric_snapshot", render: (value: Record<string, unknown>) => typeof value.sample_count === "number" ? `${value.sample_count} 份` : "-" },
          { title: "操作", key: "actions", render: (_, incident: GradingQualityIncident) => <Space><Button type="link" size="small" onClick={() => startBackmarkFromIncident(incident)}>创建回标</Button><Button type="link" size="small" onClick={() => void resolveIncident(incident.id)}>关闭</Button></Space> }
        ]} locale={{ emptyText: "当前没有开放的质量事件" }} />
      </Drawer>
    </section>
  );
}
