package subjective

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGradingSourceHandlerRechecksTenantActorAndExamScope(t *testing.T) {
	exam := "00000000-0000-0000-0000-000000000011"
	segment := "00000000-0000-0000-0000-000000000012"
	store := NewMemoryStore()
	store.AddContext("tenant", segment, Context{Question: paper.Question{ID: "q", ExamID: exam, QuestionType: "essay"}, AnswerText: "private", Rubric: paper.Rubric{ID: "r"}, AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "frozen"}})
	h := NewHandler(store, nil, nil)
	for _, scenario := range []struct {
		name   string
		scope  auth.AccessScope
		status int
	}{
		{"allowed", auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}, 200},
		{"assigned-only material scope", auth.AccessScope{TenantID: "tenant", ActorID: "actor", ExamIDs: []string{exam}, AssignedOnly: true}, 403},
		{"other exam", auth.AccessScope{TenantID: "tenant", ActorID: "actor", ExamIDs: []string{"other"}}, 403},
		{"other actor", auth.AccessScope{TenantID: "tenant", ActorID: "other", TenantWide: true}, 403},
		{"other tenant", auth.AccessScope{TenantID: "other", ActorID: "actor", TenantWide: true}, 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.SetPathValue("examId", exam)
			ctx := auth.WithUser(r.Context(), auth.User{TenantID: "tenant", ID: "actor"})
			ctx = auth.WithAccessScope(ctx, scenario.scope)
			w := httptest.NewRecorder()
			h.ListGradingSources(w, r.WithContext(ctx))
			if w.Code != scenario.status || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Code == 200 && !strings.Contains(w.Body.String(), segment) {
				t.Fatalf("missing selected source %s", w.Body.String())
			}
		})
	}
}

func TestGradingSourceDirectoryIncludesUnassignedMaterialsWithoutAnswerContents(t *testing.T) {
	store := NewMemoryStore()
	for _, fixture := range []struct {
		tenant, id, exam, kind string
		ready                  bool
	}{
		{"tenant", "A", "exam", "essay", true}, {"tenant", "B", "exam", "calculation", false},
		{"other", "C", "exam", "essay", true}, {"tenant", "D", "other-exam", "essay", true}, {"tenant", "E", "exam", "single_choice", true},
	} {
		item := Context{Question: paper.Question{ID: "q", ExamID: fixture.exam, QuestionNo: "2", QuestionType: fixture.kind}, AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "frozen"}, Rubric: paper.Rubric{ID: "rubric"}}
		if fixture.ready {
			item.AnswerText = "private synthetic answer"
		}
		store.AddContext(fixture.tenant, fixture.id, item)
	}
	rows, err := store.ListGradingSources(context.Background(), "tenant", "exam", GradingSourceFilter{Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].AnswerSegmentID != "A" || !rows[0].Ready {
		t.Fatalf("sources=%+v error=%v", rows, err)
	}
	rows, err = store.ListGradingSources(context.Background(), "tenant", "exam", GradingSourceFilter{Limit: 10, Cursor: "A"})
	if err != nil || len(rows) != 1 || rows[0].AnswerSegmentID != "B" || rows[0].Ready || rows[0].UnavailableReason == "" {
		t.Fatalf("sources=%+v error=%v", rows, err)
	}
}
