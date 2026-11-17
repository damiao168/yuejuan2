package appeal

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"encoding/json"
	"errors"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateAppeal(ctx context.Context, tenantID string, actorID string, input CreateAppealInput) (Appeal, error) {
	input = normalizeCreateInput(input)
	if err := validateCreateInput(input); err != nil {
		return Appeal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireHistoricalPublicationTx(ctx, tx, tenantID, input.ExamID); err != nil {
		return Appeal{}, err
	}
	var submissionGradeID, submissionID string
	if err := tx.QueryRowContext(ctx, `
SELECT id::text, submission_id::text
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND student_id::text = $3
  AND status = 'published' AND locked = true AND deleted_at IS NULL
`, tenantID, input.ExamID, input.StudentID).Scan(&submissionGradeID, &submissionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrUnpublishedGrade
		}
		return Appeal{}, err
	}
	var final finalGradeRef
	if input.TargetType != "exam" {
		var err error
		final, err = s.getFinalGradeRefTx(ctx, tx, tenantID, input.FinalGradeID, submissionID)
		if err != nil {
			return Appeal{}, err
		}
	}
	attachment, _ := json.Marshal(input.Attachment)
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
INSERT INTO appeal (
  tenant_id, exam_id, submission_id, submission_grade_id, student_id, target_type,
  final_grade_id, question_id, question_no, deduction_point_id, reason, attachment, status, created_by
)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid, $9, $10, $11, $12, 'submitted', $13::uuid)
RETURNING `+appealColumns()+`
`, tenantID, input.ExamID, submissionID, submissionGradeID, input.StudentID, input.TargetType,
		input.FinalGradeID, final.QuestionID, final.QuestionNo, input.DeductionPointID, input.Reason, attachment, actorID))
	if err != nil {
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

// 旧申诉的写操作与正式发布共用考试锁。已有正式版本时，更正必须走基于发布快照的申诉和复评。
func requireHistoricalPublicationTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID); err != nil {
		return err
	}
	var published bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM score_release_current WHERE tenant_id=$1::uuid AND exam_id::text=$2)`, tenantID, examID).Scan(&published)
	if err != nil {
		return err
	}
	if published {
		return ErrVersionedAppealRequired
	}
	return nil
}

func (s *PostgresStore) GetAppeal(ctx context.Context, tenantID string, id string) (Appeal, error) {
	item, err := scanAppeal(s.db.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) AssignAppeal(ctx context.Context, tenantID string, id string, _ string, input AssignAppealInput) (Appeal, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	// 新申诉复核分派使用本场考试资格，不能沿用候选列表打开时的旧学科或跨校人员。
	if err := workflowactors.ValidateReviewAssignee(ctx, tx, tenantID, item.ExamID, input.AssignedTo, "appeal:work"); err != nil {
		if errors.Is(err, workflowactors.ErrAssigneeIneligible) {
			return Appeal{}, errors.Join(ErrForbidden, err)
		}
		return Appeal{}, err
	}

	// 已参与这道题原始人工批阅的教师不能再受派复核同一成绩。
	if item.FinalGradeID != "" {
		var participated bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM human_grade hg
  JOIN final_grade fg ON fg.tenant_id = hg.tenant_id AND fg.answer_segment_id = hg.answer_segment_id
  WHERE fg.tenant_id = $1 AND fg.id::text = $2 AND hg.reviewer_id::text = $3
    AND fg.deleted_at IS NULL AND hg.deleted_at IS NULL
)
`, tenantID, item.FinalGradeID, input.AssignedTo).Scan(&participated); err != nil {
			return Appeal{}, err
		}
		if participated {
			return Appeal{}, ErrForbidden
		}
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET assigned_to = $3::uuid, status = 'under_review', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.AssignedTo, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, ErrRevisionConflict
		}
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) SubmitRecommendation(ctx context.Context, tenantID string, id string, actorID string, input SubmitRecommendationInput) (Appeal, error) {
	input = normalizeRecommendationInput(input)
	if err := validateRecommendationInput(input); err != nil {
		return Appeal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	if item.AssignedTo != actorID {
		return Appeal{}, ErrForbidden
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	if input.RecommendedScore != nil {
		if item.FinalGradeID == "" {
			return Appeal{}, ErrInvalidInput
		}
		var maxScore float64
		if err := tx.QueryRowContext(ctx, `
SELECT max_score::float8
FROM final_grade
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
		`, tenantID, item.FinalGradeID).Scan(&maxScore); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Appeal{}, ErrInvalidInput
			}
			return Appeal{}, err
		}
		if *input.RecommendedScore > maxScore {
			return Appeal{}, ErrInvalidInput
		}
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET status = 'under_review',
    teacher_recommendation = $3,
    teacher_recommendation_reason = $4,
    teacher_recommended_score = $5,
    teacher_recommendation_by = $6::uuid,
    teacher_recommendation_at = now(),
    revision = revision + 1,
    updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $7 AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.Recommendation, input.Reason, input.RecommendedScore, actorID, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, ErrRevisionConflict
		}
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) CloseAppeal(ctx context.Context, tenantID string, id string, actorID string, input CloseAppealInput) (Appeal, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	item, err := scanAppeal(s.db.QueryRowContext(ctx, `
UPDATE appeal
SET status = 'closed', result_reason = $3, closed_by = $4::uuid, closed_at = now(),
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $5 AND status <> 'closed' AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.Reason, actorID, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			var currentStatus string
			var currentRevision int64
			lookupErr := s.db.QueryRowContext(ctx, `SELECT status, revision FROM appeal WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&currentStatus, &currentRevision)
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return Appeal{}, ErrNotFound
			}
			if lookupErr != nil {
				return Appeal{}, lookupErr
			}
			if currentRevision != input.ExpectedRevision {
				return Appeal{}, ErrRevisionConflict
			}
			return Appeal{}, ErrInvalidTransition
		}
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}
