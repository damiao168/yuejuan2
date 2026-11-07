package dashboard

import (
	"context"
	"database/sql"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// DashboardExamRosterCounts 按冻结名册计人数，“已收”只认名册学生名下的未删除答卷，与收卷页口径一致。
// examIDs 来自已按访问范围筛选的考试列表，这里只再加租户条件；没有名册的考试不出现在结果中。
func (s *PostgresOrganizationSummaryStore) DashboardExamRosterCounts(ctx context.Context, tenantID string, examIDs []string) (map[string]ExamRosterCount, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	result := make(map[string]ExamRosterCount, len(examIDs))
	if len(examIDs) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT roster.exam_id::text, count(*), count(*) FILTER (WHERE roster.submitted)
FROM (
  SELECT c.exam_id, EXISTS (
    SELECT 1 FROM submission x
    WHERE x.tenant_id=c.tenant_id AND x.exam_id=c.exam_id AND x.student_id=c.student_id AND x.deleted_at IS NULL
  ) AS submitted
  FROM exam_candidate_snapshot c
  WHERE c.tenant_id=$1::uuid AND c.exam_id = ANY($2::text[]::uuid[])
) roster
GROUP BY roster.exam_id`, tenantID, examIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var examID string
		var count ExamRosterCount
		if err := rows.Scan(&examID, &count.RosterCount, &count.SubmittedCount); err != nil {
			return nil, err
		}
		result[examID] = count
	}
	return result, rows.Err()
}

// DashboardAppealStatistics 与 GET /api/v1/question-appeals 的管理列表使用同一授权谓词（Management 范围），
// 首页计数不会多于“成绩复核”页可见的记录。待裁决沿用列表 review_opinion 投影的条件：存在复核教师提交意见的事件。
// 不按考试状态收窄，申诉发生在成绩发布之后。
func (s *PostgresOrganizationSummaryStore) DashboardAppealStatistics(ctx context.Context, tenantID string, scope auth.AccessScope) (AppealStatistics, error) {
	if s == nil || s.db == nil {
		return AppealStatistics{}, sql.ErrConnDone
	}
	access := auth.ExamCollectionAccess{Scope: &scope, Management: true}
	accessArgs, err := access.SQLArgs(tenantID)
	if err != nil {
		return AppealStatistics{}, err
	}
	var result AppealStatistics
	err = s.db.QueryRowContext(ctx, `SELECT
  count(*) FILTER (WHERE status='submitted'),
  count(*) FILTER (WHERE status='under_review' AND has_opinion)
FROM (
  SELECT question_appeal.status, EXISTS (
    SELECT 1 FROM question_appeal_event event
    WHERE event.tenant_id=question_appeal.tenant_id AND event.appeal_id=question_appeal.id
      AND event.event_type='review_opinion_submitted'
  ) AS has_opinion
  FROM question_appeal
  WHERE question_appeal.tenant_id=$1::uuid AND question_appeal.status IN ('submitted','under_review')
    AND `+auth.ExamCollectionPredicate("question_appeal.exam_id", 2)+`
) appeals`, append([]any{tenantID}, accessArgs...)...).Scan(&result.PendingAssignmentCount, &result.PendingDecisionCount)
	if err != nil {
		return AppealStatistics{}, err
	}
	return result, nil
}
