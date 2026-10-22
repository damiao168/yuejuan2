package subjective

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validateIdempotencyKey(value string) error {
	if value == "" {
		return nil
	}
	if !idempotencyKeyPattern.MatchString(value) {
		return fmt.Errorf("%w: idempotency key must be 1-128 characters using letters, digits, '.', '_', ':', or '-'", ErrInvalidInput)
	}
	return nil
}

func stableRequestID(tenantID string, ctx Context, policy ModelPolicy, key string) (string, error) {
	if key == "" {
		return newAdapterRequestID(), nil
	}
	payload := struct {
		TenantID               string  `json:"tenant_id"`
		SegmentID              string  `json:"segment_id"`
		AnswerVersion          string  `json:"answer_version"`
		QuestionID             string  `json:"question_id"`
		ExamQuestionSnapshotID string  `json:"exam_question_snapshot_id,omitempty"`
		RubricID               string  `json:"rubric_id"`
		RubricVersion          string  `json:"rubric_version"`
		ModelVersion           string  `json:"model_version"`
		PromptVersion          string  `json:"prompt_version"`
		MinConfidence          float64 `json:"min_confidence"`
		IdempotencyKey         string  `json:"idempotency_key"`
		MathArtifactID         string  `json:"math_artifact_id,omitempty"`
		MathArtifactVersion    int64   `json:"math_artifact_version,omitempty"`
		MathCorrectionRevision int64   `json:"math_correction_revision,omitempty"`
		MathScoringVersion     string  `json:"math_scoring_version,omitempty"`
	}{TenantID: tenantID, SegmentID: ctx.SegmentID, AnswerVersion: ctx.AnswerVersion, QuestionID: ctx.Question.ID,
		ExamQuestionSnapshotID: ctx.AssessmentSnapshot.ID,
		RubricID:               ctx.Rubric.ID, RubricVersion: ctx.Rubric.Version, ModelVersion: policy.ModelVersion,
		PromptVersion: policy.PromptVersion, MinConfidence: policy.MinConfidence, IdempotencyKey: key}
	if ctx.MathEvidence != nil {
		payload.MathArtifactID = ctx.MathEvidence.ArtifactID
		payload.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		payload.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		payload.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "subjective-" + hex.EncodeToString(digest[:]), nil
}

func sameGradeIdentity(left, right Grade) bool {
	left.ID, right.ID = "", ""
	left.TenantID, right.TenantID = "", ""
	left.CreatedBy, right.CreatedBy = "", ""
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	return gradeFingerprint(left) == gradeFingerprint(right)
}

func gradeFingerprint(grade Grade) string {
	raw, _ := json.Marshal(grade)
	return string(raw)
}
