package reviewbookmark

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var (
	ErrNotFound        = errors.New("review bookmark not found")
	ErrForbidden       = errors.New("review bookmark forbidden")
	ErrInvalid         = errors.New("invalid review bookmark input")
	ErrArchived        = errors.New("exam archived")
	ErrEvidenceChanged = errors.New("review bookmark evidence changed")
)

type RevisionConflict struct{ Revision int64 }

func (e *RevisionConflict) Error() string { return "review bookmark revision conflict" }

// Source 只在服务内部使用；两个真实来源标识不得序列化到公开清单。
// Provider 对每次读取重新校验实际权限、同校关系、当前负责人及完成任务的本人意见。
type Source struct {
	TaskID             string
	ReviewTaskID       string
	SeedTaskID         string
	AnswerSegmentID    string
	MaxScore           float64
	ExamID             string
	ExamName           string
	QuestionID         string
	QuestionNo         string
	Subject            string
	AnonymousCode      string
	State              string
	Archived           bool
	ObservedCropSHA256 string
	ObservedSnapshotID string
}

type SourceProvider interface {
	Resolve(context.Context, auth.User, string) (Source, error)
}

// TransactionalSourceProvider 与书签共用事务；lock=true 时锁住源任务，
// 避免刚核验负责人后，另一事务转派任务而本次仍继续写入。
type TransactionalSourceProvider interface {
	ResolveTx(context.Context, *sql.Tx, auth.User, string, bool) (Source, error)
}

type Record struct {
	ID                 string
	TenantID           string
	OwnerID            string
	TaskID             string
	ReviewTaskID       string
	SeedTaskID         string
	Reason             string
	Revision           int64
	MarkedAt           time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
	ObservedCropSHA256 string
	ObservedSnapshotID string
}

type Bookmark struct {
	ID            *string    `json:"id"`
	TaskID        string     `json:"task_id"`
	Marked        bool       `json:"marked"`
	Reason        string     `json:"reason"`
	Revision      int64      `json:"revision"`
	MarkedAt      *time.Time `json:"marked_at,omitempty"`
	CanEditReason bool       `json:"can_edit_reason"`
}

type PutInput struct {
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type DeleteInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}
type Acknowledgement struct {
	BookmarkID string `json:"bookmark_id"`
	Revision   int64  `json:"revision"`
	Marked     bool   `json:"marked"`
}
type CleanupItem struct {
	BookmarkID string `json:"bookmark_id"`
	Revision   int64  `json:"revision"`
}
type ListItem struct {
	Bookmark
	ExamID        string `json:"exam_id"`
	ExamName      string `json:"exam_name"`
	QuestionID    string `json:"question_id"`
	QuestionNo    string `json:"question_no"`
	Subject       string `json:"subject"`
	AnonymousCode string `json:"anonymous_code"`
	State         string `json:"state"`
}
type ExamOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type QuestionOption struct {
	ID         string `json:"id"`
	QuestionNo string `json:"question_no"`
}
type ListResult struct {
	Items      []ListItem       `json:"items"`
	TotalCount int              `json:"total_count"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Exams      []ExamOption     `json:"exams"`
	Questions  []QuestionOption `json:"questions"`
}
type ListFilter struct {
	ExamID, QuestionID, Subject, State, Q, Cursor string
	Limit                                         int
}

type Store interface {
	State(context.Context, auth.User, string) (Bookmark, error)
	Put(context.Context, auth.User, string, PutInput) (Acknowledgement, error)
	Delete(context.Context, auth.User, string, DeleteInput) (Acknowledgement, error)
	List(context.Context, auth.User, ListFilter) (ListResult, error)
	Cleanup(context.Context, auth.User) ([]CleanupItem, error)
	GetByID(context.Context, auth.User, string) (Record, error)
}
