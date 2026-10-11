package schoolassistant

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

type queryError struct {
	status        int
	code, message string
}

func (e *queryError) Error() string                 { return e.code }
func reject(status int, code, message string) error { return &queryError{status, code, message} }

type preparedQuery struct {
	request       QueryRequest
	user          auth.User
	access        auth.AccessScope
	scope         Scope
	clarification string
}

func principal(ctx context.Context) (auth.User, auth.AccessScope, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return auth.User{}, auth.AccessScope{}, reject(http.StatusUnauthorized, "unauthenticated", "请先登录。")
	}
	if !auth.HasRole(user, "school_admin") {
		return user, auth.AccessScope{}, reject(http.StatusForbidden, "forbidden", "仅学校管理员可使用学校业务助手。")
	}
	scope, ok := auth.AccessScopeFromContext(ctx)
	// 不能用 body 或另一次身份解析补充授权；租户与操作者必须来自同一认证上下文。
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID || !scope.HasDataAccess() {
		return user, scope, reject(http.StatusForbidden, "access_scope_missing", "当前账号没有有效的数据访问范围。")
	}
	return user, scope, nil
}

func requiredPermission(intent string) string {
	if intent == IntentExamProgress {
		return "dashboard:read"
	}
	if intent == IntentPublishDiagnosis {
		return "score:manage"
	}
	return ""
}

func validateRequest(input *QueryRequest) error {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Question = strings.TrimSpace(input.Question)
	input.ContextHint.ExamID = strings.TrimSpace(input.ContextHint.ExamID)
	if _, err := uuid.Parse(input.RequestID); err != nil {
		return reject(400, "invalid_request_id", "请求标识必须是 UUID。")
	}
	if utf8.RuneCountInString(input.Question) == 0 || utf8.RuneCountInString(input.Question) > 2000 {
		return reject(400, "invalid_question", "问题长度应为 1–2000 字符。")
	}
	if requiredPermission(input.PresetIntent) == "" {
		return reject(400, "unsupported_intent", "首期仅支持考试进度和学生端发布诊断。")
	}
	if input.ContextHint.ExamID != "" {
		if _, err := uuid.Parse(input.ContextHint.ExamID); err != nil {
			return reject(400, "invalid_exam_id", "考试标识必须是 UUID。")
		}
	}
	if len(input.ContextHint.ClassIDs) > 0 || (input.PreviousScopeHint != nil && len(input.PreviousScopeHint.ClassIDs) > 0) {
		return reject(400, "unsupported_filter", "首期暂不支持按班级查询；请明确选择整场考试范围。")
	}
	if input.PreviousScopeHint != nil {
		return reject(400, "unsupported_previous_scope", "首期请通过当前范围选择考试，不支持自动继承上次范围。")
	}
	if channel := input.ContextHint.ReleaseChannel; channel != "" && channel != "student_portal" {
		return reject(400, "unsupported_channel", "首期仅提供学生端成绩发布诊断，请到原考试流程处理旧版发布。")
	}
	return nil
}

func (s *Service) prepare(ctx context.Context, input QueryRequest) (preparedQuery, error) {
	if err := validateRequest(&input); err != nil {
		return preparedQuery{}, err
	}
	user, access, err := principal(ctx)
	if err != nil {
		return preparedQuery{}, err
	}
	if !auth.HasPermission(user, requiredPermission(input.PresetIntent)) {
		return preparedQuery{}, reject(403, "permission_denied", "当前账号不能查询这类数据。")
	}
	// 班级角色的父学校 ID 只用于组织导航，不能据此返回整场考试统计。
	// 未选择考试时只能返回范围澄清；只有带了具体考试 ID 才检查该考试是否属于当前任务范围。
	if input.ContextHint.ExamID != "" && !access.TenantWide && !access.IsSchoolWide() && !access.AllowsExam(input.ContextHint.ExamID) {
		return preparedQuery{}, reject(403, "unsupported_access_scope", "首期整场考试统计不支持当前班级或任务范围。")
	}
	q := preparedQuery{request: input, user: user, access: access, scope: Scope{ClassIDs: []string{}, DataBasis: "workspace_live", ScopeLabel: "请选择考试"}}
	if input.ContextHint.ExamID == "" {
		q.clarification = "请选择要查询的考试。"
		return q, nil
	}
	if s.deps.Exams == nil {
		return preparedQuery{}, reject(503, "assistant_unavailable", "考试查询暂不可用。")
	}
	// 通用 URL 边界不会解析新接口的 body；每次查询都先走获准考试读取，再检查可用资源 resolver。
	item, err := s.authorizedExam(ctx, user, access, input.ContextHint.ExamID)
	if err != nil {
		return preparedQuery{}, err
	}
	q.scope.ExamID, q.scope.ExamName, q.scope.Subject = item.ID, item.Name, item.Subject
	q.scope.ScopeLabel = item.Name + " · 整场考试"
	if input.PresetIntent == IntentPublishDiagnosis {
		if input.ContextHint.ReleaseChannel == "" {
			q.clarification = "请确认查询学生端成绩发布；首期暂不支持旧版考试发布诊断。"
		} else {
			q.scope.ReleaseChannel = "student_portal"
		}
	}
	return q, nil
}
