import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, App, Button, Drawer, Form, Select, Space, Spin, type FormInstance } from "antd";
import { BookOpenCheck, LockKeyhole, Save } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import {
  getQuestionAssessmentProfile,
  getQuestionAssessmentSnapshot,
  listQuestionArchetypes,
  listSubjectProfiles,
  updateQuestionAssessmentProfile,
  type AssessmentProfile,
  type AssessmentRiskTier,
  type AssessmentScoringMode,
  type AssessmentSnapshot,
  type EducationStage,
  type EvidenceType,
  type QuestionArchetype,
  type QuestionArchetypeCode,
  type SubjectCode,
  type SubjectProfile
} from "../../../api/assessment";
import type { Question } from "../../../api/papers";
import { StatusTag } from "../../StatusTag";
import { defaultArchetypeForQuestionType, isAssessmentPolicyBlocked } from "./assessmentPolicy";
import { assessmentScoringGuidance, defaultAssessmentScoringMode } from "./assessmentScoringDefaults";
import { normalizeAssessmentSubject } from "./assessmentSubjectScope";

interface AssessmentFormValues {
  education_stage: EducationStage;
  subject_code: SubjectCode;
  subject_profile_id: string;
  question_archetype: QuestionArchetypeCode;
  evidence_types: EvidenceType[];
  scoring_mode: AssessmentScoringMode;
  risk_tier: AssessmentRiskTier;
}

const educationStageOptions = [
  { label: "初中", value: "junior" },
  { label: "高中", value: "senior" }
];

const subjectOptions = [
  { label: "语文", value: "chinese" },
  { label: "数学", value: "mathematics" },
  { label: "英语", value: "english" },
  { label: "物理", value: "physics" },
  { label: "化学", value: "chemistry" },
  { label: "生物", value: "biology" },
  { label: "历史", value: "history" },
  { label: "地理", value: "geography" },
  { label: "道德与法治 / 思想政治", value: "ethics_politics" }
];

const archetypeLabels: Record<QuestionArchetypeCode, string> = {
  selected_response: "选择作答",
  exact_text: "精确文本",
  numeric_expression: "数值与表达式",
  structured_steps: "分步解答",
  short_constructed: "简短构造回答",
  extended_response: "开放论述 / 写作",
  diagram_graph: "作图与图像",
  table_experiment: "表格与实验"
};

const evidenceLabels: Record<EvidenceType, string> = {
  selected_option: "选择项",
  exact_text: "精确文本",
  text_span: "原文片段",
  numeric_value: "数值",
  concept: "概念命中",
  relation: "关系与因果",
  math_expression: "数学表达式",
  math_step: "推导步骤",
  unit_value: "数值与单位",
  chemical_equation: "化学方程式",
  diagram_feature: "图形特征",
  table_cell: "表格单元"
};

const scoringModeOptions: { label: string; value: AssessmentScoringMode }[] = [
  { label: "规则自动评分", value: "RULE_AUTO" },
  { label: "AI 辅助人工评分", value: "AI_ASSIST" },
  { label: "AI 快速确认", value: "AI_FAST_CONFIRM" },
  { label: "人工主评", value: "HUMAN_PRIMARY" },
  { label: "双人独立评分", value: "DUAL_HUMAN" },
  { label: "仅人工评分", value: "MANUAL_ONLY" }
];

const riskTierOptions: { label: string; value: AssessmentRiskTier }[] = [
  { label: "R1 · 课堂与形成性评价", value: "R1" },
  { label: "R2 · 校内总结性考试", value: "R2" },
  { label: "R3 · 高风险考试", value: "R3" }
];

const inlineScoringModeOptions = scoringModeOptions.map((option) => ({ ...option, label: ({
  RULE_AUTO: "按规则自动判分",
  AI_ASSIST: "AI 辅助，教师评分",
  AI_FAST_CONFIRM: "AI 评分，教师快速确认",
  HUMAN_PRIMARY: "教师主评",
  DUAL_HUMAN: "两位教师独立评分",
  MANUAL_ONLY: "仅由教师评分"
} satisfies Record<AssessmentScoringMode, string>)[option.value] }));

function formatError(error: unknown) {
  return getUserErrorMessage(error, "学科评分配置加载失败");
}

// 写入证据必须同时符合学科和题型；只按题型全选会让合法题目的默认配置被后台拒绝。
function assessmentEvidenceOptions(profile: SubjectProfile | undefined, archetype: QuestionArchetype | undefined): EvidenceType[] {
  const values = profile?.evidence_policy.allowed_types;
  const allowed = new Set(Array.isArray(values) ? values.filter((item): item is string => typeof item === "string") : []);
  return [...new Set(archetype?.evidence_types.filter((item) => allowed.has(item)) ?? [])];
}

function AssessmentProfileHeading({ question, effectiveProfile, snapshot, loading, saving, frozen, canManage, onOpen }: Readonly<{
  question: Question | null;
  effectiveProfile?: AssessmentProfile | AssessmentSnapshot;
  snapshot?: AssessmentSnapshot;
  loading: boolean;
  saving: boolean;
  frozen: boolean;
  canManage: boolean;
  onOpen: () => void;
}>) {
  return <>
    <div>
      <Space size="small">
        <BookOpenCheck size={18} />
        <h2>学科评分配置</h2>
        {effectiveProfile ? <StatusTag tone={snapshot ? "success" : "processing"}>{snapshot ? "已冻结" : "已配置"}</StatusTag> : null}
      </Space>
      <p>{question ? "选择该题适用的学科模板、证据和评分方式。" : "保存题目后可配置学科评分策略。"}</p>
    </div>
    <Button disabled={!question || loading || saving} icon={frozen ? <LockKeyhole size={16} /> : <BookOpenCheck size={16} />} onClick={onOpen}>
      {canManage && !frozen ? "配置" : "查看配置"}
    </Button>
  </>;
}

function AssessmentScopeFields({ examSubjectCode, examSubjectLabel, subject, editable, subjectMismatch, matchingProfiles, archetypes,
  selectScope, selectProfile, selectArchetype }: Readonly<{
  examSubjectCode: ReturnType<typeof normalizeAssessmentSubject>;
  examSubjectLabel?: string;
  subject?: SubjectCode;
  editable: boolean;
  subjectMismatch: boolean;
  matchingProfiles: SubjectProfile[];
  archetypes: QuestionArchetype[];
  selectScope: (field: "education_stage" | "subject_code", value: EducationStage | SubjectCode) => void;
  selectProfile: (value: string) => void;
  selectArchetype: (value: QuestionArchetypeCode) => void;
}>) {
  return <div className="assessment-form-section">
    <h3>适用范围</h3>
    <div className="form-grid">
      <Form.Item label="学段" name="education_stage" rules={[{ required: true, message: "请选择学段" }]}>
        <Select options={educationStageOptions} onChange={(value: EducationStage) => selectScope("education_stage", value)} />
      </Form.Item>
      <Form.Item label="学科" name="subject_code" rules={[{ required: true, message: "请选择学科" }]}>
        <Select
          // 未知学科须继承 Form 的只读和保存中状态；显式 false 会覆盖 Ant Design 的表单禁用值。
          disabled={examSubjectCode ? true : undefined}
          options={examSubjectCode ? subjectOptions.filter((item) => item.value === examSubjectCode || item.value === subject) : subjectOptions}
          onChange={(value: SubjectCode) => selectScope("subject_code", value)}
        />
      </Form.Item>
    </div>
    <p className="assessment-field-help">{examSubjectCode
      ? `学科由考试设置确定：${examSubjectLabel}。可选择该学科的学段和模板版本。`
      : "当前考试学科没有对应的固定学科代码，请按本题选择适用学科、学段和模板版本。"}</p>
    {editable && subjectMismatch && examSubjectCode ? <Alert type="error" showIcon message="已有配置学科与考试不一致" description={`原配置已保留。保存前请改用本场考试的${examSubjectLabel}模板，并重新核对证据。`} action={<Button onClick={() => selectScope("subject_code", examSubjectCode)}>改用考试学科</Button>} /> : null}
    <Form.Item label="学科模板版本" name="subject_profile_id" rules={[{ required: true, message: "请选择学科模板" }]}>
      <Select placeholder={matchingProfiles.length ? "选择版本" : "当前学段与学科暂无可用模板"}
        options={matchingProfiles.map((profile) => ({ label: `${profile.code} · v${profile.version}`, value: profile.id }))} onChange={selectProfile} />
    </Form.Item>
    <Form.Item label="题型原型" name="question_archetype" rules={[{ required: true, message: "请选择题型原型" }]}>
      <Select options={archetypes.map((item) => ({ label: archetypeLabels[item.code], value: item.code }))} onChange={selectArchetype} />
    </Form.Item>
  </div>;
}

function AssessmentProfileDrawerBody({ loading, error, onRetry, form, editable, saving, configurationReady, frozen, canManage,
  scopeProps, availableEvidence, selectedEvidence, evidenceNotice, selectedProfileDefinition, selectedArchetypeDefinition,
  invalidEvidence, policyBlocked, scoringGuidance, inline = false, actions, advancedOpen, onAdvancedChange }: Readonly<{
  loading: boolean;
  error?: string;
  onRetry: () => void;
  form: FormInstance<AssessmentFormValues>;
  editable: boolean;
  saving: boolean;
  configurationReady: boolean;
  frozen: boolean;
  canManage: boolean;
  scopeProps: Parameters<typeof AssessmentScopeFields>[0];
  availableEvidence: EvidenceType[];
  selectedEvidence: EvidenceType[];
  evidenceNotice: string;
  selectedProfileDefinition?: SubjectProfile;
  selectedArchetypeDefinition?: QuestionArchetype;
  invalidEvidence: boolean;
  policyBlocked: boolean;
  scoringGuidance: ReturnType<typeof assessmentScoringGuidance>;
  inline?: boolean;
  actions?: ReactNode;
  advancedOpen?: boolean;
  onAdvancedChange?: (open: boolean) => void;
}>) {
  // 读取未完成或失败时不挂载旧字段；重试成功后仍使用父组件的同一个表单实例。
  if (loading) return <div className="assessment-drawer-loading"><Spin /><span>正在读取评分模板</span></div>;
  if (error) return <Alert type="error" showIcon message="配置加载失败" description={error} action={<Button size="small" onClick={onRetry}>重试</Button>} />;
  const advancedFields = <>
    <AssessmentScopeFields {...scopeProps} />

    <div className="assessment-form-section">
      <h3>评分证据</h3>
      <Form.Item label="允许使用的证据" name="evidence_types" rules={[{ required: true, message: "至少选择一种评分证据" }]}>
        <Select mode="multiple" placeholder="选择评分时必须引用的证据"
          options={(editable ? availableEvidence : [...new Set([...availableEvidence, ...selectedEvidence])]).map((value: EvidenceType) => ({ label: evidenceLabels[value], value }))} />
      </Form.Item>
      <p className="assessment-field-help">可选证据同时符合当前学科模板与题型。评分结果将引用证据结构和原图位置，不复制或覆盖原始答卷。</p>
      {editable && evidenceNotice ? <Alert type="info" showIcon message="请核对更新后的评分证据" description={evidenceNotice} /> : null}
      {editable && configurationReady && (!selectedProfileDefinition || !selectedArchetypeDefinition || !availableEvidence.length) ? <Alert type="error" showIcon message="当前模板与题型没有可用的评分证据" description="请重新选择学段、学科模板或题型；没有共同支持的证据时不能保存配置。" /> : null}
      {editable && invalidEvidence ? <Alert type="warning" showIcon message="已有证据与当前模板或题型不兼容" description="已保存的选择已保留，请重新选择当前支持的证据后再保存。" /> : null}
    </div>

    <div className="assessment-form-section">
      <h3>评分策略</h3>
      <div className="form-grid">
        <Form.Item label="考试风险等级" name="risk_tier" rules={[{ required: true, message: "请选择风险等级" }]}>
          <Select options={riskTierOptions} />
        </Form.Item>
        {!inline && <Form.Item label="默认评分模式" name="scoring_mode" rules={[{ required: true, message: "请选择评分模式" }]}>
          <Select options={scoringModeOptions} status={policyBlocked ? "error" : undefined} />
        </Form.Item>}
      </div>
      {!inline && policyBlocked ? <Alert className="assessment-policy-alert" type="error" showIcon message="该组合不允许保存" description="R3 高风险开放题必须由人工主评或双人评分，AI 只能提供建议或质检信号。" /> : null}
      {!inline && scoringGuidance ? <Alert type={scoringGuidance.type} showIcon message={scoringGuidance.message} description={scoringGuidance.description} /> : null}
    </div>
  </>;
  return <Form form={form} layout="vertical" disabled={!editable || saving || !configurationReady} requiredMark="optional">
    {frozen ? <Alert className="assessment-policy-alert" type="info" showIcon message="考试配置已冻结" description="进入准备完成阶段后，本题只展示考试快照；后续修改不会覆盖本场考试。" /> : null}
    {!canManage ? <Alert className="assessment-policy-alert" type="info" showIcon message="当前为只读模板" description="学科评分策略由学校管理员维护，阅卷教师按已发布模板执行。" /> : null}
    {inline ? <>
      <div className="assessment-inline-controls">
        <Form.Item label="判分方式" name="scoring_mode" rules={[{ required: true, message: "请选择判分方式" }]}>
          <Select options={inlineScoringModeOptions} status={policyBlocked ? "error" : undefined} />
        </Form.Item>
        <div className="assessment-inline-actions">{actions}</div>
      </div>
      {policyBlocked && <Alert className="assessment-policy-alert" type="error" showIcon message="该组合不允许保存" description="R3 高风险开放题必须由人工主评或双人评分，AI 只能提供建议或质检信号。" />}
      {scoringGuidance?.type === "warning" && <Alert type="warning" showIcon message={scoringGuidance.message} description={scoringGuidance.description} />}
      {/* 字段保持注册，折叠只改变可见性；保存仍校验模板、证据及风险限制。 */}
      <details className="assessment-advanced-settings" open={advancedOpen} onToggle={(event) => onAdvancedChange?.(event.currentTarget.open)}>
        <summary>模板、证据与风险设置</summary>
        {advancedFields}
      </details>
    </> : advancedFields}
  </Form>;
}

export function AssessmentProfileEditor({
  examId,
  examSubject,
  examStatus,
  examGradingMode,
  question,
  canManage,
  onChanged,
  presentation = "drawer",
  active = true
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

      form.setFieldsValue({
        education_stage: defaultStage,
        subject_code: defaultSubject,
        subject_profile_id: selectedProfile ?? "",
        question_archetype: archetype,
        // 已保存配置和冻结快照保留原选择，只有未落库配置使用当前学科与题型的交集作为初值。
        evidence_types: currentSnapshot?.allowed_evidence_types ?? currentProfile?.allowed_evidence_types ?? assessmentEvidenceOptions(defaultProfile, archetypeDefinition),
        scoring_mode: currentSnapshot?.scoring_policy_snapshot.mode ?? currentProfile?.scoring_policy.mode ?? defaultAssessmentScoringMode(archetypeDefinition, examGradingMode),
        risk_tier: currentSnapshot?.risk_tier ?? currentProfile?.risk_tier ?? "R2"
      });
      loaded.current = contextKey;
      setLoadedContext(contextKey);
    } catch (currentError) {
      if (isCurrent(version)) setError(formatError(currentError));
    } finally {
      if (isCurrent(version)) { reading.current = false; setLoading(false); }
    }
  }

  function canChangeSelection() {
    return editable && configurationReady && isCurrent(viewVersion) && !writing.current;
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

  async function save() {
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
        message.error(`本场考试学科为${examSubjectLabel}，请选择该学科的模板后保存`);
        return;
      }
      const archetype = archetypes.find((item) => item.code === values.question_archetype);
      const available = assessmentEvidenceOptions(profile, archetype);
      // 按提交值重算交集，不能依赖按钮状态或尚未完成的表单重绘阻止失效证据写入。
      if (!profile || !archetype || !available.length || !values.evidence_types?.length || values.evidence_types.some((item) => !available.includes(item))) {
        message.error("当前学科模板与题型没有可提交的合法证据，请重新选择模板、题型和证据后保存");
        return;
      }
      if (isAssessmentPolicyBlocked(values.risk_tier, values.question_archetype, values.scoring_mode)) {
        message.error("R3 开放题不能使用 AI 快速确认，请改为人工主评或双人评分");
        return;
      }
      const profileDefault = profile.scoring_default;
      const result = await updateQuestionAssessmentProfile(examId, question.id, {
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
      });
      if (!isCurrent(version)) return;
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
    } catch (currentError) {
      if (isCurrent(version)) message.error(formatError(currentError));
    } finally {
      if (isCurrent(version)) { writing.current = false; setSaving(false); }
    }
  }

  if (presentation === "inline") {
    const savedMode = snapshot?.scoring_policy_snapshot.mode ?? savedProfile?.scoring_policy.mode;
    return <section className="assessment-profile-section is-inline" aria-label="本题判分方式">
      {!question ? <span className="muted">保存题目后设置判分方式</span> : open && <AssessmentProfileDrawerBody
        loading={loading} error={error} onRetry={() => void loadConfiguration()} form={form} inline
        editable={editable} saving={saving} configurationReady={configurationReady} frozen={frozen} canManage={canManage}
        scopeProps={{ examSubjectCode, examSubjectLabel, subject, editable, subjectMismatch, matchingProfiles, archetypes,
          selectScope, selectProfile, selectArchetype }} availableEvidence={availableEvidence} selectedEvidence={selectedEvidence}
        evidenceNotice={evidenceNotice} selectedProfileDefinition={selectedProfileDefinition} selectedArchetypeDefinition={selectedArchetypeDefinition}
        invalidEvidence={invalidEvidence} policyBlocked={policyBlocked} scoringGuidance={scoringGuidance}
        advancedOpen={advancedOpen} onAdvancedChange={setAdvancedOpen}
        actions={<>
          {editable && <Button type="primary" disabled={!canSave} loading={saving} onClick={() => void save()}>保存判分方式</Button>}
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
