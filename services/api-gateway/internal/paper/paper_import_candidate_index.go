package paper

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func paperImportQuestionNumbersWithinBounds(values ...string) bool {
	for _, value := range values {
		if utf8.RuneCountInString(value) > maxPaperImportQuestionNumberChars {
			return false
		}
		compact := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, value)
		compact = strings.NewReplacer("第", "", "题", "").Replace(compact)
		for _, digits := range paperImportNumberDigits.FindAllString(compact, -1) {
			if _, err := strconv.ParseUint(digits, 10, 63); err != nil {
				return false
			}
		}
	}
	return true
}

type paperImportExistingQuestion struct {
	id, number, kind string
	score            float64
	sortOrder        int
}

func normalizePaperImportQuestionNumber(value string) string {
	// 与Python校验使用同一长度和非负int64边界；溢出不能被当成最大整数自动匹配。
	if !paperImportQuestionNumbersWithinBounds(value) {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	value = strings.NewReplacer("第", "", "题", "", "（", "(", "）", ")", "．", ".", "。", ".", "）", ")").Replace(value)
	value = strings.TrimRight(value, ".、:：")
	for len(value) >= 2 && strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if normalized, ok := normalizeChineseQuestionNumber(value); ok {
		return normalized
	}
	if matched := regexp.MustCompile(`^0*(\d+)\(0*(\d+)\)$`).FindStringSubmatch(value); matched != nil {
		parent, parentErr := strconv.ParseInt(matched[1], 10, 64)
		child, childErr := strconv.ParseInt(matched[2], 10, 64)
		if parentErr != nil || childErr != nil {
			return ""
		}
		return fmt.Sprintf("%d(%d)", parent, child)
	}
	if matched := regexp.MustCompile(`^0*(\d+)\.0*(\d+)$`).FindStringSubmatch(value); matched != nil {
		parent, parentErr := strconv.ParseInt(matched[1], 10, 64)
		child, childErr := strconv.ParseInt(matched[2], 10, 64)
		if parentErr != nil || childErr != nil {
			return ""
		}
		return fmt.Sprintf("%d(%d)", parent, child)
	}
	if matched := regexp.MustCompile(`^0*(\d+)\)?$`).FindStringSubmatch(value); matched != nil {
		parent, err := strconv.ParseInt(matched[1], 10, 64)
		if err != nil {
			return ""
		}
		return strconv.FormatInt(parent, 10)
	}
	return strings.ToLower(value)
}

func normalizeChineseQuestionNumber(value string) (string, bool) {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	if value == "十" {
		return "10", true
	}
	runes := []rune(value)
	switch len(runes) {
	case 1:
		value, ok := digits[runes[0]]
		if ok && value > 0 {
			return strconv.Itoa(value), true
		}
	case 2:
		return normalizeChineseQuestionTens(runes, digits)
	case 3:
		if runes[1] != '十' {
			return "", false
		}
		tens, tensOK := digits[runes[0]]
		ones, onesOK := digits[runes[2]]
		if tensOK && onesOK && tens > 0 {
			return strconv.Itoa(tens*10 + ones), true
		}
	}
	return "", false
}

func normalizeChineseQuestionTens(runes []rune, digits map[rune]int) (string, bool) {
	if runes[0] == '十' {
		value, ok := digits[runes[1]]
		if ok && value > 0 {
			return strconv.Itoa(10 + value), true
		}
	}
	if runes[1] == '十' {
		value, ok := digits[runes[0]]
		if ok && value > 0 {
			return strconv.Itoa(value * 10), true
		}
	}
	return "", false
}

// 题号回写到输入切片，后续保存候选时继续使用同一规范化结果。
func indexPaperImportQuestions(questions []QuestionCandidate, issues *[]PaperImportIssue) map[string][]int {
	questionByNo := map[string][]int{}
	for i := range questions {
		if !paperImportQuestionNumbersWithinBounds(questions[i].QuestionNoRaw, questions[i].QuestionNoNormalized, questions[i].ParentQuestionNo, questions[i].SubquestionNo) {
			questions[i].QuestionNoNormalized = ""
			*issues = append(*issues, invalidPaperImportQuestionNumberIssue(questions[i].SourceRefs))
			continue
		}
		questions[i].QuestionNoNormalized = normalizePaperImportQuestionNumber(firstNonEmpty(questions[i].QuestionNoNormalized, questions[i].QuestionNoRaw))
		if questions[i].QuestionNoNormalized != "" {
			questionByNo[questions[i].QuestionNoNormalized] = append(questionByNo[questions[i].QuestionNoNormalized], i)
		} else {
			*issues = append(*issues, candidateIssue("AMBIGUOUS_MATCH", "error", "unknown", "", "有一道题未识别到可用题号，无法自动匹配", "请人工填写题号并确认", questions[i].SourceRefs))
		}
	}
	return questionByNo
}

func indexPaperImportAnswers(answers []AnswerCandidate, issues *[]PaperImportIssue) map[string][]int {
	answerByNo := map[string][]int{}
	for i := range answers {
		if !paperImportQuestionNumbersWithinBounds(answers[i].QuestionNoHint, answers[i].QuestionNoNormalized, answers[i].SubquestionNoHint) {
			answers[i].QuestionNoNormalized = ""
			*issues = append(*issues, invalidPaperImportQuestionNumberIssue(answers[i].SourceRefs))
			continue
		}
		answers[i].QuestionNoNormalized = normalizePaperImportQuestionNumber(firstNonEmpty(answers[i].QuestionNoNormalized, answers[i].QuestionNoHint))
		if answers[i].QuestionNoNormalized != "" {
			answerByNo[answers[i].QuestionNoNormalized] = append(answerByNo[answers[i].QuestionNoNormalized], i)
		} else {
			*issues = append(*issues, candidateIssue("AMBIGUOUS_MATCH", "warning", "unknown", "", "有一个标准答案缺少题号，尚未自动匹配", "请对照来源手动匹配", answers[i].SourceRefs))
		}
	}
	return answerByNo
}

func indexPaperImportSolutions(solutions []SolutionCandidate, issues *[]PaperImportIssue) map[string][]int {
	solutionByNo := map[string][]int{}
	for i := range solutions {
		if !paperImportQuestionNumbersWithinBounds(solutions[i].QuestionNoHint, solutions[i].QuestionNoNormalized, solutions[i].SubquestionNoHint) {
			solutions[i].QuestionNoNormalized = ""
			*issues = append(*issues, invalidPaperImportQuestionNumberIssue(solutions[i].SourceRefs))
			continue
		}
		solutions[i].QuestionNoNormalized = normalizePaperImportQuestionNumber(firstNonEmpty(solutions[i].QuestionNoNormalized, solutions[i].QuestionNoHint))
		if solutions[i].QuestionNoNormalized != "" {
			solutionByNo[solutions[i].QuestionNoNormalized] = append(solutionByNo[solutions[i].QuestionNoNormalized], i)
		} else {
			*issues = append(*issues, candidateIssue("UNMATCHED_SOLUTION", "warning", "unknown", "", "有一份解析缺少题号，尚未自动匹配", "请对照来源手动匹配", solutions[i].SourceRefs))
		}
	}
	return solutionByNo
}

func indexPaperImportRubrics(rubrics []RubricCandidate, issues *[]PaperImportIssue) map[string][]int {
	rubricByNo := map[string][]int{}
	for i := range rubrics {
		if !paperImportQuestionNumbersWithinBounds(rubrics[i].QuestionNoHint, rubrics[i].QuestionNoNormalized) {
			rubrics[i].QuestionNoNormalized = ""
			*issues = append(*issues, invalidPaperImportQuestionNumberIssue(rubrics[i].SourceRefs))
			continue
		}
		rubrics[i].QuestionNoNormalized = normalizePaperImportQuestionNumber(firstNonEmpty(rubrics[i].QuestionNoNormalized, rubrics[i].QuestionNoHint))
		if rubrics[i].QuestionNoNormalized != "" {
			rubricByNo[rubrics[i].QuestionNoNormalized] = append(rubricByNo[rubrics[i].QuestionNoNormalized], i)
		} else {
			*issues = append(*issues, candidateIssue("AMBIGUOUS_RUBRIC_MATCH", "error", "unknown", "", "评分标准缺少题号，尚未自动匹配", "请人工匹配评分标准", rubrics[i].SourceRefs))
		}
	}
	return rubricByNo
}

func invalidPaperImportQuestionNumberIssue(refs []PaperImportSourceRef) PaperImportIssue {
	// 不把异常长数字拼进诊断文本；原候选与来源仍保存，等待人工纠正题号。
	return candidateIssue("AMBIGUOUS_MATCH", "error", "unknown", "", "题号超过64字符或数字超出非负int64范围，无法自动匹配", "请对照来源填写有效题号并确认", refs)
}

func sortedPaperImportKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addQuestionNumberGapIssues(byNo map[string][]int, issues *[]PaperImportIssue) {
	nums := []int64{}
	for no := range byNo {
		if n, err := strconv.ParseInt(no, 10, 64); err == nil && n >= 0 {
			nums = append(nums, n)
		}
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	type numberGap struct{ start, end int64 }
	gaps := []numberGap{}
	var missing int64
	for i := 1; i < len(nums); i++ {
		if nums[i]-nums[i-1] > 1 {
			gaps = append(gaps, numberGap{nums[i-1] + 1, nums[i] - 1})
			missing += nums[i] - nums[i-1] - 1
		}
	}
	// 小断档保留逐题提示；大断档只汇总有界区间，模型的两个题号不能放大成海量问题对象。
	if missing > maxPaperImportGapDetails {
		ranges := make([]string, 0, maxPaperImportGapRanges+1)
		for i, gap := range gaps {
			if i == maxPaperImportGapRanges {
				break
			}
			ranges = append(ranges, fmt.Sprintf("%d–%d", gap.start, gap.end))
		}
		if len(gaps) > maxPaperImportGapRanges {
			ranges = append(ranges, fmt.Sprintf("另%d处断档", len(gaps)-maxPaperImportGapRanges))
		}
		message := fmt.Sprintf("存在%d处题号断档，共%d个可能缺失题号（%s）；未逐题展开", len(gaps), missing, strings.Join(ranges, "、"))
		*issues = append(*issues, candidateIssue("POSSIBLE_MISSING_QUESTION", "warning", "suspected", "", message, "可能是合法编号、漏传页面或OCR识别造成，请对照来源核对；不会自动补建题目", nil))
		return
	}
	for _, gap := range gaps {
		for n := gap.start; n <= gap.end; n++ {
			no := strconv.FormatInt(n, 10)
			*issues = append(*issues, candidateIssue("POSSIBLE_MISSING_QUESTION", "warning", "suspected", no, "未检测到第"+no+"题，存在题号断档", "可能是漏传页面、编号方式或 OCR 识别造成，请核对", nil))
		}
	}
}
