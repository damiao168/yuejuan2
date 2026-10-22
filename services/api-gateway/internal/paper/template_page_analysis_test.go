package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

type pageAnalysisExamReader struct{ status string }

func (reader *pageAnalysisExamReader) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	if scope.TenantID != "tenant" || !scope.TenantWide || id != "exam" {
		return exam.Exam{}, exam.ErrNotFound
	}
	return exam.Exam{ID: "exam", TenantID: "tenant", Status: reader.status}, nil
}

func testPageAnalysisInput(t *testing.T) TemplatePageAnalysisInput {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 100, 140))); err != nil {
		t.Fatal(err)
	}
	return TemplatePageAnalysisInput{
		ExpectedRevision: 1, ExpectedSourceSHA256: strings.Repeat("a", 64), PageNo: 1, Width: 100, Height: 140,
		MediaType: "image/png", DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes()), SHA256: fmt.Sprintf("%x", sha256.Sum256(buffer.Bytes())),
		Candidates: []TemplateAnalysisGeometry{}, WritingState: "unknown",
	}
}

func testPageAnalysisResult(input TemplatePageAnalysisInput) TemplatePageAnalysis {
	label := "17"
	box := TemplateAnalysisBBox{X: .1, Y: .1, Width: .8, Height: .5}
	return TemplatePageAnalysis{
		RequestID: "analysis-request", SchemaVersion: templatePageAnalysisSchema, PageNo: 1,
		Status: "review_required", SemanticStatus: "completed", SourceSHA256: input.SHA256, CoordinateSpace: "source_display_normalized",
		Candidates: []TemplateAnalyzedRegion{{PageNo: 1, CandidateID: "cv-1", PrintedQuestionLabel: &label, Role: "answer", SourceBBox: box,
			ProposedBBox: box, AssociationStatus: "associated", EvidenceRefs: []string{"cv-1"}, ReasonCodes: []string{}, SemanticCheck: "supported"}},
		ModelIdentity: map[string]string{}, Versions: map[string]string{"schema": templatePageAnalysisSchema},
		ModelUsage: json.RawMessage(`{}`), SemanticError: json.RawMessage(`null`), ReasonCodes: []string{},
	}
}

func TestTemplatePageAnalysisRejectsIncorrectImageAndGeometry(t *testing.T) {
	valid := testPageAnalysisInput(t)
	if err := validateTemplatePageAnalysisInput(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*TemplatePageAnalysisInput)
	}{
		{"checksum", func(in *TemplatePageAnalysisInput) { in.SHA256 = strings.Repeat("b", 64) }},
		{"wrong dimensions", func(in *TemplatePageAnalysisInput) { in.Width++ }},
		{"wrong media", func(in *TemplatePageAnalysisInput) { in.MediaType = "image/jpeg" }},
		{"source hash", func(in *TemplatePageAnalysisInput) { in.ExpectedSourceSHA256 = "not-a-digest" }},
		{"out of bounds", func(in *TemplatePageAnalysisInput) {
			in.Candidates = []TemplateAnalysisGeometry{{CandidateID: "x", BBox: TemplateAnalysisBBox{X: .9, Width: .2, Height: .2}}}
		}},
		{"duplicate candidate", func(in *TemplatePageAnalysisInput) {
			region := TemplateAnalysisGeometry{CandidateID: "x", BBox: TemplateAnalysisBBox{Width: .2, Height: .2}}
			in.Candidates = []TemplateAnalysisGeometry{region, region}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.edit(&input)
			if validateTemplatePageAnalysisInput(input) == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestTemplatePageAnalysisResultCannotInventQuestionOrPublish(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name string
		edit func(*TemplatePageAnalysis)
	}{
		{"automatic publish", func(out *TemplatePageAnalysis) { out.Status = "approved" }},
		{"another page", func(out *TemplatePageAnalysis) { out.Candidates[0].PageNo = 2 }},
		{"another question", func(out *TemplatePageAnalysis) { text := "99"; out.Candidates[0].PrintedQuestionLabel = &text }},
		{"unassociated student value", func(out *TemplatePageAnalysis) {
			out.Candidates[0].Role, out.Candidates[0].AssociationStatus = "uncertain", "unassociated"
			out.Candidates[0].PrintedQuestionLabel = new("学生填写内容")
		}},
		{"failed semantics assigned", func(out *TemplatePageAnalysis) { out.SemanticStatus = "failed" }},
		{"duplicate question", func(out *TemplatePageAnalysis) {
			extra := out.Candidates[0]
			extra.CandidateID = "cv-2"
			out.Candidates = append(out.Candidates, extra)
		}},
		{"identity assigned", func(out *TemplatePageAnalysis) { out.Candidates[0].Role = "identity" }},
		{"failed semantics identity", func(out *TemplatePageAnalysis) {
			out.SemanticStatus = "failed"
			out.Candidates[0].Role, out.Candidates[0].PrintedQuestionLabel, out.Candidates[0].AssociationStatus = "identity", nil, "unassociated"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testPageAnalysisResult(input)
			test.edit(&out)
			if validateTemplatePageAnalysisResult(out, input, []string{"17"}) == nil {
				t.Fatal("unsafe association accepted")
			}
		})
	}
}

func TestTemplatePageAnalysisLocalCandidatesCannotClaimVerifiedOrAssociated(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name, origin, semantic, association string
		wantValid                           bool
	}{
		{"legacy model", "", "supported", "associated", true},
		{"explicit model", "model", "supported", "associated", true},
		{"local candidate", "local_detector", "needs_review", "unassociated", true},
		{"local verified", "local_detector", "supported", "unassociated", false},
		{"local associated", "local_detector", "needs_review", "associated", false},
		{"local ambiguous", "local_detector", "needs_review", "ambiguous", false},
		{"unknown origin", "human", "needs_review", "unassociated", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testPageAnalysisResult(input)
			out.Candidates[0].Origin, out.Candidates[0].SemanticCheck, out.Candidates[0].AssociationStatus = test.origin, test.semantic, test.association
			if got := validateTemplatePageAnalysisResult(out, input, []string{"17"}) == nil; got != test.wantValid {
				t.Fatalf("candidate valid=%v, want %v", got, test.wantValid)
			}
		})
	}
}

func testVerifiedLocalFillResult(input TemplatePageAnalysisInput) TemplatePageAnalysis {
	out := testPageAnalysisResult(input)
	out.Candidates[0].Origin = "local_detector"
	out.Candidates[0].ReasonCodes = []string{"LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED", "LOCAL_FILL_UNDERLINE_VERIFIED", "LOCAL_FILL_LAYOUT_VERIFIED"}
	return out
}

func TestTemplatePageAnalysisVerifiedLocalFillRequiresCompleteProofAndOfficialType(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name       string
		fillLabels []string
		edit       func(*TemplatePageAnalysis)
		wantValid  bool
	}{
		{"complete local fill", []string{"17"}, func(*TemplatePageAnalysis) {}, true},
		{"no official type context", nil, func(*TemplatePageAnalysis) {}, false},
		{"different official fill question", []string{"12"}, func(*TemplatePageAnalysis) {}, false},
		{"layout conclusion alone", []string{"17"}, func(out *TemplatePageAnalysis) {
			out.Candidates[0].ReasonCodes = []string{"LOCAL_FILL_LAYOUT_VERIFIED"}
		}, false},
		{"no header proof", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].ReasonCodes = out.Candidates[0].ReasonCodes[1:] }, false},
		{"no printed marker proof", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].ReasonCodes[1] = "OTHER_PROOF" }, false},
		{"no underline proof", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].ReasonCodes[2] = "OTHER_PROOF" }, false},
		{"no layout conclusion", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].ReasonCodes = out.Candidates[0].ReasonCodes[:3] }, false},
		{"no question", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].PrintedQuestionLabel = nil }, false},
		{"wrong role", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].Role = "identity" }, false},
		{"identity metadata", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].IdentityLabel = new("姓名") }, false},
		{"objective metadata", []string{"17"}, func(out *TemplatePageAnalysis) {
			out.Candidates[0].ObjectiveItems = []TemplateAnalyzedObjectiveItem{{PrintedQuestionLabel: "17"}}
		}, false},
		{"supported but unassociated", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].AssociationStatus = "unassociated" }, false},
		{"review but associated", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].SemanticCheck = "needs_review" }, false},
		{"failed semantic run", []string{"17"}, func(out *TemplatePageAnalysis) { out.SemanticStatus = "failed" }, false},
		{"invalid geometry", []string{"17"}, func(out *TemplatePageAnalysis) { out.Candidates[0].ProposedBBox.Width = 2 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testVerifiedLocalFillResult(input)
			test.edit(&out)
			if got := validateTemplatePageAnalysisResult(out, input, []string{"17"}, test.fillLabels) == nil; got != test.wantValid {
				t.Fatalf("local fill valid=%v, want %v", got, test.wantValid)
			}
		})
	}
}

func TestTemplatePageAnalysisVerifiedLocalFillStillPassesSemanticConflictDefense(t *testing.T) {
	input := testPageAnalysisInput(t)
	labels := templateAnalysisQuestionLabels{All: []string{"17"}, FillBlank: []string{"17"}}
	for _, conflict := range []string{"REGION_CONFLICT", "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MODEL_OBS_MULTIPLE_QUESTION_LABELS", "duplicate question"} {
		t.Run(conflict, func(t *testing.T) {
			out := testVerifiedLocalFillResult(input)
			if conflict == "duplicate question" {
				extra := out.Candidates[0]
				extra.CandidateID = "local-fill-duplicate"
				out.Candidates = append(out.Candidates, extra)
			} else {
				out.Candidates[0].ReasonCodes = append(out.Candidates[0].ReasonCodes, conflict)
			}
			// 来源许可只开放严格布局结论；已有禁区、重复题号等反证仍由同一语义防线撤回关联。
			for _, candidate := range out.Candidates {
				if err := validateTemplateCandidateOrigin(candidate, labels.FillBlank); err != nil {
					t.Fatal(err)
				}
			}
			defendTemplatePageSemantics(&out, labels)
			for _, candidate := range out.Candidates {
				if candidate.Role != "uncertain" || candidate.SemanticCheck != "needs_review" || candidate.AssociationStatus != "unassociated" {
					t.Fatalf("conflicted local fill retained automatic association: %+v", candidate)
				}
			}
			if err := validateTemplatePageAnalysisResult(out, input, labels.All, labels.FillBlank); err != nil {
				t.Fatalf("downgraded conflict must remain available for correction: %v", err)
			}
		})
	}
}

func TestTemplatePageAnalysisIdentityUsesOnlyStaticFieldCategories(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name          string
		label         *string
		questionLabel *string
		role          string
		wantValid     bool
	}{
		{"legacy generic identity", nil, nil, "identity", true},
		{"name field", new("姓名"), nil, "identity", true},
		{"student number field", new("学号"), nil, "identity", true},
		{"exam number field", new("准考证号"), nil, "identity", true},
		{"barcode field", new("条码"), nil, "identity", true},
		{"student value is not a field label", new("测试学生甲"), nil, "identity", false},
		{"student number is not a field label", new("2026001234"), nil, "identity", false},
		{"identity cannot carry a student value in question label", new("姓名"), new("测试学生甲"), "identity", false},
		{"answer cannot carry identity metadata", new("学号"), nil, "answer", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testPageAnalysisResult(input)
			candidate := &out.Candidates[0]
			candidate.Role, candidate.IdentityLabel, candidate.PrintedQuestionLabel = test.role, test.label, test.questionLabel
			candidate.AssociationStatus = "unassociated"
			if gotValid := validateTemplatePageAnalysisResult(out, input, []string{"17"}) == nil; gotValid != test.wantValid {
				t.Fatalf("identity candidate valid=%v, want %v", gotValid, test.wantValid)
			}
		})
	}
}

func TestTemplatePageAnalysisObjectiveGroupCannotBindOneQuestion(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name      string
		edit      func(*TemplatePageAnalysis)
		wantValid bool
	}{
		{"whole objective area", func(*TemplatePageAnalysis) {}, true},
		{"single question label", func(out *TemplatePageAnalysis) { out.Candidates[0].PrintedQuestionLabel = new("17") }, false},
		{"single question association", func(out *TemplatePageAnalysis) { out.Candidates[0].AssociationStatus = "associated" }, false},
		{"ambiguous association", func(out *TemplatePageAnalysis) { out.Candidates[0].AssociationStatus = "ambiguous" }, false},
		{"identity metadata", func(out *TemplatePageAnalysis) { out.Candidates[0].IdentityLabel = new("姓名") }, false},
		{"failed semantics", func(out *TemplatePageAnalysis) { out.SemanticStatus = "failed" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testPageAnalysisResult(input)
			out.Candidates[0].Role, out.Candidates[0].PrintedQuestionLabel, out.Candidates[0].AssociationStatus = "objective_group", nil, "unassociated"
			test.edit(&out)
			if got := validateTemplatePageAnalysisResult(out, input, []string{"17"}) == nil; got != test.wantValid {
				t.Fatalf("objective group valid=%v, want %v", got, test.wantValid)
			}
		})
	}
}

func TestTemplatePageAnalysisObjectiveLabelsUseOfficialQuestionTypes(t *testing.T) {
	input := testPageAnalysisInput(t)
	store := NewMemoryStore()
	store.templates["template"] = AnswerSheetTemplate{ID: "template", TenantID: "tenant", ExamID: "exam", ExamPaperID: "paper", Revision: 1, Status: "draft", PageCount: 1}
	store.papers["paper"] = Paper{ID: "paper", TenantID: "tenant", ExamID: "exam", Purpose: PaperPurposeAnswerSheet, File: FileAssetInput{HashSHA256: input.ExpectedSourceSHA256}}
	wantObjective := map[string]bool{"1": true, "2": true, "3": true}
	for index, questionType := range []string{"single_choice", "multiple_choice", "true_false", "fill_blank", "numeric", "formula", "short_answer", "calculation"} {
		id := fmt.Sprint(index + 1)
		store.questions[id] = Question{ID: id, TenantID: "tenant", ExamID: "exam", QuestionNo: id, QuestionType: questionType}
	}
	handler := NewHandler(store, nil).WithQuestionRubricExams(&pageAnalysisExamReader{status: "draft"})
	scope := auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}
	_, _, labels, _, err := handler.templateAnalysisSnapshot(t.Context(), scope, "template", input)
	if err != nil || len(labels.All) != 8 || len(labels.Objective) != 3 || len(labels.FillBlank) != 1 || labels.FillBlank[0] != "4" {
		t.Fatalf("official labels missing: labels=%+v err=%v", labels, err)
	}
	for _, label := range labels.Objective {
		if !wantObjective[label] {
			t.Fatalf("non-choice question included in objective group: %q", label)
		}
		delete(wantObjective, label)
	}
	if len(wantObjective) != 0 {
		t.Fatalf("official objective questions omitted: %v", wantObjective)
	}
}

// 模型请求期间另一教师可以改框或冻结考试；即使 HTTP 返回成功，也不能交付过期建议。
func TestTemplatePageAnalysisChecksScopeAndSnapshotAroundModelCall(t *testing.T) {
	for _, scenario := range []string{"success", "identity regions", "objective group", "local fill candidate", "local verified", "local associated", "local layout verified", "local layout conflict", "unknown origin", "geometry fields", "browser frame metadata", "browser diagnostic metadata", "browser fill labels", "foreign scope", "stale revision", "source changed", "changed during call", "question type changed during call", "frozen during call", "invalid result"} {
		t.Run(scenario, func(t *testing.T) {
			input := testPageAnalysisInput(t)
			store := NewMemoryStore()
			store.templates["template"] = AnswerSheetTemplate{ID: "template", TenantID: "tenant", ExamID: "exam", ExamPaperID: "paper", Revision: 1, Status: "draft", PageCount: 1, ContentHash: "layout"}
			store.papers["paper"] = Paper{ID: "paper", TenantID: "tenant", ExamID: "exam", Purpose: PaperPurposeAnswerSheet, File: FileAssetInput{HashSHA256: input.ExpectedSourceSHA256}}
			store.questions["q17"] = Question{ID: "q17", TenantID: "tenant", ExamID: "exam", QuestionNo: "17", QuestionType: "short_answer"}
			if scenario == "objective group" {
				question := store.questions["q17"]
				question.QuestionType = "multiple_choice"
				store.questions[question.ID] = question
			}
			if strings.HasPrefix(scenario, "local ") {
				question := store.questions["q17"]
				question.QuestionType = "fill_blank"
				store.questions[question.ID] = question
			}
			exams := &pageAnalysisExamReader{status: "draft"}
			officialQuestions, _ := store.ListQuestions(t.Context(), "tenant", "exam")
			wantQuestionHash := stableContentHash(officialQuestions)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/answer-sheet/analyze" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("wrong internal authentication or operation")
				}
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				var labels []string
				_ = json.Unmarshal(request["question_labels"], &labels)
				if len(labels) != 1 || labels[0] != "17" {
					t.Error("question labels did not come from current exam")
				}
				var objectiveLabels []string
				if err := json.Unmarshal(request["objective_question_labels"], &objectiveLabels); err != nil || objectiveLabels == nil {
					t.Error("objective question labels must be an array from the official snapshot")
				}
				if scenario == "objective group" {
					if len(objectiveLabels) != 1 || objectiveLabels[0] != "17" {
						t.Error("official choice labels were not sent to the model analysis service")
					}
				} else if len(objectiveLabels) != 0 {
					t.Error("subjective question sent as an objective label")
				}
				var fillLabels []string
				if err := json.Unmarshal(request["fill_blank_question_labels"], &fillLabels); err != nil || fillLabels == nil {
					t.Error("fill question labels must be an array from the official snapshot")
				}
				if strings.HasPrefix(scenario, "local ") {
					if len(fillLabels) != 1 || fillLabels[0] != "17" {
						t.Error("official fill labels were not sent to the local detector")
					}
				} else if len(fillLabels) != 0 {
					t.Error("non-fill question sent to the local fill detector")
				}
				var diagnostic templateAnalysisDiagnosticContext
				if err := json.Unmarshal(request["diagnostic_context"], &diagnostic); err != nil || diagnostic.TemplateID != "template" || diagnostic.TemplateRevision != 1 || diagnostic.SourcePaperID != "paper" || diagnostic.SourcePDFSHA256 != input.ExpectedSourceSHA256 || diagnostic.QuestionSnapshotSHA256 != wantQuestionHash {
					t.Errorf("diagnostic context must describe the authenticated snapshot: %+v %v", diagnostic, err)
				}
				if scenario == "changed during call" {
					store.mu.Lock()
					item := store.templates["template"]
					item.Revision++
					store.templates[item.ID] = item
					store.mu.Unlock()
				}
				if scenario == "frozen during call" {
					exams.status = "collecting"
				}
				if scenario == "question type changed during call" {
					store.mu.Lock()
					question := store.questions["q17"]
					question.QuestionType = "single_choice"
					store.questions[question.ID] = question
					store.mu.Unlock()
				}
				out := testPageAnalysisResult(input)
				_ = json.Unmarshal(request["request_id"], &out.RequestID)
				if scenario == "invalid result" {
					out.Candidates[0].ProposedBBox.Width = 2
				}
				if scenario == "identity regions" {
					box := TemplateAnalysisBBox{X: .05, Y: .02, Width: .5, Height: .05}
					out.Candidates = append(out.Candidates, TemplateAnalyzedRegion{PageNo: 1, CandidateID: "identity-number", Role: "identity",
						IdentityLabel: new("准考证号"), SourceBBox: box, ProposedBBox: box, AssociationStatus: "unassociated",
						EvidenceRefs: []string{"page-header"}, ReasonCodes: []string{"IDENTITY_LAYOUT_REQUIRES_REVIEW"}, SemanticCheck: "supported"})
				}
				if scenario == "objective group" {
					out.Candidates[0].Role, out.Candidates[0].PrintedQuestionLabel, out.Candidates[0].AssociationStatus = "objective_group", nil, "unassociated"
				}
				if strings.HasPrefix(scenario, "local ") {
					out.Candidates[0].Origin, out.Candidates[0].SemanticCheck, out.Candidates[0].AssociationStatus = "local_detector", "needs_review", "unassociated"
					if scenario == "local verified" {
						out.Candidates[0].SemanticCheck = "supported"
					}
					if scenario == "local associated" {
						out.Candidates[0].AssociationStatus = "associated"
					}
					if strings.HasPrefix(scenario, "local layout ") {
						verified := testVerifiedLocalFillResult(input)
						out.Candidates = verified.Candidates
						if scenario == "local layout conflict" {
							out.Candidates[0].ReasonCodes = append(out.Candidates[0].ReasonCodes, "REGION_CONFLICT")
						}
					}
				}
				if scenario == "unknown origin" {
					out.Candidates[0].Origin = "human"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer upstream.Close()
			service := NewDocumentImportService(store, nil, nil, upstream.URL, strings.Repeat("k", 32), time.Second)
			service.WithTemplateVisionResolver(func(context.Context, string) (*DocumentModelConfig, error) {
				return &DocumentModelConfig{AdapterType: "openai_compatible", ModelName: "verified-vision-fixture", APIKey: "internal-fixture-key"}, nil
			})
			handler := NewHandler(store, nil).WithDocumentImport(service).WithQuestionRubricExams(exams)
			scope := auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}
			want := http.StatusOK
			switch scenario {
			case "foreign scope":
				scope.TenantWide = false
				want = http.StatusNotFound
			case "stale revision":
				input.ExpectedRevision = 2
				want = http.StatusConflict
			case "source changed":
				input.ExpectedSourceSHA256 = strings.Repeat("b", 64)
				want = http.StatusConflict
			case "changed during call", "question type changed during call", "frozen during call":
				want = http.StatusConflict
			case "invalid result", "local verified", "local associated", "unknown origin":
				want = http.StatusBadGateway
			case "browser diagnostic metadata", "browser fill labels":
				want = http.StatusBadRequest
			case "geometry fields", "browser frame metadata":
				input.Candidates = []TemplateAnalysisGeometry{{CandidateID: "page-1-frame-1",
					BBox: TemplateAnalysisBBox{X: .1, Y: .2, Width: .7, Height: .3}, EvidenceRefs: []string{}}}
				if scenario == "browser frame metadata" {
					want = http.StatusBadRequest
				}
			}
			body, _ := json.Marshal(input)
			if scenario == "browser frame metadata" {
				// 浏览器几何对象另带 confidence；必须先挑选四个坐标字段，不能靠后端忽略未知字段。
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				payload["candidates"].([]any)[0].(map[string]any)["bbox"].(map[string]any)["confidence"] = .95
				body, _ = json.Marshal(payload)
			}
			if scenario == "browser diagnostic metadata" {
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)
				payload["diagnostic_context"] = map[string]any{"template_id": "another-template"}
				body, _ = json.Marshal(payload)
			}
			if scenario == "browser fill labels" {
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)
				payload["fill_blank_question_labels"] = []string{"学生填写内容"}
				body, _ = json.Marshal(payload)
			}
			request := httptest.NewRequest(http.MethodPost, "/template/analyze-page", bytes.NewReader(body))
			request.SetPathValue("id", "template")
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: "tenant"}), scope))
			response := httptest.NewRecorder()
			handler.AnalyzeTemplatePage(response, request)
			if response.Code != want {
				t.Fatalf("got %d %s, want %d", response.Code, response.Body.String(), want)
			}
			if scenario == "identity regions" && !strings.Contains(response.Body.String(), `"identity_label":"准考证号"`) {
				t.Fatalf("identity layout metadata was dropped: %s", response.Body.String())
			}
			if scenario == "objective group" && !strings.Contains(response.Body.String(), `"role":"objective_group"`) {
				t.Fatalf("whole objective region was dropped: %s", response.Body.String())
			}
			if scenario == "local fill candidate" && (!strings.Contains(response.Body.String(), `"origin":"local_detector"`) || !strings.Contains(response.Body.String(), `"semantic_check":"needs_review"`) || !strings.Contains(response.Body.String(), `"association_status":"unassociated"`)) {
				t.Fatalf("local review facts were dropped: %s", response.Body.String())
			}
			if scenario == "local layout verified" && (!strings.Contains(response.Body.String(), `"origin":"local_detector"`) || !strings.Contains(response.Body.String(), `"semantic_check":"supported"`) || !strings.Contains(response.Body.String(), `"association_status":"associated"`)) {
				t.Fatalf("verified local fill did not retain automatic association: %s", response.Body.String())
			}
			if scenario == "local layout conflict" && (!strings.Contains(response.Body.String(), `"role":"uncertain"`) || !strings.Contains(response.Body.String(), `"semantic_check":"needs_review"`) || !strings.Contains(response.Body.String(), `"association_status":"unassociated"`)) {
				t.Fatalf("verified local fill bypassed semantic conflict defense: %s", response.Body.String())
			}
			if (scenario == "browser frame metadata" || scenario == "browser diagnostic metadata" || scenario == "browser fill labels" || scenario == "foreign scope" || scenario == "stale revision" || scenario == "source changed") && calls.Load() != 0 {
				t.Fatal("invalid snapshot spent an upstream request")
			}
			if scenario == "browser frame metadata" && !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("unknown geometry metadata did not fail strict JSON decoding: %s", response.Body.String())
			}
			if scenario == "geometry fields" && calls.Load() != 1 {
				t.Fatal("valid geometry did not reach the model analysis service exactly once")
			}
			if store.templates["template"].Status != "draft" || len(store.bindings) != 0 {
				t.Fatal("analysis published or bound a template")
			}
		})
	}
}

// 教师识别只能使用独立视觉用途；文本模型配置和几何服务可用都不能绕过该门禁。
func TestTemplatePageAnalysisRequiresIndependentVisionAssignment(t *testing.T) {
	for _, scenario := range []string{"no resolver", "no assignment", "assignment unavailable", "chat only"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				calls.Add(1)
			}))
			defer upstream.Close()
			service := NewDocumentImportService(NewMemoryStore(), nil, nil, upstream.URL, strings.Repeat("k", 32), time.Second)
			switch scenario {
			case "no assignment":
				service.WithTemplateVisionResolver(func(context.Context, string) (*DocumentModelConfig, error) { return nil, nil })
			case "assignment unavailable":
				service.WithTemplateVisionResolver(func(context.Context, string) (*DocumentModelConfig, error) {
					return nil, errors.New("assigned model is unavailable")
				})
			case "chat only":
				service.WithModelResolver(func(context.Context, string) (*DocumentModelConfig, error) {
					t.Fatal("answer sheet recognition borrowed the text parser model")
					return nil, nil
				})
			}
			_, err := service.analyzeTemplatePage(t.Context(), "tenant", testPageAnalysisInput(t), []string{"17"})
			if !errors.Is(err, errTemplateVisionModelRequired) || calls.Load() != 0 {
				t.Fatalf("missing vision assignment sent an upstream request: err=%v calls=%d", err, calls.Load())
			}
		})
	}
}
