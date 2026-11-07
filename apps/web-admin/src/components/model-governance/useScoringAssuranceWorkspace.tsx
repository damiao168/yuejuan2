import type { ReviewTask } from "@edugrade/sdk";
import { App, Button, Form, Space, Tag, Typography, type TableColumnsType } from "antd";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { addGradingEvaluationObservation, addModelCalibrationEvidence, approveModelCalibration, classifyAIHumanDisagreement, completeGradingEvaluation, completeModelCalibration, createGradingEvaluation, createModelCalibration, getAIEligibilityPolicy, getGradingEvaluationQualitySummary, invalidateGradingEvaluation, invalidateModelCalibration, listAIHumanDisagreements, listGradingEvaluationResponseDifficulty, listGradingEvaluations, listGradingEvaluationSliceMetrics, listModelCalibrationEvidence, listModelCalibrations, putAIEligibilityPolicy, routeAIHumanDisagreement, setScoringAssuranceSchool, type AddGradingEvaluationObservationRequest, type AddModelCalibrationEvidenceRequest, type AIEligibilityPolicy, type AIHumanDisagreement, type AIHumanDisagreementTaxonomy, type GradingEvaluationQualitySummary, type GradingEvaluationResponseDifficulty, type GradingEvaluationRun, type GradingEvaluationSliceMetric, type ModelCalibration, type ModelCalibrationEvidence } from "../../api/scoringAssurance";
import { isCompleteEligibilitySelection, type EligibilitySelection } from "./eligibilityScope";
import { asErrorMessage, AssuranceDraft, CalibrationForm, defaultPolicy, EvaluationForm, isNotFound, ObservationForm, PolicyForm, ScoringAssuranceProps } from "./scoringAssuranceModel";
import { assuranceCalibrationStatusColor, assuranceCalibrationStatusLabel, assuranceCalibrationTransitionMessage, assuranceEvaluationStatusColor, assuranceEvaluationStatusLabel } from "./scoringAssurancePresentation";


import { scoringMaterialMatchesReport, type ScoringMaterial } from "./materialIntake";
import { validateObservationRow } from "./observationMaterial";

// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function useScoringAssuranceWorkspace({
  tenantID,
  initialAxis,
  canReadEligibility,
  canManageEligibility,
  canManageEvaluations,
  canReadDisagreements,
  canManageDisagreements,
  onDirtyChange,
  onSavingChange,
  materialDelivery,
  onMaterialReceived
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
  const draftVersion = useRef(0);
  const draftScopes = useRef<Partial<Record<AssuranceDraft, number>>>({});
  const mounted = useRef(true);
  const pendingDraftAction = useRef<{ kind: AssuranceDraft; version: number } | null>(null);
  const currentAccess = useRef({ canManageEvaluations, canManageDisagreements });
  if (currentAccess.current.canManageEvaluations !== canManageEvaluations || currentAccess.current.canManageDisagreements !== canManageDisagreements) draftVersion.current += 1;
  currentAccess.current = { canManageEvaluations, canManageDisagreements };
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; draftVersion.current += 1; };
  }, []);
  // 表单属于打开时的对象；切换对象、关闭或重新打开后，旧校验与旧回执都不能完成新草稿。
  const openDraft = (kind: AssuranceDraft, setOpen: (open: boolean) => void) => {
    draftScopes.current[kind] = ++draftVersion.current;
    setOpen(true);
  };
  const closeDraft = (setOpen: (open: boolean) => void) => { draftVersion.current += 1; setOpen(false); };
  const hasDraftAccess = (kind: AssuranceDraft) => kind === "classification" || kind === "route" ? currentAccess.current.canManageDisagreements : currentAccess.current.canManageEvaluations;
  const isCurrentDraft = (operation: { kind: AssuranceDraft; version: number }) => mounted.current && hasDraftAccess(operation.kind) && operation.version === draftVersion.current && draftScopes.current[operation.kind] === operation.version;
  const beginDraftAction = (kind: AssuranceDraft) => {
    if (!mounted.current || !hasDraftAccess(kind) || actioning || pendingDraftAction.current) return null;
    if (draftScopes.current[kind] !== draftVersion.current) { message.error("当前对象已变化，请关闭并重新打开草稿。"); return null; }
    const operation = { kind, version: draftVersion.current };
    pendingDraftAction.current = operation;
    // 校验前同步通知外层，防止校验等待期的重复提交或学校切换。
    setActioning(true); onSavingChange?.(true);
    return operation;
  };
  const finishDraftAction = (operation: { kind: AssuranceDraft; version: number }) => {
    if (pendingDraftAction.current !== operation) return;
    pendingDraftAction.current = null;
    if (mounted.current) { setActioning(false); onSavingChange?.(policySavingRef.current); }
  };
  const [selectedEvaluationID, setSelectedEvaluationID] = useState<string>();
  const [selectedCalibrationID, setSelectedCalibrationID] = useState<string>();
  const [sliceMetrics, setSliceMetrics] = useState<GradingEvaluationSliceMetric[]>([]);
  const [responseDifficulty, setResponseDifficulty] = useState<GradingEvaluationResponseDifficulty[]>([]);
  const [qualitySummary, setQualitySummary] = useState<GradingEvaluationQualitySummary>();
  const [calibrationEvidence, setCalibrationEvidence] = useState<ModelCalibrationEvidence[]>([]);
  const [evaluationRead, setEvaluationRead] = useState<{ id?: string; status: "loading" | "loaded" | "error"; error?: string }>({ status: "loading" });
  const [calibrationRead, setCalibrationRead] = useState<{ id?: string; status: "loading" | "loaded" | "error"; error?: string }>({ status: "loading" });
  const [evaluationRetry, setEvaluationRetry] = useState(0);
  const [calibrationRetry, setCalibrationRetry] = useState(0);
  const currentEvaluation = useRef(selectedEvaluationID);
  currentEvaluation.current = selectedEvaluationID;
  const currentCalibration = useRef(selectedCalibrationID);
  currentCalibration.current = selectedCalibrationID;
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
  const [sourceMaterial, setSourceMaterial] = useState<ScoringMaterial>();
  const receivedMaterial = useRef(0);
  useEffect(() => {
    if (!materialDelivery || materialDelivery.sequence === receivedMaterial.current || materialDelivery.tenantID !== tenantID || !canManageEvaluations) return;
    receivedMaterial.current = materialDelivery.sequence;
    setSourceMaterial(materialDelivery.material);
    evaluationForm.resetFields(); evaluationForm.setFieldsValue(materialDelivery.material.evaluation);
    draftScopes.current.evaluation = ++draftVersion.current;
    setEvaluationDrawerOpen(true); onMaterialReceived?.();
  }, [materialDelivery, tenantID, canManageEvaluations, evaluationForm, onMaterialReceived]);
  // 部分导入成功后保留原输入与已确认游标；切校确认不能在保存中绕过。
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
  const [routeTask, setRouteTask] = useState<ReviewTask>();
  const [classificationForm] = Form.useForm<{ taxonomy: AIHumanDisagreementTaxonomy; notes?: string }>();
  const [routeForm] = Form.useForm<{ review_task_id: string }>();
  useEffect(() => { onDirtyChange?.(policyDrawerOpen || evaluationDrawerOpen || observationDrawerOpen || calibrationDrawerOpen || importOpen || classificationDrawerOpen || routeDrawerOpen); return () => onDirtyChange?.(false); }, [policyDrawerOpen, evaluationDrawerOpen, observationDrawerOpen, calibrationDrawerOpen, importOpen, classificationDrawerOpen, routeDrawerOpen, onDirtyChange]);

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
  const evaluationReady = evaluationRead.id === selectedEvaluationID && evaluationRead.status === "loaded";
  const calibrationReady = calibrationRead.id === selectedCalibrationID && calibrationRead.status === "loaded";

  useEffect(() => {
    // 证据属于选中对象；新读取开始即清旧事实，失败也不能把旧指标显示在新标题下。
    setSliceMetrics([]); setResponseDifficulty([]); setQualitySummary(undefined);
    setEvaluationRead({ id: selectedEvaluationID, status: "loading" });
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
        if (!active || currentEvaluation.current !== selectedEvaluationID) return;
        setSliceMetrics(slices.slice_metrics);
        setResponseDifficulty(difficulty.response_difficulty);
        setQualitySummary(quality.quality_summary);
        setEvaluationRead({ id: selectedEvaluationID, status: "loaded" });
      })
      .catch((error: unknown) => { if (active && currentEvaluation.current === selectedEvaluationID) {
        setEvaluationRead({ id: selectedEvaluationID, status: "error", error: asErrorMessage(error) });
        message.error(asErrorMessage(error));
      } });
    return () => { active = false; };
  }, [canManageEvaluations, message, selectedEvaluationID, evaluationRetry]);

  useEffect(() => {
    setCalibrationEvidence([]);
    setCalibrationRead({ id: selectedCalibrationID, status: "loading" });
    if (!selectedCalibrationID || !canManageEvaluations) {
      setCalibrationEvidence([]);
      return;
    }
    // 校准证据与评测证据独立加载；仅本轮选中校准仍有效时回写。
    let active = true;
    listModelCalibrationEvidence(selectedCalibrationID)
      .then((response) => { if (active && currentCalibration.current === selectedCalibrationID) {
        setCalibrationEvidence(response.evidence);
        setCalibrationRead({ id: selectedCalibrationID, status: "loaded" });
      } })
      .catch((error: unknown) => { if (active && currentCalibration.current === selectedCalibrationID) {
        setCalibrationRead({ id: selectedCalibrationID, status: "error", error: asErrorMessage(error) });
        message.error(asErrorMessage(error));
      } });
    return () => { active = false; };
  }, [canManageEvaluations, message, selectedCalibrationID, calibrationRetry]);

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
    const operation = beginDraftAction("evaluation");
    if (!operation) return;
    let submitted = false;
    try {
      const values = await evaluationForm.validateFields();
      if (!isCurrentDraft(operation)) return;
      if (sourceMaterial && !scoringMaterialMatchesReport(sourceMaterial, values)) {
        message.error("材料的来源或版本已改变，请清除原材料后重新登记。"); return;
      }
      submitted = true;
      const response = await createGradingEvaluation(values);
      if (!isCurrentDraft(operation)) { if (mounted.current && hasDraftAccess(operation.kind)) await loadAssurance(); return; }
      setEvaluationDrawerOpen(false);
      evaluationForm.resetFields();
      // 列表刷新会先保留或回退选择，之后再明确选中新建评测。
      await loadAssurance();
      setSelectedEvaluationID(response.evaluation_run.id);
      if (sourceMaterial && isCurrentDraft(operation)) {
        // 逐卷证据只绑定刚登记且来源/版本完全一致的新报告，不能落到列表刷新后回退选中的旧报告。
        setImportRunID(response.evaluation_run.id); setImportText(JSON.stringify(sourceMaterial.observations, null, 2));
        setImportRows(sourceMaterial.observations); setImportCompleted(0); setImportError(""); setImportOpen(true); setSourceMaterial(undefined);
      }
      message.success("评分验证报告已登记；请录入或导入对齐观察，核对后再冻结。");
    } catch (error) {
      if (submitted && isCurrentDraft(operation)) message.error(asErrorMessage(error));
    } finally {
      finishDraftAction(operation);
    }
  };

  const addObservation = async () => {
    if (!selectedEvaluation) return;
    const operation = beginDraftAction("observation");
    if (!operation) return;
    let submitted = false;
    try {
      const values = await observationForm.validateFields();
      if (!isCurrentDraft(operation)) return;
      submitted = true;
      await addGradingEvaluationObservation(selectedEvaluation.id, values);
      if (!isCurrentDraft(operation)) { if (mounted.current && hasDraftAccess(operation.kind)) await loadAssurance(); return; }
      setObservationDrawerOpen(false);
      observationForm.resetFields();
      await loadAssurance();
      message.success("已记录脱敏的对齐观察，不会上传答题文本或原图。");
    } catch (error) {
      if (submitted && isCurrentDraft(operation)) message.error(asErrorMessage(error));
    } finally {
      finishDraftAction(operation);
    }
  };

  const transitionEvaluation = async (kind: "complete" | "invalidate") => {
    if (!selectedEvaluation || !evaluationReady) return;
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
    const operation = beginDraftAction("calibration");
    if (!operation) return;
    let submitted = false;
    try {
      const values = await calibrationForm.validateFields();
      if (!isCurrentDraft(operation)) return;
      submitted = true;
      const response = await createModelCalibration(values);
      if (!isCurrentDraft(operation)) { if (mounted.current && hasDraftAccess(operation.kind)) await loadAssurance(); return; }
      setCalibrationDrawerOpen(false);
      calibrationForm.resetFields();
      await loadAssurance();
      setSelectedCalibrationID(response.calibration.id);
      message.success("校准草稿已创建；需要补齐对齐观察的置信度后才可完成和批准。");
    } catch (error) {
      if (submitted && isCurrentDraft(operation)) message.error(asErrorMessage(error));
    } finally {
      finishDraftAction(operation);
    }
  };

  const addCalibrationEvidence = async () => {
    if (!selectedCalibration || !calibrationReady) return;
    const calibrationID = selectedCalibration.id;
    const values = await evidenceForm.validateFields();
    setActioning(true); onSavingChange?.(true);
    try {
      // 先保存并重读本校准证据，再刷新列表；这些请求不能换成并发。
      await addModelCalibrationEvidence(selectedCalibration.id, values);
      evidenceForm.resetFields();
      const response = await listModelCalibrationEvidence(selectedCalibration.id);
      if (currentCalibration.current === calibrationID) setCalibrationEvidence(response.evidence);
      await loadAssurance();
      message.success("置信度观察已加入校准草稿。");
    } catch (error) {
      message.error(asErrorMessage(error));
    } finally {
      setActioning(false); onSavingChange?.(false);
    }
  };

  const transitionCalibration = async (kind: "complete" | "approve" | "invalidate") => {
    if (!selectedCalibration || !calibrationReady) return;
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
    const operation = beginDraftAction("classification");
    if (!operation) return;
    let submitted = false;
    try {
      const values = await classificationForm.validateFields();
      if (!isCurrentDraft(operation)) return;
      submitted = true;
      // 提交使用打开分类时的分歧版本，刷新后的列表不能替代这个修订号。
      await classifyAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      if (!isCurrentDraft(operation)) { if (mounted.current && hasDraftAccess(operation.kind)) await loadDisagreements(); return; }
      setClassificationDrawerOpen(false);
      await loadDisagreements();
      message.success("已记录人工分类；它不会自动更改分数或认定责任。");
    } catch (error) {
      if (submitted && isCurrentDraft(operation)) message.error(asErrorMessage(error));
    } finally {
      finishDraftAction(operation);
    }
  };

  const submitRoute = async () => {
    if (!selectedDisagreement) return;
    const operation = beginDraftAction("route");
    if (!operation) return;
    let submitted = false;
    try {
      const values = await routeForm.validateFields();
      if (!isCurrentDraft(operation)) return;
      if (!routeTask || values.review_task_id !== routeTask.id || routeTask.tenant_id !== tenantID || routeTask.exam_id !== selectedDisagreement.exam_id || routeTask.question_id !== selectedDisagreement.question_id || routeTask.submission_id !== selectedDisagreement.submission_id || routeTask.answer_segment_id !== selectedDisagreement.answer_segment_id) {
        message.error("请从当前学校同题同答卷的授权任务中选择并核对后再关联");
        return;
      }
      submitted = true;
      await routeAIHumanDisagreement(selectedDisagreement.id, { ...values, expected_revision: selectedDisagreement.revision });
      if (!isCurrentDraft(operation)) { if (mounted.current && hasDraftAccess(operation.kind)) await loadDisagreements(); return; }
      setRouteDrawerOpen(false);
      await loadDisagreements();
      message.success("分歧已关联人工阅卷任务；不会创建或直接修改最终成绩。");
    } catch (error) {
      if (submitted && isCurrentDraft(operation)) message.error(asErrorMessage(error));
    } finally {
      finishDraftAction(operation);
    }
  };

  const evaluationColumns: TableColumnsType<GradingEvaluationRun> = [
    { title: "评测", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.display_name}</strong><Typography.Text type="secondary">{item.key}</Typography.Text></Space> },
    { title: "模型 / Prompt / Rubric", render: (_value, item) => <Space direction="vertical" size={0}><span>{item.model_reference}</span><Typography.Text type="secondary">{item.prompt_version} · {item.rubric_version}</Typography.Text></Space> },
    { title: "对齐观察", dataIndex: "observation_count", width: 100 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={assuranceEvaluationStatusColor(value)}>{assuranceEvaluationStatusLabel(value)}</Tag> },
    { title: "操作", width: 90, render: (_value, item) => <Button type={item.id === selectedEvaluationID ? "primary" : "link"} size="small" onClick={() => { draftVersion.current += 1; setSelectedEvaluationID(item.id); }}>查看</Button> }
  ];

  const calibrationColumns: TableColumnsType<ModelCalibration> = [
    { title: "校准", render: (_value, item) => <Space direction="vertical" size={0}><strong>{item.key}</strong><Typography.Text type="secondary">{item.axis.subject} · {item.axis.archetype} · {item.axis.slice_key}</Typography.Text></Space> },
    { title: "版本轴", render: (_value, item) => <Typography.Text>{item.axis.model_reference}<br />{item.axis.prompt_version} · {item.axis.rubric_version}</Typography.Text> },
    { title: "样本", dataIndex: "calibration_n", width: 76 },
    { title: "状态", dataIndex: "status", width: 105, render: (value) => <Tag color={assuranceCalibrationStatusColor(value)}>{assuranceCalibrationStatusLabel(value)}</Tag> },
    { title: "操作", width: 90, render: (_value, item) => <Button type={item.id === selectedCalibrationID ? "primary" : "link"} size="small" onClick={() => { draftVersion.current += 1; setSelectedCalibrationID(item.id); }}>查看</Button> }
  ];

  const severeCount = useMemo(() => disagreements.filter((item) => item.severity === "severe" && item.status === "needs_review").length, [disagreements]);
  const canSeeAny = canReadEligibility || canManageEvaluations || canReadDisagreements;
  return {
    tenantID, canReadEligibility, canManageEligibility, canManageEvaluations,
    canReadDisagreements, canManageDisagreements, message, axis,
    policyReadState, policy, policyLoading, policyError,
    policyDrawerOpen, setPolicyDrawerOpen, policySaving, policySavingRef,
    policyForm, changeAxis, evaluationRuns, calibrations,
    assuranceLoading, assuranceError, evaluationDrawerOpen, setEvaluationDrawerOpen,
    observationDrawerOpen, setObservationDrawerOpen, calibrationDrawerOpen, setCalibrationDrawerOpen,
    actioning, openDraft, closeDraft, selectedEvaluationID,
    selectedCalibrationID, sliceMetrics, responseDifficulty, qualitySummary,
    calibrationEvidence, evaluationRead, calibrationRead, setEvaluationRetry,
    setCalibrationRetry, evaluationForm, observationForm, calibrationForm,
    evidenceForm, importText, setImportText, importRows,
    setImportRows, importCompleted, setImportCompleted, importRunID,
    setImportRunID, importOpen, setImportOpen, importError,
    setImportError, sourceMaterial, setSourceMaterial, previewObservations,
    importObservations, disagreements, disagreementLoading, disagreementError,
    selectedDisagreement, setSelectedDisagreement, classificationDrawerOpen, setClassificationDrawerOpen,
    routeDrawerOpen, setRouteDrawerOpen, routeTask, setRouteTask,
    classificationForm, routeForm, loadPolicy, loadAssurance,
    loadDisagreements, selectedEvaluation, selectedCalibration, evaluationReady,
    calibrationReady, openPolicyEditor, savePolicy, createEvaluation,
    addObservation, transitionEvaluation, createCalibration, addCalibrationEvidence,
    transitionCalibration, submitClassification, submitRoute, evaluationColumns,
    calibrationColumns, severeCount, canSeeAny,
  };
}
