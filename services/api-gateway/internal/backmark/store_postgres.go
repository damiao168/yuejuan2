package backmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func sourceTaskQuery(tenantID, examID, questionID string, selector Selector) (string, []any) {
	query := `
WITH latest_grade AS (
  SELECT rt.id::text AS review_task_id, hg.id::text AS original_grade_id,
         hg.reviewer_id::text AS original_reviewer_id, hg.score::float8 AS original_score,
         hg.max_score::float8 AS max_score, hg.created_at AS graded_at,
         row_number() OVER (PARTITION BY rt.id ORDER BY hg.created_at DESC, hg.id DESC) AS rank
  FROM review_task rt
  JOIN human_grade hg ON hg.tenant_id=rt.tenant_id AND hg.review_task_id=rt.id AND hg.deleted_at IS NULL
  WHERE rt.tenant_id=$1::uuid AND rt.exam_id=$2::uuid AND rt.question_id=$3::uuid
    AND rt.deleted_at IS NULL AND rt.status IN ('submitted','completed')
)
SELECT review_task_id,original_grade_id,original_reviewer_id,original_score,max_score,graded_at
FROM latest_grade WHERE rank=1`
	args := []any{tenantID, examID, questionID}
	add := func(clause string, value any) {
		args = append(args, value)
		query += " AND " + fmt.Sprintf(clause, len(args))
	}
	if selector.TimeRange != nil {
		if selector.TimeRange.From != nil {
			add("graded_at >= $%d", selector.TimeRange.From.UTC())
		}
		if selector.TimeRange.To != nil {
			add("graded_at <= $%d", selector.TimeRange.To.UTC())
		}
	}
	if selector.GraderID != "" {
		add("original_reviewer_id = $%d::text", selector.GraderID)
	}
	if selector.ScoreBand != nil {
		if selector.ScoreBand.Min != nil {
			add("original_score >= $%d", *selector.ScoreBand.Min)
		}
		if selector.ScoreBand.Max != nil {
			add("original_score <= $%d", *selector.ScoreBand.Max)
		}
	}
	if len(selector.TaskIDs) > 0 {
		placeholders := make([]string, 0, len(selector.TaskIDs))
		for _, taskID := range selector.TaskIDs {
			args = append(args, taskID)
			placeholders = append(placeholders, fmt.Sprintf("$%d::text", len(args)))
		}
		query += " AND review_task_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	return query, args
}

func (s *PostgresStore) SelectSourceTasks(ctx context.Context, tenantID, examID, questionID string, selector Selector, limit int) ([]SourceTask, error) {
	query, args := sourceTaskQuery(tenantID, examID, questionID, selector)
	query += " ORDER BY graded_at ASC, review_task_id ASC"
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceTask{}
	for rows.Next() {
		var item SourceTask
		if err := rows.Scan(&item.ReviewTaskID, &item.OriginalGradeID, &item.OriginalReviewer, &item.OriginalScore, &item.MaxScore, &item.GradedAt); err != nil {
			return nil, err
		}
		item.GradedAt = item.GradedAt.UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Preview(ctx context.Context, tenantID, examID, questionID string, selector Selector) (Preview, error) {
	query, args := sourceTaskQuery(tenantID, examID, questionID, selector)
	rows, err := s.db.QueryContext(ctx, `WITH selected AS (`+query+`)
SELECT original_score,count(*)::int,min(graded_at),max(graded_at)
FROM selected GROUP BY original_score ORDER BY original_score`, args...)
	if err != nil {
		return Preview{}, err
	}
	defer rows.Close()
	preview := Preview{ScoreBands: []Band{}}
	var first, last time.Time
	for rows.Next() {
		var band Band
		var from, to time.Time
		if err := rows.Scan(&band.Score, &band.Count, &from, &to); err != nil {
			return Preview{}, err
		}
		preview.AffectedCount += band.Count
		preview.ScoreBands = append(preview.ScoreBands, band)
		if first.IsZero() || from.Before(first) {
			first = from.UTC()
		}
		if last.IsZero() || to.After(last) {
			last = to.UTC()
		}
	}
	if !first.IsZero() {
		preview.TimeRange = TimeRange{From: &first, To: &last}
	}
	return preview, rows.Err()
}

func (s *PostgresStore) CreateBatch(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput, sources []SourceTask) (Batch, []Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, nil, err
	}
	defer tx.Rollback()
	selectorJSON, err := json.Marshal(input.Selector)
	if err != nil {
		return Batch{}, nil, err
	}
	policyJSON, err := json.Marshal(input.Policy)
	if err != nil {
		return Batch{}, nil, err
	}
	batch, err := scanBatch(tx.QueryRowContext(ctx, `
INSERT INTO backmark_batch (tenant_id,exam_id,question_id,source_incident_id,selector_json,policy_json,affected_count,status,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,'open',$8::uuid)
RETURNING `+batchColumns, tenantID, examID, questionID, input.SourceIncidentID, selectorJSON, policyJSON, len(sources), actorID))
	if err != nil {
		return Batch{}, nil, err
	}
	items := make([]Item, 0, len(sources))
	for _, source := range sources {
		item, err := scanItem(tx.QueryRowContext(ctx, `
INSERT INTO backmark_item (tenant_id,batch_id,review_task_id,original_grade_id,original_reviewer_id,reassigned_to,original_score,max_score,status)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,'pending')
RETURNING `+itemColumns, tenantID, batch.ID, source.ReviewTaskID, source.OriginalGradeID, source.OriginalReviewer, input.ReassignedTo, source.OriginalScore, source.MaxScore))
		if err != nil {
			return Batch{}, nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return Batch{}, nil, err
	}
	return batch, items, nil
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM backmark_batch WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, batchID))
}

func (s *PostgresStore) ListBatchItems(ctx context.Context, tenantID, batchID string, page PageOptions) ([]Item, error) {
	cursorTime := nullableCursorTime(page.CursorCreatedAt)
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM backmark_item
WHERE tenant_id=$1::uuid AND batch_id=$2::uuid
  AND ($3::timestamptz IS NULL OR (created_at,id) > ($3::timestamptz,$4::uuid))
  AND ($6='' OR status=$6)
ORDER BY created_at ASC,id ASC LIMIT $5`, tenantID, batchID, cursorTime, nullableCursorID(page.CursorID), page.Limit, page.Status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) GetHistogram(ctx context.Context, tenantID, batchID string) ([]Histogram, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT diff::float8,count(*)::int FROM backmark_item
WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND diff IS NOT NULL
GROUP BY diff ORDER BY diff`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Histogram{}
	for rows.Next() {
		var value Histogram
		if err := rows.Scan(&value.Delta, &value.Count); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetStatusCounts(ctx context.Context, tenantID, batchID string) (StatusCounts, error) {
	var counts StatusCounts
	err := s.db.QueryRowContext(ctx, `SELECT
count(*) FILTER (WHERE status='regrade_required'),
count(*) FILTER (WHERE status IN ('pending','in_progress')),
count(*) FILTER (WHERE status IN ('diff_ready','arbitration_required','regrade_required'))
FROM backmark_item WHERE tenant_id=$1::uuid AND batch_id=$2::uuid`, tenantID, batchID).Scan(
		&counts.RegradeRequiredCount, &counts.PendingCount, &counts.CompletedCount)
	return counts, err
}

func (s *PostgresStore) ListBatches(ctx context.Context, tenantID, examID, questionID string, page PageOptions) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+batchColumns+` FROM backmark_batch
WHERE tenant_id=$1::uuid AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3)
  AND ($4::timestamptz IS NULL OR (created_at,id) < ($4::timestamptz,$5::uuid))
ORDER BY created_at DESC,id DESC LIMIT $6`, tenantID, examID, questionID, nullableCursorTime(page.CursorCreatedAt), nullableCursorID(page.CursorID), page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		batch, scanErr := scanBatch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, batch)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAssigned(ctx context.Context, tenantID, graderID string, page PageOptions) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM backmark_item
WHERE tenant_id=$1::uuid AND reassigned_to=$2::uuid AND status IN ('pending','in_progress')
  AND ($3::timestamptz IS NULL OR (created_at,id) > ($3::timestamptz,$4::uuid))
ORDER BY created_at ASC,id ASC LIMIT $5`, tenantID, graderID, nullableCursorTime(page.CursorCreatedAt), nullableCursorID(page.CursorID), page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAssigned(ctx context.Context, tenantID, itemID, graderID string) (Item, error) {
	item, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM backmark_item
WHERE tenant_id=$1::uuid AND id=$2::uuid AND reassigned_to=$3::uuid`, tenantID, itemID, graderID))
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

func (s *PostgresStore) Claim(ctx context.Context, tenantID, itemID, graderID string) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM backmark_item WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, itemID))
	if err != nil {
		return Item{}, err
	}
	if item.ReassignedTo != graderID {
		return Item{}, ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return Item{}, ErrOriginalGrader
	}
	if item.Status != ItemPending && item.Status != ItemInProgress {
		return Item{}, ErrStateConflict
	}
	if item.Status == ItemPending {
		item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE backmark_item SET status='in_progress',revision=revision+1,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+itemColumns, tenantID, itemID))
		if err != nil {
			return Item{}, err
		}
		if err := refreshBatchTx(ctx, tx, tenantID, item.BatchID); err != nil {
			return Item{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}

func (s *PostgresStore) Submit(ctx context.Context, tenantID, itemID, graderID string, input SubmitInput) (Item, Grade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, Grade{}, err
	}
	defer tx.Rollback()
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM backmark_item WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, itemID))
	if err != nil {
		return Item{}, Grade{}, err
	}
	if item.ReassignedTo != graderID {
		return Item{}, Grade{}, ErrAssigneeForbidden
	}
	if item.OriginalReviewer == graderID {
		return Item{}, Grade{}, ErrOriginalGrader
	}
	if item.Status != ItemInProgress {
		return Item{}, Grade{}, ErrStateConflict
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, Grade{}, ErrRevisionConflict
	}
	if input.Score > item.MaxScore {
		return Item{}, Grade{}, ErrInvalidInput
	}
	var policyJSON []byte
	if err := tx.QueryRowContext(ctx, `SELECT policy_json FROM backmark_batch WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, item.BatchID).Scan(&policyJSON); err != nil {
		return Item{}, Grade{}, err
	}
	policy, err := decodePolicy(policyJSON)
	if err != nil {
		return Item{}, Grade{}, err
	}
	selections, err := json.Marshal(input.RubricSelections)
	if err != nil {
		return Item{}, Grade{}, err
	}
	grade, err := scanGrade(tx.QueryRowContext(ctx, `
INSERT INTO backmark_grade (tenant_id,backmark_item_id,reviewer_id,score,max_score,rubric_selections,comments)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7)
RETURNING `+gradeColumns, tenantID, item.ID, graderID, input.Score, item.MaxScore, selections, input.Comments))
	if err != nil {
		return Item{}, Grade{}, err
	}
	diff := input.Score - item.OriginalScore
	status := nextItemStatus(policy, diff)
	item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE backmark_item
SET new_grade_id=$3::uuid,new_score=$4,diff=$5,status=$6,revision=revision+1,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$7
RETURNING `+itemColumns, tenantID, item.ID, grade.ID, input.Score, diff, status, input.ExpectedRevision))
	if errors.Is(err, ErrNotFound) {
		return Item{}, Grade{}, ErrRevisionConflict
	}
	if err != nil {
		return Item{}, Grade{}, err
	}
	if err := refreshBatchTx(ctx, tx, tenantID, item.BatchID); err != nil {
		return Item{}, Grade{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, Grade{}, err
	}
	return item, grade, nil
}

func refreshBatchTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backmark_item WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND status IN ('pending','in_progress')`, tenantID, batchID).Scan(&remaining); err != nil {
		return err
	}
	status := BatchReadyForConfirmation
	if remaining > 0 {
		var started bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backmark_item WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND status <> 'pending')`, tenantID, batchID).Scan(&started); err != nil {
			return err
		}
		if started {
			status = BatchInProgress
		} else {
			status = BatchOpen
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE backmark_batch SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status <> 'cancelled'`, tenantID, batchID, status)
	return err
}

func nullableCursorTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableCursorID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

const batchColumns = `id::text,exam_id::text,question_id::text,source_incident_id,selector_json,policy_json,affected_count,status,created_by::text,created_at,updated_at`
const itemColumns = `id::text,batch_id::text,review_task_id::text,original_grade_id::text,COALESCE(reassigned_task_id::text,''),COALESCE(new_grade_id::text,''),original_reviewer_id::text,reassigned_to::text,original_score::float8,max_score::float8,new_score::float8,diff::float8,status,revision,created_at,updated_at`
const gradeColumns = `id::text,backmark_item_id::text,reviewer_id::text,score::float8,max_score::float8,rubric_selections,comments,created_at`

type scanner interface{ Scan(...any) error }

func scanBatch(row scanner) (Batch, error) {
	var value Batch
	var selector, policy []byte
	if err := row.Scan(&value.ID, &value.ExamID, &value.QuestionID, &value.SourceIncidentID, &selector, &policy, &value.AffectedCount, &value.Status, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Batch{}, ErrNotFound
		}
		return Batch{}, err
	}
	if err := json.Unmarshal(selector, &value.Selector); err != nil {
		return Batch{}, fmt.Errorf("decode backmark selector: %w", err)
	}
	if err := json.Unmarshal(policy, &value.Policy); err != nil {
		return Batch{}, fmt.Errorf("decode backmark policy: %w", err)
	}
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	return value, nil
}
func scanItem(row scanner) (Item, error) {
	var value Item
	var newScore, diff sql.NullFloat64
	if err := row.Scan(&value.ID, &value.BatchID, &value.ReviewTaskID, &value.OriginalGradeID, &value.ReassignedTaskID, &value.NewGradeID, &value.OriginalReviewer, &value.ReassignedTo, &value.OriginalScore, &value.MaxScore, &newScore, &diff, &value.Status, &value.Revision, &value.CreatedAt, &value.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, ErrNotFound
		}
		return Item{}, err
	}
	if newScore.Valid {
		number := newScore.Float64
		value.NewScore = &number
	}
	if diff.Valid {
		number := diff.Float64
		value.Diff = &number
	}
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	return value, nil
}
func scanGrade(row scanner) (Grade, error) {
	var value Grade
	var selections []byte
	if err := row.Scan(&value.ID, &value.BackmarkItemID, &value.ReviewerID, &value.Score, &value.MaxScore, &selections, &value.Comments, &value.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grade{}, ErrNotFound
		}
		return Grade{}, err
	}
	if err := json.Unmarshal(selections, &value.RubricSelections); err != nil {
		return Grade{}, fmt.Errorf("decode backmark rubric selections: %w", err)
	}
	value.CreatedAt = value.CreatedAt.UTC()
	return value, nil
}
func decodePolicy(data []byte) (Policy, error) {
	var value Policy
	if err := json.Unmarshal(data, &value); err != nil {
		return Policy{}, err
	}
	return normalizePolicy(value), nil
}
