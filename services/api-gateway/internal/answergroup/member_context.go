package answergroup

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/taskmaterials"
	"errors"
)

type MemberContext struct {
	GroupID          string              `json:"group_id"`
	Member           Member              `json:"member"`
	ExpectedRevision int64               `json:"expected_revision"`
	Question         paper.Question      `json:"question"`
	AnswerText       string              `json:"answer_text"`
	SegmentImageURL  string              `json:"segment_image_url"`
	ManualReview     *MemberManualReview `json:"manual_review,omitempty"`
}

// MemberManualReview 仅定位实际存在的单份复核任务，不把样本拒绝当作已新建或已完成阅卷。
type MemberManualReview struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	Assigned bool   `json:"assigned"`
}
type MemberContextSource interface {
	GetMemberContext(context.Context, string, string, string) (MemberContext, error)
}

// GetMemberContext 只读取该管理分组的真实成员。当前答案与构建时的表示不一致时拒绝复核，
// 避免把后来编辑的内容当作先前相似度和候选评分的证据。
func (s *PostgresStore) GetMemberContext(ctx context.Context, tenantID, groupID, segmentID string) (MemberContext, error) {
	group, err := s.Get(ctx, tenantID, groupID)
	if err != nil {
		return MemberContext{}, err
	}
	var member *Member
	for i := range group.Members {
		if group.Members[i].SegmentID == segmentID {
			member = &group.Members[i]
			break
		}
	}
	if member == nil {
		return MemberContext{}, ErrNotFound
	}
	var text string
	err = s.db.QueryRowContext(ctx, `SELECT a.answer_text FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.exam_id=$4::uuid AND sub.deleted_at IS NULL
JOIN LATERAL (SELECT answer_text FROM answer_segment_answer WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1) a ON true
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.submission_id=$3::uuid AND seg.question_id=$5::uuid AND seg.deleted_at IS NULL`, tenantID, segmentID, member.SubmissionID, group.ExamID, group.QuestionID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberContext{}, ErrNotFound
	}
	if err != nil {
		return MemberContext{}, err
	}
	if s.provider.Version() != group.RepresentationVersion || s.provider.Represent(text).Hash != member.RepresentationHash {
		return MemberContext{}, ErrRevisionConflict
	}
	question, err := (taskmaterials.PostgresReader{DB: s.db}).QuestionForSnapshot(ctx, tenantID, group.ExamID, group.QuestionID, group.ExamQuestionSnapshotID)
	if err != nil {
		return MemberContext{}, err
	}
	revision := int64(0)
	if group.Decision != nil {
		revision = group.Decision.Revision
	}
	manual, err := s.memberManualReview(ctx, tenantID, group, *member)
	if err != nil {
		return MemberContext{}, err
	}
	return MemberContext{GroupID: groupID, Member: *member, ExpectedRevision: revision, Question: question, AnswerText: text, SegmentImageURL: "/api/v1/answer-groups/" + groupID + "/samples/" + segmentID + "/segment-image", ManualReview: manual}, nil
}

func (s *PostgresStore) memberManualReview(ctx context.Context, tenantID string, group Group, member Member) (*MemberManualReview, error) {
	var task MemberManualReview
	// 优先指向仍在处理的真实任务；只附带状态与是否已分派，不把教师身份或学生资料复制到分组材料。
	err := s.db.QueryRowContext(ctx, `SELECT task.id::text,task.status,task.assigned_to IS NOT NULL
FROM review_task task
WHERE task.tenant_id=$1::uuid AND task.exam_id=$2::uuid AND task.question_id=$3::uuid
  AND task.submission_id=$4::uuid AND task.answer_segment_id=$5::uuid AND task.deleted_at IS NULL
ORDER BY (task.status IN ('pending','assigned','in_progress','returned')) DESC,task.updated_at DESC,task.id DESC LIMIT 1`,
		tenantID, group.ExamID, group.QuestionID, member.SubmissionID, member.SegmentID).Scan(&task.TaskID, &task.Status, &task.Assigned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}
