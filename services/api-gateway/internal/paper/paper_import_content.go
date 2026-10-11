package paper

import (
	"context"
	"strings"
)

type FillPaperImportContentInput struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}

type PaperImportContentSkip struct {
	QuestionNo string `json:"question_no"`
	Reason     string `json:"reason"`
}

type PaperImportContentResult struct {
	FilledQuestions []string                 `json:"filled_questions"`
	MissingAnswers  []string                 `json:"missing_answers"`
	Skipped         []PaperImportContentSkip `json:"skipped"`
}

type PaperImportContentRepository interface {
	FillPaperImportContent(context.Context, string, string, string, FillPaperImportContentInput) (PaperImportContentResult, error)
}

type paperImportContentPatch struct {
	question Question
	draft    PaperImportDraftQuestion
	stem     bool
	options  bool
}

type paperImportContentIndex struct {
	drafts    map[string][]PaperImportDraftQuestion
	numbers   map[string]int
	conflicts map[string]bool
}

// 自动填入只补已有题目的空白内容，不改变题型、分值、题目数量或评分细则。
// 题号唯一且题型一致才可匹配；有答案冲突、题库快照或锁定细则时保留原配置，交给人工核对。
func planPaperImportContent(job PaperImportJob, questions []Question) ([]paperImportContentPatch, PaperImportContentResult) {
	result := PaperImportContentResult{FilledQuestions: []string{}, MissingAnswers: []string{}, Skipped: []PaperImportContentSkip{}}
	patches := []paperImportContentPatch{}
	index := indexPaperImportContent(job, questions)
	for _, question := range questions {
		draft, reason := index.match(question)
		if reason == "" {
			patch := planPaperImportContentPatch(question, draft)
			if patch.stem || patch.options || patch.draft.AnswerKey != nil || patch.draft.Solution != nil {
				patches = append(patches, patch)
				result.FilledQuestions = append(result.FilledQuestions, question.QuestionNo)
			}
			// 只补题干或解析仍可能没有答案，只有本次新增答案才能免于缺答案提示。
			if patch.draft.AnswerKey != nil {
				continue
			}
		} else {
			result.Skipped = append(result.Skipped, PaperImportContentSkip{QuestionNo: question.QuestionNo, Reason: reason})
		}
		if question.AnswerKey == nil || emptyAnswer(question.AnswerKey.StandardAnswer) {
			result.MissingAnswers = append(result.MissingAnswers, question.QuestionNo)
		}
	}
	return patches, result
}

func indexPaperImportContent(job PaperImportJob, questions []Question) paperImportContentIndex {
	index := paperImportContentIndex{
		drafts:    map[string][]PaperImportDraftQuestion{},
		numbers:   map[string]int{},
		conflicts: map[string]bool{},
	}
	for _, draft := range job.Questions {
		no := normalizePaperImportQuestionNumber(draft.QuestionNo)
		index.drafts[no] = append(index.drafts[no], draft)
	}
	for _, question := range questions {
		index.numbers[normalizePaperImportQuestionNumber(question.QuestionNo)]++
	}
	for _, issue := range job.StructuredIssues {
		switch issue.Code {
		case "CONFLICTING_ANSWERS", "DUPLICATE_QUESTION_NO", "HUMAN_CONFIRMED_CONFLICT":
			index.conflicts[normalizePaperImportQuestionNumber(issue.QuestionNo)] = true
		}
	}
	return index
}

// 多个限制同时命中时，先报告匹配与冲突问题，再报告原题保护和结构差异。
func (index paperImportContentIndex) match(question Question) (PaperImportDraftQuestion, string) {
	no := normalizePaperImportQuestionNumber(question.QuestionNo)
	matches := index.drafts[no]
	switch {
	case len(matches) == 0:
		return PaperImportDraftQuestion{}, "没有对应识别内容"
	case no == "" || len(matches) != 1 || index.numbers[no] != 1:
		return PaperImportDraftQuestion{}, "题号不唯一，请核对对应关系"
	case index.conflicts[no]:
		return PaperImportDraftQuestion{}, "识别内容存在冲突，请核对来源"
	case question.SourceType == "question_bank" || question.SourceBankItemID != "":
		return PaperImportDraftQuestion{}, "保留题库题目的原内容"
	case question.Rubric != nil && question.Rubric.Status == "locked":
		return PaperImportDraftQuestion{}, "保留已锁定题目的内容"
	}
	draft := matches[0]
	if draft.QuestionType != question.QuestionType || draft.ParentQuestionNo != question.ParentQuestionNo || draft.SubquestionNo != question.SubquestionNo {
		return PaperImportDraftQuestion{}, "题型或子题结构不同，请核对对应关系"
	}
	return draft, ""
}

func planPaperImportContentPatch(question Question, draft PaperImportDraftQuestion) paperImportContentPatch {
	patch := paperImportContentPatch{question: question, draft: draft}
	patch.stem = strings.TrimSpace(question.Stem) == "" && strings.TrimSpace(draft.Stem) != ""
	patch.options = len(question.Options) == 0 && len(draft.Options) > 0
	// 自动补齐不是人工确认；完整导入和评分规则发布仍走各自的核对流程。
	patch.draft.HumanConfirmedFields, patch.draft.Rubric = nil, nil
	patch.draft.Score = question.Score
	if question.AnswerKey != nil && !emptyAnswer(question.AnswerKey.StandardAnswer) || draft.AnswerKey == nil || emptyAnswer(draft.AnswerKey.StandardAnswer) {
		patch.draft.AnswerKey = nil
	}
	if question.Solution != nil && (strings.TrimSpace(question.Solution.RawText) != "" || len(question.Solution.Steps) > 0) || draft.Solution == nil || (strings.TrimSpace(draft.Solution.RawText) == "" && len(draft.Solution.Steps) == 0) {
		patch.draft.Solution = nil
	}
	return patch
}

// FillPaperImportContent 在同一把内存锁内校验导入和考试状态，再应用空白内容补齐。
// 采集题目与分配答案、解析 ID 也属于该锁范围；不提供跨进程事务保证。
func (s *MemoryStore) FillPaperImportContent(_ context.Context, tenantID, id, userID string, input FillPaperImportContentInput) (PaperImportContentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportContentResult{}, ErrNotFound
	}
	if input.ExpectedGeneration <= 0 {
		return PaperImportContentResult{}, ErrInvalidInput
	}
	if job.Generation != input.ExpectedGeneration || job.Status != "review_required" {
		return PaperImportContentResult{}, ErrConflict
	}
	if err := s.ensureExamPaperMutableLocked(job.ExamID); err != nil {
		return PaperImportContentResult{}, err
	}
	questions := s.paperImportContentQuestionsLocked(tenantID, job.ExamID)
	patches, result := planPaperImportContent(job, questions)
	for _, patch := range patches {
		s.applyPaperImportContentPatchLocked(job.ID, patch)
	}
	return result, nil
}

// 调用方持有 s.mu，题目及其最后一个 rubric 必须来自同一轮采集。
func (s *MemoryStore) paperImportContentQuestionsLocked(tenantID, examID string) []Question {
	questions := []Question{}
	for _, q := range s.questions {
		if q.TenantID == tenantID && q.ExamID == examID && q.Status != "deleted" {
			if rubrics := s.rubrics[q.ID]; len(rubrics) > 0 {
				q.Rubric = &rubrics[len(rubrics)-1]
			}
			questions = append(questions, q)
		}
	}
	return questions
}

// 调用方持有 s.mu；计划已经筛掉已有内容，重复补齐不再分配答案或解析 ID。
func (s *MemoryStore) applyPaperImportContentPatchLocked(importID string, patch paperImportContentPatch) {
	q, draft := patch.question, patch.draft
	if patch.stem {
		q.Stem = draft.Stem
	}
	if patch.options {
		q.Options = cloneStrings(draft.Options)
	}
	q.PaperImportID, q.PaperImportCandidateID, q.PaperImportSourceRefs = importID, draft.CandidateID, draft.SourceRefs
	if draft.AnswerKey != nil {
		q.AnswerKey = &AnswerKey{ID: s.id("answer"), QuestionID: q.ID, AnswerVersion: "v1", StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers, Tolerance: draft.AnswerKey.Tolerance, PaperImportID: importID, PaperImportCandidateID: draft.AnswerCandidateID, PaperImportSourceRefs: draft.SourceRefs}
	}
	if draft.Solution != nil {
		q.Solution = &QuestionSolution{ID: s.id("solution"), QuestionID: q.ID, PaperImportID: importID, SolutionVersion: "v1", RawText: draft.Solution.RawText, Steps: draft.Solution.Steps, SourceRefs: draft.Solution.SourceRefs, VerificationStatus: "machine"}
	}
	s.questions[q.ID] = q
}
