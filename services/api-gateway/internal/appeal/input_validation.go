package appeal

import (
	"strings"
)

func normalizeCreateInput(input CreateAppealInput) CreateAppealInput {
	input.ExamID = strings.TrimSpace(input.ExamID)
	input.StudentID = strings.TrimSpace(input.StudentID)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.FinalGradeID = strings.TrimSpace(input.FinalGradeID)
	input.DeductionPointID = strings.TrimSpace(input.DeductionPointID)
	input.Reason = strings.TrimSpace(input.Reason)
	return input
}

func validateCreateInput(input CreateAppealInput) error {
	if input.ExamID == "" || input.StudentID == "" || input.Reason == "" || !validTargetType(input.TargetType) {
		return ErrInvalidInput
	}
	if input.TargetType != "exam" && input.FinalGradeID == "" {
		return ErrInvalidInput
	}
	if input.TargetType == "deduction_point" && input.DeductionPointID == "" {
		return ErrInvalidInput
	}
	return nil
}

func normalizeReviewInput(input ReviewAppealInput) ReviewAppealInput {
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	input.FinalGradeID = strings.TrimSpace(input.FinalGradeID)
	return input
}

func normalizeRecommendationInput(input SubmitRecommendationInput) SubmitRecommendationInput {
	input.Recommendation = strings.TrimSpace(input.Recommendation)
	input.Reason = strings.TrimSpace(input.Reason)
	return input
}

func validateRecommendationInput(input SubmitRecommendationInput) error {
	switch input.Recommendation {
	case "accept", "reject", "need_more_info":
		if input.RecommendedScore != nil {
			return ErrInvalidInput
		}
	case "adjust_score":
		if input.RecommendedScore == nil || *input.RecommendedScore < 0 {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if input.Reason == "" {
		return ErrInvalidInput
	}
	return nil
}

func validateReviewInput(input ReviewAppealInput) error {
	if !validStatus(input.Status) || input.Status == "submitted" || input.Status == "closed" {
		return ErrInvalidInput
	}
	if input.Status != "under_review" && input.Reason == "" {
		return ErrInvalidInput
	}
	if input.Status == "score_adjusted" && input.AdjustedScore == nil {
		return ErrInvalidInput
	}
	return nil
}

func canReviewTransition(current string, next string) bool {
	switch current {
	case "submitted":
		return next == "under_review" || next == "need_more_info" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	case "under_review":
		return next == "under_review" || next == "need_more_info" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	case "need_more_info":
		return next == "need_more_info" || next == "under_review" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	default:
		return false
	}
}

func isTerminalStatus(status string) bool {
	return status == "accepted" || status == "rejected" || status == "score_adjusted" || status == "closed"
}

func validTargetType(value string) bool {
	switch value {
	case "exam", "question", "deduction_point":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	for _, status := range Statuses() {
		if value == status {
			return true
		}
	}
	return false
}
