package scorerelease

import (
	"context"
	"database/sql"
	"errors"
)

// 只解析一次不可变发布，不加载任何题目载荷。只有可见性策略要求时才需要同组群体总数。
func (s *PostgresStore) studentDetail(ctx context.Context, tenantID, examID, studentID string) (Detail, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, examID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	release, err := s.release(ctx, tenantID, id)
	if err != nil {
		return Detail{}, err
	}
	filter := studentID
	p := release.VisibilityPolicy
	if p.ShowCohortStatistics || p.ShowScoreDistribution || p.ShowPercentile || p.ShowExactRank {
		filter = ""
	}
	items, err := s.filteredItems(ctx, tenantID, id, filter)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Release: release, Items: items}, nil
}
