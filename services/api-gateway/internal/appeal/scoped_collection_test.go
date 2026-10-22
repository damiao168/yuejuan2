package appeal

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAppealCollectionScopeFiltersBeforePaginationAndEveryStatistic(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	for _, seed := range []struct {
		id, school string
		hours      float64
	}{
		{"own", "school-a", 2}, {"foreign", "school-b", 100},
	} {
		reviewed := now.Add(time.Duration(seed.hours) * time.Hour)
		created := now
		if seed.id == "foreign" {
			created = now.Add(time.Minute)
		}
		store.appeals[seed.id] = Appeal{ID: seed.id, TenantID: "tenant", ExamID: seed.id + "-exam",
			StudentID: seed.id + "-student", SubmissionID: seed.id + "-submission", Status: "score_adjusted",
			CreatedAt: created, ReviewedAt: &reviewed}
		store.submissions[key(seed.id+"-exam", seed.id+"-student")] = SubmissionGradeSeed{SchoolID: seed.school}
		store.adjustments[seed.id] = []ScoreAdjustment{{ID: seed.id + "-adjustment"}}
	}
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: "admin", TenantID: "tenant", Roles: []string{"school_admin"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}})
	if err != nil {
		t.Fatal(err)
	}
	// 外校记录更新、排序更靠前；必须在 LIMIT 前过滤，不能拿到空页或外校游标。
	items, err := store.ListAppeals(context.Background(), "tenant", ListFilter{AccessScope: &scope, Limit: 1})
	if err != nil || len(items) != 1 || items[0].ID != "own" {
		t.Fatalf("scoped page=%v err=%v", items, err)
	}
	stats, err := store.Statistics(context.Background(), "tenant", StatisticsFilter{AccessScope: &scope})
	if err != nil || stats.Total != 1 || stats.ByStatus["score_adjusted"] != 1 || stats.ScoreAdjustedCount != 1 || stats.AverageHandleHours != 2 {
		t.Fatalf("scoped statistics=%+v err=%v", stats, err)
	}
	foreign, err := store.Statistics(context.Background(), "tenant", StatisticsFilter{AccessScope: &scope, ExamID: "foreign-exam"})
	if err != nil || foreign.Total != 0 || foreign.ScoreAdjustedCount != 0 || foreign.AverageHandleHours != 0 {
		t.Fatalf("foreign exam statistics=%+v err=%v", foreign, err)
	}
	tenantScope := auth.AccessScope{TenantID: "tenant", TenantWide: true}
	all, err := store.ListAppeals(context.Background(), "tenant", ListFilter{AccessScope: &tenantScope})
	if err != nil || len(all) != 2 {
		t.Fatalf("tenant manager page=%v err=%v", all, err)
	}
	classScope := auth.AccessScope{TenantID: "tenant", SchoolIDs: []string{"school-a"}}
	denied, err := store.ListAppeals(context.Background(), "tenant", ListFilter{AccessScope: &classScope})
	if err != nil || len(denied) != 0 {
		t.Fatalf("school navigation projection expanded access: %v err=%v", denied, err)
	}
	classScope.ExamIDs = []string{"own-exam"}
	allowed, err := store.ListAppeals(context.Background(), "tenant", ListFilter{AccessScope: &classScope})
	if err != nil || len(allowed) != 1 || allowed[0].ID != "own" {
		t.Fatalf("explicit exam scope=%v err=%v", allowed, err)
	}
}
