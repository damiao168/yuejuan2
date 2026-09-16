package scorerelease

import (
	"context"
	"database/sql"
	"errors"
)

// Resolve the immutable release once, without loading any question payloads.
// Cohort totals are needed only when the visibility policy asks for them.
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
	if p.ShowCohortStatistics || p.ShowScoreDistribution || p.ShowPercentile || p.ShowExactRank || (p.ShowQuestionScores && p.ShowHighScorePaper) {
		filter = ""
	}
	items, err := s.filteredItems(ctx, tenantID, id, filter)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Release: release, Items: items}, nil
}
