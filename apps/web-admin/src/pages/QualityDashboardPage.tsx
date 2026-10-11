import { primitiveDisplayValue } from "../utils/displayValue";
import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, App, Button, DatePicker, Descriptions, Drawer, Empty, Form, InputNumber, Select, Space, Switch, Table, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import type { Dayjs } from "dayjs";
import type { QualityDashboard } from "../api/qualityDashboard";
import { getExamQualityDashboard } from "../api/qualityDashboard";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getSeedPolicy, listBackmarkBatches, putSeedPolicy, type BackmarkBatch, type SeedPolicy } from "../api/qualityOperations";
import { createBackmarkBatch, createBackmarkBatchRegradeJob, getBackmarkBatch, previewBackmarkBatch, previewBackmarkBatchRegrade, type BackmarkPolicy, type BackmarkPreview, type BackmarkSelector, type BackmarkSummary } from "../api/backmark";
import { listScoreReleases, type RegradePreview, type ScoreRelease } from "../api/scoreReleases";
import { listReviewActors, type WorkflowActor } from "../api/workflowActors";
import { listGradingQualityIncidents, recomputeGraderDrift, resolveGradingQualityIncident, type GradingQualityIncident } from "../api/graderDrift";
import { ErrorState, LoadingState } from "../components/PageState";
import { BackmarkBatchHistory, BackmarkSummaryContent, QualityDashboardOverview, workflowActorLabel, workflowActorOptions } from "./QualityDashboardViews";
import { GoldPaperManagerDrawer } from "../features/gold-papers";
import { CalibrationDrawer } from "../features/calibration";
import { AnswerGroupingDrawer } from "../features/answer-groups";

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "质量看板暂时无法加载，请稍后重试");
}

const qualityIncidentLabels: Record<string, string> = {
  grader_bias_high: "评分持续偏高", grader_bias_low: "评分持续偏低",
  high_inconsistency: "评分差异较大", severe_seed_failure: "盲测出现严重评分差异",
  rubric_disagreement_spike: "评分点判断差异增多", suspicious_speed: "阅卷速度需要核查"
};

function incidentTypeLabel(type: string) { return qualityIncidentLabels[type] ?? "其他评分质量异常"; }
function incidentTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "发生时间未提供" : date.toLocaleString("zh-CN", { hour12: false });
}

function backmarkPreviewTimeRange(range: BackmarkPreview["time_range"]) {
  // 缺少任一时间端点时沿用空结果提示，不能拼出不完整的预览范围。
  if (range.from && range.to) return `${new Date(range.from).toLocaleString("zh-CN", { hour12: false })} 至 ${new Date(range.to).toLocaleString("zh-CN", { hour12: false })}`;
  return "无匹配记录";
}

export function QualityDashboardPage({ examId, canManage = false, currentUserId, onNavigate }: Readonly<{ examId: string; canManage?: boolean; currentUserId?: string; onNavigate?: (path: string) => void }>) {
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
  const [backmarkReviewers, setBackmarkReviewers] = useState<WorkflowActor[]>([]);
  const [backmarkSummary, setBackmarkSummary] = useState<BackmarkSummary>();
  const [backmarkSummaryNextCursor, setBackmarkSummaryNextCursor] = useState("");
  const [backmarkSummaryOpen, setBackmarkSummaryOpen] = useState(false);
  const [selectedBackmarkBatch, setSelectedBackmarkBatch] = useState<BackmarkBatch>();
  const [backmarkSummaryError, setBackmarkSummaryError] = useState("");
  const [backmarkReleasesError, setBackmarkReleasesError] = useState("");
  const [backmarkReviewersError, setBackmarkReviewersError] = useState("");
  const [backmarkRegradeReviewers, setBackmarkRegradeReviewers] = useState<WorkflowActor[]>([]);
  const summaryRequest = useRef(0);
  const summaryScope = useRef<{ examId: string; batchId: string } | undefined>(undefined);
  const [backmarkRegradePreview, setBackmarkRegradePreview] = useState<RegradePreview>();
  const [backmarkRegradeAssignee, setBackmarkRegradeAssignee] = useState("");
  const [backmarkRegradeReleaseID, setBackmarkRegradeReleaseID] = useState("");
  const [backmarkRegradeReleases, setBackmarkRegradeReleases] = useState<ScoreRelease[]>([]);
  const [backmarkRegradeLoading, setBackmarkRegradeLoading] = useState(false);
  const [incidentsOpen, setIncidentsOpen] = useState(false);
  const [incidents, setIncidents] = useState<GradingQualityIncident[]>([]);
  const [incidentsLoading, setIncidentsLoading] = useState(false);
  const [materialAction, setMaterialAction] = useState<{ type: "gold" | "calibration" | "groups"; questionId: string }>();
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

  useEffect(() => {
    summaryRequest.current += 1;
    summaryScope.current = undefined;
    setBackmarkSummaryOpen(false);
    setSelectedBackmarkBatch(undefined);
    setBackmarkSummary(undefined);
    return () => { summaryRequest.current += 1; summaryScope.current = undefined; };
  }, [examId]);

  if (loading && !data) return <LoadingState label="正在加载评分质量" />;
  if (error && !data) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!data) return <Empty description="暂无质量数据" />;
  const questionLabels = Object.fromEntries(data.questions.map((row) => [row.question.id, row.question.question_no]));

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
    void Promise.all([listBackmarkBatches(examId), listReviewActors(examId), listGradingQualityIncidents(examId)])
      .then(([response, users, events]) => {
        setIncidents(events.incidents);
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
      const recipient = backmarkReviewers.find((user) => user.id === values.reassigned_to && user.username?.trim());
      if (!recipient) { message.error("无法核对接手教师账号，请重新读取人员目录后选择。"); return; }
      // 提交预览的范围哈希，由服务端识别预览后已变化的任务集合。
      const response = await createBackmarkBatch(examId, values.question_id, {
        selector_hash: preview.selector_hash,
        source_incident_id: values.source_incident_id.trim(), selector: selectorFromForm(values), reassigned_to: values.reassigned_to,
        policy: { disposition: values.disposition, arbitration_delta: values.disposition === "arbitrate" ? values.arbitration_delta : 0 }
      });
      setBackmarkBatches((current) => [response.backmark.batch, ...current]);
      invalidateBackmarkPreview();
      message.success(`${questionLabels[values.question_id] || "题号待核对"}：已创建 ${response.backmark.batch.affected_count} 份独立回标任务，接手教师：${workflowActorLabel(recipient)}`);
    } catch (reason) {
      if (reason instanceof ApiClientError && reason.code === "backmark_preview_stale") invalidateBackmarkPreview();
      if (reason instanceof ApiClientError) message.error(errorMessage(reason));
    }
  };
  // 筛选变化立即淘汰在途预览；请求返回和表单校验结束都需核对同一代次。
  function invalidateBackmarkPreview() {
    backmarkPreviewRevision.current += 1;
    setBackmarkPreview(undefined);
  }
  const loadIncidents = async () => {
    setIncidentsLoading(true);
    try {
      // 教师姓名只从已有考试管理目录读取，目录失败不隐藏已读取的质量事件。
      const [events, reviewers] = await Promise.allSettled([
        listGradingQualityIncidents(examId), canManage ? listReviewActors(examId) : Promise.resolve([])
      ]);
      if (events.status === "rejected") throw events.reason;
      setIncidents(events.value.incidents);
      setBackmarkReviewers(reviewers.status === "fulfilled" ? reviewers.value : []);
      if (reviewers.status === "rejected") message.warning("质量事件已读取，教师姓名暂不可读；可刷新重试。");
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
  const handleFinding = (code: string, questionId: string) => {
    if (!canManage) return;
    if (code.startsWith("gold_")) setMaterialAction({ type: "gold", questionId });
    else if (code.startsWith("calibration_")) setMaterialAction({ type: "calibration", questionId });
    else if (code.startsWith("answer_group_")) setMaterialAction({ type: "groups", questionId });
    else if (code.startsWith("seed_")) { setSeedOpen(true); void loadSeedPolicy(questionId); }
    else if (code.startsWith("quality_incident_")) openIncidents();
    else if (code === "backmark_pending") openBackmark({ question_id: questionId });
  };

  const openBackmarkSummary = async (batch: BackmarkBatch, reset = true) => {
    if (batch.exam_id !== examId) return;
    const request = ++summaryRequest.current;
    summaryScope.current = { examId, batchId: batch.id };
    const isCurrent = () => request === summaryRequest.current && summaryScope.current?.examId === examId && summaryScope.current?.batchId === batch.id;
    setSelectedBackmarkBatch(batch);
    setBackmarkSummaryOpen(true);
    if (reset) { setBackmarkSummary(undefined); setBackmarkSummaryNextCursor(""); }
    setBackmarkSummaryError("");
    setBackmarkReleasesError("");
    setBackmarkReviewersError("");
    setBackmarkRegradePreview(undefined);
    setBackmarkRegradeAssignee("");
    setBackmarkRegradeReleaseID("");
    setBackmarkRegradeLoading(true);
    try {
      // 差异是查看必需材料；版本和人员目录只决定能否交接，不应遮住已成功读取的证据。
      const [summaryResponse, releasesResponse, usersResponse] = await Promise.allSettled([
        getBackmarkBatch(batch.id, { status: "regrade_required" }),
        listScoreReleases(batch.exam_id),
        listReviewActors(examId)
      ]);
      if (!isCurrent()) return;
      if (summaryResponse.status === "fulfilled" && summaryResponse.value.backmark.batch.id === batch.id && summaryResponse.value.backmark.batch.exam_id === examId) {
        setBackmarkSummary(summaryResponse.value.backmark);
        setBackmarkSummaryNextCursor(summaryResponse.value.has_more ? summaryResponse.value.next_cursor : "");
      } else setBackmarkSummaryError(summaryResponse.status === "rejected" ? errorMessage(summaryResponse.reason) : "返回的差异与当前批次不一致，请重新读取。");
      const published = releasesResponse.status === "fulfilled" ? releasesResponse.value.score_releases.filter((release) => release.status === "published") : [];
      setBackmarkRegradeReleases(published);
      setBackmarkRegradeReleaseID(published[0]?.id ?? "");
      if (releasesResponse.status === "rejected") setBackmarkReleasesError(`成绩版本读取失败：${errorMessage(releasesResponse.reason)}`);
      const graders = usersResponse.status === "fulfilled" ? usersResponse.value : [];
      setBackmarkRegradeReviewers(graders);
      setBackmarkRegradeAssignee(graders.find((user) => user.username?.trim())?.id ?? "");
      if (usersResponse.status === "rejected") setBackmarkReviewersError(`教师目录读取失败：${errorMessage(usersResponse.reason)}`);
    } finally {
      if (isCurrent()) setBackmarkRegradeLoading(false);
    }
  };

  const closeBackmarkSummary = () => {
    // 关闭、切批次和换考试都淘汰旧请求；迟到结果不能重开弹窗或清除新批次的选择。
    summaryRequest.current += 1;
    summaryScope.current = undefined;
    setBackmarkSummaryOpen(false);
    setBackmarkRegradeLoading(false);
  };

  const loadMoreBackmarkSummary = async () => {
    if (!backmarkSummary || !backmarkSummaryNextCursor) return;
    const request = ++summaryRequest.current;
    const batchId = backmarkSummary.batch.id;
    setBackmarkRegradeLoading(true);
    try {
      const response = await getBackmarkBatch(backmarkSummary.batch.id, { limit: 50, cursor: backmarkSummaryNextCursor, status: "regrade_required" });
      if (request !== summaryRequest.current || summaryScope.current?.batchId !== batchId) return;
      // 追加页也要验证服务端返回的对象，不能把其他批次的差异拼入当前证据。
      if (response.backmark.batch.id !== batchId || response.backmark.batch.exam_id !== examId) {
        setBackmarkSummaryError("返回的差异与当前批次不一致，请重新读取。");
        return;
      }
      setBackmarkSummary((current) => current?.batch.id === batchId ? { ...response.backmark, items: [...current.items, ...response.backmark.items] } : current);
      setBackmarkSummaryNextCursor(response.has_more ? response.next_cursor : "");
    } catch (reason) {
      if (request === summaryRequest.current) setBackmarkSummaryError(errorMessage(reason));
    } finally {
      if (request === summaryRequest.current) setBackmarkRegradeLoading(false);
    }
  };

  const previewBackmarkRegrade = async () => {
    if (!backmarkSummary || backmarkSummaryError || backmarkReleasesError || backmarkReviewersError || !backmarkRegradeReleaseID || !backmarkRegradeReviewers.some((user) => user.id === backmarkRegradeAssignee && user.username?.trim())) return;
    const request = ++summaryRequest.current;
    setBackmarkRegradeLoading(true);
    try {
      const response = await previewBackmarkBatchRegrade(backmarkSummary.batch.id, {
        source_release_id: backmarkRegradeReleaseID,
        assignee_id: backmarkRegradeAssignee
      });
      if (request === summaryRequest.current) setBackmarkRegradePreview(response.preview);
    } catch (reason) {
      if (request === summaryRequest.current) message.error(errorMessage(reason));
    } finally {
      if (request === summaryRequest.current) setBackmarkRegradeLoading(false);
    }
  };

  const createBackmarkRegrade = async () => {
    if (!backmarkSummary || !backmarkRegradePreview || !backmarkRegradeReleaseID || !backmarkRegradeAssignee) return;
    const recipient = backmarkRegradeReviewers.find((user) => user.id === backmarkRegradeAssignee && user.username?.trim());
    if (!recipient || backmarkSummaryError || backmarkReleasesError || backmarkReviewersError) return;
    const request = ++summaryRequest.current;
    setBackmarkRegradeLoading(true);
    try {
      const response = await createBackmarkBatchRegradeJob(backmarkSummary.batch.id, {
        source_release_id: backmarkRegradeReleaseID,
        assignee_id: backmarkRegradeAssignee
      });
      if (request !== summaryRequest.current) return;
      message.success(`${questionLabels[backmarkSummary.batch.question_id] || "题号待核对"}：已创建题目复评任务（${response.regrade.job.affected_count} 份），接手教师：${workflowActorLabel(recipient)}；仍需审批、完成复评并发布新版本。`);
      closeBackmarkSummary();
      void load();
    } catch (reason) {
      if (request === summaryRequest.current) message.error(errorMessage(reason));
    } finally {
      if (request === summaryRequest.current) setBackmarkRegradeLoading(false);
    }
  };

  return (
    <section className="page-stack">
      <QualityDashboardOverview data={data} error={error} canManage={canManage} onSeed={openSeed} onIncidents={openIncidents} onBackmark={openBackmark} onRefresh={() => void load()} onRecompute={(questionId) => void refreshQuestionDrift(questionId)} onHandleFinding={handleFinding} />
      {materialAction?.type === "gold" ? <GoldPaperManagerDrawer open examId={examId} questionId={materialAction.questionId} onClose={() => { setMaterialAction(undefined); void load(); }} /> : null}
      {materialAction?.type === "calibration" ? <CalibrationDrawer open examId={examId} questionId={materialAction.questionId} graderId={currentUserId} canManagePolicy={canManage} onClose={() => { setMaterialAction(undefined); void load(); }} /> : null}
      {materialAction?.type === "groups" ? <AnswerGroupingDrawer open examId={examId} initialQuestionId={materialAction.questionId} availableQuestions={data.questions.map(row => ({ id: row.question.id, question_no: row.question.question_no, question_type: row.question.archetype_code }))} canManage={canManage} canReadReview={canManage} onNavigate={onNavigate} onClose={() => { setMaterialAction(undefined); void load(); }} /> : null}
      {onNavigate ? <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/grading`)}>前往本场阅卷工作区</Button> : null}
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
          if ("question_id" in changed) backmarkForm.setFieldsValue({ source_incident_id: undefined });
        }}>
          <Form.Item name="question_id" label="题目" rules={[{ required: true, message: "请选择题目" }]}><Select options={data.questions.map((item) => ({ value: item.question.id, label: `${item.question.question_no} · ${item.question.archetype_code || "未配置"}` }))} /></Form.Item>
          <Form.Item name="date_range" label="完成阅卷时间（可选）"><DatePicker.RangePicker style={{ width: "100%" }} /></Form.Item>
          <Space.Compact block>
            <Form.Item name="score_min" label="原分下限" style={{ flex: 1 }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
            <Form.Item name="score_max" label="原分上限" style={{ flex: 1 }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item>
          </Space.Compact>
          <Form.Item name="grader_id" label="原阅卷教师（可选）"><Select allowClear showSearch optionFilterProp="label" options={workflowActorOptions(backmarkReviewers)} placeholder="全部原阅卷教师" /></Form.Item>
          <Button onClick={() => void previewBackmark()} loading={backmarkLoading}>预览影响范围</Button>
          {backmarkPreview ? <Descriptions className="backmark-preview" size="small" bordered column={1} items={[
            { key: "count", label: "受影响任务", children: `${backmarkPreview.affected_count} 份` },
            { key: "time", label: "完成时间", children: backmarkPreviewTimeRange(backmarkPreview.time_range) },
            { key: "band", label: "原分分布", children: backmarkPreview.score_bands.map((item) => `${item.score}分 · ${item.count}份`).join("，") || "无" }
          ]} /> : null}
          {backmarkPreview && backmarkPreview.affected_count > 1000 ? <Alert type="warning" showIcon message="单个回标批次最多分配 1000 份答卷，请缩小范围后重新预览。" /> : null}
          <Form.Item noStyle shouldUpdate={(previous, current) => previous.question_id !== current.question_id}>{({ getFieldValue }) => <Form.Item name="source_incident_id" label="来源质量事件" rules={[{ required: true, message: "请选择触发回标的质量事件" }]}><Select showSearch optionFilterProp="label" options={incidents.filter(incident => incident.question_id === getFieldValue("question_id")).map(incident => ({ value: incident.id, label: `${data.questions.find(item => item.question.id === incident.question_id)?.question.question_no ?? "题号暂不可读"} · ${incident.severity === "critical" ? "严重" : "警告"} · ${incidentTypeLabel(incident.type)} · ${primitiveDisplayValue(incident.metric_snapshot.sample_count, "未知")}份样本 · ${incidentTime(incident.created_at)}` }))} placeholder="选择本题实际开放事件" /></Form.Item>}</Form.Item>
          {!incidents.length ? <Alert type="info" showIcon message="当前没有开放质量事件" description="先在质量事件中核查信号，再从真实事件发起独立回标。" action={<Button onClick={() => { setBackmarkOpen(false); openIncidents(); }}>查看质量事件</Button>} /> : null}
          <Form.Item name="reassigned_to" label="指定回标阅卷员" rules={[{ required: true, message: "请选择非原阅卷人" }]}><Select showSearch optionFilterProp="label" options={workflowActorOptions(backmarkReviewers)} placeholder="选择一名阅卷员" /></Form.Item>
          <Form.Item name="disposition" label="出现差异后的去向"><Select options={[{ value: "confirm", label: "记录差异，待人工确认" }, { value: "arbitrate", label: "达到阈值进入仲裁" }, { value: "regrade", label: "进入题目重评流程" }]} /></Form.Item>
          <Form.Item noStyle shouldUpdate={(previous, current) => previous.disposition !== current.disposition}>{({ getFieldValue }) => getFieldValue("disposition") === "arbitrate" ? <Form.Item name="arbitration_delta" label="仲裁分差阈值" rules={[{ required: true }]}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item> : null}</Form.Item>
          <Form.Item noStyle shouldUpdate>{({ getFieldValue }) => getFieldValue("reassigned_to") ? <Alert type="info" showIcon message="确认回标交接" description={`${questionLabels[getFieldValue("question_id")] || "题号待核对"}；接手教师：${workflowActorLabel(backmarkReviewers.find((user) => user.id === getFieldValue("reassigned_to")))}；分配范围以当前影响预览为准。`} /> : null}</Form.Item>
        </Form>
        <Space><Button onClick={() => setBackmarkOpen(false)}>取消</Button><Button type="primary" disabled={!backmarkPreview?.affected_count || !backmarkPreview.selector_hash} loading={backmarkLoading} onClick={() => void createBackmark()}>创建并分配回标</Button></Space>
        <BackmarkBatchHistory batches={backmarkBatches} questionLabels={questionLabels} nextCursor={backmarkBatchesNextCursor} loading={backmarkLoading} onLoadMore={() => void loadMoreBackmarkBatches()} onSummary={(batch) => void openBackmarkSummary(batch)} />
      </Drawer>
      <Drawer title="回标差异与复评交接" width={760} open={backmarkSummaryOpen} onClose={closeBackmarkSummary} extra={<Button loading={backmarkRegradeLoading} disabled={!selectedBackmarkBatch} onClick={() => selectedBackmarkBatch && void openBackmarkSummary(selectedBackmarkBatch, false)}>刷新</Button>}>
        <BackmarkSummaryContent summary={backmarkSummary} questionLabel={questionLabels[selectedBackmarkBatch?.question_id || ""] || "题号待核对"} error={backmarkSummaryError} releasesError={backmarkReleasesError} reviewersError={backmarkReviewersError} onRetry={() => selectedBackmarkBatch && void openBackmarkSummary(selectedBackmarkBatch, false)} nextCursor={backmarkSummaryNextCursor} canManage={canManage} loading={backmarkRegradeLoading} releases={backmarkRegradeReleases} reviewers={backmarkRegradeReviewers} releaseID={backmarkRegradeReleaseID} assignee={backmarkRegradeAssignee} preview={backmarkRegradePreview} onLoadMore={() => void loadMoreBackmarkSummary()} onReleaseChange={(value) => { summaryRequest.current += 1; setBackmarkRegradeReleaseID(value); setBackmarkRegradePreview(undefined); setBackmarkRegradeLoading(false); }} onAssigneeChange={(value) => { summaryRequest.current += 1; setBackmarkRegradeAssignee(value); setBackmarkRegradePreview(undefined); setBackmarkRegradeLoading(false); }} onPreview={() => void previewBackmarkRegrade()} onCreate={() => void createBackmarkRegrade()} />
      </Drawer>
      <Drawer title="阅卷质量事件" width={760} open={incidentsOpen} onClose={() => setIncidentsOpen(false)} extra={<Button icon={<RefreshCw size={15} />} loading={incidentsLoading} onClick={() => void loadIncidents()}>刷新</Button>}>
        <Alert type="info" showIcon message="仅展示汇总质量信号" description="不会展示标准卷答案、学生答卷或教师私密评分。可从事件创建独立回标，或在处置完成后关闭事件。" />
        <Table className="dense-data-table" size="small" rowKey="id" loading={incidentsLoading} pagination={false} dataSource={incidents} columns={[
          { title: "题目", dataIndex: "question_id", render: (value: string) => data.questions.find(item => item.question.id === value)?.question.question_no ?? "题号暂不可读" },
          { title: "类型", dataIndex: "type", render: (value: string) => <Tag>{incidentTypeLabel(value)}</Tag> },
          { title: "阅卷教师", dataIndex: "grader_id", render: (value: string) => { const teacher = backmarkReviewers.find(item => item.id === value); return teacher ? `${teacher.display_name || teacher.username}（账号：${teacher.username}）` : "教师姓名暂不可读"; } },
          { title: "发生时间", dataIndex: "created_at", render: incidentTime },
          { title: "级别", dataIndex: "severity", render: (value: string) => <Tag color={value === "critical" ? "error" : "warning"}>{value === "critical" ? "严重" : "警告"}</Tag> },
          { title: "样本", dataIndex: "metric_snapshot", render: (value: Record<string, unknown>) => typeof value.sample_count === "number" ? `${value.sample_count} 份` : "-" },
          { title: "操作", key: "actions", render: (_, incident: GradingQualityIncident) => <Space><Button type="link" size="small" onClick={() => startBackmarkFromIncident(incident)}>创建回标</Button><Button type="link" size="small" onClick={() => void resolveIncident(incident.id)}>关闭</Button></Space> }
        ]} locale={{ emptyText: "当前没有开放的质量事件" }} />
      </Drawer>
    </section>
  );
}
