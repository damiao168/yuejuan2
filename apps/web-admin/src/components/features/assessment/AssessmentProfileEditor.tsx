import { App, Button, Drawer, Form } from "antd";
import { Save } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { getQuestionAssessmentProfile, getQuestionAssessmentSnapshot, listQuestionArchetypes, listSubjectProfiles, updateQuestionAssessmentProfile, type AssessmentProfile, type AssessmentSnapshot, type EducationStage, type EvidenceType, type QuestionArchetype, type QuestionArchetypeCode, type SubjectCode, type SubjectProfile } from "../../../api/assessment";
import { ApiClientError } from "../../../api/client";
import type { Question } from "../../../api/papers";
import type { RegisterQuestionSaveParticipant } from "../../../features/paper-rubric/questionSaveParticipant";
import { defaultArchetypeForQuestionType, isAssessmentPolicyBlocked } from "./assessmentPolicy";
import { assessmentDraftKey, assessmentEvidenceOptions, AssessmentFormValues, AssessmentProfileDrawerBody, AssessmentProfileHeading, evidenceLabels, formatError, inlineScoringModeOptions, subjectOptions } from "./AssessmentProfileFields";
import { assessmentScoringGuidance, defaultAssessmentScoringMode } from "./assessmentScoringDefaults";
import { normalizeAssessmentSubject } from "./assessmentSubjectScope";

export function AssessmentProfileEditor({
  examId,
  examSubject,
  examStatus,
  examGradingMode,
  question,
  canManage,
  onChanged,
  presentation = "drawer",
  active = true,
  registerSaveParticipant,
  externalSaving = false
}: Readonly<{
  examId: string;
  examSubject: string;
  examStatus: string;
  examGradingMode?: string;
  question: Question | null;
  canManage: boolean;
  onChanged?: () => void;
  presentation?: "drawer" | "inline";
  active?: boolean;
  registerSaveParticipant?: RegisterQuestionSaveParticipant;
  externalSaving?: boolean;
}>) {
  const { message } = App.useApp();
  const [form] = Form.useForm<AssessmentFormValues>();
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const [profiles, setProfiles] = useState<SubjectProfile[]>([]);
  const [archetypes, setArchetypes] = useState<QuestionArchetype[]>([]);
  const [savedProfile, setSavedProfile] = useState<AssessmentProfile>();
  const [snapshot, setSnapshot] = useState<AssessmentSnapshot>();
  const [evidenceNotice, setEvidenceNotice] = useState("");
  const [loadedContext, setLoadedContext] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const savedValues = useRef<Partial<AssessmentFormValues> | undefined>(undefined);
  // 页面、题目和阅卷方式共同限定这次读取；关闭重开或 A→B→A 时还要用代次淘汰旧请求。
  const contextKey = JSON.stringify([examId, question?.id, question?.question_type, examSubject, examStatus, examGradingMode, canManage, presentation]);
  const currentContext = useRef(contextKey);
  currentContext.current = contextKey;
  const operationVersion = useRef(0);
  const viewVersion = operationVersion.current;
  const reading = useRef(false);
  const writing = useRef(false);
  const opened = useRef(false);
  const loaded = useRef("");
  const stage = Form.useWatch("education_stage", form);
  const subject = Form.useWatch("subject_code", form);
  const selectedArchetype = Form.useWatch("question_archetype", form);
  const scoringMode = Form.useWatch("scoring_mode", form);
  const riskTier = Form.useWatch("risk_tier", form);
  const profileId = Form.useWatch("subject_profile_id", form);
  const selectedEvidence = Form.useWatch("evidence_types", form) ?? [];
  const currentDraftKey = assessmentDraftKey({ education_stage: stage, subject_code: subject, subject_profile_id: profileId, question_archetype: selectedArchetype, evidence_types: selectedEvidence, scoring_mode: scoringMode, risk_tier: riskTier });
  const examSubjectCode = normalizeAssessmentSubject(examSubject);
  const examSubjectLabel = subjectOptions.find((item) => item.value === examSubjectCode)?.label;

  const matchingProfiles = useMemo(
    () => profiles.filter((profile) => profile.education_stage === stage && profile.subject_code === subject),
    [profiles, stage, subject]
  );
  const selectedArchetypeDefinition = archetypes.find((item) => item.code === selectedArchetype);
  const selectedProfileDefinition = matchingProfiles.find((item) => item.id === profileId);
  const availableEvidence = assessmentEvidenceOptions(selectedProfileDefinition, selectedArchetypeDefinition);
  const invalidEvidence = selectedEvidence.some((item: EvidenceType) => !availableEvidence.includes(item));
  const policyBlocked = isAssessmentPolicyBlocked(riskTier, selectedArchetype, scoringMode);
  // 已生成评分快照或考试已离开配置阶段时，配置就是运行依据；任一条件成立都必须禁止再改评分策略。
  const frozen = Boolean(snapshot) || !["draft", "configured"].includes(examStatus);
  const editable = canManage && !frozen;
  const effectiveProfile = snapshot ?? savedProfile;
  const scoringGuidance = assessmentScoringGuidance(question?.question_type, scoringMode);
  const subjectMismatch = Boolean(examSubjectCode && (subject !== examSubjectCode
    || (selectedProfileDefinition && selectedProfileDefinition.subject_code !== examSubjectCode)));
  const configurationReady = open && loadedContext === contextKey && !loading && !error;
  const canSave = editable && configurationReady && Boolean(selectedProfileDefinition && selectedArchetypeDefinition)
    && availableEvidence.length > 0 && selectedEvidence.length > 0 && !invalidEvidence && !policyBlocked && !subjectMismatch;

  // biome-ignore lint/correctness/useExhaustiveDependencies: 考试、题目或权限上下文变化时使旧弹窗和保存回调失效；只保留 form 依赖会跨题沿用配置。
  useLayoutEffect(() => {
    operationVersion.current++;
    reading.current = false; writing.current = false; opened.current = false; loaded.current = "";
    setOpen(false);
    setLoading(false); setSaving(false); setLoadedContext(""); setEvidenceNotice("");
    setError(undefined);
    setSavedProfile(undefined);
    setSnapshot(undefined);
    setAdvancedOpen(false);
    form.resetFields();
    return () => { operationVersion.current++; opened.current = false; loaded.current = ""; };
  }, [contextKey, form]);

  // 评分标签首次可见时读取；切回题目标签不重置表单，换题仍由上下文代次隔离旧响应。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 读取依赖由 contextKey 和标签可见性限定，不能随每次渲染重发请求。
  useLayoutEffect(() => {
    if (presentation === "inline" && active && question && !opened.current) void loadConfiguration();
  }, [contextKey, presentation, active]);

  function isCurrent(version: number) {
    return currentContext.current === contextKey && version === operationVersion.current && opened.current;
  }

  function closeConfiguration() {
    // 保存请求已发出时关闭不能撤销后台写入；完成之前保留本窗口，换题或换考试仍会丢弃旧响应。
    if (writing.current || currentContext.current !== contextKey || viewVersion !== operationVersion.current) return;
    operationVersion.current++;
    opened.current = false; reading.current = false; loaded.current = "";
    setOpen(false); setLoading(false); setLoadedContext("");
  }

  async function loadConfiguration() {
    if (!question || currentContext.current !== contextKey || reading.current || writing.current) return;
    const version = ++operationVersion.current;
    reading.current = true; opened.current = true; loaded.current = "";
    setOpen(true);
    setLoading(true);
    setLoadedContext(""); setError(undefined); setEvidenceNotice("");
    try {
      const [profileResult, archetypeResult, currentProfile, currentSnapshot] = await Promise.all([
        listSubjectProfiles(), listQuestionArchetypes(),
        getQuestionAssessmentProfile(examId, question.id)
          .then((result) => result.assessment_profile)
          .catch((profileError: unknown) => {
            if (profileError instanceof ApiClientError && profileError.status === 404) return undefined;
            throw profileError;
          }),
        getQuestionAssessmentSnapshot(examId, question.id)
          .then((result) => result.assessment_snapshot)
          .catch((snapshotError: unknown) => {
            if (snapshotError instanceof ApiClientError && snapshotError.status === 404) return undefined;
            throw snapshotError;
          })
      ]);
      if (!isCurrent(version)) return;
      setProfiles(profileResult.subject_profiles);
      setArchetypes(archetypeResult.question_archetypes);
      setSavedProfile(currentProfile);
      setSnapshot(currentSnapshot);

      const defaultArchetype = defaultArchetypeForQuestionType(question.question_type);
      const archetype = currentSnapshot?.archetype_code ?? currentProfile?.archetype_code ?? defaultArchetype;
      const archetypeDefinition = archetypeResult.question_archetypes.find((item) => item.code === archetype);
      const selectedProfileRecord = profileResult.subject_profiles.find(
        (item) => item.id === (currentSnapshot?.subject_profile_id ?? currentProfile?.subject_profile_id)
      );
      // 历史配置按原值展示；未知考试学科不默认当作数学，未配置的题目由管理员明确选择学科。
      const defaultSubject = currentSnapshot?.subject_code ?? currentProfile?.subject_code ?? selectedProfileRecord?.subject_code ?? examSubjectCode;
      const defaultStage = currentSnapshot?.education_stage ?? currentProfile?.education_stage ?? selectedProfileRecord?.education_stage;
      const selectedProfile = currentSnapshot?.subject_profile_id ?? currentProfile?.subject_profile_id
        ?? profileResult.subject_profiles.find((item) => item.education_stage === defaultStage && item.subject_code === defaultSubject)?.id;
      const defaultProfile = profileResult.subject_profiles.find((item) => item.id === selectedProfile);
      // 首次设置缺少学段或模板时展开必填字段；已配置题目默认只露出判分方式。
      const allowedEvidence = assessmentEvidenceOptions(defaultProfile, archetypeDefinition);
      const originalEvidence = currentSnapshot?.allowed_evidence_types ?? currentProfile?.allowed_evidence_types;
      setAdvancedOpen(!defaultProfile || !archetypeDefinition || !allowedEvidence.length
        || Boolean(originalEvidence && (!originalEvidence.length || originalEvidence.some((item) => !allowedEvidence.includes(item))))
        || Boolean(examSubjectCode && defaultSubject !== examSubjectCode));

      const values: Partial<AssessmentFormValues> = {
        education_stage: defaultStage,
        subject_code: defaultSubject,
        subject_profile_id: selectedProfile ?? "",
        question_archetype: archetype,
        // 已保存配置和冻结快照保留原选择，只有未落库配置使用当前学科与题型的交集作为初值。
        evidence_types: currentSnapshot?.allowed_evidence_types ?? currentProfile?.allowed_evidence_types ?? assessmentEvidenceOptions(defaultProfile, archetypeDefinition),
        scoring_mode: currentSnapshot?.scoring_policy_snapshot.mode ?? currentProfile?.scoring_policy.mode ?? defaultAssessmentScoringMode(archetypeDefinition, examGradingMode),
        risk_tier: currentSnapshot?.risk_tier ?? currentProfile?.risk_tier ?? "R2"
      };
      savedValues.current = structuredClone(values);
      form.setFieldsValue(values);
      loaded.current = contextKey;
      setLoadedContext(contextKey);
    } catch (currentError) {
      if (isCurrent(version)) setError(formatError(currentError));
    } finally {
      if (isCurrent(version)) { reading.current = false; setLoading(false); }
    }
  }

  function canChangeSelection() {
    return editable && !externalSaving && configurationReady && isCurrent(viewVersion) && !writing.current;
  }

  function updateEvidence(profile: SubjectProfile | undefined, archetype: QuestionArchetype | undefined, reset = false) {
    const available = assessmentEvidenceOptions(profile, archetype);
    const previous = (form.getFieldValue("evidence_types") ?? []) as EvidenceType[];
    const retained = previous.filter((item) => available.includes(item));
    const removed = previous.filter((item) => !available.includes(item));
    form.setFieldValue("evidence_types", reset || !retained.length ? available : retained);
    setEvidenceNotice(removed.length
      ? `已移除不适用于当前学科与题型的证据：${removed.map((item) => evidenceLabels[item]).join("、")}。请重新核对证据选择。`
      : "适用范围或题型已切换，可选证据已更新，请重新核对证据选择。");
  }

  function selectScope(field: "education_stage" | "subject_code", value: EducationStage | SubjectCode) {
    if (!canChangeSelection()) return;
    if (field === "subject_code" && examSubjectCode && value !== examSubjectCode) return;
    const nextStage = field === "education_stage" ? value : form.getFieldValue("education_stage");
    const nextSubject = field === "subject_code" ? value : form.getFieldValue("subject_code");
    const currentId = form.getFieldValue("subject_profile_id");
    const candidates = profiles.filter((item) => item.education_stage === nextStage && item.subject_code === nextSubject);
    const profile = candidates.find((item) => item.id === currentId) ?? candidates[0];
    form.setFieldsValue({ [field]: value, subject_profile_id: profile?.id ?? "" });
    updateEvidence(profile, archetypes.find((item) => item.code === form.getFieldValue("question_archetype")));
  }

  function selectProfile(value: string) {
    if (!canChangeSelection()) return;
    form.setFieldValue("subject_profile_id", value);
    updateEvidence(matchingProfiles.find((item) => item.id === value), archetypes.find((item) => item.code === form.getFieldValue("question_archetype")));
  }

  function selectArchetype(value: QuestionArchetypeCode) {
    if (!canChangeSelection()) return;
    const definition = archetypes.find((item) => item.code === value);
    form.setFieldsValue({
      question_archetype: value,
      scoring_mode: defaultAssessmentScoringMode(definition, examGradingMode)
    });
    updateEvidence(matchingProfiles.find((item) => item.id === form.getFieldValue("subject_profile_id")), definition, true);
  }

  async function save(prepareOnly = false): Promise<(() => Promise<void>) | void> {
    if (!question || !editable || !configurationReady || !isCurrent(viewVersion) || loaded.current !== contextKey || writing.current) return;
    const version = operationVersion.current;
    // 验证也是异步操作；同步忙碌标识禁止重复提交，并在验证结束后再次核对题目和窗口代次。
    writing.current = true;
    setSaving(true);
    try {
      const values = await form.validateFields();
      if (!isCurrent(version)) return;
      const profile = profiles.find((item) => item.id === values.subject_profile_id
        && item.education_stage === values.education_stage && item.subject_code === values.subject_code);
      // 后台只对可识别的考试学科施加模板约束；提交时再核对，不能由禁用下拉或过期按钮状态代替。
      if (examSubjectCode && (values.subject_code !== examSubjectCode || profile?.subject_code !== examSubjectCode)) {
        if (prepareOnly) throw new Error(`本场考试学科为${examSubjectLabel}，请选择该学科的模板后保存`);
        message.error(`本场考试学科为${examSubjectLabel}，请选择该学科的模板后保存`);
        return;
      }
      const archetype = archetypes.find((item) => item.code === values.question_archetype);
      const available = assessmentEvidenceOptions(profile, archetype);
      // 按提交值重算交集，不能依赖按钮状态或尚未完成的表单重绘阻止失效证据写入。
      if (!profile || !archetype || !available.length || !values.evidence_types?.length || values.evidence_types.some((item) => !available.includes(item))) {
        if (prepareOnly) throw new Error("当前学科模板与题型没有可提交的合法证据，请重新选择模板、题型和证据后保存");
        message.error("当前学科模板与题型没有可提交的合法证据，请重新选择模板、题型和证据后保存");
        return;
      }
      if (isAssessmentPolicyBlocked(values.risk_tier, values.question_archetype, values.scoring_mode)) {
        if (prepareOnly) throw new Error("R3 开放题不能使用 AI 快速确认，请改为人工主评或双人评分");
        message.error("R3 开放题不能使用 AI 快速确认，请改为人工主评或双人评分");
        return;
      }
      const profileDefault = profile.scoring_default;
      const input = {
        subject_profile_id: values.subject_profile_id,
        archetype_code: values.question_archetype,
        allowed_evidence_types: values.evidence_types,
        risk_tier: values.risk_tier,
        scoring_policy: {
          ...profileDefault,
          ...savedProfile?.scoring_policy,
          mode: values.scoring_mode,
          require_evidence: true,
          human_review_below_confidence:
            savedProfile?.scoring_policy.human_review_below_confidence
            ?? profileDefault?.human_review_below_confidence
            ?? ["AI_ASSIST", "AI_FAST_CONFIRM"].includes(values.scoring_mode)
        },
        expected_revision: savedProfile?.revision ?? 0
      };
      const persist = async () => {
        if (!isCurrent(version) || loaded.current !== contextKey) throw new Error("判分方式所属题目已变化，请重新核对后保存");
        writing.current = true; setSaving(true);
        try {
          const result = await updateQuestionAssessmentProfile(examId, question.id, input);
          if (!isCurrent(version)) throw new Error("判分方式已提交，请回到原题核对结果");
          savedValues.current = structuredClone(values);
          setSavedProfile(result.assessment_profile);
          message.success(presentation === "inline" ? "判分方式已保存" : "学科评分配置已保存");
          if (presentation === "inline") {
            // 页面内保存后保留同一表单和新修订，继续编辑时不能回到上次读取的版本。
            writing.current = false; setSaving(false);
            onChanged?.();
            return;
          }
          opened.current = false; loaded.current = ""; operationVersion.current++;
          writing.current = false;
          setOpen(false); setSaving(false); setLoadedContext("");
          onChanged?.();
        } finally { if (isCurrent(version)) { writing.current = false; setSaving(false); } }
      };
      if (prepareOnly) return persist;
      await persist();
    } catch (currentError) {
      if (prepareOnly) throw currentError;
      if (isCurrent(version)) message.error(formatError(currentError));
    } finally {
      if (isCurrent(version)) { writing.current = false; setSaving(false); }
    }
  }

  const participantSave = useRef(save); participantSave.current = save;
  const participantDirty = editable && configurationReady && Boolean(savedValues.current) && currentDraftKey !== assessmentDraftKey(savedValues.current ?? {});
  useEffect(() => {
    registerSaveParticipant?.("assessment", { owner: `${examId}:${question?.id ?? "create"}`, dirty: participantDirty, busy: saving,
      prepare: async () => { const persist = await participantSave.current(true); if (!persist) throw new Error("判分方式暂不可保存，请先完成本区配置"); return persist; },
      discard: () => { if (savedValues.current) form.setFieldsValue(structuredClone(savedValues.current)); }
    });
  }, [registerSaveParticipant, examId, question?.id, participantDirty, saving, currentDraftKey, form]);
  useEffect(() => () => registerSaveParticipant?.("assessment", null), [registerSaveParticipant, contextKey]);

  if (presentation === "inline") {
    const savedMode = snapshot?.scoring_policy_snapshot.mode ?? savedProfile?.scoring_policy.mode;
    return <section className="assessment-profile-section is-inline" aria-label="本题判分方式">
      {!question ? <span className="muted">保存题目后设置判分方式</span> : open && <AssessmentProfileDrawerBody
        loading={loading} error={error} onRetry={() => void loadConfiguration()} form={form} inline
        editable={editable} saving={saving || externalSaving} configurationReady={configurationReady} frozen={frozen} canManage={canManage}
        scopeProps={{ examSubjectCode, examSubjectLabel, subject, editable, subjectMismatch, matchingProfiles, archetypes,
          selectScope, selectProfile, selectArchetype }} availableEvidence={availableEvidence} selectedEvidence={selectedEvidence}
        evidenceNotice={evidenceNotice} selectedProfileDefinition={selectedProfileDefinition} selectedArchetypeDefinition={selectedArchetypeDefinition}
        invalidEvidence={invalidEvidence} policyBlocked={policyBlocked} scoringGuidance={scoringGuidance}
        advancedOpen={advancedOpen} onAdvancedChange={setAdvancedOpen}
        actions={<>
          {editable && <Button type="primary" disabled={!canSave || externalSaving} loading={saving} onClick={() => void save()}>保存判分方式</Button>}
          {configurationReady && <span className="assessment-inline-saved">{savedMode ? `${snapshot ? "已冻结" : "已保存"}：${inlineScoringModeOptions.find((item) => item.value === savedMode)?.label ?? savedMode}` : "尚未保存"}</span>}
        </>} />}
    </section>;
  }

  return (
    <section className="assessment-profile-section">
      <AssessmentProfileHeading question={question} effectiveProfile={effectiveProfile} snapshot={snapshot} loading={loading}
        saving={saving} frozen={frozen} canManage={canManage} onOpen={() => void loadConfiguration()} />

      <Drawer
        title={question ? `${question.question_no} · 学科评分配置` : "学科评分配置"}
        width={560}
        open={open}
        closable={!saving}
        maskClosable={!saving}
        onClose={closeConfiguration}
        footer={
          <div className="assessment-drawer-footer">
            <Button disabled={saving} onClick={closeConfiguration}>关闭</Button>
            {editable ? <Button type="primary" icon={<Save size={16} />} disabled={!canSave} loading={saving} onClick={() => void save()}>保存配置</Button> : null}
          </div>
        }
      >
        <AssessmentProfileDrawerBody loading={loading} error={error} onRetry={() => void loadConfiguration()} form={form}
          editable={editable} saving={saving} configurationReady={configurationReady} frozen={frozen} canManage={canManage}
          scopeProps={{ examSubjectCode, examSubjectLabel, subject, editable, subjectMismatch, matchingProfiles, archetypes,
            selectScope, selectProfile, selectArchetype }} availableEvidence={availableEvidence} selectedEvidence={selectedEvidence}
          evidenceNotice={evidenceNotice} selectedProfileDefinition={selectedProfileDefinition}
          selectedArchetypeDefinition={selectedArchetypeDefinition} invalidEvidence={invalidEvidence}
          policyBlocked={policyBlocked} scoringGuidance={scoringGuidance} />
      </Drawer>
    </section>
  );
}
