package submission

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBatch28SubmissionListScopeOrderAndCursor(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	// 固定种子时间才能覆盖同时间 ID 排序；生产入口和分页判断仍调用真实 Store。
	for _, item := range []Submission{
		{ID: "a", TenantID: "tenant-a", ExamID: "exam-a", CreatedAt: now},
		{ID: "b", TenantID: "tenant-a", ExamID: "exam-a", CreatedAt: now},
		{ID: "new", TenantID: "tenant-a", ExamID: "exam-a", CreatedAt: now.Add(time.Hour)},
		{ID: "old", TenantID: "tenant-a", ExamID: "exam-a", CreatedAt: now.Add(-time.Hour)},
		{ID: "other-tenant", TenantID: "tenant-b", ExamID: "exam-a", CreatedAt: now.Add(2 * time.Hour)},
		{ID: "other-exam", TenantID: "tenant-a", ExamID: "exam-b", CreatedAt: now.Add(2 * time.Hour)},
	} {
		store.submissions[item.ID] = item
	}
	cases := []struct {
		name   string
		filter ListFilter
		want   []string
	}{
		{"full", ListFilter{}, []string{"new", "b", "a", "old"}},
		{"strict-tie", ListFilter{CursorID: "b", CursorCreatedAt: now}, []string{"a", "old"}},
		{"cursor-before-limit", ListFilter{CursorID: "b", CursorCreatedAt: now, Limit: 1}, []string{"a"}},
		{"timestamp-alone-disabled", ListFilter{CursorCreatedAt: now}, []string{"new", "b", "a", "old"}},
		{"future-cursor", ListFilter{CursorID: "z", CursorCreatedAt: now.Add(3 * time.Hour)}, []string{"new", "b", "a", "old"}},
		{"past-cursor", ListFilter{CursorID: "z", CursorCreatedAt: now.Add(-3 * time.Hour)}, []string{}},
		{"id-with-zero-time", ListFilter{CursorID: "z"}, []string{}},
		{"positive-limit", ListFilter{Limit: 2}, []string{"new", "b"}},
		{"negative-limit-unbounded", ListFilter{Limit: -1}, []string{"new", "b", "a", "old"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := store.ListByExam(context.Background(), "tenant-a", "exam-a", tc.filter)
			if err != nil || items == nil {
				t.Fatalf("list: %#v, %v", items, err)
			}
			ids := make([]string, len(items))
			for i, item := range items {
				ids[i] = item.ID
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}
	items, err := store.ListByExams(context.Background(), "tenant-a", []string{"exam-a", "exam-b", "empty"})
	if err != nil || len(items["exam-a"]) != 4 || len(items["exam-b"]) != 1 || items["empty"] == nil || len(items["empty"]) != 0 {
		t.Fatalf("list by exams: %#v, %v", items, err)
	}
}
