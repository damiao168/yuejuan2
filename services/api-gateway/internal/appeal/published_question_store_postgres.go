package appeal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// PublishedQuestionAppealPostgresStore reads the released fact and writes an
// appeal workflow in the same database. It deliberately has no dependency on
// submission_grade/final_grade as an authoritative source.
type PublishedQuestionAppealPostgresStore struct {
	db *sql.DB
}

func NewPublishedQuestionAppealPostgresStore(db *sql.DB) *PublishedQuestionAppealPostgresStore {
	return &PublishedQuestionAppealPostgresStore{db: db}
}

func (s *PublishedQuestionAppealPostgresStore) CreatePublishedQuestionAppeal(ctx context.Context, tenantID, studentID, actorID string, input CreatePublishedQuestionAppealInput) (PublishedQuestionAppeal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer func() { _ = tx.Rollback() }()

	source, window, err := s.sourceFactTx(ctx, tx, tenantID, studentID, input)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if !releaseAppealWindowAllows(window, input.ReasonCode, time.Now().UTC()) {
		return PublishedQuestionAppeal{}, ErrAppealWindowClosed
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM question_appeal
  WHERE tenant_id = $1 AND student_id = $2::uuid
    AND source_release_id = $3::uuid AND question_id = $4::uuid
)`, tenantID, studentID, input.SourceReleaseID, input.QuestionID).Scan(&exists); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if exists {
		return PublishedQuestionAppeal{}, ErrAppealAlreadyFiled
	}
	selectedRegion, err := json.Marshal(input.SelectedRegion)
	if err != nil {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	item, err := scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `
INSERT INTO question_appeal (
  tenant_id, exam_id, student_id, submission_id, source_release_id,
  source_release_version, question_id, question_no, source_final_grade_id,
  source_score, source_max_score, reason_code, reason, selected_region,
  created_by
)
VALUES (
  $1, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
  $6, $7::uuid, $8, $9::uuid,
  $10, $11, $12, $13, $14::jsonb,
  $15::uuid
)
RETURNING `+publishedQuestionAppealColumns("")+`
`, tenantID, input.ExamID, studentID, source.SubmissionID, input.SourceReleaseID,
		source.ReleaseVersion, input.QuestionID, source.QuestionNo, source.FinalGradeID,
		source.Score, source.MaxScore, input.ReasonCode, input.Reason, string(selectedRegion), actorID))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, item.ID, actorID, "submitted", map[string]any{
		"source_release_id":      item.SourceReleaseID,
		"source_release_version": item.SourceReleaseVersion,
		"question_id":            item.QuestionID,
		"reason_code":            item.ReasonCode,
	}); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

func (s *PublishedQuestionAppealPostgresStore) GetPublishedQuestionAppeal(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppeal, error) {
	item, err := scanPublishedQuestionAppeal(s.db.QueryRowContext(ctx, `
SELECT `+publishedQuestionAppealColumns("")+`
FROM question_appeal
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, appealID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	return item, err
}

func (s *PublishedQuestionAppealPostgresStore) GetPublishedQuestionAppealContext(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppealContext, error) {
	appeal, err := s.GetPublishedQuestionAppeal(ctx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	context := PublishedQuestionAppealContext{Appeal: appeal, RubricSnapshot: map[string]any{}, ReleaseHistory: []QuestionAppealReleaseVersion{}}
	var rubricJSON []byte
	err = s.db.QueryRowContext(ctx, `
SELECT segment.id::text, grade.source, snapshot.rubric_snapshot_json
FROM final_grade grade
JOIN answer_segment segment
  ON segment.tenant_id = grade.tenant_id AND segment.id = grade.answer_segment_id AND segment.deleted_at IS NULL
LEFT JOIN exam_question_snapshot snapshot
  ON snapshot.tenant_id = grade.tenant_id AND snapshot.id = grade.exam_question_snapshot_id
WHERE grade.tenant_id = $1 AND grade.id = $2::uuid AND grade.deleted_at IS NULL
`, tenantID, appeal.SourceFinalGradeID).Scan(&context.AnswerSegmentID, &context.SourceType, &rubricJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppealContext{}, err
	}
	if err == nil {
		context.AnswerImageReady = context.AnswerSegmentID != ""
		if len(rubricJSON) > 0 {
			if json.Unmarshal(rubricJSON, &context.RubricSnapshot) != nil {
				return PublishedQuestionAppealContext{}, ErrInvalidInput
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, version, source, status, reason, published_at
FROM score_release
WHERE tenant_id = $1 AND exam_id = $2::uuid
ORDER BY version DESC
`, tenantID, appeal.ExamID)
	if err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var version QuestionAppealReleaseVersion
		var publishedAt sql.NullTime
		if err := rows.Scan(&version.ID, &version.Version, &version.Source, &version.Status, &version.Reason, &publishedAt); err != nil {
			return PublishedQuestionAppealContext{}, err
		}
		if publishedAt.Valid {
			value := publishedAt.Time.UTC()
			version.PublishedAt = &value
		}
		context.ReleaseHistory = append(context.ReleaseHistory, version)
	}
	if err := rows.Err(); err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	return context, nil
}

func (s *PublishedQuestionAppealPostgresStore) ListPublishedQuestionAppeals(ctx context.Context, tenantID string, filter QuestionAppealFilter) ([]PublishedQuestionAppeal, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+publishedQuestionAppealColumns("")+`
FROM question_appeal
WHERE tenant_id = $1
  AND ($2 = '' OR exam_id::text = $2)
  AND ($3 = '' OR student_id::text = $3)
  AND ($4 = '' OR assigned_to::text = $4)
  AND ($5 = '' OR status = $5)
ORDER BY created_at DESC, id DESC
`, tenantID, filter.ExamID, filter.StudentID, filter.AssignedTo, filter.Status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PublishedQuestionAppeal{}
	for rows.Next() {
		item, err := scanPublishedQuestionAppeal(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PublishedQuestionAppealPostgresStore) StartPublishedQuestionAppealReview(ctx context.Context, tenantID, appealID, actorID string, input StartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := publishedQuestionAppealForUpdate(ctx, tx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if item.Status != QuestionAppealSubmitted {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	if ok, err := appealWorkerExists(ctx, tx, tenantID, input.AssignedTo); err != nil {
		return PublishedQuestionAppeal{}, err
	} else if !ok {
		return PublishedQuestionAppeal{}, ErrForbidden
	}
	item, err = scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `
UPDATE question_appeal
SET status = 'under_review', assigned_to = $3::uuid, updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid AND revision = $4
RETURNING `+publishedQuestionAppealColumns("")+`
`, tenantID, appealID, input.AssignedTo, input.ExpectedRevision))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, item.ID, actorID, "review_started", map[string]any{"assigned_to": input.AssignedTo}); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

func (s *PublishedQuestionAppealPostgresStore) DecidePublishedQuestionAppeal(ctx context.Context, tenantID, appealID, actorID string, input DecideQuestionAppealInput) (PublishedQuestionAppeal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := publishedQuestionAppealForUpdate(ctx, tx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if item.Status != QuestionAppealUnderReview {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	status, regradeJobID := QuestionAppealRejected, ""
	if input.Decision == QuestionAppealDecisionReferRegrade {
		if ok, err := matchingAppealRegradeJob(ctx, tx, tenantID, item, input.RegradeJobID); err != nil {
			return PublishedQuestionAppeal{}, err
		} else if !ok {
			return PublishedQuestionAppeal{}, ErrInvalidInput
		}
		status, regradeJobID = QuestionAppealUpheldPendingRegrade, input.RegradeJobID
	}
	item, err = scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `
UPDATE question_appeal
SET status = $3, decision = $4, public_response = $5, private_note = $6,
    regrade_job_id = NULLIF($7, '')::uuid, decided_by = $8::uuid, decided_at = now(),
    updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid AND revision = $9
RETURNING `+publishedQuestionAppealColumns("")+`
`, tenantID, appealID, status, input.Decision, input.PublicResponse, input.PrivateNote, regradeJobID, actorID, input.ExpectedRevision))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, item.ID, actorID, "decided", map[string]any{"decision": item.Decision, "regrade_job_id": item.RegradeJobID}); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

func (s *PublishedQuestionAppealPostgresStore) ResolvePublishedQuestionAppeal(ctx context.Context, tenantID, appealID, actorID string, input ResolveQuestionAppealInput) (PublishedQuestionAppeal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := publishedQuestionAppealForUpdate(ctx, tx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if item.Status != QuestionAppealUpheldPendingRegrade {
		return PublishedQuestionAppeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	if ok, err := successorPublishedReleaseExists(ctx, tx, tenantID, item, input.NewReleaseID); err != nil {
		return PublishedQuestionAppeal{}, err
	} else if !ok {
		return PublishedQuestionAppeal{}, ErrResolutionRelease
	}
	item, err = scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `
UPDATE question_appeal
SET status = 'resolved', new_release_id = $3::uuid,
    public_response = CASE WHEN $4 = '' THEN public_response ELSE $4 END,
    private_note = CASE WHEN $5 = '' THEN private_note ELSE $5 END,
    updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid AND revision = $6
RETURNING `+publishedQuestionAppealColumns("")+`
`, tenantID, appealID, input.NewReleaseID, input.PublicResponse, input.PrivateNote, input.ExpectedRevision))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, item.ID, actorID, "resolved", map[string]any{"new_release_id": item.NewReleaseID}); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

func (s *PublishedQuestionAppealPostgresStore) ListPublishedQuestionAppealEvents(ctx context.Context, tenantID, appealID string) ([]PublishedQuestionAppealEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, appeal_id::text, event_type, actor_id::text, payload, created_at
FROM question_appeal_event
WHERE tenant_id = $1 AND appeal_id = $2::uuid
ORDER BY created_at, id
`, tenantID, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []PublishedQuestionAppealEvent{}
	for rows.Next() {
		var item PublishedQuestionAppealEvent
		var payload []byte
		if err := rows.Scan(&item.ID, &item.AppealID, &item.Type, &item.ActorID, &payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		events = append(events, item)
	}
	return events, rows.Err()
}

type publishedQuestionSourceFact struct {
	SubmissionID   string
	ReleaseVersion int
	QuestionNo     string
	FinalGradeID   string
	Score          float64
	MaxScore       float64
}

func (s *PublishedQuestionAppealPostgresStore) sourceFactTx(ctx context.Context, tx *sql.Tx, tenantID, studentID string, input CreatePublishedQuestionAppealInput) (publishedQuestionSourceFact, json.RawMessage, error) {
	var source publishedQuestionSourceFact
	var window json.RawMessage
	err := tx.QueryRowContext(ctx, `
SELECT item.submission_id::text, release.version, question_fact.question_no,
       question_fact.final_grade_id::text, question_fact.score, question_fact.max_score,
       release.appeal_window
FROM score_release release
JOIN score_release_item item
  ON item.tenant_id = release.tenant_id AND item.release_id = release.id
JOIN score_release_question question_fact
  ON question_fact.tenant_id = release.tenant_id
 AND question_fact.release_id = release.id
 AND question_fact.submission_id = item.submission_id
WHERE release.tenant_id = $1 AND release.id = $2::uuid AND release.exam_id = $3::uuid
  AND release.status = 'published' AND item.student_id = $4::uuid
  AND question_fact.question_id = $5::uuid
`, tenantID, input.SourceReleaseID, input.ExamID, studentID, input.QuestionID).Scan(
		&source.SubmissionID, &source.ReleaseVersion, &source.QuestionNo, &source.FinalGradeID, &source.Score, &source.MaxScore, &window,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return publishedQuestionSourceFact{}, nil, ErrSourceRelease
		}
		return publishedQuestionSourceFact{}, nil, err
	}
	return source, window, nil
}

func publishedQuestionAppealForUpdate(ctx context.Context, tx *sql.Tx, tenantID, appealID string) (PublishedQuestionAppeal, error) {
	item, err := scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `
SELECT `+publishedQuestionAppealColumns("")+`
FROM question_appeal WHERE tenant_id = $1 AND id = $2::uuid FOR UPDATE
`, tenantID, appealID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	return item, err
}

// 事件与申诉状态共用事务，避免留下有状态无历史或有历史无状态的记录。
func insertPublishedQuestionAppealEvent(ctx context.Context, tx *sql.Tx, tenantID, appealID, actorID, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO question_appeal_event (tenant_id, appeal_id, event_type, actor_id, payload)
VALUES ($1, $2::uuid, $3, $4::uuid, $5::jsonb)
`, tenantID, appealID, eventType, actorID, string(encoded))
	return err
}

func appealWorkerExists(ctx context.Context, tx *sql.Tx, tenantID, userID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM app_user user_row
  JOIN user_role role_link ON role_link.tenant_id = user_row.tenant_id AND role_link.user_id = user_row.id AND role_link.deleted_at IS NULL
  JOIN role_permission permission_link ON permission_link.tenant_id = role_link.tenant_id AND permission_link.role_id = role_link.role_id AND permission_link.deleted_at IS NULL
  JOIN permission permission_row ON permission_row.tenant_id = permission_link.tenant_id AND permission_row.id = permission_link.permission_id AND permission_row.deleted_at IS NULL
  WHERE user_row.tenant_id = $1 AND user_row.id = $2::uuid AND user_row.status = 'active' AND user_row.deleted_at IS NULL
    AND permission_row.code = 'appeal:work'
)
`, tenantID, userID).Scan(&exists)
	return exists, err
}

// 重评任务必须锚定同一考试、题目和原发布版本，不能借用其他批次的重评任务。
func matchingAppealRegradeJob(ctx context.Context, tx *sql.Tx, tenantID string, item PublishedQuestionAppeal, jobID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM regrade_job
  WHERE tenant_id = $1 AND id = $2::uuid AND exam_id = $3::uuid
    AND question_id = $4::uuid AND source_release_id = $5::uuid
)
`, tenantID, jobID, item.ExamID, item.QuestionID, item.SourceReleaseID).Scan(&exists)
	return exists, err
}

// 结案版本不仅要更新且已发布，还必须包含原学生、原答卷和原题目的成绩事实。
func successorPublishedReleaseExists(ctx context.Context, tx *sql.Tx, tenantID string, item PublishedQuestionAppeal, releaseID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM score_release successor
  JOIN score_release_item successor_item
    ON successor_item.tenant_id = successor.tenant_id AND successor_item.release_id = successor.id
  JOIN score_release_question successor_question
    ON successor_question.tenant_id = successor.tenant_id
   AND successor_question.release_id = successor.id
   AND successor_question.submission_id = successor_item.submission_id
  WHERE successor.tenant_id = $1 AND successor.id = $2::uuid
    AND successor.exam_id = $3::uuid AND successor.status = 'published'
    AND successor.version > $4
    AND successor_item.student_id = $5::uuid
    AND successor_item.submission_id = $6::uuid
    AND successor_question.question_id = $7::uuid
)
`, tenantID, releaseID, item.ExamID, item.SourceReleaseVersion, item.StudentID, item.SubmissionID, item.QuestionID).Scan(&exists)
	return exists, err
}

func publishedQuestionAppealColumns(prefix string) string {
	if prefix != "" {
		prefix += "."
	}
	return prefix + `id::text, tenant_id::text, exam_id::text, student_id::text, submission_id::text,
source_release_id::text, source_release_version, question_id::text, question_no,
source_final_grade_id::text, source_score, source_max_score, reason_code, reason, selected_region,
status, COALESCE(assigned_to::text, ''), COALESCE(decision, ''), public_response, private_note,
COALESCE(regrade_job_id::text, ''), COALESCE(new_release_id::text, ''), created_by::text,
COALESCE(decided_by::text, ''), decided_at, created_at, updated_at, revision`
}

type publishedQuestionAppealScanner interface{ Scan(...any) error }

func scanPublishedQuestionAppeal(scanner publishedQuestionAppealScanner) (PublishedQuestionAppeal, error) {
	var item PublishedQuestionAppeal
	var selectedRegion []byte
	var decidedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.TenantID, &item.ExamID, &item.StudentID, &item.SubmissionID,
		&item.SourceReleaseID, &item.SourceReleaseVersion, &item.QuestionID, &item.QuestionNo,
		&item.SourceFinalGradeID, &item.SourceScore, &item.SourceMaxScore, &item.ReasonCode, &item.Reason, &selectedRegion,
		&item.Status, &item.AssignedTo, &item.Decision, &item.PublicResponse, &item.PrivateNote,
		&item.RegradeJobID, &item.NewReleaseID, &item.CreatedBy, &item.DecidedBy, &decidedAt,
		&item.CreatedAt, &item.UpdatedAt, &item.Revision,
	)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := json.Unmarshal(selectedRegion, &item.SelectedRegion); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if decidedAt.Valid {
		item.DecidedAt = &decidedAt.Time
	}
	return item, nil
}

type releaseAppealWindow struct {
	Enabled            bool       `json:"enabled"`
	OpensAt            *time.Time `json:"opens_at,omitempty"`
	ClosesAt           *time.Time `json:"closes_at,omitempty"`
	AllowedReasonCodes []string   `json:"allowed_reason_codes,omitempty"`
}

func releaseAppealWindowAllows(raw json.RawMessage, reasonCode string, now time.Time) bool {
	var window releaseAppealWindow
	if len(raw) == 0 || json.Unmarshal(raw, &window) != nil {
		return false
	}
	return sourceAppealWindowOpen(window.Enabled, window.OpensAt, window.ClosesAt, window.AllowedReasonCodes, reasonCode, now)
}
