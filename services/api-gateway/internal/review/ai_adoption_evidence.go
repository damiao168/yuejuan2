package review

import "math"

// 只记录当前上下文仍有效的真实模型建议。识别或建议在交分前更新时，保留教师分数但不把它计入采纳率。
func recordedAIAdoption(input SubmitGradeInput, taskContext Context) *AIAdoption {
	adoption := input.AIAdoption
	if adoption == nil || adoption.AIGradeID == "" || input.AutoSubmit {
		return nil
	}
	eligible := func(suggestion map[string]any) bool {
		score, hasScore := suggestion["suggested_score"].(float64)
		return suggestion["ai_grade_id"] == adoption.AIGradeID && suggestion["grader_type"] == "llm_subjective" &&
			suggestion["status"] == "succeeded" && suggestion["mock"] == false &&
			(suggestion["delivery_mode"] == "teacher_suggestion" || suggestion["delivery_mode"] == "teacher_review") &&
			hasScore && (!adoption.Accepted || math.Abs(input.Score-score) < 0.000001)
	}
	if eligible(taskContext.AISuggestion) {
		return adoption
	}
	history, _ := taskContext.AISuggestion["suggestion_history"].([]any)
	for _, candidate := range history {
		suggestion, _ := candidate.(map[string]any)
		if eligible(suggestion) {
			return adoption
		}
	}
	return nil
}
