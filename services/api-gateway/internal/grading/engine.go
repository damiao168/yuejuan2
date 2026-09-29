package grading

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
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
	if setEqual(expected, actual) {
		grade.SuggestedScore = ctx.Question.Score
		grade.MatchedPoints = append(grade.MatchedPoints, point("multiple_choice_all_correct", "all selected options match answer key", ctx.Question.Score))
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

func (e *Engine) gradeFillBlank(ctx Context) Grade {
	grade := e.baseGrade(ctx, "fill_blank_match")
	actual := normalizeFill(answerScalar(ctx.Answer), ctx.AnswerKey.Tolerance)
	candidates := append([]any{ctx.AnswerKey.StandardAnswer}, ctx.AnswerKey.EquivalentAnswers...)
	for _, candidate := range candidates {
		if actual != "" && actual == normalizeFill(stringValue(candidate), ctx.AnswerKey.Tolerance) {
			grade.Confidence = 0.95
			grade.SuggestedScore = ctx.Question.Score
			grade.MatchedPoints = append(grade.MatchedPoints, point("fill_blank_match", "answer matches standard or equivalent answer", ctx.Question.Score))
			return finalize(grade)
		}
	}
	grade.Confidence = 0.9
	grade.MissingPoints = append(grade.MissingPoints, point("fill_blank_match", "answer does not match standard or equivalent answer", ctx.Question.Score))
	return finalize(grade)
}

func (e *Engine) gradeNumeric(ctx Context) Grade {
	grade := e.baseGrade(ctx, "numeric_tolerance")
	expected, expectedUnit, expectedOK := numericValue(ctx.AnswerKey.StandardAnswer)
	actual, actualUnit, actualOK := numericAnswer(ctx.Answer)
	if !expectedOK || !actualOK {
		grade.Confidence = 0.6
		grade.RiskFlags = append(grade.RiskFlags, "numeric_parse_failed")
		grade.MissingPoints = append(grade.MissingPoints, point("numeric_parse", "numeric answer or answer key could not be parsed", ctx.Question.Score))
		return finalize(grade)
	}
	if boolConfig(ctx.AnswerKey.Tolerance, "unit_required", false) && expectedUnit != "" && normalizeUnit(actualUnit) != normalizeUnit(expectedUnit) {
		grade.Confidence = 0.9
		grade.MissingPoints = append(grade.MissingPoints, point("numeric_unit", "unit does not match answer key", ctx.Question.Score))
		return finalize(grade)
	}
	tolerance := floatConfig(ctx.AnswerKey.Tolerance, "absolute", math.NaN())
	if math.IsNaN(tolerance) {
		tolerance = floatConfig(ctx.AnswerKey.Tolerance, "value", 0)
	}
	relative := floatConfig(ctx.AnswerKey.Tolerance, "relative", 0)
	// 绝对误差与相对误差取较宽者，相对误差以标准答案绝对值为基准，不相加。
	allowedDifference := tolerance
	if relative > 0 {
		allowedDifference = math.Max(allowedDifference, math.Abs(expected)*relative)
	}
	if math.Abs(actual-expected) <= allowedDifference {
		grade.Confidence = 0.95
		grade.SuggestedScore = ctx.Question.Score
		grade.MatchedPoints = append(grade.MatchedPoints, point("numeric_tolerance", fmt.Sprintf("numeric answer within tolerance %.6g", allowedDifference), ctx.Question.Score))
	} else {
		grade.Confidence = 0.95
		grade.MissingPoints = append(grade.MissingPoints, point("numeric_tolerance", fmt.Sprintf("numeric answer outside tolerance %.6g", allowedDifference), ctx.Question.Score))
	}
	return finalize(grade)
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

// 默认只做首尾空白及 NFKC 归一化；忽略大小写、内部空白、标点都需要规则显式开启。
func normalizeFill(value string, tolerance any) string {
	value = norm.NFKC.String(strings.TrimSpace(value))
	if boolConfig(tolerance, "ignore_case", false) {
		value = strings.ToLower(value)
	}
	if boolConfig(tolerance, "ignore_spaces", false) {
		value = strings.Join(strings.Fields(value), "")
	}
	if boolConfig(tolerance, "ignore_punctuation", false) {
		value = strings.Map(func(r rune) rune {
			if unicode.IsPunct(r) {
				return -1
			}
			return r
		}, value)
	}
	return value
}

func numericAnswer(answer SegmentAnswer) (float64, string, bool) {
	if value, ok := answer.AnswerPayload["value"]; ok {
		number, ok := floatValue(value)
		return number, stringValue(answer.AnswerPayload["unit"]), ok
	}
	return numericFromString(answer.AnswerText)
}

func numericValue(value any) (float64, string, bool) {
	if payload, ok := value.(map[string]any); ok {
		number, ok := floatValue(payload["value"])
		return number, stringValue(payload["unit"]), ok
	}
	return numericFromString(stringValue(value))
}

func numericFromString(value string) (float64, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, "", false
	}
	if slash := strings.Index(value, "/"); slash > 0 {
		left := strings.TrimSpace(value[:slash])
		rest := strings.TrimSpace(value[slash+1:])
		denominatorText := numericPrefixPattern.FindString(rest)
		numerator, numeratorErr := strconv.ParseFloat(left, 64)
		denominator, denominatorErr := strconv.ParseFloat(denominatorText, 64)
		if numeratorErr == nil && denominatorErr == nil && denominator != 0 {
			return numerator / denominator, strings.TrimSpace(strings.TrimPrefix(rest, denominatorText)), true
		}
	}
	match := numericPrefixPattern.FindString(value)
	if match == "" {
		return 0, "", false
	}
	number, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, "", false
	}
	unit := strings.TrimSpace(strings.TrimPrefix(value, match))
	if strings.HasPrefix(unit, "%") {
		number /= 100
		unit = strings.TrimSpace(strings.TrimPrefix(unit, "%"))
	}
	return number, unit, true
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

func normalizeUnit(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
}

func stringify(value any) string {
	return stringValue(value)
}

func hasReviewRisk(flags []string) bool {
	for _, flag := range flags {
		switch flag {
		case "true_false_parse_failed", "multiple_choice_empty_answer", "numeric_parse_failed":
			return true
		case "answer_confidence_missing", "answer_low_confidence":
			return true
		}
	}
	return false
}
