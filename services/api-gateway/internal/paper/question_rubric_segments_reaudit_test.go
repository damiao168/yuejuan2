package paper

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 分段后步骤重新编号，但理解公式所需的完整上下文仍来自仓储；模型引用不能跳回原步骤编号。
func TestQuestionRubricSegmentsReachRealPythonWithStoredContext(t *testing.T) {
	if os.Getenv("EDUGRADE_PAPER_AI_CONTRACT") != "1" {
		t.Skip("set EDUGRADE_PAPER_AI_CONTRACT=1 to run the installed Python contract")
	}
	question := questionRubricFixture()
	question.Solution.Steps = []SolutionStep{{Content: "  "}, {StepNo: 9, Content: " A😀BC "}, {StepNo: 12, Content: " 求解 "}}
	question.Solution.RawText = "过期原文不能送入模型"
	store := NewMemoryStore()
	store.questions[question.ID] = copyQuestionRubricFixture(t, question)
	segments := []QuestionRubricSolutionSegment{
		{Spans: []QuestionRubricSolutionSpan{{SourceID: "step-2", Start: 0, End: 3}}},
		{Spans: []QuestionRubricSolutionSpan{{SourceID: "step-2", Start: 3, End: 5}, {SourceID: "step-3", Start: 0, End: 2}}},
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		command := exec.Command("python", "-c", questionRubricPythonSegmentContract)
		command.Dir = filepath.Join("..", "..", "..", "..", "ai-services")
		command.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
		command.Stdin = &body
		output, err := command.CombinedOutput()
		if err != nil {
			t.Errorf("real Python segmented contract rejected gateway input: %v\n%s", err, output)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(output)
	}))
	defer upstream.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(upstream.URL, strings.Repeat("x", 32), 10*time.Second)}
	result, err := suggestQuestionMathRubricDraft(context.Background(), store, service, question.TenantID, question.ExamID, question.ID,
		QuestionMathRubricDraftInput{ExpectedQuestion: &question, SolutionSegments: segments})
	if err != nil || calls != 1 || len(result.SuggestedRubricCandidates) != 1 {
		t.Fatalf("segmented inference: %#v calls=%d err=%v", result, calls, err)
	}
	if !reflect.DeepEqual(result.SuggestedRubricCandidates[0].Points[0].EvidenceStepIDs, []string{"step-2"}) {
		t.Fatal("response did not retain the new segment identity")
	}
	if !reflect.DeepEqual(store.questions[question.ID].Solution, question.Solution) || store.questions[question.ID].Rubric != nil {
		t.Fatal("inference persisted a partition or rubric")
	}
}

const questionRubricPythonSegmentContract = `import json,sys
from contextlib import nullcontext
from grading_agent.math_rubric_draft import MathRubricDraftGenerator
class Model:
    def session(self, request_id): return nullcontext()
    def request_structured(self, request_id, messages, schema, name):
        supplied=json.loads(messages[-1]["content"])
        assert supplied["solution_context"]=="A😀BC\n\n求解", supplied
        assert supplied["solution_steps"]==[{"id":"step-1","content":"A😀"},{"id":"step-2","content":"BC\n\n求解"}], supplied
        assert schema["properties"]["points"]["items"]["properties"]["evidence_step_ids"]["items"]["enum"]==["step-1","step-2"]
        return {"points":[{"description":"正确求解","suggested_score":4,"evidence_step_ids":["step-2"],"review_note":""}]}
request=json.load(sys.stdin)
result=MathRubricDraftGenerator(Model()).generate(request["request_id"],request["question_candidate"],request["answer_candidate"],request["solution_candidate"],question_reference=request["question_reference"])
print(json.dumps(result,ensure_ascii=False))
`
