package auth

import "context"

// CanReadSubmittedReviewTask 每次回看都核验当前负责人和本人正式评分；改派、任务或评分软删除后不再放行。
// 不派生考试、提交或文件 ID，原图与具体阅卷路由仍继续执行各自的权限检查。
func (s *PostgresStore) CanReadSubmittedReviewTask(ctx context.Context, tenantID, taskID, reviewerID string) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM review_task t
  JOIN human_grade g ON g.tenant_id=t.tenant_id AND g.review_task_id=t.id
  WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.assigned_to=$3::uuid
    AND t.status IN ('submitted','completed') AND t.deleted_at IS NULL
    AND g.reviewer_id=$3::uuid AND g.deleted_at IS NULL
)`, tenantID, taskID, reviewerID).Scan(&allowed)
	return allowed, err
}
