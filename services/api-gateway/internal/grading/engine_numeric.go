package grading

import (
	"fmt"
	"golang.org/x/text/unicode/norm"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

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

// 默认只做首尾空白及 NFKC 归一化；忽略大小写、内部空白、标点都需要规则显式开启。
func normalizeFill(value string, tolerance any) string {
	value = norm.NFKC.String(strings.TrimSpace(value))
	value = canonicalFillMath(value)
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

var simpleMathFraction = regexp.MustCompile(`^([+-]?)\\(?:dfrac|tfrac|frac)\{([+-]?\d+)\}\{([+-]?\d+)\}$`)

// 去掉整段数学内容的展示包装，并统一简单数字分式的书写格式；不求值或放宽数学条件。
// 端点括号、负号和变量必须保留；0.5 与 1/2 是否可互换仍由等价答案配置决定。
func canonicalFillMath(value string) string {
	for _, pair := range [][2]string{{"$$", "$$"}, {"$", "$"}, {`\(`, `\)`}, {`\[`, `\]`}} {
		if len(value) > len(pair[0])+len(pair[1]) && strings.HasPrefix(value, pair[0]) && strings.HasSuffix(value, pair[1]) {
			value = strings.TrimSpace(value[len(pair[0]) : len(value)-len(pair[1])])
			break
		}
	}
	value = strings.NewReplacer(`\left`, "", `\right`, "").Replace(value)
	if fraction := simpleMathFraction.FindStringSubmatch(value); fraction != nil {
		return fraction[1] + fraction[2] + "/" + fraction[3]
	}
	return value
}

func numericAnswer(answer SegmentAnswer) (float64, string, bool) {
	if value, ok := answer.AnswerPayload["value"]; ok {
		number, ok := floatValue(value)
		// JSON 字符串仍可能表示 NaN/Inf；结构化输入与纯文本使用相同的有限数边界。
		return number, stringValue(answer.AnswerPayload["unit"]), ok && finiteNumeric(number)
	}
	return numericFromString(answer.AnswerText)
}

func numericValue(value any) (float64, string, bool) {
	if payload, ok := value.(map[string]any); ok {
		number, ok := floatValue(payload["value"])
		return number, stringValue(payload["unit"]), ok && finiteNumeric(number)
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
		numerator, numeratorErr := strconv.ParseFloat(left, 64)
		if numeratorErr == nil {
			// 一旦左侧已经是数字，斜杠就表示分式；分母无效或为零时必须拒绝整项，
			// 不能退回前缀解析把“1/0”当成数字 1 并把“/0”吞进单位。
			denominatorText := numericPrefixPattern.FindString(rest)
			denominator, denominatorErr := strconv.ParseFloat(denominatorText, 64)
			if denominatorText == "" || denominatorErr != nil || denominator == 0 || !finiteNumeric(numerator) || !finiteNumeric(denominator) {
				return 0, "", false
			}
			unit := strings.TrimSpace(strings.TrimPrefix(rest, denominatorText))
			result := numerator / denominator
			if !finiteNumeric(result) || !numericUnitSuffix(unit) {
				return 0, "", false
			}
			return result, unit, true
		}
	}
	match := numericPrefixPattern.FindString(value)
	if match == "" {
		return 0, "", false
	}
	number, err := strconv.ParseFloat(match, 64)
	if err != nil || !finiteNumeric(number) {
		return 0, "", false
	}
	unit := strings.TrimSpace(strings.TrimPrefix(value, match))
	if strings.HasPrefix(unit, "%") {
		number /= 100
		unit = strings.TrimSpace(strings.TrimPrefix(unit, "%"))
	}
	if !numericUnitSuffix(unit) {
		return 0, "", false
	}
	return number, unit, true
}

func finiteNumeric(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func numericUnitSuffix(unit string) bool {
	if unit == "" {
		return true
	}
	// 数值后剩余的算术符号不能当作单位；单位中的 m/s2 等写法仍从字母或度数符号开始。
	for _, first := range unit {
		return unicode.IsLetter(first) || first == '°' || first == '℃' || first == '℉'
	}
	return false
}

func normalizeUnit(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
}
