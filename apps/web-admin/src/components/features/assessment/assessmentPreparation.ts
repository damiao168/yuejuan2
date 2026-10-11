import type { AssessmentProfileInput, EducationStage, QuestionArchetype, SubjectProfile } from "../../../api/assessment";
import type { Exam } from "../../../api/exams";
import type { Grade, SchoolClass } from "../../../api/org";
import type { Question } from "../../../api/papers";
import { defaultArchetypeForQuestionType } from "./assessmentPolicy";
import { defaultAssessmentScoringMode } from "./assessmentScoringDefaults";

export function examEducationStage(exam: Pick<Exam, "school_id" | "class_ids" | "exam_session_grade_id">, grades: Grade[], classes: SchoolClass[]): EducationStage | undefined {
  const gradeIds = new Set(classes.filter((item) => exam.class_ids.includes(item.id)).map((item) => item.grade_id));
  if (exam.exam_session_grade_id) gradeIds.add(exam.exam_session_grade_id);
  const stages = new Set(grades.filter((item) => item.school_id === exam.school_id && gradeIds.has(item.id)).map((item) => item.education_stage));
  // 学段必须来自本场年级；查不到或跨学段时要求明确选择，不能把普通高中考试默认成初中。
  return stages.size === 1 ? [...stages][0] : undefined;
}

export function preparationAssessmentInput(question: Question, profile: SubjectProfile | undefined, archetypes: QuestionArchetype[], gradingMode: string): AssessmentProfileInput | undefined {
  const archetype = archetypes.find((item) => item.code === defaultArchetypeForQuestionType(question.question_type));
  if (!profile || !archetype) return undefined;
  const allowed = profile.evidence_policy.allowed_types;
  const evidence = archetype.evidence_types.filter((item) => Array.isArray(allowed) && allowed.includes(item));
  if (!evidence.length) return undefined;
  return { subject_profile_id: profile.id, archetype_code: archetype.code, allowed_evidence_types: [...new Set(evidence)], risk_tier: "R2",
    scoring_policy: { ...profile.scoring_default, mode: defaultAssessmentScoringMode(archetype, gradingMode), require_evidence: true,
      human_review_below_confidence: profile.scoring_default?.human_review_below_confidence ?? true }, expected_revision: 0 };
}

export const preparationScoringModeLabels: Record<string, string> = {
  RULE_AUTO: "按标准答案自动判分", AI_ASSIST: "系统建议分数，教师逐题确认", AI_FAST_CONFIRM: "系统建议分数，教师快速确认",
  HUMAN_PRIMARY: "教师人工评分", DUAL_HUMAN: "两名教师独立评分", MANUAL_ONLY: "仅人工评分"
};
