package appeal

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PublishedQuestionAppealSource 仅用于测试，具化生产存储读取的发布事实。
// 将它与 FinalGradeSeed 分开，可验证申诉不会跟随后续可变分数变化。
type PublishedQuestionAppealSource struct {
	TenantID           string
	ExamID             string
	StudentID          string
	SubmissionID       string
	ReleaseID          string
	ReleaseVersion     int
	Published          bool
	AppealEnabled      bool
	AppealOpensAt      *time.Time
	AppealClosesAt     *time.Time
	AllowedReasonCodes []string
	QuestionID         string
	QuestionNo         string
	FinalGradeID       string
	Score              float64
	MaxScore           float64
	ReviewerIDs        []string
	ReleaseSource      string
	SourceReleaseID    string
	PublishedAt        *time.Time
	SourceType         string
	SourceID           string
}

type PublishedQuestionAppealMemoryStore struct {
	mu                sync.RWMutex
	next              int
	now               func() time.Time
	sources           map[string]PublishedQuestionAppealSource
	sourceReviewers   map[string][]string
	publishedReleases map[string]publishedQuestionAppealRelease
	regradeJobs       map[string]publishedQuestionAppealRegradeJob
	appeals           map[string]PublishedQuestionAppeal
	events            map[string][]PublishedQuestionAppealEvent
}

type publishedQuestionAppealRelease struct {
	TenantID        string
	ExamID          string
	ID              string
	Version         int
	Source          string
	SourceReleaseID string
	PublishedAt     *time.Time
}

type publishedQuestionAppealRegradeJob struct {
	TenantID        string
	ExamID          string
	QuestionID      string
	SourceReleaseID string
	Status          string
	FinalizedAt     *time.Time
	Items           map[string]PublishedQuestionAppealRegradeItemSource
}

// PublishedQuestionAppealRegradeItemSource 仅用于测试，保存 regrade_item 的原成绩与复核事实。
type PublishedQuestionAppealRegradeItemSource struct {
	SubmissionID    string
	OldFinalGradeID string
	OldScore        float64
	MaxScore        float64
	ReviewedScore   *float64
	ReviewedGradeID string
	Status          string
}

func NewPublishedQuestionAppealMemoryStore() *PublishedQuestionAppealMemoryStore {
	return &PublishedQuestionAppealMemoryStore{
		now:               func() time.Time { return time.Now().UTC() },
		sources:           map[string]PublishedQuestionAppealSource{},
		sourceReviewers:   map[string][]string{},
		publishedReleases: map[string]publishedQuestionAppealRelease{},
		regradeJobs:       map[string]publishedQuestionAppealRegradeJob{},
		appeals:           map[string]PublishedQuestionAppeal{},
		events:            map[string][]PublishedQuestionAppealEvent{},
	}
}

func (s *PublishedQuestionAppealMemoryStore) SetNow(value func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = value
}

func (s *PublishedQuestionAppealMemoryStore) SeedSource(source PublishedQuestionAppealSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[publishedQuestionSourceKey(source.TenantID, source.ExamID, source.StudentID, source.ReleaseID, source.QuestionID)] = clonePublishedQuestionAppealSource(source)
	if len(source.ReviewerIDs) > 0 {
		s.sourceReviewers[source.TenantID+"\x00"+source.FinalGradeID] = append([]string(nil), source.ReviewerIDs...)
	}
	if source.Published {
		s.publishedReleases[publishedQuestionReleaseKey(source.TenantID, source.ExamID, source.ReleaseID)] = publishedQuestionAppealRelease{
			TenantID: source.TenantID, ExamID: source.ExamID, ID: source.ReleaseID, Version: source.ReleaseVersion,
			Source: source.ReleaseSource, SourceReleaseID: source.SourceReleaseID, PublishedAt: clonePublishedQuestionAppealTime(source.PublishedAt),
		}
	}
}

func (s *PublishedQuestionAppealMemoryStore) SeedPublishedResolutionRelease(tenantID, examID, releaseID string, version int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishedReleases[publishedQuestionReleaseKey(tenantID, examID, releaseID)] = publishedQuestionAppealRelease{TenantID: tenantID, ExamID: examID, ID: releaseID, Version: version}
}

// SeedRegradeJob 表示已冻结来源发布、题目与原答卷集合的 A19 任务，不能用空任务代替该学生的重评事实。
func (s *PublishedQuestionAppealMemoryStore) SeedRegradeJob(jobID, tenantID, examID, questionID, sourceReleaseID string, items ...PublishedQuestionAppealRegradeItemSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := publishedQuestionAppealRegradeJob{TenantID: tenantID, ExamID: examID, QuestionID: questionID, SourceReleaseID: sourceReleaseID, Status: "awaiting_approval", Items: map[string]PublishedQuestionAppealRegradeItemSource{}}
	for _, item := range items {
		if item.ReviewedScore != nil {
			score := *item.ReviewedScore
			item.ReviewedScore = &score
		}
		job.Items[item.SubmissionID] = item
	}
	s.regradeJobs[jobID] = job
}

// SeedRegradeJobState 仅更新测试任务的完成状态，结案还会核对原答卷条目与后续发布事实。
func (s *PublishedQuestionAppealMemoryStore) SeedRegradeJobState(jobID, status string, finalizedAt *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.regradeJobs[jobID]
	job.Status, job.FinalizedAt = status, clonePublishedQuestionAppealTime(finalizedAt)
	s.regradeJobs[jobID] = job
}

func (s *PublishedQuestionAppealMemoryStore) CreatePublishedQuestionAppeal(_ context.Context, tenantID, studentID, actorID string, input CreatePublishedQuestionAppealInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.sources[publishedQuestionSourceKey(tenantID, input.ExamID, studentID, input.SourceReleaseID, input.QuestionID)]
	if !ok || !source.Published {
		return PublishedQuestionAppeal{}, ErrSourceRelease
	}
	if !sourceAppealWindowOpen(source.AppealEnabled, source.AppealOpensAt, source.AppealClosesAt, source.AllowedReasonCodes, input.ReasonCode, s.now().UTC()) {
		return PublishedQuestionAppeal{}, ErrAppealWindowClosed
	}
	for _, existing := range s.appeals {
		if existing.TenantID == tenantID && existing.StudentID == studentID && existing.SourceReleaseID == input.SourceReleaseID && existing.QuestionID == input.QuestionID {
			return PublishedQuestionAppeal{}, ErrAppealAlreadyFiled
		}
	}
	now := s.now().UTC()
	item := PublishedQuestionAppeal{
		ID:                   s.idLocked("question-appeal"),
		TenantID:             tenantID,
		ExamID:               input.ExamID,
		StudentID:            studentID,
		SubmissionID:         source.SubmissionID,
		SourceReleaseID:      source.ReleaseID,
		SourceReleaseVersion: source.ReleaseVersion,
		QuestionID:           source.QuestionID,
		QuestionNo:           source.QuestionNo,
		SourceFinalGradeID:   source.FinalGradeID,
		SourceScore:          source.Score,
		SourceMaxScore:       source.MaxScore,
		ReasonCode:           input.ReasonCode,
		Reason:               input.Reason,
		SelectedRegion:       cloneMap(input.SelectedRegion),
		Status:               QuestionAppealSubmitted,
		CreatedBy:            actorID,
		CreatedAt:            now,
		UpdatedAt:            now,
		Revision:             1,
	}
	s.appeals[item.ID] = item
	s.appendEventLocked(item.ID, actorID, "submitted", map[string]any{"source_release_id": item.SourceReleaseID, "question_id": item.QuestionID, "reason_code": item.ReasonCode})
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealMemoryStore) GetPublishedQuestionAppeal(_ context.Context, tenantID, appealID string) (PublishedQuestionAppeal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealMemoryStore) GetPublishedQuestionAppealContext(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppealContext, error) {
	item, err := s.GetPublishedQuestionAppeal(ctx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	history := make([]QuestionAppealReleaseVersion, 0)
	for _, release := range s.publishedReleases {
		if release.TenantID == tenantID && release.ExamID == item.ExamID {
			history = append(history, QuestionAppealReleaseVersion{ID: release.ID, Version: release.Version, Source: "published", Status: "published"})
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Version > history[j].Version })
	return PublishedQuestionAppealContext{Appeal: item, RubricSnapshot: map[string]any{}, ReleaseHistory: history}, nil
}

func (s *PublishedQuestionAppealMemoryStore) ListPublishedQuestionAppeals(_ context.Context, tenantID string, filter QuestionAppealFilter) ([]PublishedQuestionAppeal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]PublishedQuestionAppeal, 0)
	for _, item := range s.appeals {
		if item.TenantID != tenantID || (filter.ExamID != "" && item.ExamID != filter.ExamID) || (filter.StudentID != "" && item.StudentID != filter.StudentID) || (filter.AssignedTo != "" && item.AssignedTo != filter.AssignedTo) || (filter.Status != "" && item.Status != filter.Status) {
			continue
		}
		items = append(items, clonePublishedQuestionAppeal(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *PublishedQuestionAppealMemoryStore) StartPublishedQuestionAppealReview(_ context.Context, tenantID, appealID, actorID string, input StartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	if item.Status != QuestionAppealSubmitted {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	// 原评参与者按冻结成绩标识隔离；后续发布夹具不能改用当前分数绕过独立复核。
	for _, reviewerID := range s.sourceReviewers[tenantID+"\x00"+item.SourceFinalGradeID] {
		if reviewerID == input.AssignedTo {
			return PublishedQuestionAppeal{}, ErrForbidden
		}
	}
	item.AssignedTo, item.Status, item.UpdatedAt, item.Revision = input.AssignedTo, QuestionAppealUnderReview, s.now().UTC(), item.Revision+1
	s.appeals[appealID] = item
	s.appendEventLocked(appealID, actorID, "review_started", map[string]any{"assigned_to": input.AssignedTo})
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealMemoryStore) DecidePublishedQuestionAppeal(_ context.Context, tenantID, appealID, actorID string, input DecideQuestionAppealInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	if item.Status != QuestionAppealUnderReview {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	now := s.now().UTC()
	item.Decision, item.PublicResponse, item.PrivateNote, item.DecidedBy, item.DecidedAt = input.Decision, input.PublicResponse, input.PrivateNote, actorID, &now
	if input.Decision == QuestionAppealDecisionReferRegrade {
		if _, _, ok := s.matchingRegradeJobLocked(item, input.RegradeJobID); !ok {
			return PublishedQuestionAppeal{}, ErrInvalidInput
		}
		item.Status, item.RegradeJobID = QuestionAppealUpheldPendingRegrade, input.RegradeJobID
	} else {
		item.Status = QuestionAppealRejected
	}
	item.UpdatedAt, item.Revision = now, item.Revision+1
	s.appeals[appealID] = item
	s.appendEventLocked(appealID, actorID, "decided", map[string]any{"decision": item.Decision, "regrade_job_id": item.RegradeJobID})
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealMemoryStore) ResolvePublishedQuestionAppeal(_ context.Context, tenantID, appealID, actorID string, input ResolveQuestionAppealInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	if item.Status != QuestionAppealUpheldPendingRegrade {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	resolution, ok := s.publishedReleases[publishedQuestionReleaseKey(tenantID, item.ExamID, input.NewReleaseID)]
	if !ok || resolution.Version <= item.SourceReleaseVersion {
		return PublishedQuestionAppeal{}, ErrResolutionRelease
	}
	job, regrade, ok := s.matchingRegradeJobLocked(item, item.RegradeJobID)
	source, sourceExists := s.sources[publishedQuestionSourceKey(tenantID, item.ExamID, item.StudentID, input.NewReleaseID, item.QuestionID)]
	// 夹具也必须具化真实发布的学生答卷题目及重评结果，只有版本号的记录不能结案。
	if !ok || job.Status != "ready_for_release" || job.FinalizedAt == nil || regrade.Status != "resolved" ||
		resolution.Source != "regrade" || resolution.SourceReleaseID != item.SourceReleaseID ||
		resolution.PublishedAt == nil || resolution.PublishedAt.Before(*job.FinalizedAt) ||
		!sourceExists || !source.Published || source.SubmissionID != item.SubmissionID ||
		source.FinalGradeID != regrade.OldFinalGradeID || source.MaxScore != regrade.MaxScore {
		return PublishedQuestionAppeal{}, ErrResolutionRelease
	}
	expectedScore := regrade.OldScore
	if regrade.ReviewedScore != nil {
		expectedScore = *regrade.ReviewedScore
		if source.SourceType != "single_review" || source.SourceID != regrade.ReviewedGradeID {
			return PublishedQuestionAppeal{}, ErrResolutionRelease
		}
	}
	if source.Score != expectedScore {
		return PublishedQuestionAppeal{}, ErrResolutionRelease
	}
	item.NewReleaseID, item.Status, item.UpdatedAt, item.Revision = input.NewReleaseID, QuestionAppealResolved, s.now().UTC(), item.Revision+1
	item.NewReleaseVersion = resolution.Version
	item.ReviewedScore, item.ReviewedMaxScore = &source.Score, &source.MaxScore
	if input.PublicResponse != "" {
		item.PublicResponse = input.PublicResponse
	}
	if input.PrivateNote != "" {
		item.PrivateNote = input.PrivateNote
	}
	s.appeals[appealID] = item
	s.appendEventLocked(appealID, actorID, "resolved", map[string]any{"new_release_id": item.NewReleaseID})
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealMemoryStore) matchingRegradeJobLocked(item PublishedQuestionAppeal, jobID string) (publishedQuestionAppealRegradeJob, PublishedQuestionAppealRegradeItemSource, bool) {
	job, exists := s.regradeJobs[jobID]
	regrade, includesSubmission := job.Items[item.SubmissionID]
	ok := exists && includesSubmission && job.TenantID == item.TenantID && job.ExamID == item.ExamID &&
		job.QuestionID == item.QuestionID && job.SourceReleaseID == item.SourceReleaseID && job.Status != "cancelled" &&
		regrade.OldFinalGradeID == item.SourceFinalGradeID && regrade.Status != "cancelled"
	return job, regrade, ok
}

func (s *PublishedQuestionAppealMemoryStore) ListPublishedQuestionAppealEvents(_ context.Context, tenantID, appealID string) ([]PublishedQuestionAppealEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return nil, ErrNotFound
	}
	events := s.events[appealID]
	out := make([]PublishedQuestionAppealEvent, len(events))
	for index := range events {
		out[index] = clonePublishedQuestionAppealEvent(events[index])
	}
	return out, nil
}

func (s *PublishedQuestionAppealMemoryStore) appendEventLocked(appealID, actorID, eventType string, payload map[string]any) {
	event := PublishedQuestionAppealEvent{ID: s.idLocked("question-appeal-event"), AppealID: appealID, Type: eventType, ActorID: actorID, Payload: cloneMap(payload), CreatedAt: s.now().UTC()}
	s.events[appealID] = append(s.events[appealID], event)
}

func (s *PublishedQuestionAppealMemoryStore) idLocked(prefix string) string {
	s.next++
	return fmt.Sprintf("%s-%d", prefix, s.next)
}

func publishedQuestionSourceKey(tenantID, examID, studentID, releaseID, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + studentID + "\x00" + releaseID + "\x00" + questionID
}

func publishedQuestionReleaseKey(tenantID, examID, releaseID string) string {
	return tenantID + "\x00" + examID + "\x00" + releaseID
}

// 申诉窗口包含开始时刻、不包含截止时刻；原因白名单为空时不额外限制原因类型。
func sourceAppealWindowOpen(enabled bool, opensAt, closesAt *time.Time, allowed []string, reasonCode string, now time.Time) bool {
	if !enabled || (opensAt != nil && now.Before(*opensAt)) || (closesAt != nil && !now.Before(*closesAt)) {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, code := range allowed {
		if code == reasonCode {
			return true
		}
	}
	return false
}

func clonePublishedQuestionAppeal(in PublishedQuestionAppeal) PublishedQuestionAppeal {
	in.SelectedRegion = cloneMap(in.SelectedRegion)
	in.ReviewOpinion = cloneQuestionAppealReviewOpinion(in.ReviewOpinion)
	if in.ReviewedScore != nil {
		value := *in.ReviewedScore
		in.ReviewedScore = &value
	}
	if in.ReviewedMaxScore != nil {
		value := *in.ReviewedMaxScore
		in.ReviewedMaxScore = &value
	}
	return in
}

func clonePublishedQuestionAppealEvent(in PublishedQuestionAppealEvent) PublishedQuestionAppealEvent {
	in.Payload = cloneMap(in.Payload)
	return in
}

func clonePublishedQuestionAppealSource(in PublishedQuestionAppealSource) PublishedQuestionAppealSource {
	in.AllowedReasonCodes = append([]string(nil), in.AllowedReasonCodes...)
	in.ReviewerIDs = append([]string(nil), in.ReviewerIDs...)
	in.PublishedAt = clonePublishedQuestionAppealTime(in.PublishedAt)
	return in
}

func clonePublishedQuestionAppealTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func studentQuestionAppealView(item PublishedQuestionAppeal) StudentQuestionAppeal {
	return StudentQuestionAppeal{
		ID: item.ID, ExamID: item.ExamID, SourceReleaseID: item.SourceReleaseID, SourceReleaseVersion: item.SourceReleaseVersion,
		QuestionID: item.QuestionID, QuestionNo: item.QuestionNo, SourceScore: item.SourceScore, SourceMaxScore: item.SourceMaxScore,
		ReasonCode: item.ReasonCode, Reason: item.Reason, SelectedRegion: cloneMap(item.SelectedRegion), Status: item.Status,
		Decision: item.Decision, PublicResponse: item.PublicResponse, NewReleaseID: item.NewReleaseID, NewReleaseVersion: item.NewReleaseVersion, ReviewedScore: item.ReviewedScore, ReviewedMaxScore: item.ReviewedMaxScore, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}
