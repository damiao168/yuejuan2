package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

type fakeExamReader struct{ item exam.Exam }

func (f fakeExamReader) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	if f.item.ID != id || f.item.TenantID != scope.TenantID {
		return exam.Exam{}, exam.ErrNotFound
	}
	return f.item, nil
}

type fakeSessionExamReader struct {
	fakeExamReader
	siblings []exam.Exam
	err      error
}

func (f fakeSessionExamReader) ListSessionExams(_ context.Context, _ auth.AccessScope, _ string) ([]exam.Exam, error) {
	return f.siblings, f.err
}

func TestProjectionIncludesSessionSubjectsWithoutFullExamList(t *testing.T) {
	current := exam.Exam{ID: "math", TenantID: "tenant-1", SessionID: "session-1", SessionName: "期末考试", Name: "数学考试", Subject: "mathematics", TotalScore: 150, Status: "grading"}
	service := NewService(Dependencies{
		Exams:  fakeSessionExamReader{fakeExamReader: fakeExamReader{item: current}, siblings: []exam.Exam{current, {ID: "physics", Subject: "physics", TotalScore: 100}}},
		Papers: fakePaperReader{}, Submissions: fakeSubmissionReader{}, Reviews: fakeReviewReader{},
	})
	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExamSessionName != "期末考试" || len(result.SubjectExams) != 2 || result.SubjectExams[1].ExamID != "physics" {
		t.Fatalf("expected bounded session subjects, got %#v", result.SubjectExams)
	}
}

func TestProjectionKeepsCurrentSubjectWhenSessionLookupFails(t *testing.T) {
	current := exam.Exam{ID: "math", TenantID: "tenant-1", SessionID: "session-1", Subject: "mathematics", TotalScore: 150, Status: "grading"}
	service := NewService(Dependencies{
		Exams:  fakeSessionExamReader{fakeExamReader: fakeExamReader{item: current}, err: errors.New("session lookup unavailable")},
		Papers: fakePaperReader{}, Submissions: fakeSubmissionReader{}, Reviews: fakeReviewReader{},
	})
	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SubjectExams) != 1 || result.SubjectExams[0].ExamID != current.ID {
		t.Fatalf("current subject disappeared after session lookup failure: %#v", result.SubjectExams)
	}
	var found bool
	for _, warning := range result.Warnings {
		if warning.Code == "session_subjects_unavailable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing session lookup warning: %#v", result.Warnings)
	}
}

type fakePaperReader struct {
	papers    []paper.Paper
	questions []paper.Question
	readiness paper.ReadinessResult
	err       error
}

func (f fakePaperReader) ListPapers(context.Context, string, string) ([]paper.Paper, error) {
	return f.papers, f.err
}
func (f fakePaperReader) ListQuestions(context.Context, string, string) ([]paper.Question, error) {
	return f.questions, f.err
}
func (f fakePaperReader) Readiness(context.Context, string, string) (paper.ReadinessResult, error) {
	return f.readiness, f.err
}

type fakeSubmissionReader struct {
	items []submission.Submission
	err   error
}

type fakePaperImportReader struct {
	items []paper.PaperImportJob
	err   error
}

func (f fakePaperImportReader) ListPaperImports(context.Context, string, string) ([]paper.PaperImportJob, error) {
	return f.items, f.err
}

func (f fakeSubmissionReader) ListByExam(context.Context, string, string, submission.ListFilter) ([]submission.Submission, error) {
	return f.items, f.err
}

type fakeReviewReader struct {
	tasks        []review.ReviewTask
	arbitrations []review.ArbitrationTask
	err          error
}

type fakeAssessmentReader struct {
	summary assessment.ExamAssessmentSummary
	err     error
}

type fakeProcessingReader struct {
	summary processing.Summary
	items   processing.ListResult
	err     error
}

func (f fakeProcessingReader) Summary(context.Context, string, string) (processing.Summary, error) {
	return f.summary, f.err
}

func (f fakeProcessingReader) ListExceptions(context.Context, string, processing.ExceptionFilter) (processing.ListResult, error) {
	return f.items, f.err
}

func (f fakeAssessmentReader) GetExamAssessmentSummary(context.Context, string, string) (assessment.ExamAssessmentSummary, error) {
	return f.summary, f.err
}

func (f fakeReviewReader) ListTasks(context.Context, string, review.ListFilter) ([]review.ReviewTask, error) {
	return f.tasks, f.err
}
func (f fakeReviewReader) ListArbitrationTasks(context.Context, string, review.ArbitrationFilter) ([]review.ArbitrationTask, error) {
	return f.arbitrations, f.err
}

func TestProjectionKeepsFiveStagesAndActionableBlockers(t *testing.T) {
	now := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	service := NewService(Dependencies{
		Exams: fakeExamReader{item: exam.Exam{
			ID: "exam-1", TenantID: "tenant-1", Name: "九年级期末考试", Subject: "math", TotalScore: 120,
			Status: "configured", GradingMode: "blind_double_mark", Revision: 7,
		}},
		Papers: fakePaperReader{
			papers: []paper.Paper{{ID: "paper-1"}},
			questions: []paper.Question{
				{ID: "q1", QuestionType: "single_choice"}, {ID: "q2", QuestionType: "extended_response"},
			},
			readiness: paper.ReadinessResult{Checks: []paper.ReadinessCheck{
				{Code: "locked_template", Label: "答题卡模板", Message: "需要锁定模板", Section: "template", Severity: "blocker"},
			}},
		},
		Submissions: fakeSubmissionReader{items: []submission.Submission{
			{ID: "submission-1", Status: "processing_failed", QualityStatus: "failed"},
		}},
		Reviews: fakeReviewReader{arbitrations: []review.ArbitrationTask{{ID: "arb-1", Status: "pending"}}},
		Assessments: fakeAssessmentReader{summary: assessment.ExamAssessmentSummary{
			ExamID: "exam-1", SubjectCode: assessment.SubjectMathematics, RiskTier: assessment.RiskR3,
			ConfiguredQuestionCount: 2, FrozenQuestionCount: 2, Source: "snapshot",
		}},
		Now: func() time.Time { return now },
	})

	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
	if err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if len(result.Stages) != 5 || result.Stage != "prepare" {
		t.Fatalf("workspace must expose exactly five stages and current stage, got %#v", result.Stages)
	}
	if result.RiskTier != "R3" || result.SubjectSummary.Label != "数学" || result.SubjectSummary.QuestionTypes["extended_response"] != 1 || result.SubjectSummary.RiskTierSource != "snapshot" {
		t.Fatalf("subject and risk projection mismatch: %#v", result)
	}
	if result.Counts.FailedSubmissionCount != 1 || result.Counts.UnmatchedSubmissionCount != 1 || result.Counts.PendingArbitrationCount != 1 {
		t.Fatalf("operational counts mismatch: %#v", result.Counts)
	}
	if len(result.Blockers) < 3 {
		t.Fatalf("expected readiness, failed submission, identity and arbitration blockers: %#v", result.Blockers)
	}
	for _, blocker := range result.Blockers {
		if blocker.ActionRoute == "" {
			t.Fatalf("every blocker must be actionable: %#v", blocker)
		}
	}
	if result.Blockers[0].ActionRoute != "/exams/exam-1/template" {
		t.Fatalf("readiness blocker must stay inside the five-stage workspace: %#v", result.Blockers[0])
	}
	if len(result.NextActions) == 0 || result.UpdatedAt != now {
		t.Fatalf("projection must provide deterministic next actions and update time: %#v", result)
	}
	if len(result.StageProgress) != 5 || result.StageProgress[0].Total == nil || *result.StageProgress[0].Total != 1 || result.StageProgress[1].Total != nil {
		t.Fatalf("stage progress must use real denominators and omit unknown capture total: %#v", result.StageProgress)
	}
}

func TestProjectionKeepsExamAvailableWhenOptionalCountsFail(t *testing.T) {
	service := NewService(Dependencies{
		Exams:       fakeExamReader{item: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Subject: "history", Status: "grading"}},
		Papers:      fakePaperReader{err: errors.New("paper down")},
		Submissions: fakeSubmissionReader{err: errors.New("submission down")},
		Reviews:     fakeReviewReader{err: errors.New("review down")},
	})

	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
	if err != nil {
		t.Fatalf("partial dependency failures must not hide the exam workspace: %v", err)
	}
	if result.Stage != "grading" || len(result.Warnings) != 6 {
		t.Fatalf("expected grading stage with explicit partial-data warnings, got %#v", result)
	}
}

func TestProjectionLinksBlockingProcessingExceptionIntoUnifiedProcessingCentre(t *testing.T) {
	service := NewService(Dependencies{
		Exams:       fakeExamReader{item: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Status: "collecting"}},
		Papers:      fakePaperReader{},
		Submissions: fakeSubmissionReader{},
		Reviews:     fakeReviewReader{},
		Processing: fakeProcessingReader{
			summary: processing.Summary{ExamID: "exam-1", BlockedPages: 2},
			items: processing.ListResult{Exceptions: []processing.Exception{{
				ID: "exception-1", PageID: "12345678-page", Blocking: true, Status: processing.ExceptionOpen,
				Code: processing.IssueBadAlignment,
			}}},
		},
	})

	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	var found bool
	for _, blocker := range result.Blockers {
		if blocker.Code == "processing_exception_exception-1" {
			found = true
			if blocker.ActionRoute != "/exams/exam-1/processing?exception=exception-1" {
				t.Fatalf("processing blocker route=%q", blocker.ActionRoute)
			}
		}
	}
	if !found {
		t.Fatalf("workspace must surface a canonical processing blocker: %#v", result.Blockers)
	}
}

func TestReviewRequiredPaperImportIsTheFirstPreparationAction(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	service := NewService(Dependencies{
		Exams: fakeExamReader{item: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Status: "configured"}},
		Papers: fakePaperReader{readiness: paper.ReadinessResult{Ready: false, Checks: []paper.ReadinessCheck{
			{Code: "paper_file", Label: "试卷文件", Message: "缺少试卷", Section: "paper", Severity: "blocker"},
			{Code: "rubrics", Label: "主观题评分标准", Message: "缺少评分标准", Section: "questions", Severity: "blocker"},
		}}},
		PaperImports: fakePaperImportReader{items: []paper.PaperImportJob{{ID: "import-1", Status: "review_required", CreatedAt: now}}},
		Submissions:  fakeSubmissionReader{},
		Reviews:      fakeReviewReader{},
		Now:          func() time.Time { return now },
	})

	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blockers) == 0 || result.Blockers[0].Code != "paper_import_review_required" {
		t.Fatalf("paper import review was not prioritized over derivative readiness failures: %#v", result.Blockers)
	}
	if len(result.NextActions) == 0 || result.NextActions[0].Code != "paper_import_review_required" || result.NextActions[0].Label != "核对考试资料" || result.NextActions[0].Route != "/exams/exam-1/paper" {
		t.Fatalf("workspace did not expose the expected review action first: %#v", result.NextActions)
	}
}

func TestPaperImportWorkspaceNoticesUseLatestStatusAndCanonicalReadiness(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		readiness   paper.ReadinessResult
		imports     []paper.PaperImportJob
		warningCode string
		blockerCode string
	}{
		{
			name:        "processing is a warning",
			imports:     []paper.PaperImportJob{{ID: "processing", Status: "processing", CreatedAt: now}},
			warningCode: "paper_import_processing",
		},
		{
			name:        "review required is warning once canonical readiness passes",
			readiness:   paper.ReadinessResult{Ready: true},
			imports:     []paper.PaperImportJob{{ID: "review", Status: "review_required", CreatedAt: now}},
			warningCode: "paper_import_review_required",
		},
		{
			name:        "failed is a warning",
			imports:     []paper.PaperImportJob{{ID: "failed", Status: "failed", CreatedAt: now}},
			warningCode: "paper_import_failed",
		},
		{
			name:      "newer applied import suppresses historical review notice",
			readiness: paper.ReadinessResult{Ready: true},
			imports: []paper.PaperImportJob{
				{ID: "old-review", Status: "review_required", CreatedAt: now.Add(-time.Hour)},
				{ID: "latest-applied", Status: "applied", CreatedAt: now},
			},
		},
		{
			name:        "review required blocks while readiness is incomplete",
			imports:     []paper.PaperImportJob{{ID: "review", Status: "review_required", CreatedAt: now}},
			blockerCode: "paper_import_review_required",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(Dependencies{
				Exams:        fakeExamReader{item: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Status: "configured"}},
				Papers:       fakePaperReader{readiness: testCase.readiness},
				PaperImports: fakePaperImportReader{items: testCase.imports},
				Submissions:  fakeSubmissionReader{},
				Reviews:      fakeReviewReader{},
				Now:          func() time.Time { return now },
			})
			result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
			if err != nil {
				t.Fatal(err)
			}
			workspaceNoticeFound := false
			for _, notice := range append(append([]Notice{}, result.Warnings...), result.Blockers...) {
				if notice.Code == "paper_import_processing" || notice.Code == "paper_import_review_required" || notice.Code == "paper_import_failed" {
					workspaceNoticeFound = true
				}
			}
			if testCase.warningCode == "" && testCase.blockerCode == "" && workspaceNoticeFound {
				t.Fatalf("historical import unexpectedly produced a notice: warnings=%#v blockers=%#v", result.Warnings, result.Blockers)
			}
			if testCase.warningCode != "" && !noticeListHasCode(result.Warnings, testCase.warningCode) {
				t.Fatalf("missing warning %s: %#v", testCase.warningCode, result.Warnings)
			}
			if testCase.blockerCode != "" && !noticeListHasCode(result.Blockers, testCase.blockerCode) {
				t.Fatalf("missing blocker %s: %#v", testCase.blockerCode, result.Blockers)
			}
		})
	}
}

func noticeListHasCode(values []Notice, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}
