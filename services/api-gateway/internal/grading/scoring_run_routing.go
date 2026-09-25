package grading

import (
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

const (
	scoringRouteRuleInput    = "rule_input"
	scoringRouteOMR          = "omr"
	scoringRouteManualReview = "manual_review"

	// reviewSourceHumanGraded marks work that is human graded by design, as
	// opposed to an automated result waiting for confirmation.
	reviewSourceHumanGraded = "subjective_default_review"
	reviewSourceRuleReview  = "rule_review_required"

	reviewReasonRuleReviewRequired = "rule_review_required"
	reviewReasonRuleNotConfirmed   = "rule_not_auto_confirmed"

	// reviewReasonManualOnlyQuestionType marks a segment whose question type has
	// no automated scoring path at all. Paper configuration accepts such types
	// and the readiness gate reports them as an advisory, so the scoring run has
	// to hand them to a human instead of treating them as a scoring defect.
	reviewReasonManualOnlyQuestionType = "manual_only_question_type"
	// reviewReasonRuleGradingUnsupported marks a rule-grading input for a type
	// the rule engine does not implement but another automated path does.
	reviewReasonRuleGradingUnsupported = "rule_grading_unsupported"
)

// scoringSegmentRoute is the durable next action for one answer segment: queue
// automated work, or open a human task with a reason an operator can act on.
type scoringSegmentRoute struct {
	Kind   string
	Source string
	Reason string
}

func routeScoringSegment(questionType string, hasPublishedRule bool, hasRecordedAnswer bool, optionRegionCount int) scoringSegmentRoute {
	isOMR := questionType == "single_choice" || questionType == "true_false" || questionType == "multiple_choice"
	isTextRule := questionType == "fill_blank" || questionType == "numeric"
	if isTextRule && hasPublishedRule && hasRecordedAnswer {
		return scoringSegmentRoute{Kind: scoringRouteRuleInput}
	}
	if isOMR && hasPublishedRule && optionRegionCount >= 2 {
		return scoringSegmentRoute{Kind: scoringRouteOMR}
	}
	if !paper.HasAutomatedScoringPath(questionType) {
		return manualOnlyQuestionTypeRoute()
	}
	reason := reviewReasonRuleReviewRequired
	if isOMR && optionRegionCount < 2 {
		reason = "omr_option_regions_missing"
	}
	if isOMR && !hasPublishedRule {
		reason = "scoring_rule_missing"
	}
	return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceRuleReview, Reason: reason}
}

// The frozen question policy takes precedence over the presence of a rule.
// A configured human path must not silently become an automatic score just
// because a rule was published for the same question.
func routeScoringSegmentForMode(questionType, mode string, hasPublishedRule, hasRecordedAnswer bool, optionRegionCount int) scoringSegmentRoute {
	switch assessment.ScoringMode(mode) {
	case assessment.ScoringHumanPrimary, assessment.ScoringDualHuman, assessment.ScoringManualOnly:
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "frozen_scoring_policy_requires_human"}
	case assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm:
		if questionType == "short_answer" || questionType == "calculation" || questionType == "essay" || questionType == "discussion" {
			return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceRuleReview, Reason: "ai_assist_teacher_confirmation"}
		}
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "ai_mode_unsupported_question_type"}
	case assessment.ScoringRuleAuto:
		return routeScoringSegment(questionType, hasPublishedRule, hasRecordedAnswer, optionRegionCount)
	default:
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "frozen_scoring_policy_unavailable"}
	}
}

// scoringRunStatusForCounts is the run state a start produces. A run whose
// segments all became human tasks still started successfully: only a run with
// nothing to score is a failure.
func scoringRunStatusForCounts(total int, queued int, review int) string {
	if total == 0 {
		return "failed"
	}
	if queued == 0 && review > 0 {
		return "needs_review"
	}
	return "processing"
}

func manualOnlyQuestionTypeRoute() scoringSegmentRoute {
	return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: reviewReasonManualOnlyQuestionType}
}

// manualReviewRouteForGradeError keeps a question type the rule engine cannot
// score from failing the whole scoring run. Every other grading error is still
// fatal: it means the run itself is unhealthy, not that this one segment needs a
// human.
func manualReviewRouteForGradeError(err error, questionType string) (scoringSegmentRoute, bool) {
	if !errors.Is(err, ErrUnsupportedQuestionType) {
		return scoringSegmentRoute{}, false
	}
	if !paper.HasAutomatedScoringPath(questionType) {
		return manualOnlyQuestionTypeRoute(), true
	}
	return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: reviewReasonRuleGradingUnsupported}, true
}
