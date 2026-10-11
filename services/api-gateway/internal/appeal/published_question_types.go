package appeal

import (
	"context"
	"errors"
	"time"
)

// PublishedQuestionAppeal 是独立版本化的申诉记录。
// 旧 Appeal 模型保留以兼容历史业务数据，此记录则绑定 Score Release 快照；该类型不将可变最终成绩作为权威分数。
type PublishedQuestionAppeal struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	ExamID   string `json:"exam_id"`
	// 仅在教职工已获授权的任务投影中补充当前考试摘要；不参与冻结成绩或申诉版本校验。
	ExamName             string                       `json:"exam_name,omitempty"`
	ExamSubject          string                       `json:"exam_subject,omitempty"`
	StudentID            string                       `json:"student_id,omitempty"`
	SubmissionID         string                       `json:"submission_id"`
	SourceReleaseID      string                       `json:"source_release_id"`
	SourceReleaseVersion int                          `json:"source_release_version"`
	QuestionID           string                       `json:"question_id"`
	QuestionNo           string                       `json:"question_no"`
	SourceFinalGradeID   string                       `json:"-"`
	SourceScore          float64                      `json:"source_score"`
	SourceMaxScore       float64                      `json:"source_max_score"`
	ReasonCode           string                       `json:"reason_code"`
	Reason               string                       `json:"reason"`
	SelectedRegion       map[string]any               `json:"selected_region,omitempty"`
	Status               string                       `json:"status"`
	AssignedTo           string                       `json:"assigned_to,omitempty"`
	ReviewOpinion        *QuestionAppealReviewOpinion `json:"review_opinion,omitempty"`
	Decision             string                       `json:"decision,omitempty"`
	PublicResponse       string                       `json:"public_response,omitempty"`
	PrivateNote          string                       `json:"-"`
	RegradeJobID         string                       `json:"regrade_job_id,omitempty"`
	NewReleaseID         string                       `json:"new_release_id,omitempty"`
	// 版本来自关联的已发布成绩；仅学生公开投影使用，不能据此推断它仍是当前生效版本。
	NewReleaseVersion int        `json:"-"`
	ReviewedScore     *float64   `json:"-"`
	ReviewedMaxScore  *float64   `json:"-"`
	CreatedBy         string     `json:"created_by,omitempty"`
	DecidedBy         string     `json:"decided_by,omitempty"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Revision          int64      `json:"revision"`
}

type QuestionAppealExamSummary struct {
	Name    string
	Subject string
}

// QuestionAppealExamSummaryReader 只读取已授权申请所指向的考试名称与学科，不返回目录、班级或学生身份。
// 调用者必须先检查申请的租户及分派权限，不能使用请求中的任意考试编号调用它。
type QuestionAppealExamSummaryReader interface {
	ReadQuestionAppealExamSummary(context.Context, string, string) (QuestionAppealExamSummary, error)
}

// StudentQuestionAppeal 比 PublishedQuestionAppeal 的字段更少，使学生查询结果时无法获得教职工身份、私有讨论或重评实现细节。
type StudentQuestionAppeal struct {
	ID                   string         `json:"id"`
	ExamID               string         `json:"exam_id"`
	SourceReleaseID      string         `json:"source_release_id"`
	SourceReleaseVersion int            `json:"source_release_version"`
	QuestionID           string         `json:"question_id"`
	QuestionNo           string         `json:"question_no"`
	SourceScore          float64        `json:"source_score"`
	SourceMaxScore       float64        `json:"source_max_score"`
	ReasonCode           string         `json:"reason_code"`
	Reason               string         `json:"reason"`
	SelectedRegion       map[string]any `json:"selected_region,omitempty"`
	Status               string         `json:"status"`
	Decision             string         `json:"decision,omitempty"`
	PublicResponse       string         `json:"public_response,omitempty"`
	NewReleaseID         string         `json:"new_release_id,omitempty"`
	NewReleaseVersion    int            `json:"new_release_version,omitempty"`
	ReviewedScore        *float64       `json:"reviewed_score,omitempty"`
	ReviewedMaxScore     *float64       `json:"reviewed_max_score,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type PublishedQuestionAppealEvent struct {
	ID        string         `json:"id"`
	AppealID  string         `json:"appeal_id"`
	Type      string         `json:"type"`
	ActorID   string         `json:"actor_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// QuestionAppealReleaseVersion 是面向教职工的审计摘要，提供同一考试的发布历史，不暴露其他学生的分数事实。
type QuestionAppealReleaseVersion struct {
	ID          string     `json:"id"`
	Version     int        `json:"version"`
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// PublishedQuestionAppealContext 提供单条不可变申诉的教职工工作上下文。
// AnswerSegmentID 保持私有，仅由图片代理在教职工授权后使用。
type PublishedQuestionAppealContext struct {
	Appeal           PublishedQuestionAppeal        `json:"appeal"`
	SourceType       string                         `json:"source_type"`
	RubricSnapshot   map[string]any                 `json:"rubric_snapshot,omitempty"`
	ReleaseHistory   []QuestionAppealReleaseVersion `json:"release_history"`
	AnswerSegmentID  string                         `json:"-"`
	AnswerImageReady bool                           `json:"answer_image_ready"`
}

type CreatePublishedQuestionAppealInput struct {
	ExamID          string         `json:"exam_id"`
	SourceReleaseID string         `json:"source_release_id"`
	QuestionID      string         `json:"question_id"`
	ReasonCode      string         `json:"reason_code"`
	Reason          string         `json:"reason"`
	SelectedRegion  map[string]any `json:"selected_region,omitempty"`
}

type StartQuestionAppealReviewInput struct {
	AssignedTo       string `json:"assigned_to"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// QuestionAppealReviewOpinion 只记录独立教师的建议；不给学生公开，也不能替代管理员裁决或已发布成绩。
type QuestionAppealReviewOpinion struct {
	Opinion        string    `json:"opinion"`
	SuggestedScore *float64  `json:"suggested_score,omitempty"`
	Explanation    string    `json:"explanation"`
	SubmittedAt    time.Time `json:"submitted_at"`
}

type SubmitQuestionAppealReviewOpinionInput struct {
	Opinion          string   `json:"opinion"`
	SuggestedScore   *float64 `json:"suggested_score,omitempty"`
	Explanation      string   `json:"explanation"`
	ExpectedRevision int64    `json:"expected_revision"`
}

// DecideQuestionAppealInput 刻意不包含分数字段。申诉成立时只能发起受治理的重评，再由发布工作流具化为后续 Score Release。
type DecideQuestionAppealInput struct {
	Decision         string `json:"decision"`
	PublicResponse   string `json:"public_response"`
	PrivateNote      string `json:"private_note,omitempty"`
	RegradeJobID     string `json:"regrade_job_id,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ResolveQuestionAppealInput struct {
	NewReleaseID     string `json:"new_release_id"`
	PublicResponse   string `json:"public_response,omitempty"`
	PrivateNote      string `json:"private_note,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type QuestionAppealFilter struct {
	ExamID     string
	StudentID  string
	AssignedTo string
	Status     string
}

const (
	QuestionAppealSubmitted            = "submitted"
	QuestionAppealUnderReview          = "under_review"
	QuestionAppealRejected             = "rejected"
	QuestionAppealUpheldPendingRegrade = "upheld_pending_regrade"
	QuestionAppealResolved             = "resolved"

	QuestionAppealDecisionReject       = "reject"
	QuestionAppealDecisionReferRegrade = "refer_regrade"

	ReasonRecognitionError   = "recognition_error"
	ReasonMissingStepCredit  = "missing_step_credit"
	ReasonRubricDisagreement = "rubric_disagreement"
	ReasonCalculationError   = "calculation_error"
	ReasonAnnotationIssue    = "annotation_issue"
	ReasonOther              = "other"
)

var (
	ErrAppealWindowClosed = errors.New("appeal window is closed")
	ErrAppealAlreadyFiled = errors.New("appeal already filed for this released question")
	ErrSourceRelease      = errors.New("appeal source is not a published score release question")
	ErrResolutionRelease  = errors.New("appeal resolution release is invalid")
)

// PublishedQuestionAppealStore 只负责不可变发布的申诉证据，不得更新 score、human_grade 或 final_grade 记录。
type PublishedQuestionAppealStore interface {
	CreatePublishedQuestionAppeal(context.Context, string, string, string, CreatePublishedQuestionAppealInput) (PublishedQuestionAppeal, error)
	GetPublishedQuestionAppeal(context.Context, string, string) (PublishedQuestionAppeal, error)
	GetPublishedQuestionAppealContext(context.Context, string, string) (PublishedQuestionAppealContext, error)
	ListPublishedQuestionAppeals(context.Context, string, QuestionAppealFilter) ([]PublishedQuestionAppeal, error)
	StartPublishedQuestionAppealReview(context.Context, string, string, string, StartQuestionAppealReviewInput) (PublishedQuestionAppeal, error)
	SubmitPublishedQuestionAppealReviewOpinion(context.Context, string, string, string, SubmitQuestionAppealReviewOpinionInput) (PublishedQuestionAppeal, error)
	DecidePublishedQuestionAppeal(context.Context, string, string, string, DecideQuestionAppealInput) (PublishedQuestionAppeal, error)
	ResolvePublishedQuestionAppeal(context.Context, string, string, string, ResolveQuestionAppealInput) (PublishedQuestionAppeal, error)
	ListPublishedQuestionAppealEvents(context.Context, string, string) ([]PublishedQuestionAppealEvent, error)
}
