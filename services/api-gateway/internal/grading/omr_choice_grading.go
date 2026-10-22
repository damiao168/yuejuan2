package grading

import "math"

// gradeConfirmedOMRChoice 只由已通过服务端资格和逐题质量门禁的 OMR 确认分支调用。
// 规则强度保留识别原值；单选、多选按本次冻结阈值决定自动接受，其他来源和题型继续使用通用门槛。
func (e *Engine) gradeConfirmedOMRChoice(ctx Context, minimumConfidence float64) (Grade, error) {
	if ctx.Answer.Source != "omr" || (ctx.Question.QuestionType != "single_choice" && ctx.Question.QuestionType != "multiple_choice") {
		return e.Grade(ctx)
	}
	if math.IsNaN(minimumConfidence) || math.IsInf(minimumConfidence, 0) || minimumConfidence < 0 || minimumConfidence > 1 {
		return Grade{}, ErrInvalidInput
	}
	var grade Grade
	switch {
	case ctx.Answer.AnswerPayload["presence"] == "blank":
		// 清晰空白已由上游质量分支确认，按冻结题分记 0；不能当成多选答案解析失败。
		grade = finalize(e.baseGrade(ctx, "omr_explicit_blank"))
	case ctx.Question.QuestionType == "single_choice":
		grade = e.gradeSingleChoice(ctx)
	default:
		grade = e.gradeMultipleChoice(ctx)
	}
	if ctx.Answer.Confidence == nil {
		grade.RiskFlags = appendUnique(grade.RiskFlags, "answer_confidence_missing")
	} else {
		confidence := *ctx.Answer.Confidence
		if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
			return Grade{}, ErrInvalidInput
		}
		grade.Confidence = confidence
		if confidence < minimumConfidence {
			grade.RiskFlags = appendUnique(grade.RiskFlags, "answer_low_confidence")
		}
	}
	// 只替换输入接受阈值，不清除规则比较本身发现的缺失、解析失败等风险。
	grade.NeedsHumanReview = grade.Confidence < minimumConfidence || hasReviewRisk(grade.RiskFlags)
	grade.AutoPass = !grade.NeedsHumanReview && grade.Confidence >= minimumConfidence
	return grade, nil
}
