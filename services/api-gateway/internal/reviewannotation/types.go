package reviewannotation

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound         = errors.New("review annotation resource not found")
	ErrInvalidInput     = errors.New("invalid review annotation input")
	ErrRevisionConflict = errors.New("review annotation revision conflict")
	ErrShortcutConflict = errors.New("review comment template shortcut conflict")
)

type Visibility string

const (
	VisibilityPrivate             Visibility = "private"
	VisibilityStudentAfterPublish Visibility = "student_after_publish"
)

func (v Visibility) Valid() bool {
	return v == VisibilityPrivate || v == VisibilityStudentAfterPublish
}

type AnnotationType string

const (
	AnnotationNote      AnnotationType = "note"
	AnnotationHighlight AnnotationType = "highlight"
	AnnotationRectangle AnnotationType = "rectangle"
	AnnotationFreehand  AnnotationType = "freehand"
)

func (kind AnnotationType) Valid() bool {
	switch kind {
	case AnnotationNote, AnnotationHighlight, AnnotationRectangle, AnnotationFreehand:
		return true
	default:
		return false
	}
}

// 坐标相对于方向校正后的原图，并归一化到 [0,1]；显示尺寸变化时无需保存像素坐标。
type ImageGeometry struct {
	CoordinateSpace string  `json:"coordinate_space"`
	X               float64 `json:"x"`
	Y               float64 `json:"y"`
	Width           float64 `json:"width"`
	Height          float64 `json:"height"`
}

const CanonicalImageNormalized = "canonical_image_normalized"

// 坐标必须属于校正后的原图比例；前端显示尺寸变化时仍能落回同一位置。
func (geometry ImageGeometry) Valid() bool {
	return geometry.CoordinateSpace == CanonicalImageNormalized &&
		geometry.X >= 0 && geometry.X <= 1 && geometry.Y >= 0 && geometry.Y <= 1 &&
		geometry.Width >= 0 && geometry.Height >= 0 &&
		geometry.X+geometry.Width <= 1 && geometry.Y+geometry.Height <= 1
}

type Annotation struct {
	ID               string         `json:"id"`
	TenantID         string         `json:"tenant_id"`
	ReviewTaskID     string         `json:"review_task_id"`
	AnswerSegmentID  string         `json:"answer_segment_id"`
	SubmissionPageID string         `json:"submission_page_id"`
	Type             AnnotationType `json:"type"`
	Geometry         ImageGeometry  `json:"geometry"`
	Payload          map[string]any `json:"payload"`
	Content          string         `json:"content"`
	Visibility       Visibility     `json:"visibility"`
	Revision         int64          `json:"revision"`
	CreatedBy        string         `json:"created_by"`
	UpdatedBy        string         `json:"updated_by"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// 学生 DTO 刻意不包含租户、操作者、版本、payload 和可见性字段；
// 私有批注无法由该类型表达，转换前必须先完成可见性过滤。
type StudentAnnotation struct {
	ID               string         `json:"id"`
	AnswerSegmentID  string         `json:"answer_segment_id"`
	SubmissionPageID string         `json:"submission_page_id"`
	Type             AnnotationType `json:"type"`
	Geometry         ImageGeometry  `json:"geometry"`
	Content          string         `json:"content"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// 先按公开可见性过滤，再转换为不含租户、操作者、版本和 payload 的学生 DTO。
func StudentAnnotations(items []Annotation) []StudentAnnotation {
	out := make([]StudentAnnotation, 0, len(items))
	for _, item := range items {
		if item.Visibility != VisibilityStudentAfterPublish {
			continue
		}
		out = append(out, StudentAnnotation{
			ID: item.ID, AnswerSegmentID: item.AnswerSegmentID,
			SubmissionPageID: item.SubmissionPageID, Type: item.Type,
			Geometry: item.Geometry, Content: item.Content,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return out
}

type CreateAnnotationInput struct {
	Type       AnnotationType `json:"type"`
	Geometry   ImageGeometry  `json:"geometry"`
	Payload    map[string]any `json:"payload"`
	Content    string         `json:"content"`
	Visibility Visibility     `json:"visibility"`
}

type UpdateAnnotationInput struct {
	Type             AnnotationType `json:"type"`
	Geometry         ImageGeometry  `json:"geometry"`
	Payload          map[string]any `json:"payload"`
	Content          string         `json:"content"`
	Visibility       Visibility     `json:"visibility"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type DeleteInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type CommentTemplate struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	OwnerID    string    `json:"owner_id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Shortcut   string    `json:"shortcut"`
	UsageCount int64     `json:"usage_count"`
	Revision   int64     `json:"revision"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateCommentTemplateInput struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Shortcut string `json:"shortcut"`
}

type UpdateCommentTemplateInput struct {
	Title            string `json:"title"`
	Content          string `json:"content"`
	Shortcut         string `json:"shortcut"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Store interface {
	CreateAnnotation(context.Context, string, string, string, CreateAnnotationInput) (Annotation, error)
	ListAnnotations(context.Context, string, string) ([]Annotation, error)
	GetAnnotation(context.Context, string, string) (Annotation, error)
	UpdateAnnotation(context.Context, string, string, string, UpdateAnnotationInput) (Annotation, error)
	DeleteAnnotation(context.Context, string, string, string, int64) error
	ListStudentAnnotations(context.Context, string, string) ([]StudentAnnotation, error)
	// 学生查询必须沿当前不可变发布版本解析答案，并使用学生和题目范围；
	// 不接收调用方提交 ID，避免枚举他人批注。
	ListStudentQuestionAnnotations(context.Context, string, string, string, string) ([]StudentAnnotation, error)

	CreateCommentTemplate(context.Context, string, string, CreateCommentTemplateInput) (CommentTemplate, error)
	ListCommentTemplates(context.Context, string, string) ([]CommentTemplate, error)
	GetCommentTemplate(context.Context, string, string, string) (CommentTemplate, error)
	UpdateCommentTemplate(context.Context, string, string, string, UpdateCommentTemplateInput) (CommentTemplate, error)
	DeleteCommentTemplate(context.Context, string, string, string, int64) error
	UseCommentTemplate(context.Context, string, string, string) (CommentTemplate, error)
}

// 缺省坐标系和可见性只在输入层补齐；存储层仍会校验最终值，避免写入未定义坐标。
func normalizeAnnotationInput(input CreateAnnotationInput) CreateAnnotationInput {
	input.Content = strings.TrimSpace(input.Content)
	if input.Geometry.CoordinateSpace == "" {
		input.Geometry.CoordinateSpace = CanonicalImageNormalized
	}
	if input.Visibility == "" {
		input.Visibility = VisibilityPrivate
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	return input
}

func validateAnnotationInput(input CreateAnnotationInput) error {
	if !input.Type.Valid() || !input.Geometry.Valid() || !input.Visibility.Valid() || len(input.Content) > 4000 {
		return ErrInvalidInput
	}
	return nil
}

// 快捷键统一去空格并转小写，创建、更新和使用必须共享同一规范化规则。
func normalizeTemplate(title, content, shortcut string) (string, string, string) {
	return strings.TrimSpace(title), strings.TrimSpace(content), strings.ToLower(strings.TrimSpace(shortcut))
}

func validateTemplate(title, content, shortcut string) error {
	if title == "" || len(title) > 120 || content == "" || len(content) > 4000 || len(shortcut) == 0 || len(shortcut) > 32 {
		return ErrInvalidInput
	}
	for index, value := range shortcut {
		valid := value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || (index > 0 && (value == '.' || value == '_' || value == '-'))
		if !valid {
			return ErrInvalidInput
		}
	}
	return nil
}

// 返回新的顶层 map，避免调用方修改批注时直接改动存储中的 map；嵌套值按输入类型原样保留。
func clonePayload(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
