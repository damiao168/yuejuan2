package score

import (
	"context"
	"database/sql"
	"math"

	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
)

// 确认和旧版发布都核对当前终分；汇总后补齐的双评、仲裁不能沿用旧总分。
// 调用方在写入状态前持有考试锁，检查失败时要求重新汇总，不自动改动已确认事实。
// prepared=true 只用于发布前只读读取：按准备之后的状态核对，可重算的总分不算不一致；
// 首发前已确认记录只有确认后确有新终评才可恢复，已锁定总分仍严格核对。
func (s *PostgresStore) totalIntegrityIssuesTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, issues []QualityIssue, prepared bool) ([]QualityIssue, error) {
	const strict = `WITH latest AS (
 SELECT DISTINCT ON (answer_segment_id) submission_id,score,max_score
 FROM final_grade WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
 ORDER BY answer_segment_id,created_at DESC,id DESC
), totals AS (
 SELECT submission_id,SUM(score) AS score,SUM(max_score) AS max_score FROM latest GROUP BY submission_id
)
SELECT COUNT(*) FROM submission_grade grade
LEFT JOIN totals ON totals.submission_id=grade.submission_id
WHERE grade.tenant_id=$1::uuid AND grade.exam_id=$2::uuid AND grade.deleted_at IS NULL
AND (totals.submission_id IS NULL OR grade.total_score IS DISTINCT FROM totals.score OR grade.max_score IS DISTINCT FROM totals.max_score)`
	query := strict
	if prepared {
		// 现有终评与自动终评不会落在同一答题区，合并后即准备之后每个答题区的最新终评。
		query = `WITH ` + autoprep.FinalSourcesCTE + `, existing_latest AS (
 SELECT DISTINCT ON (answer_segment_id) submission_id,score,max_score
 FROM final_grade WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
 ORDER BY answer_segment_id,created_at DESC,id DESC
), latest AS (
 SELECT submission_id,score,max_score FROM existing_latest
 UNION ALL
 SELECT submission_id,score,max_score FROM automatic_final
), totals AS (
 SELECT submission_id,SUM(score) AS score,SUM(max_score) AS max_score FROM latest GROUP BY submission_id
)
SELECT COUNT(*) FROM submission_grade grade
LEFT JOIN totals ON totals.submission_id=grade.submission_id
WHERE grade.tenant_id=$1::uuid AND grade.exam_id=$2::uuid AND grade.deleted_at IS NULL
AND (totals.submission_id IS NULL OR grade.total_score IS DISTINCT FROM totals.score OR grade.max_score IS DISTINCT FROM totals.max_score)
AND NOT (` + autoprep.RecalculableAfterPreparationPredicate("grade") + ` AND totals.submission_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM submission sub WHERE sub.tenant_id=grade.tenant_id AND sub.id=grade.submission_id AND sub.deleted_at IS NULL))`
	}
	count, err := scalarCountTx(ctx, tx, query, tenantID, examID)
	if err != nil {
		return nil, err
	}
	return appendTotalIntegrityIssue(issues, count), nil
}

func appendTotalIntegrityIssue(issues []QualityIssue, count int) []QualityIssue {
	if count > 0 {
		issues = append(issues, QualityIssue{Code: "score_integrity_mismatch", Message: "submission totals differ from current final grades; finalize again before confirmation", Blocking: true, Count: count})
	}
	return issues
}

func (s *MemoryStore) totalIntegrityIssuesLocked(examID string, issues []QualityIssue) []QualityIssue {
	totals, maxima := map[string]float64{}, map[string]float64{}
	for _, gradeID := range s.finalBySeg {
		grade := s.finals[gradeID]
		if grade.ExamID == examID {
			totals[grade.SubmissionID] += grade.Score
			maxima[grade.SubmissionID] += grade.MaxScore
		}
	}
	count := 0
	for _, grade := range s.submissions {
		if grade.ExamID == examID && (math.Abs(grade.TotalScore-totals[grade.SubmissionID]) > 1e-9 || math.Abs(grade.MaxScore-maxima[grade.SubmissionID]) > 1e-9) {
			count++
		}
	}
	return appendTotalIntegrityIssue(issues, count)
}
