package regrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// SourceItems reads only an immutable, published score-release snapshot. It
// never follows final_grade, so a later operational grade change cannot alter
// the population that a regrade preview or job means.
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
		// Distinguish an invalid/mismatched release from a valid release whose
		// selector simply has no matches, without leaking another tenant's ID.
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

func (s *PostgresStore) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput, sources []SourceItem) (Job, []Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, nil, err
	}
	defer tx.Rollback()
	// 按租户和考试串行创建任务，让幂等键查询和任务写入处于同一临界区。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID); err != nil {
		return Job{}, nil, err
	}
	if existing, found, err := s.idempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Job{}, nil, err
	} else if found {
		items, err := s.itemsTx(ctx, tx, tenantID, existing.ID)
		if err != nil {
			return Job{}, nil, err
		}
		return existing, items, nil
	}
	// A regrade must point at one immutable assessment snapshot. Resolve the
	// latest snapshot at creation when an operator leaves the field empty, and
	// validate a supplied ID against this exact exam/question. This prevents a
	// later configuration edit from silently changing a running correction.
	if err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM exam_question_snapshot
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
  AND ($4='' OR id=$4::uuid)
ORDER BY snapshot_version DESC
LIMIT 1
`, tenantID, examID, questionID, input.NewRubricSnapshotID).Scan(&input.NewRubricSnapshotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, nil, ErrInvalidInput
		}
		return Job{}, nil, err
	}
	// Releases are immutable.  Re-read the source in this transaction so the
	// persisted item population is never based on current grade facts.
	fresh, err := sourceItemsQuery(ctx, tx, tenantID, examID, questionID, input.SourceReleaseID, input.Selector)
	if err != nil {
		return Job{}, nil, err
	}
	if len(fresh) == 0 {
		return Job{}, nil, ErrNoAffectedItems
	}
	_ = sources // Service preview inputs are intentionally non-authoritative.
	selectorJSON, err := json.Marshal(input.Selector)
	if err != nil {
		return Job{}, nil, err
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `
INSERT INTO regrade_job (tenant_id,exam_id,question_id,source_release_id,reason_code,reason_text,strategy,selector_json,new_rubric_snapshot_id,new_policy_version,severity_delta,idempotency_key,status,affected_count,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,NULLIF($9,'')::uuid,NULLIF($10,''),$11,$12,'awaiting_approval',$13,$14::uuid)
RETURNING `+jobReturning, tenantID, examID, questionID, input.SourceReleaseID, input.ReasonCode, input.ReasonText, input.Strategy, string(selectorJSON), input.NewRubricSnapshotID, input.NewPolicyVersion, *input.SeverityDelta, input.IdempotencyKey, len(fresh), actorID))
	if err != nil {
		return Job{}, nil, err
	}
	items := make([]Item, 0, len(fresh))
	for _, source := range fresh {
		item, err := scanItem(tx.QueryRowContext(ctx, `
INSERT INTO regrade_item (tenant_id,job_id,submission_id,old_final_grade_id,old_score,max_score,status,assigned_to)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'pending',NULLIF($7,'')::uuid)
RETURNING `+itemReturning, tenantID, job.ID, source.SubmissionID, source.OldFinalGradeID, source.OldScore, source.MaxScore, input.AssigneeID))
		if err != nil {
			return Job{}, nil, err
		}
		items = append(items, item)
	}
	if err := addEventTx(ctx, tx, tenantID, job.ID, "", "created", actorID, map[string]any{"affected_count": len(items), "source_release_id": job.SourceReleaseID, "strategy": job.Strategy}); err != nil {
		return Job{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, nil, err
	}
	return job, items, nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, jobID string) (Summary, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	if err != nil {
		return Summary{}, err
	}
	items, err := s.items(ctx, tenantID, jobID)
	if err != nil {
		return Summary{}, err
	}
	events, err := s.events(ctx, tenantID, jobID)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Job: job, Items: items, Events: events}, nil
}
func (s *PostgresStore) List(ctx context.Context, tenantID, examID, questionID string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3) ORDER BY created_at DESC,id DESC`, tenantID, examID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) ListAssigned(ctx context.Context, tenantID, reviewerID string) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM (
SELECT item.* FROM regrade_item item
JOIN regrade_job job ON job.tenant_id=item.tenant_id AND job.id=item.job_id
WHERE item.tenant_id=$1::uuid AND item.assigned_to=$2::uuid
  AND job.status IN ('running','diff_review')
  AND (item.status='pending' OR (item.status='claimed' AND item.claimed_by=$2::uuid))
) AS regrade_item ORDER BY created_at,id`, tenantID, reviewerID)
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

func (s *PostgresStore) Approve(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	return s.jobTransition(ctx, tenantID, jobID, actorID, StatusAwaitingApproval, StatusApproved, "approved", func(ctx context.Context, tx *sql.Tx) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='approved',approved_by=$3::uuid,approved_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID, actorID))
	})
}
func (s *PostgresStore) Start(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	return s.jobTransition(ctx, tenantID, jobID, actorID, StatusApproved, StatusRunning, "started", func(ctx context.Context, tx *sql.Tx) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='running',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	})
}
func (s *PostgresStore) Pause(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusPaused {
		return job, nil
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	job, err = scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='paused',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, jobID, "", "paused", actorID, map[string]any{}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}
func (s *PostgresStore) Resume(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	return s.jobTransition(ctx, tenantID, jobID, actorID, StatusPaused, StatusRunning, "resumed", func(ctx context.Context, tx *sql.Tx) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='running',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	})
}

// jobTransition 在任务行锁内检查状态，并把状态变化与事件一起提交；已到目标状态的重试直接返回。
func (s *PostgresStore) jobTransition(ctx context.Context, tenantID, jobID, actorID, expected, target, eventType string, update func(context.Context, *sql.Tx) (Job, error)) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	current, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if current.Status == target {
		return current, nil
	}
	if current.Status != expected {
		return Job{}, ErrStateConflict
	}
	job, err := update(ctx, tx)
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, jobID, "", eventType, actorID, map[string]any{}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) Claim(ctx context.Context, tenantID, itemID, actorID string) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	item, job, err := itemAndJobForUpdate(ctx, tx, tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.AssignedTo != "" && item.AssignedTo != actorID {
		return Item{}, ErrAssignmentForbidden
	}
	if item.Status == ItemClaimed && item.ClaimedBy == actorID {
		return item, nil
	}
	if item.Status != ItemPending {
		return Item{}, ErrStateConflict
	}
	item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE regrade_item SET status='claimed',claimed_by=$3::uuid,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+itemReturning, tenantID, itemID, actorID))
	if err != nil {
		return Item{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, job.ID, item.ID, "item_claimed", actorID, map[string]any{}); err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}
func (s *PostgresStore) RecordCandidate(ctx context.Context, tenantID, itemID, actorID string, input CandidateInput) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	item, job, err := itemAndJobForUpdate(ctx, tx, tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.AssignedTo != "" && item.AssignedTo != actorID {
		return Item{}, ErrAssignmentForbidden
	}
	if item.Status == ItemAwaitingReview && item.CandidateScore != nil && math.Abs(*item.CandidateScore-input.Score) < 0.000001 && item.CandidateGradeID == input.CandidateGradeID {
		return item, nil
	}
	if item.ClaimedBy != actorID || item.Status != ItemClaimed {
		return Item{}, ErrAssignmentForbidden
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, ErrRevisionConflict
	}
	if input.Score > item.MaxScore {
		return Item{}, ErrInvalidInput
	}
	selectionsJSON, err := json.Marshal(input.RubricSelections)
	if err != nil {
		return Item{}, err
	}
	item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE regrade_item
SET candidate_grade_id=NULLIF($3,'')::uuid,candidate_score=$4,delta=$4-old_score,
    candidate_rubric_selections=$5::jsonb,candidate_comment=$6,
    status='awaiting_review',revision=revision+1,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+itemReturning,
		tenantID, itemID, input.CandidateGradeID, input.Score, string(selectionsJSON), input.Comment))
	if err != nil {
		return Item{}, err
	}
	if job.Status == StatusRunning {
		if _, err = tx.ExecContext(ctx, `UPDATE regrade_job SET status='diff_review',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, job.ID); err != nil {
			return Item{}, err
		}
	}
	if err = addEventTx(ctx, tx, tenantID, job.ID, item.ID, "candidate_recorded", actorID, map[string]any{"requires_manual_review": input.RequireManualReview}); err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}
func (s *PostgresStore) Review(ctx context.Context, tenantID, itemID, actorID string, input ReviewInput) (Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	item, job, err := itemAndJobForUpdate(ctx, tx, tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.Status == ItemResolved && item.ReviewedBy == actorID {
		return item, nil
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, ErrRevisionConflict
	}
	if item.Status != ItemAwaitingReview {
		return Item{}, ErrStateConflict
	}
	var query string
	args := []any{tenantID, itemID, actorID, input.ReviewedGradeID, input.Note}
	switch input.Decision {
	case ReviewAccept:
		score := item.CandidateScore
		if input.ReviewedScore != nil {
			score = input.ReviewedScore
		}
		if score == nil || *score > item.MaxScore {
			return Item{}, ErrInvalidInput
		}
		args = append(args, *score)
		query = `UPDATE regrade_item SET reviewed_by=$3::uuid,reviewed_grade_id=NULLIF($4,'')::uuid,reviewed_score=$6,delta=$6-old_score,status='resolved',review_note=$5,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING ` + itemReturning
	case ReviewReject:
		// 驳回只是结束这项复核，清空新分数后，发布计划会保留原分数。
		query = `UPDATE regrade_item SET reviewed_by=$3::uuid,reviewed_grade_id=NULL,reviewed_score=NULL,delta=NULL,status='resolved',review_note=$5,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING ` + itemReturning
	case ReviewException:
		query = `UPDATE regrade_item SET reviewed_by=$3::uuid,status='exception',review_note=$5,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING ` + itemReturning
	default:
		return Item{}, ErrInvalidInput
	}
	item, err = scanItem(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Item{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, job.ID, item.ID, "item_reviewed", actorID, map[string]any{"decision": input.Decision}); err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}
func (s *PostgresStore) Finalize(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusReadyForRelease {
		return job, nil
	}
	if job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	var outstanding int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM regrade_item WHERE tenant_id=$1::uuid AND job_id=$2::uuid AND status<>'resolved'`, tenantID, jobID).Scan(&outstanding); err != nil {
		return Job{}, err
	}
	if outstanding > 0 {
		return Job{}, ErrStateConflict
	}
	job, err = scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='ready_for_release',finalized_by=$3::uuid,finalized_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID, actorID))
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, jobID, "", "finalized", actorID, map[string]any{"next": "create_new_score_release"}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) items(ctx context.Context, tenantID, jobID string) ([]Item, error) {
	return s.itemsQuery(ctx, s.db, tenantID, jobID)
}
func (s *PostgresStore) itemsTx(ctx context.Context, tx *sql.Tx, tenantID, jobID string) ([]Item, error) {
	return s.itemsQuery(ctx, tx, tenantID, jobID)
}
func (s *PostgresStore) itemsQuery(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, tenantID, jobID string) ([]Item, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+itemColumns+` FROM regrade_item WHERE tenant_id=$1::uuid AND job_id=$2::uuid ORDER BY created_at,id`, tenantID, jobID)
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
func (s *PostgresStore) events(ctx context.Context, tenantID, jobID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,job_id::text,COALESCE(item_id::text,''),event_type,actor_id::text,payload,created_at FROM regrade_event WHERE tenant_id=$1::uuid AND job_id=$2::uuid ORDER BY created_at,id`, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var event Event
		var payload []byte
		if err := rows.Scan(&event.ID, &event.JobID, &event.ItemID, &event.Type, &event.ActorID, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &event.Payload); err != nil {
			return nil, err
		}
		event.CreatedAt = event.CreatedAt.UTC()
		out = append(out, event)
	}
	return out, rows.Err()
}
func (s *PostgresStore) idempotentTx(ctx context.Context, tx *sql.Tx, tenantID, examID, key string) (Job, bool, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND idempotency_key=$3`, tenantID, examID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return job, err == nil, err
}
func jobForUpdate(ctx context.Context, tx *sql.Tx, tenantID, jobID string) (Job, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}
func itemAndJobForUpdate(ctx context.Context, tx *sql.Tx, tenantID, itemID string) (Item, Job, error) {
	// 先锁明细，再锁所属任务；领题、候选提交和复核都在拿到两把锁后检查状态。
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM regrade_item WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, Job{}, ErrNotFound
	}
	if err != nil {
		return Item{}, Job{}, err
	}
	job, err := jobForUpdate(ctx, tx, tenantID, item.JobID)
	return item, job, err
}
func addEventTx(ctx context.Context, tx *sql.Tx, tenantID, jobID, itemID, eventType, actorID string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO regrade_event (tenant_id,job_id,item_id,event_type,actor_id,payload) VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,$6::jsonb)`, tenantID, jobID, itemID, eventType, actorID, string(raw))
	return err
}

const jobColumns = `id::text,tenant_id::text,exam_id::text,question_id::text,source_release_id::text,reason_code,reason_text,strategy,selector_json,COALESCE(new_rubric_snapshot_id::text,''),COALESCE(new_policy_version,''),severity_delta::float8,idempotency_key,status,affected_count,created_by::text,COALESCE(approved_by::text,''),approved_at,COALESCE(finalized_by::text,''),finalized_at,created_at,updated_at`
const jobReturning = jobColumns
const itemColumns = `id::text,job_id::text,submission_id::text,old_final_grade_id::text,old_score::float8,max_score::float8,COALESCE(candidate_grade_id::text,''),candidate_score::float8,candidate_rubric_selections,candidate_comment,COALESCE(reviewed_grade_id::text,''),reviewed_score::float8,delta::float8,status,COALESCE(assigned_to::text,''),COALESCE(claimed_by::text,''),COALESCE(reviewed_by::text,''),review_note,revision,created_at,updated_at`
const itemReturning = itemColumns

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, error) {
	var job Job
	var selector []byte
	var approved, finalized sql.NullTime
	if err := row.Scan(&job.ID, &job.TenantID, &job.ExamID, &job.QuestionID, &job.SourceReleaseID, &job.ReasonCode, &job.ReasonText, &job.Strategy, &selector, &job.NewRubricSnapshotID, &job.NewPolicyVersion, &job.SeverityDelta, &job.IdempotencyKey, &job.Status, &job.AffectedCount, &job.CreatedBy, &job.ApprovedBy, &approved, &job.FinalizedBy, &finalized, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return Job{}, err
	}
	if err := json.Unmarshal(selector, &job.Selector); err != nil {
		return Job{}, err
	}
	if approved.Valid {
		value := approved.Time.UTC()
		job.ApprovedAt = &value
	}
	if finalized.Valid {
		value := finalized.Time.UTC()
		job.FinalizedAt = &value
	}
	job.CreatedAt, job.UpdatedAt = job.CreatedAt.UTC(), job.UpdatedAt.UTC()
	return job, nil
}
func scanItem(row scanner) (Item, error) {
	var item Item
	var candidate, reviewed, delta sql.NullFloat64
	var selectionsJSON []byte
	if err := row.Scan(&item.ID, &item.JobID, &item.SubmissionID, &item.OldFinalGradeID, &item.OldScore, &item.MaxScore, &item.CandidateGradeID, &candidate, &selectionsJSON, &item.CandidateComment, &item.ReviewedGradeID, &reviewed, &delta, &item.Status, &item.AssignedTo, &item.ClaimedBy, &item.ReviewedBy, &item.ReviewNote, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Item{}, err
	}
	if candidate.Valid {
		value := candidate.Float64
		item.CandidateScore = &value
	}
	if err := json.Unmarshal(selectionsJSON, &item.CandidateRubricSelections); err != nil {
		return Item{}, err
	}
	if reviewed.Valid {
		value := reviewed.Float64
		item.ReviewedScore = &value
	}
	if delta.Valid {
		value := delta.Float64
		item.Delta = &value
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

// Compile-time shape checks make accidental replacement with a current-score
// writer harder when A18 integrates a ReleasePlan consumer.
var _ Store = (*PostgresStore)(nil)
