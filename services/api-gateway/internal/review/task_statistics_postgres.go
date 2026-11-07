package review

import (
	"context"
	"strings"
)

// 每个样本只保留一份正式成绩。已开始但尚未最终合分的双评，不进入管理员正确率分母。
func (s *PostgresStore) reviewStatistics(ctx context.Context, tenantID string, filter ListFilter) (*ReviewStatistics, error) {
	scopeMode := filter.ScopeMode
	if scopeMode == "" {
		scopeMode = "tenant"
	}
	scope := "exam"
	if filter.AssignedTo != "" {
		scope = "personal"
	}
	// 学校管理员统计仅限本校；只有普通受派阅卷人员可读取本人外校正式意见。
	query := `WITH readable_exam AS (
 SELECT e.id FROM exam e WHERE e.tenant_id=$1::uuid AND e.id::text=$2 AND e.deleted_at IS NULL
 AND ($4='' OR e.subject=$4)
 AND ($5 IN ('platform','tenant')
   OR ($5='school' AND e.school_id::text=ANY(string_to_array(NULLIF($6,''),',')))
   OR ($5='class' AND e.id::text=ANY(string_to_array(NULLIF($7,''),',')))
   OR ($5='assigned' AND $8<>'' AND $8=$9 AND EXISTS (
     SELECT 1 FROM review_task allowed WHERE allowed.tenant_id=e.tenant_id AND allowed.exam_id=e.id
       AND allowed.assigned_to::text=$9 AND allowed.deleted_at IS NULL)))
), samples AS (`
	if scope == "personal" {
		// 本人历史意见保留独立评分事实；不能借此读取另一教师的分数或尚未公开的最终合分。
		query += `SELECT DISTINCT ON (t.answer_segment_id) t.answer_segment_id,t.question_id,COALESCE(q.question_no,t.question_no) AS question_no,
 g.score::float8 AS score,g.max_score::float8 AS maximum
 FROM human_grade g JOIN review_task t ON t.tenant_id=g.tenant_id AND t.id=g.review_task_id
 JOIN readable_exam e ON e.id=t.exam_id
 JOIN question q ON q.tenant_id=t.tenant_id AND q.id=t.question_id AND q.deleted_at IS NULL
 WHERE g.tenant_id=$1::uuid AND g.reviewer_id::text=$8 AND t.assigned_to::text=$8
 AND t.status IN ('submitted','completed') AND t.deleted_at IS NULL AND g.deleted_at IS NULL
 AND ($3='' OR t.question_id::text=$3)
 ORDER BY t.answer_segment_id,g.created_at DESC,g.id DESC`
	} else {
		query += `SELECT DISTINCT ON (g.answer_segment_id) g.answer_segment_id,g.question_id,q.question_no,
 g.score::float8 AS score,g.max_score::float8 AS maximum
 FROM question_grade g JOIN readable_exam e ON e.id=g.exam_id
 JOIN question q ON q.tenant_id=g.tenant_id AND q.id=g.question_id AND q.deleted_at IS NULL
 WHERE g.tenant_id=$1::uuid AND g.is_current AND g.status='confirmed' AND g.deleted_at IS NULL
 AND ($3='' OR g.question_id::text=$3)
 ORDER BY g.answer_segment_id,g.created_at DESC,g.id DESC`
	}
	query += `) SELECT answer_segment_id::text,question_id::text,question_no,score,maximum FROM samples`
	rows, err := s.db.QueryContext(ctx, query, tenantID, filter.ExamID, filter.QuestionID, filter.Subject,
		scopeMode, strings.Join(filter.ScopeSchoolIDs, ","), strings.Join(filter.ScopeExamIDs, ","), filter.AssignedTo, filter.ScopeActorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := []reviewStatisticSample{}
	for rows.Next() {
		var sample reviewStatisticSample
		if err := rows.Scan(&sample.segmentID, &sample.questionID, &sample.questionNo, &sample.score, &sample.maximum); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildReviewStatistics(filter.ExamID, scope, samples), nil
}
