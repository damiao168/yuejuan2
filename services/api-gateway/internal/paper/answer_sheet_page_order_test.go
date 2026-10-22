package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func testAnswerSheetOrderInput(t *testing.T) AnswerSheetPageOrderInput {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 100, 140)), nil); err != nil {
		t.Fatal(err)
	}
	first := AnswerSheetOrderPage{PageID: "page-b", Width: 100, Height: 140, MediaType: "image/jpeg", DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes()), SHA256: fmt.Sprintf("%x", sha256.Sum256(buffer.Bytes()))}
	second := first
	second.PageID = "page-a"
	return AnswerSheetPageOrderInput{Pages: []AnswerSheetOrderPage{first, second}}
}

func TestAnswerSheetPageOrderInputRejectsImageAndPageIdentityMismatch(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong hash", "wrong dimensions", "too large", "duplicate id", "unsafe id", "wrong media", "empty"} {
		input := testAnswerSheetOrderInput(t)
		switch scenario {
		case "wrong hash":
			input.Pages[0].SHA256 = strings.Repeat("a", 64)
		case "wrong dimensions":
			input.Pages[0].Width++
		case "too large":
			input.Pages[0].Width = 1001
		case "duplicate id":
			input.Pages[0].PageID = input.Pages[1].PageID
		case "unsafe id":
			input.Pages[0].PageID = "file/student"
		case "wrong media":
			input.Pages[0].MediaType = "image/png"
		case "empty":
			input.Pages = nil
		}
		if valid := validateAnswerSheetPageOrderInput(input) == nil; valid != (scenario == "valid") {
			t.Fatalf("%s validity=%v", scenario, valid)
		}
	}
}

func TestAnswerSheetPageOrderResultCannotOmitInventOrRepeatPageIDs(t *testing.T) {
	input := testAnswerSheetOrderInput(t)
	for _, scenario := range []string{"valid", "missing", "repeated", "foreign", "uncertain partial", "personal reason", "invented status"} {
		result := AnswerSheetPageOrderResponse{Status: "ordered", PageOrder: []string{"page-a", "page-b"}, ReasonCodes: []string{"PAGE_ORDER_DOES_NOT_VERIFY_STUDENT_IDENTITY"}, ModelIdentity: map[string]string{}}
		switch scenario {
		case "missing":
			result.PageOrder = result.PageOrder[:1]
		case "repeated":
			result.PageOrder[0] = "page-b"
		case "foreign":
			result.PageOrder[0] = "page-c"
		case "uncertain partial":
			result.Status = "uncertain"
		case "personal reason":
			result.ReasonCodes = []string{"测试学生甲"}
		case "invented status":
			result.Status = "approved"
		}
		if valid := validateAnswerSheetPageOrderResult(result, input) == nil; valid != (scenario == "valid") {
			t.Fatalf("%s validity=%v", scenario, valid)
		}
	}
}

func TestAnswerSheetPageOrderHTTPUsesManagedVisionAndRechecksMutableExam(t *testing.T) {
	for _, scenario := range []string{"success", "uncertain", "frozen before", "frozen during", "wrong result request", "bad page permutation", "no assignment", "bad image"} {
		t.Run(scenario, func(t *testing.T) {
			input := testAnswerSheetOrderInput(t)
			exams := &pageAnalysisExamReader{status: "draft"}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/answer-sheet/page-order" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("wrong authenticated internal operation")
				}
				var body struct {
					RequestID     string                 `json:"request_id"`
					SchemaVersion string                 `json:"schema_version"`
					Pages         []AnswerSheetOrderPage `json:"pages"`
					ManagedModel  *DocumentModelConfig   `json:"managed_model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.SchemaVersion != answerSheetPageOrderSchema || body.ManagedModel == nil || body.ManagedModel.ModelName != "vision-fixture" || len(body.Pages) != 2 {
					t.Error("managed model or page manifest missing")
				}
				if scenario == "frozen during" {
					exams.status = "collecting"
				}
				if scenario == "wrong result request" {
					body.RequestID = "another-request"
				}
				status, order, reasons := "ordered", []string{"page-a", "page-b"}, []string{"PAGE_ORDER_DOES_NOT_VERIFY_STUDENT_IDENTITY"}
				if scenario == "uncertain" {
					status, order, reasons = "uncertain", []string{}, []string{"PRINTED_PAGE_NUMBER_REQUIRED"}
				}
				if scenario == "bad page permutation" {
					order[0] = "foreign-page"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"request_id": body.RequestID, "schema_version": answerSheetPageOrderSchema, "status": status, "page_order": order, "reason_codes": reasons, "model_identity": map[string]string{}})
			}))
			defer upstream.Close()
			service := NewDocumentImportService(NewMemoryStore(), nil, nil, upstream.URL, strings.Repeat("k", 32), time.Second)
			service.WithTemplateVisionResolver(func(context.Context, string) (*DocumentModelConfig, error) {
				if scenario == "no assignment" {
					return nil, nil
				}
				return &DocumentModelConfig{AdapterType: "openai_compatible", ModelName: "vision-fixture", APIKey: "private-model-key"}, nil
			})
			handler := NewHandler(NewMemoryStore(), nil).WithDocumentImport(service).WithQuestionRubricExams(exams)
			want := http.StatusOK
			switch scenario {
			case "frozen before":
				exams.status, want = "collecting", http.StatusConflict
			case "frozen during":
				want = http.StatusConflict
			case "wrong result request", "bad page permutation":
				want = http.StatusBadGateway
			case "no assignment":
				want = http.StatusServiceUnavailable
			case "bad image":
				input.Pages[0].Width, want = 2000, http.StatusBadRequest
			}
			body, _ := json.Marshal(input)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam/answer-sheet-page-order", bytes.NewReader(body))
			request.SetPathValue("examId", "exam")
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: "tenant"}), auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}))
			response := httptest.NewRecorder()
			handler.AnalyzeAnswerSheetPageOrder(response, request)
			if response.Code != want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private-model-key") || strings.Contains(response.Body.String(), `"request_id"`) && response.Code == 200 {
				t.Fatal("internal request or managed credentials exposed")
			}
			if scenario == "frozen before" || scenario == "no assignment" || scenario == "bad image" {
				if calls.Load() != 0 {
					t.Fatal("invalid/precluded request spent a model call")
				}
			}
		})
	}
}

func TestAnswerSheetPageOrderAcceptsContentEvidenceWithoutPrintedTotal(t *testing.T) {
	input := testAnswerSheetOrderInput(t)
	result := AnswerSheetPageOrderResponse{Status: "ordered", PageOrder: []string{"page-a", "page-b"},
		ReasonCodes:   []string{"PAGE_COUNT_NOT_VERIFIED", "PAGE_ORDER_FROM_QUESTION_SEQUENCE"},
		ModelIdentity: map[string]string{}}
	if err := validateAnswerSheetPageOrderResult(result, input); err != nil {
		t.Fatalf("content-based page order was rejected: %v", err)
	}
}
