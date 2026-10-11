package seedquality

import (
	"context"
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

var ErrContextUnavailable = errors.New("seed grader context is unavailable")

// ContextSource 的范围小于 assessment.Store。内存和 PostgreSQL 评估存储均已实现该接口。
type ContextSource interface {
	GetQuestionSnapshot(context.Context, string, string, string) (assessment.ExamQuestionSnapshot, error)
}

// ReviewHook 是普通复核路由所用的完整、小型拦截接口。
// handler 先尝试这些方法，仅在 handled/issued 为 false 时才转入正常存储。
type ReviewHook interface {
	RecoverCommand(context.Context, string, string, string) (commandreceipt.Receipt, error)
	MaybeIssue(context.Context, string, string, string, string, string) (Task, bool, error)
	GetGraderTaskContext(context.Context, string, string, string) (GraderTaskContext, bool, error)
	GetGraderImageSource(context.Context, string, string, string) (GraderImageSource, bool, error)
	TrySubmit(context.Context, string, string, string, SubmitInput) (SubmitReceipt, bool, error)
}

var _ ReviewHook = (*Service)(nil)

// GraderTask 对应既有 ReviewTask UI 使用的字段，但所有可能暴露 Gold 提交的标识都改为合成值。
type GraderTask struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	ExamID          string     `json:"exam_id"`
	QuestionID      string     `json:"question_id"`
	QuestionNo      string     `json:"question_no"`
	AnswerSegmentID string     `json:"answer_segment_id"`
	SubmissionID    string     `json:"submission_id"`
	AnonymousCode   string     `json:"anonymous_code"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	AssignedTo      string     `json:"assigned_to,omitempty"`
	ReturnReason    string     `json:"return_reason,omitempty"`
	GradeRound      string     `json:"grade_round"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	Revision        int64      `json:"revision"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type GraderAnswerArtifact struct {
	AnswerSegmentID string `json:"answer_segment_id"`
	Source          string `json:"source,omitempty"`
	Status          string `json:"status"`
	SegmentImageURL string `json:"segment_image_url"`
}

type GraderClaim struct {
	OwnerID  string `json:"owner_id,omitempty"`
	State    string `json:"state"`
	CanRenew bool   `json:"can_renew"`
}

type GraderSubjectToolHints struct {
	SubjectCode          assessment.SubjectCode    `json:"subject_code"`
	ArchetypeCode        string                    `json:"archetype_code"`
	AllowedEvidenceTypes []assessment.EvidenceType `json:"allowed_evidence_types"`
	ParserPolicy         map[string]any            `json:"parser_policy"`
	EvidencePolicy       map[string]any            `json:"evidence_policy"`
	ResponseSchema       map[string]any            `json:"response_schema"`
}

// GraderTaskContext 序列化为与 GET /review-tasks/{id}/context 相同的投影。
// 它不包含 Gold ID、参考分数、来源提交、原始 AI 材料或原始页面 URL。
type GraderTaskContext struct {
	Task             GraderTask                      `json:"task"`
	ExpectedRevision int64                           `json:"expected_revision"`
	QuestionSnapshot assessment.ExamQuestionSnapshot `json:"question_snapshot"`
	Question         paper.Question                  `json:"question"`
	AnswerArtifact   GraderAnswerArtifact            `json:"answer_artifact"`
	FrozenRubric     paper.Rubric                    `json:"frozen_rubric"`
	AICandidates     []any                           `json:"ai_candidates"`
	ScoringEvidence  []assessment.ScoringEvidence    `json:"scoring_evidence"`
	Claim            GraderClaim                     `json:"claim"`
	Draft            any                             `json:"draft"`
	SubjectToolHints GraderSubjectToolHints          `json:"subject_tool_hints"`
}

// GraderImageSource 仅供服务器使用。普通分段图像 handler 可使用 SourceURL 代理既有的已认证答案分段图像响应；
// 绝不能将此值序列化给阅卷人员。
type GraderImageSource struct {
	SourceURL       string `json:"-"`
	AnswerSegmentID string `json:"-"`
}

func (s *Service) GetGraderTaskContext(ctx context.Context, tenantID, taskID, graderID string) (GraderTaskContext, bool, error) {
	task, handled, err := s.GetTaskForGrader(ctx, tenantID, taskID, graderID)
	if !handled || err != nil {
		return GraderTaskContext{}, handled, err
	}
	if s.contexts == nil {
		return GraderTaskContext{}, true, ErrContextUnavailable
	}
	snapshot, err := s.contexts.GetQuestionSnapshot(ctx, tenantID, task.ExamID, task.QuestionID)
	if err != nil {
		return GraderTaskContext{}, true, err
	}
	if snapshot.ID == "" || snapshot.ID != task.SnapshotID {
		return GraderTaskContext{}, true, ErrContextUnavailable
	}
	rubric, err := frozenRubric(snapshot, task)
	if err != nil {
		return GraderTaskContext{}, true, err
	}
	answerSegmentID := opaqueID(task.ID, "answer-artifact")
	graderTask := PublicTask(task, tenantID)
	question := paper.Question{ID: task.QuestionID, TenantID: tenantID, ExamID: task.ExamID,
		QuestionNo: task.QuestionNo, QuestionType: snapshot.ArchetypeCode, Score: task.MaxScore,
		KnowledgePoints: []string{}, Status: "ready"}
	return GraderTaskContext{
		Task: graderTask, ExpectedRevision: task.Revision, QuestionSnapshot: safeSnapshot(snapshot), Question: question,
		AnswerArtifact: GraderAnswerArtifact{AnswerSegmentID: answerSegmentID, Source: "answer_image", Status: "ready", SegmentImageURL: task.AnswerImageURL},
		FrozenRubric:   rubric, AICandidates: []any{}, ScoringEvidence: []assessment.ScoringEvidence{},
		// Seed 任务不属于普通领取租约或草稿 API。
		// 因此正常工作台不能尝试通过 review 存储为这个合成任务续租或自动保存。
		Claim: GraderClaim{OwnerID: task.AssignedTo, State: "claimed", CanRenew: false}, Draft: nil,
		SubjectToolHints: GraderSubjectToolHints{SubjectCode: snapshot.SubjectCode, ArchetypeCode: snapshot.ArchetypeCode,
			AllowedEvidenceTypes: append([]assessment.EvidenceType(nil), snapshot.AllowedEvidenceTypes...),
			ParserPolicy:         objectField(snapshot.ProfileSnapshot, "parser_policy"),
			EvidencePolicy:       objectField(snapshot.ProfileSnapshot, "evidence_policy"),
			ResponseSchema:       objectField(snapshot.ArchetypeSnapshot, "response_schema")},
	}, true, nil
}

// PublicTask 为既有复核客户端提供正常任务结构，同时把全部 Gold 来源链接替换为确定的不透明 ID。
func PublicTask(task Task, tenantID string) GraderTask {
	return GraderTask{ID: task.ID, TenantID: tenantID, ExamID: task.ExamID, QuestionID: task.QuestionID,
		QuestionNo: task.QuestionNo, AnswerSegmentID: opaqueID(task.ID, "answer-artifact"), SubmissionID: opaqueID(task.ID, "submission"),
		AnonymousCode: task.AnonymousCode, Source: task.Source, Status: task.Status, Priority: task.Priority,
		AssignedTo: task.AssignedTo, GradeRound: "first", Revision: task.Revision,
		CreatedBy: opaqueID(task.ID, "creator"), CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
}

func (s *Service) GetGraderImageSource(ctx context.Context, tenantID, taskID, graderID string) (GraderImageSource, bool, error) {
	task, handled, err := s.GetTaskForGrader(ctx, tenantID, taskID, graderID)
	if !handled || err != nil {
		return GraderImageSource{}, handled, err
	}
	if task.SourceImageURL == "" {
		return GraderImageSource{}, true, ErrContextUnavailable
	}
	segmentID := answerSegmentID(task.SourceImageURL)
	if segmentID == "" {
		return GraderImageSource{}, true, ErrContextUnavailable
	}
	return GraderImageSource{SourceURL: task.SourceImageURL, AnswerSegmentID: segmentID}, true, nil
}

func frozenRubric(snapshot assessment.ExamQuestionSnapshot, task Task) (paper.Rubric, error) {
	payload, err := json.Marshal(snapshot.RubricSnapshot)
	if err != nil {
		return paper.Rubric{}, fmt.Errorf("encode frozen rubric: %w", err)
	}
	var rubric paper.Rubric
	if err := json.Unmarshal(payload, &rubric); err != nil {
		return paper.Rubric{}, fmt.Errorf("decode frozen rubric: %w", err)
	}
	if rubric.ID == "" {
		rubric.ID = "snapshot-" + snapshot.ID
	}
	if rubric.QuestionID == "" {
		rubric.QuestionID = task.QuestionID
	}
	if rubric.Version == "" {
		rubric.Version = strconv.Itoa(snapshot.SnapshotVersion)
	}
	if rubric.Status == "" {
		rubric.Status = "frozen"
	}
	if rubric.MaxScore == 0 {
		rubric.MaxScore = task.MaxScore
	}
	if rubric.Points == nil {
		rubric.Points = []paper.RubricPoint{}
	}
	if rubric.Deductions == nil {
		rubric.Deductions = []any{}
	}
	if rubric.Examples == nil {
		rubric.Examples = []any{}
	}
	return rubric, nil
}

func safeSnapshot(value assessment.ExamQuestionSnapshot) assessment.ExamQuestionSnapshot {
	value.TenantID = ""
	value.ProfileSnapshot = cloneObject(value.ProfileSnapshot)
	value.ArchetypeSnapshot = cloneObject(value.ArchetypeSnapshot)
	value.RubricSnapshot = cloneObject(value.RubricSnapshot)
	value.AllowedEvidenceTypes = append([]assessment.EvidenceType(nil), value.AllowedEvidenceTypes...)
	return value
}

func objectField(source map[string]any, key string) map[string]any {
	value, _ := source[key].(map[string]any)
	return cloneObject(value)
}

func opaqueID(taskID, scope string) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + taskID))
	value, _ := uuid.FromBytes(digest[:16])
	return value.String()
}

func answerSegmentID(sourceURL string) string {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] != "image" {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-2])
}
