package grading

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var numericPrefixPattern = regexp.MustCompile(`^[-+]?((\d+(\.\d*)?)|(\.\d+))([eE][-+]?\d+)?`)

type Engine struct{}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Grade(ctx Context) (Grade, error) {
	var grade Grade
	switch ctx.Question.QuestionType {
	case "single_choice":
		grade = e.gradeSingleChoice(ctx)
	case "true_false":
		grade = e.gradeTrueFalse(ctx)
	case "multiple_choice":
		grade = e.gradeMultipleChoice(ctx)
	case "fill_blank":
		grade = e.gradeFillBlank(ctx)
	case "numeric":
		grade = e.gradeNumeric(ctx)
	default:
		return Grade{}, ErrUnsupportedQuestionType
	}
	return applyInputConfidence(ctx, grade), nil
}

func (e *Engine) baseGrade(ctx Context, rule string) Grade {
	return Grade{
		TenantID:        ctx.Answer.TenantID,
		AnswerSegmentID: ctx.SegmentID,
		QuestionID:      ctx.Question.ID,
		QuestionNo:      ctx.Question.QuestionNo,
		QuestionType:    ctx.Question.QuestionType,
		AnswerVersion:   ctx.AnswerKey.AnswerVersion,
		GraderType:      GraderType,
		RuleVersion:     RuleVersion,
		MaxScore:        ctx.Question.Score,
		Confidence:      0.99,
		MatchedPoints:   []PointResult{},
		MissingPoints:   []PointResult{},
		Evidence: []Evidence{{
			Type:           "answer_segment_answer",
			AnswerSegment:  ctx.SegmentID,
			AnswerText:     ctx.Answer.AnswerText,
			StandardAnswer: stringify(ctx.AnswerKey.StandardAnswer),
			Rule:           rule,
		}},
		RiskFlags: []string{},
		Mock:      false,
		RawOutput: map[string]any{
			"rule_version": RuleVersion,
			"grader_type":  GraderType,
		},
	}
}

func finalize(grade Grade) Grade {
	if grade.SuggestedScore < 0 {
		grade.SuggestedScore = 0
	}
	if grade.SuggestedScore > grade.MaxScore {
		grade.SuggestedScore = grade.MaxScore
	}
	if grade.Confidence < 0 {
		grade.Confidence = 0
	}
	if grade.Confidence > 1 {
		grade.Confidence = 1
	}
	grade.NeedsHumanReview = grade.Confidence < 0.8 || hasReviewRisk(grade.RiskFlags)
	grade.AutoPass = !grade.NeedsHumanReview && grade.Confidence >= 0.9
	return grade
}

func point(code string, label string, score float64) PointResult {
	return PointResult{Code: code, Label: label, Score: score}
}

func answerScalar(answer SegmentAnswer) string {
	if value, ok := answer.AnswerPayload["answer"]; ok {
		return stringValue(value)
	}
	return answer.AnswerText
}

// 规则匹配的置信度不能高于输入置信度；机器答案缺少置信度会转人工，任意来源低于 0.9 也会留下复核风险。
func applyInputConfidence(ctx Context, grade Grade) Grade {
	if ctx.Answer.Confidence == nil {
		if ctx.Answer.Source != "manual_entry" && ctx.Answer.Source != "imported_answer" {
			grade.RiskFlags = appendUnique(grade.RiskFlags, "answer_confidence_missing")
		}
	} else {
		if *ctx.Answer.Confidence < grade.Confidence {
			grade.Confidence = *ctx.Answer.Confidence
		}
		if *ctx.Answer.Confidence < 0.9 {
			grade.RiskFlags = appendUnique(grade.RiskFlags, "answer_low_confidence")
		}
	}
	grade.NeedsHumanReview = grade.Confidence < 0.8 || hasReviewRisk(grade.RiskFlags)
	grade.AutoPass = !grade.NeedsHumanReview && grade.Confidence >= 0.9
	return grade
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func boolValue(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	default:
		text := strings.ToLower(strings.TrimSpace(stringValue(value)))
		switch text {
		case "true", "t", "yes", "y", "1", "正确", "对":
			return true, true
		case "false", "f", "no", "n", "0", "错误", "错":
			return false, true
		default:
			return false, false
		}
	}
}

func stringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case float64:
		if math.Trunc(v) == v {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func floatValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func toleranceMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{}
}

func boolConfig(config any, key string, fallback bool) bool {
	value, ok := toleranceMap(config)[key]
	if !ok {
		return fallback
	}
	if result, ok := value.(bool); ok {
		return result
	}
	parsed, ok := boolValue(value)
	if !ok {
		return fallback
	}
	return parsed
}

func floatConfig(config any, key string, fallback float64) float64 {
	value, ok := toleranceMap(config)[key]
	if !ok {
		return fallback
	}
	if result, ok := floatValue(value); ok {
		return result
	}
	return fallback
}

func stringify(value any) string {
	return stringValue(value)
}

func hasReviewRisk(flags []string) bool {
	for _, flag := range flags {
		switch flag {
		case "true_false_parse_failed", "multiple_choice_empty_answer", "multiple_choice_policy_invalid", "numeric_parse_failed":
			return true
		case "answer_confidence_missing", "answer_low_confidence":
			return true
		}
	}
	return false
}
