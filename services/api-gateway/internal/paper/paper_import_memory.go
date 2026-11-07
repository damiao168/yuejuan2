package paper

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) CreatePaperImport(_ context.Context, tenantID, examID, userID string, input CreatePaperImportInput) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.ExamPaperID != "" {
		p, ok := s.papers[input.ExamPaperID]
		if !ok || p.TenantID != tenantID || p.ExamID != examID {
			return PaperImportJob{}, ErrInvalidInput
		}
	}
	sources := normalizePaperImportSourceInputs(input)
	if len(sources) == 0 || strings.TrimSpace(input.Subject) == "" {
		return PaperImportJob{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	job := PaperImportJob{ID: s.id("paper-import"), TenantID: tenantID, ExamID: examID, ExamPaperID: input.ExamPaperID, PaperFileAssetID: input.PaperFileAssetID, AnswerFileAssetID: input.AnswerFileAssetID, Status: "processing", Generation: 1, RunID: s.id(paperImportRunIDPrefix), Subject: input.Subject, Questions: []PaperImportDraftQuestion{}, Issues: []string{}, QuestionCandidates: []QuestionCandidate{}, AnswerCandidates: []AnswerCandidate{}, SolutionCandidates: []SolutionCandidate{}, RubricCandidates: []RubricCandidate{}, StructuredIssues: []PaperImportIssue{}, CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
	seenAssets, seenIndexes := map[string]bool{}, map[int]bool{}
	for _, source := range sources {
		if source.FileAssetID == "" || source.DocumentIndex < 0 || seenAssets[source.FileAssetID] || seenIndexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		job.Sources = append(job.Sources, PaperImportSource{ID: s.id("paper-import-source"), FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint, DetectedRole: "unknown", ProcessingStatus: "pending", CreatedAt: now})
		seenAssets[source.FileAssetID], seenIndexes[source.DocumentIndex] = true, true
	}
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	s.imports[job.ID] = job
	return job, nil
}

func (s *MemoryStore) AddPaperImportSources(_ context.Context, tenantID, id, _ string, input AddPaperImportSourcesInput) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status == "applied" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	if input.ExpectedGeneration != job.Generation {
		return PaperImportJob{}, ErrConflict
	}
	seen := map[string]bool{}
	indexes := map[int]bool{}
	for _, source := range job.Sources {
		seen[source.FileAssetID] = true
		indexes[source.DocumentIndex] = true
	}
	now := time.Now().UTC()
	for _, source := range input.Sources {
		if source.FileAssetID == "" || source.DocumentIndex < 0 || seen[source.FileAssetID] || indexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		job.Sources = append(job.Sources, PaperImportSource{ID: s.id("paper-import-source"), FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: defaultRoleHint(source.RoleHint), DetectedRole: "unknown", ProcessingStatus: "pending", CreatedAt: now})
		seen[source.FileAssetID], indexes[source.DocumentIndex] = true, true
	}
	job.Status, job.ErrorCode, job.Issues, job.UpdatedAt = "processing", "", []string{}, now
	job.Generation++
	job.RunID = s.id(paperImportRunIDPrefix)
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	job.ResultGeneration = 0
	job.StructuredIssues = []PaperImportIssue{}
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) ReplacePaperImportSources(_ context.Context, tenantID, id, _ string, input ReplacePaperImportSourcesInput) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status == "applied" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	if input.ExpectedGeneration != job.Generation {
		return PaperImportJob{}, ErrConflict
	}
	current := map[string]PaperImportSource{}
	for _, source := range job.Sources {
		current[source.ID] = source
	}
	seenIDs := map[string]bool{}
	seenIndexes := map[int]bool{}
	replaced := make([]PaperImportSource, 0, len(input.Sources))
	now := time.Now().UTC()
	for _, inputSource := range input.Sources {
		source, exists := current[inputSource.ID]
		if !exists || inputSource.DocumentIndex < 0 || seenIDs[inputSource.ID] || seenIndexes[inputSource.DocumentIndex] || !validPaperImportRole(inputSource.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		source.DocumentIndex = inputSource.DocumentIndex
		source.RoleHint = defaultRoleHint(inputSource.RoleHint)
		source.DetectedRole = "unknown"
		source.RoleConfidence = 0
		source.ProcessingStatus = "pending"
		replaced = append(replaced, source)
		seenIDs[inputSource.ID], seenIndexes[inputSource.DocumentIndex] = true, true
	}
	sort.Slice(replaced, func(i, j int) bool { return replaced[i].DocumentIndex < replaced[j].DocumentIndex })
	job.Sources = replaced
	job.StructuredIssues = []PaperImportIssue{}
	if len(replaced) == 0 {
		job.Status = "failed"
		job.ErrorCode = "paper_import_no_sources"
		job.Issues = []string{"已删除全部考试资料，请重新上传正确的资料"}
	} else {
		job.Status = "processing"
		job.ErrorCode = ""
		job.Issues = []string{}
	}
	job.UpdatedAt = now
	job.Generation++
	job.RunID = s.id(paperImportRunIDPrefix)
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	job.ResultGeneration = 0
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) CompletePaperImportCandidates(_ context.Context, tenantID, id string, parsed PaperImportParseResult) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 这里沿用六类候选结果的存储范围；解析响应中的模型用量不随候选写入。
	detected, questions, answers := parsed.Documents, parsed.QuestionCandidates, parsed.AnswerCandidates
	solutions, rubrics, issues := parsed.SolutionCandidates, parsed.RubricCandidates, parsed.Issues
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status == "cancelled" || job.Status == "applied" {
		return PaperImportJob{}, ErrConflict
	}
	job.QuestionCandidates, job.AnswerCandidates, job.SolutionCandidates, job.RubricCandidates = questions, answers, solutions, rubrics
	fresh, structured := reconcilePaperImportCandidates(questions, answers, solutions, rubrics, appendDetectedRoleIssues(issues, detected))
	structured = appendHumanConfirmationConflicts(structured, fresh, job.Questions)
	job.Questions = preserveHumanConfirmedDrafts(fresh, job.Questions)
	job.StructuredIssues = issuesAfterHumanReview(structured, job.Questions, false)
	job.Issues = issueMessages(job.StructuredIssues)
	job.Status = "review_required"
	job.ErrorCode = ""
	job.UpdatedAt = time.Now().UTC()
	roles := map[string]PaperImportDetectedDocument{}
	for _, item := range detected {
		roles[item.SourceID] = item
	}
	for i := range job.Sources {
		job.Sources[i].ProcessingStatus = "processed"
		if item, ok := roles[job.Sources[i].ID]; ok && validPaperImportRole(item.DetectedRole, false) {
			job.Sources[i].DetectedRole, job.Sources[i].RoleConfidence = item.DetectedRole, item.RoleConfidence
		}
	}
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) SavePaperImportReview(_ context.Context, tenantID, id, _ string, input ReviewPaperImportInput) (PaperImportJob, error) {
	expectedUpdatedAt, err := time.Parse(time.RFC3339Nano, input.ExpectedUpdatedAt)
	if err != nil || expectedUpdatedAt.IsZero() {
		return PaperImportJob{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	// 与数据库路径一样，在持锁期间校验内容快照；同generation的旧整卷不能静默覆盖。
	if job.Status != "review_required" || job.Generation != input.ExpectedGeneration || !job.UpdatedAt.Equal(expectedUpdatedAt) {
		return PaperImportJob{}, ErrConflict
	}
	reviewedQuestions, reconciliationIssues := s.reconcileMemoryPaperImport(job.ExamID, input.Questions, nil)
	input.Questions = reviewedQuestions
	for i := range input.Questions {
		normalizeAnswerKeyOnlyPaperImportDraft(&input.Questions[i])
		if input.Questions[i].AssessmentArchetype == "" {
			input.Questions[i].AssessmentArchetype = defaultPaperImportArchetype(input.Questions[i].QuestionType)
		}
		input.Questions[i].HumanConfirmedFields = normalizeHumanConfirmedFields(input.Questions[i].HumanConfirmedFields)
		refreshDraftCompleteness(&input.Questions[i], len(job.QuestionCandidates) > 0)
	}
	job.Questions = input.Questions
	job.StructuredIssues = withPaperImportReconciliationIssues(appendReviewedDraftIssues(issuesAfterHumanReview(job.StructuredIssues, job.Questions, true), job.Questions), reconciliationIssues)
	job.Issues = issueMessages(job.StructuredIssues)
	// 连续保存可能落在同一系统时钟刻度，仍须使旧预览和推理请求失效。
	now := time.Now().UTC()
	if !now.After(job.UpdatedAt) {
		now = job.UpdatedAt.Add(time.Nanosecond)
	}
	job.UpdatedAt = now
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) CompletePaperImport(_ context.Context, tenantID, id string, questions []PaperImportDraftQuestion, issues []string) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	questions, issues = s.reconcileMemoryPaperImport(job.ExamID, questions, issues)
	job.Status, job.Questions, job.Issues, job.UpdatedAt = "review_required", questions, issues, time.Now().UTC()
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) FailPaperImport(_ context.Context, tenantID, id, code string, issues []string) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status != "processing" {
		return PaperImportJob{}, ErrConflict
	}
	job.Status, job.ErrorCode, job.Issues, job.UpdatedAt = "failed", code, issues, time.Now().UTC()
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) CancelPaperImport(_ context.Context, tenantID, id string) (PaperImportJob, error) {
	return s.cancelPaperImportGeneration(tenantID, id, 0)
}

func (s *MemoryStore) CancelPaperImportGeneration(_ context.Context, tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	if expectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	return s.cancelPaperImportGeneration(tenantID, id, expectedGeneration)
}

func (s *MemoryStore) cancelPaperImportGeneration(tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status != "processing" {
		return PaperImportJob{}, ErrConflict
	}
	if expectedGeneration > 0 && job.Generation != expectedGeneration {
		return PaperImportJob{}, ErrConflict
	}
	job.Status = "cancelled"
	job.ErrorCode = "paper_import_cancelled"
	job.Issues = []string{"识别任务已手动停止"}
	job.UpdatedAt = time.Now().UTC()
	for index := range job.Sources {
		if job.Sources[index].ProcessingStatus == "pending" || job.Sources[index].ProcessingStatus == "processing" {
			job.Sources[index].ProcessingStatus = "failed"
		}
	}
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) GetPaperImport(_ context.Context, tenantID, id string) (PaperImportJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	return job, nil
}

func (s *MemoryStore) ListPaperImports(_ context.Context, tenantID, examID string) ([]PaperImportJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []PaperImportJob{}
	for _, job := range s.imports {
		if job.TenantID == tenantID && job.ExamID == examID {
			out = append(out, job)
		}
	}
	return out, nil
}

func (s *MemoryStore) ApplyPaperImport(_ context.Context, tenantID, id, userID string) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status != "review_required" || job.StructureConfirmedGeneration > 0 && job.StructureConfirmedGeneration == job.Generation {
		return PaperImportJob{}, ErrConflict
	}
	if err := s.ensureExamPaperMutableLocked(job.ExamID); err != nil {
		return PaperImportJob{}, err
	}
	existing := s.importQuestionCountLocked(tenantID, job.ExamID)
	job.Questions, job.Issues = s.reconcileMemoryPaperImport(job.ExamID, job.Questions, job.Issues)
	if paperImportHasBlockingIssues(job) {
		return PaperImportJob{}, ErrInvalidInput
	}
	for i := range job.Questions {
		if err := s.applyImportQuestionLocked(tenantID, job, &job.Questions[i], i, existing > 0); err != nil {
			return PaperImportJob{}, err
		}
	}
	now := time.Now().UTC()
	job.Status, job.AppliedAt, job.UpdatedAt = "applied", &now, now
	s.imports[id] = job
	_ = userID
	return job, nil
}

func (s *MemoryStore) importQuestionCountLocked(tenantID, examID string) int {
	count := 0
	for _, question := range s.questions {
		if question.TenantID == tenantID && question.ExamID == examID && question.Status != "deleted" {
			count++
		}
	}
	return count
}

// 每题依次校验和写入；后题失败时，保留前题修改与已经消耗的 ID。
func (s *MemoryStore) applyImportQuestionLocked(tenantID string, job PaperImportJob, draft *PaperImportDraftQuestion, index int, hasExisting bool) error {
	if err := validateMemoryImportDraft(draft); err != nil {
		return err
	}
	if hasExisting {
		return s.updateImportedQuestionLocked(tenantID, job, draft)
	}
	return s.createImportedQuestionLocked(tenantID, job, draft, index)
}

func validateMemoryImportDraft(draft *PaperImportDraftQuestion) error {
	if err := validateQuestionInput(draft.QuestionNo, draft.QuestionType, draft.Score); err != nil {
		return ErrInvalidInput
	}
	if draft.Rubric != nil && !ValidRubricPointIdentities(draft.Rubric.Points) {
		return ErrInvalidInput
	}
	if draft.Rubric != nil && (!scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score) || !scoreEqual(draft.Rubric.MaxScore, draft.Score)) {
		return ErrRubricMismatch
	}
	if draft.Rubric != nil && draft.Rubric.Status == "" {
		draft.Rubric.Status = "draft"
	}
	if draft.Rubric != nil && (!IsValidRubricStatus(draft.Rubric.Status) || !ValidRubricEvidenceRequirements(draft.Rubric.Points)) {
		return ErrInvalidInput
	}
	return nil
}

func (s *MemoryStore) updateImportedQuestionLocked(tenantID string, job PaperImportJob, draft *PaperImportDraftQuestion) error {
	if draft.MatchedQuestionID == "" || draft.MatchStatus == "extra" || draft.MatchStatus == "ambiguous" {
		return nil
	}
	q, ok := s.questions[draft.MatchedQuestionID]
	if !ok || q.TenantID != tenantID || q.ExamID != job.ExamID {
		return ErrConflict
	}
	q.ExamPaperID, q.Stem, q.KnowledgePoints = job.ExamPaperID, draft.Stem, cloneStrings(draft.KnowledgePoints)
	q.QuestionType, q.Score, q.AssessmentArchetype = draft.QuestionType, draft.Score, draft.AssessmentArchetype
	q.ScoreSource = importedScoreSource(*draft)
	q.ParentQuestionNo, q.SubquestionNo, q.Options = draft.ParentQuestionNo, draft.SubquestionNo, cloneStrings(draft.Options)
	q.PaperImportID, q.PaperImportCandidateID, q.PaperImportSourceRefs = job.ID, draft.CandidateID, append([]PaperImportSourceRef{}, draft.SourceRefs...)
	s.attachImportedAnswerSolutionLocked(&q, job, draft, fmt.Sprintf("v%d", 1))
	if draft.Rubric != nil {
		// 已锁量规的检查仍晚于答案、解析 ID 分配，但早于本题写回。
		if rubrics := s.rubrics[q.ID]; len(rubrics) > 0 && rubrics[len(rubrics)-1].Status == "locked" {
			return ErrRubricLocked
		}
		r, err := s.importedRubricLocked(q.ID, job, draft, fmt.Sprintf("v%d", len(s.rubrics[q.ID])+1))
		if err != nil {
			return err
		}
		s.rubrics[q.ID] = append(s.rubrics[q.ID], r)
	}
	s.questions[q.ID] = q
	return nil
}

func (s *MemoryStore) createImportedQuestionLocked(tenantID string, job PaperImportJob, draft *PaperImportDraftQuestion, index int) error {
	qid := s.id("question")
	q := Question{ID: qid, TenantID: tenantID, ExamID: job.ExamID, ExamPaperID: job.ExamPaperID, QuestionNo: draft.QuestionNo, QuestionType: draft.QuestionType, AssessmentArchetype: draft.AssessmentArchetype, Score: draft.Score, Stem: draft.Stem, ParentQuestionNo: draft.ParentQuestionNo, SubquestionNo: draft.SubquestionNo, Options: cloneStrings(draft.Options), KnowledgePoints: cloneStrings(draft.KnowledgePoints), AnswerArea: map[string]any{}, SortOrder: index + 1, Status: "active", PaperImportID: job.ID, PaperImportCandidateID: draft.CandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
	q.ScoreSource = importedScoreSource(*draft)
	s.attachImportedAnswerSolutionLocked(&q, job, draft, "v1")
	// 新题先写入，再校验量规确认；失败时维持原有部分副作用。
	s.questions[qid] = q
	if draft.Rubric != nil {
		r, err := s.importedRubricLocked(qid, job, draft, "v1")
		if err != nil {
			return err
		}
		s.rubrics[qid] = []Rubric{r}
	}
	draft.MatchedQuestionID = qid
	draft.MatchStatus = "matched"
	return nil
}

func (s *MemoryStore) attachImportedAnswerSolutionLocked(q *Question, job PaperImportJob, draft *PaperImportDraftQuestion, answerVersion string) {
	if draft.AnswerKey != nil {
		q.AnswerKey = &AnswerKey{ID: s.id("answer"), QuestionID: q.ID, AnswerVersion: answerVersion, StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers, Tolerance: draft.AnswerKey.Tolerance, PaperImportID: job.ID, PaperImportCandidateID: draft.AnswerCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
	}
	if draft.Solution != nil {
		q.Solution = &QuestionSolution{ID: s.id("solution"), QuestionID: q.ID, PaperImportID: job.ID, SolutionVersion: "v1", RawText: draft.Solution.RawText, Steps: draft.Solution.Steps, SourceRefs: draft.Solution.SourceRefs, VerificationStatus: solutionVerificationStatus(*draft)}
	}
}

func (s *MemoryStore) importedRubricLocked(questionID string, job PaperImportJob, draft *PaperImportDraftQuestion, version string) (Rubric, error) {
	status := draft.Rubric.Status
	if status != "locked" {
		status = "draft"
	}
	if status == "locked" && !stringSet(draft.HumanConfirmedFields)["rubric"] {
		return Rubric{}, ErrInvalidInput
	}
	return Rubric{ID: s.id("rubric"), QuestionID: questionID, Version: version, Status: status, MaxScore: draft.Rubric.MaxScore, Points: draft.Rubric.Points, Deductions: draft.Rubric.Deductions, Examples: draft.Rubric.Examples, PaperImportID: job.ID, PaperImportCandidateID: draft.RubricCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}, nil
}

func solutionVerificationStatus(draft PaperImportDraftQuestion) string {
	if stringSet(draft.HumanConfirmedFields)["solution"] {
		return "human_confirmed"
	}
	return "machine"
}

func (s *MemoryStore) reconcileMemoryPaperImport(examID string, drafts []PaperImportDraftQuestion, issues []string) ([]PaperImportDraftQuestion, []string) {
	existing := []Question{}
	for _, question := range s.questions {
		if question.ExamID == examID && question.Status != "deleted" {
			existing = append(existing, question)
		}
	}
	blueprint := make([]paperImportExistingQuestion, 0, len(existing))
	for _, question := range existing {
		blueprint = append(blueprint, paperImportExistingQuestion{id: question.ID, number: question.QuestionNo, kind: question.QuestionType, score: question.Score, sortOrder: question.SortOrder})
	}
	var expected *float64
	if total, ok := s.examTotals[examID]; ok {
		expected = &total
	}
	return reconcilePaperImportDrafts(drafts, blueprint, expected, issues)
}
