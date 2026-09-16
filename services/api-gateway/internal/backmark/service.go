package backmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

type ContextSource interface {
	GetTaskContext(context.Context, string, string) (review.TaskContext, error)
}

// TaskSource is intentionally narrower than the normal review handler.  It
// is used only by a manager-triggered hand-off to A19 to turn completed
// backmark items into the immutable source-release submission filter.  It is
// never exposed to the independent backmark grader surface.
type TaskSource interface {
	GetTask(context.Context, string, string) (review.ReviewTask, error)
}

type Service struct {
	store   Store
	context ContextSource
	tasks   TaskSource
}

func NewService(store Store) *Service { return &Service{store: store} }

// WithContextSource gives the back-mark queue a narrow read-only bridge to
// the original task facts. It never calls review submission APIs.
func (s *Service) WithContextSource(source ContextSource) *Service {
	s.context = source
	return s
}

func (s *Service) WithTaskSource(source TaskSource) *Service {
	s.tasks = source
	return s
}

func (s *Service) Preview(ctx context.Context, tenantID, examID, questionID string, selector Selector) (Preview, error) {
	if !validScope(tenantID, examID, questionID) || !validSelector(selector) {
		return Preview{}, ErrInvalidInput
	}
	selector = normalizeSelector(selector)
	sources, err := s.store.SelectSourceTasks(ctx, tenantID, examID, questionID, selector, MaxSynchronousItems+1)
	if err != nil {
		return Preview{}, err
	}
	if len(sources) > MaxSynchronousItems {
		return s.store.Preview(ctx, tenantID, examID, questionID, selector)
	}
	return previewSources(tenantID, examID, questionID, selector, sources), nil
}

func (s *Service) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput) (Summary, error) {
	if !validScope(tenantID, examID, questionID) || strings.TrimSpace(actorID) == "" || !validCreate(input) {
		return Summary{}, ErrInvalidInput
	}
	input.SourceIncidentID = strings.TrimSpace(input.SourceIncidentID)
	input.ReassignedTo = strings.TrimSpace(input.ReassignedTo)
	input.Selector = normalizeSelector(input.Selector)
	input.Policy = normalizePolicy(input.Policy)
	sources, err := s.store.SelectSourceTasks(ctx, tenantID, examID, questionID, input.Selector, MaxSynchronousItems+1)
	if err != nil {
		return Summary{}, err
	}
	if input.SelectorHash != "" && input.SelectorHash != sourceHash(tenantID, examID, questionID, input.Selector, sources) {
		return Summary{}, ErrPreviewStale
	}
	if len(sources) == 0 {
		return Summary{}, ErrNoAffectedTasks
	}
	if len(sources) > MaxSynchronousItems {
		return Summary{}, ErrTooManyItems
	}
	for _, source := range sources {
		if source.OriginalReviewer == input.ReassignedTo {
			return Summary{}, ErrOriginalGrader
		}
	}
	batch, items, err := s.store.CreateBatch(ctx, tenantID, examID, questionID, actorID, input, sources)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Batch: batch, Items: items, Histogram: histogram(items), StatusCounts: statusCounts(items)}, nil
}

func (s *Service) Get(ctx context.Context, tenantID, batchID string) (Summary, error) {
	return s.GetPage(ctx, tenantID, batchID, PageOptions{Limit: DefaultPageSize})
}

func (s *Service) GetPage(ctx context.Context, tenantID, batchID string, page PageOptions) (Summary, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(batchID) == "" {
		return Summary{}, ErrInvalidInput
	}
	if !validPage(page) {
		return Summary{}, ErrInvalidInput
	}
	batch, err := s.store.GetBatch(ctx, tenantID, batchID)
	if err != nil {
		return Summary{}, err
	}
	items, err := s.store.ListBatchItems(ctx, tenantID, batchID, page)
	if err != nil {
		return Summary{}, err
	}
	histogram, err := s.store.GetHistogram(ctx, tenantID, batchID)
	if err != nil {
		return Summary{}, err
	}
	counts, err := s.store.GetStatusCounts(ctx, tenantID, batchID)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Batch: batch, Items: items, Histogram: histogram, StatusCounts: counts}, nil
}

// RegradeSelection returns exactly the submissions whose independently
// re-marked score crossed this batch's regrade policy.  The caller still has
// to select a published source release and create the A19 job explicitly;
// this method never changes a score or release by itself.
func (s *Service) RegradeSelection(ctx context.Context, tenantID, batchID string) (Batch, []string, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(batchID) == "" || s.tasks == nil {
		return Batch{}, nil, ErrInvalidInput
	}
	batch, err := s.store.GetBatch(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(batchID))
	if err != nil {
		return Batch{}, nil, err
	}
	// A partial batch cannot be a stable correction population.  Managers may
	// inspect its current diff, but must wait until every assigned item has
	// completed before entering the formal regrade workflow.
	if batch.Status != BatchReadyForConfirmation {
		return Batch{}, nil, ErrStateConflict
	}
	seen := make(map[string]struct{})
	submissionIDs := make([]string, 0)
	page := PageOptions{Limit: MaxPageSize}
	for {
		items, listErr := s.store.ListBatchItems(ctx, tenantID, batchID, page)
		if listErr != nil {
			return Batch{}, nil, listErr
		}
		for _, item := range items {
			if item.Status != ItemRegradeRequired {
				continue
			}
			task, getErr := s.tasks.GetTask(ctx, tenantID, item.ReviewTaskID)
			if getErr != nil {
				return Batch{}, nil, getErr
			}
			if strings.TrimSpace(task.SubmissionID) == "" {
				return Batch{}, nil, ErrStateConflict
			}
			if _, exists := seen[task.SubmissionID]; exists {
				continue
			}
			seen[task.SubmissionID] = struct{}{}
			submissionIDs = append(submissionIDs, task.SubmissionID)
		}
		if len(items) < page.Limit {
			break
		}
		last := items[len(items)-1]
		page.CursorCreatedAt, page.CursorID = last.CreatedAt, last.ID
	}
	if len(submissionIDs) == 0 {
		return Batch{}, nil, ErrNoRegradeItems
	}
	sort.Strings(submissionIDs)
	return batch, submissionIDs, nil
}

func (s *Service) List(ctx context.Context, tenantID, examID, questionID string) ([]Batch, error) {
	return s.ListPage(ctx, tenantID, examID, questionID, PageOptions{Limit: DefaultPageSize})
}

func (s *Service) ListPage(ctx context.Context, tenantID, examID, questionID string, page PageOptions) ([]Batch, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	if !validPage(page) {
		return nil, ErrInvalidInput
	}
	return s.store.ListBatches(ctx, tenantID, strings.TrimSpace(examID), strings.TrimSpace(questionID), page)
}

func (s *Service) Claim(ctx context.Context, tenantID, itemID, graderID string) (Item, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(itemID) == "" || strings.TrimSpace(graderID) == "" {
		return Item{}, ErrInvalidInput
	}
	return s.store.Claim(ctx, tenantID, itemID, graderID)
}

func (s *Service) ListAssigned(ctx context.Context, tenantID, graderID string) ([]GraderItem, error) {
	return s.ListAssignedPage(ctx, tenantID, graderID, PageOptions{Limit: DefaultPageSize})
}

func (s *Service) ListAssignedPage(ctx context.Context, tenantID, graderID string, page PageOptions) ([]GraderItem, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(graderID) == "" {
		return nil, ErrInvalidInput
	}
	if !validPage(page) {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListAssigned(ctx, tenantID, graderID, page)
	if err != nil {
		return nil, err
	}
	out := make([]GraderItem, 0, len(items))
	for _, item := range items {
		out = append(out, graderView(item))
	}
	return out, nil
}

func (s *Service) GetGraderContext(ctx context.Context, tenantID, itemID, graderID string) (GraderContext, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(itemID) == "" || strings.TrimSpace(graderID) == "" {
		return GraderContext{}, ErrInvalidInput
	}
	if s.context == nil {
		return GraderContext{}, ErrNotFound
	}
	item, err := s.store.GetAssigned(ctx, tenantID, itemID, graderID)
	if err != nil {
		return GraderContext{}, err
	}
	if item.ReassignedTo != graderID {
		return GraderContext{}, ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return GraderContext{}, ErrOriginalGrader
	}
	if item.Status != ItemPending && item.Status != ItemInProgress {
		return GraderContext{}, ErrStateConflict
	}
	contextValue, err := s.context.GetTaskContext(ctx, tenantID, item.ReviewTaskID)
	if err != nil {
		return GraderContext{}, err
	}
	question := contextValue.Question
	return GraderContext{
		Item:             graderView(item),
		ExpectedRevision: item.Revision,
		Question: GraderQuestion{
			ID: question.ID, QuestionNo: question.QuestionNo, QuestionType: question.QuestionType,
			Score: question.Score, Stem: question.Stem, KnowledgePoints: append([]string(nil), question.KnowledgePoints...),
		},
		FrozenRubric: contextValue.FrozenRubric,
		Answer: GraderAnswer{
			RawAnswer: contextValue.AnswerArtifact.RawAnswer, OCRText: contextValue.AnswerArtifact.OCRText,
			SegmentStatus:   contextValue.AnswerArtifact.Status,
			SegmentImageURL: "/api/v1/backmark-items/" + item.ID + "/segment-image",
		},
	}, nil
}

func (s *Service) GetSegmentID(ctx context.Context, tenantID, itemID, graderID string) (string, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(itemID) == "" || strings.TrimSpace(graderID) == "" {
		return "", ErrInvalidInput
	}
	if s.context == nil {
		return "", ErrNotFound
	}
	item, err := s.store.GetAssigned(ctx, tenantID, itemID, graderID)
	if err != nil {
		return "", err
	}
	if item.ReassignedTo != graderID {
		return "", ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return "", ErrOriginalGrader
	}
	if item.Status != ItemPending && item.Status != ItemInProgress {
		return "", ErrStateConflict
	}
	contextValue, err := s.context.GetTaskContext(ctx, tenantID, item.ReviewTaskID)
	if err != nil {
		return "", err
	}
	return contextValue.AnswerArtifact.AnswerSegmentID, nil
}

func (s *Service) Submit(ctx context.Context, tenantID, itemID, graderID string, input SubmitInput) (Item, Grade, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(itemID) == "" || strings.TrimSpace(graderID) == "" ||
		input.ExpectedRevision <= 0 || !finiteNonNegative(input.Score) || !validSelections(input.RubricSelections) {
		return Item{}, Grade{}, ErrInvalidInput
	}
	item, err := s.store.GetAssigned(ctx, tenantID, itemID, graderID)
	if err != nil {
		return Item{}, Grade{}, err
	}
	if item.ReassignedTo != graderID || item.OriginalReviewer == graderID {
		return Item{}, Grade{}, ErrAssigneeForbidden
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
	if s.context == nil {
		return Item{}, Grade{}, ErrNotFound
	}
	taskContext, err := s.context.GetTaskContext(ctx, tenantID, item.ReviewTaskID)
	if err != nil {
		return Item{}, Grade{}, err
	}
	if !validFrozenSelections(input, taskContext.FrozenRubric) {
		return Item{}, Grade{}, ErrInvalidInput
	}
	return s.store.Submit(ctx, tenantID, itemID, graderID, input)
}

// Match normal review scoring: a rubric-backed grade equals the sum of its
// awarded points. Required describes the rubric criterion, not a requirement
// that a student's answer earn positive credit for it.
func validFrozenSelections(input SubmitInput, rubric paper.Rubric) bool {
	hasRubric := rubric.ID != "" || len(rubric.Points) > 0
	if hasRubric && (!finiteNonNegative(rubric.MaxScore) || input.Score > rubric.MaxScore) {
		return false
	}
	allowed := make(map[string]float64, len(rubric.Points))
	for _, point := range rubric.Points {
		if strings.TrimSpace(point.ID) == "" || !finiteNonNegative(point.Score) {
			return false
		}
		if _, duplicate := allowed[point.ID]; duplicate {
			return false
		}
		allowed[point.ID] = point.Score
	}
	seen := make(map[string]bool, len(input.RubricSelections))
	sum := 0.0
	for _, selection := range input.RubricSelections {
		max, exists := allowed[selection.PointID]
		if !exists || seen[selection.PointID] || !finiteNonNegative(selection.Score) || selection.Score > max {
			return false
		}
		seen[selection.PointID] = true
		sum += selection.Score
	}
	return finiteNonNegative(sum) && (!hasRubric || math.Abs(sum-input.Score) <= 0.0001)
}

func sourceHash(tenantID, examID, questionID string, selector Selector, sources []SourceTask) string {
	ordered := append([]SourceTask{}, sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ReviewTaskID < ordered[j].ReviewTaskID })
	for i := range ordered {
		ordered[i].GradedAt = ordered[i].GradedAt.UTC()
	}
	encoded, err := json.Marshal(struct {
		TenantID, ExamID, QuestionID string
		Selector                     Selector
		Sources                      []SourceTask
	}{tenantID, examID, questionID, selector, ordered})
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func previewSources(tenantID, examID, questionID string, selector Selector, sources []SourceTask) Preview {
	preview := Preview{AffectedCount: len(sources), ScoreBands: []Band{}, SelectorHash: sourceHash(tenantID, examID, questionID, selector, sources)}
	counts := map[float64]int{}
	for _, source := range sources {
		counts[source.OriginalScore]++
		gradedAt := source.GradedAt.UTC()
		if preview.TimeRange.From == nil || gradedAt.Before(*preview.TimeRange.From) {
			preview.TimeRange.From = &gradedAt
		}
		if preview.TimeRange.To == nil || gradedAt.After(*preview.TimeRange.To) {
			preview.TimeRange.To = &gradedAt
		}
	}
	for score, count := range counts {
		preview.ScoreBands = append(preview.ScoreBands, Band{Score: score, Count: count})
	}
	sort.Slice(preview.ScoreBands, func(i, j int) bool { return preview.ScoreBands[i].Score < preview.ScoreBands[j].Score })
	return preview
}

func statusCounts(items []Item) StatusCounts {
	var counts StatusCounts
	for _, item := range items {
		switch item.Status {
		case ItemPending, ItemInProgress:
			counts.PendingCount++
		case ItemDiffReady, ItemArbitrationRequired, ItemRegradeRequired:
			counts.CompletedCount++
		}
		if item.Status == ItemRegradeRequired {
			counts.RegradeRequiredCount++
		}
	}
	return counts
}

func validScope(tenantID, examID, questionID string) bool {
	return strings.TrimSpace(tenantID) != "" && strings.TrimSpace(examID) != "" && strings.TrimSpace(questionID) != ""
}

func validCreate(input CreateInput) bool {
	return strings.TrimSpace(input.SourceIncidentID) != "" && strings.TrimSpace(input.ReassignedTo) != "" &&
		validSelector(input.Selector) && validPolicy(input.Policy)
}

func validSelector(value Selector) bool {
	if len(value.TaskIDs) > MaxSelectorTaskIDs {
		return false
	}
	if value.TimeRange != nil {
		if value.TimeRange.From != nil && value.TimeRange.To != nil && value.TimeRange.To.Before(*value.TimeRange.From) {
			return false
		}
	}
	if value.ScoreBand != nil {
		if value.ScoreBand.Min != nil && (!finiteNonNegative(*value.ScoreBand.Min)) {
			return false
		}
		if value.ScoreBand.Max != nil && (!finiteNonNegative(*value.ScoreBand.Max)) {
			return false
		}
		if value.ScoreBand.Min != nil && value.ScoreBand.Max != nil && *value.ScoreBand.Max < *value.ScoreBand.Min {
			return false
		}
	}
	for _, taskID := range value.TaskIDs {
		if strings.TrimSpace(taskID) == "" {
			return false
		}
	}
	return true
}

func validPage(page PageOptions) bool {
	return validItemStatus(page.Status) && page.Limit > 0 && page.Limit <= MaxPageSize+1 &&
		(page.CursorCreatedAt.IsZero() == (strings.TrimSpace(page.CursorID) == ""))
}

func validItemStatus(status string) bool {
	switch status {
	case "", ItemPending, ItemInProgress, ItemDiffReady, ItemArbitrationRequired, ItemRegradeRequired, ItemCancelled:
		return true
	}
	return false
}

func validPolicy(value Policy) bool {
	value = normalizePolicy(value)
	return (value.Disposition == DispositionConfirm || value.Disposition == DispositionArbitrate || value.Disposition == DispositionRegrade) &&
		finiteNonNegative(value.ArbitrationDelta)
}

func normalizePolicy(value Policy) Policy {
	value.Disposition = strings.TrimSpace(value.Disposition)
	if value.Disposition == "" {
		value.Disposition = DispositionConfirm
	}
	return value
}

func normalizeSelector(value Selector) Selector {
	value.GraderID = strings.TrimSpace(value.GraderID)
	if value.TimeRange != nil {
		rangeValue := *value.TimeRange
		if rangeValue.From != nil {
			from := rangeValue.From.UTC()
			rangeValue.From = &from
		}
		if rangeValue.To != nil {
			to := rangeValue.To.UTC()
			rangeValue.To = &to
		}
		value.TimeRange = &rangeValue
		if rangeValue.From == nil && rangeValue.To == nil {
			value.TimeRange = nil
		}
	}
	if value.ScoreBand != nil && value.ScoreBand.Min == nil && value.ScoreBand.Max == nil {
		value.ScoreBand = nil
	}
	if len(value.TaskIDs) == 0 {
		value.TaskIDs = nil
		return value
	}
	seen := make(map[string]struct{}, len(value.TaskIDs))
	ids := make([]string, 0, len(value.TaskIDs))
	for _, id := range value.TaskIDs {
		id = strings.TrimSpace(id)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	value.TaskIDs = ids
	return value
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validSelections(values []RubricSelection) bool {
	for _, value := range values {
		if strings.TrimSpace(value.PointID) == "" || !finiteNonNegative(value.Score) {
			return false
		}
	}
	return true
}

func histogram(items []Item) []Histogram {
	counts := map[float64]int{}
	for _, item := range items {
		if item.Diff != nil {
			counts[*item.Diff]++
		}
	}
	out := make([]Histogram, 0, len(counts))
	for delta, count := range counts {
		out = append(out, Histogram{Delta: delta, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Delta < out[j].Delta })
	return out
}

func graderView(item Item) GraderItem {
	return GraderItem{ID: item.ID, ReviewTaskID: item.ReviewTaskID, Status: item.Status, MaxScore: item.MaxScore, Revision: item.Revision, CreatedAt: item.CreatedAt}
}
