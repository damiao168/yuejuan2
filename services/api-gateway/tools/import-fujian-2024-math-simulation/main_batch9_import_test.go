package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func batch9DatasetFixture(t *testing.T) (config, dataset) {
	t.Helper()
	dir := t.TempDir()
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"page1.png", "page2.png", "blank.pdf", "questions.pdf", "answers.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), content.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	definitions, regions := []questionDef{}, map[string]region{}
	for number := 1; number <= 19; number++ {
		question := "Q" + strconv.Itoa(number)
		definitions = append(definitions, questionDef{QuestionNo: question, QuestionType: "single_choice", Score: 4, Stem: "synthetic", StandardAnswer: "A"})
		regions[question] = region{X: .1, Y: .2, Width: .3, Height: .4}
	}
	return config{datasetDir: dir, graderUser: "teacher", aiLimit: 3}, dataset{
		Manifest:  manifest{Files: map[string]any{"blank_answer_sheet": "blank.pdf", "official_questions": "questions.pdf", "official_answers": "answers.pdf"}},
		Questions: definitions,
		Candidates: []candidateDef{
			{CandidateNo: "C001", PageFiles: []string{"page1.png", "page2.png"}, Synthetic: true},
			{CandidateNo: "C001", PageFiles: []string{"page1.png", "page2.png"}, Synthetic: true},
		},
		Regions: map[string]map[string]region{"C001": regions},
	}
}

type batch9HTTPEvent struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

type batch9ImportHTTP struct {
	t             *testing.T
	mode          string
	questions     []questionDef
	events        []batch9HTTPEvent
	sequence      *[]string
	students      int
	questionCount int
	assetCount    int
	pageCount     int
}

func (fixture *batch9ImportHTTP) roundTrip(request *http.Request) (*http.Response, error) {
	event := batch9HTTPEvent{Method: request.Method, Path: request.URL.Path}
	if strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/") {
		values, name, content := readBatch9Multipart(fixture.t, request)
		event.Body = map[string]any{"fields": values, "filename": name, "sha256": sha256Hex(content)}
	} else if request.Body != nil {
		if err := json.NewDecoder(request.Body).Decode(&event.Body); err != nil {
			fixture.t.Fatal(err)
		}
	}
	fixture.events = append(fixture.events, event)
	*fixture.sequence = append(*fixture.sequence, request.Method+":"+request.URL.Path)
	status, body := fixture.response(request)
	return batch9Response(request, status, body), nil
}

func (fixture *batch9ImportHTTP) response(request *http.Request) (int, string) {
	path := request.URL.Path
	switch {
	case path == "/api/v1/schools":
		return 201, `{"school":{"id":"school"}}`
	case path == "/api/v1/grades":
		return 201, `{"grade":{"id":"grade"}}`
	case path == "/api/v1/classes":
		return 201, `{"class":{"id":"class"}}`
	case path == "/api/v1/students":
		fixture.students++
		return 201, fmt.Sprintf(`{"student":{"id":"student-%d"}}`, fixture.students)
	case path == "/api/v1/exams":
		return 201, `{"exam":{"id":"exam"}}`
	case path == "/api/v1/files":
		fixture.assetCount++
		return 201, fmt.Sprintf(`{"file":{"id":"asset-%d","hash_sha256":"hash-%d","size_bytes":321}}`, fixture.assetCount, fixture.assetCount)
	case strings.HasSuffix(path, "/papers"):
		return 201, `{"paper":{"id":"paper"}}`
	case strings.HasSuffix(path, "/questions"):
		fixture.questionCount++
		return 201, fmt.Sprintf(`{"question":{"id":"question-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/scoring-rules"):
		return 201, fmt.Sprintf(`{"scoring_rule":{"id":"rule-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/publish"):
		if fixture.mode == "publish_failure" {
			return 500, `{"error":"injected"}`
		}
		return 200, `{}`
	case strings.HasSuffix(path, "/rubric"):
		return 201, fmt.Sprintf(`{"rubric":{"id":"rubric-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/answer-sheet-templates"):
		return 201, `{"template":{"id":"template"}}`
	case strings.HasSuffix(path, "/lock"):
		if fixture.mode == "lock_failure" {
			return 500, `{"error":"injected"}`
		}
		return 200, `{"template":{"content_hash":"template-hash"}}`
	case strings.HasSuffix(path, "/readiness/confirm"), strings.HasSuffix(path, "/start-collection"):
		return 200, `{}`
	case strings.HasSuffix(path, "/submissions"):
		return 201, `{"submission":{"id":"submission"}}`
	case strings.HasSuffix(path, "/pages"):
		fixture.pageCount++
		return 201, fmt.Sprintf(`{"page":{"id":"page-%d"}}`, fixture.pageCount)
	case strings.HasSuffix(path, "/quality-check"):
		if fixture.mode == "integrity_failure" {
			return 200, `{"result":{"valid":false,"issues":["missing_page"]}}`
		}
		return 200, `{"result":{"valid":true}}`
	case strings.HasSuffix(path, "/run-quality-check"):
		return 202, `{}`
	case request.Method == http.MethodGet && path == "/api/v1/submissions/submission":
		return 200, `{"submission":{"quality_status":"passed","revision":7}}`
	case strings.HasSuffix(path, "/status"):
		return 200, `{}`
	case strings.HasSuffix(path, "/segment-answers"):
		return fixture.segmentResponse()
	default:
		fixture.t.Fatalf("unexpected HTTP request %s %s", request.Method, path)
		return 500, `{}`
	}
}

func (fixture *batch9ImportHTTP) segmentResponse() (int, string) {
	segments := []any{}
	for _, question := range fixture.questions {
		segments = append(segments, map[string]any{"question_no": question.QuestionNo, "id": "segment-" + question.QuestionNo})
	}
	if fixture.mode == "segment_count_failure" {
		segments = segments[:len(segments)-1]
	}
	body, err := json.Marshal(map[string]any{"result": map[string]any{"segments": segments}})
	if err != nil {
		fixture.t.Fatal(err)
	}
	return 200, string(body)
}

func TestBatch9ImportStageOrderAndFailureResult(t *testing.T) {
	// 在 OCR 原生事务入口注入失败，核对此前配准与裁剪顺序，并拒绝返回半成品汇总。
	tests := []struct{ name, wantError string }{
		{"missing_region", "missing region for Q1"},
		{"publish_failure", "publish rule for Q1:"},
		{"lock_failure", "lock answer sheet template:"},
		{"integrity_failure", "collection integrity failed for C001: [missing_page]"},
		{"segment_count_failure", "C001 generated 18 segments, want 19"},
		{"ocr_boundary", "atomically seed OCR task for C001: stop before OCR"},
		{"question_order", "atomically seed OCR task for C001: stop before OCR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, data := prepareBatch9ImportFailureDataset(t, test.name)
			sequence := []string{}
			fixture := &batch9ImportHTTP{t: t, mode: test.name, questions: data.Questions, sequence: &sequence}
			script := &batch9SQLScript{
				affected: 1, importMode: true, sequence: &sequence,
				queryErr: errors.New("stop before OCR"), queryErrContains: "INSERT INTO ocr_task",
			}
			result, err := runBatch9Dataset(context.Background(), cfg, data, batch9Client(fixture.roundTrip), openBatch9DB(t, script))
			assertBatch9Error(t, err, test.wantError)
			if !reflect.DeepEqual(result, importResult{}) {
				t.Fatalf("failed import returned partial result: %#v", result)
			}
			assertBatch9ImportRosterOrder(t, fixture.events)
			assertBatch9CandidateBindings(t, fixture.events)
			if test.name == "ocr_boundary" || test.name == "question_order" {
				assertBatch9ScoringDefinitions(t, fixture.events, data.Questions)
				if script.execCount != 24 || script.queryCount != 8 || script.events[len(script.events)-1].Kind != "rollback" {
					t.Fatalf("pre-OCR SQL = %#v", script.events)
				}
			}
			logBatch9Proof(t, test.name, map[string]any{"error": batch9ErrorText(err), "result": result, "http": fixture.events, "sql": script.events, "sequence": sequence})
		})
	}
}

func prepareBatch9ImportFailureDataset(t *testing.T, name string) (config, dataset) {
	t.Helper()
	cfg, data := batch9DatasetFixture(t)
	// 故障输入必须在记录 HTTP/SQL 事件前设置；交换题序用来核对导入沿用数据集顺序。
	if name == "missing_region" {
		delete(data.Regions["C001"], "Q1")
	}
	if name == "question_order" {
		data.Questions[0], data.Questions[16] = data.Questions[16], data.Questions[0]
	}
	return cfg, data
}

func assertBatch9ImportRosterOrder(t *testing.T, events []batch9HTTPEvent) {
	t.Helper()
	if len(events) < 8 || events[0].Path != "/api/v1/schools" || events[1].Path != "/api/v1/grades" || events[2].Path != "/api/v1/classes" || events[3].Path != "/api/v1/students" || events[4].Path != "/api/v1/students" || events[5].Path != "/api/v1/exams" {
		t.Fatalf("roster order = %#v", events)
	}
}

func assertBatch9CandidateBindings(t *testing.T, events []batch9HTTPEvent) {
	t.Helper()
	for _, event := range events {
		if strings.HasSuffix(event.Path, "/submissions") {
			body := event.Body.(map[string]any)
			if body["student_id"] != "student-2" || body["candidate_no"] != "C001" || body["expected_page_count"] != float64(2) {
				t.Fatalf("duplicate candidate binding = %#v", body)
			}
		}
		if strings.HasSuffix(event.Path, "/status") {
			if !reflect.DeepEqual(event.Body, map[string]any{"status": "ready_for_ocr", "expected_revision": float64(7)}) {
				t.Fatalf("ready transition = %#v", event.Body)
			}
		}
	}
}

func assertBatch9ScoringDefinitions(t *testing.T, events []batch9HTTPEvent, questions []questionDef) {
	t.Helper()
	rules, rubrics, ordinal := 0, 0, 0
	for _, event := range events {
		if strings.HasSuffix(event.Path, "/questions") {
			assertBatch9QuestionOrder(t, event, questions[ordinal], ordinal)
			ordinal++
		}
		if strings.HasSuffix(event.Path, "/scoring-rules") {
			rules++
			assertBatch9ObjectiveRuleRoute(t, event, rules)
		}
		if strings.HasSuffix(event.Path, "/rubric") {
			rubrics++
			assertBatch9SubjectiveRubricRoute(t, event, rubrics)
		}
	}
	if rules != 16 || rubrics != 3 || ordinal != 19 {
		t.Fatalf("definitions = %d/%d/%d", rules, rubrics, ordinal)
	}
}

func assertBatch9QuestionOrder(t *testing.T, event batch9HTTPEvent, question questionDef, ordinal int) {
	t.Helper()
	body := event.Body.(map[string]any)
	if body["question_no"] != question.QuestionNo || body["sort_order"] != float64(ordinal+1) {
		t.Fatalf("question order = %#v", body)
	}
}

func assertBatch9ObjectiveRuleRoute(t *testing.T, event batch9HTTPEvent, rules int) {
	t.Helper()
	if rules > 16 || event.Path != fmt.Sprintf("/api/v1/questions/question-%d/scoring-rules", rules) {
		t.Fatalf("rule route = %s", event.Path)
	}
}

func assertBatch9SubjectiveRubricRoute(t *testing.T, event batch9HTTPEvent, rubrics int) {
	t.Helper()
	if event.Path != fmt.Sprintf("/api/v1/questions/question-%d/rubric", 16+rubrics) {
		t.Fatalf("rubric route = %s", event.Path)
	}
}
