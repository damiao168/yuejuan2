package exam

import (
	"context"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// ManagementExam 是考试列表中的整场对象；Exams 仅含调用者有权读取的学科。
// ScopeComplete=false 时不能把局部列表当成整场执行编辑或归档。
type ManagementExam struct {
	ManagementArchiveMetadata
	ID                  string                     `json:"id"`
	Kind                string                     `json:"kind"`
	SchoolID            string                     `json:"school_id"`
	GradeID             string                     `json:"grade_id"`
	Name                string                     `json:"name"`
	ExamType            string                     `json:"exam_type"`
	Revision            int64                      `json:"revision"`
	CreatedBy           string                     `json:"created_by"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
	ClassIDs            []string                   `json:"class_ids"`
	DefaultClassIDs     []string                   `json:"default_class_ids"`
	InheritedSubjectIDs []string                   `json:"inherited_subject_ids"`
	ArchivedSubjectIDs  []string                   `json:"archived_subject_ids"`
	LegacyArchived      bool                       `json:"legacy_archived"`
	Exams               []Exam                     `json:"exams"`
	Progress            ManagementProgress         `json:"progress"`
	ScopeComplete       bool                       `json:"scope_complete"`
	CanManage           bool                       `json:"can_manage"`
	CanEdit             bool                       `json:"can_edit"`
	CanArchive          bool                       `json:"can_archive"`
	CanRestore          bool                       `json:"can_restore"`
	ArchiveBlockers     []ManagementArchiveBlocker `json:"archive_blockers,omitempty"`
	// 仓库在列表读取事务内已检查归档时，处理器不能再逐场启动新快照和重复扫描任务。
	archiveCheckResolved bool
}

type ManagementProgress struct {
	Status         string         `json:"status"`
	Label          string         `json:"label"`
	TotalCount     int            `json:"total_count"`
	ReadyCount     int            `json:"ready_count"`
	PublishedCount int            `json:"published_count"`
	Counts         map[string]int `json:"counts"`
	// 内存测试仓库没有确认/发布服务；false 时计数不能解释为已核实的业务完成率。
	FactsAvailable bool `json:"facts_available"`
}

type ManagementListFilter struct {
	SchoolID string
	GradeID  string
	Search   string
	ExamType string
	Status   string
	Archive  string
	Limit    int
	CursorAt time.Time
	CursorID string
}

type ManagementUpdateInput struct {
	Name                     *string          `json:"name,omitempty"`
	ExamType                 *string          `json:"exam_type,omitempty"`
	ClassIDs                 *[]string        `json:"class_ids,omitempty"`
	ExpectedRevision         int64            `json:"expected_revision"`
	ExpectedSubjectRevisions map[string]int64 `json:"expected_subject_revisions,omitempty"`
}

type ManagementStore interface {
	ListManagementExams(context.Context, auth.AccessScope, ManagementListFilter) ([]ManagementExam, error)
	GetManagementExam(context.Context, auth.AccessScope, string, string) (ManagementExam, error)
	UpdateManagementExam(context.Context, auth.AccessScope, string, string, ManagementUpdateInput) (ManagementExam, error)
}

type ManagementArchiveBulkChecker interface {
	CheckManagementArchives(context.Context, auth.AccessScope, []ManagementExam) (map[string]ManagementArchiveCheck, error)
}

type managementSubjectFacts struct {
	Ready     bool
	Published bool
	Available bool
}
