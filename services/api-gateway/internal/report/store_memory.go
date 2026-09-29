package report

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"fmt"
	"sync"
)

// MemoryStore 为测试提供单份报表数据集；种子数据不模拟数据库的租户和考试过滤。
type MemoryStore struct {
	receipts                 commandreceipt.Memory
	mu                       sync.RWMutex
	next                     int
	data                     dataset
	totalSegments            int
	doubleMarkDiffs          []float64
	arbitrationCount         int
	ocrTaskCount             int
	ocrFailedCount           int
	lowConfidenceReviewCount int
	exports                  []ExportResult
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, data: dataset{Submissions: []submissionRecord{}, Grades: []gradeRecord{}}}
}

type SubmissionSeed struct {
	ID            string
	ExamID        string
	StudentID     string
	ClassID       string
	ClassName     string
	AnonymousCode string
	TotalScore    float64
	MaxScore      float64
}

type GradeSeed struct {
	SubmissionID        string
	StudentID           string
	ClassID             string
	ClassName           string
	QuestionID          string
	QuestionNo          string
	QuestionType        string
	AnswerSegmentID     string
	Score               float64
	MaxScore            float64
	Source              string
	KnowledgePoints     []string
	AnswerPayload       map[string]any
	AIScore             *float64
	HumanScore          *float64
	AIFeedbackText      string
	TeacherFeedbackText string
	ErrorClueText       string
}

type QualitySeed struct {
	TotalSegments            int
	DoubleMarkDiffs          []float64
	ArbitrationCount         int
	OCRTaskCount             int
	OCRFailedCount           int
	LowConfidenceReviewCount int
}

func (s *MemoryStore) AddSubmission(seed SubmissionSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ExamID = seed.ExamID
	s.data.Submissions = append(s.data.Submissions, submissionRecord{
		ID:            seed.ID,
		StudentID:     seed.StudentID,
		ClassID:       seed.ClassID,
		ClassName:     seed.ClassName,
		AnonymousCode: seed.AnonymousCode,
		TotalScore:    seed.TotalScore,
		MaxScore:      seed.MaxScore,
	})
}

func (s *MemoryStore) AddGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := gradeRecord{
		SubmissionID:    seed.SubmissionID,
		StudentID:       seed.StudentID,
		ClassID:         seed.ClassID,
		ClassName:       seed.ClassName,
		QuestionID:      seed.QuestionID,
		QuestionNo:      seed.QuestionNo,
		QuestionType:    seed.QuestionType,
		AnswerSegmentID: seed.AnswerSegmentID,
		Score:           seed.Score,
		MaxScore:        seed.MaxScore,
		Source:          seed.Source,
		KnowledgePoints: append([]string(nil), seed.KnowledgePoints...),
		AnswerPayload:   seed.AnswerPayload,
		AIScore:         seed.AIScore,
		HumanScore:      seed.HumanScore,
	}
	if seed.AIFeedbackText != "" {
		record.AIFeedback = append(record.AIFeedback, FeedbackItem{QuestionID: seed.QuestionID, QuestionNo: seed.QuestionNo, Source: "ai_grade", Text: seed.AIFeedbackText})
	}
	if seed.TeacherFeedbackText != "" {
		record.TeacherFeedback = append(record.TeacherFeedback, FeedbackItem{QuestionID: seed.QuestionID, QuestionNo: seed.QuestionNo, Source: "human_grade", Text: seed.TeacherFeedbackText})
	}
	if seed.ErrorClueText != "" {
		record.ErrorClues = append(record.ErrorClues, ErrorClue{QuestionID: seed.QuestionID, QuestionNo: seed.QuestionNo, Source: "ai_grade", Text: seed.ErrorClueText})
	}
	s.data.Grades = append(s.data.Grades, record)
}

func (s *MemoryStore) SetQuality(seed QualitySeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalSegments = seed.TotalSegments
	s.doubleMarkDiffs = append([]float64(nil), seed.DoubleMarkDiffs...)
	s.arbitrationCount = seed.ArbitrationCount
	s.ocrTaskCount = seed.OCRTaskCount
	s.ocrFailedCount = seed.OCRFailedCount
	s.lowConfidenceReviewCount = seed.LowConfidenceReviewCount
}

func (s *MemoryStore) StudentReport(_ context.Context, _ string, examID string, studentID string) (StudentReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data := s.copyDataset(examID)
	return buildStudentReport(data, studentID), nil
}

func (s *MemoryStore) Overview(_ context.Context, _ string, examID string) (OverviewReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return buildOverview(s.copyDataset(examID)), nil
}

func (s *MemoryStore) ClassReports(_ context.Context, _ string, examID string) ([]ClassReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return buildClassReports(s.copyDataset(examID)), nil
}

func (s *MemoryStore) QuestionAnalysis(_ context.Context, _ string, examID string) ([]QuestionAnalysis, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return buildQuestionAnalysis(s.copyDataset(examID)), nil
}

func (s *MemoryStore) GradingQuality(_ context.Context, _ string, examID string) (GradingQualityReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data := s.copyDataset(examID)
	return s.qualityLocked(examID, data), nil
}

func (s *MemoryStore) Export(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay ExportResult
	if found, err := s.receipts.Load(ctx, tenantID, actorID, "report.export", examID, nil, &replay); err != nil || found {
		return replay, err
	}
	data := s.copyDataset(examID)
	overview := buildOverview(data)
	classes := buildClassReports(data)
	questions := buildQuestionAnalysis(data)
	quality := s.qualityLocked(examID, data)
	result := buildExportCSV(tenantID, examID, actorID, overview, classes, questions, quality)
	result.ReportID = s.id("report")
	s.exports = append(s.exports, result)
	if err := s.receipts.Save(ctx, tenantID, actorID, "report.export", examID, nil, result); err != nil {
		return ExportResult{}, err
	}
	return result, nil
}

func (s *MemoryStore) ExportCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.exports)
}

func (s *MemoryStore) copyDataset(examID string) dataset {
	data := dataset{ExamID: examID}
	data.Submissions = append(data.Submissions, s.data.Submissions...)
	data.Grades = append(data.Grades, s.data.Grades...)
	return data
}

func (s *MemoryStore) qualityLocked(examID string, data dataset) GradingQualityReport {
	out := GradingQualityReport{ExamID: examID, TotalSegments: s.totalSegments, ArbitrationCount: s.arbitrationCount, OCRTaskCount: s.ocrTaskCount, OCRFailedCount: s.ocrFailedCount, LowConfidenceReviewCount: s.lowConfidenceReviewCount}
	if out.TotalSegments == 0 {
		out.TotalSegments = len(data.Grades)
	}
	for _, grade := range data.Grades {
		if grade.AIScore != nil {
			out.AIGradeCount++
		}
		if grade.Source == "rule_auto" {
			out.AIAcceptedCount++
		}
		if grade.AIScore != nil && grade.HumanScore != nil {
			out.HumanComparableCount++
			if round2(*grade.AIScore) != round2(*grade.HumanScore) {
				out.HumanModifiedCount++
			}
		}
	}
	out.AIAdoptionRate = metric(out.AIAcceptedCount, out.AIGradeCount, "no_ai_grades")
	out.HumanModificationRate = metric(out.HumanModifiedCount, out.HumanComparableCount, "no_ai_human_comparison")
	out.DoubleMarkSessionCount = len(s.doubleMarkDiffs)
	if len(s.doubleMarkDiffs) == 0 {
		out.AverageDoubleMarkDiff = Metric{Available: false, Reason: "no_double_mark_sessions"}
		out.MaxDoubleMarkDiff = Metric{Available: false, Reason: "no_double_mark_sessions"}
	} else {
		var sum float64
		max := s.doubleMarkDiffs[0]
		for _, diff := range s.doubleMarkDiffs {
			sum += diff
			if diff > max {
				max = diff
			}
		}
		out.AverageDoubleMarkDiff = Metric{Available: true, Value: round2(sum / float64(len(s.doubleMarkDiffs))), Denominator: len(s.doubleMarkDiffs)}
		out.MaxDoubleMarkDiff = Metric{Available: true, Value: round2(max), Denominator: len(s.doubleMarkDiffs)}
	}
	out.OCRFailureRate = metric(out.OCRFailedCount, out.OCRTaskCount, "no_ocr_tasks")
	return out
}

func metric(numerator int, denominator int, emptyReason string) Metric {
	if denominator == 0 {
		return Metric{Available: false, Reason: emptyReason}
	}
	return Metric{Available: true, Value: round2(float64(numerator) / float64(denominator)), Numerator: numerator, Denominator: denominator}
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}
