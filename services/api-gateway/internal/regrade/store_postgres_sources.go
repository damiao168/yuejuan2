package regrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// SourceItems 只读取不可变的已发布成绩快照。
// 它不沿 final_grade 查询，因此后续业务成绩变化不会改变重评预览或作业所指的群体。
func (s *PostgresStore) SourceItems(ctx context.Context, tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	return sourceItemsQuery(ctx, s.db, tenantID, examID, questionID, releaseID, selector)
}

type sourceQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func sourceItemsQuery(ctx context.Context, querier sourceQuerier, tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	query := `
SELECT question.submission_id::text, COALESCE(item.student_id::text, ''), question.final_grade_id::text,
       question.score::float8, question.max_score::float8
FROM score_release release
JOIN score_release_question question
  ON question.tenant_id = release.tenant_id AND question.release_id = release.id
LEFT JOIN score_release_item item
  ON item.tenant_id = question.tenant_id AND item.release_id = question.release_id AND item.submission_id = question.submission_id
WHERE release.tenant_id = $1::uuid AND release.exam_id = $2::uuid AND release.id = $3::uuid
  AND release.status = 'published' AND question.question_id = $4::uuid`
	args := []any{tenantID, examID, releaseID, questionID}
	if selector.ScoreBand != nil {
		if selector.ScoreBand.Min != nil {
			args = append(args, *selector.ScoreBand.Min)
			query += fmt.Sprintf(" AND question.score >= $%d", len(args))
		}
		if selector.ScoreBand.Max != nil {
			args = append(args, *selector.ScoreBand.Max)
			query += fmt.Sprintf(" AND question.score <= $%d", len(args))
		}
	}
	if len(selector.SubmissionIDs) > 0 {
		placeholders := make([]string, 0, len(selector.SubmissionIDs))
		for _, id := range selector.SubmissionIDs {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(args)))
		}
		query += " AND question.submission_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY question.submission_id"
	rows, err := querier.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var item SourceItem
		if err := rows.Scan(&item.SubmissionID, &item.StudentID, &item.OldFinalGradeID, &item.OldScore, &item.MaxScore); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		// 区分无效或不匹配的发布与有效但选择器无匹配项的发布，同时避免泄漏其他租户的 ID。
		var exists bool
		err := sourceExists(ctx, querier, tenantID, examID, releaseID, &exists)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrSourceRelease
		}
	}
	return items, nil
}

func sourceExists(ctx context.Context, querier sourceQuerier, tenantID, examID, releaseID string, exists *bool) error {
	return querier.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM score_release WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND status='published')`, tenantID, examID, releaseID).Scan(exists)
}

func (s *PostgresStore) Preview(ctx context.Context, tenantID, examID, questionID, releaseID string, selector Selector) (Preview, error) {
	items, err := s.SourceItems(ctx, tenantID, examID, questionID, releaseID, selector)
	if err != nil {
		return Preview{}, err
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM score_release WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid`, tenantID, examID, releaseID).Scan(&version); err != nil {
		return Preview{}, err
	}
	var currentID sql.NullString
	var currentVersion sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT current.release_id::text, release.version FROM score_release_current current JOIN score_release release ON release.tenant_id=current.tenant_id AND release.id=current.release_id WHERE current.tenant_id=$1::uuid AND current.exam_id=$2::uuid`, tenantID, examID).Scan(&currentID, &currentVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Preview{}, err
	}
	return previewFromSources(examID, questionID, releaseID, version, currentID.String, int(currentVersion.Int64), items), nil
}
