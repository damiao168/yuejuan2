package qualitydashboard

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/backmark"
)

type batch47BackmarkCall struct {
	operation  string
	tenantID   string
	examID     string
	questionID string
	batchID    string
	page       backmark.PageOptions
	ctx        context.Context
}

type batch47BackmarkStore struct {
	backmark.Store
	batches []backmark.Batch
	items   map[string][]backmark.Item
	calls   []batch47BackmarkCall
	failAt  string
	failure error
}

func (s *batch47BackmarkStore) record(call batch47BackmarkCall) error {
	s.calls = append(s.calls, call)
	if call.operation+":"+call.batchID == s.failAt {
		return s.failure
	}
	return nil
}

func (s *batch47BackmarkStore) ListBatches(ctx context.Context, tenantID, examID, questionID string, page backmark.PageOptions) ([]backmark.Batch, error) {
	err := s.record(batch47BackmarkCall{operation: "list", tenantID: tenantID, examID: examID, questionID: questionID, page: page, ctx: ctx})
	return s.batches, err
}

func (s *batch47BackmarkStore) GetBatch(ctx context.Context, tenantID, batchID string) (backmark.Batch, error) {
	err := s.record(batch47BackmarkCall{operation: "batch", tenantID: tenantID, batchID: batchID, ctx: ctx})
	return backmark.Batch{ID: batchID}, err
}

func (s *batch47BackmarkStore) ListBatchItems(ctx context.Context, tenantID, batchID string, page backmark.PageOptions) ([]backmark.Item, error) {
	err := s.record(batch47BackmarkCall{operation: "items", tenantID: tenantID, batchID: batchID, page: page, ctx: ctx})
	return s.items[batchID], err
}

func (s *batch47BackmarkStore) GetHistogram(ctx context.Context, tenantID, batchID string) ([]backmark.Histogram, error) {
	err := s.record(batch47BackmarkCall{operation: "histogram", tenantID: tenantID, batchID: batchID, ctx: ctx})
	return []backmark.Histogram{{Delta: 99, Count: 99}}, err
}

func (s *batch47BackmarkStore) GetStatusCounts(ctx context.Context, tenantID, batchID string) (backmark.StatusCounts, error) {
	err := s.record(batch47BackmarkCall{operation: "counts", tenantID: tenantID, batchID: batchID, ctx: ctx})
	return backmark.StatusCounts{PendingCount: 99, CompletedCount: 99}, err
}

func TestBatch47BackmarkReaderPreservesProjectionAndDefaultPages(t *testing.T) {
	store := &batch47BackmarkStore{
		batches: []backmark.Batch{
			{ID: "open", Status: backmark.BatchOpen},
			{ID: "completed", Status: backmark.BatchCompleted},
			{ID: "cancelled", Status: backmark.BatchCancelled},
			{ID: "ready", Status: backmark.BatchReadyForConfirmation},
		},
		items: map[string][]backmark.Item{
			"open": {
				{Status: backmark.ItemPending},
				{Status: backmark.ItemInProgress},
				{Status: backmark.ItemDiffReady, Diff: floatPointer(0)},
			},
			"completed": {{Status: backmark.ItemCancelled, Diff: floatPointer(2)}},
			"cancelled": {{Status: "future_status", Diff: floatPointer(-1)}},
			"ready":     {{Status: backmark.ItemDiffReady}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := NewBackmarkReader(backmark.NewService(store))
	summary, err := reader.GetBackmarkSummary(ctx, "tenant-1", "exam-1", "question-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenBatches != 2 || summary.PendingItems != 2 || summary.CompletedItems != 4 {
		t.Fatalf("summary=%#v", summary)
	}
	rate := summary.CorrectionRate
	if rate.Numerator != 2 || rate.Denominator != 3 || rate.SampleSize != 3 || rate.Rate == nil || *rate.Rate != 2.0/3.0 {
		t.Fatalf("correction rate=%#v", rate)
	}
	wantCalls := []string{
		"list:",
		"batch:open", "items:open", "histogram:open", "counts:open",
		"batch:completed", "items:completed", "histogram:completed", "counts:completed",
		"batch:cancelled", "items:cancelled", "histogram:cancelled", "counts:cancelled",
		"batch:ready", "items:ready", "histogram:ready", "counts:ready",
	}
	assertBatch47BackmarkCalls(t, store.calls, wantCalls)
	assertBackmarkReadScopeAndDefaultPages(t, ctx, store.calls)
}

func TestBatch47BackmarkReaderEmptyAndNilService(t *testing.T) {
	if reader := NewBackmarkReader(nil); reader != nil {
		t.Fatalf("nil service reader=%#v", reader)
	}
	store := &batch47BackmarkStore{}
	summary, err := NewBackmarkReader(backmark.NewService(store)).GetBackmarkSummary(context.Background(), "tenant-1", "exam-1", "question-1")
	if err != nil || !reflect.DeepEqual(summary, BackmarkSummary{}) {
		t.Fatalf("empty summary=%#v err=%v", summary, err)
	}
	assertBatch47BackmarkCalls(t, store.calls, []string{"list:"})
}

func TestBatch47BackmarkReaderDiscardsPartialProjectionAtFirstError(t *testing.T) {
	failure := errors.New("batch47 backing read failed")
	cases := []struct {
		name   string
		failAt string
		err    error
		want   []string
	}{
		{name: "list", failAt: "list:", err: failure, want: []string{"list:"}},
		{name: "missing_batch", failAt: "batch:a", err: backmark.ErrNotFound, want: []string{"list:", "batch:a"}},
		{name: "second_batch", failAt: "batch:b", err: failure, want: []string{"list:", "batch:a", "items:a", "histogram:a", "counts:a", "batch:b"}},
		{name: "second_items", failAt: "items:b", err: failure, want: []string{"list:", "batch:a", "items:a", "histogram:a", "counts:a", "batch:b", "items:b"}},
		{name: "second_histogram", failAt: "histogram:b", err: failure, want: []string{"list:", "batch:a", "items:a", "histogram:a", "counts:a", "batch:b", "items:b", "histogram:b"}},
		{name: "second_counts", failAt: "counts:b", err: failure, want: []string{"list:", "batch:a", "items:a", "histogram:a", "counts:a", "batch:b", "items:b", "histogram:b", "counts:b"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// 第一批已经贡献统计；第二批失败仍必须返回零投影，并停止读取第三批。
			store := &batch47BackmarkStore{
				batches: []backmark.Batch{{ID: "a", Status: backmark.BatchOpen}, {ID: "b", Status: backmark.BatchOpen}, {ID: "c", Status: backmark.BatchOpen}},
				items:   map[string][]backmark.Item{"a": {{Status: backmark.ItemDiffReady, Diff: floatPointer(1)}}},
				failAt:  test.failAt,
				failure: test.err,
			}
			summary, err := NewBackmarkReader(backmark.NewService(store)).GetBackmarkSummary(context.Background(), "tenant-1", "exam-1", "question-1")
			if err != test.err || !reflect.DeepEqual(summary, BackmarkSummary{}) {
				t.Fatalf("summary=%#v err=%v, want original error=%v and zero projection", summary, err, test.err)
			}
			assertBatch47BackmarkCalls(t, store.calls, test.want)
		})
	}
}

func TestBatch47DashboardUsesRealBackmarkReader(t *testing.T) {
	store := &batch47BackmarkStore{batches: []backmark.Batch{{ID: "a", Status: backmark.BatchOpen}}}
	service := NewService(Sources{
		Questions: staticQuestions{{ID: "question-1", QuestionNo: "Q1", RiskTier: "R2"}},
		Backmark:  NewBackmarkReader(backmark.NewService(store)),
	})
	dashboard, err := service.Get(context.Background(), "tenant-1", "exam-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Questions) != 1 || dashboard.Questions[0].Backmark.OpenBatches != 1 {
		t.Fatalf("dashboard=%#v", dashboard)
	}
	found := false
	for _, finding := range dashboard.Questions[0].Findings {
		if finding.Code == "backmark_pending" {
			found = true
		}
	}
	if !found {
		t.Fatalf("open batch has no backmark_pending finding: %#v", dashboard.Questions[0].Findings)
	}
	store.calls = nil
	store.failAt = "items:a"
	store.failure = errors.New("batch47 dashboard backing read failed")
	dashboard, err = service.Get(context.Background(), "tenant-1", "exam-1")
	if err != store.failure || !reflect.DeepEqual(dashboard, Dashboard{}) {
		t.Fatalf("dashboard=%#v err=%v, want zero dashboard and original error", dashboard, err)
	}
	assertBatch47BackmarkCalls(t, store.calls, []string{"list:", "batch:a", "items:a"})
}

func assertBatch47BackmarkCalls(t *testing.T, calls []batch47BackmarkCall, want []string) {
	t.Helper()
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, call.operation+":"+call.batchID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v, want %v", got, want)
	}
}

// 所有读操作沿用同一context和租户，列表与条目分页仍使用服务默认页长。
func assertBackmarkReadScopeAndDefaultPages(t *testing.T, ctx context.Context, calls []batch47BackmarkCall) {
	t.Helper()
	for _, call := range calls {
		if call.tenantID != "tenant-1" || call.ctx != ctx {
			t.Fatalf("scope/context changed: %#v", call)
		}
		if call.operation == "list" && (call.examID != "exam-1" || call.questionID != "question-1") {
			t.Fatalf("list scope changed: %#v", call)
		}
		if (call.operation == "list" || call.operation == "items") && call.page != (backmark.PageOptions{Limit: backmark.DefaultPageSize}) {
			t.Fatalf("default page changed: %#v", call)
		}
	}
}
