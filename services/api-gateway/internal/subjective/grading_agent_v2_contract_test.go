package subjective

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	gradingAgentV2SchemaVersion = "grading-agent-v2"
	gradingAgentV2MaxMediaBytes = 5 * 1024 * 1024
	gradingAgentV2MaxPixels     = 12_000_000
	gradingAgentV2BBoxScale     = 1_000_000
)

func TestGradingAgentV2RequestFixtureHasVerifiedBoundPNG(t *testing.T) {
	request := loadGradingAgentV2Fixture(t, "valid-request.json")
	if err := validateGradingAgentV2RequestFixture(request); err != nil {
		t.Fatal(err)
	}
}

func TestGradingAgentV2InvalidRequestFixturesFailClosed(t *testing.T) {
	for _, name := range []string{"invalid-request-remote-url.json", "invalid-request-whole-page.json"} {
		t.Run(name, func(t *testing.T) {
			if err := validateGradingAgentV2RequestFixture(loadGradingAgentV2Fixture(t, name)); err == nil {
				t.Fatalf("%s unexpectedly passed validation", name)
			}
		})
	}
}

func TestGradingAgentV2ResponseFixturesBindKnownMathEvidenceWithoutScores(t *testing.T) {
	request := loadGradingAgentV2Fixture(t, "valid-request.json")
	response := loadGradingAgentV2Fixture(t, "valid-response.json")
	if err := validateGradingAgentV2ResponseFixture(response, request); err != nil {
		t.Fatal(err)
	}
	invalid := loadGradingAgentV2Fixture(t, "invalid-response-unknown-evidence-id.json")
	if err := validateGradingAgentV2ResponseFixture(invalid, request); err == nil {
		t.Fatal("unknown math evidence fixture unexpectedly passed validation")
	}
}

func TestGradingAgentV2ErrorFixtureRemainsNonRetryable(t *testing.T) {
	fixture := loadGradingAgentV2Fixture(t, "valid-error.json")
	if err := exactV2Fields(fixture, "schema_version", "request_id", "error"); err != nil {
		t.Fatal(err)
	}
	if fixture["schema_version"] != gradingAgentV2SchemaVersion {
		t.Fatalf("unexpected schema version: %v", fixture["schema_version"])
	}
	body, ok := fixture["error"].(map[string]any)
	if !ok {
		t.Fatal("error body must be an object")
	}
	if err := exactV2Fields(body, "code", "message", "retryable"); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "invalid_request" || body["retryable"] != false {
		t.Fatalf("unsafe error fixture: %#v", body)
	}
}

func TestLegacyProductionHTTPAdapterRemainsOnGradingAgentV1(t *testing.T) {
	if gradingAgentSchemaVersion != "grading-agent-v1" {
		t.Fatalf("061B0.3A must not switch the production adapter: %s", gradingAgentSchemaVersion)
	}
}

func loadGradingAgentV2Fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "contracts", "grading-agent", "v2", "fixtures", name)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("fixture contains trailing JSON: %v", err)
	}
	return value
}

func validateGradingAgentV2RequestFixture(request map[string]any) error {
	expected := []string{
		"schema_version", "request_id", "subject", "grade_level", "agent_role", "question_id", "answer_segment_id",
		"question_type", "question_text", "max_score", "answer_text", "ocr_confidence", "rubric_version",
		"prompt_version", "rubric", "model_policy", "prompt_guard", "output_constraint", "media_evidence", "math_evidence",
	}
	if _, exists := request["reference_context"]; exists {
		expected = append(expected, "reference_context")
	}
	if err := exactV2Fields(request, expected...); err != nil {
		return err
	}
	if request["schema_version"] != gradingAgentV2SchemaVersion {
		return fmt.Errorf("unexpected schema version: %v", request["schema_version"])
	}
	for _, forbidden := range []string{
		"tenant_id", "school_id", "student_id", "student_name", "student_no", "class_id", "exam_id",
		"submission_id", "final_score", "published_score", "original_filename", "bucket", "storage_key", "url",
	} {
		if _, exists := request[forbidden]; exists {
			return fmt.Errorf("request leaks forbidden field %s", forbidden)
		}
	}
	constraint, ok := request["output_constraint"].(map[string]any)
	if !ok {
		return errors.New("output_constraint must be an object")
	}
	if err := exactV2Fields(constraint, "criteria_evidence_only", "allow_model_final_score", "final_score_authority"); err != nil {
		return err
	}
	if constraint["criteria_evidence_only"] != true || constraint["allow_model_final_score"] != false ||
		constraint["final_score_authority"] != "server_rubric_or_human_confirmation" {
		return errors.New("output_constraint must preserve server-owned final-score authority")
	}
	mathEvidence, ok := request["math_evidence"].(map[string]any)
	if !ok {
		return errors.New("math_evidence must be an object")
	}
	if err := exactV2Fields(mathEvidence, "artifact_id", "artifact_version", "correction_revision", "exam_question_snapshot_id", "scoring_version", "overall_confidence", "quality", "steps", "formulas", "verifications", "rubric_evidence", "has_diagram", "graph_uncertain", "required_rubric_uncertain"); err != nil {
		return err
	}
	if mathEvidence["scoring_version"] != MathScoringVersionV1 {
		return errors.New("math scoring version is invalid")
	}

	media, ok := request["media_evidence"].(map[string]any)
	if !ok {
		return errors.New("media_evidence must be an object")
	}
	if err := exactV2Fields(
		media,
		"kind", "encoding", "media_type", "sha256", "byte_size", "width_pixels", "height_pixels",
		"normalized_bbox", "binding_hash", "data_base64",
	); err != nil {
		return err
	}
	if media["kind"] != "answer_segment_crop" || media["encoding"] != "base64" || media["media_type"] != "image/png" {
		return errors.New("media evidence literals are not approved")
	}
	size, err := v2JSONInt(media["byte_size"])
	if err != nil || size < 1 || size > gradingAgentV2MaxMediaBytes {
		return errors.New("media byte size is invalid")
	}
	width, err := v2JSONInt(media["width_pixels"])
	if err != nil || width < 1 {
		return errors.New("media width is invalid")
	}
	height, err := v2JSONInt(media["height_pixels"])
	if err != nil || height < 1 || width > gradingAgentV2MaxPixels/height {
		return errors.New("media height or pixel count is invalid")
	}
	bbox, units, err := gradingAgentV2BBox(media["normalized_bbox"])
	if err != nil {
		return err
	}
	if bbox[2]*bbox[3] >= 0.9 {
		return errors.New("whole-page or near-whole-page media evidence is forbidden")
	}
	encoded, ok := media["data_base64"].(string)
	if !ok || encoded == "" {
		return errors.New("media data must be non-empty base64")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return errors.New("media data is not valid base64")
	}
	if len(decoded) != size {
		return errors.New("media byte size does not match")
	}
	if len(decoded) < 24 || !bytes.Equal(decoded[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) || string(decoded[12:16]) != "IHDR" {
		return errors.New("media is not a PNG")
	}
	if int(binary.BigEndian.Uint32(decoded[16:20])) != width || int(binary.BigEndian.Uint32(decoded[20:24])) != height {
		return errors.New("media dimensions do not match decoded PNG")
	}
	digest := sha256.Sum256(decoded)
	if media["sha256"] != hex.EncodeToString(digest[:]) {
		return errors.New("media hash does not match decoded PNG")
	}
	expectedBinding, err := gradingAgentV2BindingHash(request, units)
	if err != nil {
		return err
	}
	if media["binding_hash"] != expectedBinding {
		return errors.New("media binding does not match request")
	}
	return nil
}

func validateGradingAgentV2ResponseFixture(response map[string]any, request map[string]any) error {
	if err := exactV2Fields(
		response,
		"schema_version", "request_id", "status", "delivery", "criterion_candidates", "alternative_solution_candidate", "risk_flags", "needs_human_review",
		"model_version", "prompt_version", "rubric_version",
		"capability_profile", "mock", "telemetry",
	); err != nil {
		return err
	}
	if response["schema_version"] != gradingAgentV2SchemaVersion ||
		response["request_id"] != request["request_id"] ||
		response["status"] != "candidate_mapping" || response["delivery"] != "teacher_suggestion" ||
		response["needs_human_review"] != true ||
		response["mock"] != false {
		return errors.New("response governance fields are invalid")
	}
	for _, forbidden := range []string{"suggested_score", "max_score", "matched_points", "missing_points", "deductions", "final_score"} {
		if _, exists := response[forbidden]; exists {
			return fmt.Errorf("model response contains forbidden score field %s", forbidden)
		}
	}
	mathEvidence := request["math_evidence"].(map[string]any)
	knownEvidence := map[string]bool{}
	for _, field := range []string{"steps", "formulas"} {
		items, ok := mathEvidence[field].([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", field)
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return errors.New("math evidence item must be an object")
			}
			id, _ := item["id"].(string)
			knownEvidence[id] = id != ""
		}
	}
	rubric := request["rubric"].(map[string]any)
	knownPoints := map[string]bool{}
	for _, raw := range rubric["points"].([]any) {
		point := raw.(map[string]any)
		id, _ := point["id"].(string)
		knownPoints[id] = true
	}
	candidates, ok := response["criterion_candidates"].([]any)
	if !ok {
		return errors.New("criterion_candidates must be an array")
	}
	seen := map[string]bool{}
	for _, raw := range candidates {
		candidate, ok := raw.(map[string]any)
		if !ok {
			return errors.New("candidate must be an object")
		}
		if err := exactV2Fields(candidate, "rubric_point_id", "status", "evidence_ids", "confidence", "reason_code"); err != nil {
			return err
		}
		pointID, _ := candidate["rubric_point_id"].(string)
		if !knownPoints[pointID] || seen[pointID] {
			return errors.New("candidate point is unknown or duplicated")
		}
		seen[pointID] = true
		links, ok := candidate["evidence_ids"].([]any)
		if !ok {
			return errors.New("candidate evidence ids must be an array")
		}
		for _, link := range links {
			id, _ := link.(string)
			if !knownEvidence[id] {
				return errors.New("candidate evidence link is invalid")
			}
		}
	}
	return nil
}

func gradingAgentV2BBox(raw any) ([4]float64, [4]int, error) {
	var values [4]float64
	var units [4]int
	bbox, ok := raw.(map[string]any)
	if !ok {
		return values, units, errors.New("bbox must be an object")
	}
	if err := exactV2Fields(bbox, "x", "y", "width", "height"); err != nil {
		return values, units, err
	}
	for index, field := range []string{"x", "y", "width", "height"} {
		number, ok := bbox[field].(json.Number)
		if !ok {
			return values, units, errors.New("bbox value must be a number")
		}
		value, err := number.Float64()
		if err != nil {
			return values, units, err
		}
		values[index] = value
		units[index] = int(value*gradingAgentV2BBoxScale + 0.5)
		if values[index] != float64(units[index])/gradingAgentV2BBoxScale {
			return values, units, errors.New("bbox supports at most six decimal places")
		}
	}
	if values[0] < 0 || values[1] < 0 || values[2] <= 0 || values[3] <= 0 ||
		values[0]+values[2] > 1 || values[1]+values[3] > 1 {
		return values, units, errors.New("bbox must stay within its source")
	}
	return values, units, nil
}

func gradingAgentV2BindingHash(request map[string]any, bbox [4]int) (string, error) {
	media, ok := request["media_evidence"].(map[string]any)
	if !ok {
		return "", errors.New("media_evidence must be an object")
	}
	parts := []string{gradingAgentV2SchemaVersion}
	for _, field := range []string{"request_id", "question_id", "answer_segment_id"} {
		value, ok := request[field].(string)
		if !ok || value == "" {
			return "", fmt.Errorf("%s must be a string", field)
		}
		parts = append(parts, value)
	}
	cropHash, ok := media["sha256"].(string)
	if !ok || cropHash == "" {
		return "", errors.New("media sha256 must be a string")
	}
	parts = append(parts, cropHash)
	for _, value := range bbox {
		parts = append(parts, strconv.Itoa(value))
	}
	var material strings.Builder
	for _, part := range parts {
		_, _ = fmt.Fprintf(&material, "%d:%s", len([]byte(part)), part)
	}
	digest := sha256.Sum256([]byte(material.String()))
	return hex.EncodeToString(digest[:]), nil
}

func exactV2Fields(value map[string]any, expected ...string) error {
	if len(value) != len(expected) {
		return fmt.Errorf("field count mismatch: got=%d want=%d", len(value), len(expected))
	}
	for _, field := range expected {
		if _, ok := value[field]; !ok {
			return fmt.Errorf("missing field %s", field)
		}
	}
	return nil
}

func v2JSONInt(value any) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("value must be a JSON number")
	}
	parsed, err := strconv.Atoi(number.String())
	if err != nil {
		return 0, err
	}
	return parsed, nil
}
