package score

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
	"fmt"
)

func (s *PostgresStore) FinalizeExam(ctx context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FinalizeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	} else if locked {
		return FinalizeResult{}, ErrInvalidTransition
	}
	state, err := autoprep.ReadState(ctx, tx, tenantID, examID)
	if err != nil {
		return FinalizeResult{}, err
	}
	if state.Published {
		return FinalizeResult{}, ErrInvalidTransition
	}
	if invalid, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade grade
WHERE grade.tenant_id=$1 AND grade.exam_id::text=$2 AND grade.deleted_at IS NULL
  AND NOT (`+autoprep.RecalculableTotalPredicate("grade")+`)`, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	} else if invalid > 0 {
		return FinalizeResult{}, ErrInvalidTransition
	}
	createdHuman, err := s.createFinalsFromHumanTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from human: %w", err)
	}
	createdRule, err := s.createFinalsFromRuleTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from rule: %w", err)
	}
	if err := s.recalculateSubmissionGradesTx(ctx, tx, tenantID, examID, actorID); err != nil {
		return FinalizeResult{}, fmt.Errorf("recalculate submission grades: %w", err)
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("quality report: %w", err)
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("list grades: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FinalizeResult{}, err
	}
	return FinalizeResult{
		Status:            "pending_confirmation",
		CreatedFinals:     int(createdHuman + createdRule),
		SubmissionGrades:  grades,
		Quality:           quality,
		AvailableStatuses: Statuses(),
	}, nil
}

// createFinalsFromHumanTx 与 createFinalsFromRuleTx 只从 autoprep.FinalSourcesCTE 插入终评。
// 发布前的只读检查用同一片段判断“可直接生成的终评”，修改条件时只改那一处。
// 调用顺序必须先人工后规则：规则来源按“尚无终评”跳过已由人工补齐的答题区。
func (s *PostgresStore) createFinalsFromHumanTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `WITH `+autoprep.FinalSourcesCTE+`
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'single_review', 'pending_confirmation', false, $3::uuid
FROM automatic_human_final
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) createFinalsFromRuleTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `WITH `+autoprep.FinalSourcesCTE+`
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'rule_auto', 'pending_confirmation', false, $3::uuid
FROM automatic_rule_final
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) recalculateSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) error {
	// 首发前，确认后新增的双评或仲裁终分可让旧总分重新进入待确认；没有新终评的已确认记录不改写。
	_, err := tx.ExecContext(ctx, `
WITH latest_final AS (
  SELECT DISTINCT ON (answer_segment_id)
    tenant_id, exam_id, submission_id, anonymous_code, score, max_score
  FROM final_grade
  WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  ORDER BY answer_segment_id, created_at DESC, id DESC
),
summed AS (
  SELECT
    lf.tenant_id,
    lf.exam_id,
    lf.submission_id,
    COALESCE(sub.student_id, NULL) AS student_id,
    COALESCE(NULLIF(sub.candidate_no, ''), MIN(lf.anonymous_code)) AS anonymous_code,
    SUM(lf.score) AS total_score,
    SUM(lf.max_score) AS max_score
  FROM latest_final lf
  JOIN submission sub ON sub.tenant_id = lf.tenant_id AND sub.id = lf.submission_id AND sub.deleted_at IS NULL
  GROUP BY lf.tenant_id, lf.exam_id, lf.submission_id, sub.student_id, sub.candidate_no
)
INSERT INTO submission_grade (
  tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, status, locked, created_by
)
SELECT tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, 'pending_confirmation', false, $3::uuid
FROM summed
ON CONFLICT (tenant_id, exam_id, submission_id) DO UPDATE
SET total_score = EXCLUDED.total_score,
    max_score = EXCLUDED.max_score,
    anonymous_code = EXCLUDED.anonymous_code,
    student_id = EXCLUDED.student_id,
    status = CASE WHEN submission_grade.locked THEN submission_grade.status ELSE 'pending_confirmation' END,
    confirmed_by = NULL,
    confirmed_at = NULL,
    revision = submission_grade.revision + 1,
    updated_at = now()
WHERE `+autoprep.RecalculableTotalPredicate("submission_grade")+`
`, tenantID, examID, actorID)
	return err
}
