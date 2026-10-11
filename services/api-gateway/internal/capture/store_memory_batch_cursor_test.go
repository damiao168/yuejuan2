package capture

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMemoryBatchCursorKeepsDescendingBoundaryAndScope(t *testing.T) {
	now := time.Date(2026, time.October, 3, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	for _, batch := range []Batch{
		{ID: "b", TenantID: "tenant", ExamID: "exam", CreatedAt: now},
		{ID: "a", TenantID: "tenant", ExamID: "exam", CreatedAt: now.Add(-time.Second)},
		{ID: "d", TenantID: "tenant", ExamID: "exam", CreatedAt: now},
		{ID: "c", TenantID: "tenant", ExamID: "exam", CreatedAt: now},
		{ID: "f", TenantID: "tenant", ExamID: "exam", CreatedAt: now.Add(time.Second)},
		{ID: "other-tenant", TenantID: "other", ExamID: "exam", CreatedAt: now},
		{ID: "other-exam", TenantID: "tenant", ExamID: "other", CreatedAt: now},
	} {
		store.batches[batch.ID] = batch
	}
	tests := []struct {
		name   string
		filter BatchListFilter
		ids    []string
	}{
		{"no cursor", BatchListFilter{}, []string{"f", "d", "c", "b", "a"}},
		{"same time excludes cursor", BatchListFilter{CursorID: "c", CursorCreatedAt: now}, []string{"b", "a"}},
		{"cursor need not exist", BatchListFilter{CursorID: "cc", CursorCreatedAt: now}, []string{"c", "b", "a"}},
		{"limit after cursor", BatchListFilter{CursorID: "c", CursorCreatedAt: now, Limit: 1}, []string{"b"}},
		{"cursor beyond last", BatchListFilter{CursorID: "a", CursorCreatedAt: now.Add(-time.Second)}, []string{}},
		{"cursor newer than first", BatchListFilter{CursorID: "z", CursorCreatedAt: now.Add(2 * time.Second)}, []string{"f", "d", "c", "b", "a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batches, err := store.ListBatches(context.Background(), "tenant", "exam", test.filter)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(batches))
			for _, batch := range batches {
				ids = append(ids, batch.ID)
			}
			if !reflect.DeepEqual(ids, test.ids) || batches == nil {
				t.Fatalf("batch cursor returned %#v, want %#v; batches=%#v", ids, test.ids, batches)
			}
		})
	}
}
