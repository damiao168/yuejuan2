package review

// 提交继续先锁父评分运行再锁任务；命令回执、正式成绩、草稿清理和运行计数沿用同一事务。
import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) SubmitGrade(ctx context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput) (SubmitResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmitResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay SubmitResult
	identity := commandreceipt.Identity{TenantID: tenantID, ActorID: reviewerID, Operation: "review.submit", TargetID: id}
	// 已成功的命令先返回原回执；任务后来已提交或删除，也不应让客户端把成功误判为失败。
	if found, err := commandreceipt.Load(ctx, tx, identity, input, &replay); err != nil || found {
		return replay, err
	}
	// 回执未命中后，必须先锁父评分运行，再执行下面的任务行锁。
	runID, readOnlyParent, err := lockScoringRunForGradeSubmissionTx(ctx, tx, tenantID, id, input.AutoSubmit)
	if err != nil {
		return SubmitResult{}, err
	}
	task, err := scanTask(tx.QueryRowContext(ctx, `
	SELECT `+reviewTaskColumns+`
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return SubmitResult{}, err
	}
	if task.AssignedTo != reviewerID {
		return SubmitResult{}, ErrForbidden
	}
	if input.AutoSubmit && submittedReviewStatus(task.Status) {
		// 重新打开已提交任务只能读回本人正式分；传入的新分数和旧版本号均不构成改分授权。
		grade, err := scanGrade(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, review_task_id::text, answer_segment_id::text, reviewer_id::text,
  score::float8, max_score::float8, rubric_selections, comments, private_note, student_feedback,
  reason, grade_round, COALESCE(ai_grade_id::text, ''), created_at
FROM human_grade WHERE tenant_id=$1::uuid AND review_task_id=$2::uuid AND reviewer_id=$3::uuid AND deleted_at IS NULL
ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID, task.ID, reviewerID))
		if err != nil {
			return SubmitResult{}, err
		}
		result := SubmitResult{Task: task, Grade: grade}
		if err := commandreceipt.Save(ctx, tx, identity, input, result); err != nil {
			return SubmitResult{}, err
		}
		return result, tx.Commit()
	}
	if readOnlyParent {
		return SubmitResult{}, ErrInvalidTransition
	}
	if !canSubmit(task.Status) {
		return SubmitResult{}, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || task.Revision != input.ExpectedRevision {
		return SubmitResult{}, ErrRevisionConflict
	}
	if input.AutoSubmit {
		// 与标记 Put 共用父运行→任务锁；持有任务锁后再查标记，首次新增也不能插入检查与写分之间。
		var marked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND review_task_id=$3::uuid AND deleted_at IS NULL)`, tenantID, reviewerID, task.ID).Scan(&marked); err != nil {
			return SubmitResult{}, err
		}
		if marked {
			return SubmitResult{}, ErrTaskMarked
		}
	}
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, task.AnswerSegmentID)
	if err != nil {
		return SubmitResult{}, err
	}
	if err := validateSubmit(input, taskContext); err != nil {
		return SubmitResult{}, err
	}
	if err := requireSubmissionQualification(ctx, tenantID, reviewerID, task, taskContext); err != nil {
		return SubmitResult{}, err
	}
	selections, _ := json.Marshal(input.RubricSelections)
	grade, err := scanGrade(tx.QueryRowContext(ctx, `
INSERT INTO human_grade (
  tenant_id, review_task_id, answer_segment_id, exam_question_snapshot_id, reviewer_id, score, max_score,
  rubric_selections, comments, private_note, student_feedback, reason, grade_round, ai_grade_id
)
VALUES ($1, $2, $3, (SELECT eqs.id FROM answer_segment seg JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id WHERE seg.tenant_id=$1::uuid AND seg.id=$3::uuid), $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, '')::uuid)
RETURNING id::text, tenant_id::text, review_task_id::text, answer_segment_id::text, reviewer_id::text,
  score::float8, max_score::float8, rubric_selections, comments, private_note, student_feedback,
  reason, grade_round, COALESCE(ai_grade_id::text, ''), created_at
`, tenantID, task.ID, task.AnswerSegmentID, reviewerID, input.Score, taskContext.Question.Score,
		selections, stringsTrim(input.Comments), stringsTrim(input.PrivateNote),
		stringsTrim(input.StudentFeedback), stringsTrim(input.Reason), task.GradeRound,
		aiGradeIDFromContext(taskContext)))
	if err != nil {
		return SubmitResult{}, err
	}
	questionGradeID := ""
	// 单评直接替换当前题目成绩；双评先保存各自的人工评分，等合分或仲裁后再产生当前成绩。
	if task.GradeRound == "single" {
		evidence, marshalErr := json.Marshal(map[string]any{
			"human_grade_id":    grade.ID,
			"rubric_selections": input.RubricSelections,
			"comments":          stringsTrim(input.Comments),
			"student_feedback":  stringsTrim(input.StudentFeedback),
			"reason":            stringsTrim(input.Reason),
		})
		if marshalErr != nil {
			return SubmitResult{}, marshalErr
		}
		var priorID string
		_ = tx.QueryRowContext(ctx, `SELECT id::text FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL FOR UPDATE`, tenantID, task.AnswerSegmentID).Scan(&priorID)
		if priorID != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='superseded' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, priorID); err != nil {
				return SubmitResult{}, err
			}
		}
		err = tx.QueryRowContext(ctx, `
INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,review_task_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
SELECT rt.tenant_id,rt.exam_id,rt.submission_id,rt.question_id,rt.answer_segment_id,rt.scoring_run_id,eqs.id,rt.id,'human','confirmed',$3,$4,$5::jsonb,
  (SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=rt.answer_segment_id),NULLIF($6,'')::uuid,true,$7::uuid
FROM review_task rt JOIN exam_question_snapshot eqs ON eqs.tenant_id=rt.tenant_id AND eqs.exam_id=rt.exam_id AND eqs.question_id=rt.question_id WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid
RETURNING id::text`, tenantID, task.ID, input.Score, taskContext.Question.Score, evidence, priorID, reviewerID).Scan(&questionGradeID)
		if err != nil {
			return SubmitResult{}, err
		}
	}
	task, err = scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET status = 'submitted', current_grade_id = NULLIF($3,'')::uuid,
  claim_expires_at = NULL, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4
RETURNING `+reviewTaskColumns+`
`, tenantID, id, questionGradeID, input.ExpectedRevision))
	if err != nil {
		return SubmitResult{}, err
	}
	// 提交成绩与清除本人草稿处于同一事务，失败回滚后草稿仍可继续使用。
	if _, err = tx.ExecContext(ctx, `UPDATE review_draft SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND review_task_id=$2::uuid AND reviewer_id=$3::uuid AND deleted_at IS NULL`, tenantID, task.ID, reviewerID); err != nil {
		return SubmitResult{}, err
	}
	if task.GradeRound == "single" {
		// 人工成绩、逐题状态和批次计数一并提交，避免总数已完成而任务仍显示待处理；历史取消条目不能复活。
		if _, err = tx.ExecContext(ctx, `WITH confirmed_items AS (
UPDATE scoring_run_item item SET processing_status='succeeded',decision_status='human_confirmed',stage='done',
reason_code='',next_action='none',retryable=false,revision=item.revision+1,updated_at=now()
FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND item.tenant_id=rt.tenant_id
AND item.scoring_run_id=rt.scoring_run_id AND item.answer_segment_id=rt.answer_segment_id
AND item.processing_status NOT IN ('cancelled','superseded') AND item.decision_status<>'human_confirmed'
RETURNING item.id
) UPDATE scoring_run sr SET review_count=GREATEST(0,sr.review_count-1),human_confirmed_count=sr.human_confirmed_count+1,
status=CASE WHEN sr.queued_count=0 AND GREATEST(0,sr.review_count-1)=0 AND sr.failed_count=0 THEN 'completed' WHEN sr.queued_count=0 THEN 'needs_review' ELSE sr.status END,
completed_at=CASE WHEN sr.queued_count=0 AND GREATEST(0,sr.review_count-1)=0 AND sr.failed_count=0 THEN now() ELSE sr.completed_at END,updated_at=now()
FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND rt.scoring_run_id=sr.id AND sr.tenant_id=rt.tenant_id`, tenantID, task.ID); err != nil {
			return SubmitResult{}, err
		}
	}
	session, finalGrade, arbitrationTask, err := s.resolveDoubleMarkAfterGradeTx(ctx, tx, tenantID, reviewerID, task)
	if err != nil {
		return SubmitResult{}, err
	}
	if runID.Valid && (task.GradeRound == "first_mark" || task.GradeRound == "second_mark") {
		questionGradeID, err = s.syncSubmittedDoubleMarkTx(ctx, tx, tenantID, runID.String, task.ID, reviewerID, finalGrade)
		if err != nil {
			return SubmitResult{}, err
		}
	}
	if refreshed, refreshErr := s.getTaskTx(ctx, tx, tenantID, id); refreshErr == nil {
		task = refreshed
	}
	result := SubmitResult{
		Task:              task,
		Grade:             grade,
		QuestionGradeID:   questionGradeID,
		DoubleMarkSession: session,
		FinalGrade:        finalGrade,
		ArbitrationTask:   arbitrationTask,
	}

	if err := commandreceipt.Save(ctx, tx, identity, input, result); err != nil {
		return SubmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

// 先锁父评分运行再锁任务，与取消和标记写入保持同一锁序；已取消的运行不能再写入成绩。
// 已完成父运行只允许自动请求读回已提交分数，调用方必须在锁住任务后再次核对终态。
func lockScoringRunForGradeSubmissionTx(ctx context.Context, tx *sql.Tx, tenantID, id string, autoSubmit bool) (sql.NullString, bool, error) {
	var runID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT scoring_run_id::text FROM review_task WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runID, false, ErrNotFound
		}
		return runID, false, err
	}
	if runID.Valid {
		var runStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID.String).Scan(&runStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return runID, false, ErrInvalidTransition
			}
			return runID, false, err
		}
		if runStatus == "completed" && autoSubmit {
			return runID, true, nil
		}
		if runStatus != "queued" && runStatus != "processing" && runStatus != "needs_review" && runStatus != "failed" {
			return runID, false, ErrInvalidTransition
		}
	}
	return runID, false, nil
}

// 双评尚无终分也同步待复核数；已有终分先写当前成绩，写入失败不能继续更新运行统计。
func (s *PostgresStore) syncSubmittedDoubleMarkTx(ctx context.Context, tx *sql.Tx, tenantID, runID, reviewTaskID, reviewerID string, finalGrade *FinalGrade) (string, error) {
	questionGradeID := ""
	if finalGrade != nil {
		var err error
		questionGradeID, err = s.recordScoringDoubleMarkGradeTx(ctx, tx, tenantID, runID, reviewTaskID, reviewerID, finalGrade)
		if err != nil {
			return "", err
		}
	}
	if err := s.syncScoringDoubleMarkRunTx(ctx, tx, tenantID, runID); err != nil {
		return "", err
	}
	return questionGradeID, nil
}
