package subjective

type gradingAgentRequest struct {
	SchemaVersion    string                       `json:"schema_version"`
	RequestID        string                       `json:"request_id"`
	Subject          string                       `json:"subject"`
	GradeLevel       string                       `json:"grade_level"`
	AgentRole        string                       `json:"agent_role"`
	QuestionID       string                       `json:"question_id"`
	AnswerSegmentID  string                       `json:"answer_segment_id"`
	QuestionType     string                       `json:"question_type"`
	QuestionText     string                       `json:"question_text"`
	MaxScore         float64                      `json:"max_score"`
	AnswerText       string                       `json:"answer_text"`
	OCRConfidence    float64                      `json:"ocr_confidence"`
	RubricVersion    string                       `json:"rubric_version"`
	PromptVersion    string                       `json:"prompt_version"`
	Rubric           gradingAgentRubric           `json:"rubric"`
	ModelPolicy      gradingAgentModelPolicy      `json:"model_policy"`
	PromptGuard      gradingAgentPromptGuard      `json:"prompt_guard"`
	OutputConstraint gradingAgentOutputConstraint `json:"output_constraint"`
}

type gradingAgentRubric struct {
	RubricID          string                    `json:"rubric_id"`
	RubricVersion     string                    `json:"rubric_version"`
	QuestionType      string                    `json:"question_type"`
	MaxScore          float64                   `json:"max_score"`
	AllowPartialTotal bool                      `json:"allow_partial_total"`
	Points            []gradingAgentRubricPoint `json:"points"`
	Deductions        []any                     `json:"deductions"`
	EquivalentAnswers []any                     `json:"equivalent_answers"`
	Examples          []any                     `json:"examples"`
	ScoringNotes      []string                  `json:"scoring_notes"`
}

type gradingAgentRubricPoint struct {
	ID               string   `json:"id"`
	Description      string   `json:"description"`
	Score            float64  `json:"score"`
	Required         bool     `json:"required"`
	Aliases          []string `json:"aliases"`
	EvidenceRequired bool     `json:"evidence_required"`
	MatchPolicy      string   `json:"match_policy"`
}

type gradingAgentModelPolicy struct {
	Mode          string  `json:"mode"`
	ModelVersion  string  `json:"model_version"`
	MinConfidence float64 `json:"min_confidence"`
}

type gradingAgentPromptGuard struct {
	StudentAnswerIsUntrusted bool     `json:"student_answer_is_untrusted"`
	SuspectedInjection       bool     `json:"suspected_injection"`
	Signals                  []string `json:"signals"`
}

type gradingAgentOutputConstraint struct {
	CriteriaEvidenceOnly bool   `json:"criteria_evidence_only"`
	AllowModelFinalScore bool   `json:"allow_model_final_score"`
	FinalScoreAuthority  string `json:"final_score_authority"`
}

type gradingAgentResponse struct {
	SchemaVersion     string                     `json:"schema_version"`
	RequestID         string                     `json:"request_id"`
	Status            string                     `json:"status"`
	Delivery          string                     `json:"delivery"`
	SuggestedScore    float64                    `json:"suggested_score"`
	MaxScore          float64                    `json:"max_score"`
	Confidence        float64                    `json:"confidence"`
	MatchedPoints     []gradingAgentMatchedPoint `json:"matched_points"`
	MissingPoints     []gradingAgentMissingPoint `json:"missing_points"`
	Deductions        []any                      `json:"deductions"`
	Evidence          []gradingAgentEvidence     `json:"evidence"`
	RiskFlags         []string                   `json:"risk_flags"`
	NeedsHumanReview  bool                       `json:"needs_human_review"`
	StudentFeedback   string                     `json:"student_feedback"`
	TeacherNote       string                     `json:"teacher_note"`
	ModelVersion      string                     `json:"model_version"`
	PromptVersion     string                     `json:"prompt_version"`
	RubricVersion     string                     `json:"rubric_version"`
	CapabilityProfile string                     `json:"capability_profile"`
	Mock              bool                       `json:"mock"`
	Telemetry         gradingAgentTelemetry      `json:"telemetry"`
}

type gradingAgentMatchedPoint struct {
	RubricPointID string   `json:"rubric_point_id"`
	Label         string   `json:"label"`
	Score         float64  `json:"score"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type gradingAgentMissingPoint struct {
	RubricPointID string `json:"rubric_point_id"`
	Label         string `json:"label"`
	Reason        string `json:"reason"`
}

type gradingAgentEvidence struct {
	EvidenceID    string  `json:"evidence_id"`
	RubricPointID string  `json:"rubric_point_id"`
	TextExcerpt   string  `json:"text_excerpt"`
	Location      string  `json:"location"`
	Confidence    float64 `json:"confidence"`
}

type gradingAgentTelemetry struct {
	Adapter              string                `json:"adapter"`
	Provider             string                `json:"provider"`
	Deployment           string                `json:"deployment"`
	Region               string                `json:"region"`
	Attempts             int                   `json:"attempts"`
	RepairAttempted      bool                  `json:"repair_attempted"`
	PriorErrorCodes      []string              `json:"prior_error_codes"`
	ElapsedMS            int64                 `json:"elapsed_ms"`
	Usage                ModelTokenUsage       `json:"usage"`
	ExecutionDiagnostics *ExecutionDiagnostics `json:"execution_diagnostics,omitempty"`
}

type gradingAgentErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Error         struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable *bool  `json:"retryable"`
	} `json:"error"`
	ExecutionDiagnostics *ExecutionDiagnostics `json:"execution_diagnostics,omitempty"`
}
