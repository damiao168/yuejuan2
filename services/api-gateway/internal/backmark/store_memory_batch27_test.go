package backmark

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBatch27ListBatchesKeepsScopeStrictCursorAndLimit(t *testing.T) {
	store := NewMemoryStore()
	created := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	for id, at := range map[string]time.Time{"z": created.Add(time.Hour), "c": created, "b": created, "a": created, "o": created.Add(-time.Hour)} {
		store.batches[id] = Batch{ID: id, ExamID: "exam-1", QuestionID: "question-1", CreatedAt: at}
	}
	store.batches["other-exam"] = Batch{ID: "other-exam", ExamID: "exam-2", QuestionID: "question-1", CreatedAt: created.Add(2 * time.Hour)}
	store.batches["other-question"] = Batch{ID: "other-question", ExamID: "exam-1", QuestionID: "question-2", CreatedAt: created.Add(3 * time.Hour)}
	for _, tc := range []struct {
		name     string
		exam     string
		question string
		page     PageOptions
		want     []string
	}{
		{name: "first narrowed page", exam: "exam-1", question: "question-1", page: PageOptions{Limit: 3}, want: []string{"z", "c", "b"}},
		{name: "limit follows strict cursor", exam: "exam-1", question: "question-1", page: PageOptions{Limit: 2, CursorCreatedAt: created, CursorID: "c"}, want: []string{"b", "a"}},
		{name: "cursor may identify absent item", exam: "exam-1", question: "question-1", page: PageOptions{Limit: 10, CursorCreatedAt: created, CursorID: "bb"}, want: []string{"b", "a", "o"}},
		{name: "cursor item is excluded", exam: "exam-1", question: "question-1", page: PageOptions{Limit: 10, CursorCreatedAt: created.Add(-time.Hour), CursorID: "o"}, want: []string{}},
		{name: "exam condition", exam: "exam-2", page: PageOptions{Limit: 10}, want: []string{"other-exam"}},
		{name: "question condition", question: "question-2", page: PageOptions{Limit: 10}, want: []string{"other-question"}},
		{name: "empty optional scope", page: PageOptions{Limit: 10}, want: []string{"other-question", "other-exam", "z", "c", "b", "a", "o"}},
		{name: "direct zero limit stays empty", exam: "exam-1", question: "question-1", want: []string{}},
		{name: "direct ID alone does not enable cursor", exam: "exam-1", question: "question-1", page: PageOptions{Limit: 2, CursorID: "c"}, want: []string{"z", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.ListBatches(context.Background(), "tenant-1", tc.exam, tc.question, tc.page)
			ids := make([]string, 0, len(got))
			for _, batch := range got {
				ids = append(ids, batch.ID)
			}
			if err != nil || got == nil || !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("batch IDs=%v want=%v nil=%t err=%v", ids, tc.want, got == nil, err)
			}
		})
	}
}

func TestBatch27SelectSourceTasksKeepsOptionalInclusiveConditions(t *testing.T) {
	store := NewMemoryStore()
	created := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	from, to := created, created.Add(3*time.Hour)
	for _, source := range []SourceTask{
		{ReviewTaskID: "a", OriginalReviewer: "grader-1", OriginalScore: 1, GradedAt: created},
		{ReviewTaskID: "b", OriginalReviewer: "grader-1", OriginalScore: 2, GradedAt: created.Add(time.Hour)},
		{ReviewTaskID: "c", OriginalReviewer: "grader-2", OriginalScore: 3, GradedAt: created.Add(2 * time.Hour)},
		{ReviewTaskID: "d", OriginalReviewer: "grader-1", OriginalScore: 0, GradedAt: created.Add(-time.Hour)},
		{ReviewTaskID: "e", OriginalReviewer: "grader-1", OriginalScore: 4, GradedAt: to},
		{ReviewTaskID: " a ", OriginalReviewer: "grader-1", OriginalScore: 1, GradedAt: created},
	} {
		store.SeedSource(source)
	}
	all := []string{" a ", "a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		name     string
		selector Selector
		limit    int
		want     []string
	}{
		{name: "omitted conditions", want: all},
		{name: "empty optional ranges", selector: Selector{TimeRange: &TimeRange{}, ScoreBand: &ScoreBand{}}, want: all},
		{name: "explicit IDs use any match", selector: Selector{TaskIDs: []string{"b", "a", "b"}}, want: []string{"a", "b"}},
		{name: "direct selector keeps exact spaced ID", selector: Selector{TaskIDs: []string{" a "}}, want: []string{" a "}},
		{name: "direct selector does not trim ID", selector: Selector{TaskIDs: []string{" a"}}, want: []string{}},
		{name: "grader condition", selector: Selector{GraderID: "grader-2"}, want: []string{"c"}},
		{name: "inclusive lower time endpoint", selector: Selector{TimeRange: &TimeRange{From: &from}}, want: []string{" a ", "a", "b", "c", "e"}},
		{name: "inclusive upper time endpoint", selector: Selector{TimeRange: &TimeRange{To: &from}}, want: []string{" a ", "a", "d"}},
		{name: "equal time endpoints", selector: Selector{TimeRange: &TimeRange{From: &from, To: &from}}, want: []string{" a ", "a"}},
		{name: "inclusive score minimum", selector: Selector{ScoreBand: &ScoreBand{Min: batch27Score(2)}}, want: []string{"b", "c", "e"}},
		{name: "inclusive score maximum", selector: Selector{ScoreBand: &ScoreBand{Max: batch27Score(1)}}, want: []string{" a ", "a", "d"}},
		{name: "equal score endpoints", selector: Selector{ScoreBand: &ScoreBand{Min: batch27Score(2), Max: batch27Score(2)}}, want: []string{"b"}},
		{name: "all supplied conditions are AND", selector: Selector{TaskIDs: []string{"a", "b", "c", "d", "e"}, GraderID: "grader-1", TimeRange: &TimeRange{From: &from, To: &to}, ScoreBand: &ScoreBand{Min: batch27Score(2), Max: batch27Score(4)}}, want: []string{"b", "e"}},
		{name: "one condition can exclude match", selector: Selector{GraderID: "grader-2", ScoreBand: &ScoreBand{Max: batch27Score(2)}}, want: []string{}},
		{name: "limit follows ID sort", selector: Selector{TaskIDs: []string{"e", "a", "b"}}, limit: 2, want: []string{"a", "b"}},
		{name: "negative limit stays unlimited", limit: -1, want: all},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 直接 Store 路径保留原 selector；Service 的去空格和去重不能移入 matcher。
			got, err := store.SelectSourceTasks(context.Background(), "tenant-1", "exam-1", "question-1", tc.selector, tc.limit)
			ids := make([]string, 0, len(got))
			for _, source := range got {
				ids = append(ids, source.ReviewTaskID)
			}
			if err != nil || got == nil || !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("source IDs=%v want=%v nil=%t err=%v", ids, tc.want, got == nil, err)
			}
		})
	}
}

func batch27Score(score float64) *float64 {
	return &score
}
