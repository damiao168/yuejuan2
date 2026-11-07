package paper

import (
	"context"
	"fmt"
	"sync"
)

const (
	paperImportRunIDPrefix = "paper-import-run"
)

// MemoryStore 以进程内锁维护状态，不提供数据库事务回滚或跨进程任务租约。
// 用它验证业务分支时，不能据此推断 PostgreSQL 的并发提交和持久化恢复行为。
type MemoryStore struct {
	mu                      sync.RWMutex
	next                    int
	examTotals              map[string]float64
	papers                  map[string]Paper
	questions               map[string]Question
	rubrics                 map[string][]Rubric
	templates               map[string]AnswerSheetTemplate
	bindings                map[string]ExamTemplateBinding
	bindingRevisions        map[string]int
	readiness               map[string]ReadinessResult
	imports                 map[string]PaperImportJob
	examState               map[string]memoryExamState
	rubricDraftJobs         map[string]rubricDraftJobRecord
	templateLibrary         map[string]AnswerSheetTemplateLibraryVersion
	librarySources          map[string]templateLibrarySource
	libraryInstances        map[string]string
	libraryExams            QuestionRubricExamReader
	rubricDraftExamBoundary rubricDraftExamBoundary
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
		next:             1,
		examTotals:       map[string]float64{},
		papers:           map[string]Paper{},
		questions:        map[string]Question{},
		rubrics:          map[string][]Rubric{},
		templates:        map[string]AnswerSheetTemplate{},
		bindings:         map[string]ExamTemplateBinding{},
		bindingRevisions: map[string]int{},
		readiness:        map[string]ReadinessResult{},
		imports:          map[string]PaperImportJob{},
		examState:        map[string]memoryExamState{},
		rubricDraftJobs:  map[string]rubricDraftJobRecord{},
		templateLibrary:  map[string]AnswerSheetTemplateLibraryVersion{},
		librarySources:   map[string]templateLibrarySource{},
		libraryInstances: map[string]string{},
	}
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
	purpose, err := normalizePaperPurpose(input.Purpose)
	if err != nil {
		return Paper{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureExamPaperMutableLocked(examID); err != nil {
		return Paper{}, err
	}
	item, exists, err := s.replayedPaperForFileLocked(tenantID, examID, input.FileAssetID, purpose)
	if err != nil {
		return Paper{}, err
	}
	if exists {
		item.Duplicate = true
		return item, nil
	}
	if purpose == PaperPurposeAnswerSheet && input.File.HashSHA256 != "" {
		for _, item := range s.papers {
			if item.TenantID == tenantID && item.ExamID == examID && item.Purpose == purpose && item.File.HashSHA256 == input.File.HashSHA256 {
				item.Duplicate = true
				return item, nil
			}
		}
	}
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
		Purpose:     purpose,
		File:        input.File,
	}
	if paper.FileAssetID == "" {
		paper.FileAssetID = s.id("file")
	}
	s.papers[paper.ID] = paper
	return paper, nil
}

func (s *MemoryStore) replayedPaperForFileLocked(tenantID, examID, fileAssetID, purpose string) (Paper, bool, error) {
	if fileAssetID == "" {
		return Paper{}, false, nil
	}
	for _, item := range s.papers {
		if item.TenantID == tenantID && item.FileAssetID == fileAssetID {
			itemPurpose, _ := normalizePaperPurpose(item.Purpose)
			if item.ExamID != examID || itemPurpose != purpose {
				return Paper{}, false, ErrInvalidInput
			}
			return item, true, nil
		}
	}
	return Paper{}, false, nil
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
		ScoreSource:      "human",
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
		item.ScoreSource = "human"
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
	if !ValidRubricPointIdentities(input.Points) {
		return Rubric{}, ErrInvalidInput
	}
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
