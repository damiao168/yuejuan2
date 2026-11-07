package grading

import (
	"math"
	"sort"
	"strings"
)

func (e *Engine) gradeSingleChoice(ctx Context) Grade {
	grade := e.baseGrade(ctx, "single_choice_exact")
	expected := normalizeChoice(stringValue(ctx.AnswerKey.StandardAnswer))
	actual := normalizeChoice(answerScalar(ctx.Answer))
	if actual == expected && actual != "" {
		grade.SuggestedScore = ctx.Question.Score
		grade.MatchedPoints = append(grade.MatchedPoints, point("choice_exact", "selected option matches answer key", ctx.Question.Score))
	} else {
		grade.MissingPoints = append(grade.MissingPoints, point("choice_exact", "selected option does not match answer key", ctx.Question.Score))
	}
	return finalize(grade)
}

func (e *Engine) gradeTrueFalse(ctx Context) Grade {
	grade := e.baseGrade(ctx, "true_false_exact")
	expected, expectedOK := boolValue(ctx.AnswerKey.StandardAnswer)
	actual, actualOK := boolValue(answerScalar(ctx.Answer))
	if !actualOK {
		grade.Confidence = 0.6
		grade.RiskFlags = append(grade.RiskFlags, "true_false_parse_failed")
		grade.MissingPoints = append(grade.MissingPoints, point("true_false_exact", "student answer could not be parsed as true/false", ctx.Question.Score))
		return finalize(grade)
	}
	if expectedOK && actual == expected {
		grade.SuggestedScore = ctx.Question.Score
		grade.MatchedPoints = append(grade.MatchedPoints, point("true_false_exact", "true/false answer matches answer key", ctx.Question.Score))
	} else {
		grade.MissingPoints = append(grade.MissingPoints, point("true_false_exact", "true/false answer does not match answer key", ctx.Question.Score))
	}
	return finalize(grade)
}

func (e *Engine) gradeMultipleChoice(ctx Context) Grade {
	grade := e.baseGrade(ctx, "multiple_choice_set")
	expected := normalizeChoiceSet(stringSlice(ctx.AnswerKey.StandardAnswer))
	actual := normalizeChoiceSet(answerList(ctx.Answer))
	if len(expected) == 0 || len(actual) == 0 {
		grade.Confidence = 0.6
		grade.RiskFlags = append(grade.RiskFlags, "multiple_choice_empty_answer")
		grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_set", "expected or student options are empty", ctx.Question.Score))
		return finalize(grade)
	}
	config := toleranceMap(ctx.AnswerKey.Tolerance)
	partialRaw, hasPartial := config["partial_score"]
	wrongRaw, hasWrong := config["wrong_answer_score"]
	partialScore, partialOK := floatValue(partialRaw)
	wrongScore, wrongOK := floatValue(wrongRaw)
	fixedScores := hasPartial || hasWrong
	if fixedScores && (!hasPartial || !hasWrong || !partialOK || !wrongOK ||
		math.IsNaN(partialScore) || math.IsInf(partialScore, 0) || partialScore < 0 || partialScore > ctx.Question.Score ||
		math.IsNaN(wrongScore) || math.IsInf(wrongScore, 0) || wrongScore < 0 || wrongScore > ctx.Question.Score) {
		grade.RiskFlags = append(grade.RiskFlags, "multiple_choice_policy_invalid")
		return finalize(grade)
	}
	if setEqual(expected, actual) {
		grade.SuggestedScore = ctx.Question.Score
		grade.MatchedPoints = append(grade.MatchedPoints, point("multiple_choice_all_correct", "all selected options match answer key", ctx.Question.Score))
		return finalize(grade)
	}
	// 固定得分只区分全对、少选无错选、有错选；一旦明确采用此规则，不叠加历史每项分值或罚分。
	if fixedScores {
		if hasWrongOption(expected, actual) {
			grade.SuggestedScore = wrongScore
			grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_wrong_option", "student selected at least one wrong option", ctx.Question.Score-wrongScore))
		} else {
			grade.SuggestedScore = partialScore
			grade.MatchedPoints = append(grade.MatchedPoints, point("multiple_choice_partial", "student selected a correct subset", partialScore))
			grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_missing_options", "student missed one or more correct options", ctx.Question.Score-partialScore))
		}
		return finalize(grade)
	}
	// 错选先按每个正确项得分减去每个错误项罚分；allow_partial 只控制后面的纯漏选分支。
	if hasWrongOption(expected, actual) {
		penalty := floatConfig(ctx.AnswerKey.Tolerance, "wrong_option_penalty", ctx.Question.Score)
		if penalty < 0 {
			penalty = 0
		}
		correctCount := matchingOptionCount(expected, actual)
		perOption := floatConfig(ctx.AnswerKey.Tolerance, "score_per_correct_option", 0)
		if perOption > 0 {
			grade.SuggestedScore = math.Max(0, float64(correctCount)*perOption-penalty*float64(len(actual)-correctCount))
		}
		grade.SuggestedScore = math.Max(grade.SuggestedScore, floatConfig(ctx.AnswerKey.Tolerance, "minimum_score", 0))
		grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_wrong_option", "student selected at least one wrong option", math.Min(ctx.Question.Score, penalty)))
		return finalize(grade)
	}
	if boolConfig(ctx.AnswerKey.Tolerance, "allow_partial", false) {
		score := floatConfig(ctx.AnswerKey.Tolerance, "score_per_correct_option", 0) * float64(len(actual))
		if score == 0 {
			score = ctx.Question.Score * float64(len(actual)) / float64(len(expected))
		}
		score = math.Max(score, floatConfig(ctx.AnswerKey.Tolerance, "minimum_score", 0))
		grade.SuggestedScore = score
		grade.MatchedPoints = append(grade.MatchedPoints, point("multiple_choice_partial", "student selected a correct subset", score))
		grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_missing_options", "student missed one or more correct options", ctx.Question.Score-score))
		return finalize(grade)
	}
	grade.MissingPoints = append(grade.MissingPoints, point("multiple_choice_incomplete", "student selected only a subset and partial scoring is disabled", ctx.Question.Score))
	return finalize(grade)
}

func answerList(answer SegmentAnswer) []string {
	if value, ok := answer.AnswerPayload["answers"]; ok {
		return stringSlice(value)
	}
	return splitAnswerList(answer.AnswerText)
}

func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	case []any:
		out := []string{}
		for _, item := range v {
			out = append(out, stringValue(item))
		}
		return out
	default:
		return splitAnswerList(stringValue(value))
	}
}

func splitAnswerList(value string) []string {
	value = strings.NewReplacer("，", ",", "；", ",", ";", ",", "|", ",", " ", ",").Replace(value)
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeChoice(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeChoiceSet(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = normalizeChoice(value)
		// 识别资料常将多选答案写成 BC；与 [B,C] 使用同一选项集合，不能把 BC 当成一个选项。
		if len(value) > 1 && strings.IndexFunc(value, func(r rune) bool { return r < 'A' || r > 'Z' }) == -1 {
			for _, option := range value {
				label := string(option)
				if !seen[label] {
					seen[label] = true
					out = append(out, label)
				}
			}
			continue
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func setEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasWrongOption(expected []string, actual []string) bool {
	allowed := map[string]bool{}
	for _, value := range expected {
		allowed[value] = true
	}
	for _, value := range actual {
		if !allowed[value] {
			return true
		}
	}
	return false
}

func matchingOptionCount(expected []string, actual []string) int {
	allowed := map[string]bool{}
	for _, value := range expected {
		allowed[value] = true
	}
	count := 0
	for _, value := range actual {
		if allowed[value] {
			count++
		}
	}
	return count
}
