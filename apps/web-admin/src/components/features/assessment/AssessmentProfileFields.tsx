import { Alert, Button, Form, Select, Space, Spin, type FormInstance } from "antd";
import { BookOpenCheck, LockKeyhole } from "lucide-react";
import { type ReactNode } from "react";
import { type AssessmentProfile, type AssessmentRiskTier, type AssessmentScoringMode, type AssessmentSnapshot, type EducationStage, type EvidenceType, type QuestionArchetype, type QuestionArchetypeCode, type SubjectCode, type SubjectProfile } from "../../../api/assessment";
import { getUserErrorMessage } from "../../../api/client";
import type { Question } from "../../../api/papers";
import { StatusTag } from "../../StatusTag";
import { assessmentScoringGuidance } from "./assessmentScoringDefaults";
import { normalizeAssessmentSubject } from "./assessmentSubjectScope";


export interface AssessmentFormValues {
  education_stage: EducationStage;
  subject_code: SubjectCode;
  subject_profile_id: string;
  question_archetype: QuestionArchetypeCode;
  evidence_types: EvidenceType[];
  scoring_mode: AssessmentScoringMode;
  risk_tier: AssessmentRiskTier;
}

export function assessmentDraftKey(values: Partial<AssessmentFormValues>) {
  return JSON.stringify([values.education_stage, values.subject_code, values.subject_profile_id, values.question_archetype, values.evidence_types ?? [], values.scoring_mode, values.risk_tier]);
}

export const educationStageOptions = [
  { label: "初中", value: "junior" },
  { label: "高中", value: "senior" }
];

export const subjectOptions = [
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

export const archetypeLabels: Record<QuestionArchetypeCode, string> = {
  selected_response: "选择作答",
  exact_text: "精确文本",
  numeric_expression: "数值与表达式",
  structured_steps: "分步解答",
  short_constructed: "简短构造回答",
  extended_response: "开放论述 / 写作",
  diagram_graph: "作图与图像",
  table_experiment: "表格与实验"
};

export const evidenceLabels: Record<EvidenceType, string> = {
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

export const scoringModeOptions: { label: string; value: AssessmentScoringMode }[] = [
  { label: "规则自动评分", value: "RULE_AUTO" },
  { label: "AI 辅助人工评分", value: "AI_ASSIST" },
  { label: "AI 快速确认", value: "AI_FAST_CONFIRM" },
  { label: "人工主评", value: "HUMAN_PRIMARY" },
  { label: "双人独立评分", value: "DUAL_HUMAN" },
  { label: "仅人工评分", value: "MANUAL_ONLY" }
];

export const riskTierOptions: { label: string; value: AssessmentRiskTier }[] = [
  { label: "R1 · 课堂与形成性评价", value: "R1" },
  { label: "R2 · 校内总结性考试", value: "R2" },
  { label: "R3 · 高风险考试", value: "R3" }
];

export const inlineScoringModeOptions = scoringModeOptions.map((option) => ({ ...option, label: ({
  RULE_AUTO: "按规则自动判分",
  AI_ASSIST: "AI 辅助，教师评分",
  AI_FAST_CONFIRM: "AI 评分，教师快速确认",
  HUMAN_PRIMARY: "教师主评",
  DUAL_HUMAN: "两位教师独立评分",
  MANUAL_ONLY: "仅由教师评分"
} satisfies Record<AssessmentScoringMode, string>)[option.value] }));

export function formatError(error: unknown) {
  return getUserErrorMessage(error, "学科评分配置加载失败");
}

// 写入证据必须同时符合学科和题型；只按题型全选会让合法题目的默认配置被后台拒绝。
export function assessmentEvidenceOptions(profile: SubjectProfile | undefined, archetype: QuestionArchetype | undefined): EvidenceType[] {
  const values = profile?.evidence_policy.allowed_types;
  const allowed = new Set(Array.isArray(values) ? values.filter((item): item is string => typeof item === "string") : []);
  return [...new Set(archetype?.evidence_types.filter((item) => allowed.has(item)) ?? [])];
}

export function AssessmentProfileHeading({ question, effectiveProfile, snapshot, loading, saving, frozen, canManage, onOpen }: Readonly<{
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

export function AssessmentScopeFields({ examSubjectCode, examSubjectLabel, subject, editable, subjectMismatch, matchingProfiles, archetypes,
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

export function AssessmentProfileDrawerBody({ loading, error, onRetry, form, editable, saving, configurationReady, frozen, canManage,
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
