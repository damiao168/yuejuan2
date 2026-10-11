package assessment

import "context"

type ExamAssessmentSummary struct {
	ExamID                  string      `json:"exam_id"`
	SubjectCode             SubjectCode `json:"subject_code"`
	RiskTier                RiskTier    `json:"risk_tier"`
	ConfiguredQuestionCount int         `json:"configured_question_count"`
	FrozenQuestionCount     int         `json:"frozen_question_count"`
	Source                  string      `json:"source"`
}

// GetExamAssessmentSummary 为工作区和发布门禁返回租户范围内的权威聚合结果。冻结快照优先于可变题目配置；调用方不得从考试级评分模式推断风险。
func (s *MemoryStore) GetExamAssessmentSummary(_ context.Context, tenantID string, examID string) (ExamAssessmentSummary, error) {
	if tenantID == "" || examID == "" {
		return ExamAssessmentSummary{}, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := ExamAssessmentSummary{ExamID: examID, Source: "configuration"}
	for _, config := range s.configs {
		if config.TenantID != tenantID || config.ExamID != examID {
			continue
		}
		result.ConfiguredQuestionCount++
		risk, subject := config.RiskTier, config.SubjectCode
		if snapshot, ok := s.snapshots[assessmentKey(tenantID, examID, config.QuestionID)]; ok {
			result.FrozenQuestionCount++
			risk, subject = snapshot.RiskTier, snapshot.SubjectCode
		}
		if result.SubjectCode == "" {
			result.SubjectCode = subject
		} else if result.SubjectCode != subject {
			return ExamAssessmentSummary{}, ErrInvalidInput
		}
		result.RiskTier = higherRisk(result.RiskTier, risk)
	}
	if result.ConfiguredQuestionCount == 0 || !result.RiskTier.Valid() || !result.SubjectCode.Valid() {
		return ExamAssessmentSummary{}, ErrNotFound
	}
	if result.FrozenQuestionCount == result.ConfiguredQuestionCount {
		result.Source = "snapshot"
	} else if result.FrozenQuestionCount > 0 {
		result.Source = "mixed"
	}
	return result, nil
}

func (s *PostgresStore) GetExamAssessmentSummary(ctx context.Context, tenantID string, examID string) (ExamAssessmentSummary, error) {
	if tenantID == "" || examID == "" {
		return ExamAssessmentSummary{}, ErrInvalidInput
	}
	result := ExamAssessmentSummary{ExamID: examID}
	var riskRank, subjectCount int
	err := s.db.QueryRowContext(ctx, `
WITH latest_snapshot AS (
  SELECT DISTINCT ON (question_id)
         question_id, risk_tier, profile_snapshot_json
  FROM exam_question_snapshot
  WHERE tenant_id = $1 AND exam_id = $2
  ORDER BY question_id, snapshot_version DESC
), effective AS (
  SELECT config.question_id,
         COALESCE(snapshot.risk_tier, config.risk_tier) AS risk_tier,
         COALESCE(snapshot.profile_snapshot_json->>'subject_code', profile.subject_code) AS subject_code,
         snapshot.question_id IS NOT NULL AS frozen
  FROM question_assessment_config config
  JOIN subject_profile profile
    ON profile.tenant_id = config.tenant_id AND profile.id = config.subject_profile_id
  LEFT JOIN latest_snapshot snapshot ON snapshot.question_id = config.question_id
  WHERE config.tenant_id = $1 AND config.exam_id = $2
)
SELECT COUNT(*)::int,
       COUNT(*) FILTER (WHERE frozen)::int,
       COALESCE(MIN(subject_code), ''),
       COUNT(DISTINCT subject_code)::int,
       COALESCE(MAX(CASE risk_tier WHEN 'R3' THEN 3 WHEN 'R2' THEN 2 WHEN 'R1' THEN 1 ELSE 0 END), 0)::int
FROM effective
`, tenantID, examID).Scan(&result.ConfiguredQuestionCount, &result.FrozenQuestionCount, &result.SubjectCode, &subjectCount, &riskRank)
	if err != nil {
		return ExamAssessmentSummary{}, mapStoreError(err)
	}
	result.RiskTier = riskFromRank(riskRank)
	if result.ConfiguredQuestionCount == 0 || subjectCount != 1 || !result.RiskTier.Valid() || !result.SubjectCode.Valid() {
		return ExamAssessmentSummary{}, ErrNotFound
	}
	result.Source = "configuration"
	if result.FrozenQuestionCount == result.ConfiguredQuestionCount {
		result.Source = "snapshot"
	} else if result.FrozenQuestionCount > 0 {
		result.Source = "mixed"
	}
	return result, nil
}

func higherRisk(current RiskTier, candidate RiskTier) RiskTier {
	if riskRank(candidate) > riskRank(current) {
		return candidate
	}
	return current
}

func riskRank(value RiskTier) int {
	switch value {
	case RiskR3:
		return 3
	case RiskR2:
		return 2
	case RiskR1:
		return 1
	default:
		return 0
	}
}

func riskFromRank(rank int) RiskTier {
	switch rank {
	case 3:
		return RiskR3
	case 2:
		return RiskR2
	case 1:
		return RiskR1
	default:
		return ""
	}
}
