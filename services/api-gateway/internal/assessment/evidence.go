package assessment

import "time"

type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type CreateScoringEvidenceInput struct {
	SubmissionID           string         `json:"submission_id"`
	QuestionID             string         `json:"question_id"`
	ExamQuestionSnapshotID string         `json:"exam_question_snapshot_id"`
	EvidenceType           EvidenceType   `json:"evidence_type"`
	SourceArtifactID       string         `json:"source_artifact_id"`
	RubricCriterionKey     string         `json:"rubric_criterion_key,omitempty"`
	Payload                map[string]any `json:"payload"`
	BoundingBox            *BoundingBox   `json:"bbox,omitempty"`
	// Quality 取值为 0 到 1；nil 表示未提供质量数据，不应当作满质量。
	Quality                *float64       `json:"quality,omitempty"`
}

type ScoringEvidence struct {
	ID                     string         `json:"id"`
	TenantID               string         `json:"tenant_id"`
	SubmissionID           string         `json:"submission_id"`
	QuestionID             string         `json:"question_id"`
	ExamQuestionSnapshotID string         `json:"exam_question_snapshot_id"`
	EvidenceType           EvidenceType   `json:"evidence_type"`
	SourceArtifactID       string         `json:"source_artifact_id"`
	RubricCriterionKey     string         `json:"rubric_criterion_key,omitempty"`
	Payload                map[string]any `json:"payload"`
	BoundingBox            *BoundingBox   `json:"bbox,omitempty"`
	Quality                *float64       `json:"quality,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
}

func ValidateScoringEvidence(input CreateScoringEvidenceInput) error {
	if input.SubmissionID == "" || input.QuestionID == "" || input.ExamQuestionSnapshotID == "" ||
		input.SourceArtifactID == "" || !input.EvidenceType.Valid() {
		return ErrInvalidInput
	}
	if input.Quality != nil && (*input.Quality < 0 || *input.Quality > 1) {
		return ErrInvalidInput
	}
	if input.BoundingBox != nil && (input.BoundingBox.X < 0 || input.BoundingBox.Y < 0 ||
		input.BoundingBox.Width <= 0 || input.BoundingBox.Height <= 0) {
		return ErrInvalidInput
	}
	return nil
}
