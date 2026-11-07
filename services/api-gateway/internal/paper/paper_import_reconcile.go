package paper

import (
	"fmt"
	"regexp"
	"strings"
)

const paperImportIssuePrefix = "paper_import."

const (
	maxPaperImportQuestionNumberChars = 64
	maxPaperImportGapDetails          = 100
	maxPaperImportGapRanges           = 10
)

var paperImportNumberDigits = regexp.MustCompile(`[0-9]+`)

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

func questionCandidatesFromDrafts(drafts []PaperImportDraftQuestion) []QuestionCandidate {
	out := make([]QuestionCandidate, 0, len(drafts))
	for _, d := range drafts {
		score := d.Score
		out = append(out, QuestionCandidate{QuestionNoRaw: d.QuestionNo, QuestionNoNormalized: normalizePaperImportQuestionNumber(d.QuestionNo), ParentQuestionNo: d.ParentQuestionNo, SubquestionNo: d.SubquestionNo, Options: append([]string{}, d.Options...), QuestionType: d.QuestionType, Score: &score, Stem: d.Stem})
	}
	return out
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
