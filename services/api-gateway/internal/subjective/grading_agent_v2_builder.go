package subjective

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
)

const (
	gradingAgentV2BuilderSchemaVersion = "grading-agent-v2"
	maxGradingAgentV2RequestBytes      = 8 * 1024 * 1024
)

var gradingAgentV2InternalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var gradingAgentV2Subjects = map[string]bool{"mathematics": true}

var gradingAgentV2GradeLevels = map[string]bool{"junior": true, "senior": true}

var gradingAgentV2QuestionTypes = map[string]bool{
	"short_answer": true, "calculation": true, "essay": true, "discussion": true,
}

// BuiltGradingAgentV2Request exposes only the internal request body and
// non-sensitive idempotency facts to the production v2 transport.
type BuiltGradingAgentV2Request struct {
	RequestID         string
	Body              []byte
	IdempotencyDigest string
	MediaSHA256       string
	MediaByteSize     int64
}

// Clear releases the only retained encoded request body and overwrites its
// backing bytes so callers can shorten the lifetime of answer images.
func (r *BuiltGradingAgentV2Request) Clear() {
	if r == nil {
		return
	}
	clear(r.Body)
	r.Body = nil
}

type gradingAgentV2Request struct {
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
	MediaEvidence    gradingAgentV2MediaEvidence  `json:"media_evidence"`
	MathEvidence     MathEvidenceContext          `json:"math_evidence"`
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
	MediaEvidence    gradingAgentV2DigestEvidence `json:"media_evidence"`
	MathEvidence     MathEvidenceContext          `json:"math_evidence"`
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

// BuildGradingAgentV2Request assembles one verified active crop and one
// version-bound mathematical evidence projection into the internal v2
// contract. It performs no I/O.
func BuildGradingAgentV2Request(
	requestID string,
	input AdapterInput,
	crop ResolvedActiveCrop,
	policy ModelPolicy,
) (BuiltGradingAgentV2Request, error) {
	requestID = strings.TrimSpace(requestID)
	segmentID := strings.TrimSpace(input.SegmentID)
	questionID := strings.TrimSpace(input.Question.ID)
	subject := gradingAgentSubject(input.Subject)
	gradeLevel := gradingAgentGradeLevel(input.GradeLevel)
	agentRole := gradingAgentContractRole(input.AgentRole)
	questionType := strings.ToLower(strings.TrimSpace(input.Question.QuestionType))
	rubricID := strings.TrimSpace(input.Rubric.ID)
	rubricVersion := strings.TrimSpace(input.Rubric.Version)
	modelVersion := strings.TrimSpace(policy.ModelVersion)
	promptVersion := strings.TrimSpace(policy.PromptVersion)
	if len(requestID) < 8 ||
		!gradingAgentV2InternalID.MatchString(requestID) ||
		!gradingAgentV2InternalID.MatchString(segmentID) ||
		!gradingAgentV2InternalID.MatchString(questionID) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_identifier_invalid"}
	}
	if !gradingAgentV2Subjects[subject] ||
		!gradingAgentV2GradeLevels[gradeLevel] ||
		!gradingAgentV2QuestionTypes[questionType] ||
		!boundedNonBlank(input.Question.Stem, 12_000) ||
		!boundedNonBlank(input.AnswerText, 20_000) ||
		math.IsNaN(input.Question.Score) ||
		math.IsInf(input.Question.Score, 0) ||
		input.Question.Score <= 0 ||
		input.Question.Score > 1_000 {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_context_incomplete"}
	}
	if !boundedNonBlank(rubricID, 128) ||
		!boundedNonBlank(rubricVersion, 128) ||
		len(input.Rubric.Points) == 0 ||
		len(input.Rubric.Points) > 100 ||
		len(input.Rubric.Deductions) > 100 ||
		len(input.Rubric.Examples) > 100 ||
		math.IsNaN(input.Rubric.MaxScore) ||
		math.IsInf(input.Rubric.MaxScore, 0) ||
		input.Rubric.MaxScore <= 0 ||
		input.Rubric.MaxScore > 1_000 ||
		!almostEqual(input.Rubric.MaxScore, input.Question.Score) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "rubric_context_incomplete"}
	}
	if !boundedNonBlank(modelVersion, 256) ||
		!boundedNonBlank(promptVersion, 128) ||
		math.IsNaN(policy.MinConfidence) ||
		math.IsInf(policy.MinConfidence, 0) ||
		policy.MinConfidence <= 0 ||
		policy.MinConfidence > 1 {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_policy_invalid"}
	}
	if !validMathEvidenceForV2(input.MathEvidence, input) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "math_evidence_invalid"}
	}
	if err := validateResolvedActiveCropForV2(crop); err != nil {
		return BuiltGradingAgentV2Request{}, err
	}

	ocrConfidence := 0.0
	if input.OCRConfidence != nil {
		ocrConfidence = *input.OCRConfidence
	}
	if math.IsNaN(ocrConfidence) || math.IsInf(ocrConfidence, 0) || ocrConfidence < 0 || ocrConfidence > 1 {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_ocr_confidence_invalid"}
	}
	if len(input.PromptGuard.Signals) > 32 {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_prompt_guard_invalid"}
	}
	if !input.OutputConstraint.CriteriaEvidenceOnly || input.OutputConstraint.AllowModelFinalScore ||
		input.OutputConstraint.FinalScoreAuthority != "server_rubric_or_human_confirmation" {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_output_constraint_invalid"}
	}
	for _, signal := range input.PromptGuard.Signals {
		if !boundedNonBlank(signal, 128) {
			return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_prompt_guard_invalid"}
		}
	}
	points := make([]gradingAgentRubricPoint, 0, len(input.Rubric.Points))
	pointIDs := make(map[string]bool, len(input.Rubric.Points))
	pointTotal := 0.0
	for _, point := range input.Rubric.Points {
		pointID := strings.TrimSpace(point.ID)
		if !boundedNonBlank(pointID, 128) ||
			!boundedNonBlank(point.Description, 4_000) ||
			pointIDs[pointID] ||
			math.IsNaN(point.Score) ||
			math.IsInf(point.Score, 0) ||
			point.Score <= 0 ||
			point.Score > 1_000 {
			return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "rubric_context_invalid"}
		}
		pointIDs[pointID] = true
		pointTotal += point.Score
		points = append(points, gradingAgentRubricPoint{
			ID:               pointID,
			Description:      point.Description,
			Score:            point.Score,
			Required:         point.Required,
			Aliases:          []string{},
			EvidenceRequired: true,
			MatchPolicy:      "semantic",
		})
	}
	if !almostEqual(pointTotal, input.Rubric.MaxScore) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "rubric_context_invalid"}
	}

	bindingHash := computeGradingAgentV2BindingHash(
		requestID,
		questionID,
		segmentID,
		crop.SHA256,
		crop.NormalizedBBox,
	)
	media := gradingAgentV2MediaEvidence{
		Kind:           crop.Kind,
		Encoding:       "base64",
		MediaType:      crop.MediaType,
		SHA256:         crop.SHA256,
		ByteSize:       crop.ByteSize,
		WidthPixels:    crop.WidthPixels,
		HeightPixels:   crop.HeightPixels,
		NormalizedBBox: crop.NormalizedBBox,
		BindingHash:    bindingHash,
		DataBase64:     base64.StdEncoding.EncodeToString(crop.Data),
	}
	request := gradingAgentV2Request{
		SchemaVersion:   gradingAgentV2BuilderSchemaVersion,
		RequestID:       requestID,
		Subject:         subject,
		GradeLevel:      gradeLevel,
		AgentRole:       agentRole,
		QuestionID:      questionID,
		AnswerSegmentID: segmentID,
		QuestionType:    questionType,
		QuestionText:    input.Question.Stem,
		MaxScore:        input.Question.Score,
		AnswerText:      input.AnswerText,
		OCRConfidence:   ocrConfidence,
		RubricVersion:   rubricVersion,
		PromptVersion:   promptVersion,
		Rubric: gradingAgentRubric{
			RubricID:          rubricID,
			RubricVersion:     rubricVersion,
			QuestionType:      questionType,
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
			ModelVersion:  modelVersion,
			MinConfidence: policy.MinConfidence,
		},
		PromptGuard: gradingAgentPromptGuard{
			StudentAnswerIsUntrusted: true,
			SuspectedInjection:       input.PromptGuard.SuspectedInjection,
			Signals:                  nonNilStrings(input.PromptGuard.Signals),
		},
		OutputConstraint: gradingAgentOutputConstraint{
			CriteriaEvidenceOnly: true,
			AllowModelFinalScore: false,
			FinalScoreAuthority:  input.OutputConstraint.FinalScoreAuthority,
		},
		MediaEvidence: media,
		MathEvidence:  *input.MathEvidence,
	}
	request.MathEvidence.effective = mathunderstanding.EffectiveArtifact{}
	request.MathEvidence.frozen = mathunderstanding.FrozenRubric{}
	request.MathEvidence.baseline = mathunderstanding.RubricScore{}
	body, err := json.Marshal(request)
	media.DataBase64 = ""
	request.MediaEvidence.DataBase64 = ""
	if err != nil {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "request_encoding_failed", cause: err}
	}
	if len(body) > maxGradingAgentV2RequestBytes {
		clear(body)
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_v2_request_too_large"}
	}
	digest, err := gradingAgentV2IdempotencyDigest(request)
	if err != nil {
		clear(body)
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "request_encoding_failed", cause: err}
	}
	return BuiltGradingAgentV2Request{
		RequestID:         requestID,
		Body:              body,
		IdempotencyDigest: digest,
		MediaSHA256:       crop.SHA256,
		MediaByteSize:     crop.ByteSize,
	}, nil
}

func boundedNonBlank(value string, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return strings.TrimSpace(value) != "" && length <= maximum
}

func validateResolvedActiveCropForV2(crop ResolvedActiveCrop) error {
	if crop.Kind != "answer_segment_crop" ||
		crop.MediaType != "image/png" ||
		crop.ByteSize <= 0 ||
		crop.ByteSize > maxActiveCropBytes ||
		crop.ByteSize != int64(len(crop.Data)) ||
		!validLowerSHA256(crop.SHA256) {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	width, height, err := validateActiveCropPNG(crop.Data)
	if err != nil ||
		width != crop.WidthPixels ||
		height != crop.HeightPixels {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	actualHash := sha256.Sum256(crop.Data)
	if hex.EncodeToString(actualHash[:]) != crop.SHA256 {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	bbox, err := activeCropBBox(map[string]any{
		"x": crop.NormalizedBBox.X, "y": crop.NormalizedBBox.Y,
		"width": crop.NormalizedBBox.Width, "height": crop.NormalizedBBox.Height,
	})
	if err != nil || bbox != crop.NormalizedBBox {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	return nil
}

func computeGradingAgentV2BindingHash(
	requestID string,
	questionID string,
	segmentID string,
	mediaSHA256 string,
	bbox ActiveCropBBox,
) string {
	parts := []string{
		gradingAgentV2BuilderSchemaVersion,
		requestID,
		questionID,
		segmentID,
		mediaSHA256,
		fmt.Sprintf("%.0f", math.Round(bbox.X*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Y*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Width*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Height*activeCropBBoxScale)),
	}
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:%s", len([]byte(part)), part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func gradingAgentV2IdempotencyDigest(request gradingAgentV2Request) (string, error) {
	media := request.MediaEvidence
	safe := gradingAgentV2DigestRequest{
		SchemaVersion:    request.SchemaVersion,
		RequestID:        request.RequestID,
		Subject:          request.Subject,
		GradeLevel:       request.GradeLevel,
		AgentRole:        request.AgentRole,
		QuestionID:       request.QuestionID,
		AnswerSegmentID:  request.AnswerSegmentID,
		QuestionType:     request.QuestionType,
		QuestionText:     request.QuestionText,
		MaxScore:         request.MaxScore,
		AnswerText:       request.AnswerText,
		OCRConfidence:    request.OCRConfidence,
		RubricVersion:    request.RubricVersion,
		PromptVersion:    request.PromptVersion,
		Rubric:           request.Rubric,
		ModelPolicy:      request.ModelPolicy,
		PromptGuard:      request.PromptGuard,
		OutputConstraint: request.OutputConstraint,
		MediaEvidence: gradingAgentV2DigestEvidence{
			Kind:           media.Kind,
			Encoding:       media.Encoding,
			MediaType:      media.MediaType,
			SHA256:         media.SHA256,
			ByteSize:       media.ByteSize,
			WidthPixels:    media.WidthPixels,
			HeightPixels:   media.HeightPixels,
			NormalizedBBox: media.NormalizedBBox,
			BindingHash:    media.BindingHash,
		},
		MathEvidence: request.MathEvidence,
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	clear(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validMathEvidenceForV2(evidence *MathEvidenceContext, input AdapterInput) bool {
	if evidence == nil || evidence.ArtifactID == "" || evidence.ArtifactVersion <= 0 || evidence.CorrectionRevision < 0 ||
		evidence.ExamQuestionSnapshotID == "" || evidence.ExamQuestionSnapshotID != input.AssessmentSnapshot.ID ||
		evidence.ScoringVersion != MathScoringVersionV1 || evidence.OverallConfidence < 0 || evidence.OverallConfidence > 1 ||
		evidence.Quality.Critical < 0 || evidence.Quality.Critical > 1 || len(evidence.Steps) == 0 || len(evidence.Steps) > 512 ||
		len(evidence.Formulas) > 512 || len(evidence.Verifications) > 2048 || len(evidence.RubricEvidence) > 512 {
		return false
	}
	known := map[string]bool{}
	for _, step := range evidence.Steps {
		if !boundedNonBlank(step.ID, 128) || len(step.Text) > 4_000 || step.Confidence < 0 || step.Confidence > 1 || known[step.ID] {
			return false
		}
		known[step.ID] = true
	}
	for _, formula := range evidence.Formulas {
		if !boundedNonBlank(formula.ID, 128) || len(formula.CanonicalLatex) > 4_000 || formula.Confidence < 0 || formula.Confidence > 1 || known[formula.ID] {
			return false
		}
		known[formula.ID] = true
	}
	for _, check := range evidence.Verifications {
		if !boundedNonBlank(check.ID, 128) || check.Confidence < 0 || check.Confidence > 1 {
			return false
		}
	}
	return true
}
