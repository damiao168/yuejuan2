package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMathUnderstandingContractKeepsEvidenceAndSubjectBoundaries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document, "paths")
	for _, path := range []string{"/api/v1/math-answer-segments/{segmentId}/understanding", "/api/v1/math-understanding/{artifactId}/corrections", "/api/v1/math-pilot-gates", "/api/v1/math-pilot-gates/evaluate", "/api/v1/internal/math-verification/tasks/{taskId}/input", "/api/v1/internal/math-verification/tasks/{taskId}/complete", "/api/v1/internal/math-verification/tasks/{taskId}/fail"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("missing math contract path %s", path)
		}
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	if _, ok := paths["/api/v1/answer-segments/{id}/subjective-ai-grade"]; !ok {
		t.Fatal("missing production subjective math grading route")
	}
	if _, ok := paths["/api/v1/math-answer-segments/{segmentId}/rubric-score"]; !ok {
		t.Fatal("missing frozen math scoring preview route")
	}
	assertMathScoringPreviewAuthority(t, schemas)
	assertMathModelCandidateAuthority(t, schemas)
	assertMathSubjectiveSuggestionBinding(t, schemas)
	assertMathUnderstandingEvidenceAndSDK(t, schemas)
	assertMathArtifactMetadataAndPilotSubjectBoundaries(t, schemas)
}

// 评分预览不能获得最终成绩权限，不确定依据必须允许空分数。
func assertMathScoringPreviewAuthority(t *testing.T, schemas map[string]any) {
	t.Helper()
	score := object(t, schemas, "MathRubricScoreResponse")
	scoreProperties := object(t, score, "properties")
	if object(t, scoreProperties, "scope")["const"] != "teacher_suggestion_only" || object(t, scoreProperties, "requires_human_review")["const"] != true {
		t.Fatal("math preview expanded final-grade authority")
	}
	for _, property := range []string{"verified_score", "unresolved_score", "score_range", "suggested_score", "criterion_decisions", "rubric_snapshot_hash", "artifact_version", "correction_revision"} {
		if _, ok := scoreProperties[property]; !ok {
			t.Fatalf("missing math scoring field %s", property)
		}
	}
	decisionProperties := object(t, object(t, schemas, "MathCriterionDecision"), "properties")
	if _, ok := object(t, decisionProperties, "awarded_score")["anyOf"]; !ok {
		t.Fatal("uncertain criterion must allow a null score")
	}
}

// 模型只能提出语义候选，不得自行发明任何评分字段。
func assertMathModelCandidateAuthority(t *testing.T, schemas map[string]any) {
	t.Helper()
	candidate := object(t, schemas, "MathCriterionCandidate")
	if candidate["additionalProperties"] != false {
		t.Fatal("model candidate must reject invented score fields")
	}
	for _, property := range []string{"score", "awarded_score", "suggested_score", "max_score"} {
		if _, ok := object(t, candidate, "properties")[property]; ok {
			t.Fatalf("model candidate controls %s", property)
		}
	}
	semanticCandidate := object(t, schemas, "MathSemanticCandidate")
	if semanticCandidate["additionalProperties"] != false {
		t.Fatal("grading-agent v2 semantic candidate must reject invented score fields")
	}
	for _, property := range []string{"score", "awarded_score", "suggested_score", "max_score"} {
		if _, ok := object(t, semanticCandidate, "properties")[property]; ok {
			t.Fatalf("grading-agent v2 semantic candidate controls %s", property)
		}
	}
}

// 主观题建议必须绑定不可变数学证据并保留人工复核要求。
func assertMathSubjectiveSuggestionBinding(t *testing.T, schemas map[string]any) {
	t.Helper()
	gradeProperties := object(t, object(t, schemas, "SubjectiveAIGrade"), "properties")
	for _, property := range []string{"math_artifact_id", "math_artifact_version", "math_correction_revision", "math_scoring_version"} {
		if _, ok := gradeProperties[property]; !ok {
			t.Fatalf("subjective AI suggestion must expose immutable math binding %s", property)
		}
	}
	if object(t, gradeProperties, "needs_human_review")["const"] != true {
		t.Fatal("subjective AI suggestion expanded final-grade authority")
	}
}

// 有效证据在HTTP与SDK中保持有类型的可空数组和修订字段。
func assertMathUnderstandingEvidenceAndSDK(t *testing.T, schemas map[string]any) {
	t.Helper()
	understanding := object(t, schemas, "MathUnderstandingResponse")
	understandingProperties := object(t, understanding, "properties")
	for _, property := range []string{"artifact", "effective_artifact", "correction_revision", "corrected", "corrections"} {
		if _, ok := understandingProperties[property]; !ok {
			t.Fatalf("math understanding response must expose %s", property)
		}
	}
	sdk, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packages", "sdk", "src", "generated", "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	var contractType string
	for _, line := range strings.Split(string(sdk), "\n") {
		if strings.HasPrefix(line, "export type MathUnderstandingContract = ") {
			contractType = line
		}
	}
	for property, sdkType := range map[string]string{
		"formulas":        "MathFormulaArtifact",
		"relations":       "Record<string, unknown>",
		"verifications":   "MathVerification",
		"rubric_evidence": "Record<string, unknown>",
	} {
		if !strings.Contains(contractType, `"`+property+`": Array<`+sdkType+`> | null;`) {
			t.Fatalf("effective %s must remain a typed nullable evidence array in the SDK", property)
		}
	}
}

// 证据修订元数据、公式候选与试点学科边界共同约束可追溯的建议能力。
func assertMathArtifactMetadataAndPilotSubjectBoundaries(t *testing.T, schemas map[string]any) {
	t.Helper()
	artifactProperties := object(t, object(t, schemas, "MathUnderstandingArtifact"), "properties")
	for _, property := range []string{"stage", "parent_artifact_id", "correction_revision", "quality_summary"} {
		if _, ok := artifactProperties[property]; !ok {
			t.Fatalf("artifact must expose immutable verification stage metadata %s", property)
		}
	}
	formula := object(t, schemas, "MathFormulaArtifact")
	formulaProperties := object(t, formula, "properties")
	for _, property := range []string{"candidates", "selected_candidate"} {
		if _, ok := formulaProperties[property]; !ok {
			t.Fatalf("formula artifact must expose %s", property)
		}
	}
	step := object(t, schemas, "MathSolutionStep")
	stepProperties := object(t, step, "properties")
	for _, property := range []string{"bbox", "kind", "recognition_confidence", "structure_confidence"} {
		if _, ok := stepProperties[property]; !ok {
			t.Fatalf("solution step must expose %s", property)
		}
	}
	decision := object(t, schemas, "MathPilotGateDecision")
	scope := object(t, object(t, decision, "properties"), "scope")
	if scope["const"] != "teacher_suggestion_only" {
		t.Fatalf("pilot gate must never grant automatic final scoring: %#v", scope)
	}
	request := object(t, schemas, "EvaluateMathPilotGateRequest")
	subject := object(t, object(t, request, "properties"), "subject_code")
	enums, ok := subject["enum"].([]any)
	if !ok || len(enums) != 3 || enums[0] != "mathematics" || enums[1] != "physics" || enums[2] != "chemistry" {
		t.Fatalf("formula subjects must remain explicitly bounded: %#v", subject)
	}
}
