package appeal

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"encoding/json"
	"errors"
	"time"
)

// PublishedQuestionAppealPostgresStore 在同一数据库读取已发布事实并写入申诉工作流，不依赖 submission_grade 或 final_grade 作为权威来源。
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
	// 只在新分派时核对同校、岗位和任教学科；既有本人意见提交保留原授权与回避检查。
	if err := workflowactors.ValidateReviewAssignee(ctx, tx, tenantID, item.ExamID, input.AssignedTo, "appeal:work"); err != nil {
		if errors.Is(err, workflowactors.ErrAssigneeIneligible) {
			return PublishedQuestionAppeal{}, errors.Join(ErrForbidden, err)
		}
		return PublishedQuestionAppeal{}, err
	}
	// 以申诉冻结的原成绩查参与者，原始人工批阅教师不能复核自己已发布的成绩。
	if participated, err := publishedQuestionSourceReviewerParticipated(ctx, tx, tenantID, item.SourceFinalGradeID, input.AssignedTo); err != nil {
		return PublishedQuestionAppeal{}, err
	} else if participated {
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
