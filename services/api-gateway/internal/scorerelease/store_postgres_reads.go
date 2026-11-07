package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
)

func (s *PostgresStore) List(ctx context.Context, tenantID, examID string) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid ORDER BY version DESC`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id string) (Detail, error) {
	release, err := s.release(ctx, tenantID, id)
	if err != nil {
		return Detail{}, err
	}
	items, err := s.items(ctx, tenantID, id)
	if err != nil {
		return Detail{}, err
	}
	questions, err := s.questions(ctx, tenantID, id, "")
	if err != nil {
		return Detail{}, err
	}
	return Detail{Release: release, Items: items, Questions: questions}, nil
}

func (s *PostgresStore) Diff(ctx context.Context, tenantID, id, baseID string) (Diff, error) {
	current, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return Diff{}, err
	}
	base, err := s.Get(ctx, tenantID, baseID)
	if err != nil {
		return Diff{}, err
	}
	if current.Release.ExamID != base.Release.ExamID {
		return Diff{}, ErrInvalidInput
	}
	return releaseDiff(current, base), nil
}

func (s *PostgresStore) CurrentPublished(ctx context.Context, tenantID, examID string) (Detail, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id = $1 AND exam_id = $2::uuid`, tenantID, examID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Detail{}, ErrNotFound
		}
		return Detail{}, err
	}
	return s.Get(ctx, tenantID, id)
}

// nextVersionTx 依赖调用方持有考试事务锁；单独计算 MAX+1 不能防止并发分配同一版本号。
func (s *PostgresStore) nextVersionTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (int, error) {
	var version int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid`, tenantID, examID).Scan(&version)
	return version, err
}

func (s *PostgresStore) findIdempotentTx(ctx context.Context, tx *sql.Tx, tenantID, examID, key string) (Release, bool, error) {
	release, err := scanRelease(tx.QueryRowContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid AND idempotency_key = $3`, tenantID, examID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, false, nil
	}
	return release, err == nil, err
}

func (s *PostgresStore) release(ctx context.Context, tenantID, id string) (Release, error) {
	release, err := scanRelease(s.db.QueryRowContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND id = $2::uuid`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	return release, err
}

func (s *PostgresStore) releaseTx(ctx context.Context, tx *sql.Tx, tenantID, id string, forUpdate bool) (Release, error) {
	query := releaseColumns + ` FROM score_release WHERE tenant_id = $1 AND id = $2::uuid`
	if forUpdate {
		query += " FOR UPDATE"
	}
	release, err := scanRelease(tx.QueryRowContext(ctx, query, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	return release, err
}

func (s *PostgresStore) items(ctx context.Context, tenantID, releaseID string) ([]ReleaseItem, error) {
	return s.filteredItems(ctx, tenantID, releaseID, "")
}

func (s *PostgresStore) filteredItems(ctx context.Context, tenantID, releaseID, studentID string) ([]ReleaseItem, error) {
	query := `SELECT release_id::text, COALESCE(student_id::text, ''), submission_id::text, total_score::float8, max_score::float8, status, snapshot_hash FROM score_release_item WHERE tenant_id = $1 AND release_id = $2::uuid`
	args := []any{tenantID, releaseID}
	if studentID != "" {
		query += " AND student_id=$3::uuid"
		args = append(args, studentID)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY submission_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReleaseItem{}
	for rows.Next() {
		var item ReleaseItem
		if err := rows.Scan(&item.ReleaseID, &item.StudentID, &item.SubmissionID, &item.TotalScore, &item.MaxScore, &item.Status, &item.SnapshotHash); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) questions(ctx context.Context, tenantID, releaseID, submissionID string) ([]ReleaseQuestion, error) {
	query := `SELECT release_id::text, submission_id::text, question_id::text, question_no, final_grade_id::text, score::float8, max_score::float8, source_type, COALESCE(source_id::text, ''), student_explanation FROM score_release_question WHERE tenant_id = $1 AND release_id = $2::uuid`
	args := []any{tenantID, releaseID}
	if submissionID != "" {
		query += " AND submission_id = $3::uuid"
		args = append(args, submissionID)
	}
	query += " ORDER BY submission_id, question_no, question_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReleaseQuestion{}
	for rows.Next() {
		var item ReleaseQuestion
		var raw []byte
		if err := rows.Scan(&item.ReleaseID, &item.SubmissionID, &item.QuestionID, &item.QuestionNo, &item.FinalGradeID, &item.Score, &item.MaxScore, &item.SourceType, &item.SourceID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Explanation); err != nil {
			return nil, err
		}
		item.Explanation.RubricSummary = append([]string(nil), item.Explanation.RubricSummary...)
		items = append(items, item)
	}
	return items, rows.Err()
}

func releaseDiff(current, base Detail) Diff {
	result := Diff{BaseReleaseID: base.Release.ID, ReleaseID: current.Release.ID, Items: []DiffItem{}, Questions: []DiffQuestion{}}
	baseItems, currentItems := indexItems(base.Items), indexItems(current.Items)
	for submissionID, item := range currentItems {
		previous, exists := baseItems[submissionID]
		if !exists || math.Abs(item.TotalScore-previous.TotalScore) > 0.000001 {
			result.Items = append(result.Items, DiffItem{SubmissionID: submissionID, OldTotal: previous.TotalScore, NewTotal: item.TotalScore})
		}
	}
	baseQuestions, currentQuestions := indexQuestions(base.Questions), indexQuestions(current.Questions)
	for key, question := range currentQuestions {
		previous, exists := baseQuestions[key]
		if !exists || math.Abs(question.Score-previous.Score) > 0.000001 {
			result.Questions = append(result.Questions, DiffQuestion{SubmissionID: question.SubmissionID, QuestionID: question.QuestionID, OldScore: previous.Score, NewScore: question.Score})
		}
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].SubmissionID < result.Items[j].SubmissionID })
	sort.Slice(result.Questions, func(i, j int) bool {
		if result.Questions[i].SubmissionID == result.Questions[j].SubmissionID {
			return result.Questions[i].QuestionID < result.Questions[j].QuestionID
		}
		return result.Questions[i].SubmissionID < result.Questions[j].SubmissionID
	})
	result.AffectedCount = len(result.Items)
	return result
}

const releaseColumns = `SELECT id::text, tenant_id::text, exam_id::text, version, source, reason, status, idempotency_key, visibility_policy, appeal_window, gate_snapshot, COALESCE(source_release_id::text, ''), COALESCE(supersedes_release_id::text, ''), created_by::text, created_at, COALESCE(published_by::text, ''), published_at`

const releaseReturning = `id::text, tenant_id::text, exam_id::text, version, source, reason, status, idempotency_key, visibility_policy, appeal_window, gate_snapshot, COALESCE(source_release_id::text, ''), COALESCE(supersedes_release_id::text, ''), created_by::text, created_at, COALESCE(published_by::text, ''), published_at`

type releaseScanner interface{ Scan(...any) error }

func scanRelease(row releaseScanner) (Release, error) {
	var release Release
	var visibility, window, gate []byte
	var publishedBy string
	if err := row.Scan(&release.ID, &release.TenantID, &release.ExamID, &release.Version, &release.Source, &release.Reason, &release.Status, &release.IdempotencyKey, &visibility, &window, &gate, &release.SourceReleaseID, &release.SupersedesReleaseID, &release.CreatedBy, &release.CreatedAt, &publishedBy, &release.PublishedAt); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(visibility, &release.VisibilityPolicy); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(window, &release.AppealWindow); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(gate, &release.GateSnapshot); err != nil {
		return Release{}, err
	}
	release.PublishedBy = publishedBy
	// 所有从数据库返回的版本（列表、详情、当前版本、创建与复评回执、发布结果）都经过这里。
	release.RegradeJobID = regradeJobID(release.IdempotencyKey)
	if release.GateSnapshot.Counts == nil {
		release.GateSnapshot = emptyGate(release.CreatedAt)
	}
	return release, nil
}
