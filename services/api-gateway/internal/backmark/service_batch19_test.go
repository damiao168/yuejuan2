package backmark

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

type batch19SelectionStore struct {
	Store
	getBatch       func(context.Context, string, string) (Batch, error)
	listBatchItems func(context.Context, string, string, PageOptions) ([]Item, error)
}

func (s batch19SelectionStore) GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error) {
	return s.getBatch(ctx, tenantID, batchID)
}

func (s batch19SelectionStore) ListBatchItems(ctx context.Context, tenantID, batchID string, page PageOptions) ([]Item, error) {
	return s.listBatchItems(ctx, tenantID, batchID, page)
}

type batch19TaskSource func(context.Context, string, string) (review.ReviewTask, error)

func (s batch19TaskSource) GetTask(ctx context.Context, tenantID, taskID string) (review.ReviewTask, error) {
	return s(ctx, tenantID, taskID)
}

func TestBackmarkBatch19RegradeSelectionUsesWholePageCursorAndExactIDs(t *testing.T) {
	now := time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
	pages := backmarkSelectionCursorPages(now)
	submissions := map[string]string{
		pages[0][0].ReviewTaskID: "b",
		pages[1][0].ReviewTaskID: "b",
		pages[1][1].ReviewTaskID: " b ",
		pages[1][2].ReviewTaskID: "a",
	}
	var requested []PageOptions
	store := batch19SelectionStore{
		getBatch: func(_ context.Context, tenantID, batchID string) (Batch, error) {
			if tenantID != "tenant-1" || batchID != "batch-1" {
				t.Fatalf("batch scope = %q/%q", tenantID, batchID)
			}
			return Batch{ID: batchID, Status: BatchReadyForConfirmation}, nil
		},
		listBatchItems: func(_ context.Context, tenantID, batchID string, page PageOptions) ([]Item, error) {
			if tenantID != " tenant-1 " || batchID != " batch-1 " {
				t.Fatalf("item scope = %q/%q", tenantID, batchID)
			}
			requested = append(requested, page)
			if len(requested) > len(pages) {
				return nil, nil
			}
			return pages[len(requested)-1], nil
		},
	}
	var taskIDs []string
	tasks := batch19TaskSource(func(_ context.Context, tenantID, taskID string) (review.ReviewTask, error) {
		if tenantID != " tenant-1 " {
			t.Fatalf("task scope = %q", tenantID)
		}
		taskIDs = append(taskIDs, taskID)
		return review.ReviewTask{SubmissionID: submissions[taskID]}, nil
	})
	batch, ids, err := NewService(store).WithTaskSource(tasks).RegradeSelection(context.Background(), " tenant-1 ", " batch-1 ")
	if err != nil || batch.ID != "batch-1" || !reflect.DeepEqual(ids, []string{" b ", "a", "b"}) {
		t.Fatalf("batch=%#v ids=%#v err=%v", batch, ids, err)
	}
	// 两页末尾都不是重评项；第二页恰好满页后，还须读取空页再结束。
	last0, last1 := pages[0][MaxPageSize-1], pages[1][MaxPageSize-1]
	wantPages := []PageOptions{
		{Limit: MaxPageSize},
		{Limit: MaxPageSize, CursorCreatedAt: last0.CreatedAt, CursorID: last0.ID},
		{Limit: MaxPageSize, CursorCreatedAt: last1.CreatedAt, CursorID: last1.ID},
	}
	wantTasks := []string{pages[0][0].ReviewTaskID, pages[1][0].ReviewTaskID, pages[1][1].ReviewTaskID, pages[1][2].ReviewTaskID}
	if !reflect.DeepEqual(requested, wantPages) || !reflect.DeepEqual(taskIDs, wantTasks) {
		t.Fatalf("pages=%#v tasks=%#v", requested, taskIDs)
	}
}

func TestBackmarkBatch19RegradeSelectionKeepsFailureOrder(t *testing.T) {
	batchFailure, listFailure, taskFailure := errors.New("batch failure"), errors.New("list failure"), errors.New("task failure")
	items := []Item{
		{ReviewTaskID: "first", Status: ItemRegradeRequired},
		{ReviewTaskID: "second", Status: ItemRegradeRequired},
		{ReviewTaskID: "third", Status: ItemRegradeRequired},
	}
	for _, test := range []struct {
		name         string
		batchStatus  string
		batchError   error
		listError    error
		taskError    error
		submissionID string
		items        []Item
		withoutTasks bool
		wantError    error
		wantCalls    []string
	}{
		{name: "missing task source", withoutTasks: true, wantError: ErrInvalidInput},
		{name: "batch error first", batchError: batchFailure, listError: listFailure, wantError: batchFailure, wantCalls: []string{"batch"}},
		{name: "incomplete batch", listError: listFailure, wantError: ErrStateConflict, wantCalls: []string{"batch"}},
		{name: "list error before task", batchStatus: BatchReadyForConfirmation, listError: listFailure, taskError: taskFailure, items: items, wantError: listFailure, wantCalls: []string{"batch", "list"}},
		{name: "task error discards partial result", batchStatus: BatchReadyForConfirmation, items: items, taskError: taskFailure, wantError: taskFailure, wantCalls: []string{"batch", "list", "task:first", "task:second"}},
		{name: "empty submission stops reads", batchStatus: BatchReadyForConfirmation, items: items, submissionID: " \t ", wantError: ErrStateConflict, wantCalls: []string{"batch", "list", "task:first", "task:second"}},
		{name: "no regrade items", batchStatus: BatchReadyForConfirmation, items: []Item{{Status: ItemDiffReady}}, wantError: ErrNoRegradeItems, wantCalls: []string{"batch", "list"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			store := batch19SelectionStore{
				getBatch: func(context.Context, string, string) (Batch, error) {
					calls = append(calls, "batch")
					return Batch{ID: "batch-1", Status: test.batchStatus}, test.batchError
				},
				listBatchItems: func(context.Context, string, string, PageOptions) ([]Item, error) {
					calls = append(calls, "list")
					return test.items, test.listError
				},
			}
			service := NewService(store)
			if !test.withoutTasks {
				service.WithTaskSource(batch19TaskSource(func(_ context.Context, _ string, taskID string) (review.ReviewTask, error) {
					calls = append(calls, "task:"+taskID)
					if taskID == "first" {
						return review.ReviewTask{SubmissionID: "first-submission"}, nil
					}
					return review.ReviewTask{SubmissionID: test.submissionID}, test.taskError
				}))
			}
			batch, ids, err := service.RegradeSelection(context.Background(), "tenant-1", "batch-1")
			if !errors.Is(err, test.wantError) || batch.ID != "" || ids != nil || !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatalf("batch=%#v ids=%#v err=%v calls=%#v", batch, ids, err, calls)
			}
		})
	}
}

func TestBackmarkBatch19FrozenRubricKeepsExactIDsAndTolerance(t *testing.T) {
	point := func(id string, score float64) paper.RubricPoint { return paper.RubricPoint{ID: id, Score: score} }
	for _, test := range []struct {
		name       string
		points     []paper.RubricPoint
		selections []RubricSelection
		score      float64
		maxScore   float64
		noRubric   bool
		want       bool
	}{
		{name: "required point may earn zero", points: []paper.RubricPoint{{ID: "p", Score: 1, Required: true}}, selections: []RubricSelection{{PointID: "p", Score: 0}}, maxScore: 1, want: true},
		{name: "blank frozen point", points: []paper.RubricPoint{point(" ", 1)}, maxScore: 1},
		{name: "negative frozen point", points: []paper.RubricPoint{point("p", -1)}, maxScore: 1},
		{name: "nan frozen point", points: []paper.RubricPoint{point("p", math.NaN())}, maxScore: 1},
		{name: "infinite frozen point", points: []paper.RubricPoint{point("p", math.Inf(1))}, maxScore: 1},
		{name: "duplicate original id", points: []paper.RubricPoint{point("p", 1), point("p", 1)}, maxScore: 2},
		{name: "whitespace ids stay distinct", points: []paper.RubricPoint{point("p", 1), point(" p ", 1)}, selections: []RubricSelection{{PointID: "p", Score: 0.5}, {PointID: " p ", Score: 0.5}}, score: 1, maxScore: 2, want: true},
		{name: "selection id is not trimmed", points: []paper.RubricPoint{point(" p ", 1)}, selections: []RubricSelection{{PointID: "p", Score: 1}}, score: 1, maxScore: 1},
		{name: "inclusive absolute tolerance", points: []paper.RubricPoint{point("p", 1)}, selections: []RubricSelection{{PointID: "p", Score: 0.0001}}, maxScore: 1, want: true},
		{name: "just outside tolerance", points: []paper.RubricPoint{point("p", 1)}, selections: []RubricSelection{{PointID: "p", Score: math.Nextafter(0.0001, math.Inf(1))}}, maxScore: 1},
		{name: "nan rubric maximum", maxScore: math.NaN()},
		{name: "empty rubric compatibility", noRubric: true, score: 1, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rubric := paper.Rubric{ID: "frozen", MaxScore: test.maxScore, Points: test.points}
			if test.noRubric {
				rubric.ID = ""
			}
			input := SubmitInput{Score: test.score, RubricSelections: test.selections}
			if got := validFrozenSelections(input, rubric); got != test.want {
				t.Fatalf("validFrozenSelections=%t want=%t", got, test.want)
			}
		})
	}
}

func TestBackmarkBatch19SelectorKeepsOptionalAndFiniteBounds(t *testing.T) {
	value := func(number float64) *float64 { return &number }
	from := time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
	before := from.Add(-time.Second)
	for _, test := range []struct {
		name     string
		selector Selector
		want     bool
	}{
		{name: "omitted bounds", want: true},
		{name: "empty band", selector: Selector{ScoreBand: &ScoreBand{}}, want: true},
		{name: "minimum only", selector: Selector{ScoreBand: &ScoreBand{Min: value(0)}}, want: true},
		{name: "maximum only", selector: Selector{ScoreBand: &ScoreBand{Max: value(1)}}, want: true},
		{name: "equal bounds", selector: Selector{ScoreBand: &ScoreBand{Min: value(1), Max: value(1)}}, want: true},
		{name: "inverted bounds", selector: Selector{ScoreBand: &ScoreBand{Min: value(2), Max: value(1)}}},
		{name: "negative minimum", selector: Selector{ScoreBand: &ScoreBand{Min: value(-1)}}},
		{name: "negative maximum", selector: Selector{ScoreBand: &ScoreBand{Max: value(-1)}}},
		{name: "nan minimum", selector: Selector{ScoreBand: &ScoreBand{Min: value(math.NaN())}}},
		{name: "infinite maximum", selector: Selector{ScoreBand: &ScoreBand{Max: value(math.Inf(1))}}},
		{name: "equal timestamps", selector: Selector{TimeRange: &TimeRange{From: &from, To: &from}}, want: true},
		{name: "inverted timestamps", selector: Selector{TimeRange: &TimeRange{From: &from, To: &before}}},
		{name: "blank task id", selector: Selector{TaskIDs: []string{" \t "}}},
		{name: "task ids not normalized during validation", selector: Selector{TaskIDs: []string{" task ", "task"}}, want: true},
		{name: "task count checked before normalization", selector: Selector{TaskIDs: make([]string, MaxSelectorTaskIDs+1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validSelector(test.selector); got != test.want {
				t.Fatalf("validSelector=%t want=%t", got, test.want)
			}
		})
	}
}

// 两页都是满页且末项不需重评，用于暴露错误地用筛选后末项推进游标的实现。
func backmarkSelectionCursorPages(now time.Time) [][]Item {
	pages := make([][]Item, 2)
	for pageIndex := range pages {
		pages[pageIndex] = make([]Item, MaxPageSize)
		for index := range pages[pageIndex] {
			id := fmt.Sprintf("item-%d-%04d", pageIndex, index)
			pages[pageIndex][index] = Item{ID: id, ReviewTaskID: id, CreatedAt: now, Status: ItemDiffReady}
		}
	}
	pages[0][0].Status = ItemRegradeRequired
	for index := 0; index < 3; index++ {
		pages[1][index].Status = ItemRegradeRequired
	}
	return pages
}
