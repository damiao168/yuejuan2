package schoolassistant

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"github.com/google/uuid"
)

const (
	examSearchPageSize = 50
	examSearchMaxScan  = 200
)

type ExamMatch struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	ActionID  string    `json:"action_id"`
}

type searchExamArgs struct {
	Query      string `json:"query,omitempty"`
	Subject    string `json:"subject,omitempty"`
	RecentDays *int   `json:"recent_days,omitempty"`
	Limit      *int   `json:"limit,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
}

type examSearchCriteria struct {
	query, subject string
	cutoff         time.Time
	limit          int
}

type examSearchPosition struct {
	cursor    pagination.Cursor
	inspected int
}

func (a *searchExamArgs) validate() error {
	a.Query, a.Subject, a.Cursor = strings.TrimSpace(a.Query), strings.TrimSpace(a.Subject), strings.TrimSpace(a.Cursor)
	if !validToolText(a.Query, 100) || !validToolText(a.Subject, 32) || len(a.Cursor) > 1024 || (a.RecentDays != nil && (*a.RecentDays < 1 || *a.RecentDays > 365)) || (a.Limit != nil && (*a.Limit < 1 || *a.Limit > 10)) {
		return reject(400, "invalid_tool_arguments", "检索参数超过允许范围。")
	}
	if a.Limit == nil {
		limit := 5
		a.Limit = &limit
	}
	cursor, err := pagination.Decode(a.Cursor)
	if err != nil {
		return reject(400, "invalid_tool_arguments", "考试检索游标不正确。")
	}
	if cursor.ID != "" {
		if _, err := uuid.Parse(cursor.ID); err != nil {
			return reject(400, "invalid_tool_arguments", "考试检索游标不正确。")
		}
	}
	return nil
}

func normalizeToolSubject(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "数学", "mathematics", "maths":
		return "math"
	case "语文", "language":
		return "chinese"
	case "英语":
		return "english"
	case "物理":
		return "physics"
	case "化学":
		return "chemistry"
	case "生物", "生物学":
		return "biology"
	case "历史":
		return "history"
	case "地理":
		return "geography"
	case "政治", "道德与法治", "思想政治":
		return "politics"
	default:
		return value
	}
}

func (s *Service) searchExams(ctx context.Context, requestID string, user auth.User, access auth.AccessScope, args searchExamArgs) (ToolOutput, error) {
	if !access.TenantWide && !access.IsSchoolWide() && len(access.ExamIDs) == 0 {
		return ToolOutput{}, reject(403, "unsupported_access_scope", "当前授权范围不支持整场考试检索。")
	}
	reader, ok := s.deps.Exams.(ExamSearchReader)
	result := Result{RequestID: requestID, Intent: ToolSearchExams, Status: "completed", Scope: Scope{ClassIDs: []string{}, DataBasis: "exam_live", ScopeLabel: "当前授权范围"}, Facts: []Fact{}, Blocks: []Block{}, Sources: []Source{}, Actions: []Action{}, Limitations: []string{"候选按考试创建时间从新到旧检索；创建时间不是考试举行日期，候选数量不是全校考试总数。"}}
	source := Source{ID: "exam_search", Kind: "exam_search", Status: "available", Label: "获准考试检索", ReadAt: time.Now().UTC(), Consistency: "source_local", ScopeLabel: result.Scope.ScopeLabel}
	output := ToolOutput{Name: ToolSearchExams, Result: result, Exams: []ExamMatch{}}
	if !ok || reader == nil {
		sourceUnavailable(&source, &output.Result, "考试检索暂不可用。")
		output.Result.Sources = append(output.Result.Sources, source)
		output.Status = "unavailable"
		return output, nil
	}
	cursor, _ := pagination.Decode(args.Cursor)
	criteria := examSearchCriteria{query: strings.ToLower(args.Query), subject: normalizeToolSubject(args.Subject), limit: *args.Limit}
	if args.RecentDays != nil {
		criteria.cutoff = source.ReadAt.AddDate(0, 0, -*args.RecentDays)
	}
	position := examSearchPosition{cursor: cursor}
	if err := s.scanExamSearch(ctx, reader, access, criteria, &position, &output, &source); err != nil {
		return ToolOutput{}, err
	}
	finishExamSearch(&output, source, position.cursor)
	s.audit(ctx, preparedQuery{request: QueryRequest{RequestID: requestID, PresetIntent: ToolSearchExams}, user: user}, output.Status)
	return output, nil
}

// scanExamSearch 每页只检查前 50 行，第 51 行用于判断是否还有候选；总检查量与获准候选数分开计算。
func (s *Service) scanExamSearch(ctx context.Context, reader ExamSearchReader, access auth.AccessScope, criteria examSearchCriteria, position *examSearchPosition, output *ToolOutput, source *Source) error {
	finished := false
	for position.inspected < examSearchMaxScan && !finished {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := reader.ListExams(ctx, access, exam.ListFilter{Limit: examSearchPageSize + 1, CursorAt: position.cursor.CreatedAt, CursorID: position.cursor.ID})
		if err != nil {
			sourceUnavailable(source, &output.Result, "考试检索来源暂不可用，不能据此推断没有考试。")
			break
		}
		if len(items) == 0 {
			break
		}
		pageMore := len(items) > examSearchPageSize
		if pageMore {
			items = items[:examSearchPageSize]
		}
		pageFinished, limitReached := s.scanExamSearchPage(ctx, access, criteria, position, output, source, items)
		if limitReached {
			output.HasMore = output.HasMore || pageMore
		}
		finished = pageFinished || !pageMore
	}
	return nil
}

// 只有达到数量上限才结合下一页标记 HasMore；时间窗结束或来源错误不能伪造可续查位置。
func (s *Service) scanExamSearchPage(ctx context.Context, access auth.AccessScope, criteria examSearchCriteria, position *examSearchPosition, output *ToolOutput, source *Source, items []exam.Exam) (finished, limitReached bool) {
	for index, item := range items {
		position.inspected++
		finished, note := s.inspectExamSearchCandidate(ctx, access, item, criteria, &position.cursor, output)
		if note != "" {
			sourceUnavailable(source, &output.Result, note)
		}
		if finished {
			return true, false
		}
		if len(output.Exams) >= criteria.limit || position.inspected >= examSearchMaxScan {
			output.HasMore = index < len(items)-1
			return true, true
		}
	}
	return false, false
}

func examSearchCursorAdvances(item exam.Exam, cursor pagination.Cursor) bool {
	if item.CreatedAt.IsZero() || item.ID == "" {
		return false
	}
	if cursor.CreatedAt.IsZero() {
		return true
	}
	return item.CreatedAt.Before(cursor.CreatedAt) || (item.CreatedAt.Equal(cursor.CreatedAt) && item.ID < cursor.ID)
}

func (s *Service) inspectExamSearchCandidate(ctx context.Context, access auth.AccessScope, item exam.Exam, criteria examSearchCriteria, cursor *pagination.Cursor, output *ToolOutput) (bool, string) {
	if !examSearchCursorAdvances(item, *cursor) {
		return true, "考试检索分页位置不一致，已停止扫描。"
	}
	// 已有列表只提供创建时间游标。即使某行未匹配，也必须推进位置；下次调用仍按当前身份重新过滤。
	*cursor = pagination.Cursor{CreatedAt: item.CreatedAt, ID: item.ID}
	if !criteria.cutoff.IsZero() && item.CreatedAt.Before(criteria.cutoff) {
		return true, ""
	}
	allowed, err := s.searchExamAccessible(ctx, access, item)
	if err != nil {
		return true, "暂时无法核实考试检索资源范围。"
	}
	// 即使读替身或以后存储出错返回越界行，也不能把越界行数量报告为当前学校的已扫描候选。
	if allowed {
		output.Scanned++
	}
	if allowed && examSearchMatches(item, criteria) {
		actionID := "exam." + item.ID + ".overview"
		output.Exams = append(output.Exams, ExamMatch{ID: item.ID, Name: item.Name, Subject: item.Subject, Status: item.Status, Revision: item.Revision, CreatedAt: item.CreatedAt, ActionID: actionID})
		output.Result.Actions = append(output.Result.Actions, Action{ID: actionID, Label: "打开" + item.Name, Target: NavigationTarget{Kind: "exam_section", ExamID: item.ID, Section: "overview"}})
	}
	return false, ""
}

func examSearchMatches(item exam.Exam, criteria examSearchCriteria) bool {
	return (criteria.query == "" || strings.Contains(strings.ToLower(item.Name+" "+item.SessionName), criteria.query)) && (criteria.subject == "" || normalizeToolSubject(item.Subject) == criteria.subject)
}

// finishExamSearch 保留已取得候选；后续来源失败降为 partial，不能把未知数量变成正常零值。
func finishExamSearch(output *ToolOutput, source Source, cursor pagination.Cursor) {
	if output.HasMore {
		output.NextCursor = pagination.Encode(cursor.CreatedAt, cursor.ID)
		output.Result.Limitations = append(output.Result.Limitations, "检索尚未覆盖全部候选；继续检索须使用 next_cursor 并保留相同筛选。当前候选不能证明考试唯一。")
	}
	if source.Status == "available" && len(output.Exams) == 0 {
		source.Status = "empty"
		source.Note = "本次有限扫描中没有匹配考试。"
	}
	if source.Status == "unavailable" && len(output.Exams) > 0 {
		source.Status, source.Note = "partial", "部分候选已读取，后续读取或边界核实失败；不能据此推断候选完整。"
	}
	output.Result.Sources = append(output.Result.Sources, source)
	count := float64(len(output.Exams))
	output.Result.Facts = append(output.Result.Facts, Fact{ID: "exam_search.returned_matches", SourceID: source.ID, MetricKey: "returned_match_count", Label: "本次返回候选", Available: source.Status != "unavailable", Value: &count, Unit: "count", DefinitionID: "assistant.exam_search.bounded_matches.v1"})
	if source.Status == "unavailable" {
		output.Result.Facts[0].Value = nil
		output.Result.Facts[0].Reason = source.Note
	}
	rows := []Row{}
	for _, item := range output.Exams {
		rows = append(rows, Row{Code: "exam_match", Label: item.Name, Message: item.Subject + " · " + item.Status + " · 创建于 " + item.CreatedAt.UTC().Format(time.RFC3339), Severity: "info", SourceID: source.ID, ActionID: item.ActionID})
	}
	output.Result.Blocks = append(output.Result.Blocks, Block{Type: "issues", Title: "考试候选", Rows: rows}, Block{Type: "notice", Text: "本次扫描 " + strconv.Itoa(output.Scanned) + " 个候选，返回 " + strconv.Itoa(len(output.Exams)) + " 个匹配；多候选时请确认考试。"})
	output.Status = toolAvailability(output.Result)
}

func (s *Service) searchExamAccessible(ctx context.Context, access auth.AccessScope, item exam.Exam) (bool, error) {
	if item.TenantID != access.TenantID || (!access.TenantWide && !access.AllowsExam(item.ID) && (!access.IsSchoolWide() || !access.AllowsSchool(item.SchoolID))) {
		return false, nil
	}
	if _, err := uuid.Parse(item.ID); err != nil {
		return false, nil
	}
	if s.deps.Resources == nil {
		return true, nil
	}
	boundary, err := s.deps.Resources.ResolveResourceBoundary(ctx, access, "exam", item.ID)
	if errors.Is(err, auth.ErrResourceBoundaryNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return auth.AllowsResourceBoundary(access, boundary), nil
}
