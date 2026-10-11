package paper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBatch42MathRubricDraftPreservesEvidenceStepPositions(t *testing.T) {
	score := 4.0
	question := QuestionCandidate{CandidateID: "q1", QuestionNoNormalized: "1", Score: &score}
	solution := SolutionCandidate{CandidateID: "s1", QuestionNoNormalized: "1",
		Steps:      []SolutionStep{{Content: " "}, {Content: "Solve"}},
		SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}
	for _, testCase := range []rubricDraftPositionCase{
		{name: "original nonblank position", evidenceID: "step-2"},
		{name: "blank position rejected", evidenceID: "step-1", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runRubricDraftPositionCase(t, question, solution, &score, testCase)
		})
	}
}

// 空白步骤仍占原始位置；模型证据只能指向实际非空的原步骤，不能重排编号。
type rubricDraftPositionCase struct {
	name, evidenceID string
	wantError        bool
}

func runRubricDraftPositionCase(t *testing.T, question QuestionCandidate, solution SolutionCandidate, score *float64, testCase rubricDraftPositionCase) {
	t.Helper()
	response := MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{
		CandidateID: "model-suggestion", QuestionNoNormalized: "1", MaxScore: score,
		Origin: "ai_suggestion_from_solution", Status: "review_required",
		Provenance: map[string]any{"solution_candidate_id": "s1"},
		Points: []MathRubricDraftPoint{{ID: "p1", Description: "Solve", SuggestedScore: score,
			EvidenceStepIDs: []string{testCase.evidenceID}, SourceRefs: solution.SourceRefs}},
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	client := NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)
	result, err := client.SuggestMathRubricDraft(context.Background(), question, nil, solution, nil)
	if testCase.wantError {
		if err == nil || err.Error() != "math rubric draft evidence invalid" {
			t.Fatalf("blank step accepted or wrong first error: %v", err)
		}
		return
	}
	if err != nil || len(result.SuggestedRubricCandidates) != 1 {
		t.Fatalf("original step position rejected: %#v, %v", result, err)
	}
	if result.SuggestedRubricCandidates[0].CandidateID != question.CandidateID {
		t.Fatal("response lost originating question identity")
	}
}
