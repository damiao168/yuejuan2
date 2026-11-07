package regrade

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"encoding/json"
	"errors"
	"math"
)

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
	if item.Status == ItemClaimed && item.ClaimedBy == actorID && item.AssignedTo != "" {
		return item, nil
	}
	if item.Status != ItemPending && !(item.Status == ItemClaimed && item.ClaimedBy == actorID) {
		return Item{}, ErrStateConflict
	}
	// 已有分派继续按原身份工作；领取未分配条目会新增授权，必须重新核对本场考试学科。
	if item.AssignedTo == "" {
		if err := validateAssigneeTx(ctx, tx, tenantID, job.ExamID, actorID); err != nil {
			return Item{}, err
		}
	}
	// 父作业和条目已锁定；首次领取同时记录分配人，材料读取不能看到领取成功但身份缺失的中间态。
	// 本人重领旧的未分配记录会补齐身份；已有分配不因重试改变。
	item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE regrade_item SET status='claimed',assigned_to=COALESCE(assigned_to,$3::uuid),claimed_by=$3::uuid,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+itemReturning, tenantID, itemID, actorID))
	if err != nil {
		return Item{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: job.ID, ItemID: item.ID, Type: "item_claimed", ActorID: actorID, Payload: map[string]any{}}); err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}

func validateAssigneeTx(ctx context.Context, tx *sql.Tx, tenantID, examID, assigneeID string) error {
	err := workflowactors.ValidateReviewAssignee(ctx, tx, tenantID, examID, assigneeID, "review:work")
	if errors.Is(err, workflowactors.ErrAssigneeIneligible) {
		return errors.Join(ErrAssignmentForbidden, err)
	}
	return err
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
	// 候选分不是最终复核分；delta 留到 ReviewAccept 写入，保持“有正式分差就必须有 reviewed_score”的数据库约束。
	item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE regrade_item
SET candidate_grade_id=NULLIF($3,'')::uuid,candidate_score=$4,delta=NULL,
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
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: job.ID, ItemID: item.ID, Type: "candidate_recorded", ActorID: actorID, Payload: map[string]any{"requires_manual_review": input.RequireManualReview}}); err != nil {
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
	if err := reviewableItem(item, input); err != nil {
		return Item{}, err
	}
	payload := reviewEventPayload(item, input)
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
		args = []any{tenantID, itemID, actorID, input.Note}
		query = `UPDATE regrade_item SET reviewed_by=$3::uuid,reviewed_grade_id=NULL,reviewed_score=NULL,delta=NULL,status='resolved',review_note=$4,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING ` + itemReturning
	case ReviewException:
		// 异常条目保留待处理事实并阻止作业完成；参数只绑定本分支实际使用的字段。
		args = []any{tenantID, itemID, actorID, input.Note}
		query = `UPDATE regrade_item SET reviewed_by=$3::uuid,status='exception',review_note=$4,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING ` + itemReturning
	default:
		return Item{}, ErrInvalidInput
	}
	item, err = scanItem(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Item{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: job.ID, ItemID: item.ID, Type: "item_reviewed", ActorID: actorID, Payload: payload}); err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return item, nil
}
