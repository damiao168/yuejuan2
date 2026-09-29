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

func TestSuggestMathRubricDraftUsesCurrentCandidateWithoutPersistingSuggestion(t *testing.T) {
	const token = "test-service-token-with-at-least-32-characters"
	store, job := candidateImport(t)
	job.Status = "review_required"
	job.Generation = 2
	job.Questions = []PaperImportDraftQuestion{{CandidateID: "q15", QuestionNo: "16", SolutionCandidateID: "s15", QuestionType: "calculation", Score: 11, Stem: "解方程", MatchStatus: "matched", AnswerKey: &AnswerKeyInput{StandardAnswer: "x=2"}, Solution: &SolutionInput{Steps: []SolutionStep{{StepNo: 1, Content: "移项"}}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}}}
	job.QuestionCandidates = []QuestionCandidate{{CandidateID: "q15", QuestionNoNormalized: "15", QuestionType: "calculation", Stem: "旧题干"}}
	job.AnswerCandidates = []AnswerCandidate{{CandidateID: "a15", QuestionNoNormalized: "15", StandardAnswer: "x=2"}}
	job.SolutionCandidates = []SolutionCandidate{{CandidateID: "s15", QuestionNoNormalized: "15", Steps: []SolutionStep{{StepNo: 1, Content: "移项"}}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}}
	store.imports[job.ID] = job
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/paper/rubric-draft" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		question := input["question_candidate"].(map[string]any)
		if question["stem"] != "解方程" || question["score"] != float64(11) {
			t.Errorf("reviewed question context not sent: %#v", question)
		}
		if question["question_no_normalized"] != "16" || input["solution_candidate"].(map[string]any)["candidate_id"] != "s15" {
			t.Errorf("renumbered question lost its bound solution: %#v", input)
		}
		maximum := 11.0
		_ = json.NewEncoder(w).Encode(MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{
			CandidateID: "suggested-15", QuestionNoNormalized: "16", MaxScore: &maximum, Origin: "ai_suggestion_from_solution", Status: "review_required",
			Provenance: map[string]any{"solution_candidate_id": "s15"},
			Points:     []MathRubricDraftPoint{{ID: "p1", Description: "正确移项", EvidenceStepIDs: []string{"step-1"}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}},
		}}})
	}))
	defer server.Close()
	service := &DocumentImportService{store: store, parser: NewHTTPDocumentParser(server.URL, token, time.Second)}
	result, err := service.SuggestMathRubricDraft(context.Background(), "tenant", job.ID, MathRubricDraftInput{CandidateID: "q15", ExpectedGeneration: 2, ExpectedUpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)})
	if err != nil || len(result.SuggestedRubricCandidates) != 1 {
		t.Fatalf("draft failed: %#v, %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("expected one model call, got %d", calls)
	}
	if result.SuggestedRubricCandidates[0].CandidateID != "q15" {
		t.Fatal("browser response must retain originating question candidate identity")
	}
	stored, err := store.GetPaperImport(context.Background(), "tenant", job.ID)
	if err != nil || stored.Questions[0].Rubric != nil {
		t.Fatalf("AI suggestion must not mutate source draft: %#v, %v", stored.Questions, err)
	}
	_, err = service.SuggestMathRubricDraft(context.Background(), "tenant", job.ID, MathRubricDraftInput{CandidateID: "q15", ExpectedGeneration: 1, ExpectedUpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)})
	if err != ErrConflict || calls != 1 {
		t.Fatalf("stale request should be rejected before model call: %v", err)
	}
	_, err = service.SuggestMathRubricDraft(context.Background(), "tenant", job.ID, MathRubricDraftInput{CandidateID: "q15", ExpectedGeneration: 2, ExpectedUpdatedAt: job.UpdatedAt.Add(-time.Microsecond).Format(time.RFC3339Nano)})
	if err != ErrConflict || calls != 1 {
		t.Fatalf("stale review save should be rejected before model call: %v", err)
	}
	_, err = service.SuggestMathRubricDraft(context.Background(), "tenant", job.ID, MathRubricDraftInput{CandidateID: strings.Repeat("x", 5), ExpectedGeneration: 2, ExpectedUpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)})
	if err != ErrInvalidInput || calls != 1 {
		t.Fatalf("unknown candidate should be rejected: %v", err)
	}
}

func TestSuggestMathRubricDraftRejectsReviewSavedDuringInference(t *testing.T) {
	const token = "test-service-token-with-at-least-32-characters"
	store, job := candidateImport(t)
	job.Status = "review_required"
	job.Questions = []PaperImportDraftQuestion{{CandidateID: "q1", QuestionNo: "1", SolutionCandidateID: "s1", QuestionType: "calculation", Score: 5, Stem: "计算", MatchStatus: "matched", Solution: &SolutionInput{Steps: []SolutionStep{{StepNo: 1, Content: "先计算"}}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}}}
	job.QuestionCandidates = []QuestionCandidate{{CandidateID: "q1", QuestionNoNormalized: "1", QuestionType: "calculation"}}
	job.SolutionCandidates = []SolutionCandidate{{CandidateID: "s1", QuestionNoNormalized: "1", Steps: []SolutionStep{{StepNo: 1, Content: "先计算"}}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}}
	store.imports[job.ID] = job
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "teacher", ReviewPaperImportInput{ExpectedGeneration: job.Generation, Questions: job.Questions}); err != nil {
			t.Errorf("concurrent review save: %v", err)
		}
		maximum := 5.0
		_ = json.NewEncoder(w).Encode(MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{
			QuestionNoNormalized: "1", MaxScore: &maximum, Origin: "ai_suggestion_from_solution", Status: "review_required",
			Provenance: map[string]any{"solution_candidate_id": "s1"},
			Points:     []MathRubricDraftPoint{{ID: "p1", Description: "先计算", EvidenceStepIDs: []string{"step-1"}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}},
		}}})
	}))
	defer server.Close()
	service := &DocumentImportService{store: store, parser: NewHTTPDocumentParser(server.URL, token, time.Second)}
	_, err := service.SuggestMathRubricDraft(context.Background(), "tenant", job.ID, MathRubricDraftInput{CandidateID: "q1", ExpectedGeneration: job.Generation, ExpectedUpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)})
	if err != ErrConflict {
		t.Fatalf("save during inference must invalidate result, got %v", err)
	}
}

func TestMathRubricDraftRejectsUnboundModelResponse(t *testing.T) {
	score := 4.0
	question := QuestionCandidate{CandidateID: "q1", QuestionNoNormalized: "1", Score: &score}
	solution := SolutionCandidate{CandidateID: "s1", QuestionNoNormalized: "1", Steps: []SolutionStep{{Content: "Solve"}}, SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}
	for _, invalid := range []string{"question", "solution", "evidence", "negative", "source"} {
		t.Run(invalid, func(t *testing.T) {
			pointScore := 4.0
			result := MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{
				QuestionNoNormalized: "1", MaxScore: &score, Origin: "ai_suggestion_from_solution", Status: "review_required",
				Provenance: map[string]any{"solution_candidate_id": "s1"},
				Points:     []MathRubricDraftPoint{{ID: "p1", Description: "Solve correctly", SuggestedScore: &pointScore, EvidenceStepIDs: []string{"step-1"}, SourceRefs: solution.SourceRefs}},
			}}}
			suggestion := &result.SuggestedRubricCandidates[0]
			switch invalid {
			case "question":
				suggestion.QuestionNoNormalized = "2"
			case "solution":
				suggestion.Provenance["solution_candidate_id"] = "s2"
			case "evidence":
				suggestion.Points[0].EvidenceStepIDs = []string{"step-99"}
			case "negative":
				pointScore = -1
			case "source":
				suggestion.Points[0].SourceRefs = []PaperImportSourceRef{{SourceID: "other"}}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(result) }))
			defer server.Close()
			client := NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)
			if _, err := client.SuggestMathRubricDraft(context.Background(), question, nil, solution, nil); err == nil {
				t.Fatal("unbound suggestion must not reach teacher review")
			}
		})
	}
}
