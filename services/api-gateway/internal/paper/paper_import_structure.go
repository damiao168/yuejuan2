package paper

import (
	"context"
	"sort"
	"strings"
	"time"
)

type PaperStructureChange struct {
	QuestionNo    string   `json:"question_no"`
	QuestionID    string   `json:"question_id,omitempty"`
	Action        string   `json:"action"`
	PreviousType  string   `json:"previous_type,omitempty"`
	QuestionType  string   `json:"question_type,omitempty"`
	PreviousScore *float64 `json:"previous_score,omitempty"`
	Score         *float64 `json:"score,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}
type PaperStructurePreview struct {
	ImportID        string                 `json:"import_id"`
	Generation      int64                  `json:"generation"`
	PreviewHash     string                 `json:"preview_hash"`
	CurrentCount    int                    `json:"current_count"`
	RecognizedCount int                    `json:"recognized_count"`
	ExpectedTotal   float64                `json:"expected_total"`
	RecognizedTotal float64                `json:"recognized_total"`
	MissingScores   []string               `json:"missing_scores"`
	Changes         []PaperStructureChange `json:"changes"`
	BlockedReasons  []string               `json:"blocked_reasons"`
	PaperDetected   bool                   `json:"paper_detected"`
	Confirmed       bool                   `json:"confirmed"`
}
type AdoptPaperStructureInput struct {
	ExpectedGeneration     int64  `json:"expected_generation"`
	PreviewHash            string `json:"preview_hash"`
	CompletePaperConfirmed bool   `json:"complete_paper_confirmed"`
}
type PaperStructureRepository interface {
	PreviewPaperStructure(context.Context, string, string) (PaperStructurePreview, error)
	AdoptPaperStructure(context.Context, string, string, string, AdoptPaperStructureInput) (PaperStructurePreview, error)
}

// 试卷结构只来自题目资料；答案文件中的题号不能证明试卷完整，也不能增加正式题目。
// 缺少资料分值时留为待确认，不从总分、模板分值或题干中的游戏得分反推。
func planPaperStructure(job PaperImportJob, questions []Question, total float64) (PaperStructurePreview, []PaperImportDraftQuestion) {
	sort.Slice(questions, func(i, j int) bool {
		if questions[i].SortOrder != questions[j].SortOrder {
			return questions[i].SortOrder < questions[j].SortOrder
		}
		if questions[i].QuestionNo != questions[j].QuestionNo {
			return questions[i].QuestionNo < questions[j].QuestionNo
		}
		return questions[i].ID < questions[j].ID
	})
	out := PaperStructurePreview{ImportID: job.ID, Generation: job.Generation, CurrentCount: len(questions), ExpectedTotal: total, Changes: []PaperStructureChange{}, MissingScores: []string{}, BlockedReasons: []string{}, Confirmed: job.StructureConfirmedGeneration == job.Generation}
	out.PaperDetected = paperStructureDetected(job.Sources)
	if !out.PaperDetected {
		out.BlockedReasons = append(out.BlockedReasons, "尚未识别到试卷题目资料；仅有答案或解析时不能确认题目结构")
	}
	drafts := []PaperImportDraftQuestion{}
	seen := map[string]bool{}
	conflicts := structureConflictNumbers(job.StructuredIssues)
	existing, reasons := structureQuestionsByNumber(questions)
	out.BlockedReasons = append(out.BlockedReasons, reasons...)
	for _, draft := range job.Questions {
		draft, change, reasons, accepted := prepareStructureDraft(job, draft, existing, conflicts, seen)
		out.BlockedReasons = append(out.BlockedReasons, reasons...)
		if !accepted {
			continue
		}
		drafts = append(drafts, draft)
		if draft.Score > 0 {
			out.RecognizedTotal += draft.Score
		} else {
			out.MissingScores = append(out.MissingScores, draft.QuestionNo)
		}
		out.Changes = append(out.Changes, change)
	}
	appendStructureRemovalChanges(&out, questions, seen)
	out.RecognizedCount = len(drafts)
	if len(drafts) == 0 {
		out.BlockedReasons = append(out.BlockedReasons, "没有可采用的试卷题目")
	}
	// 哈希同时覆盖资料和教师当前配置；预览后发生编辑、换资料或规则发布时须重新预览。
	out.PreviewHash = stableContentHash(struct {
		Job       PaperImportJob
		Questions []Question
		Total     float64
	}{job, questions, total})
	return out, drafts
}

func paperStructureDetected(sources []PaperImportSource) bool {
	for _, source := range sources {
		if source.DetectedRole == "question" || source.DetectedRole == "mixed" {
			return true
		}
	}
	return false
}

func structureConflictNumbers(issues []PaperImportIssue) map[string]bool {
	conflicts := map[string]bool{}
	for _, issue := range issues {
		switch issue.Code {
		case "CONFLICTING_ANSWERS", "DUPLICATE_QUESTION_NO", "HUMAN_CONFIRMED_CONFLICT":
			conflicts[normalizePaperImportQuestionNumber(issue.QuestionNo)] = true
		}
	}
	return conflicts
}

func structureQuestionsByNumber(questions []Question) (map[string]Question, []string) {
	existing := map[string]Question{}
	reasons := []string{}
	for _, q := range questions {
		no := normalizePaperImportQuestionNumber(q.QuestionNo)
		if _, ok := existing[no]; ok {
			reasons = append(reasons, "正式题号不唯一："+no)
		}
		// 重复正式号仍保留后项供预览展示；阻断原因会使这份结构不能采用。
		existing[no] = q
	}
	return existing, reasons
}

func prepareStructureDraft(job PaperImportJob, draft PaperImportDraftQuestion, existing map[string]Question, conflicts, seen map[string]bool) (PaperImportDraftQuestion, PaperStructureChange, []string, bool) {
	no := normalizePaperImportQuestionNumber(draft.QuestionNo)
	if no == "" || seen[no] || !IsValidQuestionType(draft.QuestionType) || strings.TrimSpace(draft.Stem) == "" {
		return draft, PaperStructureChange{}, []string{"识别题号、题型或题干需要核对：" + draft.QuestionNo}, false
	}
	// 基本题号合法后即记为已见；来源待核对时也不能把同号正式题误判为多余题。
	seen[no] = true
	reasons := []string{}
	// 独立识别出的子题不能当成新的整题；保留候选，先核对父题归属后再采用。
	if draft.ParentQuestionNo != "" && draft.SubquestionNo != "" {
		reasons = append(reasons, "第 "+draft.QuestionNo+" 题是独立子题，请先合并到对应大题并核对结构")
	}
	var found bool
	draft, found = structureDraftCandidateOptions(draft, job.QuestionCandidates, no)
	if !found {
		reasons = append(reasons, "题目缺少试卷识别来源："+no)
		return draft, PaperStructureChange{}, reasons, false
	}
	draft = normalizeStructureDraftScore(draft, existing[no])
	draft.Rubric = nil
	// 结构确认只确认题目范围，不能同时替教师裁决不同来源的答案冲突。
	if conflicts[no] || conflicts[""] {
		draft.AnswerKey, draft.Solution = nil, nil
	}
	change, reason := structureChangeForDraft(draft, existing)
	if reason != "" {
		reasons = append(reasons, reason)
	}
	return draft, change, reasons, true
}

// 识别候选必须含来自试卷的题干；未匹配答案候选不作为新增题目。
func structureDraftCandidateOptions(draft PaperImportDraftQuestion, candidates []QuestionCandidate, no string) (PaperImportDraftQuestion, bool) {
	if len(candidates) == 0 {
		return draft, true
	}
	for _, c := range candidates {
		if normalizePaperImportQuestionNumber(c.QuestionNoNormalized) == no || normalizePaperImportQuestionNumber(c.QuestionNoRaw) == no {
			if len(draft.Options) == 0 {
				draft.Options = c.Options
			}
			return draft, true
		}
	}
	return draft, false
}

func normalizeStructureDraftScore(draft PaperImportDraftQuestion, old Question) PaperImportDraftQuestion {
	score := 0.0
	if old.ScoreSource == "human" && old.Score > 0 {
		// 正式题目中已保存的人工分值优先；资料差异留给逐题核对，不能整批覆盖。
		score = old.Score
		draft.ScoreSource = "human"
	} else if draft.Score > 0 && (draft.ScoreSource == "material" || draft.ScoreSource == "human" && stringSet(draft.HumanConfirmedFields)["score"]) {
		// 旧版整批保存会把未核对字段也标为确认；没有分值来源的历史数字不能据此采用。
		score = draft.Score
		draft.ScoreSource = importedScoreSource(draft)
	}
	if score == 0 {
		draft.ScoreSource = "missing"
	} else if draft.ScoreSource != "human" {
		draft.ScoreSource = "material"
	}
	draft.Score = score
	return draft
}

func structureChangeForDraft(draft PaperImportDraftQuestion, existing map[string]Question) (PaperStructureChange, string) {
	change := PaperStructureChange{QuestionNo: draft.QuestionNo, QuestionType: draft.QuestionType, Action: "add"}
	score := draft.Score
	if score > 0 {
		change.Score = &score
	}
	q, ok := existing[normalizePaperImportQuestionNumber(draft.QuestionNo)]
	if !ok {
		return change, ""
	}
	oldScore := q.Score
	change.PreviousScore = &oldScore
	change.QuestionID = q.ID
	change.PreviousType = q.QuestionType
	change.Action = "keep"
	if q.QuestionType != draft.QuestionType || !scoreEqual(q.Score, score) || q.ParentQuestionNo != draft.ParentQuestionNo || q.SubquestionNo != draft.SubquestionNo {
		change.Action = "update"
	}
	if change.Action == "update" && structureQuestionProtected(q) {
		change.Action = "conflict"
		change.Reason = "题库内容或生效评分配置需单独处理"
		return change, "第 " + q.QuestionNo + " 题已有生效配置，不能自动调整"
	}
	return change, ""
}

func structureQuestionProtected(q Question) bool {
	return q.SourceBankItemID != "" || q.SourceType == "question_bank" || q.Rubric != nil && q.Rubric.Status == "locked"
}

func appendStructureRemovalChanges(out *PaperStructurePreview, questions []Question, seen map[string]bool) {
	for _, q := range questions {
		if !seen[normalizePaperImportQuestionNumber(q.QuestionNo)] {
			oldScore := q.Score
			change := PaperStructureChange{QuestionNo: q.QuestionNo, QuestionID: q.ID, PreviousScore: &oldScore, PreviousType: q.QuestionType, Action: "remove", Reason: "预设占位题在试卷资料中没有对应题目"}
			if !emptyStructurePlaceholder(q) {
				change.Action = "conflict"
				change.Reason = "已有内容，需核对后单独处理"
				out.BlockedReasons = append(out.BlockedReasons, "第 "+q.QuestionNo+" 题已有内容，不能直接移除")
			}
			out.Changes = append(out.Changes, change)
		}
	}
}

// 采用沿用排序后的首项匹配；重复号在预览阶段阻断，不复用预览的后项索引。
func findStructureQuestion(questions []Question, questionNo string) Question {
	for _, old := range questions {
		if normalizePaperImportQuestionNumber(old.QuestionNo) == normalizePaperImportQuestionNumber(questionNo) {
			return old
		}
	}
	return Question{}
}

func emptyStructurePlaceholder(q Question) bool {
	return strings.TrimSpace(q.Stem) == "" && len(q.Options) == 0 && q.AnswerKey == nil && q.Solution == nil && q.Rubric == nil && q.SourceBankItemID == "" && q.SourceType != "question_bank" && len(q.AnswerArea) == 0
}

func importedScoreSource(draft PaperImportDraftQuestion) string {
	if stringSet(draft.HumanConfirmedFields)["score"] {
		return "human"
	}
	if draft.ScoreSource == "blueprint" {
		return "preset"
	}
	if draft.ScoreSource == "material" {
		return "material"
	}
	return "missing"
}

func (s *MemoryStore) PreviewPaperStructure(_ context.Context, tenantID, id string) (PaperStructurePreview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperStructurePreview{}, ErrNotFound
	}
	qs := s.structureQuestionsLocked(tenantID, job.ExamID)
	preview, _ := planPaperStructure(job, qs, s.examState[job.ExamID].Total)
	return preview, nil
}
func (s *MemoryStore) structureQuestionsLocked(tenantID, examID string) []Question {
	qs := []Question{}
	for _, q := range s.questions {
		if q.TenantID == tenantID && q.ExamID == examID && q.Status != "deleted" {
			if r := s.rubrics[q.ID]; len(r) > 0 {
				q.Rubric = &r[len(r)-1]
			}
			qs = append(qs, q)
		}
	}
	return qs
}
func (s *MemoryStore) AdoptPaperStructure(_ context.Context, tenantID, id, userID string, input AdoptPaperStructureInput) (PaperStructurePreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperStructurePreview{}, ErrNotFound
	}
	if input.ExpectedGeneration != job.Generation || job.Status != "review_required" {
		return PaperStructurePreview{}, ErrConflict
	}
	if !input.CompletePaperConfirmed || input.PreviewHash == "" {
		return PaperStructurePreview{}, ErrInvalidInput
	}
	if err := s.ensureExamPaperMutableLocked(job.ExamID); err != nil {
		return PaperStructurePreview{}, err
	}
	qs := s.structureQuestionsLocked(tenantID, job.ExamID)
	preview, drafts := planPaperStructure(job, qs, s.examState[job.ExamID].Total)
	if preview.PreviewHash != input.PreviewHash {
		return preview, ErrConflict
	}
	if len(preview.BlockedReasons) > 0 {
		return preview, ErrInvalidInput
	}
	if preview.Confirmed {
		return preview, nil
	}
	s.removeStructureQuestionsLocked(preview.Changes)
	s.adoptStructureDraftsLocked(tenantID, job, qs, drafts)
	job.StructureConfirmedGeneration = job.Generation
	job.UpdatedAt = time.Now().UTC()
	s.imports[id] = job
	s.invalidateReadiness(job.ExamID)
	preview.Confirmed = true
	return preview, nil
}

// 调用方持有同一独占锁，先移除空白占位题再补写候选，最后才保存结构回执。
func (s *MemoryStore) removeStructureQuestionsLocked(changes []PaperStructureChange) {
	for _, change := range changes {
		if change.Action == "remove" {
			q := s.questions[change.QuestionID]
			q.Status = "deleted"
			s.questions[q.ID] = q
		}
	}
}

func (s *MemoryStore) adoptStructureDraftsLocked(tenantID string, job PaperImportJob, qs []Question, drafts []PaperImportDraftQuestion) {
	for i, draft := range drafts {
		q := findStructureQuestion(qs, draft.QuestionNo)
		// 题库快照和生效评分内容即使结构相同也不补写；采用只确认它们的题号仍在试卷内。
		if structureQuestionProtected(q) {
			continue
		}
		if q.ID == "" {
			q = Question{ID: s.id("question"), TenantID: tenantID, ExamID: job.ExamID, QuestionNo: draft.QuestionNo, Status: "draft"}
		}
		q.QuestionType, q.Score, q.ParentQuestionNo, q.SubquestionNo, q.SortOrder = draft.QuestionType, draft.Score, draft.ParentQuestionNo, draft.SubquestionNo, i+1
		q.AssessmentArchetype = draftAssessmentArchetype(draft)
		q.ScoreSource = draft.ScoreSource
		if q.Stem == "" {
			q.Stem = draft.Stem
		}
		if len(q.Options) == 0 {
			q.Options = draft.Options
		}
		q.PaperImportID, q.PaperImportCandidateID = job.ID, draft.CandidateID
		if q.AnswerKey == nil && draft.AnswerKey != nil {
			q.AnswerKey = &AnswerKey{StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers, Tolerance: draft.AnswerKey.Tolerance}
		}
		if q.Solution == nil && draft.Solution != nil {
			q.Solution = &QuestionSolution{RawText: draft.Solution.RawText, Steps: draft.Solution.Steps, VerificationStatus: "machine"}
		}
		s.questions[q.ID] = q
	}
}
