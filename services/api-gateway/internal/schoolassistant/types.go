// Package schoolassistant 为学校管理员提供 AI 对话和只读业务工具。
// 身份与资源范围来自认证上下文；事实由既有工作区和完整发布门禁提供。
// 兼容查询接口仍按明确任务读取；对话入口由模型选择工具，不保存聊天正文。
package schoolassistant

import (
	"context"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

const (
	IntentExamProgress     = "exam_progress"
	IntentPublishDiagnosis = "publish_diagnosis"
)

type ContextHint struct {
	ExamID         string   `json:"exam_id,omitempty"`
	ClassIDs       []string `json:"class_ids,omitempty"`
	OriginModule   string   `json:"origin_module,omitempty"`
	OriginSection  string   `json:"origin_section,omitempty"`
	Revision       *int64   `json:"revision,omitempty"`
	ReleaseChannel string   `json:"release_channel,omitempty"`
}

type QueryRequest struct {
	RequestID         string       `json:"request_id"`
	Question          string       `json:"question"`
	PresetIntent      string       `json:"preset_intent"`
	ContextHint       ContextHint  `json:"context_hint"`
	PreviousScopeHint *ContextHint `json:"previous_scope_hint,omitempty"`
}

type Capability struct {
	Intent             string `json:"intent"`
	Label              string `json:"label"`
	Available          bool   `json:"available"`
	RequiredPermission string `json:"required_permission"`
	Reason             string `json:"reason,omitempty"`
}

type Capabilities struct {
	Mode          string       `json:"mode"`
	ModelRequired bool         `json:"model_required"`
	Intents       []Capability `json:"intents"`
}

type Scope struct {
	ExamID         string   `json:"exam_id,omitempty"`
	ExamName       string   `json:"exam_name,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	ClassIDs       []string `json:"class_ids"`
	ReleaseChannel string   `json:"release_channel,omitempty"`
	DataBasis      string   `json:"data_basis"`
	ScopeLabel     string   `json:"scope_label"`
}

type Source struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	Label          string    `json:"label"`
	ReadAt         time.Time `json:"read_at"`
	Revision       string    `json:"revision,omitempty"`
	ReleaseID      string    `json:"release_id,omitempty"`
	ReleaseVersion *int      `json:"release_version,omitempty"`
	Consistency    string    `json:"consistency"`
	ScopeLabel     string    `json:"scope_label"`
	Note           string    `json:"note,omitempty"`
}

// Fact 的不可用值使用 null；不把来源故障转换为正常的零值。
type Fact struct {
	ID           string   `json:"id"`
	SourceID     string   `json:"source_id"`
	MetricKey    string   `json:"metric_key"`
	Label        string   `json:"label"`
	Available    bool     `json:"available"`
	Value        *float64 `json:"value"`
	Unit         string   `json:"unit"`
	DefinitionID string   `json:"definition_id"`
	Reason       string   `json:"reason,omitempty"`
}

type Row struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Count    *int   `json:"count,omitempty"`
	Unit     string `json:"unit,omitempty"`
	SourceID string `json:"source_id"`
	ActionID string `json:"action_id,omitempty"`
}

type Block struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	Rows  []Row  `json:"rows,omitempty"`
}

type NavigationTarget struct {
	Kind    string `json:"kind"`
	ExamID  string `json:"exam_id"`
	Section string `json:"section"`
}

type Action struct {
	ID     string           `json:"id"`
	Label  string           `json:"label"`
	Target NavigationTarget `json:"target"`
}

type Result struct {
	RequestID   string   `json:"request_id"`
	Intent      string   `json:"intent"`
	Status      string   `json:"status"`
	Scope       Scope    `json:"scope"`
	Facts       []Fact   `json:"facts"`
	Blocks      []Block  `json:"blocks"`
	Sources     []Source `json:"sources"`
	Actions     []Action `json:"actions"`
	Limitations []string `json:"limitations"`
}

type Event struct {
	Type       string  `json:"type"`
	RequestID  string  `json:"request_id"`
	Seq        int     `json:"seq"`
	Scope      *Scope  `json:"scope,omitempty"`
	SourceID   string  `json:"source_id,omitempty"`
	Label      string  `json:"label,omitempty"`
	Source     *Source `json:"source,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
	Result     *Result `json:"result,omitempty"`
	Status     string  `json:"status,omitempty"`
	Code       string  `json:"code,omitempty"`
	Message    string  `json:"message,omitempty"`
	Retryable  bool    `json:"retryable,omitempty"`
}

type ExamReader interface {
	GetExam(context.Context, auth.AccessScope, string) (exam.Exam, error)
}
type WorkspaceReader interface {
	Get(context.Context, auth.AccessScope, string) (workspace.Projection, error)
}
type GateReader interface {
	Inspect(context.Context, string, string) (releasegate.Evaluation, error)
}
type CurrentReleaseReader interface {
	CurrentPublished(context.Context, string, string) (scorerelease.Detail, error)
}

type Dependencies struct {
	Exams           ExamReader
	Workspaces      WorkspaceReader
	Gates           GateReader
	CurrentReleases CurrentReleaseReader
	Resources       auth.ResourceBoundaryResolver
	Audit           auth.AuditRecorder
	Conversation    ConversationDependencies
}
