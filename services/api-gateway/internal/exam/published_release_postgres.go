package exam

import "context"

// PublishedExamIDs 返回学生端当前生效版本已正式发布的考试，口径与考试管理的“已发布”一致。
// 学生端发布只写 score_release 与 score_release_current，不改 exam.status，调用方不能只按状态判断。
// examIDs 须来自已按访问范围筛选的考试，这里只再加租户条件；不在结果中的考试表示没有学生端正式成绩。
func (s *PostgresStore) PublishedExamIDs(ctx context.Context, tenantID string, examIDs []string) (map[string]bool, error) {
	published := make(map[string]bool, len(examIDs))
	if len(examIDs) == 0 {
		return published, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sc.exam_id::text
FROM score_release_current sc
JOIN score_release sr ON sr.tenant_id=sc.tenant_id AND sr.id=sc.release_id AND sr.exam_id=sc.exam_id
WHERE sc.tenant_id=$1::uuid AND sc.exam_id = ANY($2::text[]::uuid[])
  AND sr.status='published' AND sr.published_at IS NOT NULL`, tenantID, examIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var examID string
		if err := rows.Scan(&examID); err != nil {
			return nil, err
		}
		published[examID] = true
	}
	return published, rows.Err()
}
