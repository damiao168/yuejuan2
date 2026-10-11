package paper

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

// 保存会进入评估快照的语义内容及学科模板身份，不包含配置修订号、审计时间或新生成的快照记录 ID。
type readinessAssessmentFact struct {
	QuestionID      string                    `json:"question_id"`
	Policy          assessment.ScoringPolicy  `json:"scoring_policy"`
	RiskTier        assessment.RiskTier       `json:"risk_tier"`
	AllowedEvidence []assessment.EvidenceType `json:"allowed_evidence_types"`
	Profile         map[string]any            `json:"profile_snapshot"`
	Archetype       map[string]any            `json:"archetype_snapshot"`
}

func normalizeReadinessAssessmentFact(fact readinessAssessmentFact) readinessAssessmentFact {
	fact.AllowedEvidence = append([]assessment.EvidenceType{}, fact.AllowedEvidence...)
	sort.Slice(fact.AllowedEvidence, func(i, j int) bool { return fact.AllowedEvidence[i] < fact.AllowedEvidence[j] })
	unique := fact.AllowedEvidence[:0]
	for _, evidence := range fact.AllowedEvidence {
		if len(unique) == 0 || unique[len(unique)-1] != evidence {
			unique = append(unique, evidence)
		}
	}
	fact.AllowedEvidence = unique
	return fact
}

// v3 摘要绑定完整评估事实；风险、证据、学科及题型模板变化都必须重新核对。
func withReadinessAssessmentFacts(result ReadinessResult, facts []readinessAssessmentFact, candidates []readinessCandidateClass) ReadinessResult {
	ordered := make([]readinessAssessmentFact, len(facts))
	for index, fact := range facts {
		ordered[index] = normalizeReadinessAssessmentFact(fact)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QuestionID < ordered[j].QuestionID })
	// v2 的排序和候选归属复制也用于 v3；兼容摘要仍保留原始 v2 结构。
	classes := withReadinessReviewFacts(ReadinessResult{}, nil, candidates).candidateClasses
	result.candidateClasses = classes
	result.assessmentFactsHash = stableContentHash(ordered)
	result.ConfigurationHash = stableContentHash(struct {
		Version       int                       `json:"version"`
		Configuration string                    `json:"configuration_hash"`
		Assessment    []readinessAssessmentFact `json:"assessment_facts"`
		Candidates    []readinessCandidateClass `json:"candidate_classes"`
	}{3, result.ConfigurationHash, ordered, classes})
	return result
}

func readinessAssessmentFactsHash(facts []readinessAssessmentFact) string {
	return withReadinessAssessmentFacts(ReadinessResult{}, facts, nil).assessmentFactsHash
}

// 准备阶段按 assessment_apply_default_question_config 的实际规则补齐未落库的有效配置。
// 冻结后直接使用评估快照，不能让后来更新的模板内容改变已经确认的考试版本。
func loadReadinessAssessmentFacts(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string) ([]readinessAssessmentFact, error) {
	query := `
SELECT DISTINCT ON (snapshot.question_id) snapshot.question_id::text,
       jsonb_build_object(
         'scoring_policy',snapshot.scoring_policy_snapshot_json,
         'risk_tier',snapshot.risk_tier,
         'allowed_evidence_types',snapshot.allowed_evidence_types,
         'profile_snapshot',snapshot.profile_snapshot_json,
         'archetype_snapshot',snapshot.archetype_snapshot_json)
FROM exam_question_snapshot snapshot
JOIN question ON question.tenant_id=snapshot.tenant_id AND question.exam_id=snapshot.exam_id AND question.id=snapshot.question_id
WHERE snapshot.tenant_id=$1::uuid AND snapshot.exam_id=$2::uuid
  AND question.deleted_at IS NULL AND question.status<>'deleted'
ORDER BY snapshot.question_id,snapshot.snapshot_version DESC
`
	if status == "draft" || status == "configured" || status == "" {
		query = `
WITH exam_context AS (
  SELECT exam.*,
         CASE lower(btrim(exam.subject))
           WHEN 'math' THEN 'mathematics' WHEN '数学' THEN 'mathematics'
           WHEN 'politics' THEN 'ethics_politics' WHEN 'civics' THEN 'ethics_politics'
           WHEN '政治' THEN 'ethics_politics' WHEN '道德与法治' THEN 'ethics_politics' WHEN '思想政治' THEN 'ethics_politics'
           WHEN '语文' THEN 'chinese' WHEN '英语' THEN 'english' WHEN '物理' THEN 'physics'
           WHEN '化学' THEN 'chemistry' WHEN '生物' THEN 'biology' WHEN '历史' THEN 'history' WHEN '地理' THEN 'geography'
           ELSE lower(btrim(exam.subject)) END AS canonical_subject,
         CASE WHEN EXISTS (
           SELECT 1 FROM exam_class ec
           JOIN school_class cls ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id
           JOIN grade ON grade.tenant_id=cls.tenant_id AND grade.id=cls.grade_id
           WHERE ec.tenant_id=exam.tenant_id AND ec.exam_id=exam.id
             AND ec.deleted_at IS NULL AND cls.deleted_at IS NULL AND grade.deleted_at IS NULL AND grade.level_no>=10
         ) THEN 'senior' ELSE 'junior' END AS effective_stage
  FROM exam WHERE exam.tenant_id=$1::uuid AND exam.id=$2::uuid
)
SELECT question.id::text,
       jsonb_build_object(
         'scoring_policy',COALESCE(config.scoring_policy_json,jsonb_build_object(
           'mode',CASE WHEN exam.grading_mode IN ('double_mark','blind_double_mark') THEN 'DUAL_HUMAN' ELSE archetype.default_scoring_mode END,
           'require_evidence',true,'human_review_below_confidence',true)),
         'risk_tier',COALESCE(config.risk_tier,'R2'),
         'allowed_evidence_types',COALESCE(config.allowed_evidence_types,(
           SELECT COALESCE(jsonb_agg(value ORDER BY value),'[]'::jsonb)
           FROM jsonb_array_elements_text(archetype.evidence_types_json) evidence(value)
           WHERE (profile.evidence_policy_json->'allowed_types') ? evidence.value)),
         'profile_snapshot',jsonb_build_object(
           'id',profile.id,'code',profile.code,'education_stage',profile.education_stage,
           'subject_code',profile.subject_code,'version',profile.version,
           'parser_policy',profile.parser_policy_json,'evidence_policy',profile.evidence_policy_json,
           'scoring_default',profile.scoring_default_json),
         'archetype_snapshot',jsonb_build_object(
           'code',archetype.code,'response_schema',archetype.response_schema_json,
           'evidence_types',archetype.evidence_types_json,'default_scoring_mode',archetype.default_scoring_mode))
FROM question
JOIN exam_context exam ON exam.tenant_id=question.tenant_id AND exam.id=question.exam_id
LEFT JOIN question_assessment_config config
  ON config.tenant_id=question.tenant_id AND config.exam_id=question.exam_id AND config.question_id=question.id
LEFT JOIN question_archetype archetype ON archetype.code=COALESCE(config.archetype_code,CASE question.question_type
  WHEN 'single_choice' THEN 'selected_response' WHEN 'multiple_choice' THEN 'selected_response' WHEN 'true_false' THEN 'selected_response'
  WHEN 'fill_blank' THEN 'exact_text' WHEN 'numeric' THEN 'numeric_expression' WHEN 'formula' THEN 'numeric_expression'
  WHEN 'calculation' THEN 'structured_steps' WHEN 'short_answer' THEN 'short_constructed'
  WHEN 'essay' THEN 'extended_response' WHEN 'discussion' THEN 'extended_response' ELSE 'structured_steps' END)
LEFT JOIN LATERAL (
  SELECT candidate.* FROM subject_profile candidate
  WHERE candidate.tenant_id=question.tenant_id
    AND ((config.id IS NOT NULL AND candidate.id=config.subject_profile_id)
      OR (config.id IS NULL AND candidate.education_stage=exam.effective_stage
        AND candidate.subject_code=exam.canonical_subject AND candidate.status='active'))
  ORDER BY candidate.version DESC LIMIT 1
) profile ON true
WHERE question.tenant_id=$1::uuid AND question.exam_id=$2::uuid
  AND question.deleted_at IS NULL AND question.status<>'deleted'
ORDER BY question.id
`
	}
	rows, err := queryer.QueryContext(ctx, query, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []readinessAssessmentFact{}
	for rows.Next() {
		var fact readinessAssessmentFact
		var raw []byte
		if err := rows.Scan(&fact.QuestionID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &fact); err != nil {
			return nil, fmt.Errorf("decode readiness assessment fact: %w", err)
		}
		facts = append(facts, normalizeReadinessAssessmentFact(fact))
	}
	return facts, rows.Err()
}
