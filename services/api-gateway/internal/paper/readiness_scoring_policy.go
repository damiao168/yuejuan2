package paper

import (
	"context"
	"fmt"
	"sort"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

type readinessScoringPolicy struct {
	QuestionID string                   `json:"question_id"`
	Policy     assessment.ScoringPolicy `json:"scoring_policy"`
}

type readinessCandidateClass struct {
	StudentID string `json:"student_id"`
	ClassID   string `json:"class_id"`
}

func readinessCandidateIDs(candidates []readinessCandidateClass) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.StudentID)
	}
	sort.Strings(ids)
	unique := ids[:0]
	for _, id := range ids {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique
}

// 准备内容与评估配置分开保存；学生归属、模式、证据要求或置信度变化也必须使复核版本失效。
// 旧内容摘要仍单独计算，用于兼容已经冻结的历史考试，不用于新的准备确认。
func withReadinessReviewFacts(result ReadinessResult, policies []readinessScoringPolicy, candidates []readinessCandidateClass) ReadinessResult {
	ordered := append([]readinessScoringPolicy(nil), policies...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QuestionID < ordered[j].QuestionID })
	classes := append([]readinessCandidateClass(nil), candidates...)
	sort.Slice(classes, func(i, j int) bool {
		if classes[i].StudentID == classes[j].StudentID {
			return classes[i].ClassID < classes[j].ClassID
		}
		return classes[i].StudentID < classes[j].StudentID
	})
	result.candidateClasses = classes
	result.ConfigurationHash = stableContentHash(struct {
		Version       int                       `json:"version"`
		Configuration string                    `json:"configuration_hash"`
		Policies      []readinessScoringPolicy  `json:"scoring_policies"`
		Candidates    []readinessCandidateClass `json:"candidate_classes"`
	}{2, result.ConfigurationHash, ordered, classes})
	return result
}

// 学籍更新不取得考试锁；准备复核需要绑定学生在本场冻结的班级，而不能只比较总人数或学生集合。
func loadReadinessCandidateClasses(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string) ([]readinessCandidateClass, error) {
	query := `
SELECT candidate.student_id::text,candidate.class_id_snapshot::text
FROM exam_candidate_snapshot candidate
WHERE candidate.tenant_id=$1::uuid AND candidate.exam_id=$2::uuid
ORDER BY candidate.student_id::text,candidate.class_id_snapshot::text
`
	if status == "draft" || status == "configured" {
		query = `
SELECT DISTINCT st.id::text,cls.id::text
FROM exam_class ec
JOIN school_class cls ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id AND cls.deleted_at IS NULL
JOIN grade ON grade.tenant_id=cls.tenant_id AND grade.id=cls.grade_id AND grade.deleted_at IS NULL
JOIN student_enrollment enrollment
  ON enrollment.tenant_id=ec.tenant_id AND enrollment.class_id=ec.class_id
  AND enrollment.academic_year_id=cls.academic_year_id
  AND enrollment.status='enrolled' AND enrollment.end_date IS NULL AND enrollment.deleted_at IS NULL
JOIN student st ON st.tenant_id=enrollment.tenant_id AND st.id=enrollment.student_id
  AND st.status='active' AND st.deleted_at IS NULL
WHERE ec.tenant_id=$1::uuid AND ec.exam_id=$2::uuid AND ec.deleted_at IS NULL
ORDER BY st.id::text,cls.id::text
`
	}
	rows, err := queryer.QueryContext(ctx, query, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	classes := []readinessCandidateClass{}
	for rows.Next() {
		var candidate readinessCandidateClass
		if err := rows.Scan(&candidate.StudentID, &candidate.ClassID); err != nil {
			return nil, err
		}
		classes = append(classes, candidate)
	}
	return classes, rows.Err()
}

// 历史 v1/v2 摘要只在考试已冻结且仍有匹配 passed 记录时沿用；准备阶段始终使用完整 v3 事实。
func assessmentReadinessHash(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string, result ReadinessResult) (ReadinessResult, error) {
	facts, err := loadReadinessAssessmentFacts(ctx, queryer, tenantID, examID, status)
	if err != nil {
		return ReadinessResult{}, err
	}
	candidates, err := loadReadinessCandidateClasses(ctx, queryer, tenantID, examID, status)
	if err != nil {
		return ReadinessResult{}, err
	}
	current := withReadinessAssessmentFacts(result, facts, candidates)
	if status == "draft" || status == "configured" || status == "" {
		current = withAssessmentProfileCheck(current, facts)
	}
	if status != "draft" && status != "configured" && status != "" {
		policies := make([]readinessScoringPolicy, len(facts))
		for index, fact := range facts {
			policies[index] = readinessScoringPolicy{QuestionID: fact.QuestionID, Policy: fact.Policy}
		}
		v2 := withReadinessReviewFacts(result, policies, candidates)
		var historicalHash string
		if err := queryer.QueryRowContext(ctx, `
SELECT CASE
  WHEN EXISTS (SELECT 1 FROM exam_readiness_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='passed' AND configuration_hash=$3) THEN $3
  WHEN EXISTS (SELECT 1 FROM exam_readiness_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='passed' AND configuration_hash=$4) THEN $4
  ELSE '' END
`, tenantID, examID, result.ConfigurationHash, v2.ConfigurationHash).Scan(&historicalHash); err != nil {
			return ReadinessResult{}, err
		}
		if historicalHash != "" {
			current.ConfigurationHash = historicalHash
		}
	}
	return current, nil
}

func withAssessmentProfileCheck(current ReadinessResult, facts []readinessAssessmentFact) ReadinessResult {
	missing := []string{}
	contextMissing := false
	for _, fact := range facts {
		if reason, section := assessmentFactBlockingReason(fact); reason != "" {
			missing = append(missing, fact.QuestionID)
			contextMissing = contextMissing || section == "exam"
		}
	}
	// 缺省模板尚未建立是准备阻断项，页面仍需返回其他检查结果，不能变成读取 500。
	assessmentMessage := "题目评估配置完整，已核对有效学科模板、题型和评分策略"
	if len(missing) > 0 {
		assessmentMessage = fmt.Sprintf("有 %d 道题的评估配置待完善，请前往题目页核对学科模板、题型和评分策略", len(missing))
	}
	section := "questions"
	if contextMissing {
		section = "exam"
		assessmentMessage = "考试年级的学段缺失或冲突，请在考试基本信息核对年级和选班后重新匹配评分方案"
	}
	current.Checks = append(current.Checks, ReadinessCheck{Code: "assessment_profiles", Label: "题目评估配置", Passed: len(missing) == 0, Severity: "blocker", Section: section, Message: assessmentMessage})
	if len(missing) > 0 {
		current.Ready = false
	}
	return current
}
