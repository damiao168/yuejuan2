import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Divider,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  List,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  type TableColumnsType
} from "antd";
import { CheckCircle2, ClipboardCheck, FlaskConical, RefreshCw, ShieldAlert, SlidersHorizontal } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../../api/client";
import {
  addGradingEvaluationObservation,
  addModelCalibrationEvidence,
  approveModelCalibration,
  classifyAIHumanDisagreement,
  completeGradingEvaluation,
  completeModelCalibration,
  createGradingEvaluation,
  createModelCalibration,
  getAIEligibilityDecision,
  getAIEligibilityPolicy,
  getGradingEvaluationQualitySummary,
  invalidateGradingEvaluation,
  invalidateModelCalibration,
  listAIHumanDisagreements,
  listGradingEvaluationResponseDifficulty,
  listGradingEvaluationSliceMetrics,
  listGradingEvaluations,
  listModelCalibrationEvidence,
  listModelCalibrations,
  putAIEligibilityPolicy,
  routeAIHumanDisagreement,
  type AddGradingEvaluationObservationRequest,
  type AddModelCalibrationEvidenceRequest,
  type AIEligibilityDecision,
  type AIEligibilityPolicy,
  type AIHumanDisagreement,
  type AIHumanDisagreementTaxonomy,
  type CreateGradingEvaluationRequest,
  type CreateModelCalibrationRequest,
  type EligibilityAxis,
  type GradingEvaluationResponseDifficulty,
  type GradingEvaluationQualitySummary,
  type GradingEvaluationRun,
  type GradingEvaluationSliceMetric,
  type ModelCalibration,
  type ModelCalibrationEvidence,
  type PutAIEligibilityPolicyRequest
} from "../../api/scoringAssurance";

const subjectOptions = [
  ["chinese", "语文"], ["mathematics", "数学"], ["english", "英语"], ["physics", "物理"], ["chemistry", "化学"],
  ["biology", "生物"], ["history", "历史"], ["geography", "地理"], ["ethics_politics", "道德与法治 / 思想政治"]
].map(([value, label]) => ({ value, label }));

const archetypeOptions = [
  ["selected_response", "选择题"], ["exact_text", "精确文本"], ["numeric_expression", "数值表达式"], ["structured_steps", "步骤题"],
  ["short_constructed", "简答题"], ["extended_response", "长作答"], ["diagram_graph", "图表题"], ["table_experiment", "表格/实验题"]
].map(([value, label]) => ({ value, label }));

const scoringModeOptions = [
  ["RULE_AUTO", "规则自动"], ["AI_ASSIST", "AI 建议"], ["AI_FAST_CONFIRM", "AI 快速确认"],
  ["HUMAN_PRIMARY", "人工主评"], ["DUAL_HUMAN", "双人阅卷"], ["MANUAL_ONLY", "仅人工"]
].map(([value, label]) => ({ value, label }));

const taxonomyOptions: Array<{ value: AIHumanDisagreementTaxonomy; label: string }> = [
  ["ai_scoring_error", "AI 评分错误"], ["human_scoring_error", "人工评分错误"], ["ocr_error", "文字识别错误"],
  ["parser_error", "解析错误"], ["rubric_ambiguity", "评分细则歧义"], ["reference_answer_issue", "参考答案问题"],
  ["question_issue", "题目问题"], ["insufficient_evidence", "证据不足"], ["acceptable_variation", "可接受差异"]
].map(([value, label]) => ({ value: value as AIHumanDisagreementTaxonomy, label }));

const errorSourceOptions = [
  ["none", "无错误"], ["image_quality", "图像质量"], ["page_matching", "页面匹配"], ["answer_crop", "答题区域裁切"],
  ["handwriting_ocr", "手写识别"], ["formula_recognition", "公式识别"], ["answer_structuring", "答案结构化"],
  ["rubric", "Rubric"], ["model_scoring", "模型评分"], ["score_calculation", "分值计算"], ["system", "系统错误"],
  ["unattributed", "待归因"]
].map(([value, label]) => ({ value, label }));

const defaultAxis: EligibilityAxis = {
  subject_code: "mathematics",
  education_stage: "junior",
  archetype_code: "numeric_expression",
  risk_tier: "R2"
};

const defaultPolicy = (axis: EligibilityAxis): PutAIEligibilityPolicyRequest => ({
  ...axis,
  min_ocr_quality: 0.9,
  min_parser_quality: 0.9,
  min_eval_n: 30,
  max_severe_error_rate: 0.02,
  allowed_modes: ["RULE_AUTO", "AI_ASSIST"],
  status: "active",
  expected_version: 0
});

function asErrorMessage(error: unknown) {
  return getUserErrorMessage(error, "请求失败，请稍后重试");
}

function percentage(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function optionalPercentage(value?: number) {
  return value === undefined ? "未采集" : percentage(value);
}

function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function isNotFound(error: unknown) {
  return error instanceof ApiClientError && error.status === 404;
}

type PolicyForm = PutAIEligibilityPolicyRequest;
type EvaluationForm = CreateGradingEvaluationRequest;
type ObservationForm = AddGradingEvaluationObservationRequest;
type CalibrationForm = CreateModelCalibrationRequest;

export function ScoringAssuranceWorkspace({
  canReadEligibility,
  canManageEligibility,
  canManageEvaluations,
  canReadDisagreements,
  canManageDisagreements
}: {
  canReadEligibility: boolean;
  canManageEligibility: boolean;
  canManageEvaluations: boolean;
  canReadDisagreements: boolean;
  canManageDisagreements: boolean;
}) {
  const { message } = App.useApp();
  const [axis, setAxis] = useState<EligibilityAxis>(defaultAxis);
  const [policy, setPolicy] = useState<AIEligibilityPolicy>();
  const [policyLoading, setPolicyLoading] = useState(false);
  const [policyError, setPolicyError] = useState<string>();
  const [policyDrawerOpen, setPolicyDrawerOpen] = useState(false);
  const [policySaving, setPolicySaving] = useState(false);
  const [policyForm] = Form.useForm<PolicyForm>();

  const [evaluationRuns, setEvaluationRuns] = useState<GradingEvaluationRun[]>([]);
  const [calibrations, setCalibrations] = useState<ModelCalibration[]>([]);
  const [assuranceLoading, setAssuranceLoading] = useState(false);
  const [assuranceError, setAssuranceError] = useState<string>();
  const [evaluationDrawerOpen, setEvaluationDrawerOpen] = useState(false);
  const [observationDrawerOpen, setObservationDrawerOpen] = useState(false);
  const [calibrationDrawerOpen, setCalibrationDrawerOpen] = useState(false);
  const [actioning, setActioning] = useState(false);
  const [selectedEvaluationID, setSelectedEvaluationID] = useState<string>();
  const [selectedCalibrationID, setSelectedCalibrationID] = useState<string>();
  const [sliceMetrics, setSliceMetrics] = useState<GradingEvaluationSliceMetric[]>([]);
  const [responseDifficulty, setResponseDifficulty] = useState<GradingEvaluationResponseDifficulty[]>([]);
  const [qualitySummary, setQualitySummary] = useState<GradingEvaluationQualitySummary>();
  const [calibrationEvidence, setCalibrationEvidence] = useState<ModelCalibrationEvidence[]>([]);
  const [evaluationForm] = Form.useForm<EvaluationForm>();
  const [observationForm] = Form.useForm<ObservationForm>();
  const [calibrationForm] = Form.useForm<CalibrationForm>();
  const [evidenceForm] = Form.useForm<AddModelCalibrationEvidenceRequest>();

  const [disagreements, setDisagreements] = useState<AIHumanDisagreement[]>([]);
  const [disagreementLoading, setDisagreementLoading] = useState(false);
  const [disagreementError, setDisagreementError] = useState<string>();
  const [selectedDisagreement, setSelectedDisagreement] = useState<AIHumanDisagreement>();
  const [classificationDrawerOpen, setClassificationDrawerOpen] = useState(false);
  const [routeDrawerOpen, setRouteDrawerOpen] = useState(false);
  const [classificationForm] = Form.useForm<{ taxonomy: AIHumanDisagreementTaxonomy; notes?: string }>();
  const [routeForm] = Form.useForm<{ review_task_id: string }>();

  const loadPolicy = useCallback(async () => {
    if (!canReadEligibility) return;
    setPolicyLoading(true);
    setPolicyError(undefined);
    try {
      const response = await getAIEligibilityPolicy(axis);
      setPolicy(response.policy);
    } catch (error) {
      if (isNotFound(error)) {
        setPolicy(undefined);
        setPolicyError("该学科、学段、题型与风险级别尚未配置准入策略；评分链路会保守转人工。");
      } else {
        setPolicyError(asErrorMessage(error));
      }
    } finally {
      setPolicyLoading(false);
    }
  }, [axis, canReadEligibility]);

  const loadAssurance = useCallback(async () => {
    if (!canManageEvaluations) return;
    setAssuranceLoading(true);
    setAssuranceError(undefined);
    try {
      const [evaluationResponse, calibrationResponse] = await Promise.all([listGradingEvaluations(), listModelCalibrations()]);
      setEvaluationRuns(evaluationResponse.evaluation_runs);
      setCalibrations(calibrationResponse.calibrations);
      setSelectedEvaluationID((current) => current && evaluationResponse.evaluation_runs.some((item) => item.id === current) ? current : evaluationResponse.evaluation_runs[0]?.id);
      setSelectedCalibrationID((current) => current && calibrationResponse.calibrations.some((item) => item.id === current) ? current : calibrationResponse.calibrations[0]?.id);
    } catch (error) {
      setAssuranceError(asErrorMessage(error));
    } finally {
      setAssuranceLoading(false);
    }
  }, [canManageEvaluations]);

  const loadDisagreements = useCallback(async () => {
    if (!canReadDisagreements) return;
    setDisagreementLoading(true);
    setDisagreementError(undefined);
    try {
      const response = await listAIHumanDisagreements({ limit: 100 });
      setDisagreements(response.disagreements);
    } catch (error) {
      setDisagreementError(asErrorMessage(error));
    } finally {
      setDisagreementLoading(false);
    }
  }, [canReadDisagreements]);

  useEffect(() => { void loadPolicy(); }, [loadPolicy]);
  useEffect(() => { void loadAssurance(); }, [loadAssurance]);
  useEffect(() => { void loadDisagreements(); }, [loadDisagreements]);

  const selectedEvaluation = evaluationRuns.find((item) => item.id === selectedEvaluationID);
  const selectedCalibration = calibrations.find((item) => item.id === selectedCalibrationID);

  useEffect(() => {
    if (!selectedEvaluationID || !canManageEvaluations) {
      setSliceMetrics([]);
      setResponseDifficulty([]);
      setQualitySummary(undefined);
      return;
    }
    let active = true;
    Promise.all([listGradingEvaluationSliceMetrics(selectedEvaluationID), listGradingEvaluationResponseDifficulty(selectedEvaluationID), getGradingEvaluationQualitySummary(selectedEvaluationID)])
      .then(([slices, difficulty, quality]) => {
        if (!active) return;
        setSliceMetrics(slices.slice_metrics);
        setResponseDifficulty(difficulty.response_difficulty);
        setQualitySummary(quality.quality_summary);
      })
      .catch((error: unknown) => { if (active) message.error(asErrorMessage(error)); });
    return () => { active = false; };
  }, [canManageEvaluations, message, selectedEvaluationID]);

  useEffect(() => {
    if (!selectedCalibrationID || !canManageEvaluations) {
      setCalibrationEvidence([]);
      return;
    }
    let active = true;
    listModelCalibrationEvidence(selectedCalibrationID)
      .then((response) => { if (active) setCalibrationEvidence(response.evidence); })
      .catch((error: unknown) => { if (active) message.error(asErrorMessage(error)); });
    return () => { active = false; };
  }, [canManageEvaluations, message, selectedCalibrationID]);

  const openPolicyEditor = () => {
    policyForm.setFieldsValue(policy ? {
      subject_code: policy.subject_code,
      education_stage: policy.education_stage,
      archetype_code: policy.archetype_code,
      risk_tier: policy.risk_tier,
      min_ocr_quality: policy.min_ocr_quality,
      min_parser_quality: policy.min_parser_quality,
      min_eval_n: policy.min_eval_n,
      max_severe_error_rate: policy.max_severe_error_rate,
      allowed_modes: policy.allowed_modes,
      status: policy.status,
      expected_version: policy.version
    } : defaultPolicy(axis));
    setPolicyDrawerOpen(true);
  };

  const savePolicy = async () => {
    const values = await policyForm.validateFields();
    setPolicySaving(true);
    try {
      const response = await putAIEligibilityPolicy(values);
      setPolicy(response.policy);
      setAxis({
        subject_code: response.policy.subject_code,
        education_stage: response.policy.education_stage,
        archetype_code: response.policy.archetype_code,
        risk_tier: response.policy.risk_tier
      });
      setPolicyDrawerOpen(false);
      message.success("题型准入策略已保存为新版本；未满足条件的答卷仍会转人工。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setPolicySaving(false);
    }
  };

  const createEvaluation = async () => {
    const values = await evaluationForm.validateFields();
    setActioning(true);
    try {
      const response = await createGradingEvaluation(values);
      setEvaluationDrawerOpen(false);
      evaluationForm.resetFields();
      await loadAssurance();
      setSelectedEvaluationID(response.evaluation_run.id);
      message.success("离线评测已创建；请先录入对齐的标准卷或人工裁决观察，再冻结评测。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const addObservation = async () => {
    if (!selectedEvaluation) return;
    const values = await observationForm.validateFields();
    setActioning(true);
    try {
      await addGradingEvaluationObservation(selectedEvaluation.id, values);
      setObservationDrawerOpen(false);
      observationForm.resetFields();
      await loadAssurance();
      message.success("已记录脱敏的对齐观察，不会上传答题文本或原图。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const transitionEvaluation = async (kind: "complete" | "invalidate") => {
    if (!selectedEvaluation) return;
    let reason = "";
    if (kind === "invalidate") {
      reason = window.prompt("请说明失效原因（不会修改历史观察）：")?.trim() ?? "";
      if (!reason) return;
    }
    setActioning(true);
    try {
      if (kind === "complete") await completeGradingEvaluation(selectedEvaluation.id);
      else await invalidateGradingEvaluation(selectedEvaluation.id, reason);
      await loadAssurance();
      message.success(kind === "complete" ? "离线评测已冻结，可用于对应轴的准入证据。" : "离线评测已失效，之后不会再作为准入证据。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const createCalibration = async () => {
    const values = await calibrationForm.validateFields();
    setActioning(true);
    try {
      const response = await createModelCalibration(values);
      setCalibrationDrawerOpen(false);
      calibrationForm.resetFields();
      await loadAssurance();
      setSelectedCalibrationID(response.calibration.id);
      message.success("校准草稿已创建；需要补齐对齐观察的置信度后才可完成和批准。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const addCalibrationEvidence = async () => {
    if (!selectedCalibration) return;
    const values = await evidenceForm.validateFields();
    setActioning(true);
    try {
      await addModelCalibrationEvidence(selectedCalibration.id, values);
      evidenceForm.resetFields();
      const response = await listModelCalibrationEvidence(selectedCalibration.id);
      setCalibrationEvidence(response.evidence);
      await loadAssurance();
      message.success("置信度观察已加入校准草稿。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const transitionCalibration = async (kind: "complete" | "approve" | "invalidate") => {
    if (!selectedCalibration) return;
    let reason = "";
    if (kind === "invalidate") {
      reason = window.prompt("请说明失效原因：")?.trim() ?? "";
      if (!reason) return;
    }
    setActioning(true);
    try {
      if (kind === "complete") await completeModelCalibration(selectedCalibration.id);
      if (kind === "approve") await approveModelCalibration(selectedCalibration.id);
      if (kind === "invalidate") await invalidateModelCalibration(selectedCalibration.id, reason);
      await loadAssurance();
      message.success(kind === "complete" ? "校准工件已完成。" : kind === "approve" ? "校准工件已批准；仅精确版本轴可引用。" : "校准工件已失效。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const submitClassification = async () => {
    if (!selectedDisagreement) return;
    const values = await classificationForm.validateFields();
    setActioning(true);
    try {
      await classifyAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      setClassificationDrawerOpen(false);
      await loadDisagreements();
      message.success("已记录人工分类；它不会自动更改分数或认定责任。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const submitRoute = async () => {
    if (!selectedDisagreement) return;
    const values = await routeForm.validateFields();
    setActioning(true);
    try {
      await routeAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      setRouteDrawerOpen(false);
      await loadDisagreements();
      message.success("分歧已关联人工阅卷任务；不会创建或直接修改最终成绩。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false);
    }
  };

  const evaluationColumns: TableColumnsType<GradingEvaluationRun> = [
    { title: "评测", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.display_name}</strong><Typography.Text type="secondary">{item.key}</Typography.Text></Space> },
    { title: "模型 / Prompt / Rubric", render: (_value, item) => <Space direction="vertical" size={0}><span>{item.model_reference}</span><Typography.Text type="secondary">{item.prompt_version} · {item.rubric_version}</Typography.Text></Space> },
    { title: "对齐观察", dataIndex: "observation_count", width: 100 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={value === "completed" ? "success" : value === "invalidated" ? "default" : "processing"}>{value === "completed" ? "已冻结" : value === "invalidated" ? "已失效" : "采集中"}</Tag> },
    { title: "操作", width: 90, render: (_value, item) => <Button type={item.id === selectedEvaluationID ? "primary" : "link"} size="small" onClick={() => setSelectedEvaluationID(item.id)}>查看</Button> }
  ];

  const calibrationColumns: TableColumnsType<ModelCalibration> = [
    { title: "校准", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.key}</strong><Typography.Text type="secondary">{item.axis.subject} · {item.axis.archetype} · {item.axis.slice_key}</Typography.Text></Space> },
    { title: "版本轴", render: (_value, item) => <Typography.Text>{item.axis.model_reference}<br />{item.axis.prompt_version} · {item.axis.rubric_version}</Typography.Text> },
    { title: "样本", dataIndex: "calibration_n", width: 76 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={value === "approved" ? "success" : value === "invalidated" ? "default" : value === "completed" ? "processing" : "warning"}>{value === "approved" ? "已批准" : value === "completed" ? "待批准" : value === "invalidated" ? "已失效" : "草稿"}</Tag> },
    { title: "操作", width: 90, render: (_value, item) => <Button type={item.id === selectedCalibrationID ? "primary" : "link"} size="small" onClick={() => setSelectedCalibrationID(item.id)}>查看</Button> }
  ];

  const severeCount = useMemo(() => disagreements.filter((item) => item.severity === "severe" && item.status === "needs_review").length, [disagreements]);
  const canSeeAny = canReadEligibility || canManageEvaluations || canReadDisagreements;

  if (!canSeeAny) {
    return <Alert showIcon type="info" message="当前账号没有评分保障查看权限" description="题型准入、离线评测、校准和人机分歧分别由模型治理与阅卷质量权限控制。" />;
  }

  return (
    <div className="model-assurance-workspace">
      <Alert
        showIcon
        type="info"
        icon={<ShieldAlert size={17} />}
        message="评分保障不会授予模型最终分数权"
        description="准入策略、离线评测、置信度校准与人机分歧均是可追溯的安全证据。未满足任何一项时，系统必须转入人工流程。"
      />

      {canReadEligibility ? <Card size="small" title={<Space><SlidersHorizontal size={17} />题型准入策略</Space>} extra={<Space><Button icon={<RefreshCw size={15} />} loading={policyLoading} onClick={() => void loadPolicy()}>查询</Button>{canManageEligibility ? <Button type="primary" onClick={openPolicyEditor}>维护策略</Button> : null}</Space>}>
        <Space wrap className="model-assurance-axis">
          <Select aria-label="学科" value={axis.subject_code} options={subjectOptions} onChange={(subject_code) => setAxis((current) => ({ ...current, subject_code }))} />
          <Select aria-label="学段" value={axis.education_stage} options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} onChange={(education_stage) => setAxis((current) => ({ ...current, education_stage }))} />
          <Select aria-label="题型" value={axis.archetype_code} options={archetypeOptions} onChange={(archetype_code) => setAxis((current) => ({ ...current, archetype_code }))} />
          <Select aria-label="风险级别" value={axis.risk_tier} options={[{ value: "R1", label: "R1" }, { value: "R2", label: "R2" }, { value: "R3", label: "R3" }]} onChange={(risk_tier) => setAxis((current) => ({ ...current, risk_tier }))} />
        </Space>
        <Divider />
        <Spin spinning={policyLoading}>
          {policy ? <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} items={[
            { key: "version", label: "策略版本", children: `V${policy.version}` },
            { key: "quality", label: "最低质量", children: `OCR ${percentage(policy.min_ocr_quality)} · 解析 ${percentage(policy.min_parser_quality)}` },
            { key: "evaluation", label: "评测门槛", children: `${policy.min_eval_n} 条 · 严重误差 ≤ ${percentage(policy.max_severe_error_rate)}` },
            { key: "modes", label: "允许模式", children: policy.allowed_modes.join(" · ") }
          ]} /> : <Typography.Text type={policyError ? "warning" : "secondary"}>{policyError || "请选择轴后查询当前策略。"}</Typography.Text>}
        </Spin>
        <DecisionLookup canRead={canReadEligibility} />
      </Card> : null}

      {canManageEvaluations ? <Card size="small" title={<Space><FlaskConical size={17} />离线评测与置信度校准</Space>} extra={<Space><Button icon={<RefreshCw size={15} />} loading={assuranceLoading} onClick={() => void loadAssurance()}>刷新</Button><Button type="primary" onClick={() => setEvaluationDrawerOpen(true)}>新建评测</Button></Space>}>
        {assuranceError ? <Alert showIcon type="error" message="加载评分保障证据失败" description={assuranceError} action={<Button onClick={() => void loadAssurance()}>重试</Button>} /> : null}
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={evaluationColumns} dataSource={evaluationRuns} locale={{ emptyText: <Empty description="尚无离线评测" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} />
        {selectedEvaluation ? <section className="model-assurance-detail">
          <Divider orientation="left">{selectedEvaluation.display_name}</Divider>
          <Space wrap>
            {selectedEvaluation.status === "draft" ? <><Button onClick={() => setObservationDrawerOpen(true)}>录入对齐观察</Button><Button type="primary" loading={actioning} disabled={selectedEvaluation.observation_count === 0} onClick={() => void transitionEvaluation("complete")}>冻结评测</Button></> : null}
            {selectedEvaluation.status !== "invalidated" ? <Button danger loading={actioning} onClick={() => void transitionEvaluation("invalidate")}>标记失效</Button> : null}
            <Button onClick={() => {
              calibrationForm.setFieldsValue({ evaluation_run_id: selectedEvaluation.id, method: "auto", axis: { model_reference: selectedEvaluation.model_reference, prompt_version: selectedEvaluation.prompt_version, rubric_version: selectedEvaluation.rubric_version, subject: "mathematics", archetype: "short_constructed", slice_key: "all" } });
              setCalibrationDrawerOpen(true);
            }} disabled={selectedEvaluation.status !== "completed"}>基于评测创建校准</Button>
          </Space>
          <div className="model-assurance-metrics">
            <section><h4>全链路质量</h4>{qualitySummary ? <><Descriptions size="small" column={1} items={[
              { key: "page", label: "页面匹配", children: `${optionalPercentage(qualitySummary.page_match_accuracy.rate)} · n=${qualitySummary.page_match_accuracy.observed_count}` },
              { key: "crop", label: "平均 Crop IoU", children: `${optionalPercentage(qualitySummary.mean_crop_iou.mean)} · n=${qualitySummary.mean_crop_iou.observed_count}` },
              { key: "cer", label: "平均转录 CER", children: `${optionalPercentage(qualitySummary.mean_transcription_cer.mean)} · n=${qualitySummary.mean_transcription_cer.observed_count}` },
              { key: "formula", label: "公式精确率", children: `${optionalPercentage(qualitySummary.formula_exact_rate.rate)} · n=${qualitySummary.formula_exact_rate.observed_count}` },
              { key: "rubric", label: "Rubric 评分点一致", children: `${optionalPercentage(qualitySummary.mean_rubric_criterion_agreement.mean)} · n=${qualitySummary.mean_rubric_criterion_agreement.observed_count}` },
              { key: "routing", label: "错误转人工召回", children: optionalPercentage(qualitySummary.risky_error_routing_recall) }
            ]} />{qualitySummary.error_attribution.length ? <List size="small" dataSource={qualitySummary.error_attribution} renderItem={(item) => <List.Item><span>{errorSourceOptions.find((option) => option.value === item.source)?.label ?? "其他来源"}</span><Space size={4}><Tag>n={item.count}</Tag><Tag color={item.human_routing_recall < 1 ? "error" : "success"}>转人工 {percentage(item.human_routing_recall)}</Tag></Space></List.Item>} /> : <Typography.Text type="secondary">尚未发现或归因错误。</Typography.Text>}</> : <Typography.Text type="secondary">录入观察后显示图像、裁切、转录、公式、Rubric 与路由指标。</Typography.Text>}</section>
            <section><h4>切片指标</h4>{sliceMetrics.length ? <List size="small" dataSource={sliceMetrics.slice(0, 12)} renderItem={(item) => <List.Item><span>{item.dimension}: {item.value}</span><Space size={4}><Tag>n={item.metrics.sample_count}</Tag><Tag color={item.metrics.severe_error_rate > 0 ? "error" : "success"}>严重误差 {percentage(item.metrics.severe_error_rate)}</Tag></Space></List.Item>} /> : <Typography.Text type="secondary">完成评测后生成按题型、分段和 OCR 质量的切片。</Typography.Text>}</section>
            <section><h4>困难答卷</h4>{responseDifficulty.length ? <List size="small" dataSource={responseDifficulty.slice(0, 8)} renderItem={(item) => <List.Item><span>{item.difficulty_band} · {shortID(item.response_key)}</span><Tag color={item.severe_error ? "error" : "default"}>归一化误差 {percentage(item.normalized_error)}</Tag></List.Item>} /> : <Typography.Text type="secondary">完成评测后显示经验性难例，不代表学生能力。</Typography.Text>}</section>
          </div>
        </section> : null}
        <Divider orientation="left">校准工件</Divider>
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={calibrationColumns} dataSource={calibrations} locale={{ emptyText: "尚无校准工件" }} />
        {selectedCalibration ? <section className="model-assurance-detail">
          <Space wrap><Typography.Text>方法：{selectedCalibration.method}</Typography.Text><Typography.Text>证据：{calibrationEvidence.length} 条</Typography.Text><Typography.Text>工件：{selectedCalibration.artifact_sha256 ? shortID(selectedCalibration.artifact_sha256) : "尚未生成"}</Typography.Text></Space>
          <Space wrap style={{ marginTop: 12 }}>
            {selectedCalibration.status === "draft" ? <><Button onClick={() => evidenceForm.resetFields()} disabled={actioning}>添加置信度证据</Button><Button type="primary" loading={actioning} disabled={calibrationEvidence.length === 0} onClick={() => void transitionCalibration("complete")}>完成校准</Button></> : null}
            {selectedCalibration.status === "completed" ? <Button type="primary" icon={<CheckCircle2 size={15} />} loading={actioning} onClick={() => void transitionCalibration("approve")}>批准校准</Button> : null}
            {selectedCalibration.status !== "invalidated" ? <Button danger loading={actioning} onClick={() => void transitionCalibration("invalidate")}>标记失效</Button> : null}
          </Space>
          {selectedCalibration.status === "draft" ? <Form form={evidenceForm} layout="inline" className="model-assurance-inline-form" onFinish={() => void addCalibrationEvidence()}>
            <Form.Item name="response_key" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="脱敏答卷键" /></Form.Item>
            <Form.Item name="raw_confidence" rules={[{ required: true }]}><InputNumber min={0} max={1} step={0.01} placeholder="模型置信度" /></Form.Item>
            <Button htmlType="submit" loading={actioning}>加入证据</Button>
          </Form> : null}
        </section> : null}
      </Card> : null}

      {canReadDisagreements ? <Card size="small" title={<Space><ClipboardCheck size={17} />人机分歧队列</Space>} extra={<Space><Tag color={severeCount > 0 ? "error" : "success"}>待审阅严重分歧 {severeCount}</Tag><Button icon={<RefreshCw size={15} />} loading={disagreementLoading} onClick={() => void loadDisagreements()}>刷新</Button></Space>}>
        {disagreementError ? <Alert showIcon type="error" message="加载人机分歧失败" description={disagreementError} action={<Button onClick={() => void loadDisagreements()}>重试</Button>} /> : null}
        <List
          loading={disagreementLoading}
          dataSource={disagreements}
          locale={{ emptyText: "暂无需要处理的人机分歧" }}
          renderItem={(item) => <List.Item actions={canManageDisagreements ? [<Button key="classify" size="small" onClick={() => { setSelectedDisagreement(item); classificationForm.setFieldsValue({ taxonomy: item.taxonomy ?? "ai_scoring_error", notes: item.notes }); setClassificationDrawerOpen(true); }}>分类</Button>, <Button key="route" size="small" onClick={() => { setSelectedDisagreement(item); routeForm.resetFields(); setRouteDrawerOpen(true); }}>关联人工任务</Button>] : undefined}>
            <List.Item.Meta title={<Space><strong>题目 {shortID(item.question_id)}</strong><Tag color={item.severity === "severe" ? "error" : "warning"}>{item.severity === "severe" ? "严重" : "需关注"}</Tag><Tag>{item.status === "needs_review" ? "待审阅" : item.status === "classified" ? "已分类" : "已关联任务"}</Tag></Space>} description={<Space wrap><span>分差 {item.delta.toFixed(2)} / 满分 {item.max_score}</span><span>风险 {item.risk_tier}</span><span>{item.taxonomy ? taxonomyOptions.find((option) => option.value === item.taxonomy)?.label : "尚未分类"}</span></Space>} />
          </List.Item>}
        />
      </Card> : null}

      <Drawer title="维护题型准入策略" width={560} open={policyDrawerOpen} onClose={() => setPolicyDrawerOpen(false)} extra={<Button type="primary" loading={policySaving} onClick={() => void savePolicy()}>保存新版本</Button>}>
        <Alert type="warning" showIcon message="此处只配置是否允许调用 AI，不授予模型最终分数权。R3 长作答禁止配置为 AI 快速确认或规则自动。" />
        <Form form={policyForm} layout="vertical" style={{ marginTop: 16 }}>
          <Space.Compact block><Form.Item name="subject_code" label="学科" rules={[{ required: true }]} style={{ width: "25%" }}><Select options={subjectOptions} /></Form.Item><Form.Item name="education_stage" label="学段" rules={[{ required: true }]} style={{ width: "25%" }}><Select options={[{ value: "junior", label: "初中" }, { value: "senior", label: "高中" }]} /></Form.Item><Form.Item name="risk_tier" label="风险" rules={[{ required: true }]} style={{ width: "20%" }}><Select options={[{ value: "R1", label: "R1" }, { value: "R2", label: "R2" }, { value: "R3", label: "R3" }]} /></Form.Item></Space.Compact>
          <Form.Item name="archetype_code" label="题型" rules={[{ required: true }]}><Select options={archetypeOptions} /></Form.Item>
          <Space.Compact block><Form.Item name="min_ocr_quality" label="最低 OCR 质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_parser_quality" label="最低解析质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_eval_n" label="最低评测样本" rules={[{ required: true }]} style={{ width: "34%" }}><InputNumber min={1} precision={0} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Form.Item name="max_severe_error_rate" label="最大严重误差率" rules={[{ required: true }]}><InputNumber min={0} max={1} step={0.005} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="allowed_modes" label="允许评分模式" rules={[{ required: true }]}><Select mode="multiple" options={scoringModeOptions} /></Form.Item>
          <Form.Item name="status" label="状态" rules={[{ required: true }]}><Select options={[{ value: "active", label: "启用" }, { value: "disabled", label: "停用" }]} /></Form.Item>
          <Form.Item name="expected_version" hidden><InputNumber /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="新建离线评分评测" width={620} open={evaluationDrawerOpen} onClose={() => setEvaluationDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void createEvaluation()}>创建</Button>}>
        <Alert type="info" showIcon message="仅登记已授权的脱敏数据集引用和版本轴" description="评测不会在浏览器中调用模型，也不接收学生答案、原图或标准卷解析。" />
        <Form form={evaluationForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="key" label="评测键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="math-r2-v1" /></Form.Item>
          <Form.Item name="display_name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="model_reference" label="模型版本" rules={[{ required: true }]}><Input /></Form.Item>
          <Space.Compact block><Form.Item name="prompt_version" label="提示词版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name="rubric_version" label="评分细则版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Form.Item name="dataset_reference" label="脱敏数据集引用" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input /></Form.Item>
          <Form.Item name="dataset_sha256" label="数据集 SHA-256" rules={[{ required: true, pattern: /^[a-f0-9]{64}$/ }]}><Input /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="录入对齐观察" width={650} open={observationDrawerOpen} onClose={() => setObservationDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void addObservation()}>保存观察</Button>}>
        <Alert type="info" showIcon message="只记录脱敏键、指纹、切片标签与对齐分数" description="系统不接收答题文本、图片或标准卷解析内容。" />
        <Form form={observationForm} layout="vertical" style={{ marginTop: 16 }} initialValues={{ reference_kind: "gold", ocr_quality: "high", answer_length: "medium", rubric_complexity: "medium", error_source: "none", needs_human_review: false, reference_reviewer_count: 1, reference_adjudicated: false }}>
          <Space.Compact block><Form.Item name="response_key" label="脱敏答卷键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name="response_fingerprint" label="答卷指纹 SHA-256" rules={[{ required: true, pattern: /^[a-f0-9]{64}$/ }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="reference_kind" label="参考来源" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "gold", label: "标准卷" }, { value: "human_adjudicated", label: "双人裁决" }]} onChange={(value) => observationForm.setFieldsValue(value === "human_adjudicated" ? { reference_reviewer_count: 2, reference_adjudicated: true } : { reference_reviewer_count: 1, reference_adjudicated: false })} /></Form.Item><Form.Item name="subject" label="学科切片" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "33%" }}><Input placeholder="mathematics" /></Form.Item><Form.Item name="archetype" label="题型切片" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "34%" }}><Input placeholder="structured_steps" /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="ocr_quality" label="OCR 质量" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "high", label: "高" }, { value: "medium", label: "中" }, { value: "low", label: "低" }, { value: "unknown", label: "未知" }]} /></Form.Item><Form.Item name="answer_length" label="答案长度" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "short", label: "短" }, { value: "medium", label: "中" }, { value: "long", label: "长" }, { value: "unknown", label: "未知" }]} /></Form.Item><Form.Item name="rubric_complexity" label="细则复杂度" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={[{ value: "low", label: "低" }, { value: "medium", label: "中" }, { value: "high", label: "高" }, { value: "unknown", label: "未知" }]} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="reference_score" label="参考分" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="model_score" label="模型分" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="max_score" label="满分" rules={[{ required: true }]} style={{ width: "34%" }}><InputNumber min={0.1} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Divider orientation="left">链路证据（按实际采集填写）</Divider>
          <Space.Compact block><Form.Item name="page_match_correct" label="页面匹配正确" style={{ width: "33%" }}><Select allowClear options={[{ value: true, label: "正确" }, { value: false, label: "错误" }]} /></Form.Item><Form.Item name="crop_iou" label="Crop IoU" style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="transcription_cer" label="转录 CER" style={{ width: "34%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="formula_exact" label="公式精确识别" style={{ width: "33%" }}><Select allowClear options={[{ value: true, label: "精确" }, { value: false, label: "错误" }]} /></Form.Item><Form.Item name="rubric_criterion_agreement" label="Rubric 评分点一致率" style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="error_source" label="首要错误来源" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={errorSourceOptions} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="needs_human_review" label="已路由人工" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: false, label: "否" }, { value: true, label: "是" }]} /></Form.Item><Form.Item name="reference_reviewer_count" label="真值复核人数" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={1} precision={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="reference_adjudicated" label="已完成裁决" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={[{ value: false, label: "否" }, { value: true, label: "是" }]} /></Form.Item></Space.Compact>
        </Form>
      </Drawer>

      <Drawer title="从已冻结评测创建校准" width={620} open={calibrationDrawerOpen} onClose={() => setCalibrationDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void createCalibration()}>创建草稿</Button>}>
        <Form form={calibrationForm} layout="vertical">
          <Form.Item name="key" label="校准键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="math-r2-confidence-v1" /></Form.Item>
          <Form.Item name="evaluation_run_id" label="已冻结评测" rules={[{ required: true }]}><Select options={evaluationRuns.filter((item) => item.status === "completed").map((item) => ({ value: item.id, label: `${item.display_name} · ${item.model_reference}` }))} /></Form.Item>
          <Form.Item name={["axis", "model_reference"]} label="模型版本" rules={[{ required: true }]}><Input /></Form.Item>
          <Space.Compact block><Form.Item name={["axis", "prompt_version"]} label="提示词版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name={["axis", "rubric_version"]} label="评分细则版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name={["axis", "subject"]} label="学科" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "34%" }}><Input /></Form.Item><Form.Item name={["axis", "archetype"]} label="题型" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "33%" }}><Input /></Form.Item><Form.Item name={["axis", "slice_key"]} label="切片" rules={[{ required: true }]} style={{ width: "33%" }}><Input placeholder="all" /></Form.Item></Space.Compact>
          <Form.Item name="method" label="校准方法" rules={[{ required: true }]}><Select options={["auto", "isotonic", "logistic", "conformal"].map((value) => ({ value, label: value }))} /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="人工分类人机分歧" width={520} open={classificationDrawerOpen} onClose={() => setClassificationDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void submitClassification()}>保存分类</Button>}>
        <Alert showIcon type="info" message="分类仅记录诊断结论" description="不会自动更改学生成绩、重试 AI 或认定任何一方责任。" />
        <Form form={classificationForm} layout="vertical" style={{ marginTop: 16 }}><Form.Item name="taxonomy" label="根因分类" rules={[{ required: true }]}><Select options={taxonomyOptions} /></Form.Item><Form.Item name="notes" label="内部说明（可选）"><Input.TextArea rows={5} maxLength={2000} /></Form.Item></Form>
      </Drawer>

      <Drawer title="关联人工阅卷任务" width={520} open={routeDrawerOpen} onClose={() => setRouteDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void submitRoute()}>关联任务</Button>}>
        <Alert showIcon type="warning" message="只能关联现有人工任务" description="该操作不会新建任务、不会直接重评，也不会修改最终成绩。需要更正时请进入正式重评流程。" />
        <Form form={routeForm} layout="vertical" style={{ marginTop: 16 }}><Form.Item name="review_task_id" label="人工阅卷任务编号" rules={[{ required: true }]}><Input /></Form.Item></Form>
      </Drawer>
    </div>
  );
}

function DecisionLookup({ canRead }: { canRead: boolean }) {
  const { message } = App.useApp();
  const [runItemID, setRunItemID] = useState("");
  const [loading, setLoading] = useState(false);
  const [decision, setDecision] = useState<AIEligibilityDecision>();

  const load = async () => {
    if (!canRead || !runItemID.trim()) return;
    setLoading(true);
    try {
      const response = await getAIEligibilityDecision(runItemID.trim());
      setDecision(response.decision);
    } catch (error) {
      setDecision(undefined);
      message.error(asErrorMessage(error));
    } finally {
      setLoading(false);
    }
  };

  return <section className="model-assurance-decision"><Divider orientation="left">运行准入审计</Divider><Space.Compact style={{ maxWidth: 560, width: "100%" }}><Input value={runItemID} onChange={(event) => setRunItemID(event.target.value)} placeholder="输入 AI 评分运行项编号" /><Button loading={loading} onClick={() => void load()} disabled={!runItemID.trim()}>查看决策</Button></Space.Compact>{decision ? <Descriptions size="small" column={2} style={{ marginTop: 12 }} items={[{ key: "decision", label: "评分方式", children: scoringModeOptions.find((option) => option.value === decision.decision)?.label ?? "未知方式" }, { key: "external", label: "外部调用", children: decision.external_ai_allowed ? <Tag color="success">允许</Tag> : <Tag color="warning">禁止</Tag> }, { key: "reason", label: "原因", span: 2, children: decision.reasons.map((item) => getSafeUserText(item.message, "运行条件未满足")).join("；") || "无额外阻断原因" }]} /> : null}</section>;
}
