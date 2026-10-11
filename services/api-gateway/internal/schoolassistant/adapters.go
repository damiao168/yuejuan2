package schoolassistant

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

const (
	releaseDiagnosisTitle     = "学生端发布诊断"
	releaseVerificationFailed = "暂时无法核实完整发布条件。"
)

func (s *Service) readWorkspace(ctx context.Context, q preparedQuery, source *Source, result *Result) {
	if s.deps.Workspaces == nil {
		sourceUnavailable(source, result, "考试工作区暂不可用。")
		return
	}
	projection, err := s.deps.Workspaces.Get(ctx, q.access, q.scope.ExamID)
	if err != nil {
		sourceUnavailable(source, result, "暂时无法读取考试工作区。")
		return
	}
	if projection.ExamID != q.scope.ExamID {
		sourceUnavailable(source, result, "考试工作区返回的范围不一致。")
		return
	}
	source.Revision = strconv.FormatInt(projection.Revision, 10)
	countsReliable := true
	for _, notice := range projection.Warnings {
		if strings.HasSuffix(notice.Code, "_unavailable") {
			source.Status, source.Note = "partial", "部分工作区来源未能读取，请结合警告核实。"
			result.Status = "partial"
			// 工作区的旧计数字段没有来源状态；任何来源降级时先不展示计数，防止把故障零值当成事实。
			// 新不可用代码也按保守规则降级，无需在助手中复制每个领域的失败分类。
			countsReliable = false
		}
	}
	result.Facts = append(result.Facts, workspaceCountFacts(source.ID, projection.Counts, countsReliable)...)
	if q.request.PresetIntent == IntentExamProgress {
		text := "当前阶段：" + stageLabel(projection) + "。"
		if !countsReliable {
			text += "部分来源暂不可用，请先核实下方警告。"
		}
		result.Blocks = append(result.Blocks, Block{Type: "summary", Title: "考试进度", Text: text})
	}
	notices := append(append([]workspace.Notice{}, projection.Blockers...), projection.Warnings...)
	rows := workspaceNoticeRows(result, q.scope.ExamID, source.ID, notices)
	if len(rows) > 0 {
		result.Blocks = append(result.Blocks, Block{Type: "issues", Title: "工作区阻塞与警告", Rows: rows})
	}
	for _, action := range projection.NextActions {
		addAction(result, q.scope.ExamID, action.Route, action.Label)
	}
}

func workspaceCountFacts(sourceID string, counts workspace.Counts, reliable bool) []Fact {
	facts := make([]Fact, 0, 8)
	for _, metric := range []struct {
		key, label string
		value      int
	}{
		{"paper_count", "试卷数量", counts.PaperCount},
		{"question_count", "题目数量", counts.QuestionCount},
		{"submission_count", "答卷数量", counts.SubmissionCount},
		{"failed_submission_count", "处理失败答卷", counts.FailedSubmissionCount},
		{"quality_issue_submission_count", "质量异常答卷", counts.QualityIssueSubmissionCount},
		{"unmatched_submission_count", "未匹配答卷", counts.UnmatchedSubmissionCount},
		{"pending_review_count", "待完成阅卷任务", counts.PendingReviewCount},
		{"pending_arbitration_count", "待完成复核任务", counts.PendingArbitrationCount},
	} {
		fact := Fact{ID: "workspace." + metric.key, SourceID: sourceID, MetricKey: metric.key, Label: metric.label, Available: reliable, Unit: "count", DefinitionID: "workspace.counts.v1." + metric.key}
		if reliable {
			value := float64(metric.value)
			fact.Value = &value
		} else {
			fact.Reason = "工作区存在未能读取的来源，暂不将计数作为可靠事实。"
		}
		facts = append(facts, fact)
	}
	return facts
}

func workspaceNoticeRows(result *Result, examID, sourceID string, notices []workspace.Notice) []Row {
	rows := []Row{}
	for _, notice := range notices {
		label := notice.Title
		if label == "" {
			label = notice.Code
		}
		severity := notice.Severity
		if severity != "blocker" && severity != "warning" {
			severity = "info"
		}
		row := Row{Code: notice.Code, Label: label, Message: notice.Message, Severity: severity, SourceID: sourceID}
		row.ActionID = addAction(result, examID, notice.ActionRoute, notice.ActionLabel)
		rows = append(rows, row)
	}
	return rows
}

func (s *Service) readGate(ctx context.Context, q preparedQuery, source *Source, result *Result) {
	if s.deps.Gates == nil {
		sourceUnavailable(source, result, "完整发布门禁暂不可用。")
		result.Blocks = append(result.Blocks, Block{Type: "summary", Title: releaseDiagnosisTitle, Text: releaseVerificationFailed})
		return
	}
	evaluation, err := s.deps.Gates.Inspect(ctx, q.user.TenantID, q.scope.ExamID)
	if err != nil {
		sourceUnavailable(source, result, releaseVerificationFailed)
		result.Blocks = append(result.Blocks, Block{Type: "summary", Title: releaseDiagnosisTitle, Text: releaseVerificationFailed})
		return
	}
	if evaluation.Policy.TenantID != q.user.TenantID || evaluation.Policy.ExamID != q.scope.ExamID {
		sourceUnavailable(source, result, "完整发布门禁返回的资源范围不一致。")
		return
	}
	source.Revision = evaluation.Policy.Version
	text := "当前完整发布检查未通过，请处理下列阻塞或待确认警告。"
	if evaluation.Passed {
		text = "当前完整发布检查通过；实际发布时系统仍会重新检查，请进入成绩发布页面完成操作。"
	}
	result.Blocks = append(result.Blocks, Block{Type: "summary", Title: releaseDiagnosisTitle, Text: text})
	pending := map[string]bool{}
	for _, code := range evaluation.PendingWarningCodes {
		pending[code] = true
	}
	rows := []Row{}
	for _, issue := range append(append([]scorerelease.GateIssue{}, evaluation.BaseGate.Blocking...), evaluation.BaseGate.Warnings...) {
		severity := "warning"
		if issue.Blocking {
			severity = "blocker"
		}
		message := issue.Message
		if pending[issue.Code] {
			message += "；当前策略要求确认该警告。"
		}
		count := issue.Count
		row := Row{Code: issue.Code, Label: issue.Code, Message: message, Severity: severity, Count: &count, Unit: "项", SourceID: source.ID}
		row.ActionID = addAction(result, q.scope.ExamID, issue.ActionRoute, "前往处理")
		rows = append(rows, row)
		delete(pending, issue.Code)
	}
	for _, code := range evaluation.PendingWarningCodes {
		if pending[code] {
			rows = append(rows, Row{Code: code, Label: code, Message: "当前发布策略要求确认该警告。", Severity: "warning", SourceID: source.ID})
		}
	}
	if len(rows) > 0 {
		result.Blocks = append(result.Blocks, Block{Type: "issues", Title: "完整发布门禁事项", Rows: rows})
	}
	addAction(result, q.scope.ExamID, "scores", "打开成绩发布")
	result.Limitations = append(result.Limitations, "门禁事项可能涉及同一答卷或不同对象，数量不能相加为异常学生总数。")
}

func stageLabel(projection workspace.Projection) string {
	for _, stage := range projection.Stages {
		if stage.Key == projection.Stage {
			return stage.Label
		}
	}
	if projection.Stage != "" {
		return projection.Stage
	}
	return "暂未提供"
}

func currentReleaseText(version int) string {
	return fmt.Sprintf("学生端当前生效版本为 v%d，以当前发布指针为准。", version)
}

// 适配器仅把领域提供的已知页面目标转换为稳定导航意图；未知代码仍显示事实，未知目标不生成按钮。
func sectionForRoute(examID, route string) string {
	route = strings.TrimSpace(route)
	if strings.HasPrefix(route, "/exams/") {
		parsed, err := url.Parse(route)
		if err != nil || parsed.IsAbs() {
			return ""
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "exams" || parts[1] != examID {
			return ""
		}
		route = parts[2]
	}
	switch route {
	case "grades", "regrade":
		return "scores"
	case "overview", "students", "paper", "questions", "template", "settings", "capture", "processing", "grading", "quality", "scores", "appeals", "reports":
		return route
	default:
		return ""
	}
}

func addAction(result *Result, examID, route, label string) string {
	section := sectionForRoute(examID, route)
	if section == "" {
		return ""
	}
	for _, action := range result.Actions {
		if action.Target.Section == section {
			return action.ID
		}
	}
	if label == "" {
		label = "前往处理"
	}
	id := "exam." + section
	result.Actions = append(result.Actions, Action{ID: id, Label: label, Target: NavigationTarget{Kind: "exam_section", ExamID: examID, Section: section}})
	return id
}
