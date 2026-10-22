package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func testObjectiveGridInput(t *testing.T) ObjectiveGridInput {
	t.Helper()
	page := testPageAnalysisInput(t)
	return ObjectiveGridInput{ExpectedRevision: page.ExpectedRevision, ExpectedSourceSHA256: page.ExpectedSourceSHA256,
		PageNo: page.PageNo, Width: page.Width, Height: page.Height, MediaType: page.MediaType,
		DataBase64: page.DataBase64, SHA256: page.SHA256, BlockBBox: TemplateAnalysisBBox{X: .1, Y: .1, Width: .8, Height: .8},
		OptionCount: 4, OptionOrientation: "horizontal"}
}

func testObjectiveGridResult(input ObjectiveGridInput) ObjectiveGridAnalysis {
	options := make([]ObjectiveGridOption, input.OptionCount)
	for index := range options {
		options[index] = ObjectiveGridOption{BBox: TemplateAnalysisBBox{X: .16 + float64(index)*.6/float64(input.OptionCount), Y: .25, Width: .03, Height: .06}}
	}
	return ObjectiveGridAnalysis{RequestID: "request-grid", SchemaVersion: objectiveGridSchema, Status: "review_required",
		CoordinateSpace: "source_display_normalized", PageNo: input.PageNo, SourceSHA256: input.SHA256, BlockBBox: input.BlockBBox,
		Groups: []ObjectiveGridGroup{{GroupID: "grid-1", BBox: TemplateAnalysisBBox{X: .12, Y: .2, Width: .7, Height: .2},
			Options: options, ReasonCodes: []string{"GEOMETRY_ONLY_REQUIRES_REVIEW"}}}, ReasonCodes: []string{},
		Versions: map[string]string{"algorithm": "opencv-objective-grid-v1"}}
}

func objectiveGridHandlerFixture(t *testing.T, input ObjectiveGridInput, upstream http.HandlerFunc) (*Handler, *MemoryStore, *pageAnalysisExamReader) {
	t.Helper()
	store := NewMemoryStore()
	store.templates["template"] = AnswerSheetTemplate{ID: "template", TenantID: "tenant", ExamID: "exam", ExamPaperID: "paper", Revision: 1, Status: "draft", PageCount: 1, ContentHash: "layout"}
	store.papers["paper"] = Paper{ID: "paper", TenantID: "tenant", ExamID: "exam", Purpose: PaperPurposeAnswerSheet, File: FileAssetInput{HashSHA256: input.ExpectedSourceSHA256}}
	store.questions["q17"] = Question{ID: "q17", TenantID: "tenant", ExamID: "exam", QuestionNo: "17", QuestionType: "single_choice"}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	service := NewDocumentImportService(store, nil, nil, server.URL, strings.Repeat("k", 32), time.Second)
	service.WithTemplateVisionResolver(func(context.Context, string) (*DocumentModelConfig, error) {
		t.Error("objective geometry must not resolve a multimodal model")
		return nil, nil
	})
	exams := &pageAnalysisExamReader{status: "draft"}
	return NewHandler(store, nil).WithDocumentImport(service).WithQuestionRubricExams(exams), store, exams
}

func callObjectiveGridHandler(handler *Handler, input ObjectiveGridInput, scope auth.AccessScope, raw []byte) *httptest.ResponseRecorder {
	if raw == nil {
		raw, _ = json.Marshal(input)
	}
	request := httptest.NewRequest(http.MethodPost, "/template/analyze-objective-grid", bytes.NewReader(raw))
	request.SetPathValue("id", "template")
	request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: "tenant"}), scope))
	response := httptest.NewRecorder()
	handler.AnalyzeObjectiveGrid(response, request)
	return response
}

func TestObjectiveGridUsesCurrentExamSnapshotWithoutModelOrPersistence(t *testing.T) {
	for _, scenario := range []string{"success", "restricted exam", "scope mismatch", "stale revision", "locked", "frozen", "source changed",
		"changed during call", "content changed during call", "frozen during call", "questions changed during call"} {
		t.Run(scenario, func(t *testing.T) {
			input := testObjectiveGridInput(t)
			var calls atomic.Int32
			var store *MemoryStore
			var exams *pageAnalysisExamReader
			handler, fixtureStore, fixtureExams := objectiveGridHandlerFixture(t, input, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/answer-sheet/objective-grid" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("wrong objective grid internal path or authentication")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if _, exists := payload["managed_model"]; exists || len(payload) != 6 {
					t.Error("objective geometry included model configuration or unexpected fields")
				}
				switch scenario {
				case "changed during call", "content changed during call":
					store.mu.Lock()
					item := store.templates["template"]
					if scenario == "changed during call" {
						item.Revision++
					} else {
						item.ContentHash = "changed-layout"
					}
					store.templates[item.ID] = item
					store.mu.Unlock()
				case "frozen during call":
					exams.status = "collecting"
				case "questions changed during call":
					store.mu.Lock()
					item := store.questions["q17"]
					item.QuestionNo = "18"
					store.questions[item.ID] = item
					store.mu.Unlock()
				}
				out := testObjectiveGridResult(input)
				_ = json.Unmarshal(payload["request_id"], &out.RequestID)
				_ = json.NewEncoder(w).Encode(out)
			})
			store, exams = fixtureStore, fixtureExams
			scope := auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}
			want := http.StatusConflict
			wantCalls := int32(0)
			switch scenario {
			case "success":
				want, wantCalls = http.StatusOK, 1
			case "restricted exam":
				scope.TenantWide, want = false, http.StatusNotFound
			case "scope mismatch":
				scope.ActorID, want = "other", http.StatusForbidden
			case "stale revision":
				input.ExpectedRevision++
			case "locked":
				item := store.templates["template"]
				item.Status = "locked"
				store.templates[item.ID] = item
			case "frozen":
				exams.status = "collecting"
			case "source changed":
				input.ExpectedSourceSHA256 = strings.Repeat("b", 64)
			default:
				wantCalls = 1
			}
			response := callObjectiveGridHandler(handler, input, scope, nil)
			if response.Code != want || calls.Load() != wantCalls {
				t.Fatalf("got status=%d calls=%d body=%s, want status=%d calls=%d", response.Code, calls.Load(), response.Body.String(), want, wantCalls)
			}
			if scenario == "success" {
				var result ObjectiveGridResponse
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.TemplateID != "template" ||
					result.SourceSHA256 != input.ExpectedSourceSHA256 || result.Analysis.SourceSHA256 != input.SHA256 || len(result.Analysis.Groups) != 1 {
					t.Fatalf("invalid source/image hash envelope: %s", response.Body.String())
				}
			}
			if len(store.bindings) != 0 || len(store.templates["template"].Layout.Pages) != 0 {
				t.Fatal("geometry analysis persisted or bound a template")
			}
		})
	}
}

func TestObjectiveGridRejectsInvalidPublicInputBeforeInternalCall(t *testing.T) {
	for _, scenario := range []string{"option count", "orientation", "block", "checksum", "model config", "oversized body"} {
		t.Run(scenario, func(t *testing.T) {
			input := testObjectiveGridInput(t)
			var calls atomic.Int32
			handler, _, _ := objectiveGridHandlerFixture(t, input, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			var raw []byte
			switch scenario {
			case "option count":
				input.OptionCount = 1
			case "orientation":
				input.OptionOrientation = "guess"
			case "block":
				input.BlockBBox.Width = 1
			case "checksum":
				input.SHA256 = strings.Repeat("b", 64)
			case "model config":
				raw, _ = json.Marshal(input)
				raw = append(raw[:len(raw)-1], []byte(`,"managed_model":{"api_key":"private-key"}}`)...)
			case "oversized body":
				raw = []byte(`{"data_base64":"` + strings.Repeat("x", (2<<20)+1) + `"}`)
			}
			response := callObjectiveGridHandler(handler, input, auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}, raw)
			if response.Code != http.StatusBadRequest || calls.Load() != 0 || strings.Contains(response.Body.String(), "private-key") {
				t.Fatalf("invalid input reached analysis or leaked contents: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
			}
		})
	}
}

func TestObjectiveGridClientRejectsUnsafeOrUnrelatedResponses(t *testing.T) {
	for _, scenario := range []string{"http error", "invalid json", "oversized", "unknown field", "another request", "trailing json"} {
		t.Run(scenario, func(t *testing.T) {
			input := testObjectiveGridInput(t)
			handler, _, _ := objectiveGridHandlerFixture(t, input, func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					RequestID string `json:"request_id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				out := testObjectiveGridResult(input)
				out.RequestID = payload.RequestID
				switch scenario {
				case "http error":
					http.Error(w, "private-upstream-details", http.StatusInternalServerError)
				case "invalid json":
					_, _ = w.Write([]byte("private-upstream-invalid-json"))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
				case "unknown field":
					raw, _ := json.Marshal(out)
					_, _ = w.Write(append(raw[:len(raw)-1], []byte(`,"question_id":"private-upstream-invented"}`)...))
				case "another request":
					out.RequestID = "another-request"
					_ = json.NewEncoder(w).Encode(out)
				case "trailing json":
					_ = json.NewEncoder(w).Encode(out)
					_, _ = w.Write([]byte(`{}`))
				}
			})
			response := callObjectiveGridHandler(handler, input, auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}, nil)
			if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "private-upstream") {
				t.Fatalf("unsafe upstream response accepted or exposed: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestObjectiveGridClientStopsWaitingWhenCallerCancels(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(arrived)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	service := NewDocumentImportService(NewMemoryStore(), nil, nil, upstream.URL, strings.Repeat("k", 32), 10*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input := testObjectiveGridInput(t)
	finished := make(chan error, 1)
	go func() {
		_, err := service.analyzeObjectiveGrid(ctx, input)
		finished <- err
	}()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("objective grid request did not reach the internal service")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled grid request returned a result")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("objective grid request kept waiting after cancellation")
	}
}

func TestObjectiveGridResultValidatesGeometryAndProtocolLimits(t *testing.T) {
	input := testObjectiveGridInput(t)
	for _, scenario := range []string{"schema", "page", "hash", "publish", "coordinate space", "block", "group outside block", "option outside group", "count", "duplicate group", "duplicate option", "nil groups", "reason text", "group limit", "option limit"} {
		t.Run(scenario, func(t *testing.T) {
			in := input
			out := testObjectiveGridResult(in)
			switch scenario {
			case "schema":
				out.SchemaVersion = "invented"
			case "page":
				out.PageNo++
			case "hash":
				out.SourceSHA256 = strings.Repeat("b", 64)
			case "publish":
				out.Status = "approved"
			case "coordinate space":
				out.CoordinateSpace = "crop_relative"
			case "block":
				out.BlockBBox.X += .01
			case "group outside block":
				out.Groups[0].BBox.X = 0
			case "option outside group":
				out.Groups[0].Options[0].BBox.X = .05
			case "count":
				out.Groups[0].Options = out.Groups[0].Options[:2]
			case "duplicate group":
				out.Groups = append(out.Groups, out.Groups[0])
			case "duplicate option":
				out.Groups[0].Options[1] = out.Groups[0].Options[0]
			case "nil groups":
				out.Groups = nil
			case "reason text":
				out.ReasonCodes = []string{"private upstream content"}
			case "group limit", "option limit":
				count := 501
				if scenario == "option limit" {
					in.OptionCount, count = 12, 167
					out = testObjectiveGridResult(in)
				}
				group := out.Groups[0]
				out.Groups = make([]ObjectiveGridGroup, count)
				for index := range out.Groups {
					out.Groups[index] = group
					out.Groups[index].GroupID = fmt.Sprintf("grid-%d", index)
				}
			}
			if validateObjectiveGridResult(out, in) == nil {
				t.Fatal("invalid geometry or response protocol was accepted")
			}
		})
	}
	valid := testObjectiveGridResult(input)
	if err := validateObjectiveGridResult(valid, input); err != nil {
		t.Fatal(err)
	}
}
