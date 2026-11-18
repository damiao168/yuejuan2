package appeal

// 来源分数与后续结案版本都从同租户的发布事实核对；辅助函数继续使用调用方事务，不自行提交状态。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

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

func publishedQuestionSourceReviewerParticipated(ctx context.Context, tx *sql.Tx, tenantID, finalGradeID, reviewerID string) (bool, error) {
	var participated bool
	// 仲裁提交直接生成终分，没有 human_grade。终分创建人是冻结的实际提交者，
	// 无需从可变的任务分派推断谁决定了被申诉的分数。
	err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM final_grade grade
  WHERE grade.tenant_id = $1 AND grade.id = $2::uuid AND grade.deleted_at IS NULL
    AND (
      (grade.source = 'arbitration' AND grade.arbitration_task_id IS NOT NULL AND grade.created_by = $3::uuid)
      OR EXISTS (
        SELECT 1 FROM human_grade human
        WHERE grade.tenant_id = human.tenant_id AND grade.answer_segment_id = human.answer_segment_id
          AND human.reviewer_id = $3::uuid AND human.deleted_at IS NULL
      )
    )
)
`, tenantID, finalGradeID, reviewerID).Scan(&participated)
	return participated, err
}

// 重评任务必须包含本轮来源的本份答卷；共享锁让关联命令不能越过并发重新处理而挂到已取消任务。
func matchingAppealRegradeJob(ctx context.Context, tx *sql.Tx, tenantID string, item PublishedQuestionAppeal, jobID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM (SELECT * FROM regrade_job WHERE tenant_id=$1 AND id=$2::uuid FOR SHARE) job
  JOIN regrade_item regrade ON regrade.tenant_id = job.tenant_id AND regrade.job_id = job.id
  WHERE job.tenant_id = $1 AND job.id = $2::uuid AND job.exam_id = $3::uuid
    AND job.question_id = $4::uuid AND job.source_release_id = $5::uuid
    AND job.status <> 'cancelled' AND regrade.status <> 'cancelled'
    AND regrade.submission_id = $6::uuid AND regrade.old_final_grade_id = $7::uuid
)
`, tenantID, jobID, item.ExamID, item.QuestionID, effectiveReviewSourceID(item), item.SubmissionID, effectiveReviewFinalGradeID(item)).Scan(&exists)
	return exists, err
}

// 结案必须等关联任务完成；新版从原发布派生，且包含同学生答卷题目的已复核结果。
// 发布表没有 job 外键，这里用来源版本、复核分数和评分来源核对已有的持久化事实。
func successorPublishedReleaseExists(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tenantID string, item PublishedQuestionAppeal, releaseID string) (bool, error) {
	var exists bool
	err := query.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM score_release successor
  JOIN score_release_item successor_item
    ON successor_item.tenant_id = successor.tenant_id AND successor_item.release_id = successor.id
  JOIN score_release_question successor_question
    ON successor_question.tenant_id = successor.tenant_id
   AND successor_question.release_id = successor.id
   AND successor_question.submission_id = successor_item.submission_id
  JOIN regrade_job job
    ON job.tenant_id = successor.tenant_id AND job.id = $8::uuid
   AND job.exam_id = successor.exam_id AND job.question_id = successor_question.question_id
   AND job.source_release_id = successor.source_release_id
  JOIN regrade_item regrade
    ON regrade.tenant_id = job.tenant_id AND regrade.job_id = job.id
   AND regrade.submission_id = successor_item.submission_id
  WHERE successor.tenant_id = $1 AND successor.id = $2::uuid
    AND successor.exam_id = $3::uuid AND successor.status = 'published'
    AND successor.version > $4
    AND successor_item.student_id = $5::uuid
    AND successor_item.submission_id = $6::uuid
    AND successor_question.question_id = $7::uuid
    AND successor.source = 'regrade' AND successor.source_release_id = $9::uuid
    AND job.status = 'ready_for_release' AND job.finalized_at IS NOT NULL
    AND successor.published_at >= job.finalized_at
    AND regrade.status = 'resolved' AND regrade.old_final_grade_id = $10::uuid
    AND successor_question.final_grade_id = regrade.old_final_grade_id
    AND successor_question.score = COALESCE(regrade.reviewed_score, regrade.old_score)
    AND successor_question.max_score = regrade.max_score
    AND (regrade.reviewed_score IS NULL OR
      (successor_question.source_type = 'single_review'
       AND successor_question.source_id IS NOT DISTINCT FROM regrade.reviewed_grade_id))
)
`, tenantID, releaseID, item.ExamID, item.SourceReleaseVersion, item.StudentID, item.SubmissionID, item.QuestionID, item.RegradeJobID, effectiveReviewSourceID(item), effectiveReviewFinalGradeID(item)).Scan(&exists)
	return exists, err
}

func publishedQuestionAppealColumns(prefix string) string {
	if prefix != "" {
		prefix += "."
	}
	appealRef := prefix
	if appealRef == "" {
		appealRef = "question_appeal."
	}
	// 关联版本从同租户的正式发布记录读取，不取当前指针；学生端另外用 release ID 核对是否当前生效。
	return prefix + `id::text, tenant_id::text, exam_id::text, student_id::text, submission_id::text,
source_release_id::text, source_release_version, question_id::text, question_no,
source_final_grade_id::text, source_score, source_max_score, reason_code, reason, selected_region,
status, COALESCE(assigned_to::text, ''), COALESCE(decision, ''), public_response, private_note,
COALESCE(regrade_job_id::text, ''), COALESCE(new_release_id::text, ''), created_by::text,
COALESCE(decided_by::text, ''), decided_at, created_at, updated_at, revision,
COALESCE((SELECT release.version FROM score_release release
  WHERE release.tenant_id = ` + appealRef + `tenant_id AND release.id = ` + appealRef + `new_release_id
    AND release.status = 'published'), 0),
(SELECT q.score::float8 FROM score_release_question q JOIN score_release r ON r.tenant_id=q.tenant_id AND r.id=q.release_id
 WHERE q.tenant_id=` + appealRef + `tenant_id AND q.release_id=` + appealRef + `new_release_id
 AND q.submission_id=` + appealRef + `submission_id AND q.question_id=` + appealRef + `question_id AND r.status='published'),
(SELECT q.max_score::float8 FROM score_release_question q JOIN score_release r ON r.tenant_id=q.tenant_id AND r.id=q.release_id
 WHERE q.tenant_id=` + appealRef + `tenant_id AND q.release_id=` + appealRef + `new_release_id
  AND q.submission_id=` + appealRef + `submission_id AND q.question_id=` + appealRef + `question_id AND r.status='published'),
(SELECT event.payload FROM question_appeal_event event
 WHERE event.tenant_id=` + appealRef + `tenant_id AND event.appeal_id=` + appealRef + `id
   AND event.event_type='review_opinion_submitted'
   AND COALESCE((event.payload->>'revision')::bigint,0)>COALESCE((SELECT max((restart.payload->>'revision')::bigint)
     FROM question_appeal_event restart WHERE restart.tenant_id=event.tenant_id AND restart.appeal_id=event.appeal_id AND restart.event_type='review_restarted'),0)
 ORDER BY (event.payload->>'revision')::bigint DESC LIMIT 1),
(SELECT restart.payload FROM question_appeal_event restart
 WHERE restart.tenant_id=` + appealRef + `tenant_id AND restart.appeal_id=` + appealRef + `id AND restart.event_type='review_restarted'
 ORDER BY (restart.payload->>'revision')::bigint DESC LIMIT 1)`
}

type publishedQuestionAppealScanner interface{ Scan(...any) error }

func scanPublishedQuestionAppeal(scanner publishedQuestionAppealScanner) (PublishedQuestionAppeal, error) {
	var item PublishedQuestionAppeal
	var selectedRegion, reviewOpinion, restartSource []byte
	var decidedAt sql.NullTime
	var reviewedScore, reviewedMax sql.NullFloat64
	err := scanner.Scan(
		&item.ID, &item.TenantID, &item.ExamID, &item.StudentID, &item.SubmissionID,
		&item.SourceReleaseID, &item.SourceReleaseVersion, &item.QuestionID, &item.QuestionNo,
		&item.SourceFinalGradeID, &item.SourceScore, &item.SourceMaxScore, &item.ReasonCode, &item.Reason, &selectedRegion,
		&item.Status, &item.AssignedTo, &item.Decision, &item.PublicResponse, &item.PrivateNote,
		&item.RegradeJobID, &item.NewReleaseID, &item.CreatedBy, &item.DecidedBy, &decidedAt,
		&item.CreatedAt, &item.UpdatedAt, &item.Revision,
		&item.NewReleaseVersion,
		&reviewedScore, &reviewedMax, &reviewOpinion, &restartSource,
	)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	// 历史复核分来自关联的不可变发布；后来切换当前指针不会改写该分数。
	if reviewedScore.Valid && reviewedMax.Valid {
		item.ReviewedScore, item.ReviewedMaxScore = &reviewedScore.Float64, &reviewedMax.Float64
	}
	if err := json.Unmarshal(selectedRegion, &item.SelectedRegion); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if decidedAt.Valid {
		item.DecidedAt = &decidedAt.Time
	}
	if len(reviewOpinion) > 0 {
		if err := json.Unmarshal(reviewOpinion, &item.ReviewOpinion); err != nil {
			return PublishedQuestionAppeal{}, err
		}
	}
	if len(restartSource) > 0 {
		var source restartReviewSource
		if err := json.Unmarshal(restartSource, &source); err != nil {
			return PublishedQuestionAppeal{}, err
		}
		applyRestartReviewSource(&item, source)
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
