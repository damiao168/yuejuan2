package subjective

import (
	"strings"
)

func (a *HTTPAdapter) buildRequest(requestID string, input AdapterInput) (gradingAgentRequest, error) {
	if a.baseURL == "" || len(a.token) < 32 || a.policy.ModelVersion == "" || a.policy.PromptVersion == "" {
		return gradingAgentRequest{}, &GradingAgentError{Code: "adapter_not_configured"}
	}
	if strings.TrimSpace(input.SegmentID) == "" || strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.GradeLevel) == "" || strings.TrimSpace(input.Question.ID) == "" || strings.TrimSpace(input.Question.Stem) == "" || strings.TrimSpace(input.AnswerText) == "" {
		return gradingAgentRequest{}, &GradingAgentError{Code: "grading_context_incomplete"}
	}
	if strings.TrimSpace(input.Rubric.ID) == "" || strings.TrimSpace(input.Rubric.Version) == "" || len(input.Rubric.Points) == 0 {
		return gradingAgentRequest{}, &GradingAgentError{Code: "rubric_context_incomplete"}
	}
	ocrConfidence := 0.0
	if input.OCRConfidence != nil {
		ocrConfidence = *input.OCRConfidence
	}
	points := make([]gradingAgentRubricPoint, 0, len(input.Rubric.Points))
	for _, point := range input.Rubric.Points {
		points = append(points, gradingAgentRubricPoint{
			ID:               point.ID,
			Description:      point.Description,
			Score:            point.Score,
			Required:         point.Required,
			Aliases:          []string{},
			EvidenceRequired: true,
			MatchPolicy:      "semantic",
		})
	}
	return gradingAgentRequest{
		SchemaVersion:   gradingAgentSchemaVersion,
		RequestID:       requestID,
		Subject:         gradingAgentSubject(input.Subject),
		GradeLevel:      gradingAgentGradeLevel(input.GradeLevel),
		AgentRole:       gradingAgentContractRole(input.AgentRole),
		QuestionID:      input.Question.ID,
		AnswerSegmentID: input.SegmentID,
		QuestionType:    input.Question.QuestionType,
		QuestionText:    input.Question.Stem,
		MaxScore:        input.Question.Score,
		AnswerText:      input.AnswerText,
		OCRConfidence:   ocrConfidence,
		RubricVersion:   input.Rubric.Version,
		PromptVersion:   a.policy.PromptVersion,
		Rubric: gradingAgentRubric{
			RubricID:          input.Rubric.ID,
			RubricVersion:     input.Rubric.Version,
			QuestionType:      input.Question.QuestionType,
			MaxScore:          input.Rubric.MaxScore,
			AllowPartialTotal: false,
			Points:            points,
			Deductions:        nonNilAny(input.Rubric.Deductions),
			EquivalentAnswers: []any{},
			Examples:          nonNilAny(input.Rubric.Examples),
			ScoringNotes:      []string{},
		},
		ModelPolicy: gradingAgentModelPolicy{
			Mode:          "shadow",
			ModelVersion:  a.policy.ModelVersion,
			MinConfidence: a.policy.MinConfidence,
		},
		PromptGuard: gradingAgentPromptGuard{
			StudentAnswerIsUntrusted: true,
			SuspectedInjection:       input.PromptGuard.SuspectedInjection,
			Signals:                  nonNilStrings(input.PromptGuard.Signals),
		},
		OutputConstraint: gradingAgentOutputConstraint{
			CriteriaEvidenceOnly: input.OutputConstraint.CriteriaEvidenceOnly,
			AllowModelFinalScore: false,
			FinalScoreAuthority:  input.OutputConstraint.FinalScoreAuthority,
		},
	}, nil
}

// grading-agent 协议与评估领域使用相同的标准词汇。
// 旧别名仅在此适配器边界接受，避免旧调用方将非标准值带入 agent 契约。
func gradingAgentSubject(subject string) string {
	switch strings.ToLower(strings.TrimSpace(subject)) {
	case "math":
		return "mathematics"
	case "politics", "civics":
		return "ethics_politics"
	default:
		return strings.ToLower(strings.TrimSpace(subject))
	}
}

func gradingAgentGradeLevel(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "junior_middle":
		return "junior"
	case "senior_middle", "high_school":
		return "senior"
	default:
		return strings.ToLower(strings.TrimSpace(stage))
	}
}

func gradingAgentContractRole(role string) string {
	switch strings.TrimSpace(role) {
	case AgentRolePrimaryA, AgentRolePrimaryB:
		return "primary"
	case AgentRoleArbiter:
		return "arbiter"
	default:
		return "single"
	}
}
