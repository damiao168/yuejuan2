package score

import (
	"context"
	"database/sql"

	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
)

// PrepareForReleaseTx 在建发布快照的同一事务中补齐总分并确认可发布事实。
// 调用方持有考试锁并负责提交；已有正式发布后不再自动汇总，避免覆盖复评更正。
// ready=false 表示现有业务门禁未通过，调用方必须回滚本次准备和快照。
func (s *PostgresStore) PrepareForReleaseTx(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID string) (ready bool, err error) {
	if tx == nil || tenantID == "" || examID == "" || actorID == "" {
		return false, ErrInvalidInput
	}
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return false, err
	}
	if err := autoprep.LockCollection(ctx, tx, tenantID, examID); err != nil {
		return false, err
	}
	// 前提与发布前只读检查共用 autoprep.ReadState；这里在考试锁内读取，后续写入以它为准。
	state, err := autoprep.ReadState(ctx, tx, tenantID, examID)
	if err != nil {
		return false, err
	}
	if state.Published {
		return true, nil
	}
	if state.Invalid {
		return false, nil
	}
	locked := state.Locked
	if !locked {
		if _, err := s.createFinalsFromHumanTx(ctx, tx, tenantID, examID, actorID); err != nil {
			return false, err
		}
		if _, err := s.createFinalsFromRuleTx(ctx, tx, tenantID, examID, actorID); err != nil {
			return false, err
		}
		// 待确认总分正常重算；首发前确认之后新落下的双评、仲裁也可重新汇总，其他已确认不一致仍拒绝。
		if err := s.recalculateSubmissionGradesTx(ctx, tx, tenantID, examID, actorID); err != nil {
			return false, err
		}
	}
	count, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
	if err != nil || count == 0 {
		return false, err
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil || !quality.Passed {
		return false, err
	}
	if locked {
		// 旧版公开后已经锁定的成绩只做读取核对，不再混入自动确认写操作。
		return true, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE submission_grade
SET status='confirmed',confirmed_by=$3::uuid,confirmed_at=now(),revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND exam_id::text=$2 AND deleted_at IS NULL AND locked=false AND status='pending_confirmation'`, tenantID, examID, actorID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE final_grade SET status='confirmed',updated_at=now()
WHERE tenant_id=$1 AND exam_id::text=$2 AND deleted_at IS NULL AND locked=false AND status='pending_confirmation'`, tenantID, examID); err != nil {
		return false, err
	}
	return true, nil
}
