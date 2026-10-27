package goldpaper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type coverageHTTPProbe struct {
	Route  string `json:"route"`
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// 此探针只用生产 MemoryStore/handler/路由序列化完整与不完整的合成 Gold 覆盖，不连数据库。
func TestCoverageHTTPEmptyCollectionsAreArrays(t *testing.T) {
	const tenant = "synthetic-tenant"
	store := NewMemoryStore()
	for _, fixture := range []struct {
		exam, question, submission, grade string
		score                             float64
	}{
		{"exam-complete", "question-complete", "submission-zero", "grade-zero", 0},
		{"exam-complete", "question-complete", "submission-middle", "grade-middle", 2.5},
		{"exam-complete", "question-complete", "submission-full", "grade-full", 5},
		{"exam-incomplete", "question-incomplete", "submission-only-zero", "grade-only-zero", 0},
	} {
		store.SetSource(tenant, Source{ExamID: fixture.exam, QuestionID: fixture.question, SubmissionID: fixture.submission,
			SnapshotID: "snapshot-" + fixture.submission, SubjectCode: "mathematics", ArchetypeCode: "structured_steps", RiskTier: "R2",
			MaxScore: 5, RubricSnapshot: map[string]any{"criteria": []any{}}, AvailableGradeIDs: []string{fixture.grade}, AnswerSegmentID: "segment-" + fixture.submission,
			ImageBinding: ImageBinding{SegmentID: "segment-" + fixture.submission, AssetID: "asset-" + fixture.submission, SHA256: strings.Repeat("a", 64), OwnerType: "answer_segment_crop", OwnerID: "run-" + fixture.submission}})
		item, err := store.Nominate(context.Background(), tenant, fixture.exam, fixture.question, "synthetic-nominator", NominateInput{
			SubmissionID: fixture.submission, ReferenceScore: fixture.score, Explanation: "synthetic evidence", ErrorTags: []string{"alternative_valid_path", "missing_key_step", "correct_conclusion_insufficient_process"}, SourceGradeIDs: []string{fixture.grade}})
		if err != nil {
			t.Fatalf("nominate %s: %v", fixture.submission, err)
		}
		if _, err := store.Approve(context.Background(), tenant, item.ID, "synthetic-approver", 1); err != nil {
			t.Fatalf("approve %s: %v", fixture.submission, err)
		}
	}

	handler := NewHandler(store, nil)
	requireReviewManage := func(next http.HandlerFunc) http.Handler { return auth.RequirePermission("review:manage")(next) }
	mux := http.NewServeMux()
	RegisterRoutes(mux, handler, requireReviewManage, requireReviewManage)
	user := auth.User{ID: "synthetic-review-admin", TenantID: tenant, Permissions: []string{"review:manage"}}
	routed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
	})
	complete := runCoverageHTTPProbe(t, routed, "/api/v1/exams/exam-complete/questions/question-complete/gold-coverage")
	incomplete := runCoverageHTTPProbe(t, routed, "/api/v1/exams/exam-incomplete/questions/question-incomplete/gold-coverage")
	var completePayload struct {
		Coverage struct {
			Ready bool            `json:"ready"`
			Gaps  json.RawMessage `json:"gaps"`
		} `json:"coverage"`
	}
	var incompletePayload struct {
		Coverage struct {
			Ready bool            `json:"ready"`
			Gaps  json.RawMessage `json:"gaps"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(complete.Body), &completePayload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(incomplete.Body), &incompletePayload); err != nil {
		t.Fatal(err)
	}
	if !completePayload.Coverage.Ready || string(completePayload.Coverage.Gaps) != "[]" {
		t.Fatalf("complete control did not yield ready/[]: %s", complete.Body)
	}
	if incompletePayload.Coverage.Ready || string(incompletePayload.Coverage.Gaps) != `["missing_full_score","missing_middle_score"]` {
		t.Fatalf("incomplete control unexpected payload: %s", incomplete.Body)
	}
	t.Logf("complete: ready=%v gaps=%s; incomplete: ready=%v gaps=%s", completePayload.Coverage.Ready, completePayload.Coverage.Gaps, incompletePayload.Coverage.Ready, incompletePayload.Coverage.Gaps)
}

func runCoverageHTTPProbe(t *testing.T, handler http.Handler, path string) coverageHTTPProbe {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
	}
	return coverageHTTPProbe{Route: path, Status: response.Code, Body: strings.TrimSpace(response.Body.String())}
}
