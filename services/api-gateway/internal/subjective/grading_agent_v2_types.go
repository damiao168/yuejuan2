package subjective

type gradingAgentV2Request struct {
	SchemaVersion      string                       `json:"schema_version"`
	RequestID          string                       `json:"request_id"`
	Subject            string                       `json:"subject"`
	GradeLevel         string                       `json:"grade_level"`
	AgentRole          string                       `json:"agent_role"`
	QuestionID         string                       `json:"question_id"`
	AnswerSegmentID    string                       `json:"answer_segment_id"`
	QuestionType       string                       `json:"question_type"`
	QuestionText       string                       `json:"question_text"`
	MaxScore           float64                      `json:"max_score"`
	AnswerText         string                       `json:"answer_text"`
	OCRConfidence      float64                      `json:"ocr_confidence"`
	RubricVersion      string                       `json:"rubric_version"`
	PromptVersion      string                       `json:"prompt_version"`
	Rubric             gradingAgentRubric           `json:"rubric"`
	ModelPolicy        gradingAgentModelPolicy      `json:"model_policy"`
	PromptGuard        gradingAgentPromptGuard      `json:"prompt_guard"`
	OutputConstraint   gradingAgentOutputConstraint `json:"output_constraint"`
	MediaEvidence      gradingAgentV2MediaEvidence  `json:"media_evidence"`
	MathEvidence       MathEvidenceContext          `json:"math_evidence"`
	ReferenceContext   *GradingReferenceContext     `json:"reference_context,omitempty"`
	RequiredFeatures   []string                     `json:"required_features,omitempty"`
	ReferenceManifest  *ReferenceManifest           `json:"reference_manifest,omitempty"`
	ReferenceMedia     []gradingAgentReferenceMedia `json:"reference_media,omitempty"`
	RequestBindingHash string                       `json:"request_binding_hash,omitempty"`
}

type gradingAgentReferenceMedia struct {
	MediaID      string `json:"media_id"`
	Role         string `json:"role"`
	Ordinal      int    `json:"ordinal"`
	BindingHash  string `json:"binding_hash"`
	Encoding     string `json:"encoding"`
	MediaType    string `json:"media_type"`
	SHA256       string `json:"sha256"`
	ByteSize     int64  `json:"byte_size"`
	WidthPixels  int    `json:"width_pixels"`
	HeightPixels int    `json:"height_pixels"`
	DataBase64   string `json:"data_base64"`
}

type gradingAgentV2MediaEvidence struct {
	Kind           string         `json:"kind"`
	Encoding       string         `json:"encoding"`
	MediaType      string         `json:"media_type"`
	SHA256         string         `json:"sha256"`
	ByteSize       int64          `json:"byte_size"`
	WidthPixels    int            `json:"width_pixels"`
	HeightPixels   int            `json:"height_pixels"`
	NormalizedBBox ActiveCropBBox `json:"normalized_bbox"`
	BindingHash    string         `json:"binding_hash"`
	DataBase64     string         `json:"data_base64"`
}

type gradingAgentV2DigestRequest struct {
	SchemaVersion      string                       `json:"schema_version"`
	RequestID          string                       `json:"request_id"`
	Subject            string                       `json:"subject"`
	GradeLevel         string                       `json:"grade_level"`
	AgentRole          string                       `json:"agent_role"`
	QuestionID         string                       `json:"question_id"`
	AnswerSegmentID    string                       `json:"answer_segment_id"`
	QuestionType       string                       `json:"question_type"`
	QuestionText       string                       `json:"question_text"`
	MaxScore           float64                      `json:"max_score"`
	AnswerText         string                       `json:"answer_text"`
	OCRConfidence      float64                      `json:"ocr_confidence"`
	RubricVersion      string                       `json:"rubric_version"`
	PromptVersion      string                       `json:"prompt_version"`
	Rubric             gradingAgentRubric           `json:"rubric"`
	ModelPolicy        gradingAgentModelPolicy      `json:"model_policy"`
	PromptGuard        gradingAgentPromptGuard      `json:"prompt_guard"`
	OutputConstraint   gradingAgentOutputConstraint `json:"output_constraint"`
	MediaEvidence      gradingAgentV2DigestEvidence `json:"media_evidence"`
	MathEvidence       MathEvidenceContext          `json:"math_evidence"`
	ReferenceContext   *GradingReferenceContext     `json:"reference_context,omitempty"`
	RequiredFeatures   []string                     `json:"required_features,omitempty"`
	ReferenceManifest  *ReferenceManifest           `json:"reference_manifest,omitempty"`
	ReferenceMedia     []gradingAgentReferenceMedia `json:"reference_media,omitempty"`
	RequestBindingHash string                       `json:"request_binding_hash,omitempty"`
}

type gradingAgentV2DigestEvidence struct {
	Kind           string         `json:"kind"`
	Encoding       string         `json:"encoding"`
	MediaType      string         `json:"media_type"`
	SHA256         string         `json:"sha256"`
	ByteSize       int64          `json:"byte_size"`
	WidthPixels    int            `json:"width_pixels"`
	HeightPixels   int            `json:"height_pixels"`
	NormalizedBBox ActiveCropBBox `json:"normalized_bbox"`
	BindingHash    string         `json:"binding_hash"`
}
