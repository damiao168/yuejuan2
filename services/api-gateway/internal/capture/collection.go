package capture

import (
	"context"
	"errors"
	"time"
)

var ErrFileHasGrades = errors.New("capture file already has grading results")

// CollectionSummary 只反映导入事实；未生成识别文字或得分不会降低导入状态。
type CollectionSummary struct {
	ExamID          string            `json:"exam_id"`
	ExamStatus      string            `json:"exam_status"`
	Revision        int               `json:"revision"`
	Closed          bool              `json:"closed"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
	CompletedBy     string            `json:"completed_by,omitempty"`
	PagesPerStudent int               `json:"pages_per_student"`
	RosterCount     int               `json:"roster_count"`
	RequiredCount   int               `json:"required_count"`
	ReadyCount      int               `json:"ready_count"`
	AbsentCount     int               `json:"absent_count"`
	PendingCount    int               `json:"pending_count"`
	CanComplete     bool              `json:"can_complete"`
	Blockers        []string          `json:"blockers"`
	Entries         []CollectionEntry `json:"entries"`
	Issues          []CollectionIssue `json:"issues"`
}
type CollectionEntry struct {
	StudentID     string `json:"student_id"`
	StudentNo     string `json:"student_no"`
	StudentName   string `json:"student_name"`
	ClassName     string `json:"class_name"`
	Status        string `json:"status"`
	Code          string `json:"code"`
	BatchID       string `json:"batch_id,omitempty"`
	SubmissionID  string `json:"submission_id,omitempty"`
	ExpectedPages int    `json:"expected_pages"`
	ActualPages   int    `json:"actual_pages"`
	Reason        string `json:"reason,omitempty"`
}
type CollectionIssue struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	BatchID      string `json:"batch_id"`
	SubmissionID string `json:"submission_id,omitempty"`
	FileID       string `json:"file_id,omitempty"`
}
type CompleteCollectionInput struct {
	Revision       int    `json:"revision"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason,omitempty"`
}
type ExcludeFileInput struct {
	Reason            string `json:"reason"`
	ReplacementFileID string `json:"replacement_file_id,omitempty"`
}
type CollectionStore interface {
	GetCollectionFileExam(context.Context, string, string) (string, error)
	GetCollectionSummary(context.Context, string, string) (CollectionSummary, error)
	CompleteCollection(context.Context, string, string, string, CompleteCollectionInput) (CollectionSummary, error)
	ExcludeFile(context.Context, string, string, string, ExcludeFileInput) (Batch, error)
}

// 每人只计入一种状态；匿名材料另列，不把“份数”混入应收人数。
func finishCollectionSummary(out *CollectionSummary) {
	out.RosterCount = len(out.Entries)
	for _, entry := range out.Entries {
		switch entry.Status {
		case "absent":
			out.AbsentCount++
		case "ready":
			out.ReadyCount++
		default:
			out.PendingCount++
		}
	}
	out.RequiredCount = out.RosterCount - out.AbsentCount
	if out.RosterCount == 0 {
		out.Blockers = append(out.Blockers, "本场尚无冻结学生名册")
	}
	if out.PagesPerStudent == 0 {
		out.Blockers = append(out.Blockers, "本场尚无已锁定的答题卡模板")
	}
	if out.PendingCount > 0 {
		out.Blockers = append(out.Blockers, "仍有学生未完成答卷导入，请处理待完成列表")
	}
	if len(out.Issues) > 0 {
		out.Blockers = append(out.Blockers, "仍有未处理的文件、答卷或上传任务")
	}
	out.CanComplete = !out.Closed && (out.ExamStatus == "collecting" || out.ExamStatus == "grading" || out.ExamStatus == "reviewing") && len(out.Blockers) == 0
}
