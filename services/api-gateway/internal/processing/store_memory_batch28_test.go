package processing

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestBatch28ExceptionFiltersAndProjectionTime(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	// 此内存实现以租户下页面是否存在识别来源；种子使用不同 pageID，不宣称生产隔离的完整验证。
	for _, item := range []Exception{
		{ID: "a", PageID: "page-a", ExamID: "exam-a", Severity: SeverityP0, Status: ExceptionOpen, CreatedAt: now},
		{ID: "b", PageID: "page-b", ExamID: "exam-a", Severity: SeverityP1, Status: ExceptionAssigned, CreatedAt: now},
		{ID: "c", PageID: "page-c", ExamID: "exam-b", Severity: SeverityP0, Status: ExceptionOpen, CreatedAt: now.Add(-time.Hour)},
		{ID: "foreign", PageID: "page-foreign", ExamID: "exam-a", CreatedAt: now.Add(time.Hour)},
		{ID: "orphan", PageID: "missing", ExamID: "exam-a", CreatedAt: now.Add(time.Hour)},
	} {
		item.Details = map[string]any{"seed": item.ID}
		store.exceptions[item.ID] = item
	}
	store.states[stateKey("tenant-a", "page-a")] = PageState{CurrentStage: StageQualityChecked}
	store.states[stateKey("tenant-a", "page-b")] = PageState{CurrentStage: StageReady}
	store.states[stateKey("tenant-a", "page-c")] = PageState{CurrentStage: StageReady}
	store.states[stateKey("tenant-b", "page-foreign")] = PageState{CurrentStage: StageReady}
	store.projectedAt["tenant-a:exam-a"] = now
	store.projectedAt["tenant-a:exam-b"] = now.Add(time.Hour)
	store.projectedAt["tenant-b:exam-a"] = now.Add(10 * time.Hour)
	store.projectedAt["tenant-aa:exam-a"] = now.Add(10 * time.Hour)
	cases := []struct {
		name   string
		filter ExceptionFilter
		want   []string
		latest time.Time
	}{
		{"all", ExceptionFilter{}, []string{"b", "a", "c"}, now.Add(time.Hour)},
		{"exam", ExceptionFilter{ExamID: "exam-a"}, []string{"b", "a"}, now},
		{"severity", ExceptionFilter{Severity: SeverityP0}, []string{"a", "c"}, now.Add(time.Hour)},
		{"status", ExceptionFilter{Status: ExceptionAssigned}, []string{"b"}, now.Add(time.Hour)},
		{"stage", ExceptionFilter{Stage: StageQualityChecked}, []string{"a"}, now.Add(time.Hour)},
		{"combined", ExceptionFilter{ExamID: "exam-a", Severity: SeverityP0, Status: ExceptionOpen, Stage: StageQualityChecked}, []string{"a"}, now},
		{"empty-still-has-projection", ExceptionFilter{ExamID: "exam-a", Severity: SeverityP3}, []string{}, now},
		{"unknown-exam", ExceptionFilter{ExamID: "absent"}, []string{}, time.Time{}},
		{"legacy-cursor-and-subject-unused", ExceptionFilter{CursorAt: now.Add(-time.Hour), CursorID: "c", Subject: "math"}, []string{"b", "a", "c"}, now.Add(time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertExceptionFilterAndProjection(t, store, tc.filter, tc.want, tc.latest)
		})
	}
	result, err := store.ListExceptions(context.Background(), "tenant-a", ExceptionFilter{Limit: 1})
	if err != nil || len(result.Exceptions) != 1 || result.Exceptions[0].ID != "b" || !result.HasMore || result.NextCursor != now.Format(time.RFC3339Nano)+"|b" {
		t.Fatalf("limited result: %#v, %v", result, err)
	}
	result.Exceptions[0].Details["seed"] = "changed"
	if store.exceptions["b"].Details["seed"] != "b" {
		t.Fatal("list details must not write back to the store")
	}
}

func TestBatch28ExceptionLimitFallback(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 26; i++ {
		id := fmt.Sprintf("item-%02d", i)
		store.exceptions[id] = Exception{ID: id, PageID: id, CreatedAt: now}
		store.states[stateKey("tenant-a", id)] = PageState{}
	}
	for _, limit := range []int{-1, 0, 101} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			result, err := store.ListExceptions(context.Background(), "tenant-a", ExceptionFilter{Limit: limit})
			if err != nil || len(result.Exceptions) != 25 || !result.HasMore || result.NextCursor != now.Format(time.RFC3339Nano)+"|item-01" {
				t.Fatalf("fallback result: %#v, %v", result, err)
			}
		})
	}
}

// 没有对应考试的投影时保留 nil；空的筛选结果仍应保留已经存在的投影时间。
func assertExceptionFilterAndProjection(t *testing.T, store *MemoryStore, filter ExceptionFilter, want []string, latest time.Time) {
	t.Helper()
	result, err := store.ListExceptions(context.Background(), "tenant-a", filter)
	if err != nil || result.Exceptions == nil {
		t.Fatalf("list: %#v, %v", result, err)
	}
	ids := make([]string, len(result.Exceptions))
	for i, item := range result.Exceptions {
		ids[i] = item.ID
	}
	if !reflect.DeepEqual(ids, want) || result.HasMore || result.NextCursor != "" {
		t.Fatalf("result: %#v, want ids %v", result, want)
	}
	if latest.IsZero() {
		if result.ProjectedAt != nil {
			t.Fatalf("unexpected projection: %v", result.ProjectedAt)
		}
	} else if result.ProjectedAt == nil || !result.ProjectedAt.Equal(latest) {
		t.Fatalf("projection = %v, want %v", result.ProjectedAt, latest)
	}
}
