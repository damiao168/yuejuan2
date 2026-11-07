package paper

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
