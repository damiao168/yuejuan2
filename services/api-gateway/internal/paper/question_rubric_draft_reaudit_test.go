package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func questionRubricFixture() Question {
	return Question{ID: "question-1", TenantID: "tenant-1", ExamID: "exam-1", QuestionNo: "15", QuestionType: "calculation",
		Score: 4, Stem: "解方程", Options: []string{}, KnowledgePoints: []string{"方程"}, Status: "active",
		AnswerKey: &AnswerKey{ID: "answer-1", QuestionID: "question-1", AnswerVersion: "v1", StandardAnswer: "移项\n求解", EquivalentAnswers: []any{}},
		Solution:  &QuestionSolution{ID: "solution-1", QuestionID: "question-1", SolutionVersion: "v1", Steps: []SolutionStep{{StepNo: 7, Content: "移项"}, {StepNo: 9, Content: "求解"}}, SourceRefs: []PaperImportSourceRef{}}}
}

func copyQuestionRubricFixture(t *testing.T, question Question) Question {
	t.Helper()
	data, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	var result Question
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func serveQuestionRubricSuggestion(t *testing.T, w http.ResponseWriter, r *http.Request) map[string]any {
	t.Helper()
	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		t.Error(err)
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
	question := input["question_candidate"].(map[string]any)
	solution := input["solution_candidate"].(map[string]any)
	provenance := map[string]any{"solution_candidate_id": solution["candidate_id"]}
	if reference, ok := input["question_reference"]; ok {
		provenance["question_reference"] = reference
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"suggested_rubric_candidates": []any{map[string]any{
		"candidate_id": "model-generated", "question_no_normalized": question["question_no_normalized"], "max_score": question["score"],
		"origin": "ai_suggestion_from_solution", "status": "review_required", "provenance": provenance,
		"points": []any{map[string]any{"id": "p1", "description": "正确求解", "suggested_score": question["score"], "evidence_step_ids": []string{"step-1"}, "source_refs": solution["source_refs"]}},
	}}})
	return input
}

// 使用真实 Python 校验器和生成器，只替换模型输出；文件来源为空时仍须有仓储生成的正式题目绑定。
func TestQuestionRubricDraftUsesPythonContractWithoutImportSources(t *testing.T) {
	if os.Getenv("EDUGRADE_PAPER_AI_CONTRACT") != "1" {
		t.Skip("set EDUGRADE_PAPER_AI_CONTRACT=1 to run the installed Python contract")
	}
	for _, source := range []string{"solution_steps", "solution_text", "answer_text"} {
		t.Run(source, func(t *testing.T) {
			question := questionRubricFixture()
			if source == "solution_text" {
				question.Solution.Steps = nil
				question.Solution.RawText = "移项\n$$\nx=2\n$$"
			} else if source == "answer_text" {
				question.Solution = nil
			}
			store := NewMemoryStore()
			store.questions[question.ID] = question
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				command := exec.Command("python", "-c", questionRubricPythonContract)
				command.Dir = filepath.Join("..", "..", "..", "..", "ai-services")
				command.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
				command.Stdin = &body
				output, err := command.CombinedOutput()
				if err != nil {
					t.Errorf("real Python rubric contract rejected gateway input: %v\n%s", err, output)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(output)
			}))
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), 10*time.Second)}
			result, err := suggestQuestionMathRubricDraft(context.Background(), store, service, question.TenantID, question.ExamID, question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question})
			if err != nil || calls != 1 || len(result.SuggestedRubricCandidates) != 1 {
				t.Fatalf("formal question inference: %#v calls=%d err=%v", result, calls, err)
			}
			if result.SuggestedRubricCandidates[0].CandidateID != question.ID || store.questions[question.ID].Rubric != nil {
				t.Fatal("suggestion must retain question identity and remain unpersisted")
			}
		})
	}
}

const questionRubricPythonContract = `import json,sys
from contextlib import nullcontext
from grading_agent.math_rubric_draft import MathRubricDraftGenerator
class Model:
    def session(self, request_id): return nullcontext()
    def request_structured(self, request_id, messages, schema, name):
        return {"points":[{"description":"正确求解","suggested_score":4,"evidence_step_ids":["step-1"],"review_note":""}]}
request=json.load(sys.stdin)
arguments=[request["request_id"],request["question_candidate"],request["answer_candidate"],request["solution_candidate"]]
reference=request.get("question_reference")
result=MathRubricDraftGenerator(Model()).generate(*arguments,**({"question_reference":reference} if reference is not None else {}))
print(json.dumps(result,ensure_ascii=False))
`

func TestQuestionRubricDraftRejectsStaleInputBeforeInference(t *testing.T) {
	for name, mutate := range map[string]func(*Question){
		"knowledge":         func(q *Question) { q.KnowledgePoints = []string{"新知识点"} },
		"question_source":   func(q *Question) { q.PaperImportSourceRefs = []PaperImportSourceRef{{SourceID: "new-source"}} },
		"answer":            func(q *Question) { q.AnswerKey.AnswerVersion = "v2" },
		"solution":          func(q *Question) { q.Solution.SolutionVersion = "v2" },
		"rubric":            func(q *Question) { q.Rubric = &Rubric{ID: "r1", Version: "v2"} },
		"question_identity": func(q *Question) { q.ID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			current := questionRubricFixture()
			expected := copyQuestionRubricFixture(t, current)
			mutate(&expected)
			store := NewMemoryStore()
			store.questions[current.ID] = current
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; serveQuestionRubricSuggestion(t, w, r) }))
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)}
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, current.TenantID, current.ExamID, current.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &expected})
			if !errors.Is(err, ErrConflict) || calls != 0 {
				t.Fatalf("stale input reached inference: calls=%d err=%v", calls, err)
			}
		})
	}
}

// 模型回调期间改仓储，保护慢响应不能沿用旧知识点、来源、答案、详解或评分版本。
func TestQuestionRubricDraftRejectsChangesDuringInference(t *testing.T) {
	for name, mutate := range map[string]func(*Question){
		"knowledge":       func(q *Question) { q.KnowledgePoints = []string{"新知识点"} },
		"question_source": func(q *Question) { q.PaperImportSourceRefs = []PaperImportSourceRef{{SourceID: "new-source"}} },
		"answer":          func(q *Question) { q.AnswerKey.AnswerVersion = "v2" },
		"solution":        func(q *Question) { q.Solution.SolutionVersion = "v2" },
		"rubric":          func(q *Question) { q.Rubric = &Rubric{ID: "r1", Version: "v2"} },
	} {
		t.Run(name, func(t *testing.T) {
			current := questionRubricFixture()
			store := NewMemoryStore()
			store.questions[current.ID] = copyQuestionRubricFixture(t, current)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				changed := copyQuestionRubricFixture(t, current)
				mutate(&changed)
				store.mu.Lock()
				store.questions[current.ID] = changed
				store.mu.Unlock()
				serveQuestionRubricSuggestion(t, w, r)
			}))
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)}
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, current.TenantID, current.ExamID, current.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &current})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("changed %s still accepted: %v", name, err)
			}
		})
	}
}

func TestQuestionRubricDraftModelResolutionFailsClosed(t *testing.T) {
	question := questionRubricFixture()
	store := NewMemoryStore()
	store.questions[question.ID] = question
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; serveQuestionRubricSuggestion(t, w, r) }))
	defer server.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second),
		modelResolver: func(_ context.Context, tenant string) (*DocumentModelConfig, error) {
			if tenant != question.TenantID {
				t.Errorf("resolver used request tenant %q", tenant)
			}
			return nil, fmt.Errorf("sensitive backend failure")
		}}
	_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, question.TenantID, question.ExamID, question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question})
	if err == nil || calls != 0 || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("resolver failure fell back or leaked: calls=%d err=%v", calls, err)
	}
}

func TestQuestionRubricDraftRejectsAlteredResponseReference(t *testing.T) {
	for _, field := range []string{"question_id", "exam_id", "source_type", "source_id", "source_version", "snapshot_sha256", "missing", "unknown"} {
		t.Run(field, func(t *testing.T) {
			question := questionRubricFixture()
			store := NewMemoryStore()
			store.questions[question.ID] = question
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				buffer := httptest.NewRecorder()
				serveQuestionRubricSuggestion(t, buffer, r)
				var response map[string]any
				if err := json.Unmarshal(buffer.Body.Bytes(), &response); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				provenance := response["suggested_rubric_candidates"].([]any)[0].(map[string]any)["provenance"].(map[string]any)
				if field == "missing" {
					delete(provenance, "question_reference")
				} else {
					reference := provenance["question_reference"].(map[string]any)
					reference[field] = "tampered"
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)}
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, question.TenantID, question.ExamID, question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question})
			if err == nil || err.Error() != "math rubric draft question reference invalid" {
				t.Fatalf("tampered reference accepted: %v", err)
			}
		})
	}
}

func TestQuestionRubricDraftAcceptsSourceRefsOmittedByQuestionJSON(t *testing.T) {
	question := questionRubricFixture()
	question.PaperImportSourceRefs = []PaperImportSourceRef{}
	// GET 的 Question 会省略空来源数组；浏览器原样回传后为 nil，仍是同一份当前题目。
	expected := copyQuestionRubricFixture(t, question)
	if expected.PaperImportSourceRefs != nil {
		t.Fatal("fixture did not exercise omitted source refs")
	}
	store := NewMemoryStore()
	store.questions[question.ID] = question
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; serveQuestionRubricSuggestion(t, w, r) }))
	defer server.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("x", 32), time.Second)}
	_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, question.TenantID, question.ExamID, question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &expected})
	if err != nil || calls != 1 {
		t.Fatalf("unchanged GET snapshot rejected: calls=%d err=%v", calls, err)
	}
}
