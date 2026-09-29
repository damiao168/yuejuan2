package subjective

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
)

func TestBuildGradingAgentV2RequestBuildsOneBoundedAttestedCrop(t *testing.T) {
	input := validMathV2AdapterInput()
	input.RequestID = "subjective-grade-image-0001"
	input.AnswerImageRef = map[string]any{
		"student_name": "must-not-leak",
		"storage_key":  "must-not-leak",
		"url":          "https://must-not-leak.invalid/crop.png",
	}
	crop := validResolvedActiveCrop(t)
	crop.NormalizedBBox = ActiveCropBBox{X: 0.1, Y: 0.2, Width: 0.4, Height: 0.3}
	policy := ModelPolicy{
		ModelVersion:  "fixture-multimodal-model-v1",
		PromptVersion: "subjective-image-contract-v2",
		MinConfidence: 0.8,
	}

	built, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Clear()
	if len(built.Body) == 0 || len(built.Body) > maxGradingAgentV2RequestBytes {
		t.Fatalf("unexpected v2 request size: %d", len(built.Body))
	}
	if built.IdempotencyDigest == "" || built.MediaSHA256 != crop.SHA256 || built.MediaByteSize != crop.ByteSize {
		t.Fatalf("safe request facts are incomplete: %+v", built)
	}

	var request map[string]any
	decoder := json.NewDecoder(bytes.NewReader(built.Body))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	if err := validateGradingAgentV2RequestFixture(request); err != nil {
		t.Fatalf("builder emitted an invalid v2 contract: %v", err)
	}
	if request["schema_version"] != gradingAgentV2BuilderSchemaVersion ||
		request["request_id"] != input.RequestID ||
		request["question_id"] != input.Question.ID ||
		request["answer_segment_id"] != input.SegmentID {
		t.Fatalf("v2 request binding is incomplete: %#v", request)
	}
	constraint, ok := request["output_constraint"].(map[string]any)
	if !ok || constraint["criteria_evidence_only"] != true || constraint["allow_model_final_score"] != false ||
		constraint["final_score_authority"] != "server_rubric_or_human_confirmation" {
		t.Fatalf("v2 request lost its server-owned score boundary: %#v", request["output_constraint"])
	}
	media, ok := request["media_evidence"].(map[string]any)
	if !ok {
		t.Fatalf("media evidence missing: %#v", request["media_evidence"])
	}
	if media["kind"] != "answer_segment_crop" ||
		media["encoding"] != "base64" ||
		media["media_type"] != "image/png" ||
		media["data_base64"] != activeCropTestPNGBase64 ||
		media["binding_hash"] != "00dec19e6cb2f4f6ae51300bf1c3c65def2b747bca52a48f27f619c95f9d8c9d" {
		t.Fatalf("unexpected media evidence: %#v", media)
	}
	body := string(built.Body)
	for _, forbidden := range []string{
		"student_name", "storage_key", "must-not-leak.invalid", input.Question.TenantID,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("v2 body leaked forbidden value %q", forbidden)
		}
	}
}

func TestBuildGradingAgentV2RequestRejectsInvalidCropFacts(t *testing.T) {
	input := validMathV2AdapterInput()
	policy := ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}
	cases := []struct {
		name   string
		mutate func(*ResolvedActiveCrop)
	}{
		{"wrong kind", func(crop *ResolvedActiveCrop) { crop.Kind = "whole_page" }},
		{"wrong mime", func(crop *ResolvedActiveCrop) { crop.MediaType = "image/jpeg" }},
		{"wrong size", func(crop *ResolvedActiveCrop) { crop.ByteSize++ }},
		{"wrong hash", func(crop *ResolvedActiveCrop) { crop.SHA256 = strings.Repeat("0", 64) }},
		{"wrong dimensions", func(crop *ResolvedActiveCrop) { crop.WidthPixels = 2 }},
		{"whole page bbox", func(crop *ResolvedActiveCrop) {
			crop.NormalizedBBox = ActiveCropBBox{X: 0, Y: 0, Width: 1, Height: 1}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crop := validResolvedActiveCrop(t)
			tc.mutate(&crop)
			_, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
			assertGradingAgentV2ErrorCode(t, err, "grading_v2_crop_invalid")
		})
	}
}

func TestBuildGradingAgentV2RequestEnforcesIndependentEightMiBLimit(t *testing.T) {
	input := validMathV2AdapterInput()
	input.Rubric.Examples = []any{strings.Repeat("x", maxGradingAgentV2RequestBytes)}
	crop := validResolvedActiveCrop(t)
	policy := ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}

	_, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	assertGradingAgentV2ErrorCode(t, err, "grading_v2_request_too_large")
	if maxGradingAgentResponseBytes == maxGradingAgentV2RequestBytes {
		t.Fatal("v1 response and v2 request limits must remain independent")
	}
}

func TestBuiltGradingAgentV2RequestClearOverwritesBody(t *testing.T) {
	input := validMathV2AdapterInput()
	crop := validResolvedActiveCrop(t)
	policy := ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}
	built, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	alias := built.Body
	built.Clear()
	if built.Body != nil {
		t.Fatal("cleared request retained its body")
	}
	for index, value := range alias {
		if value != 0 {
			t.Fatalf("body byte %d was not overwritten", index)
		}
	}
}

func TestGradingAgentV2IdempotencyDigestBindsMediaWithoutEncodingIt(t *testing.T) {
	input := validMathV2AdapterInput()
	crop := validResolvedActiveCrop(t)
	policy := ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}

	first, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Clear()
	second, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Clear()
	if first.IdempotencyDigest != second.IdempotencyDigest {
		t.Fatal("identical v2 requests must have stable idempotency digests")
	}
	if strings.Contains(first.IdempotencyDigest, activeCropTestPNGBase64) || len(first.IdempotencyDigest) != 64 {
		t.Fatalf("unsafe idempotency digest: %q", first.IdempotencyDigest)
	}

	changed := input
	changed.AnswerText += " changed"
	third, err := BuildGradingAgentV2Request(input.RequestID, changed, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Clear()
	if first.IdempotencyDigest == third.IdempotencyDigest {
		t.Fatal("different normalized requests must not share a digest")
	}
}

func TestBuildGradingAgentV2RequestBindsConfirmedReference(t *testing.T) {
	input := validMathV2AdapterInput()
	input.ReferenceContext = &GradingReferenceContext{
		Source: "confirmed_exam_import_snapshot", SnapshotHash: strings.Repeat("a", 64),
		StandardAnswer: "x=2", EquivalentAnswers: []any{"2=x"},
		SolutionText: "Rearrange and solve.",
	}
	crop := validResolvedActiveCrop(t)
	policy := ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}
	built, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Clear()
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(built.Body))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if err := validateGradingAgentV2RequestFixture(body); err != nil {
		t.Fatal(err)
	}
	reference := body["reference_context"].(map[string]any)
	if reference["standard_answer"] != "x=2" || reference["solution_text"] != "Rearrange and solve." {
		t.Fatalf("reference omitted from model input: %#v", reference)
	}
	if _, ok := reference["solution_steps"].([]any); !ok {
		t.Fatalf("empty solution steps must encode as an array: %#v", reference)
	}
	changed := input
	copyReference := *input.ReferenceContext
	copyReference.SolutionText = "Different confirmed solution."
	changed.ReferenceContext = &copyReference
	other, err := BuildGradingAgentV2Request(input.RequestID, changed, crop, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Clear()
	if built.IdempotencyDigest == other.IdempotencyDigest {
		t.Fatal("confirmed reference must affect request identity")
	}
}

func validMathV2AdapterInput() AdapterInput {
	input := validHTTPAdapterInput()
	input.Subject = "mathematics"
	input.Question.QuestionType = "calculation"
	input.AssessmentSnapshot = assessment.ExamQuestionSnapshot{ID: "snapshot-001", SubjectCode: assessment.SubjectMathematics}
	input.MathEvidence = &MathEvidenceContext{
		ArtifactID: "math-artifact-001", ArtifactVersion: 3, ExamQuestionSnapshotID: "snapshot-001", ScoringVersion: MathScoringVersionV1,
		OverallConfidence: 0.94, Quality: MathEvidenceQuality{Recognition: 0.95, Formula: 0.94, Structure: 0.93, Verification: 0.96, RubricMapping: 0.92, Critical: 0.92},
		Steps:         []MathEvidenceStep{{ID: "s1", Text: "12+8=20", FormulaIDs: []string{"f1"}, Confidence: 0.94}},
		Formulas:      []MathEvidenceFormula{{ID: "f1", StepIDs: []string{"s1"}, CanonicalLatex: "12+8=20", ParseStatus: "parsed", ReasonCodes: []string{}, BoundingBox: mathunderstanding.BoundingBox{X: 0.1, Y: 0.2, Width: 0.4, Height: 0.2}, Confidence: 0.94}},
		Verifications: []MathEvidenceVerification{}, RubricEvidence: []mathunderstanding.RubricEvidence{},
	}
	return input
}

func validResolvedActiveCrop(t *testing.T) ResolvedActiveCrop {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(activeCropTestPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return ResolvedActiveCrop{
		Kind:           "answer_segment_crop",
		MediaType:      "image/png",
		SHA256:         fmt.Sprintf("%x", digest),
		ByteSize:       int64(len(data)),
		WidthPixels:    1,
		HeightPixels:   1,
		NormalizedBBox: ActiveCropBBox{X: 0.1, Y: 0.2, Width: 0.4, Height: 0.3},
		Data:           data,
	}
}

func assertGradingAgentV2ErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var agentErr *GradingAgentError
	if !errors.As(err, &agentErr) || agentErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
