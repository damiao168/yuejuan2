package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

type Dependencies struct {
	Exams         exam.Store
	Submissions   submission.Store
	Reviews       review.Store
	Audits        auth.Store
	Activities    ActivityStore
	Organizations OrganizationSummaryStore
	Now           func() time.Time
}

type OrganizationSummaryStore interface {
	DashboardOrganizationSummary(ctx context.Context, tenantID string, scope auth.AccessScope) (OrganizationStatistics, error)
}

type Service struct {
	deps Dependencies
}

func NewService(deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}
}

// Summary 沿用请求上下文中的 AccessScope；可降级的数据源失败时保留可用统计并返回中文告警。
func (s *Service) Summary(ctx context.Context, user auth.User, accessScope auth.AccessScope) (Summary, error) {
	if s.deps.Exams == nil || s.deps.Submissions == nil || s.deps.Reviews == nil || s.deps.Audits == nil {
		return Summary{}, fmt.Errorf("dashboard dependencies are incomplete")
	}
	if auth.HasRole(user, "platform_admin") {
		return Summary{}, auth.ErrForbidden
	}

	schoolID := ""
	if len(accessScope.SchoolIDs) == 1 {
		schoolID = accessScope.SchoolIDs[0]
	}
	result := Summary{
		Scope:            Scope{TenantID: user.TenantID, SchoolID: schoolID},
		UpdatedAt:        s.deps.Now().UTC(),
		BlockingIssues:   []BlockingIssue{},
		ActiveExams:      []ActiveExam{},
		RecentActivities: []RecentActivity{},
		Warnings:         []string{},
	}

	s.applyOrganizationSummary(ctx, user.TenantID, accessScope, &result)

	exams, err := s.deps.Exams.ListExams(ctx, accessScope, exam.ListFilter{SchoolID: result.Scope.SchoolID})
	if err != nil {
		return Summary{}, fmt.Errorf("list dashboard exams: %w", err)
	}

	submissionByExam, submissionWarnings := s.submissionsByExam(ctx, user.TenantID, exams)
	result.Warnings = append(result.Warnings, submissionWarnings...)
	appendExamStatistics(exams, submissionByExam, &result)

	tasks, taskErr := s.deps.Reviews.ListTasks(ctx, user.TenantID, review.ListFilter{
		ScopeMode: accessScope.QueryMode(), ScopeActorID: accessScope.ActorID,
		ScopeSchoolIDs: append([]string(nil), accessScope.SchoolIDs...),
		ScopeExamIDs:   append([]string(nil), accessScope.ExamIDs...),
		ScopeTaskIDs:   append([]string(nil), accessScope.ReviewTaskIDs...),
	})
	if taskErr != nil {
		result.Warnings = append(result.Warnings, "阅卷待办暂时无法统计")
	} else {
		result.Statistics.PendingReviewQuestionCount, result.Statistics.PendingReviewSubmissionCount = pendingReviewCounts(tasks)
	}

	arbitrations, arbitrationErr := s.deps.Reviews.ListArbitrationTasks(ctx, user.TenantID, review.ArbitrationFilter{
		ScopeMode: accessScope.QueryMode(), ScopeActorID: accessScope.ActorID,
		ScopeSchoolIDs: append([]string(nil), accessScope.SchoolIDs...),
		ScopeExamIDs:   append([]string(nil), accessScope.ExamIDs...),
		ScopeTaskIDs:   append([]string(nil), accessScope.ArbitrationTaskIDs...),
	})
	if arbitrationErr != nil {
		result.Warnings = append(result.Warnings, "人工复核待办暂时无法统计")
	} else {
		result.Statistics.PendingArbitrationCount, result.Statistics.PendingArbitrationSubmissionCount = pendingArbitrationCounts(arbitrations)
	}

	var activityErr error
	result.RecentActivities, activityErr = s.recentActivities(ctx, user.TenantID, accessScope)
	if activityErr != nil {
		result.Warnings = append(result.Warnings, "关键进展暂时无法加载")
	}

	sort.Slice(result.ActiveExams, func(i, j int) bool {
		return result.ActiveExams[i].CreatedAt.After(result.ActiveExams[j].CreatedAt)
	})
	// 阻塞项只从统计结果推导，前端可据此直接跳转处理会卡住阅卷或成绩发布的问题。
	result.BlockingIssues = blockingIssues(result.Statistics)
	return result, nil
}

func (s *Service) applyOrganizationSummary(ctx context.Context, tenantID string, accessScope auth.AccessScope, result *Summary) {
	if s.deps.Organizations == nil {
		result.Warnings = append(result.Warnings, "成员基础数据暂时无法统计")
		return
	}
	organizationStatistics, organizationErr := s.deps.Organizations.DashboardOrganizationSummary(ctx, tenantID, accessScope)
	if organizationErr != nil {
		result.Warnings = append(result.Warnings, "成员基础数据暂时无法统计")
		return
	}
	result.OrganizationStatistics = organizationStatistics
}

func (s *Service) submissionsByExam(ctx context.Context, tenantID string, exams []exam.Exam) (map[string][]submission.Submission, []string) {
	submissionByExam := make(map[string][]submission.Submission, len(exams))
	var warnings []string
	if batchLister, ok := s.deps.Submissions.(submission.ExamBatchLister); ok {
		examIDs := make([]string, 0, len(exams))
		for _, item := range exams {
			examIDs = append(examIDs, item.ID)
		}
		// 批量接口存在时只使用该次读取；失败记一次告警，不回退到逐考试读取。
		items, listErr := batchLister.ListByExams(ctx, tenantID, examIDs)
		if listErr != nil {
			return submissionByExam, append(warnings, "部分答题卡状态暂时无法统计")
		}
		return items, warnings
	}
	for _, item := range exams {
		items, listErr := s.deps.Submissions.ListByExam(ctx, tenantID, item.ID, submission.ListFilter{})
		if listErr != nil {
			warnings = append(warnings, "部分答题卡状态暂时无法统计")
			continue
		}
		submissionByExam[item.ID] = items
	}
	return submissionByExam, warnings
}

func appendExamStatistics(exams []exam.Exam, submissionByExam map[string][]submission.Submission, result *Summary) {
	for _, item := range exams {
		if item.Status == "finalized" {
			result.Statistics.FinalizedExamCount++
		}
		if item.Status == "collecting" {
			result.Statistics.CollectingExamCount++
		}
		if item.Status == "published" || item.Status == "archived" {
			continue
		}
		result.Statistics.ActiveExamCount++
		result.ActiveExams = append(result.ActiveExams, activeExamStatistics(item, submissionByExam[item.ID], &result.Statistics))
	}
}

func activeExamStatistics(item exam.Exam, answerSheets []submission.Submission, stats *Statistics) ActiveExam {
	active := ActiveExam{
		ID:        item.ID,
		Name:      item.Name,
		Subject:   item.Subject,
		Status:    item.Status,
		CreatedAt: item.CreatedAt,
	}
	// 三类问题独立按答题卡计数，同一份答题卡可以同时出现在多个问题统计中。
	for _, answerSheet := range answerSheets {
		active.SubmissionCount++
		if submissionFailed(answerSheet) {
			active.FailedCount++
			stats.FailedSubmissionCount++
		}
		if answerSheet.StudentID == "" {
			active.UnmatchedCount++
			stats.UnmatchedSubmissionCount++
		}
		if len(answerSheet.QualityIssues) > 0 || answerSheet.QualityStatus == "failed" || answerSheet.QualityStatus == "review" {
			active.QualityIssueCount++
			stats.QualityIssueSubmissionCount++
		}
	}
	return active
}

func pendingReviewCounts(tasks []review.ReviewTask) (int, int) {
	questions := 0
	submissions := map[string]struct{}{}
	for _, task := range tasks {
		if task.Status == "submitted" || task.Status == "completed" {
			continue
		}
		questions++
		submissions[task.SubmissionID] = struct{}{}
	}
	return questions, len(submissions)
}

func pendingArbitrationCounts(tasks []review.ArbitrationTask) (int, int) {
	count := 0
	submissions := map[string]struct{}{}
	for _, task := range tasks {
		if task.Status == "completed" || task.Status == "cancelled" {
			continue
		}
		count++
		submissions[task.SubmissionID] = struct{}{}
	}
	return count, len(submissions)
}

func (s *Service) recentActivities(ctx context.Context, tenantID string, accessScope auth.AccessScope) ([]RecentActivity, error) {
	if s.deps.Activities != nil {
		return s.deps.Activities.ListRecent(ctx, tenantID, accessScope, 5)
	}
	activities := []RecentActivity{}
	// 旧审计读取没有学校或任务过滤；只有平台或全租户作用域可以使用该降级来源。
	if !accessScope.IsPlatform && !accessScope.TenantWide {
		return activities, nil
	}
	audits, err := s.deps.Audits.ListAudits(ctx, tenantID, auth.AuditFilter{Limit: 10})
	if err != nil {
		return activities, err
	}
	for _, record := range audits {
		if !dashboardActivity(record.Action) {
			continue
		}
		activities = append(activities, RecentActivity{
			ID:            record.ID,
			Action:        record.Action,
			TargetType:    record.TargetType,
			TargetID:      record.TargetID,
			Reason:        record.Reason,
			CreatedAt:     record.CreatedAt,
			DrilldownPath: activityPath(record),
		})
		if len(activities) == 5 {
			break
		}
	}
	return activities, nil
}

func submissionFailed(item submission.Submission) bool {
	return strings.Contains(item.Status, "failed") || item.QualityStatus == "failed" || item.Status == "rejected"
}

func blockingIssues(stats Statistics) []BlockingIssue {
	issues := make([]BlockingIssue, 0, 4)
	if stats.FailedSubmissionCount > 0 {
		issues = append(issues, BlockingIssue{Code: "failed_submissions", Label: "答题卡处理失败", Count: stats.FailedSubmissionCount, Unit: "份", Impact: "会阻断后续阅卷", Action: "查看并重试", DrilldownPath: "/capture?issue=failed"})
	}
	if stats.UnmatchedSubmissionCount > 0 {
		issues = append(issues, BlockingIssue{Code: "unmatched_submissions", Label: "答题卡未匹配学生", Count: stats.UnmatchedSubmissionCount, Unit: "份", Impact: "无法计入学生成绩", Action: "确认学生身份", DrilldownPath: "/capture?issue=unmatched"})
	}
	if stats.QualityIssueSubmissionCount > 0 {
		issues = append(issues, BlockingIssue{Code: "quality_issues", Label: "图像质量需要处理", Count: stats.QualityIssueSubmissionCount, Unit: "份", Impact: "可能影响文字识别和切题", Action: "查看质量问题", DrilldownPath: "/capture?issue=quality"})
	}
	if stats.PendingArbitrationSubmissionCount > 0 {
		issues = append(issues, BlockingIssue{Code: "pending_arbitration", Label: "待人工复核", Count: stats.PendingArbitrationSubmissionCount, Unit: "份", Impact: "未确认最终得分", Action: "开始复核", DrilldownPath: "/arbitration?status=pending"})
	}
	return issues
}

func dashboardActivity(action string) bool {
	return strings.HasPrefix(action, "exam.") ||
		strings.HasPrefix(action, "submission.") ||
		strings.HasPrefix(action, "capture.") ||
		strings.HasPrefix(action, "review.") ||
		strings.HasPrefix(action, "score.")
}

func activityPath(record auth.AuditRecord) string {
	switch {
	case strings.HasPrefix(record.Action, "review."):
		return "/grading"
	case strings.HasPrefix(record.Action, "score."):
		return "/scores"
	case strings.HasPrefix(record.Action, "submission."), strings.HasPrefix(record.Action, "capture."):
		return "/capture"
	case strings.HasPrefix(record.Action, "exam.") && record.TargetID != "":
		return "/exams/" + record.TargetID + "/overview"
	default:
		return ""
	}
}
