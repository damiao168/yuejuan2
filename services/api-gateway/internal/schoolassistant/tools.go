package schoolassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
)

const (
	ToolSearchExams         = "search_exams"
	ToolGetExamProgress     = "get_exam_progress"
	ToolDiagnosePublication = "diagnose_publication"
	toolArgumentsLimit      = 16 << 10
)

// ToolDefinition 描述稳定的领域读能力；页面文案和菜单顺序不参与模型选工具。
// 权限描述供编排展示，真正的授权仍在每次 ExecuteTool 调用中执行。
type ToolDefinition struct {
	Name               string         `json:"name"`
	Label              string         `json:"label"`
	Description        string         `json:"description"`
	Parameters         map[string]any `json:"parameters"`
	RequiredPermission string         `json:"required_permission"`
}

// ToolOutput 同时提供模型可读事实和界面可验证来源；未读到的数据不能表达为零。
// HasMore 与 NextCursor 只表示还有候选需扫描，检索结果不承诺是全量统计。
type ToolOutput struct {
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Result     Result      `json:"result"`
	Exams      []ExamMatch `json:"exams,omitempty"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more,omitempty"`
	Scanned    int         `json:"scanned,omitempty"`
}

// ExamSearchReader 不暴露 Store 写能力，实际考试存储可直接实现此窄接口。
type ExamSearchReader interface {
	ListExams(context.Context, auth.AccessScope, exam.ListFilter) ([]exam.Exam, error)
}

type examToolArgs struct {
	ExamID string `json:"exam_id"`
}

type publicationToolArgs struct {
	ExamID         string `json:"exam_id"`
	ReleaseChannel string `json:"release_channel,omitempty"`
}

func toolDefinitions() []ToolDefinition {
	examID := map[string]any{"type": "string", "format": "uuid", "description": "获准学科考试 ID，从当前上下文或 search_exams 结果取得；名称相同或候选不唯一时先向用户确认。"}
	return []ToolDefinition{
		{Name: ToolSearchExams, Label: "查找考试", RequiredPermission: "exam:manage", Description: "查找当前授权范围内的学科考试。支持名称或考试组名称关键词、学科和近几天筛选，按创建时间从新到旧返回有限候选。创建时间不是考试举行日期；有更多候选时应继续检索或向用户确认，不能宣称已查全校所有考试。", Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "maxLength": 100, "description": "考试或考试组名称关键词，可留空。"},
				"subject":     map[string]any{"type": "string", "maxLength": 32, "description": "学科名称或代码，如数学/math、语文/chinese；可留空。"},
				"recent_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "description": "仅查近几天创建的考试，省略时不限制日期。"},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "最多返回的匹配候选数量，默认 5。"},
				"cursor":      map[string]any{"type": "string", "maxLength": 1024, "description": "只使用上一检索返回的 next_cursor；继续时保留相同筛选条件。"},
			},
		}},
		{Name: ToolGetExamProgress, Label: "读取考试进度", RequiredPermission: "dashboard:read", Description: "读取已确认考试的当前工作区阶段、数量、阻塞与警告、下一步处理入口。只读；来源故障不能当作已完成或零任务。", Parameters: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"exam_id"}, "properties": map[string]any{"exam_id": examID},
		}},
		{Name: ToolDiagnosePublication, Label: "检查发布条件", RequiredPermission: "score:manage", Description: "读取考试完整发布门禁和学生端当前生效发布指针，解释为什么不能发布以及要处理的事项。只诊断学生端发布，不发布、不确认警告、不批准豁免、不改成绩。", Parameters: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"exam_id"}, "properties": map[string]any{
				"exam_id":         examID,
				"release_channel": map[string]any{"type": "string", "enum": []string{"student_portal"}, "description": "学生端发布；省略时使用 student_portal。"},
			},
		}},
	}
}

func toolPermission(name string) string {
	for _, definition := range toolDefinitions() {
		if definition.Name == name {
			return definition.RequiredPermission
		}
	}
	return ""
}

func (s *Service) toolDependenciesAvailable(name string) bool {
	switch name {
	case ToolSearchExams:
		reader, ok := s.deps.Exams.(ExamSearchReader)
		return ok && reader != nil
	case ToolGetExamProgress:
		return s.deps.Exams != nil && s.deps.Workspaces != nil
	case ToolDiagnosePublication:
		return s.deps.Exams != nil && s.deps.Workspaces != nil && s.deps.Gates != nil && s.deps.CurrentReleases != nil
	default:
		return false
	}
}

// ToolDefinitions 只向模型提供本次身份可用的工具，不授予后续调用权限。
func (s *Service) ToolDefinitions(ctx context.Context) ([]ToolDefinition, error) {
	user, access, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	definitions := []ToolDefinition{}
	if !access.TenantWide && !access.IsSchoolWide() && len(access.ExamIDs) == 0 {
		return definitions, nil
	}
	for _, definition := range toolDefinitions() {
		if auth.HasPermission(user, definition.RequiredPermission) && s.toolDependenciesAvailable(definition.Name) {
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

// ExecuteTool 对模型选择的工具重新检查身份、权限和参数；认证上下文不能由模型参数补充。
// 考试事实沿用原领域只读服务，模型不接触发布、改分和人员写入能力。
func (s *Service) ExecuteTool(ctx context.Context, requestID, name string, arguments json.RawMessage) (ToolOutput, error) {
	user, access, err := principal(ctx)
	if err != nil {
		return ToolOutput{}, err
	}
	permission := toolPermission(name)
	if permission == "" {
		return ToolOutput{}, reject(400, "unknown_tool", "工具不存在。")
	}
	if !auth.HasPermission(user, permission) {
		return ToolOutput{}, reject(403, "permission_denied", "当前账号不能查询这类数据。")
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return ToolOutput{}, reject(400, "invalid_request_id", "请求标识必须是 UUID。")
	}
	if err := ctx.Err(); err != nil {
		return ToolOutput{}, err
	}
	if name == ToolSearchExams {
		var args searchExamArgs
		if err := decodeToolArguments(arguments, &args); err != nil {
			return ToolOutput{}, err
		}
		if err := args.validate(); err != nil {
			return ToolOutput{}, err
		}
		return s.searchExams(ctx, requestID, user, access, args)
	}
	request := QueryRequest{RequestID: requestID, Question: "读取本次工具所需的真实业务事实。"}
	if name == ToolGetExamProgress {
		var args examToolArgs
		if err := decodeToolArguments(arguments, &args); err != nil {
			return ToolOutput{}, err
		}
		request.PresetIntent, request.ContextHint.ExamID = IntentExamProgress, strings.TrimSpace(args.ExamID)
	} else {
		var args publicationToolArgs
		if err := decodeToolArguments(arguments, &args); err != nil {
			return ToolOutput{}, err
		}
		request.PresetIntent, request.ContextHint.ExamID = IntentPublishDiagnosis, strings.TrimSpace(args.ExamID)
		request.ContextHint.ReleaseChannel = strings.TrimSpace(args.ReleaseChannel)
		if request.ContextHint.ReleaseChannel == "" {
			request.ContextHint.ReleaseChannel = "student_portal"
		}
	}
	if request.ContextHint.ExamID == "" {
		return ToolOutput{}, reject(400, "invalid_exam_id", "请先确认要查询的考试。")
	}
	// Query 内的 prepare 每次核实 body 资源；不能因模型在上一轮看过候选就跳过此检查。
	result, err := s.Query(ctx, request, nil)
	if err != nil {
		return ToolOutput{}, err
	}
	return ToolOutput{Name: name, Status: toolAvailability(result), Result: result}, nil
}

func decodeToolArguments(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > toolArgumentsLimit || trimmed[0] != '{' {
		return reject(400, "invalid_tool_arguments", "工具参数必须是受限的 JSON 对象。")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(trimmed, &fields) != nil {
		return reject(400, "invalid_tool_arguments", "工具参数格式不正确。")
	}
	// 当前 schema 没有可空字段；encoding/json 会把 null 吞成零值，不能据此扩大默认检索范围。
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return reject(400, "invalid_tool_arguments", "工具参数不接受 null；无需筛选时请省略字段。")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return reject(400, "invalid_tool_arguments", "工具参数格式不正确或包含不允许的字段。")
	}
	return nil
}

func toolAvailability(result Result) string {
	available := false
	for _, source := range result.Sources {
		if source.Status == "available" || source.Status == "empty" || source.Status == "partial" {
			available = true
		}
	}
	if len(result.Sources) > 0 && !available {
		return "unavailable"
	}
	return result.Status
}

func validToolText(value string, maxRunes int) bool {
	return utf8.RuneCountInString(value) <= maxRunes && !strings.ContainsAny(value, "\x00\r\n")
}
