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
	// 下列字段只解释预览和分流；摘要仍只绑定上面的冻结评估语义。
	QuestionNo      string `json:"question_no,omitempty"`
	AnswerScope     string `json:"answer_scope,omitempty"`
	Source          string `json:"source,omitempty"`
	BlockingReason  string `json:"blocking_reason,omitempty"`
	BlockingSection string `json:"blocking_section,omitempty"`
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
		// 来源和展示原因不改变评分语义；默认值落库后仍需与原预览的 v3 摘要一致。
		ordered[index].QuestionNo, ordered[index].AnswerScope, ordered[index].Source = "", "", ""
		ordered[index].BlockingReason, ordered[index].BlockingSection = "", ""
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QuestionID < ordered[j].QuestionID })
	// v2 的排序和候选归属复制也用于 v3；兼容摘要仍保留原始 v2 结构。
	classes := withReadinessReviewFacts(ReadinessResult{}, nil, candidates).candidateClasses
	result.candidateClasses = classes
	result.ScoringPreview = buildReadinessScoringPreview(facts)
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

// 准备阶段与默认物化共同读取只读的 assessment_effective_question_config，GET 不保存逐题配置。
// 冻结后直接使用评估快照，不能让后来更新的模板内容改变已经确认的考试版本。
func loadReadinessAssessmentFacts(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string) ([]readinessAssessmentFact, error) {
	query := `
SELECT DISTINCT ON (snapshot.question_id) snapshot.question_id::text,
       jsonb_build_object(
         'scoring_policy',snapshot.scoring_policy_snapshot_json,
         'risk_tier',snapshot.risk_tier,
         'allowed_evidence_types',snapshot.allowed_evidence_types,
         'profile_snapshot',snapshot.profile_snapshot_json,
         'archetype_snapshot',snapshot.archetype_snapshot_json,
         'question_no',question.question_no,'answer_scope',question.answer_area->>'answer_scope','source','snapshot')
FROM exam_question_snapshot snapshot
JOIN question ON question.tenant_id=snapshot.tenant_id AND question.exam_id=snapshot.exam_id AND question.id=snapshot.question_id
WHERE snapshot.tenant_id=$1::uuid AND snapshot.exam_id=$2::uuid
  AND question.deleted_at IS NULL AND question.status<>'deleted'
ORDER BY snapshot.question_id,snapshot.snapshot_version DESC
`
	if status == "draft" || status == "configured" || status == "" {
		query = `
SELECT question.id::text,
       jsonb_build_object(
         'scoring_policy',config.scoring_policy_json,
         'risk_tier',config.risk_tier,
         'allowed_evidence_types',config.allowed_evidence_types,
         'profile_snapshot',config.profile_snapshot_json,
         'archetype_snapshot',config.archetype_snapshot_json,
         'question_no',question.question_no,'answer_scope',question.answer_area->>'answer_scope','source',config.source,
         'blocking_reason',config.blocking_reason,'blocking_section',config.blocking_section)
FROM question
CROSS JOIN LATERAL assessment_effective_question_config(question.tenant_id,question.exam_id,question.id) config
WHERE question.tenant_id=$1::uuid AND question.exam_id=$2::uuid
  AND question.deleted_at IS NULL AND question.status<>'deleted'
ORDER BY question.sort_order,question.question_no,question.id
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
