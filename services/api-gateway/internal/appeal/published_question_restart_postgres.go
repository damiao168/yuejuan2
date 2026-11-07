package appeal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// RestartPublishedQuestionAppealReview 显式撤销过时、尚未公布的复评，再以当前正式成绩开始新一轮。
// 原申诉来源不变；新来源只写入系统生成的只追加事件，后续裁定和结案都从该事件读取。
func (s *PublishedQuestionAppealPostgresStore) RestartPublishedQuestionAppealReview(ctx context.Context, tenantID, appealID, actorID string, input RestartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	initial, err := s.GetPublishedQuestionAppeal(ctx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer tx.Rollback()
	// 发布命令先锁发布行、后锁考试。这里沿用顺序；NOWAIT 让并发发布先完成，不能互持锁等待。
	publishedIDs, err := lockRestartReleaseRows(ctx, tx, tenantID, initial.ExamID, effectiveReviewSourceID(initial))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, tenantID, initial.ExamID); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	// 等考试锁期间可能有新版本建成或公布；重新读当前事实。新草稿若正等待发布，用 NOWAIT 回退而非形成锁环。
	publishedIDs, err = lockRestartReleaseRows(ctx, tx, tenantID, initial.ExamID, effectiveReviewSourceID(initial))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	item, err := publishedQuestionAppealForUpdate(ctx, tx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if item.Revision != input.ExpectedRevision || item.RegradeJobID != initial.RegradeJobID {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	if item.Status != QuestionAppealUpheldPendingRegrade || item.RegradeJobID == "" {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	// 复评交分按明细→任务加锁；此处先锁所有明细，再锁任务，并用 NOWAIT 拒绝并发写入。
	rows, err := tx.QueryContext(ctx, `SELECT id FROM regrade_item WHERE tenant_id=$1 AND job_id=$2::uuid ORDER BY id FOR UPDATE NOWAIT`, tenantID, item.RegradeJobID)
	if err != nil {
		return PublishedQuestionAppeal{}, mapRestartLockError(err)
	}
	itemCount := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return PublishedQuestionAppeal{}, err
		}
		itemCount++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	var jobSource, jobStatus string
	err = tx.QueryRowContext(ctx, `SELECT source_release_id::text,status FROM regrade_job
WHERE tenant_id=$1 AND id=$2::uuid AND exam_id=$3::uuid AND question_id=$4::uuid FOR UPDATE NOWAIT`, tenantID, item.RegradeJobID, item.ExamID, item.QuestionID).Scan(&jobSource, &jobStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	if err != nil {
		return PublishedQuestionAppeal{}, mapRestartLockError(err)
	}
	// 一次重新处理只撤销本申请的单份任务；历史批量任务或被另一申请共用的任务不能整批取消。
	var shared bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_appeal WHERE tenant_id=$1 AND regrade_job_id=$2::uuid AND id<>$3::uuid)`, tenantID, item.RegradeJobID, item.ID).Scan(&shared); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if itemCount != 1 || shared {
		return PublishedQuestionAppeal{}, ErrRestartReviewSharedJob
	}
	var source restartReviewSource
	err = tx.QueryRowContext(ctx, `SELECT current.release_id::text,question.final_grade_id::text,question.score::float8,question.max_score::float8
FROM score_release_current current
JOIN score_release release ON release.tenant_id=current.tenant_id AND release.id=current.release_id AND release.status='published'
JOIN score_release_item student ON student.tenant_id=release.tenant_id AND student.release_id=release.id AND student.student_id=$3::uuid AND student.submission_id=$4::uuid
JOIN score_release_question question ON question.tenant_id=student.tenant_id AND question.release_id=student.release_id AND question.submission_id=student.submission_id AND question.question_id=$5::uuid
WHERE current.tenant_id=$1 AND current.exam_id=$2::uuid`, tenantID, item.ExamID, item.StudentID, item.SubmissionID, item.QuestionID).Scan(&source.ReleaseID, &source.FinalGradeID, &source.Score, &source.MaxScore)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (source.ReleaseID == jobSource || jobStatus == "cancelled") {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	// 已公布的结果只能通过正式更正/回滚处理，不能借“重新处理”撤销已经生效的复评。
	for _, id := range publishedIDs {
		if resolved, err := successorPublishedReleaseExists(ctx, tx, tenantID, item, id); err != nil {
			return PublishedQuestionAppeal{}, err
		} else if resolved {
			return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
		}
	}
	// 新键直接关联任务；旧随机键还通过本题已复核的评分来源回认，只作废该任务对应的未发布草稿。
	if _, err := tx.ExecContext(ctx, `UPDATE score_release draft SET status='discarded'
WHERE draft.tenant_id=$1 AND draft.exam_id=$2::uuid AND draft.source='regrade' AND draft.status='draft'
 AND draft.source_release_id=$3::uuid AND (
  draft.idempotency_key='regrade-job:'||$4 OR EXISTS (
   SELECT 1 FROM score_release_question question JOIN regrade_item regrade
    ON regrade.tenant_id=question.tenant_id AND regrade.job_id=$4::uuid AND regrade.submission_id=question.submission_id
   WHERE question.tenant_id=draft.tenant_id AND question.release_id=draft.id AND question.question_id=$5::uuid
    AND question.submission_id=$6::uuid AND regrade.reviewed_grade_id IS NOT NULL
    AND question.source_id=regrade.reviewed_grade_id AND question.score=COALESCE(regrade.reviewed_score,regrade.old_score)
  ))`, tenantID, item.ExamID, jobSource, item.RegradeJobID, item.QuestionID, item.SubmissionID); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regrade_item SET status='cancelled',revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND job_id=$2::uuid`, tenantID, item.RegradeJobID); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regrade_job SET status='cancelled',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, item.RegradeJobID); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	payload := map[string]any{"revision": item.Revision + 1, "previous_regrade_job_id": item.RegradeJobID, "previous_public_response": item.PublicResponse, "review_source_release_id": source.ReleaseID, "review_source_final_grade_id": source.FinalGradeID, "review_source_score": source.Score, "review_source_max_score": source.MaxScore, "reason": input.Reason}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regrade_event(tenant_id,job_id,event_type,actor_id,payload) VALUES($1,$2::uuid,'cancelled',$3::uuid,$4::jsonb)`, tenantID, item.RegradeJobID, actorID, encoded); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, item.ID, actorID, "review_restarted", payload); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	item, err = scanPublishedQuestionAppeal(tx.QueryRowContext(ctx, `UPDATE question_appeal
SET status='under_review',decision=NULL,public_response='',private_note='',regrade_job_id=NULL,decided_by=NULL,decided_at=NULL,updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+publishedQuestionAppealColumns(""), tenantID, item.ID))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	// 各操作者保留递增的草稿墓碑；旧页面不能在新一轮重新写回上一轮未提交的私有意见。
	if _, err := tx.ExecContext(ctx, `UPDATE private_opinion_draft SET draft_json=NULL,revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND kind='question-appeal' AND record_id=$2::uuid`, tenantID, item.ID); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

func mapRestartLockError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "55P03" {
		return ErrRestartReviewUnavailable
	}
	return err
}

func lockRestartReleaseRows(ctx context.Context, tx *sql.Tx, tenantID, examID, sourceID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,status FROM score_release
WHERE tenant_id=$1 AND exam_id=$2::uuid AND source='regrade' AND source_release_id=$3::uuid
ORDER BY id FOR UPDATE NOWAIT`, tenantID, examID, sourceID)
	if err != nil {
		return nil, mapRestartLockError(err)
	}
	defer rows.Close()
	var published []string
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		if status == "published" {
			published = append(published, id)
		}
	}
	return published, rows.Err()
}
