package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

const workbenchTenant = "tenant-workbench"

var workbenchNow = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

// reviewSourcesStub 只替换工作台读取的两个列表；任务时间固定，便于核对近期完成的时窗边界。
type reviewSourcesStub struct {
	review.Store
	tasks           []review.ReviewTask
	tasksErr        error
	arbitrations    []review.ArbitrationTask
	arbitrationsErr error
}

func (s reviewSourcesStub) ListTasks(context.Context, string, review.ListFilter) ([]review.ReviewTask, error) {
	return append([]review.ReviewTask(nil), s.tasks...), s.tasksErr
}

func (s reviewSourcesStub) ListArbitrationTasks(context.Context, string, review.ArbitrationFilter) ([]review.ArbitrationTask, error) {
	return append([]review.ArbitrationTask(nil), s.arbitrations...), s.arbitrationsErr
}

// workbenchSourcesStub 同时提供组织统计、名册人数和申诉统计，并记录调用，用来核对“一次读取”和权限门槛。
type workbenchSourcesStub struct {
	organizationSummaryStub
	roster        map[string]ExamRosterCount
	rosterErr     error
	rosterCalls   int
	rosterTenant  string
	rosterExamIDs []string
	appeals       AppealStatistics
	appealErr     error
	appealCalls   int
	appealScope   auth.AccessScope
}

func (s *workbenchSourcesStub) DashboardExamRosterCounts(_ context.Context, tenantID string, examIDs []string) (map[string]ExamRosterCount, error) {
	s.rosterCalls++
	s.rosterTenant, s.rosterExamIDs = tenantID, append([]string(nil), examIDs...)
	return s.roster, s.rosterErr
}

func (s *workbenchSourcesStub) DashboardAppealStatistics(_ context.Context, _ string, scope auth.AccessScope) (AppealStatistics, error) {
	s.appealCalls++
	s.appealScope = scope
	return s.appeals, s.appealErr
}

func workbenchScope() auth.AccessScope {
	return auth.AccessScope{TenantID: workbenchTenant, ActorID: "admin-1", TenantWide: true}
}

// seedWorkbenchExams 按顺序创建考试并改成指定状态，返回的 ID 与参数顺序一致。
func seedWorkbenchExams(t *testing.T, statuses ...string) (*exam.MemoryStore, []string) {
	t.Helper()
	store := exam.NewMemoryStore()
	ids := make([]string, 0, len(statuses))
	for index, status := range statuses {
		item, err := store.CreateExam(context.Background(), workbenchScope(), "admin-1", exam.CreateInput{
			SchoolID: "school-1", Name: fmt.Sprintf("考试 %d", index+1), Subject: "math",
			ExamType: "school_exam", TotalScore: 100, GradingMode: "human_review_required", PublishPolicy: "manual",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetStatusForTest(workbenchTenant, item.ID, status); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
	}
	return store, ids
}

func workbenchSummary(t *testing.T, exams exam.Store, reviews review.Store, organizations OrganizationSummaryStore, permissions ...string) Summary {
	t.Helper()
	service := NewService(Dependencies{
		Exams: exams, Submissions: submission.NewMemoryStore(), Reviews: reviews, Audits: auth.NewMemoryStore(),
		Activities: activityStoreStub{}, Organizations: organizations, Now: func() time.Time { return workbenchNow },
	})
	summary, err := service.Summary(context.Background(), auth.User{ID: "admin-1", TenantID: workbenchTenant, Roles: []string{"tenant_admin"}, Permissions: permissions}, workbenchScope())
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func workbenchExam(t *testing.T, summary Summary, id string) ActiveExam {
	t.Helper()
	for _, item := range summary.ActiveExams {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("active exam %s missing from %#v", id, summary.ActiveExams)
	return ActiveExam{}
}

func workbenchJSON(t *testing.T, summary Summary) map[string]any {
	t.Helper()
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func equalCount(value *int, want int) bool {
	return value != nil && *value == want
}

// countValue 让失败信息区分 nil 与 0，避免断言失败时解引用空指针。
func countValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func sameIDs(got, want []string) bool {
	got, want = slices.Clone(got), slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func TestWorkbenchReviewCountsSkipCancelledAndClosedExams(t *testing.T) {
	exams, ids := seedWorkbenchExams(t, "grading", "published", "archived", "reviewing")
	active, published, archived, second := ids[0], ids[1], ids[2], ids[3]
	task := func(examID, questionID, questionNo, submissionID, status string, age time.Duration) review.ReviewTask {
		return review.ReviewTask{ExamID: examID, QuestionID: questionID, QuestionNo: questionNo, SubmissionID: submissionID, Status: status, UpdatedAt: workbenchNow.Add(-age)}
	}
	reviews := reviewSourcesStub{tasks: []review.ReviewTask{
		// q1 四项待阅、四项已交：已交项依次在时窗内、正好在下界、刚出下界、晚于 now（数据库时钟略快）。
		task(active, "q1", "1", "s1", "pending", 0),
		task(active, "q1", "1", "s1", "assigned", 0),
		task(active, "q1", "1", "s2", "in_progress", 0),
		task(active, "q1", "1", "s3", "returned", 0),
		task(active, "q1", "1", "s4", "submitted", 10*time.Minute),
		task(active, "q1", "1", "s5", "completed", time.Hour),
		task(active, "q1", "1", "s6", "submitted", time.Hour+time.Second),
		task(active, "q1", "1", "s7", "completed", -time.Minute),
		task(active, "q1", "1", "s8", "cancelled", 0),
		task(active, "q2", "2", "s1", "pending", 0),
		// 只有已取消任务的小题不再需要阅卷，不出现在按题进度中。
		task(active, "q3", "3", "s1", "cancelled", 0),
		task(published, "q9", "1", "p1", "assigned", 0),
		task(archived, "q9", "1", "a1", "pending", 0),
		task(second, "q4", "1", "t1", "pending", 0),
		task(second, "q4", "1", "t2", "returned", 0),
		task(second, "q4", "1", "t3", "submitted", 30*time.Minute),
	}}
	reviews.arbitrations = []review.ArbitrationTask{
		{ExamID: active, SubmissionID: "s1", Status: "pending"},
		{ExamID: active, SubmissionID: "s1", Status: "assigned"},
		// submitted 表示已裁决，cancelled 无需处理，已发布考试不再是工作台待办。
		{ExamID: active, SubmissionID: "s2", Status: "submitted"},
		{ExamID: active, SubmissionID: "s3", Status: "cancelled"},
		{ExamID: published, SubmissionID: "p1", Status: "pending"},
		{ExamID: second, SubmissionID: "t1", Status: "pending"},
	}
	summary := workbenchSummary(t, exams, reviews, organizationSummaryStub{})

	stats := summary.Statistics
	if stats.PendingReviewQuestionCount != 7 || stats.PendingReviewSubmissionCount != 5 {
		t.Fatalf("pending review must skip cancelled tasks and closed exams: %#v", stats)
	}
	if stats.PendingArbitrationCount != 3 || stats.PendingArbitrationSubmissionCount != 2 {
		t.Fatalf("pending arbitration must skip decided, cancelled and closed-exam tasks: %#v", stats)
	}
	if len(summary.BlockingIssues) != 1 || summary.BlockingIssues[0].Code != "pending_arbitration" || summary.BlockingIssues[0].Count != 2 {
		t.Fatalf("blocking issues must use the tightened arbitration count: %#v", summary.BlockingIssues)
	}
	if item := workbenchExam(t, summary, active); !equalCount(item.ReviewTaskCount, 9) || !equalCount(item.ReviewDoneCount, 4) || !equalCount(item.PendingArbitrationCount, 2) {
		t.Fatalf("active exam done/tasks=%v/%v arbitration=%v", countValue(item.ReviewDoneCount), countValue(item.ReviewTaskCount), countValue(item.PendingArbitrationCount))
	}
	if item := workbenchExam(t, summary, second); !equalCount(item.ReviewTaskCount, 3) || !equalCount(item.ReviewDoneCount, 1) || !equalCount(item.PendingArbitrationCount, 1) {
		t.Fatalf("second exam done/tasks=%v/%v arbitration=%v", countValue(item.ReviewDoneCount), countValue(item.ReviewTaskCount), countValue(item.PendingArbitrationCount))
	}
	want := []GradingQuestionProgress{
		{ExamID: active, QuestionID: "q1", QuestionNo: "1", TaskCount: 8, DoneCount: 4, RemainingCount: 4, RecentDoneCount: 3},
		{ExamID: second, QuestionID: "q4", QuestionNo: "1", TaskCount: 3, DoneCount: 1, RemainingCount: 2, RecentDoneCount: 1},
		{ExamID: active, QuestionID: "q2", QuestionNo: "2", TaskCount: 1, DoneCount: 0, RemainingCount: 1, RecentDoneCount: 0},
	}
	if summary.GradingProgress == nil || summary.GradingProgress.WindowSeconds != 3600 || !reflect.DeepEqual(summary.GradingProgress.Questions, want) {
		t.Fatalf("grading progress=%#v", summary.GradingProgress)
	}
	if slices.Contains(summary.Warnings, "阅卷待办暂时无法统计") || slices.Contains(summary.Warnings, "人工复核待办暂时无法统计") {
		t.Fatalf("available task sources must not warn: %#v", summary.Warnings)
	}
}

func TestWorkbenchPhaseCountsUseActiveExamScope(t *testing.T) {
	// processing 不在考试状态枚举中，用来代表未知状态：仍计入进行中，但不进入任何阶段。
	exams, _ := seedWorkbenchExams(t, "draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized", "published", "archived", "processing")
	summary := workbenchSummary(t, exams, reviewSourcesStub{}, organizationSummaryStub{})
	stats := summary.Statistics
	if stats.ActiveExamCount != 8 || len(summary.ActiveExams) != 8 || stats.PreparingExamCount != 3 || stats.GradingExamCount != 2 || stats.CollectingExamCount != 1 || stats.FinalizedExamCount != 1 {
		t.Fatalf("phase counts must cover only unpublished, unarchived exams: %#v", stats)
	}
	// 任务来源可用但没有任务：各场写 0，按题进度是空数组而不是 null。
	for _, item := range summary.ActiveExams {
		if !equalCount(item.ReviewTaskCount, 0) || !equalCount(item.ReviewDoneCount, 0) || !equalCount(item.PendingArbitrationCount, 0) {
			t.Fatalf("known empty task counts must be 0: %#v", item)
		}
	}
	document := workbenchJSON(t, summary)
	progress, ok := document["grading_progress"].(map[string]any)
	if questions, isList := progress["questions"].([]any); !ok || !isList || len(questions) != 0 || progress["window_seconds"] != float64(3600) {
		t.Fatalf("empty grading progress=%#v", document["grading_progress"])
	}
	statistics := document["statistics"].(map[string]any)
	if statistics["preparing_exam_count"] != float64(3) || statistics["grading_exam_count"] != float64(2) {
		t.Fatalf("phase counts must use the contract field names: %#v", statistics)
	}
}

func TestWorkbenchRosterCountsReadOnceAndKeepUnknownNull(t *testing.T) {
	exams, ids := seedWorkbenchExams(t, "collecting", "published", "grading")
	collecting, published, grading := ids[0], ids[1], ids[2]
	sources := &workbenchSourcesStub{roster: map[string]ExamRosterCount{
		collecting: {RosterCount: 40, SubmittedCount: 38},
		published:  {RosterCount: 50, SubmittedCount: 50},
	}}
	summary := workbenchSummary(t, exams, reviewSourcesStub{}, sources)
	if sources.rosterCalls != 1 || sources.rosterTenant != workbenchTenant || !sameIDs(sources.rosterExamIDs, []string{collecting, grading}) {
		t.Fatalf("roster counts must be read once for active exams only: calls=%d tenant=%s ids=%v", sources.rosterCalls, sources.rosterTenant, sources.rosterExamIDs)
	}
	if item := workbenchExam(t, summary, collecting); !equalCount(item.RosterCount, 40) || !equalCount(item.RosterSubmittedCount, 38) {
		t.Fatalf("collecting exam roster=%#v", item)
	}
	// 来源可用但本场还没有冻结名册：人数为 0，不是 null。
	if item := workbenchExam(t, summary, grading); !equalCount(item.RosterCount, 0) || !equalCount(item.RosterSubmittedCount, 0) {
		t.Fatalf("exam without frozen roster=%#v", item)
	}
	if slices.Contains(summary.Warnings, "考试名册人数暂时无法统计") {
		t.Fatalf("available roster source must not warn: %#v", summary.Warnings)
	}

	for _, tc := range []struct {
		name          string
		organizations OrganizationSummaryStore
	}{
		{"store missing", organizationSummaryStub{}},
		{"store error", &workbenchSourcesStub{rosterErr: errors.New("roster unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := workbenchSummary(t, exams, reviewSourcesStub{}, tc.organizations)
			if !slices.Contains(summary.Warnings, "考试名册人数暂时无法统计") {
				t.Fatalf("unknown roster must warn: %#v", summary.Warnings)
			}
			for _, row := range workbenchJSON(t, summary)["active_exams"].([]any) {
				fields := row.(map[string]any)
				rosterCount, hasRoster := fields["roster_count"]
				submittedCount, hasSubmitted := fields["roster_submitted_count"]
				if !hasRoster || !hasSubmitted || rosterCount != nil || submittedCount != nil {
					t.Fatalf("unknown roster must serialize as null: %#v", fields)
				}
			}
		})
	}

	t.Run("no active exams", func(t *testing.T) {
		closed, _ := seedWorkbenchExams(t, "published", "archived")
		sources := &workbenchSourcesStub{}
		summary := workbenchSummary(t, closed, reviewSourcesStub{}, sources)
		if sources.rosterCalls != 0 || slices.Contains(summary.Warnings, "考试名册人数暂时无法统计") {
			t.Fatalf("nothing to count must not query or warn: calls=%d warnings=%#v", sources.rosterCalls, summary.Warnings)
		}
	})
}

func TestWorkbenchTaskSourceFailureKeepsProgressUnknown(t *testing.T) {
	exams, ids := seedWorkbenchExams(t, "grading")
	failing := reviewSourcesStub{tasksErr: errors.New("tasks unavailable"), arbitrations: []review.ArbitrationTask{{ExamID: ids[0], SubmissionID: "s1", Status: "pending"}}}
	summary := workbenchSummary(t, exams, failing, organizationSummaryStub{})
	item := workbenchExam(t, summary, ids[0])
	if summary.GradingProgress != nil || item.ReviewTaskCount != nil || item.ReviewDoneCount != nil || !slices.Contains(summary.Warnings, "阅卷待办暂时无法统计") {
		t.Fatalf("task failure must leave grading progress unknown: progress=%#v exam=%#v warnings=%#v", summary.GradingProgress, item, summary.Warnings)
	}
	if !equalCount(item.PendingArbitrationCount, 1) {
		t.Fatalf("arbitration source is independent of the task source: %#v", item)
	}
	document := workbenchJSON(t, summary)
	if value, present := document["grading_progress"]; !present || value != nil {
		t.Fatalf("failed task source must serialize grading_progress as null: %#v", document["grading_progress"])
	}
	row := document["active_exams"].([]any)[0].(map[string]any)
	if _, present := row["review_task_count"]; present {
		t.Fatalf("unknown review task count must be omitted, not 0: %#v", row)
	}

	failing = reviewSourcesStub{arbitrationsErr: errors.New("arbitrations unavailable")}
	summary = workbenchSummary(t, exams, failing, organizationSummaryStub{})
	if item := workbenchExam(t, summary, ids[0]); item.PendingArbitrationCount != nil || !equalCount(item.ReviewTaskCount, 0) || !slices.Contains(summary.Warnings, "人工复核待办暂时无法统计") {
		t.Fatalf("arbitration failure must leave only its own count unknown: exam=%#v warnings=%#v", item, summary.Warnings)
	}
}
