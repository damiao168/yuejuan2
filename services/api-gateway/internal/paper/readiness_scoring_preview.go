package paper

import "edugrade-enterprise/services/api-gateway/internal/assessment"

type ReadinessScoringProfile struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	EducationStage string `json:"education_stage"`
	SubjectCode    string `json:"subject_code"`
	Version        int    `json:"version"`
}

// ReadinessScoringPreview 展示本次准备摘要实际使用的逐题方案；source 不表示已经获得分数。
type ReadinessScoringPreview struct {
	QuestionID      string                    `json:"question_id"`
	QuestionNo      string                    `json:"question_no"`
	ScoringPolicy   assessment.ScoringPolicy  `json:"scoring_policy"`
	ProcessingMode  assessment.ScoringMode    `json:"processing_mode"`
	Source          string                    `json:"source"`
	Profile         ReadinessScoringProfile   `json:"profile"`
	ArchetypeCode   string                    `json:"archetype_code"`
	RiskTier        assessment.RiskTier       `json:"risk_tier"`
	AllowedEvidence []assessment.EvidenceType `json:"allowed_evidence_types"`
	Reason          string                    `json:"reason"`
	BlockingReason  string                    `json:"blocking_reason,omitempty"`
	BlockingSection string                    `json:"blocking_section,omitempty"`
}

func assessmentFactBlockingReason(fact readinessAssessmentFact) (string, string) {
	if fact.BlockingReason != "" {
		return fact.BlockingReason, fact.BlockingSection
	}
	if !fact.Policy.Mode.Valid() || !fact.RiskTier.Valid() || fact.Profile["id"] == nil || fact.Archetype["code"] == nil {
		return "请在题目页核对学科模板、题型和评分策略", "questions"
	}
	return "", ""
}

func buildReadinessScoringPreview(facts []readinessAssessmentFact) []ReadinessScoringPreview {
	preview := make([]ReadinessScoringPreview, 0, len(facts))
	for _, fact := range facts {
		fact = normalizeReadinessAssessmentFact(fact)
		profile := ReadinessScoringProfile{}
		profile.ID, _ = fact.Profile["id"].(string)
		profile.Code, _ = fact.Profile["code"].(string)
		profile.EducationStage, _ = fact.Profile["education_stage"].(string)
		profile.SubjectCode, _ = fact.Profile["subject_code"].(string)
		if version, ok := fact.Profile["version"].(float64); ok {
			profile.Version = int(version)
		}
		archetype, _ := fact.Archetype["code"].(string)
		reason := "沿用已保存的逐题评分方案"
		switch fact.Source {
		case "default":
			reason = "按考试学段、学科和题型匹配默认方案，准备确认时保存并冻结"
		case "snapshot":
			reason = "沿用准备确认时冻结的评分方案"
		}
		// 模式只决定处理路径；规则仍可能需复核，AI 建议也必须经过教师确认。
		switch fact.Policy.Mode {
		case assessment.ScoringRuleAuto:
			reason += "；按规则尝试评分，仍须核对规则、答案和答题区域，不能判定时需人工复核"
		case assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm:
			reason += "；AI 提供建议，须由教师确认"
		case assessment.ScoringDualHuman:
			reason += "；由双评教师评分"
		case assessment.ScoringHumanPrimary, assessment.ScoringManualOnly:
			reason += "；由教师评分"
		}
		if fact.AnswerScope == "grading_group" {
			switch fact.Policy.Mode {
			case assessment.ScoringRuleAuto, assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm:
				reason += "；共用整组答题区，先识别并按题号映射作答，归属不清的题目单独复核"
			default:
				reason += "；共用整组答题区，按题目分别人工录分"
			}
		}
		// 共区无需额外子 ROI；观察负责成员归属，最终评分仍遵循每题原策略。
		processingMode := fact.Policy.Mode
		blocking, section := assessmentFactBlockingReason(fact)
		preview = append(preview, ReadinessScoringPreview{
			QuestionID: fact.QuestionID, QuestionNo: fact.QuestionNo, ScoringPolicy: fact.Policy, ProcessingMode: processingMode,
			Source: fact.Source, Profile: profile, ArchetypeCode: archetype, RiskTier: fact.RiskTier,
			AllowedEvidence: fact.AllowedEvidence, Reason: reason, BlockingReason: blocking, BlockingSection: section,
		})
	}
	return preview
}
