import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  Select,
  Space,
  Table,
  Tag,
  Typography,
  type TableColumnsType
} from "antd";
import { FlaskConical, RefreshCw, ShieldAlert } from "lucide-react";
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
  setScoringAssuranceSchool,
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
import { CalibrationDetail, DisagreementQueue, EligibilityPolicyPanel, EvaluationDetail } from "./ScoringAssuranceViews";
import { downloadGovernanceMaterial, observationExample } from "./governanceMaterials";
import {
  assuranceCalibrationStatusColor, assuranceCalibrationStatusLabel, assuranceCalibrationTransitionMessage,
  assuranceErrorSourceOptions as errorSourceOptions, assuranceEvaluationStatusColor, assuranceEvaluationStatusLabel
} from "./scoringAssurancePresentation";
import {
  eligibilitySubjects as subjectOptions, eligibilityArchetypes as archetypeOptions, eligibilityStages, eligibilityRisks,
  eligibilityScopeLabel, isCompleteEligibilitySelection, type EligibilitySelection
} from "./eligibilityScope";

const scoringModeOptions = [
  ["RULE_AUTO", "规则自动"], ["AI_ASSIST", "AI 建议"], ["AI_FAST_CONFIRM", "AI 快速确认"],
  ["HUMAN_PRIMARY", "人工主评"], ["DUAL_HUMAN", "双人阅卷"], ["MANUAL_ONLY", "仅人工"]
].map(([value, label]) => ({ value, label }));

const taxonomyOptions: Array<{ value: AIHumanDisagreementTaxonomy; label: string }> = [
  ["ai_scoring_error", "AI 评分错误"], ["human_scoring_error", "人工评分错误"], ["ocr_error", "文字识别错误"],
  ["parser_error", "解析错误"], ["rubric_ambiguity", "评分细则歧义"], ["reference_answer_issue", "参考答案问题"],
  ["question_issue", "题目问题"], ["insufficient_evidence", "证据不足"], ["acceptable_variation", "可接受差异"]
].map(([value, label]) => ({ value: value as AIHumanDisagreementTaxonomy, label }));

const defaultPolicy = (axis: EligibilityAxis): PutAIEligibilityPolicyRequest => ({
  ...axis,
  min_ocr_quality: 0.9,
  min_parser_quality: 0.9,
  min_eval_n: 30,
  max_severe_error_rate: 0.02,
  allowed_modes: axis.risk_tier === "R3" && axis.archetype_code === "extended_response" ? ["AI_ASSIST", "HUMAN_PRIMARY"] : ["RULE_AUTO", "AI_ASSIST"],
  status: "active",
  expected_version: 0
});

function asErrorMessage(error: unknown) {
  return getUserErrorMessage(error, "请求失败，请稍后重试");
}

function isNotFound(error: unknown) {
  return error instanceof ApiClientError && error.status === 404;
}

function validateObservationRow(row: unknown, acceptedKeys: ReadonlySet<string>): AddGradingEvaluationObservationRequest {
  // 一行有多处错误时按原顺序报告首错；只校验并返回原对象，不登记键或写入观察。
  if (!row || typeof row !== "object") throw new Error("观察记录须为对象");
  const item = row as AddGradingEvaluationObservationRequest;
  if (!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(item.response_key ?? "") || acceptedKeys.has(item.response_key) || !/^[a-f0-9]{64}$/.test(item.response_fingerprint ?? "")) throw new Error("答卷键须唯一，指纹须为真实64位SHA256");
  if (![item.reference_score, item.model_score, item.max_score].every((value) => typeof value === "number" && Number.isFinite(value)) || item.max_score <= 0 || item.reference_score < 0 || item.model_score < 0 || item.reference_score > item.max_score || item.model_score > item.max_score) throw new Error("请核对参考分、模型分和满分");
  if (![item.subject, item.archetype].every((value) => typeof value === "string" && /^[a-z0-9][a-z0-9._-]{0,127}$/.test(value))) throw new Error("学科和题型须填写报告中的规范标识");
  if (!["gold", "human_adjudicated"].includes(item.reference_kind) || !["high", "medium", "low", "unknown"].includes(item.ocr_quality) || !["short", "medium", "long", "unknown"].includes(item.answer_length) || !["low", "medium", "high", "unknown"].includes(item.rubric_complexity)) throw new Error("请核对真值来源与切片标签");
  const reviewerCount = item.reference_reviewer_count;
  if (typeof item.needs_human_review !== "boolean" || typeof item.reference_adjudicated !== "boolean" || typeof reviewerCount !== "number" || !Number.isInteger(reviewerCount) || reviewerCount < 1 || (item.reference_kind === "human_adjudicated" && (reviewerCount < 2 || !item.reference_adjudicated))) throw new Error("人工裁决真值须记录至少两位复核者及已裁决事实");
  if ([item.crop_iou, item.transcription_cer, item.rubric_criterion_agreement].some((value) => value !== undefined && (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 1))) throw new Error("可选质量比率须在0—1之间");
  return item;
}

type PolicyForm = PutAIEligibilityPolicyRequest;
type EvaluationForm = CreateGradingEvaluationRequest;
type ObservationForm = AddGradingEvaluationObservationRequest;
type CalibrationForm = CreateModelCalibrationRequest;

type ScoringAssuranceProps = Readonly<{
  tenantID: string;
  initialAxis?: EligibilitySelection;
  canReadEligibility: boolean;
  canManageEligibility: boolean;
  canManageEvaluations: boolean;
  canReadDisagreements: boolean;
  canManageDisagreements: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
}>;

export function ScoringAssuranceWorkspace(props: ScoringAssuranceProps) {
  // 学校或入口评分组合改变时重建工作区；旧范围表单和未返回请求不能沿用到新对象。
  return <ScoringAssuranceContent key={JSON.stringify([props.tenantID, props.initialAxis ?? {}])} {...props} />;
}

function ScoringAssuranceContent({
  tenantID,
  initialAxis,
  canReadEligibility,
  canManageEligibility,
  canManageEvaluations,
  canReadDisagreements,
  canManageDisagreements,
  onDirtyChange,
  onSavingChange
}: ScoringAssuranceProps) {
  setScoringAssuranceSchool(tenantID);
  useEffect(() => { setScoringAssuranceSchool(tenantID); return () => setScoringAssuranceSchool(""); }, [tenantID]);
  const { message } = App.useApp();
  const [axis, setAxis] = useState<EligibilitySelection>(initialAxis ?? {});
  const policyRequestRef = useRef(0);
  const scopeKey = JSON.stringify([tenantID, axis]);
  const currentScopeRef = useRef(scopeKey);
  currentScopeRef.current = scopeKey;
  const [policyReadState, setPolicyReadState] = useState<"idle" | "loaded" | "missing" | "error">("idle");
  const [policy, setPolicy] = useState<AIEligibilityPolicy>();
  const [policyLoading, setPolicyLoading] = useState(false);
  const [policyError, setPolicyError] = useState<string>();
  const [policyDrawerOpen, setPolicyDrawerOpen] = useState(false);
  const [policySaving, setPolicySaving] = useState(false);
  const policySavingRef = useRef(false);
  const [policyForm] = Form.useForm<PolicyForm>();
  useEffect(() => () => { policyRequestRef.current++; }, []);
  const changeAxis = (next: EligibilitySelection) => {
    if (policyDrawerOpen || policySavingRef.current) return;
    // 选择事件先失效请求，再等待 React 重绘，防止旧响应在这段间隙回填。
    policyRequestRef.current++;
    setPolicy(undefined); setPolicyError(undefined); setPolicyReadState("idle"); setPolicyLoading(false);
    setAxis(next);
  };

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
  const [importText, setImportText] = useState("");
  const [importRows, setImportRows] = useState<AddGradingEvaluationObservationRequest[]>([]);
  const [importCompleted, setImportCompleted] = useState(0);
  const [importRunID, setImportRunID] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [importError, setImportError] = useState("");
  const importBusyRef = useRef(false);
  // 部分导入成功后保留原输入与已确认游标；切校确认不能在保存中绕过。
  useEffect(() => { onDirtyChange?.(policyDrawerOpen || evaluationDrawerOpen || observationDrawerOpen || calibrationDrawerOpen || importOpen); return () => onDirtyChange?.(false); }, [policyDrawerOpen, evaluationDrawerOpen, observationDrawerOpen, calibrationDrawerOpen, importOpen, onDirtyChange]);
  useEffect(() => { onSavingChange?.(actioning || policySaving); return () => onSavingChange?.(false); }, [actioning, policySaving, onSavingChange]);

  const previewObservations = () => {
    try {
      const rows: unknown = JSON.parse(importText);
      if (!Array.isArray(rows) || rows.length < 1 || rows.length > 500) throw new Error("请输入1—500条JSON观察记录");
      const keys = new Set<string>();
      for (const row of rows) {
        const item = validateObservationRow(row, keys);
        // 整行通过才登记键；集合只属于本次预览，失败后的重预览不继承中途记录。
        keys.add(item.response_key);
      }
      setImportRows(rows as AddGradingEvaluationObservationRequest[]); setImportCompleted(0); setImportError("");
    } catch (reason) { setImportError(asErrorMessage(reason)); }
  };
  const importObservations = async () => {
    if (!importRunID || !importRows.length || actioning || importBusyRef.current) return;
    importBusyRef.current = true;
    setActioning(true); onSavingChange?.(true); setImportError("");
    let completed = importCompleted;
    try {
      // 服务端没有批量事务；逐条确认后移动游标，失败重试不能重发已经收到成功回执的观察。
      for (; completed < importRows.length; completed++) { await addGradingEvaluationObservation(importRunID, importRows[completed]); setImportCompleted(completed + 1); }
      message.success(`已导入 ${completed} 条脱敏观察，仍须核对后冻结评测`); await loadAssurance();
    } catch (reason) { setImportError(`已保存 ${completed}/${importRows.length} 条。${asErrorMessage(reason)}；剩余内容已保留。若网络未确认回执，请先核对该答卷键，服务端会拒绝重复观察。`); } finally { importBusyRef.current = false; setActioning(false); onSavingChange?.(false); }
  };

  const [disagreements, setDisagreements] = useState<AIHumanDisagreement[]>([]);
  const [disagreementLoading, setDisagreementLoading] = useState(false);
  const [disagreementError, setDisagreementError] = useState<string>();
  const [selectedDisagreement, setSelectedDisagreement] = useState<AIHumanDisagreement>();
  const [classificationDrawerOpen, setClassificationDrawerOpen] = useState(false);
  const [routeDrawerOpen, setRouteDrawerOpen] = useState(false);
  const [classificationForm] = Form.useForm<{ taxonomy: AIHumanDisagreementTaxonomy; notes?: string }>();
  const [routeForm] = Form.useForm<{ review_task_id: string }>();

  const loadPolicy = useCallback(async () => {
    if (!canReadEligibility || !tenantID || !isCompleteEligibilitySelection(axis)) return;
    const requestID = ++policyRequestRef.current;
    const stillCurrent = () => requestID === policyRequestRef.current && scopeKey === currentScopeRef.current;
    setPolicyLoading(true);
    setPolicyError(undefined);
    setPolicy(undefined);
    setPolicyReadState("idle");
    try {
      const response = await getAIEligibilityPolicy(axis);
      if (!stillCurrent()) return;
      if (response.policy.subject_code !== axis.subject_code || response.policy.education_stage !== axis.education_stage || response.policy.archetype_code !== axis.archetype_code || response.policy.risk_tier !== axis.risk_tier) throw new Error("返回的策略范围与当前选择不一致，请重新读取当前范围。");
      setPolicy(response.policy);
      setPolicyReadState("loaded");
    } catch (error) {
      if (!stillCurrent()) return;
      setPolicy(undefined);
      if (isNotFound(error)) {
        setPolicyReadState("missing");
        setPolicyError("该学科、学段、题型与风险级别尚未配置准入策略；评分链路会保守转人工。");
      } else {
        setPolicyReadState("error");
        setPolicyError(asErrorMessage(error));
      }
    } finally {
      if (stillCurrent()) setPolicyLoading(false);
    }
  }, [axis, canReadEligibility, scopeKey, tenantID]);

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
    // 选中评测变化后，旧请求不能覆盖当前评测的三类证据。
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
    // 校准证据与评测证据独立加载；仅本轮选中校准仍有效时回写。
    let active = true;
    listModelCalibrationEvidence(selectedCalibrationID)
      .then((response) => { if (active) setCalibrationEvidence(response.evidence); })
      .catch((error: unknown) => { if (active) message.error(asErrorMessage(error)); });
    return () => { active = false; };
  }, [canManageEvaluations, message, selectedCalibrationID]);

  const openPolicyEditor = () => {
    // 读取失败不是未配置；只有当前范围成功读取或明确不存在，才能携带正确版本维护策略。
    if (!isCompleteEligibilitySelection(axis) || policyLoading || !["loaded", "missing"].includes(policyReadState)) return;
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
    if (!isCompleteEligibilitySelection(axis) || policySavingRef.current) return;
    const requestedScope = scopeKey;
    const requestID = ++policyRequestRef.current;
    const stillCurrent = () => requestID === policyRequestRef.current && requestedScope === currentScopeRef.current;
    policySavingRef.current = true;
    setPolicySaving(true); onSavingChange?.(true);
    try {
      const values = { ...await policyForm.validateFields(), ...axis };
      if (!stillCurrent()) return;
      const response = await putAIEligibilityPolicy(values);
      if (!stillCurrent()) return;
      setPolicy(response.policy);
      setPolicyReadState("loaded"); setPolicyError(undefined);
      setPolicyDrawerOpen(false);
      message.success("题型准入策略已保存为新版本；未满足条件的答卷仍会转人工。");
    } catch (error) {
      if (stillCurrent()) message.error(asErrorMessage(error));
    } finally {
      policySavingRef.current = false;
      if (stillCurrent()) { setPolicySaving(false); onSavingChange?.(false); }
    }
  };

  const createEvaluation = async () => {
    const values = await evaluationForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      const response = await createGradingEvaluation(values);
      setEvaluationDrawerOpen(false);
      evaluationForm.resetFields();
      // 列表刷新会先保留或回退选择，之后再明确选中新建评测。
      await loadAssurance();
      setSelectedEvaluationID(response.evaluation_run.id);
      message.success("评分验证报告已登记；请录入或导入对齐观察，核对后再冻结。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const addObservation = async () => {
    if (!selectedEvaluation) return;
    const values = await observationForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      await addGradingEvaluationObservation(selectedEvaluation.id, values);
      setObservationDrawerOpen(false);
      observationForm.resetFields();
      await loadAssurance();
      message.success("已记录脱敏的对齐观察，不会上传答题文本或原图。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const transitionEvaluation = async (kind: "complete" | "invalidate") => {
    if (!selectedEvaluation) return;
    let reason = "";
    if (kind === "invalidate") {
      reason = window.prompt("请说明失效原因（不会修改历史观察）：")?.trim() ?? "";
      if (!reason) return;
    }
    setActioning(true); onSavingChange?.(true);
    try {
      if (kind === "complete") await completeGradingEvaluation(selectedEvaluation.id);
      else await invalidateGradingEvaluation(selectedEvaluation.id, reason);
      await loadAssurance();
      message.success(kind === "complete" ? "离线评测已冻结，可用于对应轴的准入证据。" : "离线评测已失效，之后不会再作为准入证据。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const createCalibration = async () => {
    const values = await calibrationForm.validateFields();
    setActioning(true); onSavingChange?.(true);
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
      setActioning(false); onSavingChange?.(false);
    }
  };

  const addCalibrationEvidence = async () => {
    if (!selectedCalibration) return;
    const values = await evidenceForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      // 先保存并重读本校准证据，再刷新列表；这些请求不能换成并发。
      await addModelCalibrationEvidence(selectedCalibration.id, values);
      evidenceForm.resetFields();
      const response = await listModelCalibrationEvidence(selectedCalibration.id);
      setCalibrationEvidence(response.evidence);
      await loadAssurance();
      message.success("置信度观察已加入校准草稿。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const transitionCalibration = async (kind: "complete" | "approve" | "invalidate") => {
    if (!selectedCalibration) return;
    let reason = "";
    if (kind === "invalidate") {
      reason = window.prompt("请说明失效原因：")?.trim() ?? "";
      if (!reason) return;
    }
    setActioning(true); onSavingChange?.(true);
    try {
      if (kind === "complete") await completeModelCalibration(selectedCalibration.id);
      if (kind === "approve") await approveModelCalibration(selectedCalibration.id);
      if (kind === "invalidate") await invalidateModelCalibration(selectedCalibration.id, reason);
      await loadAssurance();
      message.success(assuranceCalibrationTransitionMessage(kind));
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const submitClassification = async () => {
    if (!selectedDisagreement) return;
    const values = await classificationForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      // 提交使用打开分类时的分歧版本，刷新后的列表不能替代这个修订号。
      await classifyAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      setClassificationDrawerOpen(false);
      await loadDisagreements();
      message.success("已记录人工分类；它不会自动更改分数或认定责任。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const submitRoute = async () => {
    if (!selectedDisagreement) return;
    const values = await routeForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      await routeAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      setRouteDrawerOpen(false);
      await loadDisagreements();
      message.success("分歧已关联人工阅卷任务；不会创建或直接修改最终成绩。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const evaluationColumns: TableColumnsType<GradingEvaluationRun> = [
    { title: "评测", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.display_name}</strong><Typography.Text type="secondary">{item.key}</Typography.Text></Space> },
    { title: "模型 / Prompt / Rubric", render: (_value, item) => <Space direction="vertical" size={0}><span>{item.model_reference}</span><Typography.Text type="secondary">{item.prompt_version} · {item.rubric_version}</Typography.Text></Space> },
    { title: "对齐观察", dataIndex: "observation_count", width: 100 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={assuranceEvaluationStatusColor(value)}>{assuranceEvaluationStatusLabel(value)}</Tag> },
    { title: "操作", width: 90, render: (_value, item) => <Button type={item.id === selectedEvaluationID ? "primary" : "link"} size="small" onClick={() => setSelectedEvaluationID(item.id)}>查看</Button> }
  ];

  const calibrationColumns: TableColumnsType<ModelCalibration> = [
    { title: "校准", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.key}</strong><Typography.Text type="secondary">{item.axis.subject} · {item.axis.archetype} · {item.axis.slice_key}</Typography.Text></Space> },
    { title: "版本轴", render: (_value, item) => <Typography.Text>{item.axis.model_reference}<br />{item.axis.prompt_version} · {item.axis.rubric_version}</Typography.Text> },
    { title: "样本", dataIndex: "calibration_n", width: 76 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={assuranceCalibrationStatusColor(value)}>{assuranceCalibrationStatusLabel(value)}</Tag> },
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

      {canReadEligibility ? <EligibilityPolicyPanel axis={axis} onAxisChange={changeAxis} policy={policy} policyLoading={policyLoading}
        selectionLocked={policyDrawerOpen || policySaving} selectionComplete={isCompleteEligibilitySelection(axis)} canEditPolicy={["loaded", "missing"].includes(policyReadState)}
        policyError={policyError} canManageEligibility={canManageEligibility} subjectOptions={subjectOptions} archetypeOptions={archetypeOptions}
        loadPolicy={loadPolicy} openPolicyEditor={openPolicyEditor}><DecisionLookup canRead={canReadEligibility} /></EligibilityPolicyPanel> : null}

      {canManageEvaluations ? <Card size="small" title={<Space><FlaskConical size={17} />已有评分报告与置信度校准</Space>} extra={<Space><Button icon={<RefreshCw size={15} />} loading={assuranceLoading} onClick={() => void loadAssurance()}>刷新</Button><Button type="primary" onClick={() => setEvaluationDrawerOpen(true)}>登记评分验证报告</Button></Space>}>
        <p>评测负责人先完成离线评分及人工真值核验，再登记版本轴和对齐记录。此处不运行模型；冻结证据与配置准入是后续步骤。</p>
        <Space wrap><Button onClick={() => downloadGovernanceMaterial("脱敏对齐评分记录模板.json", observationExample)}>下载对齐记录模板</Button><Button disabled={selectedEvaluation?.status !== "draft" || actioning} onClick={() => {
          if (importText && importRunID !== selectedEvaluation!.id) { message.error("另一评测的导入材料尚未清除，请先回到原评测完成或清除材料。"); return; }
          if (!importText) { setImportRunID(selectedEvaluation!.id); setImportRows([]);setImportCompleted(0);setImportError(""); }
          setImportOpen(true);
        }}>导入对齐评分记录</Button></Space>
        {assuranceError ? <Alert showIcon type="error" message="加载评分保障证据失败" description={assuranceError} action={<Button onClick={() => void loadAssurance()}>重试</Button>} /> : null}
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={evaluationColumns} dataSource={evaluationRuns} locale={{ emptyText: <Empty description="尚无离线评测" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} />
        <EvaluationDetail selectedEvaluation={selectedEvaluation} actioning={actioning} qualitySummary={qualitySummary}
          sliceMetrics={sliceMetrics} responseDifficulty={responseDifficulty} onOpenObservation={() => setObservationDrawerOpen(true)}
          transitionEvaluation={transitionEvaluation} onCreateCalibration={() => {
            if (!selectedEvaluation) return;
            calibrationForm.setFieldsValue({ evaluation_run_id: selectedEvaluation.id, method: "auto", axis: { model_reference: selectedEvaluation.model_reference, prompt_version: selectedEvaluation.prompt_version, rubric_version: selectedEvaluation.rubric_version, subject: "mathematics", archetype: "short_constructed", slice_key: "all" } });
            setCalibrationDrawerOpen(true);
          }} />
        <Divider orientation="left">校准工件</Divider>
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={calibrationColumns} dataSource={calibrations} locale={{ emptyText: "尚无校准工件" }} />
        <CalibrationDetail selectedCalibration={selectedCalibration} calibrationEvidence={calibrationEvidence} actioning={actioning}
          evidenceForm={evidenceForm} transitionCalibration={transitionCalibration} addCalibrationEvidence={addCalibrationEvidence} />
      </Card> : null}

      {canReadDisagreements ? <DisagreementQueue disagreements={disagreements} severeCount={severeCount} disagreementLoading={disagreementLoading}
        disagreementError={disagreementError} canManageDisagreements={canManageDisagreements} taxonomyOptions={taxonomyOptions}
        loadDisagreements={loadDisagreements} onClassify={(item) => {
          setSelectedDisagreement(item); classificationForm.setFieldsValue({ taxonomy: item.taxonomy ?? "ai_scoring_error", notes: item.notes }); setClassificationDrawerOpen(true);
        }} onRoute={(item) => { setSelectedDisagreement(item); routeForm.resetFields(); setRouteDrawerOpen(true); }} /> : null}

      <Drawer title="维护题型准入策略" width={560} open={policyDrawerOpen} closable={!policySaving} maskClosable={!policySaving} keyboard={!policySaving} onClose={() => { if (!policySavingRef.current) setPolicyDrawerOpen(false); }} extra={<Button type="primary" loading={policySaving} onClick={() => void savePolicy()}>保存新版本</Button>}>
        <p>当前维护范围：{eligibilityScopeLabel(axis)}。需要维护其他范围时，请先关闭并在页面重新选择和查询。</p>
        <Alert type="warning" showIcon message="此处只配置是否允许调用 AI，不授予模型最终分数权。R3 长作答禁止配置为 AI 快速确认或规则自动。" />
        <Form form={policyForm} layout="vertical" style={{ marginTop: 16 }}>
          <Space.Compact block><Form.Item name="subject_code" label="学科" rules={[{ required: true }]} style={{ width: "25%" }}><Select disabled options={subjectOptions} /></Form.Item><Form.Item name="education_stage" label="学段" rules={[{ required: true }]} style={{ width: "25%" }}><Select disabled options={eligibilityStages} /></Form.Item><Form.Item name="risk_tier" label="风险" rules={[{ required: true }]} style={{ width: "20%" }}><Select disabled options={eligibilityRisks} /></Form.Item></Space.Compact>
          <Form.Item name="archetype_code" label="题型" rules={[{ required: true }]}><Select disabled options={archetypeOptions} /></Form.Item>
          <Space.Compact block><Form.Item name="min_ocr_quality" label="最低 OCR 质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_parser_quality" label="最低解析质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_eval_n" label="最低评测样本" rules={[{ required: true }]} style={{ width: "34%" }}><InputNumber min={1} precision={0} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Form.Item name="max_severe_error_rate" label="最大严重误差率" rules={[{ required: true }]}><InputNumber min={0} max={1} step={0.005} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="allowed_modes" label="允许评分模式" rules={[{ required: true }]}><Select mode="multiple" options={scoringModeOptions} /></Form.Item>
          <Form.Item name="status" label="状态" rules={[{ required: true }]}><Select options={[{ value: "active", label: "启用" }, { value: "disabled", label: "停用" }]} /></Form.Item>
          <Form.Item name="expected_version" hidden><InputNumber /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="登记已有评分验证报告" width={620} open={evaluationDrawerOpen} onClose={() => setEvaluationDrawerOpen(false)} extra={<Button type="primary" loading={actioning} onClick={() => void createEvaluation()}>登记报告</Button>}>
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

      <Drawer title="导入脱敏对齐评分记录" width={680} open={importOpen} closable={!actioning} maskClosable={!actioning} onClose={() => { if (!actioning) setImportOpen(false); }}>
        <p>关闭后材料仍留在本学校工作区，再次打开可继续；离开学校前请复制备份。清除材料不会删除已保存的观察。</p>
        <Button disabled={actioning} onClick={() => { setImportText(""); setImportRows([]); setImportCompleted(0); setImportError(""); setImportOpen(false); }}>清除本次导入材料</Button>
        <Alert type="info" showIcon message="仅导入已完成评分的脱敏记录，不调用模型" description={`目标报告：${evaluationRuns.find((run) => run.id === importRunID)?.display_name ?? importRunID}。逐条保存，没有批量回滚；真实字段和权限由服务端再次校验。`} />
        <label>JSON 观察记录<Input.TextArea rows={10} value={importText} disabled={actioning || importCompleted > 0} onChange={(event) => { setImportText(event.target.value); setImportRows([]); }} /></label>
        <Button disabled={actioning || importCompleted > 0} onClick={previewObservations}>校验并预览</Button>
        {importRows.length ? <><p>共 {importRows.length} 条，已保存 {importCompleted} 条。首条：{importRows[0].response_key}，参考 {importRows[0].reference_score} / 模型 {importRows[0].model_score}。</p><Button type="primary" loading={actioning} disabled={importCompleted === importRows.length} onClick={() => void importObservations()}>{importCompleted ? "继续保存剩余记录" : "确认导入"}</Button></> : null}
        {importError ? <Alert type="error" showIcon message={importError} /> : null}
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

function DecisionLookup({ canRead }: Readonly<{ canRead: boolean }>) {
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
