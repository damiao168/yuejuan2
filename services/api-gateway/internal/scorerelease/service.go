package scorerelease

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	store          Store
	highScorePaper HighScorePaperManager
	now            func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) WithHighScorePaper(manager HighScorePaperManager) *Service {
	s.highScorePaper = manager
	return s
}

func (s *Service) Create(ctx context.Context, tenantID, examID, actorID string, input CreateInput) (Release, error) {
	if !validIDs(tenantID, examID, actorID) || !normalizeCreate(&input) ||
		(input.VisibilityPolicy.ShowHighScorePaper && !input.VisibilityPolicy.ShowQuestionScores) {
		return Release{}, ErrInvalidInput
	}
	return s.store.Create(ctx, tenantID, examID, actorID, input)
}

func (s *Service) CreateRollback(ctx context.Context, tenantID, examID, actorID string, input RollbackInput) (Release, error) {
	if !validIDs(tenantID, examID, actorID) || !normalizeRollback(&input) {
		return Release{}, ErrInvalidInput
	}
	return s.store.CreateRollback(ctx, tenantID, examID, actorID, input)
}

// CreateFromRegrade materialises a later immutable release from a reviewed
// A19 plan. It only delegates frozen source-release facts and explicit
// reviewed replacements to the store; it never reads current mutable grades.
func (s *Service) CreateFromRegrade(ctx context.Context, tenantID, examID, actorID string, input CreateRegradeInput) (Release, error) {
	if !validIDs(tenantID, examID, actorID) || !normalizeRegrade(&input) {
		return Release{}, ErrInvalidInput
	}
	return s.store.CreateFromRegrade(ctx, tenantID, examID, actorID, input)
}

func (s *Service) List(ctx context.Context, tenantID, examID string) ([]Release, error) {
	if !validIDs(tenantID, examID) {
		return nil, ErrInvalidInput
	}
	return s.store.List(ctx, tenantID, examID)
}

func (s *Service) Get(ctx context.Context, tenantID, id string) (Detail, error) {
	if !validIDs(tenantID, id) {
		return Detail{}, ErrInvalidInput
	}
	return s.store.Get(ctx, tenantID, id)
}

func (s *Service) Diff(ctx context.Context, tenantID, id, baseID string) (Diff, error) {
	if !validIDs(tenantID, id, baseID) || id == baseID {
		return Diff{}, ErrInvalidInput
	}
	return s.store.Diff(ctx, tenantID, id, baseID)
}

func (s *Service) Gate(ctx context.Context, tenantID, examID string) (Gate, error) {
	if !validIDs(tenantID, examID) {
		return Gate{}, ErrInvalidInput
	}
	return s.store.Gate(ctx, tenantID, examID)
}

func (s *Service) Publish(ctx context.Context, tenantID, id, actorID string) (Release, error) {
	if !validIDs(tenantID, id, actorID) {
		return Release{}, ErrInvalidInput
	}
	detail, err := s.store.Get(ctx, tenantID, id)
	if err != nil {
		return Release{}, err
	}
	if detail.Release.Status == StatusPublished {
		return s.store.Publish(ctx, tenantID, id, actorID)
	}
	if detail.Release.VisibilityPolicy.ShowHighScorePaper {
		if s.highScorePaper == nil {
			return Release{}, ErrAnonymousPaperUnavailable
		}
		if err := s.highScorePaper.Prepare(ctx, tenantID, id, actorID); err != nil {
			return Release{}, err
		}
	}
	return s.store.Publish(ctx, tenantID, id, actorID)
}

func (s *Service) RevokeHighScorePaper(ctx context.Context, tenantID, id, actorID string) error {
	if !validIDs(tenantID, id, actorID) {
		return ErrInvalidInput
	}
	if s.highScorePaper == nil {
		return ErrAnonymousPaperUnavailable
	}
	return s.highScorePaper.Revoke(ctx, tenantID, id, actorID)
}

func (s *Service) CurrentPublished(ctx context.Context, tenantID, examID string) (Detail, error) {
	if !validIDs(tenantID, examID) {
		return Detail{}, ErrInvalidInput
	}
	return s.store.CurrentPublished(ctx, tenantID, examID)
}

func (s *Service) StudentResult(ctx context.Context, tenantID, examID, studentID string) (StudentResult, error) {
	if !validIDs(tenantID, examID, studentID) {
		return StudentResult{}, ErrInvalidInput
	}
	return s.store.StudentResult(ctx, tenantID, examID, studentID)
}

func (s *Service) StudentQuestion(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestion, error) {
	if !validIDs(tenantID, examID, studentID, questionID) {
		return StudentQuestion{}, ErrInvalidInput
	}
	return s.store.StudentQuestion(ctx, tenantID, examID, studentID, questionID)
}

func (s *Service) StudentQuestionImage(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestionImageSource, error) {
	if !validIDs(tenantID, examID, studentID, questionID) {
		return StudentQuestionImageSource{}, ErrInvalidInput
	}
	return s.store.StudentQuestionImage(ctx, tenantID, examID, studentID, questionID)
}

func (s *Service) StudentPaperPageImage(ctx context.Context, tenantID, examID, studentID, questionID string, highScore bool) (StudentQuestionImageSource, error) {
	if !validIDs(tenantID, examID, studentID, questionID) {
		return StudentQuestionImageSource{}, ErrInvalidInput
	}
	return s.store.StudentPaperPageImage(ctx, tenantID, examID, studentID, questionID, highScore)
}

func normalizeCreate(input *CreateInput) bool {
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Source == "" {
		input.Source = SourceInitial
	}
	if !oneOf(input.Source, SourceInitial, SourceRegrade, SourceAppeal, SourceMigration) ||
		input.Reason == "" || utf8.RuneCountInString(input.Reason) > 1000 ||
		utf8.RuneCountInString(input.IdempotencyKey) < 8 || utf8.RuneCountInString(input.IdempotencyKey) > 200 {
		return false
	}
	return normalizeWindow(&input.AppealWindow)
}

func normalizeRollback(input *RollbackInput) bool {
	input.SourceReleaseID = strings.TrimSpace(input.SourceReleaseID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input.SourceReleaseID != "" && input.Reason != "" && utf8.RuneCountInString(input.Reason) <= 1000 &&
		utf8.RuneCountInString(input.IdempotencyKey) >= 8 && utf8.RuneCountInString(input.IdempotencyKey) <= 200
}

func normalizeRegrade(input *CreateRegradeInput) bool {
	input.SourceReleaseID = strings.TrimSpace(input.SourceReleaseID)
	input.QuestionID = strings.TrimSpace(input.QuestionID)
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.SourceReleaseID == "" || input.QuestionID == "" || input.Reason == "" ||
		utf8.RuneCountInString(input.Reason) > 1000 || utf8.RuneCountInString(input.IdempotencyKey) < 8 ||
		utf8.RuneCountInString(input.IdempotencyKey) > 200 || len(input.Changes) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(input.Changes))
	for index := range input.Changes {
		change := &input.Changes[index]
		change.SubmissionID = strings.TrimSpace(change.SubmissionID)
		change.QuestionID = strings.TrimSpace(change.QuestionID)
		change.ReviewedGradeID = strings.TrimSpace(change.ReviewedGradeID)
		if change.SubmissionID == "" || change.QuestionID != input.QuestionID || math.IsNaN(change.Score) || math.IsInf(change.Score, 0) ||
			math.IsNaN(change.MaxScore) || math.IsInf(change.MaxScore, 0) || change.Score < 0 || change.MaxScore < 0 || change.Score > change.MaxScore {
			return false
		}
		key := change.SubmissionID + "\x00" + change.QuestionID
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func normalizeWindow(window *AppealWindow) bool {
	if !window.Enabled {
		window.OpensAt, window.ClosesAt, window.AllowedReasonCodes = nil, nil, nil
		return true
	}
	if window.ClosesAt != nil && window.OpensAt != nil && !window.ClosesAt.After(*window.OpensAt) {
		return false
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(window.AllowedReasonCodes))
	for _, code := range window.AllowedReasonCodes {
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" || utf8.RuneCountInString(code) > 64 || seen[code] {
			return false
		}
		seen[code] = true
		clean = append(clean, code)
	}
	sort.Strings(clean)
	window.AllowedReasonCodes = clean
	return true
}

func validIDs(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func appealView(window AppealWindow, now time.Time) StudentAppealView {
	view := StudentAppealView{}
	if !window.Enabled {
		return view
	}
	if window.OpensAt != nil && now.Before(*window.OpensAt) {
		view.ClosesAt = cloneTime(window.ClosesAt)
		return view
	}
	if window.ClosesAt != nil && !now.Before(*window.ClosesAt) {
		view.ClosesAt = cloneTime(window.ClosesAt)
		return view
	}
	view.Open = true
	view.ClosesAt = cloneTime(window.ClosesAt)
	view.AllowedReasonCodes = append([]string(nil), window.AllowedReasonCodes...)
	return view
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
