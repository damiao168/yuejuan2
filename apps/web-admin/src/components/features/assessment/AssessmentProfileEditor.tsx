import { useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Drawer, Form, Select, Space, Spin } from "antd";
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

function subjectFromExam(value: string): SubjectCode {
  const normalized = value.trim().toLowerCase();
  const aliases: Record<string, SubjectCode> = {
    chinese: "chinese", "语文": "chinese",
    math: "mathematics", mathematics: "mathematics", "数学": "mathematics",
    english: "english", "英语": "english",
    physics: "physics", "物理": "physics",
    chemistry: "chemistry", "化学": "chemistry",
    biology: "biology", "生物": "biology",
    history: "history", "历史": "history",
    geography: "geography", "地理": "geography",
    politics: "ethics_politics", ethics_politics: "ethics_politics", "道德与法治": "ethics_politics", "思想政治": "ethics_politics"
  };
  return aliases[normalized] ?? "mathematics";
}

function formatError(error: unknown) {
  return getUserErrorMessage(error, "学科评分配置加载失败");
}

export function AssessmentProfileEditor({
  examId,
  examSubject,
  examStatus,
  question,
  canManage,
  onChanged
}: {
  examId: string;
  examSubject: string;
  examStatus: string;
  question: Question | null;
  canManage: boolean;
  onChanged?: () => void;
}) {
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
  const stage = Form.useWatch("education_stage", form);
  const subject = Form.useWatch("subject_code", form);
  const selectedArchetype = Form.useWatch("question_archetype", form);
  const scoringMode = Form.useWatch("scoring_mode", form);
  const riskTier = Form.useWatch("risk_tier", form);

  const matchingProfiles = useMemo(
    () => profiles.filter((profile) => profile.education_stage === stage && profile.subject_code === subject),
    [profiles, stage, subject]
  );
  const selectedArchetypeDefinition = archetypes.find((item) => item.code === selectedArchetype);
  const policyBlocked = isAssessmentPolicyBlocked(riskTier, selectedArchetype, scoringMode);
  // 已生成评分快照或考试已离开配置阶段时，配置就是运行依据；任一条件成立都必须禁止再改评分策略。
  const frozen = Boolean(snapshot) || !["draft", "configured"].includes(examStatus);
  const editable = canManage && !frozen;
  const effectiveProfile = snapshot ?? savedProfile;

  useEffect(() => {
    setOpen(false);
    setError(undefined);
    setSavedProfile(undefined);
    setSnapshot(undefined);
  }, [question?.id]);

  useEffect(() => {
    if (!open || loading || !matchingProfiles.length) return;
    const current = form.getFieldValue("subject_profile_id");
    if (!matchingProfiles.some((profile) => profile.id === current)) {
      form.setFieldValue("subject_profile_id", matchingProfiles[0].id);
    }
  }, [form, loading, matchingProfiles, open]);

  async function loadConfiguration() {
    if (!question) return;
    setOpen(true);
    setLoading(true);
    setError(undefined);
    try {
      const [profileResult, archetypeResult] = await Promise.all([listSubjectProfiles(), listQuestionArchetypes()]);
      const [currentProfile, currentSnapshot] = await Promise.all([
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
      const defaultSubject = currentSnapshot?.subject_code ?? currentProfile?.subject_code ?? selectedProfileRecord?.subject_code ?? subjectFromExam(examSubject);
      const defaultStage = currentSnapshot?.education_stage ?? currentProfile?.education_stage ?? selectedProfileRecord?.education_stage ?? "junior";
      const selectedProfile = currentSnapshot?.subject_profile_id ?? currentProfile?.subject_profile_id
        ?? profileResult.subject_profiles.find((item) => item.education_stage === defaultStage && item.subject_code === defaultSubject)?.id;
      const defaultProfile = profileResult.subject_profiles.find((item) => item.id === selectedProfile);

      form.setFieldsValue({
        education_stage: defaultStage,
        subject_code: defaultSubject,
        subject_profile_id: selectedProfile ?? "",
        question_archetype: archetype,
        evidence_types: currentSnapshot?.allowed_evidence_types ?? currentProfile?.allowed_evidence_types ?? archetypeDefinition?.evidence_types ?? [],
        scoring_mode: currentSnapshot?.scoring_policy_snapshot.mode ?? currentProfile?.scoring_policy.mode ?? defaultProfile?.scoring_default?.mode ?? archetypeDefinition?.default_scoring_mode ?? "HUMAN_PRIMARY",
        risk_tier: currentSnapshot?.risk_tier ?? currentProfile?.risk_tier ?? "R2"
      });
    } catch (currentError) {
      setError(formatError(currentError));
    } finally {
      setLoading(false);
    }
  }

  function selectArchetype(value: QuestionArchetypeCode) {
    const definition = archetypes.find((item) => item.code === value);
    form.setFieldsValue({
      question_archetype: value,
      evidence_types: definition?.evidence_types ?? [],
      scoring_mode: definition?.default_scoring_mode ?? "HUMAN_PRIMARY"
    });
  }

  async function save() {
    if (!question || !editable) return;
    const values = await form.validateFields();
    if (isAssessmentPolicyBlocked(values.risk_tier, values.question_archetype, values.scoring_mode)) {
      message.error("R3 开放题不能使用 AI 快速确认，请改为人工主评或双人评分");
      return;
    }
    setSaving(true);
    try {
      const profileDefault = profiles.find((profile) => profile.id === values.subject_profile_id)?.scoring_default;
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
      setSavedProfile(result.assessment_profile);
      message.success("学科评分配置已保存");
      setOpen(false);
      onChanged?.();
    } catch (currentError) {
      message.error(formatError(currentError));
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="assessment-profile-section">
      <div>
        <Space size="small">
          <BookOpenCheck size={18} />
          <h2>学科评分配置</h2>
          {effectiveProfile ? <StatusTag tone={snapshot ? "success" : "processing"}>{snapshot ? "已冻结" : "已配置"}</StatusTag> : null}
        </Space>
        <p>{question ? "选择该题适用的学科模板、证据和评分方式。" : "保存题目后可配置学科评分策略。"}</p>
      </div>
      <Button disabled={!question} icon={frozen ? <LockKeyhole size={16} /> : <BookOpenCheck size={16} />} onClick={() => void loadConfiguration()}>
        {canManage && !frozen ? "配置" : "查看配置"}
      </Button>

      <Drawer
        title={question ? `${question.question_no} · 学科评分配置` : "学科评分配置"}
        width={560}
        open={open}
        onClose={() => setOpen(false)}
        footer={
          <div className="assessment-drawer-footer">
            <Button onClick={() => setOpen(false)}>关闭</Button>
            {editable ? <Button type="primary" icon={<Save size={16} />} loading={saving} onClick={() => void save()}>保存配置</Button> : null}
          </div>
        }
      >
        {loading ? <div className="assessment-drawer-loading"><Spin /><span>正在读取评分模板</span></div> : error ? (
          <Alert type="error" showIcon message="配置加载失败" description={error} action={<Button size="small" onClick={() => void loadConfiguration()}>重试</Button>} />
        ) : (
          <Form form={form} layout="vertical" disabled={!editable} requiredMark="optional">
            {frozen ? <Alert className="assessment-policy-alert" type="info" showIcon message="考试配置已冻结" description="进入准备完成阶段后，本题只展示考试快照；后续修改不会覆盖本场考试。" /> : null}
            {!canManage ? <Alert className="assessment-policy-alert" type="info" showIcon message="当前为只读模板" description="学科评分策略由学校管理员维护，阅卷教师按已发布模板执行。" /> : null}

            <div className="assessment-form-section">
              <h3>适用范围</h3>
              <div className="form-grid">
                <Form.Item label="学段" name="education_stage" rules={[{ required: true, message: "请选择学段" }]}>
                  <Select options={educationStageOptions} />
                </Form.Item>
                <Form.Item label="学科" name="subject_code" rules={[{ required: true, message: "请选择学科" }]}>
                  <Select options={subjectOptions} />
                </Form.Item>
              </div>
              <Form.Item label="学科模板版本" name="subject_profile_id" rules={[{ required: true, message: "请选择学科模板" }]}>
                <Select
                  placeholder={matchingProfiles.length ? "选择版本" : "当前学段与学科暂无可用模板"}
                  options={matchingProfiles.map((profile) => ({ label: `${profile.code} · v${profile.version}`, value: profile.id }))}
                />
              </Form.Item>
              <Form.Item label="题型原型" name="question_archetype" rules={[{ required: true, message: "请选择题型原型" }]}>
                <Select
                  options={archetypes.map((item) => ({ label: archetypeLabels[item.code], value: item.code }))}
                  onChange={selectArchetype}
                />
              </Form.Item>
            </div>

            <div className="assessment-form-section">
              <h3>评分证据</h3>
              <Form.Item label="允许使用的证据" name="evidence_types" rules={[{ required: true, message: "至少选择一种评分证据" }]}>
                <Select
                  mode="multiple"
                  placeholder="选择评分时必须引用的证据"
                  options={(selectedArchetypeDefinition?.evidence_types ?? []).map((value) => ({ label: evidenceLabels[value], value }))}
                />
              </Form.Item>
              <p className="assessment-field-help">评分结果将引用证据结构和原图位置，不复制或覆盖原始答卷。</p>
            </div>

            <div className="assessment-form-section">
              <h3>评分策略</h3>
              <div className="form-grid">
                <Form.Item label="考试风险等级" name="risk_tier" rules={[{ required: true, message: "请选择风险等级" }]}>
                  <Select options={riskTierOptions} />
                </Form.Item>
                <Form.Item label="默认评分模式" name="scoring_mode" rules={[{ required: true, message: "请选择评分模式" }]}>
                  <Select options={scoringModeOptions} status={policyBlocked ? "error" : undefined} />
                </Form.Item>
              </div>
              {policyBlocked ? <Alert className="assessment-policy-alert" type="error" showIcon message="该组合不允许保存" description="R3 高风险开放题必须由人工主评或双人评分，AI 只能提供建议或质检信号。" /> : null}
            </div>
          </Form>
        )}
      </Drawer>
    </section>
  );
}
