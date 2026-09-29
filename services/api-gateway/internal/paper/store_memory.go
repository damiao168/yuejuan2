package paper

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore 以进程内锁维护状态，不提供数据库事务回滚或跨进程任务租约。
// 用它验证业务分支时，不能据此推断 PostgreSQL 的并发提交和持久化恢复行为。
type MemoryStore struct {
	mu         sync.RWMutex
	next       int
	examTotals map[string]float64
	papers     map[string]Paper
	questions  map[string]Question
	rubrics    map[string][]Rubric
	templates  map[string]AnswerSheetTemplate
	bindings   map[string]ExamTemplateBinding
	readiness  map[string]ReadinessResult
	imports    map[string]PaperImportJob
	examState  map[string]memoryExamState
}

type memoryExamState struct {
	Total        float64
	ClassCount   int
	StudentCount int
	Status       string
}

func (s *MemoryStore) ensureExamPaperMutableLocked(examID string) error {
	status := s.examState[examID].Status
	if status != "" && status != "draft" && status != "configured" {
		return ErrExamFrozen
	}
	return nil
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:       1,
		examTotals: map[string]float64{},
		papers:     map[string]Paper{},
		questions:  map[string]Question{},
		rubrics:    map[string][]Rubric{},
		templates:  map[string]AnswerSheetTemplate{},
		bindings:   map[string]ExamTemplateBinding{},
		readiness:  map[string]ReadinessResult{},
		imports:    map[string]PaperImportJob{},
		examState:  map[string]memoryExamState{},
	}
}

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
	job := PaperImportJob{ID: s.id("paper-import"), TenantID: tenantID, ExamID: examID, ExamPaperID: input.ExamPaperID, PaperFileAssetID: input.PaperFileAssetID, AnswerFileAssetID: input.AnswerFileAssetID, Status: "processing", Generation: 1, RunID: s.id("paper-import-run"), Subject: input.Subject, Questions: []PaperImportDraftQuestion{}, Issues: []string{}, QuestionCandidates: []QuestionCandidate{}, AnswerCandidates: []AnswerCandidate{}, SolutionCandidates: []SolutionCandidate{}, RubricCandidates: []RubricCandidate{}, StructuredIssues: []PaperImportIssue{}, CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
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
	job.RunID = s.id("paper-import-run")
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
	job.RunID = s.id("paper-import-run")
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	job.ResultGeneration = 0
	s.imports[id] = job
	return job, nil
}

func (s *MemoryStore) CompletePaperImportCandidates(_ context.Context, tenantID, id string, detected []PaperImportDetectedDocument, questions []QuestionCandidate, answers []AnswerCandidate, solutions []SolutionCandidate, rubrics []RubricCandidate, issues []PaperImportIssue) (PaperImportJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.imports[id]
	if !ok || job.TenantID != tenantID {
		return PaperImportJob{}, ErrNotFound
	}
	if job.Status != "review_required" || job.Generation != input.ExpectedGeneration {
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
	job.UpdatedAt = time.Now().UTC()
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
	if job.Status != "review_required" {
		return PaperImportJob{}, ErrConflict
	}
	if err := s.ensureExamPaperMutableLocked(job.ExamID); err != nil {
		return PaperImportJob{}, err
	}
	existing := 0
	for _, question := range s.questions {
		if question.TenantID == tenantID && question.ExamID == job.ExamID && question.Status != "deleted" {
			existing++
		}
	}
	job.Questions, job.Issues = s.reconcileMemoryPaperImport(job.ExamID, job.Questions, job.Issues)
	if paperImportHasBlockingIssues(job) {
		return PaperImportJob{}, ErrInvalidInput
	}
	for i := range job.Questions {
		draft := &job.Questions[i]
		if err := validateQuestionInput(draft.QuestionNo, draft.QuestionType, draft.Score); err != nil {
			return PaperImportJob{}, ErrInvalidInput
		}
		if draft.Rubric != nil && (!scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score) || !scoreEqual(draft.Rubric.MaxScore, draft.Score)) {
			return PaperImportJob{}, ErrRubricMismatch
		}
		if draft.Rubric != nil && draft.Rubric.Status == "" {
			draft.Rubric.Status = "draft"
		}
		if draft.Rubric != nil && (!IsValidRubricStatus(draft.Rubric.Status) || !ValidRubricEvidenceRequirements(draft.Rubric.Points)) {
			return PaperImportJob{}, ErrInvalidInput
		}
		if existing > 0 {
			if draft.MatchedQuestionID == "" || draft.MatchStatus == "extra" || draft.MatchStatus == "ambiguous" {
				continue
			}
			q, ok := s.questions[draft.MatchedQuestionID]
			if !ok || q.TenantID != tenantID || q.ExamID != job.ExamID {
				return PaperImportJob{}, ErrConflict
			}
			q.ExamPaperID, q.Stem, q.KnowledgePoints = job.ExamPaperID, draft.Stem, cloneStrings(draft.KnowledgePoints)
			q.QuestionType, q.Score, q.AssessmentArchetype = draft.QuestionType, draft.Score, draft.AssessmentArchetype
			q.ParentQuestionNo, q.SubquestionNo, q.Options = draft.ParentQuestionNo, draft.SubquestionNo, cloneStrings(draft.Options)
			q.PaperImportID, q.PaperImportCandidateID, q.PaperImportSourceRefs = job.ID, draft.CandidateID, append([]PaperImportSourceRef{}, draft.SourceRefs...)
			{
				if draft.AnswerKey != nil {
					q.AnswerKey = &AnswerKey{ID: s.id("answer"), QuestionID: q.ID, AnswerVersion: fmt.Sprintf("v%d", 1), StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers, Tolerance: draft.AnswerKey.Tolerance, PaperImportID: job.ID, PaperImportCandidateID: draft.AnswerCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
				}
				if draft.Solution != nil {
					q.Solution = &QuestionSolution{ID: s.id("solution"), QuestionID: q.ID, PaperImportID: job.ID, SolutionVersion: "v1", RawText: draft.Solution.RawText, Steps: draft.Solution.Steps, SourceRefs: draft.Solution.SourceRefs, VerificationStatus: solutionVerificationStatus(*draft)}
				}
				if draft.Rubric != nil {
					if rubrics := s.rubrics[q.ID]; len(rubrics) > 0 && rubrics[len(rubrics)-1].Status == "locked" {
						return PaperImportJob{}, ErrRubricLocked
					}
					status := draft.Rubric.Status
					if status != "locked" {
						status = "draft"
					}
					if status == "locked" && !stringSet(draft.HumanConfirmedFields)["rubric"] {
						return PaperImportJob{}, ErrInvalidInput
					}
					r := Rubric{ID: s.id("rubric"), QuestionID: q.ID, Version: fmt.Sprintf("v%d", len(s.rubrics[q.ID])+1), Status: status, MaxScore: draft.Rubric.MaxScore, Points: draft.Rubric.Points, Deductions: draft.Rubric.Deductions, Examples: draft.Rubric.Examples, PaperImportID: job.ID, PaperImportCandidateID: draft.RubricCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
					s.rubrics[q.ID] = append(s.rubrics[q.ID], r)
				}
			}
			s.questions[q.ID] = q
			continue
		}
		qid := s.id("question")
		q := Question{ID: qid, TenantID: tenantID, ExamID: job.ExamID, ExamPaperID: job.ExamPaperID, QuestionNo: draft.QuestionNo, QuestionType: draft.QuestionType, AssessmentArchetype: draft.AssessmentArchetype, Score: draft.Score, Stem: draft.Stem, ParentQuestionNo: draft.ParentQuestionNo, SubquestionNo: draft.SubquestionNo, Options: cloneStrings(draft.Options), KnowledgePoints: cloneStrings(draft.KnowledgePoints), AnswerArea: map[string]any{}, SortOrder: i + 1, Status: "active", PaperImportID: job.ID, PaperImportCandidateID: draft.CandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
		if draft.AnswerKey != nil {
			q.AnswerKey = &AnswerKey{ID: s.id("answer"), QuestionID: qid, AnswerVersion: "v1", StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers, Tolerance: draft.AnswerKey.Tolerance, PaperImportID: job.ID, PaperImportCandidateID: draft.AnswerCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
		}
		if draft.Solution != nil {
			q.Solution = &QuestionSolution{ID: s.id("solution"), QuestionID: qid, PaperImportID: job.ID, SolutionVersion: "v1", RawText: draft.Solution.RawText, Steps: draft.Solution.Steps, SourceRefs: draft.Solution.SourceRefs, VerificationStatus: solutionVerificationStatus(*draft)}
		}
		s.questions[qid] = q
		if draft.Rubric != nil {
			status := draft.Rubric.Status
			if status != "locked" {
				status = "draft"
			}
			if status == "locked" && !stringSet(draft.HumanConfirmedFields)["rubric"] {
				return PaperImportJob{}, ErrInvalidInput
			}
			r := Rubric{ID: s.id("rubric"), QuestionID: qid, Version: "v1", Status: status, MaxScore: draft.Rubric.MaxScore, Points: draft.Rubric.Points, Deductions: draft.Rubric.Deductions, Examples: draft.Rubric.Examples, PaperImportID: job.ID, PaperImportCandidateID: draft.RubricCandidateID, PaperImportSourceRefs: append([]PaperImportSourceRef{}, draft.SourceRefs...)}
			s.rubrics[qid] = []Rubric{r}
		}
		draft.MatchedQuestionID = qid
		draft.MatchStatus = "matched"
	}
	now := time.Now().UTC()
	job.Status, job.AppliedAt, job.UpdatedAt = "applied", &now, now
	s.imports[id] = job
	_ = userID
	return job, nil
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

func (s *MemoryStore) SetExamTotal(examID string, total float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.examTotals[examID] = total
	state := s.examState[examID]
	state.Total = total
	if state.Status == "" {
		state.Status = "configured"
	}
	s.examState[examID] = state
}

func (s *MemoryStore) SetReadinessContext(examID string, total float64, classCount int, studentCount int, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.examTotals[examID] = total
	s.examState[examID] = memoryExamState{Total: total, ClassCount: classCount, StudentCount: studentCount, Status: status}
}

func (s *MemoryStore) ExamStatus(examID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.examState[examID].Status
}

func (s *MemoryStore) CreatePaper(_ context.Context, tenantID string, examID string, _ string, input CreatePaperInput) (Paper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version := 1
	for _, item := range s.papers {
		if item.TenantID == tenantID && item.ExamID == examID && item.VersionNo >= version {
			version = item.VersionNo + 1
		}
	}
	paper := Paper{
		ID:          s.id("paper"),
		TenantID:    tenantID,
		ExamID:      examID,
		FileAssetID: input.FileAssetID,
		VersionNo:   version,
		Status:      "uploaded",
		File:        input.File,
	}
	if paper.FileAssetID == "" {
		paper.FileAssetID = s.id("file")
	}
	s.papers[paper.ID] = paper
	return paper, nil
}

func (s *MemoryStore) ListPapers(_ context.Context, tenantID string, examID string) ([]Paper, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Paper{}
	for _, item := range s.papers {
		if item.TenantID == tenantID && item.ExamID == examID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateQuestion(_ context.Context, tenantID string, examID string, _ string, input CreateQuestionInput) (Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureExamPaperMutableLocked(examID); err != nil {
		return Question{}, err
	}
	if input.ExamPaperID != "" {
		paper, ok := s.papers[input.ExamPaperID]
		if !ok || paper.TenantID != tenantID || paper.ExamID != examID {
			return Question{}, ErrNotFound
		}
	}
	question := Question{
		ParentQuestionNo: input.ParentQuestionNo,
		SubquestionNo:    input.SubquestionNo,
		Options:          cloneStrings(input.Options),
		ID:               s.id("question"),
		TenantID:         tenantID,
		ExamID:           examID,
		ExamPaperID:      input.ExamPaperID,
		QuestionNo:       input.QuestionNo,
		QuestionType:     input.QuestionType,
		Score:            input.Score,
		Stem:             input.Stem,
		KnowledgePoints:  cloneStrings(input.KnowledgePoints),
		AnswerArea:       cloneMap(input.AnswerArea),
		SortOrder:        input.SortOrder,
		Status:           "active",
	}
	if input.AnswerKey != nil {
		question.AnswerKey = &AnswerKey{
			ID:                s.id("answer"),
			QuestionID:        question.ID,
			AnswerVersion:     "v1",
			StandardAnswer:    input.AnswerKey.StandardAnswer,
			EquivalentAnswers: input.AnswerKey.EquivalentAnswers,
			Tolerance:         input.AnswerKey.Tolerance,
		}
	}
	s.questions[question.ID] = question
	return question, nil
}

func (s *MemoryStore) ListQuestions(_ context.Context, tenantID string, examID string) ([]Question, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Question{}
	for _, item := range s.questions {
		if item.TenantID == tenantID && item.ExamID == examID && item.Status != "deleted" {
			if rubrics := s.rubrics[item.ID]; len(rubrics) > 0 {
				latest := rubrics[len(rubrics)-1]
				item.Rubric = &latest
			}
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) UpdateQuestion(_ context.Context, tenantID string, id string, _ string, input UpdateQuestionInput) (Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.questions[id]
	if !ok || item.TenantID != tenantID || item.Status == "deleted" {
		return Question{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(item.ExamID); err != nil {
		return Question{}, err
	}
	if input.QuestionNo != nil {
		item.QuestionNo = *input.QuestionNo
	}
	if input.ParentQuestionNo != nil {
		item.ParentQuestionNo = *input.ParentQuestionNo
	}
	if input.SubquestionNo != nil {
		item.SubquestionNo = *input.SubquestionNo
	}
	if input.Options != nil {
		item.Options = cloneStrings(*input.Options)
	}
	if input.QuestionType != nil {
		item.QuestionType = *input.QuestionType
	}
	if input.Score != nil {
		item.Score = *input.Score
	}
	if input.Stem != nil {
		item.Stem = *input.Stem
	}
	if input.KnowledgePoints != nil {
		item.KnowledgePoints = cloneStrings(*input.KnowledgePoints)
	}
	if input.AnswerArea != nil {
		item.AnswerArea = cloneMap(*input.AnswerArea)
	}
	if input.SortOrder != nil {
		item.SortOrder = *input.SortOrder
	}
	if input.AnswerKey != nil {
		item.AnswerKey = &AnswerKey{ID: s.id("answer"), QuestionID: item.ID, AnswerVersion: "v2", StandardAnswer: input.AnswerKey.StandardAnswer, EquivalentAnswers: input.AnswerKey.EquivalentAnswers, Tolerance: input.AnswerKey.Tolerance}
	}
	s.questions[id] = item
	return item, nil
}

func (s *MemoryStore) DeleteQuestion(_ context.Context, tenantID string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.questions[id]
	if !ok || item.TenantID != tenantID {
		return ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(item.ExamID); err != nil {
		return err
	}
	item.Status = "deleted"
	s.questions[id] = item
	return nil
}

func (s *MemoryStore) CreateRubric(_ context.Context, tenantID string, questionID string, _ string, input RubricInput) (Rubric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	question, ok := s.questions[questionID]
	if !ok || question.TenantID != tenantID || question.Status == "deleted" {
		return Rubric{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(question.ExamID); err != nil {
		return Rubric{}, err
	}
	existing := s.rubrics[questionID]
	if len(existing) > 0 && existing[len(existing)-1].Status == "locked" {
		return Rubric{}, ErrRubricLocked
	}
	if !ValidRubricEvidenceRequirements(input.Points) {
		return Rubric{}, ErrInvalidInput
	}
	if !scoreEqual(SumRubricPoints(input.Points), question.Score) || !scoreEqual(input.MaxScore, question.Score) {
		return Rubric{}, ErrRubricMismatch
	}
	status := input.Status
	if status == "" {
		status = "draft"
	}
	rubric := Rubric{
		ID:         s.id("rubric"),
		QuestionID: questionID,
		Version:    fmt.Sprintf("v%d", len(existing)+1),
		Status:     status,
		MaxScore:   input.MaxScore,
		Points:     input.Points,
		Deductions: input.Deductions,
		Examples:   input.Examples,
	}
	s.rubrics[questionID] = append(existing, rubric)
	return rubric, nil
}

func (s *MemoryStore) ValidateConfig(_ context.Context, tenantID string, examID string) (ValidationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := ValidationResult{Valid: true, Issues: []ValidationIssue{}}
	total := 0.0
	questionCount := 0
	for _, question := range s.questions {
		if question.TenantID != tenantID || question.ExamID != examID || question.Status == "deleted" {
			continue
		}
		questionCount++
		total += question.Score
		if question.QuestionType == "" || question.QuestionNo == "" {
			result.Issues = append(result.Issues, ValidationIssue{Code: "question_incomplete", Message: "题目缺少题号或题型"})
		}
		if question.AnswerArea == nil {
			result.Issues = append(result.Issues, ValidationIssue{Code: "answer_area_missing", Message: "第" + question.QuestionNo + "题缺少答题区域"})
		}
	}
	if questionCount == 0 {
		result.Issues = append(result.Issues, ValidationIssue{Code: "no_questions", Message: "试卷尚未配置题目"})
	}
	if expected, ok := s.examTotals[examID]; ok && !scoreEqual(total, expected) {
		result.Issues = append(result.Issues, ValidationIssue{Code: "total_score_mismatch", Message: fmt.Sprintf("题目总分 %.2f 分与考试总分 %.2f 分不一致", total, expected)})
	}
	result.Valid = len(result.Issues) == 0
	return result, nil
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// 这里只复制第一层键值；嵌套 map、切片和指针仍共享，调用方不能把返回值当成深拷贝。
func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
