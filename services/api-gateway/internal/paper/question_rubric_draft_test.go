package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

func questionDraftFixture(t *testing.T) (*MemoryStore, Question) {
	t.Helper()
	store := NewMemoryStore()
	question := Question{ID: "q15", TenantID: "tenant", ExamID: "exam", QuestionNo: "15", QuestionType: "calculation",
		Score: 10, Stem: "解方程", Status: "draft", Options: []string{}, KnowledgePoints: []string{},
		AnswerKey: &AnswerKey{ID: "answer-15", QuestionID: "q15", AnswerVersion: "v1", StandardAnswer: "x=2"},
		Solution: &QuestionSolution{ID: "solution-15", QuestionID: "q15", SolutionVersion: "v1",
			RawText: "移项\n得出结果", Steps: []SolutionStep{{StepNo: 7, Content: " "}, {StepNo: 9, Content: "移项得出结果"}}, SourceRefs: []PaperImportSourceRef{}}}
	store.questions[question.ID] = question
	return store, cloneQuestionDraftFixture(t, question)
}

func cloneQuestionDraftFixture(t *testing.T, question Question) Question {
	t.Helper()
	data, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	var copy Question
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func questionDraftServer(t *testing.T, onRequest func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/paper/rubric-draft" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
			t.Errorf("unexpected suggestion transport: %s", r.URL.Path)
		}
		var input struct {
			Question  QuestionCandidate        `json:"question_candidate"`
			Solution  SolutionCandidate        `json:"solution_candidate"`
			Model     *DocumentModelConfig     `json:"managed_model"`
			Reference *questionRubricReference `json:"question_reference"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if onRequest != nil {
			onRequest(input.Question, input.Solution, input.Model)
		}
		evidence := []string{}
		for index, step := range input.Solution.Steps {
			if strings.TrimSpace(step.Content) != "" {
				evidence = append(evidence, fmt.Sprintf("step-%d", index+1))
				break
			}
		}
		_ = json.NewEncoder(w).Encode(MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{
			CandidateID: "generated", QuestionNoNormalized: input.Question.QuestionNoNormalized, MaxScore: input.Question.Score,
			Origin: "ai_suggestion_from_solution", Status: "review_required", Issues: []string{},
			Provenance: map[string]any{"solution_candidate_id": input.Solution.CandidateID, "question_reference": input.Reference},
			Points: []MathRubricDraftPoint{{ID: "p1", Description: "正确推导结果", SuggestedScore: input.Question.Score,
				EvidenceStepIDs: evidence, SourceRefs: input.Solution.SourceRefs}},
		}}})
	}))
}

func TestQuestionRubricDraftUsesStoredContentAndKeepsHumanConfirmation(t *testing.T) {
	store, question := questionDraftFixture(t)
	calls := 0
	server := questionDraftServer(t, func(q QuestionCandidate, solution SolutionCandidate, model *DocumentModelConfig) {
		calls++
		if q.CandidateID != question.ID || q.Stem != question.Stem || solution.CandidateID != "question-solution-q15" || len(solution.Steps) != 2 || solution.Steps[1].StepNo != 9 {
			t.Errorf("stored question/step positions were not retained: %#v %#v", q, solution)
		}
		if solution.SourceRefs == nil || len(solution.SourceRefs) != 0 {
			t.Errorf("manual reference solution must retain explicit empty provenance: %#v", solution.SourceRefs)
		}
		if model == nil || model.ModelName != "school-document-model" || model.APIKey != "private-key" {
			t.Errorf("school document model not used: %#v", model)
		}
	})
	defer server.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)}
	service.WithModelResolver(func(_ context.Context, tenantID string) (*DocumentModelConfig, error) {
		if tenantID != "tenant" {
			t.Errorf("model tenant = %s", tenantID)
		}
		return &DocumentModelConfig{ModelName: "school-document-model", APIKey: "private-key"}, nil
	})
	result, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", "q15", QuestionMathRubricDraftInput{ExpectedQuestion: &question})
	if err != nil || calls != 1 || len(result.SuggestedRubricCandidates) != 1 {
		t.Fatalf("suggestion result %#v error %v calls %d", result, err, calls)
	}
	point := result.SuggestedRubricCandidates[0]
	if point.CandidateID != question.ID || point.Status != "review_required" || !reflect.DeepEqual(point.Points[0].EvidenceStepIDs, []string{"step-2"}) {
		t.Fatalf("suggestion binding changed: %#v", point)
	}
	stored, _ := store.ListQuestions(context.Background(), "tenant", "exam")
	if stored[0].Rubric != nil || len(store.rubrics) != 0 {
		t.Fatal("AI suggestion wrote a grading rubric before teacher confirmation")
	}
	response, _ := json.Marshal(result)
	if bytes.Contains(response, []byte("private-key")) {
		t.Fatal("managed credential reached browser response")
	}
}

func TestQuestionRubricDraftRejectsStaleAndCrossExamRequestsBeforeModel(t *testing.T) {
	for _, field := range []string{"stem", "score", "question_no", "answer", "solution", "rubric", "tenant", "exam", "locked", "objective", "no solution"} {
		t.Run(field, func(t *testing.T) {
			store, expected := questionDraftFixture(t)
			current := cloneQuestionDraftFixture(t, expected)
			wantErr := ErrConflict
			switch field {
			case "stem":
				expected.Stem = "浏览器注入的题干"
			case "score":
				expected.Score = 20
			case "question_no":
				expected.QuestionNo = "16"
			case "answer":
				expected.AnswerKey.StandardAnswer = "x=99"
			case "solution":
				expected.Solution.Steps[1].Content = "伪造参考解答"
			case "rubric":
				expected.Rubric = &Rubric{ID: "new", Version: "v2"}
			case "tenant":
				current.TenantID = "another-tenant"
				wantErr = ErrNotFound
			case "exam":
				current.ExamID = "another-exam"
				wantErr = ErrNotFound
			case "locked":
				current.Rubric = &Rubric{Status: "locked"}
				expected = cloneQuestionDraftFixture(t, current)
				wantErr = ErrRubricLocked
			case "objective":
				current.QuestionType = "single_choice"
				expected = cloneQuestionDraftFixture(t, current)
				wantErr = ErrInvalidInput
			case "no solution":
				current.Solution = nil
				current.AnswerKey.StandardAnswer = map[string]any{"final": "x=2"}
				expected = cloneQuestionDraftFixture(t, current)
				wantErr = ErrInvalidInput
			}
			store.questions[current.ID] = current
			service := &DocumentImportService{parser: NewHTTPDocumentParser("http://must-not-call.invalid", strings.Repeat("t", 32), time.Second)}
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", "q15", QuestionMathRubricDraftInput{ExpectedQuestion: &expected})
			if !errors.Is(err, wantErr) {
				t.Fatalf("expected %v before network call, got %v", wantErr, err)
			}
		})
	}
}

func TestQuestionRubricDraftDiscardsEditsDuringModelCall(t *testing.T) {
	for _, change := range []string{"stem", "answer", "solution", "new rubric", "deleted"} {
		t.Run(change, func(t *testing.T) {
			store, question := questionDraftFixture(t)
			server := questionDraftServer(t, func(_ QuestionCandidate, _ SolutionCandidate, _ *DocumentModelConfig) {
				store.mu.Lock()
				defer store.mu.Unlock()
				current := store.questions[question.ID]
				switch change {
				case "stem":
					current.Stem = "新题干"
				case "answer":
					current.AnswerKey.StandardAnswer = "x=3"
				case "solution":
					current.Solution.SolutionVersion = "v2"
				case "new rubric":
					store.rubrics[question.ID] = []Rubric{{ID: "r1", Version: "v1", Status: "draft"}}
				case "deleted":
					current.Status = "deleted"
				}
				store.questions[question.ID] = current
			})
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)}
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", "q15", QuestionMathRubricDraftInput{ExpectedQuestion: &question})
			if change == "deleted" && !errors.Is(err, ErrNotFound) || change != "deleted" && !errors.Is(err, ErrConflict) {
				t.Fatalf("concurrent %s left applicable suggestion: %v", change, err)
			}
		})
	}
}

func TestQuestionRubricDraftRejectsIneligibleSchoolModel(t *testing.T) {
	store, question := questionDraftFixture(t)
	service := &DocumentImportService{parser: NewHTTPDocumentParser("http://must-not-call.invalid", strings.Repeat("t", 32), time.Second)}
	service.WithModelResolver(func(context.Context, string) (*DocumentModelConfig, error) {
		return nil, errors.New("structured model evidence is missing")
	})
	_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", "q15", QuestionMathRubricDraftInput{ExpectedQuestion: &question})
	if err == nil || err.Error() != "school model configuration unavailable" {
		t.Fatalf("ineligible school model was not rejected: %v", err)
	}
}

func TestQuestionRubricDraftTextStepsMatchTeacherRangeEvidence(t *testing.T) {
	for _, item := range []struct {
		name, text string
		want       []string
	}{
		{"ordinary lines", "  移项\r\n\r\n x=2 \r结论", []string{"移项", "x=2", "结论"}},
		{"dollar display", "先化简\n$$\nx+1\n=3\n$$ 后得\nx=2", []string{"先化简", "$$\nx+1\n=3\n$$ 后得", "x=2"}},
		{"bracket display", "\\[\nx+1\n=3\n\\]\n答案", []string{"\\[\nx+1\n=3\n\\]", "答案"}},
		{"unclosed display", "开始\n$$\na=b\n末行", []string{"开始", "$$\na=b\n末行"}},
		{"mixed delimiters", "\\[ $$\nx\n\\] end\nlast", []string{"\\[ $$\nx\n\\] end", "last"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			steps := mathRubricTextSteps(item.text)
			contents := []string{}
			for index, step := range steps {
				if step.StepNo != index+1 {
					t.Fatal("logical lines lost stable positions")
				}
				contents = append(contents, step.Content)
			}
			if !reflect.DeepEqual(contents, item.want) {
				t.Fatalf("got %#v, want %#v", contents, item.want)
			}
		})
	}
	question := Question{Score: 5, QuestionNo: "1", QuestionType: "calculation", AnswerKey: &AnswerKey{StandardAnswer: "先计算\n得结论", PaperImportSourceRefs: []PaperImportSourceRef{{SourceID: "answer-source"}}}}
	inputs, err := prepareQuestionMathRubricInputs(question)
	if err != nil || len(inputs.solution.Steps) != 2 || inputs.solution.SourceRefs[0].SourceID != "answer-source" {
		t.Fatalf("text answer fallback failed: %#v %v", inputs, err)
	}
}

type questionDraftExamReader func(context.Context, auth.AccessScope, string) (exam.Exam, error)

func (read questionDraftExamReader) GetExam(ctx context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	return read(ctx, scope, id)
}

func TestQuestionRubricDraftHandlerEnforcesSubjectAndExamStage(t *testing.T) {
	for _, caseName := range []string{"nonmath", "frozen", "frozen during inference", "scope mismatch", "allowed"} {
		t.Run(caseName, func(t *testing.T) {
			store, question := questionDraftFixture(t)
			calls := 0
			server := questionDraftServer(t, func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig) { calls++ })
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)}
			examReads := 0
			reader := questionDraftExamReader(func(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
				if scope.ActorID != "teacher" || scope.TenantID != "tenant" || id != "exam" {
					t.Errorf("caller access scope not retained: %#v %s", scope, id)
				}
				examReads++
				item := exam.Exam{ID: "exam", TenantID: "tenant", Subject: "数学", Status: "draft"}
				if caseName == "nonmath" {
					item.Subject = "english"
				}
				if caseName == "frozen" || caseName == "frozen during inference" && examReads > 1 {
					item.Status = "collecting"
				}
				return item, nil
			})
			handler := NewHandler(store, nil).WithDocumentImport(service).WithQuestionRubricExams(reader)
			body, _ := json.Marshal(QuestionMathRubricDraftInput{ExpectedQuestion: &question})
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			r.SetPathValue("examId", "exam")
			r.SetPathValue("questionId", "q15")
			scope := auth.AccessScope{TenantID: "tenant", ActorID: "teacher", TenantWide: true}
			if caseName == "scope mismatch" {
				scope.TenantID = "other"
			}
			r = r.WithContext(auth.WithAccessScope(auth.WithUser(r.Context(), auth.User{ID: "teacher", TenantID: "tenant"}), scope))
			response := httptest.NewRecorder()
			handler.SuggestQuestionMathRubricDraft(response, r)
			wantStatus, wantCalls := http.StatusOK, 1
			switch caseName {
			case "nonmath":
				wantStatus, wantCalls = http.StatusBadRequest, 0
			case "frozen":
				wantStatus, wantCalls = http.StatusConflict, 0
			case "frozen during inference":
				wantStatus = http.StatusConflict
			case "scope mismatch":
				wantStatus, wantCalls = http.StatusForbidden, 0
			}
			if response.Code != wantStatus || calls != wantCalls {
				t.Fatalf("got %d calls=%d body=%s; want %d calls=%d", response.Code, calls, response.Body.String(), wantStatus, wantCalls)
			}
		})
	}
}
