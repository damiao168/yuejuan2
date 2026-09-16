package regrade

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

type Service struct {
	store   Store
	context ContextSource
}

func NewService(store Store) *Service { return &Service{store: store} }

// WithContextSource supplies the independent, read-only answer evidence for
// a claimed regrade item. It never exposes a source-release score or invokes
// the normal review submission workflow.
func (s *Service) WithContextSource(source ContextSource) *Service {
	s.context = source
	return s
}

func (s *Service) Preview(ctx context.Context, tenantID, examID, questionID, sourceReleaseID string, selector Selector) (Preview, error) {
	if !validScope(tenantID, examID, questionID) || strings.TrimSpace(sourceReleaseID) == "" || !validSelector(selector) {
		return Preview{}, ErrInvalidInput
	}
	return s.store.Preview(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(examID), strings.TrimSpace(questionID), strings.TrimSpace(sourceReleaseID), normalizeSelector(selector))
}

// Create freezes the selected source-release question facts into regrade_item
// rows. It creates neither a human/final grade nor a release, so retrying a
// request through its idempotency key is safe.
func (s *Service) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput) (Summary, error) {
	if !validScope(tenantID, examID, questionID) || strings.TrimSpace(actorID) == "" || !validCreate(input) {
		return Summary{}, ErrInvalidInput
	}
	input = normalizeCreate(input)
	preview, err := s.store.Preview(ctx, tenantID, examID, questionID, input.SourceReleaseID, input.Selector)
	if err != nil {
		return Summary{}, err
	}
	if preview.AffectedCount == 0 {
		return Summary{}, ErrNoAffectedItems
	}
	// The Store owns a transaction and repeats the source read under its lock;
	// the preview is for UX only, never treated as authority.
	sources, err := sourceItemsForPreview(ctx, s.store, tenantID, examID, questionID, input.SourceReleaseID, input.Selector)
	if err != nil {
		return Summary{}, err
	}
	if len(sources) == 0 {
		return Summary{}, ErrNoAffectedItems
	}
	job, items, err := s.store.Create(ctx, tenantID, examID, questionID, actorID, input, sources)
	if err != nil {
		return Summary{}, err
	}
	return summarize(Summary{Job: job, Items: items}), nil
}

// sourceItemsForPreview keeps the public Store compact. Concrete stores expose
// this internal optional capability so Create can freeze exact release facts.
// It is intentionally not implemented by querying current final_grade.
func sourceItemsForPreview(ctx context.Context, store Store, tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	reader, ok := store.(interface {
		SourceItems(context.Context, string, string, string, string, Selector) ([]SourceItem, error)
	})
	if !ok {
		return nil, ErrStateConflict
	}
	return reader.SourceItems(ctx, tenantID, examID, questionID, releaseID, selector)
}

func (s *Service) Get(ctx context.Context, tenantID, jobID string) (Summary, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(jobID) == "" {
		return Summary{}, ErrInvalidInput
	}
	summary, err := s.store.Get(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID))
	if err != nil {
		return Summary{}, err
	}
	return summarize(summary), nil
}

func (s *Service) List(ctx context.Context, tenantID, examID, questionID string) ([]Job, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.List(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(examID), strings.TrimSpace(questionID))
}

func (s *Service) ListAssigned(ctx context.Context, tenantID, reviewerID string) ([]WorkItem, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(reviewerID) == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListAssigned(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(reviewerID))
	if err != nil {
		return nil, err
	}
	out := make([]WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, workItem(item))
	}
	return out, nil
}

func (s *Service) GetGraderContext(ctx context.Context, tenantID, itemID, reviewerID string) (GraderContext, error) {
	if !validActor(tenantID, itemID, reviewerID) || s.context == nil {
		return GraderContext{}, ErrInvalidInput
	}
	return s.context.GetGraderContext(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(itemID), strings.TrimSpace(reviewerID))
}

func (s *Service) GetSegmentID(ctx context.Context, tenantID, itemID, reviewerID string) (string, error) {
	if !validActor(tenantID, itemID, reviewerID) || s.context == nil {
		return "", ErrInvalidInput
	}
	return s.context.GetSegmentID(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(itemID), strings.TrimSpace(reviewerID))
}

func (s *Service) Approve(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	if !validActor(tenantID, jobID, actorID) {
		return Job{}, ErrInvalidInput
	}
	return s.store.Approve(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID), strings.TrimSpace(actorID))
}

func (s *Service) Start(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	if !validActor(tenantID, jobID, actorID) {
		return Job{}, ErrInvalidInput
	}
	return s.store.Start(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID), strings.TrimSpace(actorID))
}

func (s *Service) Pause(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	if !validActor(tenantID, jobID, actorID) {
		return Job{}, ErrInvalidInput
	}
	return s.store.Pause(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID), strings.TrimSpace(actorID))
}

func (s *Service) Resume(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	if !validActor(tenantID, jobID, actorID) {
		return Job{}, ErrInvalidInput
	}
	return s.store.Resume(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID), strings.TrimSpace(actorID))
}

func (s *Service) Claim(ctx context.Context, tenantID, itemID, actorID string) (Item, error) {
	if !validActor(tenantID, itemID, actorID) {
		return Item{}, ErrInvalidInput
	}
	return s.store.Claim(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(itemID), strings.TrimSpace(actorID))
}

func (s *Service) RecordCandidate(ctx context.Context, tenantID, itemID, actorID string, input CandidateInput) (Item, error) {
	if !validActor(tenantID, itemID, actorID) || input.ExpectedRevision <= 0 || !finiteNonNegative(input.Score) || !validRubricSelections(input.RubricSelections) || utf8.RuneCountInString(strings.TrimSpace(input.Comment)) > 2000 {
		return Item{}, ErrInvalidInput
	}
	input.CandidateGradeID, input.Comment = strings.TrimSpace(input.CandidateGradeID), strings.TrimSpace(input.Comment)
	return s.store.RecordCandidate(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(itemID), strings.TrimSpace(actorID), input)
}

func (s *Service) Review(ctx context.Context, tenantID, itemID, actorID string, input ReviewInput) (Item, error) {
	if !validActor(tenantID, itemID, actorID) || input.ExpectedRevision <= 0 || !validReview(input) {
		return Item{}, ErrInvalidInput
	}
	input.Decision, input.ReviewedGradeID, input.Note = strings.TrimSpace(input.Decision), strings.TrimSpace(input.ReviewedGradeID), strings.TrimSpace(input.Note)
	return s.store.Review(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(itemID), strings.TrimSpace(actorID), input)
}

// Finalize only marks an entirely reviewed correction as ready for a successor
// Score Release. It does not publish or update current score facts.
func (s *Service) Finalize(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	if !validActor(tenantID, jobID, actorID) {
		return Job{}, ErrInvalidInput
	}
	return s.store.Finalize(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(jobID), strings.TrimSpace(actorID))
}

func validScope(tenantID, examID, questionID string) bool {
	return strings.TrimSpace(tenantID) != "" && strings.TrimSpace(examID) != "" && strings.TrimSpace(questionID) != ""
}
func validActor(tenantID, id, actorID string) bool {
	return strings.TrimSpace(tenantID) != "" && strings.TrimSpace(id) != "" && strings.TrimSpace(actorID) != ""
}
func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validSelector(value Selector) bool {
	if value.ScoreBand != nil {
		if value.ScoreBand.Min != nil && !finiteNonNegative(*value.ScoreBand.Min) {
			return false
		}
		if value.ScoreBand.Max != nil && !finiteNonNegative(*value.ScoreBand.Max) {
			return false
		}
		if value.ScoreBand.Min != nil && value.ScoreBand.Max != nil && *value.ScoreBand.Max < *value.ScoreBand.Min {
			return false
		}
	}
	for _, id := range value.SubmissionIDs {
		if strings.TrimSpace(id) == "" {
			return false
		}
	}
	return true
}

func validCreate(value CreateInput) bool {
	return strings.TrimSpace(value.SourceReleaseID) != "" && validReason(value.ReasonCode) && strings.TrimSpace(value.ReasonText) != "" &&
		utf8.RuneCountInString(strings.TrimSpace(value.ReasonText)) <= 2000 && validStrategy(value.Strategy) && validSelector(value.Selector) &&
		len(strings.TrimSpace(value.IdempotencyKey)) >= 8 && len(strings.TrimSpace(value.IdempotencyKey)) <= 200
}
func validReason(value string) bool {
	switch strings.TrimSpace(value) {
	case ReasonAnswerKeyError, ReasonRubricError, ReasonOCRCorrection, ReasonParserBug, ReasonModelIssue, ReasonQualityIssue, ReasonAppealPattern, ReasonOther:
		return true
	}
	return false
}
func validStrategy(value string) bool {
	switch strings.TrimSpace(value) {
	case StrategyRuleRecompute, StrategyAIRecompute, StrategyHumanRecheck, StrategyBackmarkImport:
		return true
	}
	return false
}
func validReview(value ReviewInput) bool {
	if utf8.RuneCountInString(strings.TrimSpace(value.Note)) > 2000 {
		return false
	}
	switch strings.TrimSpace(value.Decision) {
	case ReviewAccept:
		return value.ReviewedScore == nil || finiteNonNegative(*value.ReviewedScore)
	case ReviewReject, ReviewException:
		return value.ReviewedScore == nil
	}
	return false
}
func validRubricSelections(values []RubricSelection) bool {
	for _, value := range values {
		if strings.TrimSpace(value.PointID) == "" || !finiteNonNegative(value.Score) {
			return false
		}
	}
	return true
}
func normalizeSelector(value Selector) Selector {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(value.SubmissionIDs))
	for _, id := range value.SubmissionIDs {
		id = strings.TrimSpace(id)
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	value.SubmissionIDs = ids
	return value
}
func normalizeCreate(value CreateInput) CreateInput {
	value.SourceReleaseID, value.ReasonCode, value.ReasonText, value.Strategy = strings.TrimSpace(value.SourceReleaseID), strings.TrimSpace(value.ReasonCode), strings.TrimSpace(value.ReasonText), strings.TrimSpace(value.Strategy)
	value.NewRubricSnapshotID, value.NewPolicyVersion, value.IdempotencyKey, value.AssigneeID = strings.TrimSpace(value.NewRubricSnapshotID), strings.TrimSpace(value.NewPolicyVersion), strings.TrimSpace(value.IdempotencyKey), strings.TrimSpace(value.AssigneeID)
	value.Selector = normalizeSelector(value.Selector)
	if value.SeverityDelta == nil {
		defaultValue := 2.0
		value.SeverityDelta = &defaultValue
	}
	return value
}

func summarize(summary Summary) Summary {
	counts := map[float64]int{}
	severe := []SevereChange{}
	resolved := []ReleaseChange{}
	for _, item := range summary.Items {
		if item.Delta != nil {
			counts[*item.Delta]++
			if math.Abs(*item.Delta) >= summary.Job.SeverityDelta {
				newScore := item.OldScore + *item.Delta
				if item.ReviewedScore != nil {
					newScore = *item.ReviewedScore
				}
				severe = append(severe, SevereChange{SubmissionID: item.SubmissionID, OldScore: item.OldScore, NewScore: newScore, Delta: *item.Delta})
			}
		}
		if summary.Job.Status == StatusReadyForRelease && item.Status == ItemResolved && item.ReviewedScore != nil {
			resolved = append(resolved, ReleaseChange{SubmissionID: item.SubmissionID, OldFinalGradeID: item.OldFinalGradeID, SourceScore: item.OldScore, RegradedScore: *item.ReviewedScore, MaxScore: item.MaxScore, ReviewedGradeID: item.ReviewedGradeID})
		}
	}
	summary.DiffHistogram = make([]Histogram, 0, len(counts))
	for delta, count := range counts {
		summary.DiffHistogram = append(summary.DiffHistogram, Histogram{Delta: delta, Count: count})
	}
	sort.Slice(summary.DiffHistogram, func(i, j int) bool { return summary.DiffHistogram[i].Delta < summary.DiffHistogram[j].Delta })
	sort.Slice(severe, func(i, j int) bool { return severe[i].SubmissionID < severe[j].SubmissionID })
	summary.SevereChanges = severe
	if summary.Job.Status == StatusReadyForRelease {
		sort.Slice(resolved, func(i, j int) bool { return resolved[i].SubmissionID < resolved[j].SubmissionID })
		summary.ReleasePlan = &ReleasePlan{JobID: summary.Job.ID, ExamID: summary.Job.ExamID, QuestionID: summary.Job.QuestionID, SourceReleaseID: summary.Job.SourceReleaseID, AffectedCount: len(resolved), ResolvedItems: resolved}
	}
	return summary
}

func workItem(item Item) WorkItem {
	return WorkItem{ID: item.ID, JobID: item.JobID, SubmissionID: item.SubmissionID, Status: item.Status, MaxScore: item.MaxScore, Revision: item.Revision, CreatedAt: item.CreatedAt}
}
