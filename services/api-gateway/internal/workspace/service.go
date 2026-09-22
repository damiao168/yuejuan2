package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

type ExamReader interface {
	GetExam(context.Context, auth.AccessScope, string) (exam.Exam, error)
}

type sessionExamReader interface {
	ListSessionExams(context.Context, auth.AccessScope, string) ([]exam.Exam, error)
}

type PaperReader interface {
	ListPapers(context.Context, string, string) ([]paper.Paper, error)
	ListQuestions(context.Context, string, string) ([]paper.Question, error)
	Readiness(context.Context, string, string) (paper.ReadinessResult, error)
}

type PaperImportReader interface {
	ListPaperImports(context.Context, string, string) ([]paper.PaperImportJob, error)
}

type SubmissionReader interface {
	ListByExam(context.Context, string, string, submission.ListFilter) ([]submission.Submission, error)
}

type ReviewReader interface {
	ListTasks(context.Context, string, review.ListFilter) ([]review.ReviewTask, error)
	ListArbitrationTasks(context.Context, string, review.ArbitrationFilter) ([]review.ArbitrationTask, error)
}

type AssessmentSummaryReader interface {
	GetExamAssessmentSummary(context.Context, string, string) (assessment.ExamAssessmentSummary, error)
}

// ProcessingReader gives the five-stage exam workspace a bounded view of the
// canonical page-processing projection.  The processing service remains the
// source of its own facts; the workspace only turns blocking exceptions into
// actionable links for the exam owner.
type ProcessingReader interface {
	Summary(context.Context, string, string) (processing.Summary, error)
	ListExceptions(context.Context, string, processing.ExceptionFilter) (processing.ListResult, error)
}

type Dependencies struct {
	Exams        ExamReader
	Papers       PaperReader
	PaperImports PaperImportReader
	Submissions  SubmissionReader
	Reviews      ReviewReader
	Assessments  AssessmentSummaryReader
	Processing   ProcessingReader
	Now          func() time.Time
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

func (s *Service) Get(ctx context.Context, scope auth.AccessScope, examID string) (Projection, error) {
	if s.deps.Exams == nil || s.deps.Papers == nil || s.deps.Submissions == nil || s.deps.Reviews == nil {
		return Projection{}, fmt.Errorf("workspace dependencies are incomplete")
	}
	item, err := s.deps.Exams.GetExam(ctx, scope, examID)
	if err != nil {
		return Projection{}, err
	}

	result := Projection{
		ExamID:          item.ID,
		ExamName:        item.Name,
		ExamSessionName: item.SessionName,
		SubjectExams:    []SubjectExam{{ExamID: item.ID, Subject: item.Subject, TotalScore: item.TotalScore}},
		ExamStatus:      item.Status,
		Revision:        item.Revision,
		Stage:           stageForStatus(item.Status),
		Stages:          buildStages(item.ID, item.Status),
		Blockers:        []Notice{},
		Warnings:        []Notice{},
		NextActions:     []NextAction{},
		RiskTier:        "unknown",
		SubjectSummary: SubjectSummary{
			Code:          item.Subject,
			Label:         subjectLabel(item.Subject),
			TotalScore:    item.TotalScore,
			QuestionTypes: map[string]int{},
		},
		UpdatedAt: s.deps.Now().UTC(),
	}
	if item.SessionID != "" {
		if reader, ok := s.deps.Exams.(sessionExamReader); ok {
			siblings, listErr := reader.ListSessionExams(ctx, scope, item.SessionID)
			if listErr != nil {
				result.Warnings = append(result.Warnings, unavailableNotice("session_subjects_unavailable", "同场次学科暂不可用"))
			} else {
				result.SubjectExams = []SubjectExam{}
				for _, sibling := range siblings {
					result.SubjectExams = append(result.SubjectExams, SubjectExam{ExamID: sibling.ID, Subject: sibling.Subject, TotalScore: sibling.TotalScore})
				}
			}
		}
	}
	if s.deps.Assessments == nil {
		result.Warnings = append(result.Warnings, riskUnavailableNotice("风险汇总服务尚未接入"))
	} else if summary, summaryErr := s.deps.Assessments.GetExamAssessmentSummary(ctx, item.TenantID, item.ID); summaryErr != nil {
		message := "暂时无法读取每道题的评估配置"
		if errors.Is(summaryErr, assessment.ErrNotFound) {
			message = "题目评估配置尚未完成"
		}
		result.Warnings = append(result.Warnings, riskUnavailableNotice(message))
	} else {
		result.RiskTier = string(summary.RiskTier)
		result.SubjectSummary.Code = string(summary.SubjectCode)
		result.SubjectSummary.Label = subjectLabel(string(summary.SubjectCode))
		result.SubjectSummary.ConfiguredQuestionCount = summary.ConfiguredQuestionCount
		result.SubjectSummary.FrozenQuestionCount = summary.FrozenQuestionCount
		result.SubjectSummary.RiskTierSource = summary.Source
	}
	var readiness *paper.ReadinessResult

	papers, papersErr := s.deps.Papers.ListPapers(ctx, item.TenantID, item.ID)
	if papersErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("papers_unavailable", "试卷数据暂不可用"))
	} else {
		result.Counts.PaperCount = len(papers)
	}
	questions, questionsErr := s.deps.Papers.ListQuestions(ctx, item.TenantID, item.ID)
	if questionsErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("questions_unavailable", "题目数据暂不可用"))
	} else {
		result.Counts.QuestionCount = len(questions)
		result.SubjectSummary.QuestionCount = len(questions)
		for _, question := range questions {
			result.SubjectSummary.QuestionTypes[question.QuestionType]++
		}
	}

	if isPreparationStatus(item.Status) {
		if readinessResult, readinessErr := s.deps.Papers.Readiness(ctx, item.TenantID, item.ID); readinessErr != nil {
			result.Warnings = append(result.Warnings, unavailableNotice("readiness_unavailable", "开考准备状态暂不可用"))
		} else {
			readiness = &readinessResult
			for _, check := range readinessResult.Checks {
				if check.Passed {
					continue
				}
				result.Blockers = append(result.Blockers, Notice{
					Code: check.Code, Title: check.Label, Message: check.Message, Severity: "blocker",
					ActionLabel: "前往处理", ActionRoute: examRoute(item.ID, check.Section),
				})
			}
		}
		appendPaperImportNotices(ctx, s.deps.PaperImports, item.TenantID, item.ID, readiness, &result)
	}

	submissions, submissionsErr := s.deps.Submissions.ListByExam(ctx, item.TenantID, item.ID, submission.ListFilter{})
	submissionsReliable := submissionsErr == nil
	if submissionsErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("submissions_unavailable", "答卷处理数据暂不可用"))
	} else {
		for _, answerSheet := range submissions {
			result.Counts.SubmissionCount++
			if submissionFailed(answerSheet) {
				result.Counts.FailedSubmissionCount++
			}
			if answerSheet.StudentID == "" {
				result.Counts.UnmatchedSubmissionCount++
			}
			if answerSheet.QualityStatus == "failed" || answerSheet.QualityStatus == "review" || len(answerSheet.QualityIssues) > 0 {
				result.Counts.QualityIssueSubmissionCount++
			}
		}
		appendSubmissionNotices(&result)
	}
	appendProcessingNotices(ctx, s.deps.Processing, item.TenantID, item.ID, &result)

	taskTotal := -1
	tasks, tasksErr := s.deps.Reviews.ListTasks(ctx, item.TenantID, review.ListFilter{ExamID: item.ID})
	if tasksErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("review_tasks_unavailable", "阅卷任务统计暂不可用"))
	} else {
		taskTotal = len(tasks)
		for _, task := range tasks {
			if task.Status != "submitted" && task.Status != "completed" {
				result.Counts.PendingReviewCount++
			}
		}
	}
	arbitrationTotal := -1
	arbitrations, arbitrationErr := s.deps.Reviews.ListArbitrationTasks(ctx, item.TenantID, review.ArbitrationFilter{ExamID: item.ID})
	if arbitrationErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("arbitration_tasks_unavailable", "复核任务统计暂不可用"))
	} else {
		arbitrationTotal = len(arbitrations)
		for _, task := range arbitrations {
			if task.Status != "completed" && task.Status != "cancelled" {
				result.Counts.PendingArbitrationCount++
			}
		}
		if result.Counts.PendingArbitrationCount > 0 {
			result.Blockers = append(result.Blockers, Notice{
				Code: "pending_arbitration", Title: "待人工复核", Message: fmt.Sprintf("仍有 %d 个复核任务未完成", result.Counts.PendingArbitrationCount),
				Severity: "blocker", ActionLabel: "开始复核", ActionRoute: examRoute(item.ID, "quality"),
			})
		}
	}

	result.NextActions = buildNextActions(result)
	result.StageProgress = buildStageProgress(result, readiness, submissionsReliable, taskTotal, arbitrationTotal)
	return result, nil
}

func appendPaperImportNotices(ctx context.Context, reader PaperImportReader, tenantID, examID string, readiness *paper.ReadinessResult, result *Projection) {
	if reader == nil {
		return
	}
	imports, err := reader.ListPaperImports(ctx, tenantID, examID)
	if err != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("paper_imports_unavailable", "考试资料识别状态暂不可用"))
		return
	}
	if len(imports) == 0 {
		return
	}
	latest := imports[0]
	for _, candidate := range imports[1:] {
		if candidate.CreatedAt.After(latest.CreatedAt) {
			latest = candidate
		}
	}
	route := examRoute(examID, "paper")
	switch latest.Status {
	case "processing":
		result.Warnings = append(result.Warnings, Notice{Code: "paper_import_processing", Title: "考试资料正在识别", Message: "系统正在提取题目、答案、解析和评分标准", Severity: "warning", ActionLabel: "查看识别进度", ActionRoute: route})
	case "review_required":
		notice := Notice{Code: "paper_import_review_required", Title: "考试资料等待人工核对", Message: "系统已经识别资料，请确认题目、答案、解析和评分标准", Severity: "blocker", ActionLabel: "核对考试资料", ActionRoute: route}
		if readiness != nil && readiness.Ready {
			notice.Severity = "warning"
			result.Warnings = append(result.Warnings, notice)
		} else {
			// Reviewing extracted materials is the actionable prerequisite for
			// readiness failures caused by the same unconfirmed import. Put it
			// first so buildNextActions does not hide it behind a generic paper
			// readiness action with the same route.
			result.Blockers = append([]Notice{notice}, result.Blockers...)
		}
	case "failed":
		result.Warnings = append(result.Warnings, Notice{Code: "paper_import_failed", Title: "最近一次考试资料识别失败", Message: "请查看失败原因并重试或替换资料", Severity: "warning", ActionLabel: "查看考试资料", ActionRoute: route})
	}
}

func buildStageProgress(result Projection, readiness *paper.ReadinessResult, submissionsReliable bool, taskTotal int, arbitrationTotal int) []StageProgress {
	progress := make([]StageProgress, 0, 5)
	if readiness != nil {
		passed := 0
		for _, check := range readiness.Checks {
			if check.Passed {
				passed++
			}
		}
		progress = append(progress, measuredProgress("prepare", result.Stages[0].State, passed, len(readiness.Checks), "项", fmt.Sprintf("已通过 %d / %d 项开考检查", passed, len(readiness.Checks))))
	} else {
		progress = append(progress, statusProgress("prepare", result.Stages[0].State, "暂无可靠的开考检查分母"))
	}
	if submissionsReliable {
		progress = append(progress, StageProgress{
			Stage: "capture", Status: result.Stages[1].State, Completed: intPointer(result.Counts.SubmissionCount), Unit: "份",
			Summary: fmt.Sprintf("已导入 %d 份答卷；应收答卷总数尚未配置，未计算百分比", result.Counts.SubmissionCount),
		})
	} else {
		progress = append(progress, statusProgress("capture", result.Stages[1].State, "答卷导入统计暂不可用"))
	}
	if taskTotal >= 0 {
		progress = append(progress, measuredProgress("grading", result.Stages[2].State, taskTotal-result.Counts.PendingReviewCount, taskTotal, "个任务", fmt.Sprintf("已完成 %d / %d 个阅卷任务", taskTotal-result.Counts.PendingReviewCount, taskTotal)))
	} else {
		progress = append(progress, statusProgress("grading", result.Stages[2].State, "阅卷任务统计暂不可用"))
	}
	if arbitrationTotal >= 0 {
		progress = append(progress, measuredProgress("quality", result.Stages[3].State, arbitrationTotal-result.Counts.PendingArbitrationCount, arbitrationTotal, "个任务", fmt.Sprintf("已完成 %d / %d 个人工复核任务", arbitrationTotal-result.Counts.PendingArbitrationCount, arbitrationTotal)))
	} else {
		progress = append(progress, statusProgress("quality", result.Stages[3].State, "人工复核任务统计暂不可用"))
	}
	resultSummary := "成绩尚未发布；发布检查没有可量化的统一分母"
	if result.ExamStatus == "published" {
		resultSummary = "成绩已发布"
	} else if result.ExamStatus == "archived" {
		resultSummary = "考试已归档"
	} else if result.ExamStatus == "finalized" {
		resultSummary = "成绩已确认，等待发布"
	}
	progress = append(progress, statusProgress("results", result.Stages[4].State, resultSummary))
	return progress
}

func measuredProgress(stage string, status string, completed int, total int, unit string, summary string) StageProgress {
	return StageProgress{Stage: stage, Status: status, Completed: intPointer(completed), Total: intPointer(total), Unit: unit, Summary: summary}
}

func statusProgress(stage string, status string, summary string) StageProgress {
	return StageProgress{Stage: stage, Status: status, Summary: summary}
}

func intPointer(value int) *int { return &value }

func appendSubmissionNotices(result *Projection) {
	if result.Counts.FailedSubmissionCount > 0 {
		result.Blockers = append(result.Blockers, Notice{
			Code: "failed_submissions", Title: "答卷处理失败", Message: fmt.Sprintf("%d 份答卷处理失败，会阻断后续阅卷", result.Counts.FailedSubmissionCount),
			Severity: "blocker", ActionLabel: "查看并重试", ActionRoute: examRoute(result.ExamID, "capture"),
		})
	}
	if result.Counts.UnmatchedSubmissionCount > 0 {
		result.Blockers = append(result.Blockers, Notice{
			Code: "unmatched_submissions", Title: "答卷未匹配学生", Message: fmt.Sprintf("%d 份答卷尚未确认学生身份", result.Counts.UnmatchedSubmissionCount),
			Severity: "blocker", ActionLabel: "确认身份", ActionRoute: examRoute(result.ExamID, "capture"),
		})
	}
	if result.Counts.QualityIssueSubmissionCount > 0 {
		result.Warnings = append(result.Warnings, Notice{
			Code: "quality_issues", Title: "图像质量需要关注", Message: fmt.Sprintf("%d 份答卷存在图像质量问题", result.Counts.QualityIssueSubmissionCount),
			Severity: "warning", ActionLabel: "查看异常", ActionRoute: examRoute(result.ExamID, "capture"),
		})
	}
}

// appendProcessingNotices deliberately does not duplicate every capture/OCR
// metric in the workspace.  It only lifts blocking canonical exceptions into
// the owner's next-action list, where each link preserves the exact exception
// ID and opens the unified processing centre rather than an opaque worker page.
func appendProcessingNotices(ctx context.Context, reader ProcessingReader, tenantID, examID string, result *Projection) {
	if reader == nil {
		return
	}
	summary, summaryErr := reader.Summary(ctx, tenantID, examID)
	if summaryErr != nil {
		result.Warnings = append(result.Warnings, unavailableNotice("processing_unavailable", "页面处理状态暂不可用"))
		return
	}
	if summary.BlockedPages == 0 {
		return
	}
	items, listErr := reader.ListExceptions(ctx, tenantID, processing.ExceptionFilter{
		ExamID: examID,
		Status: processing.ExceptionOpen,
		Limit:  25,
	})
	if listErr != nil {
		result.Blockers = append(result.Blockers, Notice{
			Code:     "processing_blocked_pages",
			Title:    "页面处理存在阻断",
			Message:  fmt.Sprintf("%d 个页面无法进入阅卷；请在识别处理中心查看", summary.BlockedPages),
			Severity: "blocker", ActionLabel: "查看处理异常", ActionRoute: examRoute(examID, "processing"),
		})
		return
	}
	listed := 0
	for _, exception := range items.Exceptions {
		if !exception.Blocking || exception.Status == processing.ExceptionResolved {
			continue
		}
		result.Blockers = append(result.Blockers, Notice{
			Code:     "processing_exception_" + exception.ID,
			Title:    "页面处理阻断：" + string(exception.Code),
			Message:  fmt.Sprintf("页面 %s 需要处理后才能进入阅卷", shortProcessingID(exception.PageID)),
			Severity: "blocker", ActionLabel: "处理此异常", ActionRoute: processingExceptionRoute(examID, exception.ID),
		})
		listed++
		if listed == 3 {
			break
		}
	}
	if listed == 0 || summary.BlockedPages > listed {
		result.Blockers = append(result.Blockers, Notice{
			Code:     "processing_blocked_pages",
			Title:    "更多页面处理阻断",
			Message:  fmt.Sprintf("共 %d 个页面需要处理", summary.BlockedPages),
			Severity: "blocker", ActionLabel: "查看全部异常", ActionRoute: examRoute(examID, "processing"),
		})
	}
}

func shortProcessingID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func buildNextActions(result Projection) []NextAction {
	actions := make([]NextAction, 0, 3)
	seen := map[string]bool{}
	for _, blocker := range result.Blockers {
		if blocker.Code == "paper_import_review_required" && blocker.ActionRoute != "" {
			seen[blocker.ActionRoute] = true
			actions = append(actions, NextAction{Code: blocker.Code, Label: "核对考试资料", Description: blocker.Title, Route: blocker.ActionRoute, Priority: "high"})
			break
		}
	}
	for _, blocker := range result.Blockers {
		if blocker.ActionRoute == "" || seen[blocker.ActionRoute] {
			continue
		}
		seen[blocker.ActionRoute] = true
		actions = append(actions, NextAction{Code: blocker.Code, Label: blocker.ActionLabel, Description: blocker.Title, Route: blocker.ActionRoute, Priority: "high"})
		if len(actions) == 2 {
			break
		}
	}
	primary := statusAction(result.ExamID, result.ExamStatus, result.Counts)
	if !seen[primary.Route] && len(actions) < 3 {
		actions = append(actions, primary)
	}
	return actions
}

func statusAction(examID string, status string, counts Counts) NextAction {
	switch status {
	case "draft", "configured", "ready":
		return NextAction{Code: "continue_preparation", Label: "继续开考准备", Description: "完成试卷、评分标准和答题卡模板配置", Route: examRoute(examID, "prepare"), Priority: "normal"}
	case "collecting":
		return NextAction{Code: "continue_capture", Label: "继续答卷导入", Description: fmt.Sprintf("已导入 %d 份答卷", counts.SubmissionCount), Route: examRoute(examID, "capture"), Priority: "normal"}
	case "grading":
		return NextAction{Code: "continue_grading", Label: "继续阅卷", Description: fmt.Sprintf("剩余 %d 个阅卷任务", counts.PendingReviewCount), Route: examRoute(examID, "grading"), Priority: "normal"}
	case "reviewing":
		return NextAction{Code: "continue_quality", Label: "继续复核", Description: fmt.Sprintf("剩余 %d 个复核任务", counts.PendingArbitrationCount), Route: examRoute(examID, "quality"), Priority: "normal"}
	case "finalized":
		return NextAction{Code: "release_scores", Label: "检查并发布成绩", Description: "确认发布门禁后向学生开放成绩", Route: examRoute(examID, "results"), Priority: "normal"}
	default:
		return NextAction{Code: "view_results", Label: "查看考试结果", Description: "查看成绩和考试报告", Route: examRoute(examID, "results"), Priority: "normal"}
	}
}

func buildStages(examID string, status string) []Stage {
	definitions := []struct{ key, label string }{
		{"prepare", "开考准备"}, {"capture", "答卷导入"}, {"grading", "阅卷"}, {"quality", "复核与异常"}, {"results", "成绩与报告"},
	}
	current := stageIndex(stageForStatus(status))
	stages := make([]Stage, 0, len(definitions))
	for index, definition := range definitions {
		state := "pending"
		if index < current || status == "archived" {
			state = "completed"
		} else if index == current {
			state = "current"
		}
		stages = append(stages, Stage{Key: definition.key, Label: definition.label, State: state, ActionRoute: examRoute(examID, definition.key)})
	}
	return stages
}

func stageForStatus(status string) string {
	switch status {
	case "draft", "configured", "ready":
		return "prepare"
	case "collecting":
		return "capture"
	case "grading":
		return "grading"
	case "reviewing":
		return "quality"
	default:
		return "results"
	}
}

func stageIndex(stage string) int {
	switch stage {
	case "prepare":
		return 0
	case "capture":
		return 1
	case "grading":
		return 2
	case "quality":
		return 3
	default:
		return 4
	}
}

func isPreparationStatus(status string) bool {
	return status == "draft" || status == "configured" || status == "ready"
}

func submissionFailed(item submission.Submission) bool {
	return strings.Contains(item.Status, "failed") || item.Status == "rejected" || item.QualityStatus == "failed"
}

func subjectLabel(code string) string {
	labels := map[string]string{
		"chinese": "语文", "math": "数学", "mathematics": "数学", "english": "英语", "physics": "物理", "chemistry": "化学",
		"biology": "生物", "history": "历史", "geography": "地理", "politics": "道德与法治/思想政治",
		"ethics_politics": "道德与法治/思想政治",
	}
	if label := labels[strings.ToLower(strings.TrimSpace(code))]; label != "" {
		return label
	}
	return code
}

func unavailableNotice(code string, title string) Notice {
	return Notice{Code: code, Title: title, Message: "本次刷新未取得该部分数据，请稍后重试", Severity: "warning"}
}

func riskUnavailableNotice(message string) Notice {
	return Notice{Code: "risk_tier_unavailable", Title: "风险等级待确认", Message: message + "，系统不会按考试阅卷模式推断", Severity: "warning"}
}

func examRoute(examID string, section string) string {
	switch section {
	case "prepare":
		section = "settings"
	case "results":
		section = "scores"
	}
	return "/exams/" + examID + "/" + section
}

func processingExceptionRoute(examID string, exceptionID string) string {
	return examRoute(examID, "processing") + "?exception=" + exceptionID
}
