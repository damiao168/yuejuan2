package backmark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func scoringFixture(t *testing.T) (*Service, *MemoryStore, Item) {
	t.Helper()
	store := NewMemoryStore()
	store.SeedSource(SourceTask{ReviewTaskID: "task-1", OriginalGradeID: "grade-1", OriginalReviewer: "grader-a", OriginalScore: 4, MaxScore: 5, GradedAt: time.Now().UTC()})
	service := NewService(store).WithContextSource(backmarkContextSource{context: review.TaskContext{
		FrozenRubric: paper.Rubric{ID: "frozen-1", MaxScore: 5, Points: []paper.RubricPoint{
			{ID: "p1", Score: 3, Required: true}, {ID: "p2", Score: 2},
		}},
	}})
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{SourceIncidentID: "incident-1", ReassignedTo: "grader-b"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Claim(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b")
	if err != nil {
		t.Fatal(err)
	}
	return service, store, item
}

func TestSubmitValidatesFrozenRubricThroughAPI(t *testing.T) {
	for _, test := range []struct {
		name       string
		score      float64
		selections []RubricSelection
		status     int
	}{
		{"unknown point", 3, []RubricSelection{{PointID: "forged", Score: 3}}, http.StatusBadRequest},
		{"duplicate point", 4, []RubricSelection{{PointID: "p1", Score: 2}, {PointID: "p1", Score: 2}}, http.StatusBadRequest},
		{"point above maximum", 4, []RubricSelection{{PointID: "p1", Score: 4}}, http.StatusBadRequest},
		{"sum mismatch", 4, []RubricSelection{{PointID: "p1", Score: 3}}, http.StatusBadRequest},
		{"missing selections", 3, nil, http.StatusBadRequest},
		{"negative point", 0, []RubricSelection{{PointID: "p1", Score: -1}}, http.StatusBadRequest},
		{"valid partial credit", 4, []RubricSelection{{PointID: "p1", Score: 2}, {PointID: "p2", Score: 2}}, http.StatusCreated},
		{"required point can earn zero", 0, []RubricSelection{{PointID: "p1", Score: 0}}, http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, item := scoringFixture(t)
			body, err := json.Marshal(SubmitInput{Score: test.score, RubricSelections: test.selections, ExpectedRevision: item.Revision})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/backmark-items/"+item.ID+"/submit", strings.NewReader(string(body)))
			request.SetPathValue("itemId", item.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "grader-b", TenantID: "tenant-1"}))
			response := httptest.NewRecorder()
			NewHandler(service, nil).Submit(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusBadRequest {
				if len(store.grades) != 0 || store.items[item.ID].Revision != item.Revision {
					t.Fatal("invalid evidence mutated grading facts")
				}
				if !strings.Contains(response.Body.String(), "invalid_backmark_input") {
					t.Fatal(response.Body.String())
				}
			}
		})
	}
}

func TestSubmitFailsClosedWithoutFrozenContext(t *testing.T) {
	service, store, item := scoringFixture(t)
	service.context = nil
	_, _, err := service.Submit(context.Background(), "tenant-1", item.ID, "grader-b", SubmitInput{Score: 3, ExpectedRevision: item.Revision})
	if !errors.Is(err, ErrNotFound) || len(store.grades) != 0 {
		t.Fatalf("err=%v grades=%d", err, len(store.grades))
	}
}

func TestPreviewHashBindsScopeSelectorAndSourceSnapshot(t *testing.T) {
	for _, change := range []string{"none", "selector", "tenant", "exam", "question", "same count different grade", "added task", "removed task"} {
		t.Run(change, func(t *testing.T) {
			store := NewMemoryStore()
			source := SourceTask{ReviewTaskID: "task-1", OriginalGradeID: "grade-1", OriginalReviewer: "grader-a", OriginalScore: 3, MaxScore: 5, GradedAt: time.Now().UTC()}
			store.SeedSource(source)
			service := NewService(store)
			preview, err := service.Preview(context.Background(), "tenant-1", "exam-1", "question-1", Selector{})
			if err != nil || len(preview.SelectorHash) != 64 {
				t.Fatalf("preview=%#v err=%v", preview, err)
			}
			tenantID, examID, questionID := "tenant-1", "exam-1", "question-1"
			input := CreateInput{SourceIncidentID: "incident-1", ReassignedTo: "grader-b", SelectorHash: preview.SelectorHash}
			switch change {
			case "selector":
				input.Selector.GraderID = "grader-a"
			case "tenant":
				tenantID = "tenant-2"
			case "exam":
				examID = "exam-2"
			case "question":
				questionID = "question-2"
			case "same count different grade":
				source.OriginalGradeID = "grade-2"
				store.SeedSource(source)
			case "added task":
				source.ReviewTaskID = "task-2"
				store.SeedSource(source)
			case "removed task":
				delete(store.sources, source.ReviewTaskID)
			}
			_, err = service.Create(context.Background(), tenantID, examID, questionID, "manager-1", input)
			if change == "none" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrPreviewStale) || len(store.batches) != 0 {
				t.Fatalf("err=%v batches=%d", err, len(store.batches))
			}
		})
	}
}

func TestPreviewHashNormalizesSelector(t *testing.T) {
	now := time.Now().UTC()
	local := now.In(time.FixedZone("CST", 8*60*60))
	a := normalizeSelector(Selector{GraderID: " grader-a ", TaskIDs: []string{"task-2", "task-1", "task-1"}, TimeRange: &TimeRange{From: &local}})
	b := normalizeSelector(Selector{GraderID: "grader-a", TaskIDs: []string{"task-1", "task-2"}, TimeRange: &TimeRange{From: &now}, ScoreBand: &ScoreBand{}})
	if sourceHash("t", "e", "q", a, nil) != sourceHash("t", "e", "q", b, nil) {
		t.Fatal("equivalent selectors produced different hashes")
	}
}

func TestStalePreviewReturnsConflict(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(NewService(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"source_incident_id":"incident-1","reassigned_to":"grader-b","selector_hash":"stale"}`))
	request.SetPathValue("examId", "exam-1")
	request.SetPathValue("questionId", "question-1")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "manager-1", TenantID: "tenant-1"}))
	response := httptest.NewRecorder()
	handler.Create(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "backmark_preview_stale") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSummaryCountsAndStatusFilterIncludeUnloadedItems(t *testing.T) {
	store := NewMemoryStore()
	for i := 0; i < DefaultPageSize+5; i++ {
		store.SeedSource(SourceTask{ReviewTaskID: strings.Repeat("t", i+1), OriginalGradeID: "g", OriginalReviewer: "grader-a", OriginalScore: 3, MaxScore: 5, GradedAt: time.Now().UTC()})
	}
	service := NewService(store).WithContextSource(backmarkContextSource{})
	created, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{SourceIncidentID: "incident-1", ReassignedTo: "grader-b", Policy: Policy{Disposition: DispositionRegrade}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Get(context.Background(), "tenant-1", created.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	unloaded := map[string]bool{}
	for _, item := range created.Items {
		unloaded[item.ID] = true
	}
	for _, item := range first.Items {
		delete(unloaded, item.ID)
	}
	for id := range unloaded {
		item, err := service.Claim(context.Background(), "tenant-1", id, "grader-b")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.Submit(context.Background(), "tenant-1", id, "grader-b", SubmitInput{Score: 1, ExpectedRevision: item.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	first, err = service.Get(context.Background(), "tenant-1", created.Batch.ID)
	if err != nil || first.RegradeRequiredCount != 5 || first.CompletedCount != 5 || first.PendingCount != DefaultPageSize {
		t.Fatalf("counts=%#v err=%v", first.StatusCounts, err)
	}
	for _, item := range first.Items {
		if item.Status == ItemRegradeRequired {
			t.Fatal("fixture severe item appeared in first page")
		}
	}
	filtered, err := service.GetPage(context.Background(), "tenant-1", created.Batch.ID, PageOptions{Limit: 2, Status: ItemRegradeRequired})
	if err != nil || len(filtered.Items) != 2 || filtered.RegradeRequiredCount != 5 {
		t.Fatalf("filtered=%#v err=%v", filtered, err)
	}
	last := filtered.Items[1]
	second, err := service.GetPage(context.Background(), "tenant-1", created.Batch.ID, PageOptions{Limit: 10, Status: ItemRegradeRequired, CursorID: last.ID, CursorCreatedAt: last.CreatedAt})
	if err != nil || len(second.Items) != 3 || second.Items[0].ID == filtered.Items[0].ID {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
