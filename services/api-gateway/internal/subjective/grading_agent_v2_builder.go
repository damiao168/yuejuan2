package subjective

import (
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"encoding/base64"
	"encoding/json"
	"math"
	"regexp"
	"strings"
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

// BuiltGradingAgentV2Request 只向生产 v2 传输提供内部请求体和非敏感幂等事实。
type BuiltGradingAgentV2Request struct {
	RequestID         string
	Body              []byte
	IdempotencyDigest string
	MediaSHA256       string
	MediaByteSize     int64
}

// Clear 释放唯一保留的编码请求体，并覆盖其底层字节，使调用方能缩短答案图像的留存时间。
func (r *BuiltGradingAgentV2Request) Clear() {
	if r == nil {
		return
	}
	clear(r.Body)
	r.Body = nil
}

// BuildGradingAgentV2Request 将一个已核验的有效裁剪图与一个绑定版本的数学证据投影组装为内部 v2 契约。
// 它不执行 I/O。
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
	if err := validateGradingAgentV2Context(input, policy); err != nil {
		return BuiltGradingAgentV2Request{}, err
	}
	if !validMathEvidenceForV2(input.MathEvidence, input) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "math_evidence_invalid"}
	}
	if !validGradingReferenceContext(input.ReferenceContext) {
		return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "grading_reference_invalid"}
	}
	reference := normalizedGradingReferenceForV2(input.ReferenceContext)
	if err := validMaterialManifest(input.ReferenceManifest, input.ReferenceMedia); err != nil {
		return BuiltGradingAgentV2Request{}, err
	}
	if manifest := input.ReferenceManifest; manifest != nil {
		if manifest.AssessmentSnapshotID != input.AssessmentSnapshot.ID || manifest.AssessmentSnapshotHash != input.AssessmentSnapshot.ContentHash {
			return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "material_manifest_invalid"}
		}
		bytes, pixels := crop.ByteSize, int64(crop.WidthPixels)*int64(crop.HeightPixels)
		for _, item := range input.ReferenceMedia {
			bytes += item.ByteSize
			pixels += int64(item.WidthPixels) * int64(item.HeightPixels)
		}
		if bytes > maxAllMediaBytes || pixels > maxAllMediaPixels {
			return BuiltGradingAgentV2Request{}, &GradingAgentError{Code: "material_media_budget_exceeded"}
		}
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
	if err := validateGradingAgentV2OutputRules(input.PromptGuard, input.OutputConstraint); err != nil {
		return BuiltGradingAgentV2Request{}, err
	}
	points, err := gradingAgentV2RubricPoints(input.Rubric)
	if err != nil {
		return BuiltGradingAgentV2Request{}, err
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
		MediaEvidence:    media,
		MathEvidence:     *input.MathEvidence,
		ReferenceContext: reference,
	}
	if input.ReferenceManifest != nil {
		request.RequiredFeatures = []string{materialInputFeature}
		request.ReferenceManifest = input.ReferenceManifest
		request.ReferenceMedia = make([]gradingAgentReferenceMedia, 0, len(input.ReferenceMedia))
		for _, item := range input.ReferenceMedia {
			item.BindingHash = computeReferenceBindingHash(requestID, questionID, segmentID, input.ReferenceManifest, item)
			request.ReferenceMedia = append(request.ReferenceMedia, gradingAgentReferenceMedia{MediaID: item.MediaID, Role: item.Role, Ordinal: item.Ordinal, BindingHash: item.BindingHash, Encoding: item.Encoding, MediaType: item.MediaType, SHA256: item.SHA256, ByteSize: item.ByteSize, WidthPixels: item.WidthPixels, HeightPixels: item.HeightPixels, DataBase64: base64.StdEncoding.EncodeToString(item.Data)})
		}
		request.RequestBindingHash = computeMaterialRequestBindingHash(requestID, questionID, segmentID, input.ReferenceManifest, crop, input.ReferenceMedia)
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
