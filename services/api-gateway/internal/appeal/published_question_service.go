package appeal

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"
)

// PublishedQuestionAppealService keeps all entry points on the immutable
// release boundary.  In particular, it rejects client-supplied original
// scores: those facts are re-read from score_release_question by the store.
type PublishedQuestionAppealService struct {
	store PublishedQuestionAppealStore
}

func NewPublishedQuestionAppealService(store PublishedQuestionAppealStore) *PublishedQuestionAppealService {
	return &PublishedQuestionAppealService{store: store}
}

func (s *PublishedQuestionAppealService) Create(ctx context.Context, tenantID, studentID, actorID string, input CreatePublishedQuestionAppealInput) (PublishedQuestionAppeal, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(studentID) == "" || strings.TrimSpace(actorID) == "" || !validPublishedQuestionAppealCreate(input) {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	input = normalizePublishedQuestionAppealCreate(input)
	return s.store.CreatePublishedQuestionAppeal(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(studentID), strings.TrimSpace(actorID), input)
}

func (s *PublishedQuestionAppealService) Get(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppeal, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(appealID) == "" {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	return s.store.GetPublishedQuestionAppeal(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID))
}

func (s *PublishedQuestionAppealService) Context(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppealContext, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(appealID) == "" {
		return PublishedQuestionAppealContext{}, ErrInvalidInput
	}
	return s.store.GetPublishedQuestionAppealContext(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID))
}

func (s *PublishedQuestionAppealService) List(ctx context.Context, tenantID string, filter QuestionAppealFilter) ([]PublishedQuestionAppeal, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	filter.ExamID, filter.StudentID, filter.AssignedTo, filter.Status = strings.TrimSpace(filter.ExamID), strings.TrimSpace(filter.StudentID), strings.TrimSpace(filter.AssignedTo), strings.TrimSpace(filter.Status)
	if filter.Status != "" && !validPublishedQuestionAppealStatus(filter.Status) {
		return nil, ErrInvalidInput
	}
	return s.store.ListPublishedQuestionAppeals(ctx, strings.TrimSpace(tenantID), filter)
}

func (s *PublishedQuestionAppealService) StartReview(ctx context.Context, tenantID, appealID, actorID string, input StartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	if !validPublishedQuestionAppealActor(tenantID, appealID, actorID) || strings.TrimSpace(input.AssignedTo) == "" || input.ExpectedRevision <= 0 {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	return s.store.StartPublishedQuestionAppealReview(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID), strings.TrimSpace(actorID), input)
}

func (s *PublishedQuestionAppealService) Decide(ctx context.Context, tenantID, appealID, actorID string, input DecideQuestionAppealInput) (PublishedQuestionAppeal, error) {
	if !validPublishedQuestionAppealActor(tenantID, appealID, actorID) || input.ExpectedRevision <= 0 || !validPublishedQuestionAppealDecision(input) {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.PublicResponse, input.PrivateNote, input.RegradeJobID = strings.TrimSpace(input.PublicResponse), strings.TrimSpace(input.PrivateNote), strings.TrimSpace(input.RegradeJobID)
	return s.store.DecidePublishedQuestionAppeal(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID), strings.TrimSpace(actorID), input)
}

func (s *PublishedQuestionAppealService) Resolve(ctx context.Context, tenantID, appealID, actorID string, input ResolveQuestionAppealInput) (PublishedQuestionAppeal, error) {
	if !validPublishedQuestionAppealActor(tenantID, appealID, actorID) || input.ExpectedRevision <= 0 || strings.TrimSpace(input.NewReleaseID) == "" || utf8.RuneCountInString(strings.TrimSpace(input.PublicResponse)) > 2000 || utf8.RuneCountInString(strings.TrimSpace(input.PrivateNote)) > 4000 {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	input.NewReleaseID, input.PublicResponse, input.PrivateNote = strings.TrimSpace(input.NewReleaseID), strings.TrimSpace(input.PublicResponse), strings.TrimSpace(input.PrivateNote)
	return s.store.ResolvePublishedQuestionAppeal(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID), strings.TrimSpace(actorID), input)
}

func (s *PublishedQuestionAppealService) Events(ctx context.Context, tenantID, appealID string) ([]PublishedQuestionAppealEvent, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(appealID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ListPublishedQuestionAppealEvents(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID))
}

func validPublishedQuestionAppealCreate(input CreatePublishedQuestionAppealInput) bool {
	return strings.TrimSpace(input.ExamID) != "" && strings.TrimSpace(input.SourceReleaseID) != "" && strings.TrimSpace(input.QuestionID) != "" &&
		validPublishedQuestionAppealReason(input.ReasonCode) && strings.TrimSpace(input.Reason) != "" && utf8.RuneCountInString(strings.TrimSpace(input.Reason)) <= 2000 &&
		validPublishedQuestionAppealRegion(input.SelectedRegion)
}

// A selected region is deliberately canonical to the answer crop, rather
// than a browser pixel position. This makes the evidence usable after display
// scaling while preventing arbitrary JSON from becoming staff workflow data.
func validPublishedQuestionAppealRegion(region map[string]any) bool {
	if region == nil {
		return true
	}
	if len(region) != 5 || region["coordinate_space"] != "canonical_image_normalized" {
		return false
	}
	x, xOK := appealRegionNumber(region["x"])
	y, yOK := appealRegionNumber(region["y"])
	width, widthOK := appealRegionNumber(region["width"])
	height, heightOK := appealRegionNumber(region["height"])
	return xOK && yOK && widthOK && heightOK && x >= 0 && y >= 0 && width > 0 && height > 0 && x+width <= 1 && y+height <= 1
}

func appealRegionNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func normalizePublishedQuestionAppealCreate(input CreatePublishedQuestionAppealInput) CreatePublishedQuestionAppealInput {
	input.ExamID = strings.TrimSpace(input.ExamID)
	input.SourceReleaseID = strings.TrimSpace(input.SourceReleaseID)
	input.QuestionID = strings.TrimSpace(input.QuestionID)
	input.ReasonCode = strings.TrimSpace(input.ReasonCode)
	input.Reason = strings.TrimSpace(input.Reason)
	input.SelectedRegion = cloneMap(input.SelectedRegion)
	return input
}

func validPublishedQuestionAppealReason(value string) bool {
	switch strings.TrimSpace(value) {
	case ReasonRecognitionError, ReasonMissingStepCredit, ReasonRubricDisagreement, ReasonCalculationError, ReasonAnnotationIssue, ReasonOther:
		return true
	}
	return false
}

func validPublishedQuestionAppealStatus(value string) bool {
	switch value {
	case QuestionAppealSubmitted, QuestionAppealUnderReview, QuestionAppealRejected, QuestionAppealUpheldPendingRegrade, QuestionAppealResolved:
		return true
	}
	return false
}

func validPublishedQuestionAppealDecision(input DecideQuestionAppealInput) bool {
	if strings.TrimSpace(input.PublicResponse) == "" || utf8.RuneCountInString(strings.TrimSpace(input.PublicResponse)) > 2000 || utf8.RuneCountInString(strings.TrimSpace(input.PrivateNote)) > 4000 {
		return false
	}
	switch strings.TrimSpace(input.Decision) {
	case QuestionAppealDecisionReject:
		return strings.TrimSpace(input.RegradeJobID) == ""
	case QuestionAppealDecisionReferRegrade:
		return strings.TrimSpace(input.RegradeJobID) != ""
	}
	return false
}

func validPublishedQuestionAppealActor(tenantID, appealID, actorID string) bool {
	return strings.TrimSpace(tenantID) != "" && strings.TrimSpace(appealID) != "" && strings.TrimSpace(actorID) != ""
}
