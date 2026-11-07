package appeal

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"fmt"
)

func (s *PostgresStore) ListAppeals(ctx context.Context, tenantID string, filter ListFilter) ([]Appeal, error) {
	scopeArgs, err := appealScopeArgs(tenantID, filter.AccessScope)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+appealColumns("a")+`, e.name, e.subject, sg.anonymous_code
FROM appeal a
JOIN exam e ON e.tenant_id = a.tenant_id AND e.id = a.exam_id
JOIN submission_grade sg ON sg.tenant_id = a.tenant_id AND sg.id = a.submission_grade_id
WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
  AND ($2 = '' OR a.exam_id::text = $2)
  AND ($3 = '' OR a.student_id::text = $3)
  AND ($4 = '' OR a.status = $4)
  AND ($5 = '' OR a.assigned_to::text = $5)
  AND ($7 = '' OR a.created_at < $6 OR (a.created_at = $6 AND a.id::text < $7))
  AND `+appealScopePredicate(9)+`
ORDER BY a.created_at DESC, a.id::text DESC
LIMIT NULLIF($8, 0)
`, append([]any{tenantID, filter.ExamID, filter.StudentID, filter.Status, filter.AssignedTo,
		filter.CursorCreatedAt, filter.CursorID, filter.Limit}, scopeArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Appeal{}
	for rows.Next() {
		item, err := scanAppealSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Statistics(ctx context.Context, tenantID string, filter StatisticsFilter) (AppealStatistics, error) {
	scopeArgs, err := appealScopeArgs(tenantID, filter.AccessScope)
	if err != nil {
		return AppealStatistics{}, err
	}
	args := append([]any{tenantID, filter.ExamID}, scopeArgs...)
	// 三项聚合使用同一作用域谓词；先筛选申诉，再计算数量、调分和处理时长。
	rows, err := s.db.QueryContext(ctx, `
SELECT a.status, COUNT(*)
FROM appeal a
JOIN exam e ON e.tenant_id=a.tenant_id AND e.id=a.exam_id
WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND ($2 = '' OR a.exam_id::text = $2)
  AND `+appealScopePredicate(3)+`
GROUP BY a.status
`, args...)
	if err != nil {
		return AppealStatistics{}, err
	}
	defer rows.Close()
	out := AppealStatistics{ByStatus: map[string]int{}}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return AppealStatistics{}, err
		}
		out.ByStatus[status] = count
		out.Total += count
	}
	if err := rows.Err(); err != nil {
		return AppealStatistics{}, err
	}
	if err := rows.Close(); err != nil {
		return AppealStatistics{}, err
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM score_adjustment sa
JOIN appeal a ON a.tenant_id = sa.tenant_id AND a.id = sa.appeal_id
JOIN exam e ON e.tenant_id=a.tenant_id AND e.id=a.exam_id
WHERE sa.tenant_id = $1 AND sa.deleted_at IS NULL AND a.deleted_at IS NULL AND ($2 = '' OR a.exam_id::text = $2)
  AND `+appealScopePredicate(3)+`
`, args...).Scan(&out.ScoreAdjustedCount); err != nil {
		return AppealStatistics{}, err
	}
	var avg sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
SELECT AVG(EXTRACT(EPOCH FROM (a.reviewed_at - a.created_at)) / 3600.0)
FROM appeal a
JOIN exam e ON e.tenant_id=a.tenant_id AND e.id=a.exam_id
WHERE a.tenant_id = $1 AND a.deleted_at IS NULL AND a.reviewed_at IS NOT NULL AND ($2 = '' OR a.exam_id::text = $2)
  AND `+appealScopePredicate(3)+`
`, args...).Scan(&avg); err != nil {
		return AppealStatistics{}, err
	}
	if avg.Valid {
		out.AverageHandleHours = avg.Float64
	}
	return out, nil
}

func appealScopeArgs(tenantID string, scope *auth.AccessScope) ([]any, error) {
	if scope == nil {
		return []any{true, "[]", "[]", "[]", "[]", "", ""}, nil
	}
	if scope.TenantID != tenantID {
		return nil, ErrForbidden
	}
	var schools []string
	if scope.IsSchoolWide() {
		schools = scope.SchoolIDs
	}
	assignedActor := ""
	if scope.AssignedOnly {
		assignedActor = scope.ActorID
	}
	idsJSON := func(ids []string) string {
		if len(ids) == 0 {
			return "[]"
		}
		raw, _ := json.Marshal(ids)
		return string(raw)
	}
	return []any{scope.TenantWide || scope.IsPlatform, idsJSON(schools), idsJSON(scope.ExamIDs),
		idsJSON(scope.SubmissionIDs), idsJSON(scope.ClassIDs), scope.StudentID, assignedActor}, nil
}

func appealScopePredicate(start int) string {
	// 占位符由固定查询的位置生成；所有范围 ID 都作为参数传入，不插入 SQL。
	// 班级角色派生的 school_id 仅用于导航，只有可信学校级角色能按全校筛选。
	return fmt.Sprintf(`($%d::boolean
    OR e.school_id::text IN (SELECT jsonb_array_elements_text($%d::jsonb))
    OR a.exam_id::text IN (SELECT jsonb_array_elements_text($%d::jsonb))
    OR a.submission_id::text IN (SELECT jsonb_array_elements_text($%d::jsonb))
    OR EXISTS (SELECT 1 FROM exam_class ec WHERE ec.tenant_id=a.tenant_id AND ec.exam_id=a.exam_id
      AND ec.deleted_at IS NULL AND ec.class_id::text IN (SELECT jsonb_array_elements_text($%d::jsonb)))
    OR ($%d <> '' AND a.student_id::text=$%d)
    OR ($%d <> '' AND a.assigned_to::text=$%d))`,
		start, start+1, start+2, start+3, start+4, start+5, start+5, start+6, start+6)
}
