package dashboard

import (
	"context"
	"database/sql"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// ActivityStore 是运维投影，不是合规审计读取器；实现必须在每次查询中强制应用服务端派生的作用域。
type ActivityStore interface {
	ListRecent(ctx context.Context, tenantID string, scope auth.AccessScope, limit int) ([]RecentActivity, error)
}

type PostgresActivityStore struct{ db *sql.DB }

func NewPostgresActivityStore(db *sql.DB) *PostgresActivityStore {
	return &PostgresActivityStore{db: db}
}

func (s *PostgresActivityStore) ListRecent(ctx context.Context, tenantID string, scope auth.AccessScope, limit int) ([]RecentActivity, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	mode := scope.QueryMode()
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,event_type,target_type,COALESCE(target_id::text,''),title,summary,severity,happened_at,action_path
FROM activity_event
WHERE tenant_id=$1::uuid
  AND ($2 IN ('platform','tenant')
    OR ($2='school' AND school_id::text = ANY(string_to_array(NULLIF($3,''),',')))
    OR ($2='class' AND exam_id::text = ANY(string_to_array(NULLIF($4,''),','))))
ORDER BY happened_at DESC,id DESC
LIMIT $5
`, tenantID, mode, strings.Join(scope.SchoolIDs, ","), strings.Join(scope.ExamIDs, ","), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RecentActivity, 0, limit)
	for rows.Next() {
		var item RecentActivity
		if err := rows.Scan(&item.ID, &item.Action, &item.TargetType, &item.TargetID, &item.Title, &item.Summary, &item.Severity, &item.CreatedAt, &item.DrilldownPath); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
