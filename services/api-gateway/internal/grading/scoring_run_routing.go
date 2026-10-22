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
	scoringRouteObserveRule  = "observe_rule"
	scoringRouteImageAI      = "image_ai"

	// reviewSourceHumanGraded 标记设计上由人工评分的工作，区别于等待确认的自动结果。
	reviewSourceHumanGraded = "subjective_default_review"
	reviewSourceRuleReview  = "rule_review_required"

	reviewReasonRuleReviewRequired = "rule_review_required"
	reviewReasonRuleNotConfirmed   = "rule_not_auto_confirmed"

	// reviewReasonManualOnlyQuestionType 标记题型完全没有自动评分路径的片段。
	// 试卷配置接受这些题型，就绪门禁将其作为提示报告，因此评分运行应转交人工，不能视为评分缺陷。
	reviewReasonManualOnlyQuestionType = "manual_only_question_type"
	// reviewReasonRuleGradingUnsupported 标记规则引擎未实现、但其他自动路径支持的题型评分输入。
	reviewReasonRuleGradingUnsupported = "rule_grading_unsupported"
)

// scoringSegmentRoute 是单个答案片段的持久化后续动作：排入自动处理队列，或创建带有可操作原因的人工任务。
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
	if isTextRule && hasPublishedRule {
		return scoringSegmentRoute{Kind: scoringRouteObserveRule}
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

// 冻结的题目策略优先于规则是否存在；不能仅因同一题目发布了规则，就将已配置的人工路径静默改成自动评分。
func routeScoringSegmentForMode(questionType, mode string, hasPublishedRule, hasRecordedAnswer bool, optionRegionCount int) scoringSegmentRoute {
	switch assessment.ScoringMode(mode) {
	case assessment.ScoringHumanPrimary, assessment.ScoringDualHuman, assessment.ScoringManualOnly:
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "frozen_scoring_policy_requires_human"}
	case assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm:
		if questionType == "short_answer" || questionType == "calculation" || questionType == "essay" || questionType == "discussion" {
			return scoringSegmentRoute{Kind: scoringRouteImageAI}
		}
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "ai_mode_unsupported_question_type"}
	case assessment.ScoringRuleAuto:
		return routeScoringSegment(questionType, hasPublishedRule, hasRecordedAnswer, optionRegionCount)
	default:
		return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: "frozen_scoring_policy_unavailable"}
	}
}

func routeScoringSegmentForScope(questionType, mode, answerScope string, hasPublishedRule, hasRecordedAnswer bool, optionRegionCount int) scoringSegmentRoute {
	// 共区先获取成员映射，不要求教师补子 ROI；每题仍独立按自己的冻结规则判分。
	if answerScope == "grading_group" && mode == "RULE_AUTO" && hasPublishedRule && (questionType == "fill_blank" || questionType == "numeric") {
		return scoringSegmentRoute{Kind: scoringRouteObserveRule}
	}
	return routeScoringSegmentForMode(questionType, mode, hasPublishedRule, hasRecordedAnswer, optionRegionCount)
}

// scoringRunStatusForCounts 表示启动操作产生的运行状态。所有片段都转成人工任务的运行仍算启动成功；只有无可评分内容的运行才失败。
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

// manualReviewRouteForGradeError 防止规则引擎不支持的题型导致整个评分运行失败。
// 其他评分错误仍是致命错误，表示运行本身异常，不只是某个片段需要人工处理。
func manualReviewRouteForGradeError(err error, questionType string) (scoringSegmentRoute, bool) {
	if !errors.Is(err, ErrUnsupportedQuestionType) {
		return scoringSegmentRoute{}, false
	}
	if !paper.HasAutomatedScoringPath(questionType) {
		return manualOnlyQuestionTypeRoute(), true
	}
	return scoringSegmentRoute{Kind: scoringRouteManualReview, Source: reviewSourceHumanGraded, Reason: reviewReasonRuleGradingUnsupported}, true
}
