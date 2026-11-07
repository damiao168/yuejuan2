package paper

import (
	"sort"
	"strings"
)

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
		addPaperImportDraftIssue(draft, issues, "invalid_question_number", "题号为空、超过64字符或数字超出非负int64范围，请对照来源核对")
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
