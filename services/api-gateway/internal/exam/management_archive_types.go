package exam

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var ErrArchiveBlocked = errors.New("whole exam archive is blocked")

// ManagementArchiveMetadata 与学科业务阶段分开保存；恢复归档不会猜测或改写原有阶段。
type ManagementArchiveMetadata struct {
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	ArchivedBy    string     `json:"archived_by,omitempty"`
	ArchiveReason string     `json:"archive_reason,omitempty"`
}

type ManagementArchiveBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	ExamID  string `json:"exam_id,omitempty"`
	Subject string `json:"subject,omitempty"`
}

type ManagementArchiveCheck struct {
	Allowed  bool                       `json:"allowed"`
	Blockers []ManagementArchiveBlocker `json:"blockers"`
}

// ManagementArchiveInput 的命令键标识一次操作。相同键重放原结果，不再执行归档或恢复。
type ManagementArchiveInput struct {
	ExpectedRevision         int64            `json:"expected_revision"`
	ExpectedSubjectRevisions map[string]int64 `json:"expected_subject_revisions,omitempty"`
	CommandID                string           `json:"command_id"`
	Reason                   string           `json:"reason,omitempty"`
}

type ManagementArchiveStore interface {
	CheckManagementArchive(context.Context, auth.AccessScope, string, string) (ManagementArchiveCheck, error)
	ArchiveManagementExam(context.Context, auth.AccessScope, string, string, string, ManagementArchiveInput) (ManagementExam, error)
	RestoreManagementExam(context.Context, auth.AccessScope, string, string, string, ManagementArchiveInput) (ManagementExam, error)
	RecoverManagementArchiveCommand(context.Context, auth.AccessScope, string, string) (ManagementArchiveCommandResult, error)
}

type ManagementArchiveCommandResult struct {
	CommandID string          `json:"command_id"`
	Status    string          `json:"status"`
	Exam      *ManagementExam `json:"exam,omitempty"`
}

type managementArchiveCommandRecord struct {
	Hash   string
	Result ManagementExam
}
