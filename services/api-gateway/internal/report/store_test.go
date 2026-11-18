package report

import (
	"context"
	"testing"
)

func TestReportsUsePublishedGradesForStudentClassQuestionAndQuality(t *testing.T) {
	store := seededReportStore()

	student, err := store.StudentReport(context.Background(), "tenant-1", "exam-1", "student-1")
	if err != nil {
		t.Fatalf("student report: %v", err)
	}
	if student.TotalScore != 85 || len(student.Questions) != 2 || len(student.KnowledgeMastery) == 0 {
		t.Fatalf("student report mismatch: %#v", student)
	}
	if len(student.TeacherFeedback) == 0 || len(student.AIFeedback) == 0 || len(student.ErrorClues) == 0 {
		t.Fatalf("student report should include feedback and error clues: %#v", student)
	}

	overview, err := store.Overview(context.Background(), "tenant-1", "exam-1")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.StudentCount != 3 || overview.Stats.Average != 76.67 || overview.Stats.Median != 80 || overview.Stats.Highest != 85 || overview.Stats.Lowest != 65 {
		t.Fatalf("overview stats mismatch: %#v", overview.Stats)
	}

	classes, err := store.ClassReports(context.Background(), "tenant-1", "exam-1")
	if err != nil {
		t.Fatalf("classes: %v", err)
	}
	if len(classes) != 2 || classes[0].StudentCount == 0 || len(classes[0].FrequentWrongQuestions) == 0 {
		t.Fatalf("class reports mismatch: %#v", classes)
	}

	questions, err := store.QuestionAnalysis(context.Background(), "tenant-1", "exam-1")
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	if len(questions) != 2 || questions[0].ScoreRate == 0 || questions[0].CorrectRate == 0 {
		t.Fatalf("question analysis mismatch: %#v", questions)
	}
	if questions[0].OptionEmpty != nil && len(questions[0].OptionDistribution) == 0 {
		t.Fatalf("objective question should include real option distribution: %#v", questions[0])
	}

	quality, err := store.GradingQuality(context.Background(), "tenant-1", "exam-1")
	if err != nil {
		t.Fatalf("quality: %v", err)
	}
	if quality.AIAdoptionRate.Available || quality.AIAdoptionRate.Reason != "no_ai_adoption_evidence" || quality.AIAcceptedCount != 0 || !quality.OCRFailureRate.Available || quality.OCRFailureRate.Value != 0.25 {
		t.Fatalf("quality metrics mismatch: %#v", quality)
	}
}

func TestReportsReturnEmptyStateWithoutPublishedGrades(t *testing.T) {
	store := NewMemoryStore()
	overview, err := store.Overview(context.Background(), "tenant-1", "exam-empty")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.Empty == nil || !overview.Empty.Empty || overview.Empty.Reason != "no_published_grades" {
		t.Fatalf("expected empty overview, got %#v", overview)
	}
	student, err := store.StudentReport(context.Background(), "tenant-1", "exam-empty", "student-1")
	if err != nil {
		t.Fatalf("student report: %v", err)
	}
	if student.Empty == nil || student.Empty.Reason != "no_published_grade" {
		t.Fatalf("expected empty student report, got %#v", student)
	}
}

func TestReportExportRecordsWatermarkedCSV(t *testing.T) {
	store := seededReportStore()
	result, err := store.Export(context.Background(), "tenant-1", "exam-1", "teacher-1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.ReportID == "" || result.RowCount == 0 || result.Watermark == "" || store.ExportCount() != 1 {
		t.Fatalf("export mismatch: %#v", result)
	}
}

func seededReportStore() *MemoryStore {
	store := NewMemoryStore()
	store.AddSubmission(SubmissionSeed{ID: "sub-1", ExamID: "exam-1", StudentID: "student-1", ClassID: "class-a", ClassName: "Class A", AnonymousCode: "A001", TotalScore: 85, MaxScore: 100})
	store.AddSubmission(SubmissionSeed{ID: "sub-2", ExamID: "exam-1", StudentID: "student-2", ClassID: "class-a", ClassName: "Class A", AnonymousCode: "A002", TotalScore: 80, MaxScore: 100})
	store.AddSubmission(SubmissionSeed{ID: "sub-3", ExamID: "exam-1", StudentID: "student-3", ClassID: "class-b", ClassName: "Class B", AnonymousCode: "B001", TotalScore: 65, MaxScore: 100})
	aiScore := 40.0
	humanScore := 35.0
	store.AddGrade(GradeSeed{SubmissionID: "sub-1", StudentID: "student-1", ClassID: "class-a", ClassName: "Class A", QuestionID: "q1", QuestionNo: "Q1", QuestionType: "single_choice", AnswerSegmentID: "seg-1", Score: 50, MaxScore: 50, Source: "rule_auto", KnowledgePoints: []string{"motion"}, AnswerPayload: map[string]any{"selected_option": "A"}, AIScore: &aiScore, HumanScore: &humanScore, AIFeedbackText: "objective answer matched", TeacherFeedbackText: "good", ErrorClueText: "none"})
	store.AddGrade(GradeSeed{SubmissionID: "sub-1", StudentID: "student-1", ClassID: "class-a", ClassName: "Class A", QuestionID: "q2", QuestionNo: "Q2", QuestionType: "short_answer", AnswerSegmentID: "seg-2", Score: 35, MaxScore: 50, Source: "single_review", KnowledgePoints: []string{"force"}, AIScore: &aiScore, HumanScore: &humanScore, AIFeedbackText: "missing force diagram", TeacherFeedbackText: "explain force direction", ErrorClueText: "force direction"})
	store.AddGrade(GradeSeed{SubmissionID: "sub-2", StudentID: "student-2", ClassID: "class-a", ClassName: "Class A", QuestionID: "q1", QuestionNo: "Q1", QuestionType: "single_choice", AnswerSegmentID: "seg-3", Score: 40, MaxScore: 50, Source: "rule_auto", KnowledgePoints: []string{"motion"}, AnswerPayload: map[string]any{"selected_option": "B"}, AIScore: &aiScore})
	store.AddGrade(GradeSeed{SubmissionID: "sub-2", StudentID: "student-2", ClassID: "class-a", ClassName: "Class A", QuestionID: "q2", QuestionNo: "Q2", QuestionType: "short_answer", AnswerSegmentID: "seg-4", Score: 40, MaxScore: 50, Source: "single_review", KnowledgePoints: []string{"force"}})
	store.AddGrade(GradeSeed{SubmissionID: "sub-3", StudentID: "student-3", ClassID: "class-b", ClassName: "Class B", QuestionID: "q1", QuestionNo: "Q1", QuestionType: "single_choice", AnswerSegmentID: "seg-5", Score: 35, MaxScore: 50, Source: "rule_auto", KnowledgePoints: []string{"motion"}, AnswerPayload: map[string]any{"selected_option": "B"}, AIScore: &aiScore})
	store.AddGrade(GradeSeed{SubmissionID: "sub-3", StudentID: "student-3", ClassID: "class-b", ClassName: "Class B", QuestionID: "q2", QuestionNo: "Q2", QuestionType: "short_answer", AnswerSegmentID: "seg-6", Score: 30, MaxScore: 50, Source: "single_review", KnowledgePoints: []string{"force"}})
	store.SetQuality(QualitySeed{TotalSegments: 6, DoubleMarkDiffs: []float64{1.5, 2.5}, ArbitrationCount: 1, OCRTaskCount: 4, OCRFailedCount: 1, LowConfidenceReviewCount: 2})
	return store
}
