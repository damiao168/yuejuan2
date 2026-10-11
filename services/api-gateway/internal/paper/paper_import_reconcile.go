package paper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const paperImportIssuePrefix = "paper_import."

type paperImportExistingQuestion struct {
	id, number, kind string
	score            float64
	sortOrder        int
}

func normalizePaperImportQuestionNumber(value string) string {
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
		parent, _ := strconv.Atoi(matched[1])
		child, _ := strconv.Atoi(matched[2])
		return fmt.Sprintf("%d(%d)", parent, child)
	}
	if matched := regexp.MustCompile(`^0*(\d+)\.0*(\d+)$`).FindStringSubmatch(value); matched != nil {
		parent, _ := strconv.Atoi(matched[1])
		child, _ := strconv.Atoi(matched[2])
		return fmt.Sprintf("%d(%d)", parent, child)
	}
	if matched := regexp.MustCompile(`^0*(\d+)\)?$`).FindStringSubmatch(value); matched != nil {
		parent, _ := strconv.Atoi(matched[1])
		return strconv.Itoa(parent)
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

func issueMessages(issues []PaperImportIssue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Message)
	}
	return dedupeStrings(out)
}

func candidateIssue(code, severity, certainty, questionNo, message, hint string, refs []PaperImportSourceRef) PaperImportIssue {
	return PaperImportIssue{Code: code, Severity: severity, Certainty: certainty, QuestionNo: questionNo, Message: message, ResolutionHint: hint, SourceRefs: refs}
}

func appendDetectedRoleIssues(base []PaperImportIssue, detected []PaperImportDetectedDocument) []PaperImportIssue {
	out := append([]PaperImportIssue{}, base...)
	for _, item := range detected {
		if item.DetectedRole != "unknown" && item.RoleConfidence >= 0.6 {
			continue
		}
		confidence := item.RoleConfidence
		out = append(out, PaperImportIssue{
			Code: "UNKNOWN_DOCUMENT_ROLE", Severity: "warning", Certainty: "unknown",
			Message: "有一份资料的内容类型无法可靠判断", Confidence: &confidence,
			SourceRefs:     []PaperImportSourceRef{{SourceID: item.SourceID}},
			ResolutionHint: "请核对资料内容，必要时手动指定类型",
		})
	}
	return out
}

// 只合并能确定的候选事实；语义歧义仍保留为人工核对问题。
func reconcilePaperImportCandidates(questions []QuestionCandidate, answers []AnswerCandidate, solutions []SolutionCandidate, rubrics []RubricCandidate, base []PaperImportIssue) ([]PaperImportDraftQuestion, []PaperImportIssue) {
	issues := append([]PaperImportIssue{}, base...)
	questionByNo := indexPaperImportQuestions(questions, &issues)
	answerByNo := indexPaperImportAnswers(answers, &issues)
	solutionByNo := indexPaperImportSolutions(solutions, &issues)
	rubricByNo := indexPaperImportRubrics(rubrics, &issues)
	appendQuestionCandidateMatchIssues(questions, questionByNo, &issues)
	appendAnswerCandidateMatchIssues(answers, answerByNo, questionByNo, &issues)
	appendSolutionCandidateMatchIssues(solutions, solutionByNo, questionByNo, &issues)
	appendRubricCandidateMatchIssues(questions, rubrics, questionByNo, rubricByNo, &issues)

	drafts := make([]PaperImportDraftQuestion, 0, len(questions))
	for _, q := range questions {
		draft := newPaperImportCandidateDraft(q, &issues)
		applyPaperImportAnswerCandidate(&draft, q, answers, answerByNo[q.QuestionNoNormalized], &issues)
		applyPaperImportSolutionCandidate(&draft, q, solutions, solutionByNo[q.QuestionNoNormalized], &issues)
		applyPaperImportRubricCandidates(&draft, q, rubrics, rubricByNo[q.QuestionNoNormalized], &issues)
		appendCandidateDraftReviewIssues(&draft, &issues)
		drafts = append(drafts, draft)
	}
	addQuestionNumberGapIssues(questionByNo, &issues)
	if len(questions) == 0 && len(answers) == 0 && len(solutions) == 0 && len(rubrics) == 0 {
		issues = append(issues, candidateIssue("UNKNOWN_DOCUMENT_ROLE", "error", "unknown", "", "未从资料中识别到题目、答案或解析", "请核对文件内容和清晰度", nil))
	}
	return drafts, dedupePaperImportIssues(issues)
}

// 题号回写到输入切片，后续保存候选时继续使用同一规范化结果。
func indexPaperImportQuestions(questions []QuestionCandidate, issues *[]PaperImportIssue) map[string][]int {
	questionByNo := map[string][]int{}
	for i := range questions {
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
		rubrics[i].QuestionNoNormalized = normalizePaperImportQuestionNumber(firstNonEmpty(rubrics[i].QuestionNoNormalized, rubrics[i].QuestionNoHint))
		if rubrics[i].QuestionNoNormalized != "" {
			rubricByNo[rubrics[i].QuestionNoNormalized] = append(rubricByNo[rubrics[i].QuestionNoNormalized], i)
		} else {
			*issues = append(*issues, candidateIssue("AMBIGUOUS_RUBRIC_MATCH", "error", "unknown", "", "评分标准缺少题号，尚未自动匹配", "请人工匹配评分标准", rubrics[i].SourceRefs))
		}
	}
	return rubricByNo
}

func appendQuestionCandidateMatchIssues(questions []QuestionCandidate, questionByNo map[string][]int, issues *[]PaperImportIssue) {
	for _, no := range sortedPaperImportKeys(questionByNo) {
		indexes := questionByNo[no]
		if len(indexes) > 1 {
			refs := []PaperImportSourceRef{}
			for _, i := range indexes {
				refs = append(refs, questions[i].SourceRefs...)
			}
			*issues = append(*issues, candidateIssue("DUPLICATE_QUESTION_NO", "error", "confirmed", no, "检测到重复题号 "+no, "请核对并保留正确题目", refs))
		}
	}
}

func appendAnswerCandidateMatchIssues(answers []AnswerCandidate, answerByNo, questionByNo map[string][]int, issues *[]PaperImportIssue) {
	for _, no := range sortedPaperImportKeys(answerByNo) {
		indexes := answerByNo[no]
		if len(indexes) > 1 {
			values := map[string]bool{}
			refs := []PaperImportSourceRef{}
			for _, i := range indexes {
				values[fmt.Sprint(answers[i].StandardAnswer)] = true
				refs = append(refs, answers[i].SourceRefs...)
			}
			code, msg := "DUPLICATE_ANSWER", "检测到重复答案"
			if len(values) > 1 {
				code, msg = "CONFLICTING_ANSWERS", "不同资料中的答案存在冲突"
			}
			*issues = append(*issues, candidateIssue(code, "error", "confirmed", no, msg, "请对照来源选择正确答案", refs))
		}
		if len(questionByNo[no]) == 0 {
			*issues = append(*issues, candidateIssue("UNMATCHED_ANSWER", "error", "confirmed", no, "答案资料中检测到第"+no+"题答案，但尚未检测到对应题目", "继续上传试题资料或手动匹配", answers[indexes[0]].SourceRefs))
			*issues = append(*issues, candidateIssue("POSSIBLE_MISSING_QUESTION", "warning", "suspected", no, "答案资料中存在第"+no+"题答案，但试题资料中没有检测到该题", "核对是否漏传页面或 OCR 未识别", answers[indexes[0]].SourceRefs))
		}
	}
}

func appendSolutionCandidateMatchIssues(solutions []SolutionCandidate, solutionByNo, questionByNo map[string][]int, issues *[]PaperImportIssue) {
	for _, no := range sortedPaperImportKeys(solutionByNo) {
		indexes := solutionByNo[no]
		if len(questionByNo[no]) == 0 {
			*issues = append(*issues, candidateIssue("UNMATCHED_SOLUTION", "error", "confirmed", no, "解析资料尚未匹配到对应题目", "继续上传试题资料或手动匹配", solutions[indexes[0]].SourceRefs))
		}
	}
}

func appendRubricCandidateMatchIssues(questions []QuestionCandidate, rubrics []RubricCandidate, questionByNo, rubricByNo map[string][]int, issues *[]PaperImportIssue) {
	for _, no := range sortedPaperImportKeys(rubricByNo) {
		indexes := rubricByNo[no]
		questionIndexes := questionByNo[no]
		if allQuestionCandidatesUseAnswerKeyOnly(questions, questionIndexes) {
			continue
		}
		if len(questionByNo[no]) == 0 {
			*issues = append(*issues, candidateIssue("UNMATCHED_RUBRIC", "error", "confirmed", no, "评分标准中检测到第"+no+"题，但尚未检测到对应题目", "继续上传试题资料或人工匹配", rubrics[indexes[0]].SourceRefs))
		}
		if !rubricCandidatesEquivalent(rubrics, indexes) {
			refs := []PaperImportSourceRef{}
			for _, i := range indexes {
				refs = append(refs, rubrics[i].SourceRefs...)
			}
			*issues = append(*issues, candidateIssue("CONFLICTING_RUBRICS", "error", "confirmed", no, "不同资料中的评分标准存在冲突", "请对照来源选择正确评分标准", refs))
		}
	}
}

func allQuestionCandidatesUseAnswerKeyOnly(questions []QuestionCandidate, indexes []int) bool {
	if len(indexes) == 0 {
		return false
	}
	for _, i := range indexes {
		if !paperImportUsesAnswerKeyOnly(questions[i].QuestionType) {
			return false
		}
	}
	return true
}

func newPaperImportCandidateDraft(q QuestionCandidate, issues *[]PaperImportIssue) PaperImportDraftQuestion {
	no := q.QuestionNoNormalized
	draft := PaperImportDraftQuestion{
		CandidateID: q.CandidateID, QuestionNo: no, QuestionType: q.QuestionType,
		ParentQuestionNo: q.ParentQuestionNo, SubquestionNo: q.SubquestionNo,
		Options:             append([]string{}, q.Options...),
		AssessmentArchetype: defaultPaperImportArchetype(q.QuestionType),
		Stem:                q.Stem, KnowledgePoints: q.KnowledgePointHints, Confidence: q.Confidence,
		SourceRefs: append([]PaperImportSourceRef{}, q.SourceRefs...), Issues: append([]string{}, q.Issues...),
		MatchStatus: "create", CompletenessStatus: "needs_review",
	}
	if q.Score != nil {
		draft.Score = *q.Score
		draft.ScoreSource = "material"
	} else {
		draft.ScoreSource = "missing"
		*issues = append(*issues, candidateIssue("MISSING_SCORE", "error", "confirmed", no, "第"+no+"题未识别到分值", "请人工填写或补充包含分值的资料", q.SourceRefs))
	}
	if q.QuestionType == "" {
		*issues = append(*issues, candidateIssue("MISSING_QUESTION_TYPE", "error", "confirmed", no, "第"+no+"题未识别到题型", "请人工选择题型", q.SourceRefs))
	}
	if strings.TrimSpace(q.Stem) == "" {
		*issues = append(*issues, candidateIssue("MISSING_QUESTION", "error", "confirmed", no, "第"+no+"题未识别到题干", "请补充题目资料或人工填写题干", q.SourceRefs))
	}
	if q.Confidence > 0 && q.Confidence < 0.7 {
		c := q.Confidence
		*issues = append(*issues, PaperImportIssue{Code: "LOW_EXTRACTION_CONFIDENCE", Severity: "warning", Certainty: "confirmed", QuestionNo: no, Message: "第" + no + "题识别置信度较低", Confidence: &c, SourceRefs: q.SourceRefs, ResolutionHint: "请对照原始资料核对"})
	}
	return draft
}

func applyPaperImportAnswerCandidate(draft *PaperImportDraftQuestion, q QuestionCandidate, answers []AnswerCandidate, indexes []int, issues *[]PaperImportIssue) {
	no := q.QuestionNoNormalized
	if len(indexes) == 1 {
		a := answers[indexes[0]]
		draft.AnswerCandidateID = a.CandidateID
		draft.SourceRefs = append(draft.SourceRefs, a.SourceRefs...)
		draft.AnswerKey = &AnswerKeyInput{StandardAnswer: a.StandardAnswer, EquivalentAnswers: a.EquivalentAnswers, Tolerance: a.Tolerance}
	} else if len(indexes) == 0 && questionRequiresStandardAnswerForArchetype(draftAssessmentArchetype(*draft)) {
		*issues = append(*issues, candidateIssue("MISSING_ANSWER", "error", "confirmed", no, "第"+no+"题未检测到标准答案", "继续添加答案资料或人工填写", q.SourceRefs))
	}
}

func applyPaperImportSolutionCandidate(draft *PaperImportDraftQuestion, q QuestionCandidate, solutions []SolutionCandidate, indexes []int, issues *[]PaperImportIssue) {
	no := q.QuestionNoNormalized
	if len(indexes) == 1 {
		solution := solutions[indexes[0]]
		draft.SolutionCandidateID = solution.CandidateID
		draft.SourceRefs = append(draft.SourceRefs, solution.SourceRefs...)
		draft.Solution = &SolutionInput{RawText: solution.RawText, Steps: solution.Steps, SourceRefs: solution.SourceRefs}
	} else if len(indexes) == 0 {
		*issues = append(*issues, candidateIssue("MISSING_SOLUTION", "info", "confirmed", no, "第"+no+"题未检测到教师解析", "解析不是所有题型的导入前置条件，可按需继续补充", q.SourceRefs))
	}
}

func applyPaperImportRubricCandidates(draft *PaperImportDraftQuestion, q QuestionCandidate, rubrics []RubricCandidate, indexes []int, issues *[]PaperImportIssue) {
	no := q.QuestionNoNormalized
	if paperImportUsesAnswerKeyOnly(q.QuestionType) {
		return
	}
	if len(indexes) >= 1 && rubricCandidatesEquivalent(rubrics, indexes) {
		r := rubrics[indexes[0]]
		for _, i := range indexes[1:] {
			r.SourceRefs = append(r.SourceRefs, rubrics[i].SourceRefs...)
		}
		draft.RubricCandidateID = r.CandidateID
		draft.SourceRefs = append(draft.SourceRefs, r.SourceRefs...)
		points := paperImportRubricCandidatePoints(no, r, issues)
		if r.MaxScore != nil {
			draft.Rubric = &RubricInput{Status: "draft", MaxScore: *r.MaxScore, Points: points, Deductions: r.Deductions, Examples: r.Examples}
			appendRubricCandidateScoreIssues(q, r, points, issues)
		}
	} else if len(indexes) > 1 {
		*issues = append(*issues, candidateIssue("CONFLICTING_RUBRICS", "error", "confirmed", no, "评分标准候选不唯一", "请人工选择评分标准", draft.SourceRefs))
	}
}

func paperImportRubricCandidatePoints(no string, r RubricCandidate, issues *[]PaperImportIssue) []RubricPoint {
	points := make([]RubricPoint, 0, len(r.Points))
	for _, p := range r.Points {
		if p.Score == nil {
			*issues = append(*issues, candidateIssue("RUBRIC_POINT_SCORE_MISSING", "error", "confirmed", no, "第"+no+"题存在采分点但缺少分值", "请根据教师资料补充采分点分值", r.SourceRefs))
			continue
		}
		req := true
		if p.Required != nil {
			req = *p.Required
		}
		points = append(points, RubricPoint{ID: p.ID, Description: p.Description, Score: *p.Score, Required: req, EvidenceRequirements: p.EvidenceRequirements})
	}
	return points
}

func appendRubricCandidateScoreIssues(q QuestionCandidate, r RubricCandidate, points []RubricPoint, issues *[]PaperImportIssue) {
	if q.Score == nil {
		return
	}
	no := q.QuestionNoNormalized
	if !scoreEqual(*r.MaxScore, *q.Score) {
		*issues = append(*issues, candidateIssue("RUBRIC_SCORE_MISMATCH", "error", "confirmed", no, "第"+no+"题评分标准满分与题目分值不一致", "请核对评分标准满分", r.SourceRefs))
	}
	if len(points) == len(r.Points) && !scoreEqual(SumRubricPoints(points), *q.Score) {
		*issues = append(*issues, candidateIssue("RUBRIC_POINTS_SCORE_MISMATCH", "error", "confirmed", no, "第"+no+"题采分点合计与题目分值不一致", "请调整采分点分值", r.SourceRefs))
	}
}

func appendCandidateDraftReviewIssues(draft *PaperImportDraftQuestion, issues *[]PaperImportIssue) {
	no := draft.QuestionNo
	if draft.Rubric == nil && objectiveRubricEligible(*draft) {
		draft.Rubric = deterministicObjectiveRubric(draft.QuestionType, draft.Score)
	}
	if questionRequiresRubricForArchetype(draftAssessmentArchetype(*draft)) && draft.Rubric == nil {
		*issues = append(*issues, candidateIssue("MISSING_RUBRIC", "error", "confirmed", no, "第"+no+"题缺少评分细则", "请人工填写评分依据并确认", draft.SourceRefs))
	}
	*issues = append(*issues, candidateIssue("HUMAN_REVIEW_REQUIRED", "error", "confirmed", no, "第"+no+"题尚未完成人工核对", "请对照来源确认题目、答案、解析和评分依据", draft.SourceRefs))
}

func rubricCandidatesEquivalent(values []RubricCandidate, indexes []int) bool {
	if len(indexes) < 2 {
		return true
	}
	first := values[indexes[0]]
	for _, index := range indexes[1:] {
		if rubricCandidateContentKey(first) != rubricCandidateContentKey(values[index]) {
			return false
		}
	}
	return true
}

func rubricCandidateContentKey(value RubricCandidate) string {
	return stableJSON(struct {
		MaxScore             *float64
		Points               []RubricCandidatePoint
		Deductions, Examples []any
	}{value.MaxScore, value.Points, value.Deductions, value.Examples})
}

func questionRequiresStandardAnswerForArchetype(archetype string) bool {
	switch archetype {
	case "extended_response":
		return false
	default:
		return true
	}
}

func questionRequiresRubricForArchetype(archetype string) bool {
	switch archetype {
	case "structured_steps", "short_constructed", "extended_response", "diagram_graph", "table_experiment":
		return true
	default:
		return false
	}
}

func paperImportUsesAnswerKeyOnly(questionType string) bool {
	return questionType == "fill_blank"
}

func defaultPaperImportArchetype(questionType string) string {
	switch questionType {
	case "single_choice", "multiple_choice", "true_false":
		return "selected_response"
	case "fill_blank":
		return "exact_text"
	case "numeric", "formula":
		return "numeric_expression"
	case "calculation":
		return "structured_steps"
	case "essay", "discussion", "coding":
		return "extended_response"
	default:
		return "short_constructed"
	}
}

func draftAssessmentArchetype(draft PaperImportDraftQuestion) string {
	if strings.TrimSpace(draft.AssessmentArchetype) != "" {
		return draft.AssessmentArchetype
	}
	return defaultPaperImportArchetype(draft.QuestionType)
}

func objectiveRubricEligible(draft PaperImportDraftQuestion) bool {
	if draft.Score <= 0 || draft.AnswerKey == nil || emptyAnswer(draft.AnswerKey.StandardAnswer) {
		return false
	}
	switch draft.QuestionType {
	case "single_choice", "multiple_choice", "true_false", "numeric", "formula":
		return true
	default:
		return false
	}
}

func deterministicObjectiveRubric(questionType string, score float64) *RubricInput {
	return &RubricInput{
		Status: "draft", MaxScore: score,
		Points:     []RubricPoint{{ID: "objective-correct", Description: "作答与经人工确认的标准答案一致", Score: score, Required: true}},
		Deductions: []any{}, Examples: []any{},
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
	nums := []int{}
	for no := range byNo {
		if n, err := strconv.Atoi(no); err == nil {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	for i := 1; i < len(nums); i++ {
		if nums[i]-nums[i-1] > 1 {
			for n := nums[i-1] + 1; n < nums[i]; n++ {
				no := strconv.Itoa(n)
				*issues = append(*issues, candidateIssue("POSSIBLE_MISSING_QUESTION", "warning", "suspected", no, "未检测到第"+no+"题，存在题号断档", "可能是漏传页面、编号方式或 OCR 识别造成，请核对", nil))
			}
		}
	}
}
func dedupePaperImportIssues(values []PaperImportIssue) []PaperImportIssue {
	seen := map[string]bool{}
	out := make([]PaperImportIssue, 0, len(values))
	for _, v := range values {
		key := v.Code + "|" + v.QuestionNo + "|" + v.Message
		if !seen[key] {
			seen[key] = true
			if v.SourceRefs == nil {
				v.SourceRefs = []PaperImportSourceRef{}
			}
			out = append(out, v)
		}
	}
	return out
}

// 新识别结果只能更新未确认字段；已人工确认的值按题号优先、候选 ID 兜底保留。
func preserveHumanConfirmedDrafts(fresh, existing []PaperImportDraftQuestion) []PaperImportDraftQuestion {
	byKey := humanConfirmedPaperImportDraftsByKey(existing)
	for i := range fresh {
		if old, ok := matchingHumanConfirmedPaperImportDraft(fresh[i], byKey); ok {
			copyHumanConfirmedPaperImportFields(&fresh[i], old)
		}
	}
	return fresh
}

func humanConfirmedPaperImportDraftsByKey(existing []PaperImportDraftQuestion) map[string]PaperImportDraftQuestion {
	byKey := map[string]PaperImportDraftQuestion{}
	for _, draft := range existing {
		if len(draft.HumanConfirmedFields) == 0 {
			continue
		}
		for _, key := range paperImportDraftKeys(draft) {
			byKey[key] = draft
		}
	}
	return byKey
}

func matchingHumanConfirmedPaperImportDraft(draft PaperImportDraftQuestion, byKey map[string]PaperImportDraftQuestion) (PaperImportDraftQuestion, bool) {
	for _, key := range paperImportDraftKeys(draft) {
		if old, ok := byKey[key]; ok {
			return old, true
		}
	}
	return PaperImportDraftQuestion{}, false
}

func copyHumanConfirmedPaperImportFields(fresh *PaperImportDraftQuestion, old PaperImportDraftQuestion) {
	for _, field := range old.HumanConfirmedFields {
		switch field {
		case "question_no":
			fresh.QuestionNo = old.QuestionNo
		case "question_type":
			fresh.QuestionType = old.QuestionType
		case "score":
			fresh.Score = old.Score
			fresh.ScoreSource = old.ScoreSource
		case "stem":
			fresh.Stem = old.Stem
		case "options":
			fresh.Options = append([]string{}, old.Options...)
		case "answer":
			fresh.AnswerKey = old.AnswerKey
		case "solution":
			fresh.Solution = old.Solution
		case "rubric":
			fresh.Rubric = old.Rubric
		}
	}
	fresh.HumanConfirmedFields = append([]string{}, old.HumanConfirmedFields...)
	fresh.ScoreResolution = old.ScoreResolution
	fresh.QuestionTypeResolution = old.QuestionTypeResolution
}

func paperImportDraftKeys(draft PaperImportDraftQuestion) []string {
	keys := []string{}
	if number := normalizePaperImportQuestionNumber(draft.QuestionNo); number != "" {
		keys = append(keys, "number:"+number)
	}
	if draft.CandidateID != "" {
		keys = append(keys, "candidate:"+draft.CandidateID)
	}
	return keys
}

// 应在旧值覆盖新结果之前检测冲突，否则比较到的都是人工旧值，无法提示重新核对。
func appendHumanConfirmationConflicts(issues []PaperImportIssue, fresh, existing []PaperImportDraftQuestion) []PaperImportIssue {
	byKey := humanConfirmedPaperImportDraftsByKey(existing)
	for _, draft := range fresh {
		if old, ok := matchingHumanConfirmedPaperImportDraft(draft, byKey); ok {
			issues = appendPaperImportDraftConfirmationConflicts(issues, draft, old)
		}
	}
	return dedupePaperImportIssues(issues)
}

func appendPaperImportDraftConfirmationConflicts(issues []PaperImportIssue, draft, old PaperImportDraftQuestion) []PaperImportIssue {
	confirmed := stringSet(old.HumanConfirmedFields)
	if confirmed["answer"] && stableJSON(old.AnswerKey) != stableJSON(draft.AnswerKey) {
		issues = append(issues, candidateIssue("CONFLICTING_ANSWERS", "error", "confirmed", old.QuestionNo, "新识别答案与人工确认答案冲突", "已保留人工确认值；请对照新资料再次确认", draft.SourceRefs))
	}
	for _, conflict := range []struct {
		field, oldValue, freshValue, label string
	}{
		{"question_no", old.QuestionNo, draft.QuestionNo, "题号"},
		{"question_type", old.QuestionType, draft.QuestionType, "题型"},
		{"score", fmt.Sprint(old.Score), fmt.Sprint(draft.Score), "分值"},
		{"stem", old.Stem, draft.Stem, "题干"},
		{"options", stableJSON(old.Options), stableJSON(draft.Options), "选项"},
		{"solution", stableJSON(old.Solution), stableJSON(draft.Solution), "解析"},
		{"rubric", stableJSON(old.Rubric), stableJSON(draft.Rubric), "评分细则"},
	} {
		if confirmed[conflict.field] && strings.TrimSpace(conflict.oldValue) != strings.TrimSpace(conflict.freshValue) {
			issues = append(issues, candidateIssue("HUMAN_CONFIRMED_CONFLICT", "error", "confirmed", old.QuestionNo, "新识别"+conflict.label+"与人工确认值冲突", "已保留人工确认值；请对照新资料再次确认", draft.SourceRefs))
		}
	}
	return issues
}

func stableJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func normalizeHumanConfirmedFields(values []string) []string {
	allowed := map[string]bool{
		"question_no": true, "question_type": true, "score": true, "stem": true,
		"options": true,
		"answer":  true, "solution": true, "rubric": true,
	}
	if len(values) == 0 {
		return []string{}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if allowed[value] && !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	return out
}

func requiredHumanConfirmedFields(draft PaperImportDraftQuestion) []string {
	fields := []string{"question_no", "question_type", "score", "stem"}
	if questionRequiresStandardAnswerForArchetype(draftAssessmentArchetype(draft)) || draft.AnswerKey != nil {
		fields = append(fields, "answer")
	}
	if len(draft.Options) > 0 {
		fields = append(fields, "options")
	}
	if draft.Solution != nil {
		fields = append(fields, "solution")
	}
	if !paperImportUsesAnswerKeyOnly(draft.QuestionType) && !isDerivedObjectiveRubric(draft) && (questionRequiresRubricForArchetype(draftAssessmentArchetype(draft)) || draft.Rubric != nil) {
		fields = append(fields, "rubric")
	}
	return fields
}

// 填空题使用答案键，不沿用先前题型的 Rubric 或其人工确认标记。
func normalizeAnswerKeyOnlyPaperImportDraft(draft *PaperImportDraftQuestion) {
	if !paperImportUsesAnswerKeyOnly(draft.QuestionType) {
		return
	}
	draft.Rubric = nil
	draft.RubricCandidateID = ""
	confirmed := make([]string, 0, len(draft.HumanConfirmedFields))
	for _, field := range draft.HumanConfirmedFields {
		if field != "rubric" {
			confirmed = append(confirmed, field)
		}
	}
	draft.HumanConfirmedFields = confirmed
}

func humanReviewComplete(draft PaperImportDraftQuestion) bool {
	confirmed := stringSet(draft.HumanConfirmedFields)
	for _, field := range requiredHumanConfirmedFields(draft) {
		if !confirmed[field] {
			return false
		}
	}
	return true
}

func refreshDraftCompleteness(draft *PaperImportDraftQuestion, requireHumanReview bool) {
	complete := strings.TrimSpace(draft.QuestionNo) != "" && strings.TrimSpace(draft.QuestionType) != "" && draft.Score > 0 && strings.TrimSpace(draft.Stem) != ""
	if complete && questionRequiresStandardAnswerForArchetype(draftAssessmentArchetype(*draft)) {
		complete = draft.AnswerKey != nil && !emptyAnswer(draft.AnswerKey.StandardAnswer)
	}
	if complete && questionRequiresRubricForArchetype(draftAssessmentArchetype(*draft)) {
		complete = draft.Rubric != nil && scoreEqual(draft.Rubric.MaxScore, draft.Score) && scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score)
	}
	if complete && requireHumanReview {
		complete = humanReviewComplete(*draft)
	}
	if complete {
		draft.CompletenessStatus = "complete"
	} else {
		draft.CompletenessStatus = "needs_review"
	}
}

func issuesAfterHumanReview(issues []PaperImportIssue, drafts []PaperImportDraftQuestion, resolveConflicts bool) []PaperImportIssue {
	confirmed := map[string]PaperImportDraftQuestion{}
	for _, d := range drafts {
		confirmed[normalizePaperImportQuestionNumber(d.QuestionNo)] = d
	}
	out := []PaperImportIssue{}
	for _, issue := range issues {
		d, ok := confirmed[normalizePaperImportQuestionNumber(issue.QuestionNo)]
		resolved := ok && paperImportIssueResolvedByDraft(issue.Code, d, resolveConflicts)
		// 评分标准可由来源手动匹配；没有题号匹配时也要检查这一恢复路径。
		resolved = resolved || paperImportRubricIssueResolvedBySource(issue, drafts, resolveConflicts)
		if !resolved {
			out = append(out, issue)
		}
	}
	return out
}

func paperImportIssueResolvedByDraft(code string, draft PaperImportDraftQuestion, resolveConflicts bool) bool {
	fields := stringSet(draft.HumanConfirmedFields)
	switch code {
	case "MISSING_ANSWER":
		return draft.AnswerKey != nil && !emptyAnswer(draft.AnswerKey.StandardAnswer)
	case "MISSING_SCORE":
		return draft.Score > 0
	case "MISSING_QUESTION_TYPE":
		return draft.QuestionType != ""
	case "MISSING_QUESTION":
		return strings.TrimSpace(draft.Stem) != ""
	case "MISSING_RUBRIC":
		return draft.Rubric != nil
	case "RUBRIC_SCORE_MISMATCH", "RUBRIC_POINTS_SCORE_MISMATCH", "RUBRIC_POINT_SCORE_MISSING":
		return paperImportRubricScoresMatch(draft) && fields["rubric"]
	case "MISSING_SOLUTION":
		return draft.Solution != nil
	case "HUMAN_REVIEW_REQUIRED":
		return humanReviewComplete(draft)
	case "CONFLICTING_ANSWERS":
		return resolveConflicts && fields["answer"]
	case "HUMAN_CONFIRMED_CONFLICT":
		return resolveConflicts && len(fields) > 0
	default:
		return false
	}
}

func paperImportRubricScoresMatch(draft PaperImportDraftQuestion) bool {
	return draft.Rubric != nil && scoreEqual(draft.Rubric.MaxScore, draft.Score) && scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score)
}

func paperImportRubricIssueResolvedBySource(issue PaperImportIssue, drafts []PaperImportDraftQuestion, resolveConflicts bool) bool {
	if !resolveConflicts {
		return false
	}
	switch issue.Code {
	case "AMBIGUOUS_RUBRIC_MATCH", "UNMATCHED_RUBRIC", "CONFLICTING_RUBRICS":
	default:
		return false
	}
	for _, reviewed := range drafts {
		if reviewed.RubricCandidateID != "" && stringSet(reviewed.HumanConfirmedFields)["rubric"] && paperImportRubricScoresMatch(reviewed) && sourceRefsOverlap(issue.SourceRefs, reviewed.SourceRefs) {
			return true
		}
	}
	return false
}

func sourceRefsOverlap(left, right []PaperImportSourceRef) bool {
	for _, a := range left {
		for _, b := range right {
			if a.SourceID != "" && a.SourceID == b.SourceID && a.FileAssetID == b.FileAssetID {
				return true
			}
		}
	}
	return false
}

func appendReviewedDraftIssues(issues []PaperImportIssue, drafts []PaperImportDraftQuestion) []PaperImportIssue {
	validationCodes := map[string]bool{
		"MISSING_ANSWER": true, "MISSING_SCORE": true, "MISSING_QUESTION_TYPE": true,
		"MISSING_QUESTION": true, "MISSING_RUBRIC": true, "DUPLICATE_QUESTION_NO": true,
		"RUBRIC_SCORE_MISMATCH": true, "RUBRIC_POINTS_SCORE_MISMATCH": true, "RUBRIC_POINT_SCORE_MISSING": true,
	}
	out := make([]PaperImportIssue, 0, len(issues)+len(drafts))
	for _, issue := range issues {
		if !validationCodes[issue.Code] {
			out = append(out, issue)
		}
	}
	seenNumbers := map[string]bool{}
	for index := range drafts {
		draft := &drafts[index]
		no := normalizePaperImportQuestionNumber(draft.QuestionNo)
		out = appendReviewedDraftIdentityIssues(out, *draft, no, seenNumbers)
		out = appendReviewedDraftContentIssues(out, *draft, no)
	}
	return dedupePaperImportIssues(out)
}

func appendReviewedDraftIdentityIssues(out []PaperImportIssue, draft PaperImportDraftQuestion, no string, seenNumbers map[string]bool) []PaperImportIssue {
	if no == "" || strings.TrimSpace(draft.Stem) == "" {
		out = append(out, candidateIssue("MISSING_QUESTION", "error", "confirmed", no, "题目缺少题号或题干", "请对照来源补全题目", draft.SourceRefs))
	}
	if no != "" && seenNumbers[no] {
		out = append(out, candidateIssue("DUPLICATE_QUESTION_NO", "error", "confirmed", no, "检测到重复题号 "+no, "请核对并保留正确题目", draft.SourceRefs))
	}
	seenNumbers[no] = no != ""
	return out
}

func appendReviewedDraftContentIssues(out []PaperImportIssue, draft PaperImportDraftQuestion, no string) []PaperImportIssue {
	if strings.TrimSpace(draft.QuestionType) == "" {
		out = append(out, candidateIssue("MISSING_QUESTION_TYPE", "error", "confirmed", no, "第"+no+"题缺少题型", "请选择题型", draft.SourceRefs))
	}
	if draft.Score <= 0 {
		out = append(out, candidateIssue("MISSING_SCORE", "error", "confirmed", no, "第"+no+"题缺少有效分值", "请填写大于 0 的分值", draft.SourceRefs))
	}
	if questionRequiresStandardAnswerForArchetype(draftAssessmentArchetype(draft)) && (draft.AnswerKey == nil || emptyAnswer(draft.AnswerKey.StandardAnswer)) {
		out = append(out, candidateIssue("MISSING_ANSWER", "error", "confirmed", no, "第"+no+"题缺少标准答案", "请填写答案或继续添加答案资料", draft.SourceRefs))
	}
	if questionRequiresRubricForArchetype(draftAssessmentArchetype(draft)) && draft.Rubric == nil {
		out = append(out, candidateIssue("MISSING_RUBRIC", "error", "confirmed", no, "第"+no+"题缺少评分细则", "请填写评分依据", draft.SourceRefs))
	}
	if draft.Rubric != nil && !scoreEqual(draft.Rubric.MaxScore, draft.Score) {
		out = append(out, candidateIssue("RUBRIC_SCORE_MISMATCH", "error", "confirmed", no, "第"+no+"题评分细则满分与题目分值不一致", "请调整评分标准满分", draft.SourceRefs))
	}
	if draft.Rubric != nil && !scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score) {
		out = append(out, candidateIssue("RUBRIC_POINTS_SCORE_MISMATCH", "error", "confirmed", no, "第"+no+"题采分点合计与题目分值不一致", "请调整采分点分值", draft.SourceRefs))
	}
	return out
}

func questionCandidatesFromDrafts(drafts []PaperImportDraftQuestion) []QuestionCandidate {
	out := make([]QuestionCandidate, 0, len(drafts))
	for _, d := range drafts {
		score := d.Score
		out = append(out, QuestionCandidate{QuestionNoRaw: d.QuestionNo, QuestionNoNormalized: normalizePaperImportQuestionNumber(d.QuestionNo), ParentQuestionNo: d.ParentQuestionNo, SubquestionNo: d.SubquestionNo, Options: append([]string{}, d.Options...), QuestionType: d.QuestionType, Score: &score, Stem: d.Stem})
	}
	return out
}

func withoutBlueprintIssues(issues []PaperImportIssue) []PaperImportIssue {
	out := []PaperImportIssue{}
	for _, issue := range issues {
		switch issue.Code {
		case "QUESTION_COUNT_MISMATCH", "SECTION_COUNT_MISMATCH", "SCORE_TOTAL_MISMATCH":
			continue
		}
		out = append(out, issue)
	}
	return out
}

func withPaperImportReconciliationIssues(issues []PaperImportIssue, messages []string) []PaperImportIssue {
	out := make([]PaperImportIssue, 0, len(issues)+len(messages))
	for _, issue := range issues {
		if issue.Code != "BLUEPRINT_RECONCILIATION" {
			out = append(out, issue)
		}
	}
	for _, message := range messages {
		if strings.HasPrefix(message, paperImportIssuePrefix) {
			out = append(out, candidateIssue("BLUEPRINT_RECONCILIATION", "error", "confirmed", "", message, "请对照考试配置核对题目、题型和分值", nil))
		}
	}
	return dedupePaperImportIssues(out)
}

func reconcilePaperImportDrafts(drafts []PaperImportDraftQuestion, existing []paperImportExistingQuestion, expectedTotal *float64, baseIssues []string) ([]PaperImportDraftQuestion, []string) {
	issues := filterPaperImportReconciliationIssues(baseIssues)
	byNumber := indexPaperImportBlueprint(existing, &issues)
	used := map[string]bool{}
	seenDraftNumbers := map[string]bool{}
	for index := range drafts {
		draft := &drafts[index]
		key := preparePaperImportBlueprintDraft(draft, seenDraftNumbers, &issues)
		if len(existing) > 0 && draft.MatchStatus != "ambiguous" {
			matchPaperImportBlueprintDraft(draft, byNumber[key], used, &issues)
		}
		// 采用蓝图题型后再次清理：原资料可能不是填空题。
		normalizeAnswerKeyOnlyPaperImportDraft(draft)
		appendPaperImportBlueprintDraftContentIssues(draft, &issues)
		if len(draft.Issues) == 0 {
			draft.CompletenessStatus = "complete"
		} else {
			draft.CompletenessStatus = "needs_review"
		}
	}
	for _, item := range existing {
		if !used[item.id] {
			issues = append(issues, paperImportIssue("missing_question", "AI 结果缺少蓝图题目 %s", item.number))
		}
	}
	appendPaperImportTotalScoreIssues(drafts, existing, expectedTotal, &issues)
	return drafts, dedupeStrings(issues)
}

func indexPaperImportBlueprint(existing []paperImportExistingQuestion, issues *[]string) map[string][]paperImportExistingQuestion {
	sort.Slice(existing, func(i, j int) bool {
		if existing[i].sortOrder == existing[j].sortOrder {
			return existing[i].number < existing[j].number
		}
		return existing[i].sortOrder < existing[j].sortOrder
	})
	byNumber := map[string][]paperImportExistingQuestion{}
	for _, item := range existing {
		key := normalizePaperImportQuestionNumber(item.number)
		byNumber[key] = append(byNumber[key], item)
	}
	for _, key := range sortedPaperImportKeys(byNumber) {
		matches := byNumber[key]
		if key == "" || len(matches) > 1 {
			*issues = append(*issues, paperImportIssue("duplicate_question", "蓝图题号 %q 无法唯一匹配", key))
		}
	}
	return byNumber
}

func preparePaperImportBlueprintDraft(draft *PaperImportDraftQuestion, seenDraftNumbers map[string]bool, issues *[]string) string {
	normalizeAnswerKeyOnlyPaperImportDraft(draft)
	if draft.AssessmentArchetype == "" {
		draft.AssessmentArchetype = defaultPaperImportArchetype(draft.QuestionType)
	}
	draft.Issues = filterPaperImportReconciliationIssues(draft.Issues)
	draft.MatchedQuestionID = ""
	draft.MatchStatus = "create"
	key := normalizePaperImportQuestionNumber(draft.QuestionNo)
	if key == "" {
		addPaperImportDraftIssue(draft, issues, "invalid_question_number", "题号不能为空")
		draft.MatchStatus = "ambiguous"
	} else if seenDraftNumbers[key] {
		addPaperImportDraftIssue(draft, issues, "duplicate_question", "规范化题号 %q 重复", key)
		draft.MatchStatus = "ambiguous"
	}
	seenDraftNumbers[key] = key != ""
	return key
}

func matchPaperImportBlueprintDraft(draft *PaperImportDraftQuestion, matches []paperImportExistingQuestion, used map[string]bool, issues *[]string) {
	if len(matches) != 1 || used[matches[0].id] {
		draft.MatchStatus = "extra"
		addPaperImportDraftIssue(draft, issues, "unexpected_question", "题目 %s 不存在于考试蓝图", draft.QuestionNo)
		return
	}
	matched := matches[0]
	used[matched.id] = true
	draft.MatchedQuestionID = matched.id
	draft.MatchStatus = "matched"
	resolvePaperImportBlueprintDraft(draft, matched, issues)
}

func appendPaperImportBlueprintDraftContentIssues(draft *PaperImportDraftQuestion, issues *[]string) {
	if strings.TrimSpace(draft.Stem) == "" {
		addPaperImportDraftIssue(draft, issues, "missing_stem", "题目 %s 缺少题干", draft.QuestionNo)
	}
	if questionRequiresStandardAnswerForArchetype(draftAssessmentArchetype(*draft)) && (draft.AnswerKey == nil || emptyAnswer(draft.AnswerKey.StandardAnswer)) {
		addPaperImportDraftIssue(draft, issues, "missing_answer", "题目 %s 缺少标准答案", draft.QuestionNo)
	}
	if questionRequiresRubricForArchetype(draftAssessmentArchetype(*draft)) && draft.Rubric == nil {
		addPaperImportDraftIssue(draft, issues, "missing_rubric", "题目 %s 缺少评分细则", draft.QuestionNo)
	}
	if draft.Rubric != nil {
		if !scoreEqual(draft.Rubric.MaxScore, draft.Score) {
			addPaperImportDraftIssue(draft, issues, "rubric_max_score_mismatch", "题目 %s 分值 %.2f 与 rubric.max_score %.2f 不一致", draft.QuestionNo, draft.Score, draft.Rubric.MaxScore)
		}
		pointsTotal := SumRubricPoints(draft.Rubric.Points)
		if !scoreEqual(pointsTotal, draft.Score) {
			addPaperImportDraftIssue(draft, issues, "rubric_points_score_mismatch", "题目 %s 分值 %.2f 与 rubric points 合计 %.2f 不一致", draft.QuestionNo, draft.Score, pointsTotal)
		}
	}
}

func appendPaperImportTotalScoreIssues(drafts []PaperImportDraftQuestion, existing []paperImportExistingQuestion, expectedTotal *float64, issues *[]string) {
	var draftTotal, blueprintTotal float64
	for _, item := range drafts {
		draftTotal += item.Score
	}
	for _, item := range existing {
		blueprintTotal += item.score
	}
	if len(existing) > 0 && !scoreEqual(draftTotal, blueprintTotal) {
		*issues = append(*issues, paperImportIssue("total_score_mismatch", "AI 识别总分 %.2f 与蓝图总分 %.2f 不一致", draftTotal, blueprintTotal))
	}
	if expectedTotal != nil && !scoreEqual(draftTotal, *expectedTotal) {
		*issues = append(*issues, paperImportIssue("total_score_mismatch", "AI 识别总分 %.2f 与考试总分 %.2f 不一致", draftTotal, *expectedTotal))
	}
	if expectedTotal != nil && len(existing) > 0 && !scoreEqual(blueprintTotal, *expectedTotal) {
		*issues = append(*issues, paperImportIssue("blueprint_total_score_mismatch", "蓝图总分 %.2f 与考试总分 %.2f 不一致", blueprintTotal, *expectedTotal))
	}
}

func resolvePaperImportBlueprintDraft(draft *PaperImportDraftQuestion, matched paperImportExistingQuestion, issues *[]string) {
	resolvePaperImportBlueprintQuestionType(draft, matched, issues)
	resolvePaperImportBlueprintScore(draft, matched, issues)
	// 先确定最终题型和分值，再推导客观题采分点；填空题可能采用蓝图中的选择题型。
	if draft.Rubric == nil && objectiveRubricEligible(*draft) {
		draft.Rubric = deterministicObjectiveRubric(draft.QuestionType, draft.Score)
	}
}

func resolvePaperImportBlueprintQuestionType(draft *PaperImportDraftQuestion, matched paperImportExistingQuestion, issues *[]string) {
	if draft.QuestionType == matched.kind {
		return
	}
	switch draft.QuestionTypeResolution {
	case "use_blueprint":
		draft.QuestionType = matched.kind
		draft.AssessmentArchetype = defaultPaperImportArchetype(matched.kind)
	case "use_material":
		draft.AssessmentArchetype = defaultPaperImportArchetype(draft.QuestionType)
		if !stringSet(draft.HumanConfirmedFields)["question_type"] {
			draft.MatchStatus = "mismatch"
			addPaperImportDraftIssue(draft, issues, "question_type_mismatch", "题目 %s 采用资料题型前须人工确认；资料=%s，蓝图=%s", matched.number, draft.QuestionType, matched.kind)
		}
	default:
		draft.MatchStatus = "mismatch"
		addPaperImportDraftIssue(draft, issues, "question_type_mismatch", "题目 %s 题型 AI=%s，蓝图=%s；请明确选择采用资料或蓝图", matched.number, draft.QuestionType, matched.kind)
	}
}

func resolvePaperImportBlueprintScore(draft *PaperImportDraftQuestion, matched paperImportExistingQuestion, issues *[]string) {
	// 旧草稿没有分值来源字段，也允许用蓝图补足缺失的识别分值。
	if draft.Score <= 0 && matched.score > 0 && (draft.ScoreSource == "missing" || draft.ScoreSource == "") {
		draft.Score = matched.score
		draft.ScoreSource = "blueprint"
	}
	if scoreEqual(draft.Score, matched.score) {
		return
	}
	switch draft.ScoreResolution {
	case "use_blueprint":
		draft.Score = matched.score
		draft.ScoreSource = "blueprint"
		if draft.Rubric != nil && len(draft.Rubric.Points) == 1 && draft.Rubric.Points[0].ID == "objective-correct" {
			draft.Rubric = deterministicObjectiveRubric(draft.QuestionType, draft.Score)
		}
	case "use_material":
		if !stringSet(draft.HumanConfirmedFields)["score"] {
			draft.MatchStatus = "mismatch"
			addPaperImportDraftIssue(draft, issues, "question_score_mismatch", "题目 %s 采用资料分值前须人工确认；资料=%.2f，蓝图=%.2f", matched.number, draft.Score, matched.score)
		}
	default:
		draft.MatchStatus = "mismatch"
		addPaperImportDraftIssue(draft, issues, "question_score_mismatch", "题目 %s 分值 AI=%.2f，蓝图=%.2f；请明确选择采用资料或蓝图", matched.number, draft.Score, matched.score)
	}
}

func isDerivedObjectiveRubric(draft PaperImportDraftQuestion) bool {
	return draft.RubricCandidateID == "" && objectiveRubricEligible(draft) && draft.Rubric != nil &&
		stableJSON(draft.Rubric) == stableJSON(deterministicObjectiveRubric(draft.QuestionType, draft.Score))
}

func paperImportIssue(code, format string, args ...any) string {
	return paperImportIssuePrefix + code + ": " + fmt.Sprintf(format, args...)
}

func addPaperImportDraftIssue(draft *PaperImportDraftQuestion, issues *[]string, code, format string, args ...any) {
	issue := paperImportIssue(code, format, args...)
	draft.Issues = dedupeStrings(append(draft.Issues, issue))
	draft.CompletenessStatus = "needs_review"
	*issues = append(*issues, issue)
}

func filterPaperImportReconciliationIssues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !strings.HasPrefix(strings.TrimSpace(value), paperImportIssuePrefix) {
			out = append(out, value)
		}
	}
	return out
}

// 资料齐全不等于已核对：存在候选时仍要求人工确认，结构化 error 和旧版问题也阻断导入。
func paperImportHasBlockingIssues(job PaperImportJob) bool {
	if len(job.Questions) == 0 {
		return true
	}
	requireHumanReview := len(job.QuestionCandidates) > 0
	if requireHumanReview && paperImportDraftsNeedHumanReview(job.Questions) {
		return true
	}
	if paperImportJobIssuesBlock(job) {
		return true
	}
	for _, draft := range job.Questions {
		if paperImportDraftBlocks(draft, requireHumanReview) {
			return true
		}
	}
	return false
}

func paperImportDraftsNeedHumanReview(drafts []PaperImportDraftQuestion) bool {
	for _, draft := range drafts {
		if !humanReviewComplete(draft) {
			return true
		}
	}
	return false
}

func paperImportJobIssuesBlock(job PaperImportJob) bool {
	for _, issue := range job.StructuredIssues {
		if issue.Severity == "error" {
			return true
		}
	}
	for _, issue := range job.Issues {
		if strings.HasPrefix(strings.TrimSpace(issue), paperImportIssuePrefix) {
			return true
		}
	}
	if len(job.StructuredIssues) == 0 && len(job.Issues) > 0 {
		return true
	}
	return false
}

func paperImportDraftBlocks(draft PaperImportDraftQuestion, requireHumanReview bool) bool {
	if requireHumanReview && !paperImportUsesAnswerKeyOnly(draft.QuestionType) && !isDerivedObjectiveRubric(draft) && draft.Rubric != nil && !stringSet(draft.HumanConfirmedFields)["rubric"] {
		return true
	}
	if len(draft.Issues) > 0 || draft.MatchStatus == "extra" || draft.MatchStatus == "ambiguous" || draft.MatchStatus == "mismatch" {
		return true
	}
	return false
}
