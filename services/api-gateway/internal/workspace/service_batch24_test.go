package workspace

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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

var batch24SourceUnavailable = errors.New("batch24 source unavailable")

type batch24WorkspaceReader struct {
	t             *testing.T
	ctx           context.Context
	scope         auth.AccessScope
	item          exam.Exam
	calls         []string
	examErr       error
	failSources   bool
	failReadiness bool
}

func newBatch24WorkspaceReader(t *testing.T) *batch24WorkspaceReader {
	t.Helper()
	return &batch24WorkspaceReader{
		t: t, ctx: context.Background(), scope: auth.AccessScope{TenantID: "request-tenant", TenantWide: true},
		item: exam.Exam{ID: "resolved-exam", TenantID: "resolved-tenant", SessionID: "session-1", Subject: "math", Status: "configured"},
	}
}

func (f *batch24WorkspaceReader) dependencies() Dependencies {
	return Dependencies{
		Exams: f, Papers: f, PaperImports: f, Submissions: f, Reviews: f, Assessments: f, Processing: f,
		Now: func() time.Time {
			f.calls = append(f.calls, "now")
			return time.Date(2026, 10, 6, 16, 0, 0, 0, time.FixedZone("fixture", 8*60*60))
		},
	}
}

func (f *batch24WorkspaceReader) recordAccess(ctx context.Context, scope auth.AccessScope, name string) {
	f.t.Helper()
	if ctx != f.ctx || !reflect.DeepEqual(scope, f.scope) {
		f.t.Errorf("%s changed request context or access scope", name)
	}
	f.calls = append(f.calls, name)
}

func (f *batch24WorkspaceReader) recordData(ctx context.Context, tenantID, examID, name string) {
	f.t.Helper()
	if ctx != f.ctx || tenantID != f.item.TenantID || examID != f.item.ID {
		f.t.Errorf("%s changed resolved scope: tenant=%q exam=%q", name, tenantID, examID)
	}
	f.calls = append(f.calls, name)
}

func (f *batch24WorkspaceReader) sourceError() error {
	if f.failSources {
		return batch24SourceUnavailable
	}
	return nil
}

func (f *batch24WorkspaceReader) GetExam(ctx context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	f.recordAccess(ctx, scope, "exam")
	if id != "request-exam" {
		f.t.Errorf("GetExam id=%q", id)
	}
	return f.item, f.examErr
}

func (f *batch24WorkspaceReader) GetManagementExam(ctx context.Context, scope auth.AccessScope, kind, targetID string) (exam.ManagementExam, error) {
	f.recordAccess(ctx, scope, "archive")
	if kind != "session" || targetID != f.item.SessionID {
		f.t.Errorf("archive target=%s/%s", kind, targetID)
	}
	return exam.ManagementExam{}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListSessionExams(ctx context.Context, scope auth.AccessScope, sessionID string) ([]exam.Exam, error) {
	f.recordAccess(ctx, scope, "session")
	if sessionID != f.item.SessionID {
		f.t.Errorf("session id=%q", sessionID)
	}
	return []exam.Exam{f.item, {ID: "sibling", Subject: "physics", TotalScore: 100}}, f.sourceError()
}

func (f *batch24WorkspaceReader) GetExamAssessmentSummary(ctx context.Context, tenantID, examID string) (assessment.ExamAssessmentSummary, error) {
	f.recordData(ctx, tenantID, examID, "assessment")
	if f.failSources {
		return assessment.ExamAssessmentSummary{}, fmt.Errorf("wrapped: %w", assessment.ErrNotFound)
	}
	return assessment.ExamAssessmentSummary{SubjectCode: assessment.SubjectMathematics, RiskTier: assessment.RiskR3, Source: "snapshot"}, nil
}

func (f *batch24WorkspaceReader) ListPapers(ctx context.Context, tenantID, examID string) ([]paper.Paper, error) {
	f.recordData(ctx, tenantID, examID, "papers")
	return []paper.Paper{{ID: "paper-1"}}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListQuestions(ctx context.Context, tenantID, examID string) ([]paper.Question, error) {
	f.recordData(ctx, tenantID, examID, "questions")
	return []paper.Question{{ID: "question-1", QuestionType: "single_choice"}}, f.sourceError()
}

func (f *batch24WorkspaceReader) Readiness(ctx context.Context, tenantID, examID string) (paper.ReadinessResult, error) {
	f.recordData(ctx, tenantID, examID, "readiness")
	if f.failReadiness || f.failSources {
		return paper.ReadinessResult{}, batch24SourceUnavailable
	}
	return paper.ReadinessResult{Ready: true, Checks: []paper.ReadinessCheck{{Passed: true}}}, nil
}

func (f *batch24WorkspaceReader) ListPaperImports(ctx context.Context, tenantID, examID string) ([]paper.PaperImportJob, error) {
	f.recordData(ctx, tenantID, examID, "imports")
	return []paper.PaperImportJob{{ID: "import-1", Status: "review_required"}}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListByExam(ctx context.Context, tenantID, examID string, _ submission.ListFilter) ([]submission.Submission, error) {
	f.recordData(ctx, tenantID, examID, "submissions")
	return []submission.Submission{
		{ID: "submission-1", StudentID: "student-1", Status: "completed", QualityStatus: "passed"},
		{ID: "submission-2", Status: "processing_failed", QualityStatus: "review"},
	}, f.sourceError()
}

func (f *batch24WorkspaceReader) Summary(ctx context.Context, tenantID, examID string) (processing.Summary, error) {
	f.recordData(ctx, tenantID, examID, "processing_summary")
	return processing.Summary{BlockedPages: 1}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListExceptions(ctx context.Context, tenantID string, filter processing.ExceptionFilter) (processing.ListResult, error) {
	f.recordData(ctx, tenantID, filter.ExamID, "processing_exceptions")
	if filter.Status != processing.ExceptionOpen || filter.Limit != 25 {
		f.t.Errorf("processing filter=%#v", filter)
	}
	return processing.ListResult{Exceptions: []processing.Exception{{ID: "exception-1", PageID: "page-1", Blocking: true, Status: processing.ExceptionOpen}}}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListTasks(ctx context.Context, tenantID string, filter review.ListFilter) ([]review.ReviewTask, error) {
	f.recordData(ctx, tenantID, filter.ExamID, "review")
	return []review.ReviewTask{{Status: "pending"}, {Status: "submitted"}, {Status: "completed"}}, f.sourceError()
}

func (f *batch24WorkspaceReader) ListArbitrationTasks(ctx context.Context, tenantID string, filter review.ArbitrationFilter) ([]review.ArbitrationTask, error) {
	f.recordData(ctx, tenantID, filter.ExamID, "arbitration")
	return []review.ArbitrationTask{{Status: "pending"}, {Status: "completed"}, {Status: "cancelled"}}, f.sourceError()
}

func TestBatch24WorkspaceFirstErrorStopsAllLaterReaders(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		remove func(*Dependencies)
	}{
		{"exams missing", func(deps *Dependencies) { deps.Exams = nil }},
		{"papers missing", func(deps *Dependencies) { deps.Papers = nil }},
		{"submissions missing", func(deps *Dependencies) { deps.Submissions = nil }},
		{"reviews missing", func(deps *Dependencies) { deps.Reviews = nil }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader := newBatch24WorkspaceReader(t)
			reader.examErr = exam.ErrNotFound
			deps := reader.dependencies()
			scenario.remove(&deps)
			result, err := NewService(deps).Get(reader.ctx, reader.scope, "request-exam")
			if err == nil || err.Error() != "workspace dependencies are incomplete" || len(reader.calls) != 0 || !reflect.DeepEqual(result, Projection{}) {
				t.Fatalf("dependency gate: result=%#v err=%v calls=%v", result, err, reader.calls)
			}
		})
	}
	reader := newBatch24WorkspaceReader(t)
	reader.examErr = exam.ErrScopeForbidden
	result, err := NewService(reader.dependencies()).Get(reader.ctx, reader.scope, "request-exam")
	if err != exam.ErrScopeForbidden || !reflect.DeepEqual(reader.calls, []string{"exam"}) || !reflect.DeepEqual(result, Projection{}) {
		t.Fatalf("exam gate: result=%#v err=%v calls=%v", result, err, reader.calls)
	}
}

func TestBatch24WorkspacePreservesReaderOrderAndPartialResults(t *testing.T) {
	// 多个来源同时失败时仍串行继续，并按读取顺序追加警告；归档未知只限制动作。
	for _, scenario := range []workspacePartialSourcesCase{
		{"available", false, false, []string{"paper_import_review_required", "quality_issues"}},
		{"all later sources fail", true, false, []string{"archive_state_unavailable", "session_subjects_unavailable", "risk_tier_unavailable", "papers_unavailable", "questions_unavailable", "readiness_unavailable", "paper_imports_unavailable", "submissions_unavailable", "processing_unavailable", "review_tasks_unavailable", "arbitration_tasks_unavailable"}},
		{"readiness fails but import remains actionable", false, true, []string{"readiness_unavailable", "quality_issues"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assertWorkspaceReaderOrderAndPartialFacts(t, scenario)
		})
	}
}

func checkBatch24WorkspaceCounts(t *testing.T, result Projection, unavailable bool) {
	t.Helper()
	if unavailable {
		if result.Counts != (Counts{}) || result.RiskTier != "unknown" || len(result.SubjectExams) != 1 || !result.ReadOnly || result.NextActions == nil || len(result.NextActions) != 0 {
			t.Fatalf("failed sources became reliable facts or actions: %#v", result)
		}
		if result.Warnings[2].Message != "题目评估配置尚未完成，系统不会按考试阅卷模式推断" {
			t.Fatalf("wrapped not-found risk message=%q", result.Warnings[2].Message)
		}
		return
	}
	want := Counts{PaperCount: 1, QuestionCount: 1, SubmissionCount: 2, FailedSubmissionCount: 1, UnmatchedSubmissionCount: 1, QualityIssueSubmissionCount: 1, PendingReviewCount: 1, PendingArbitrationCount: 1}
	if result.Counts != want || result.RiskTier != "R3" || len(result.SubjectExams) != 2 || result.ReadOnly {
		t.Fatalf("available facts=%#v", result)
	}
}

func TestBatch24WorkspacePreparationReadsOnlyPreparationStatuses(t *testing.T) {
	for _, scenario := range []struct {
		status string
		reads  bool
	}{
		{"draft", true}, {"configured", true}, {"ready", true},
		{"collecting", false}, {"grading", false}, {"reviewing", false},
		{"finalized", false}, {"published", false}, {"archived", false}, {"unknown", false},
	} {
		t.Run(scenario.status, func(t *testing.T) {
			reader := newBatch24WorkspaceReader(t)
			reader.item.Status = scenario.status
			if _, err := NewService(reader.dependencies()).Get(reader.ctx, reader.scope, "request-exam"); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(reader.calls, "readiness") != scenario.reads || slices.Contains(reader.calls, "imports") != scenario.reads {
				t.Fatalf("preparation calls for %s=%v", scenario.status, reader.calls)
			}
		})
	}
}

func TestBatch24WorkspaceSuccessfulEmptySessionDoesNotRestoreCurrentSubject(t *testing.T) {
	item := exam.Exam{ID: "exam-1", TenantID: "tenant-1", SessionID: "session-1", Status: "grading"}
	service := NewService(Dependencies{
		Exams:  fakeSessionExamReader{fakeExamReader: fakeExamReader{item: item}},
		Papers: fakePaperReader{}, Submissions: fakeSubmissionReader{}, Reviews: fakeReviewReader{},
	})
	result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, item.ID)
	if err != nil || result.SubjectExams == nil || len(result.SubjectExams) != 0 {
		t.Fatalf("successful empty session=%#v err=%v", result.SubjectExams, err)
	}
}

func TestBatch24StageProgressDistinguishesUnknownAndZeroDenominators(t *testing.T) {
	zero, one, two, three := 0, 1, 2, 3
	for _, scenario := range []workspaceStageProgressCase{
		{"unknown", "configured", nil, false, -1, -1, Counts{}, []*int{nil, nil, nil, nil, nil}, []*int{nil, nil, nil, nil, nil}, "暂无可靠的开考检查分母", "成绩尚未发布；发布检查没有可量化的统一分母"},
		{"mixed sources", "grading", nil, false, 0, -1, Counts{}, []*int{nil, nil, &zero, nil, nil}, []*int{nil, nil, &zero, nil, nil}, "暂无可靠的开考检查分母", "成绩尚未发布；发布检查没有可量化的统一分母"},
		{"reliable empty", "published", &paper.ReadinessResult{}, true, 0, 0, Counts{}, []*int{&zero, &zero, &zero, &zero, nil}, []*int{&zero, nil, &zero, &zero, nil}, "已通过 0 / 0 项开考检查", "成绩已发布"},
		{"measured", "finalized", &paper.ReadinessResult{Checks: []paper.ReadinessCheck{{Passed: true}, {Passed: false}}}, true, 3, 2, Counts{SubmissionCount: 2, PendingReviewCount: 1, PendingArbitrationCount: 1}, []*int{&one, &two, &two, &one, nil}, []*int{&two, nil, &three, &two, nil}, "已通过 1 / 2 项开考检查", "成绩已确认，等待发布"},
		{"archived", "archived", nil, false, -1, -1, Counts{}, []*int{nil, nil, nil, nil, nil}, []*int{nil, nil, nil, nil, nil}, "暂无可靠的开考检查分母", "考试已归档"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assertWorkspaceStageProgressDenominators(t, scenario)
		})
	}
}

func batch24NoticeCodes(notices []Notice) []string {
	codes := []string{}
	for _, notice := range notices {
		codes = append(codes, notice.Code)
	}
	return codes
}

type workspacePartialSourcesCase struct {
	name          string
	failSources   bool
	failReadiness bool
	wantWarnings  []string
}

func assertWorkspaceReaderOrderAndPartialFacts(t *testing.T, scenario workspacePartialSourcesCase) {
	t.Helper()
	reader := newBatch24WorkspaceReader(t)
	reader.failSources, reader.failReadiness = scenario.failSources, scenario.failReadiness
	result, err := NewService(reader.dependencies()).Get(reader.ctx, reader.scope, "request-exam")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"exam", "now", "archive", "session", "assessment", "papers", "questions", "readiness", "imports", "submissions", "processing_summary"}
	if !scenario.failSources {
		wantCalls = append(wantCalls, "processing_exceptions")
	}
	wantCalls = append(wantCalls, "review", "arbitration")
	if !reflect.DeepEqual(reader.calls, wantCalls) || !reflect.DeepEqual(batch24NoticeCodes(result.Warnings), scenario.wantWarnings) {
		t.Fatalf("calls=%v warnings=%#v", reader.calls, result.Warnings)
	}
	if result.ExamID != reader.item.ID || result.ExamStatus != "configured" || result.Stage != "prepare" || result.UpdatedAt != time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC) {
		t.Fatalf("resolved exam projection=%#v", result)
	}
	checkBatch24WorkspaceCounts(t, result, scenario.failSources)
	if scenario.failReadiness && (len(result.Blockers) == 0 || result.Blockers[0].Code != "paper_import_review_required" || len(result.NextActions) == 0 || result.NextActions[0].Code != "paper_import_review_required") {
		t.Fatalf("unknown readiness hid import action: %#v", result)
	}
}

type workspaceStageProgressCase struct {
	name          string
	status        string
	readiness     *paper.ReadinessResult
	reliable      bool
	tasks         int
	arbitrations  int
	counts        Counts
	wantCompleted []*int
	wantTotals    []*int
	wantPrepare   string
	wantResults   string
}

func assertWorkspaceStageProgressDenominators(t *testing.T, scenario workspaceStageProgressCase) {
	t.Helper()
	result := Projection{ExamStatus: scenario.status, Counts: scenario.counts, Stages: []Stage{{State: "prepare-state"}, {State: "capture-state"}, {State: "grading-state"}, {State: "quality-state"}, {State: "results-state"}}}
	progress := buildStageProgress(result, scenario.readiness, scenario.reliable, scenario.tasks, scenario.arbitrations)
	if len(progress) != 5 {
		t.Fatalf("progress=%#v", progress)
	}
	for i, stage := range []string{"prepare", "capture", "grading", "quality", "results"} {
		if progress[i].Stage != stage || progress[i].Status != stage+"-state" || !reflect.DeepEqual(progress[i].Completed, scenario.wantCompleted[i]) || !reflect.DeepEqual(progress[i].Total, scenario.wantTotals[i]) {
			t.Fatalf("stage %s=%#v", stage, progress[i])
		}
	}
	if progress[0].Summary != scenario.wantPrepare || progress[4].Summary != scenario.wantResults {
		t.Fatalf("summaries=%#v", progress)
	}
}
