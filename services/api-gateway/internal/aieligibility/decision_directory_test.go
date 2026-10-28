package aieligibility

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestDecisionDirectoryTenantKeywordAndStablePagination(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	for _, item := range []Decision{
		{ID: "c", TenantID: "school-a", RunItemID: "run-c", CreatedAt: now, Decision: assessment.ScoringHumanPrimary, InputSnapshot: map[string]any{"subject_code": "mathematics", "archetype_code": "short_constructed", "hidden": "secret"}},
		{ID: "b", TenantID: "school-a", RunItemID: "run-b", CreatedAt: now, Decision: assessment.ScoringHumanPrimary, InputSnapshot: map[string]any{"subject_code": "mathematics"}},
		{ID: "a", TenantID: "school-a", RunItemID: "run-a", CreatedAt: now.Add(-time.Hour), Decision: assessment.ScoringHumanPrimary, InputSnapshot: map[string]any{"subject_code": "chinese"}},
		{ID: "other", TenantID: "school-b", RunItemID: "other", CreatedAt: now, Decision: assessment.ScoringHumanPrimary, InputSnapshot: map[string]any{"subject_code": "mathematics"}},
	} {
		if _, err := store.CreateOrGetDecision(t.Context(), item.TenantID, item); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewHandler(NewService(store))
	read := func(query string) (DecisionPage, int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ai-eligibility/decisions?"+query, nil)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{TenantID: "school-a"}))
		rec := httptest.NewRecorder()
		handler.ListDecisions(rec, req)
		var page DecisionPage
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		return page, rec.Code, rec.Body.String()
	}
	page, status, body := read("q=MATHEMATICS&limit=1&tenant_id=school-b")
	if status != http.StatusOK || len(page.Decisions) != 1 || page.Decisions[0].ID != "c" || !page.HasMore || page.NextCursor == "" || strings.Contains(body, "secret") {
		t.Fatalf("first=%d %s", status, body)
	}
	next, status, body := read("q=mathematics&limit=1&cursor=" + url.QueryEscape(page.NextCursor))
	if status != http.StatusOK || len(next.Decisions) != 1 || next.Decisions[0].ID != "b" || next.HasMore {
		t.Fatalf("next=%d %s", status, body)
	}
	chinese, _, _ := read("q=" + url.QueryEscape("数学"))
	if len(chinese.Decisions) != 2 {
		t.Fatal("Chinese label search must match stored subject code")
	}
	empty, _, _ := read("q=secret")
	if len(empty.Decisions) != 0 {
		t.Fatal("hidden snapshot field included in directory search")
	}
	for _, query := range []string{"limit=101", "cursor=bad", "q=" + url.QueryEscape(strings.Repeat("中", 129))} {
		if _, status, _ := read(query); status != http.StatusBadRequest {
			t.Fatalf("query=%q status=%d", query, status)
		}
	}
}
