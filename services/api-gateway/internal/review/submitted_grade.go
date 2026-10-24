package review

import (
	"context"
)

// SubmittedGradeReader 只读回当前负责人在已提交任务中的本人正式评分，不能用于查看另一个阅卷人的分数。
type SubmittedGradeReader interface {
	GetSubmittedGrade(context.Context, string, string, string) (HumanGrade, error)
}

func (s *MemoryStore) GetSubmittedGrade(_ context.Context, tenantID, taskID, reviewerID string) (HumanGrade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[key(tenantID, taskID)]
	if !exists || task.AssignedTo != reviewerID || !submittedReviewStatus(task.Status) {
		return HumanGrade{}, ErrNotFound
	}
	grades := s.grades[key(tenantID, taskID)]
	for index := len(grades) - 1; index >= 0; index-- {
		if grades[index].ReviewerID == reviewerID {
			return cloneGrade(grades[index]), nil
		}
	}
	return HumanGrade{}, ErrNotFound
}

func (s *PostgresStore) GetSubmittedGrade(ctx context.Context, tenantID, taskID, reviewerID string) (HumanGrade, error) {
	return scanGrade(s.db.QueryRowContext(ctx, `
SELECT g.id::text,g.tenant_id::text,g.review_task_id::text,g.answer_segment_id::text,g.reviewer_id::text,
  g.score::float8,g.max_score::float8,g.rubric_selections,g.comments,g.private_note,g.student_feedback,
  g.reason,g.grade_round,COALESCE(g.ai_grade_id::text,''),g.created_at
FROM human_grade g JOIN review_task t ON t.tenant_id=g.tenant_id AND t.id=g.review_task_id
WHERE g.tenant_id=$1::uuid AND g.review_task_id=$2::uuid AND g.reviewer_id=$3::uuid AND g.deleted_at IS NULL
  AND t.assigned_to=$3::uuid AND t.status IN ('submitted','completed') AND t.deleted_at IS NULL
ORDER BY g.created_at DESC,g.id DESC LIMIT 1`, tenantID, taskID, reviewerID))
}
