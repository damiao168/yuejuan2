package backmark

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore 用于处理器和单元测试；来源事实经 SeedSource 注入，并在创建批次时复制到批次中。
type MemoryStore struct {
	mu       sync.Mutex
	now      func() time.Time
	sequence int
	sources  map[string]SourceTask
	batches  map[string]Batch
	items    map[string]Item
	grades   map[string]Grade
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		now: func() time.Time { return time.Now().UTC() }, sources: map[string]SourceTask{}, batches: map[string]Batch{},
		items: map[string]Item{}, grades: map[string]Grade{},
	}
}

func (s *MemoryStore) SeedSource(source SourceTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[source.ReviewTaskID] = source
}

func (s *MemoryStore) SelectSourceTasks(_ context.Context, _ string, _, _ string, selector Selector, limit int) ([]SourceTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := make([]SourceTask, 0, len(s.sources))
	for _, source := range s.sources {
		if matchesSource(source, selector) {
			selected = append(selected, source)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ReviewTaskID < selected[j].ReviewTaskID })
	if limit > 0 && len(selected) > limit {
		selected = selected[:limit]
	}
	return selected, nil
}

func (s *MemoryStore) Preview(ctx context.Context, tenantID, examID, questionID string, selector Selector) (Preview, error) {
	items, err := s.SelectSourceTasks(ctx, tenantID, examID, questionID, selector, 0)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{AffectedCount: len(items)}
	if len(items) == 0 {
		return preview, nil
	}
	counts := map[float64]int{}
	first, last := items[0].GradedAt, items[0].GradedAt
	for _, item := range items {
		counts[item.OriginalScore]++
		if item.GradedAt.Before(first) {
			first = item.GradedAt
		}
		if item.GradedAt.After(last) {
			last = item.GradedAt
		}
	}
	preview.TimeRange = TimeRange{From: &first, To: &last}
	for score, count := range counts {
		preview.ScoreBands = append(preview.ScoreBands, Band{Score: score, Count: count})
	}
	sort.Slice(preview.ScoreBands, func(i, j int) bool { return preview.ScoreBands[i].Score < preview.ScoreBands[j].Score })
	return preview, nil
}

func (s *MemoryStore) CreateBatch(_ context.Context, _ string, examID, questionID, actorID string, input CreateInput, sources []SourceTask) (Batch, []Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	batch := Batch{ID: s.id("backmark-batch"), ExamID: examID, QuestionID: questionID, SourceIncidentID: input.SourceIncidentID,
		Selector: input.Selector, Policy: input.Policy, AffectedCount: len(sources), Status: BatchOpen, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	s.batches[batch.ID] = batch
	items := make([]Item, 0, len(sources))
	for _, source := range sources {
		item := Item{ID: s.id("backmark-item"), BatchID: batch.ID, ReviewTaskID: source.ReviewTaskID, OriginalGradeID: source.OriginalGradeID,
			OriginalReviewer: source.OriginalReviewer, ReassignedTo: input.ReassignedTo, OriginalScore: source.OriginalScore,
			MaxScore: source.MaxScore, Status: ItemPending, Revision: 1, CreatedAt: now, UpdatedAt: now}
		s.items[item.ID] = item
		items = append(items, cloneItem(item))
	}
	return cloneBatch(batch), items, nil
}

func (s *MemoryStore) GetBatch(_ context.Context, _ string, batchID string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[batchID]
	if !ok {
		return Batch{}, ErrNotFound
	}
	return cloneBatch(batch), nil
}

func (s *MemoryStore) ListBatchItems(_ context.Context, _ string, batchID string, page PageOptions) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.batches[batchID]; !ok {
		return nil, ErrNotFound
	}
	items := s.itemsForBatchLocked(batchID)
	if page.Status != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.Status == page.Status {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	return pageItems(items, page, false), nil
}

func (s *MemoryStore) GetStatusCounts(_ context.Context, _ string, batchID string) (StatusCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.batches[batchID]; !ok {
		return StatusCounts{}, ErrNotFound
	}
	return statusCounts(s.itemsForBatchLocked(batchID)), nil
}

func (s *MemoryStore) GetHistogram(_ context.Context, _ string, batchID string) ([]Histogram, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.batches[batchID]; !ok {
		return nil, ErrNotFound
	}
	return histogram(s.itemsForBatchLocked(batchID)), nil
}

// ListBatches 在同一锁内复制批次，再按时间和 ID 降序分页。
// 内存夹具的 Batch 没有租户字段，这里只执行 exam/question 条件。
func (s *MemoryStore) ListBatches(ctx context.Context, tenantID string, examID, questionID string, page PageOptions) ([]Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Batch{}
	for _, batch := range s.batches {
		allowed, err := page.Access.Allows(ctx, tenantID, "backmark_batch", batch.ID, batch.ExamID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if (examID == "" || batch.ExamID == examID) && (questionID == "" || batch.QuestionID == questionID) {
			out = append(out, cloneBatch(batch))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID > out[j].ID)
	})
	out = batchesFollowingCursor(out, page)
	if len(out) > page.Limit {
		out = out[:page.Limit]
	}
	return out, nil
}

func batchesFollowingCursor(batches []Batch, page PageOptions) []Batch {
	if page.CursorCreatedAt.IsZero() {
		return batches
	}
	// 切片已按时间和 ID 降序排列；只保留严格位于游标之后的本地副本。
	filtered := batches[:0]
	for _, batch := range batches {
		if batch.CreatedAt.Before(page.CursorCreatedAt) || (batch.CreatedAt.Equal(page.CursorCreatedAt) && batch.ID < page.CursorID) {
			filtered = append(filtered, batch)
		}
	}
	return filtered
}

func (s *MemoryStore) ListAssigned(_ context.Context, _ string, graderID string, page PageOptions) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Item{}
	for _, item := range s.items {
		if item.ReassignedTo == graderID && (item.Status == ItemPending || item.Status == ItemInProgress) {
			out = append(out, cloneItem(item))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return pageItems(out, page, false), nil
}

func (s *MemoryStore) GetAssigned(_ context.Context, _ string, itemID, graderID string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	if !ok {
		return Item{}, ErrNotFound
	}
	if item.ReassignedTo != graderID {
		return Item{}, ErrAssigneeForbidden
	}
	return cloneItem(item), nil
}

func (s *MemoryStore) Claim(_ context.Context, _ string, itemID, graderID string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	if !ok {
		return Item{}, ErrNotFound
	}
	if item.ReassignedTo != graderID {
		return Item{}, ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return Item{}, ErrOriginalGrader
	}
	if item.Status != ItemPending && item.Status != ItemInProgress {
		return Item{}, ErrStateConflict
	}
	if item.Status == ItemPending {
		item.Status, item.Revision, item.UpdatedAt = ItemInProgress, item.Revision+1, s.now().UTC()
		s.items[item.ID] = item
		s.refreshBatchStatusLocked(item.BatchID)
	}
	return cloneItem(item), nil
}

func (s *MemoryStore) Submit(_ context.Context, _ string, itemID, graderID string, input SubmitInput) (Item, Grade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemID]
	if !ok {
		return Item{}, Grade{}, ErrNotFound
	}
	if item.ReassignedTo != graderID {
		return Item{}, Grade{}, ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return Item{}, Grade{}, ErrOriginalGrader
	}
	if item.Status != ItemInProgress {
		return Item{}, Grade{}, ErrStateConflict
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, Grade{}, ErrRevisionConflict
	}
	if input.Score > item.MaxScore {
		return Item{}, Grade{}, ErrInvalidInput
	}
	batch := s.batches[item.BatchID]
	now := s.now().UTC()
	grade := Grade{ID: s.id("backmark-grade"), BackmarkItemID: item.ID, ReviewerID: graderID, Score: input.Score, MaxScore: item.MaxScore,
		RubricSelections: append([]RubricSelection(nil), input.RubricSelections...), Comments: input.Comments, CreatedAt: now}
	diff := input.Score - item.OriginalScore
	item.NewGradeID, item.NewScore, item.Diff = grade.ID, &grade.Score, &diff
	item.Status = nextItemStatus(batch.Policy, diff)
	item.Revision, item.UpdatedAt = item.Revision+1, now
	s.grades[grade.ID], s.items[item.ID] = grade, item
	s.refreshBatchStatusLocked(item.BatchID)
	return cloneItem(item), cloneGrade(grade), nil
}

func (s *MemoryStore) refreshBatchStatusLocked(batchID string) {
	batch, ok := s.batches[batchID]
	if !ok || batch.Status == BatchCancelled {
		return
	}
	items := s.itemsForBatchLocked(batchID)
	allDone, anyStarted := len(items) > 0, false
	for _, item := range items {
		if item.Status == ItemPending || item.Status == ItemInProgress {
			allDone = false
		}
		if item.Status != ItemPending {
			anyStarted = true
		}
	}
	if allDone {
		batch.Status = BatchReadyForConfirmation
	} else if anyStarted {
		batch.Status = BatchInProgress
	} else {
		batch.Status = BatchOpen
	}
	batch.UpdatedAt = s.now().UTC()
	s.batches[batchID] = batch
}

func (s *MemoryStore) itemsForBatchLocked(batchID string) []Item {
	out := []Item{}
	for _, item := range s.items {
		if item.BatchID == batchID {
			out = append(out, cloneItem(item))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out
}

func pageItems(items []Item, page PageOptions, descending bool) []Item {
	out := make([]Item, 0, min(len(items), page.Limit))
	for _, item := range items {
		if !page.CursorCreatedAt.IsZero() {
			before := item.CreatedAt.Before(page.CursorCreatedAt) || (item.CreatedAt.Equal(page.CursorCreatedAt) && item.ID < page.CursorID)
			after := item.CreatedAt.After(page.CursorCreatedAt) || (item.CreatedAt.Equal(page.CursorCreatedAt) && item.ID > page.CursorID)
			if (!descending && !after) || (descending && !before) {
				continue
			}
		}
		out = append(out, item)
		if len(out) == page.Limit {
			break
		}
	}
	return out
}

func (s *MemoryStore) id(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}

func matchesSource(source SourceTask, selector Selector) bool {
	if !selectorIncludesSourceTask(source.ReviewTaskID, selector.TaskIDs) {
		return false
	}
	if selector.GraderID != "" && selector.GraderID != source.OriginalReviewer {
		return false
	}
	if !sourceWithinTimeRange(source.GradedAt, selector.TimeRange) {
		return false
	}
	return sourceWithinScoreBand(source.OriginalScore, selector.ScoreBand)
}

func selectorIncludesSourceTask(taskID string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == taskID {
			return true
		}
	}
	return false
}

func sourceWithinTimeRange(gradedAt time.Time, bounds *TimeRange) bool {
	if bounds == nil {
		return true
	}
	// 时间端点包含在集合内；空端点只取消对应方向的限制。
	if bounds.From != nil && gradedAt.Before(*bounds.From) {
		return false
	}
	if bounds.To != nil && gradedAt.After(*bounds.To) {
		return false
	}
	return true
}

func sourceWithinScoreBand(score float64, band *ScoreBand) bool {
	if band == nil {
		return true
	}
	// 分数端点同样包含在集合内；不在 matcher 中改写上层传入的 selector。
	if band.Min != nil && score < *band.Min {
		return false
	}
	if band.Max != nil && score > *band.Max {
		return false
	}
	return true
}

func nextItemStatus(policy Policy, diff float64) string {
	if diff == 0 {
		return ItemDiffReady
	}
	switch policy.Disposition {
	case DispositionArbitrate:
		if abs(diff) >= policy.ArbitrationDelta {
			return ItemArbitrationRequired
		}
	case DispositionRegrade:
		return ItemRegradeRequired
	}
	return ItemDiffReady
}
func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
func cloneItem(value Item) Item {
	if value.NewScore != nil {
		score := *value.NewScore
		value.NewScore = &score
	}
	if value.Diff != nil {
		diff := *value.Diff
		value.Diff = &diff
	}
	return value
}
func cloneBatch(value Batch) Batch {
	value.Selector.TaskIDs = append([]string(nil), value.Selector.TaskIDs...)
	return value
}
func cloneGrade(value Grade) Grade {
	value.RubricSelections = append([]RubricSelection(nil), value.RubricSelections...)
	return value
}
