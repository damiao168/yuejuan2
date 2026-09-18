package paper

import (
	"context"
	"fmt"
)

func (s *PostgresStore) ValidateConfig(ctx context.Context, tenantID string, examID string) (ValidationResult, error) {
	result := ValidationResult{Valid: true, Issues: []ValidationIssue{}}
	var examTotal float64
	if err := s.db.QueryRowContext(ctx, `SELECT total_score::float8 FROM exam WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, examID).Scan(&examTotal); err != nil {
		return ValidationResult{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT question_no, question_type, score::float8, answer_area FROM question WHERE tenant_id = $1 AND exam_id = $2 AND deleted_at IS NULL AND status <> 'deleted'`, tenantID, examID)
	if err != nil {
		return ValidationResult{}, err
	}
	defer rows.Close()
	count := 0
	total := 0.0
	for rows.Next() {
		var no, kind string
		var score float64
		var area []byte
		if err := rows.Scan(&no, &kind, &score, &area); err != nil {
			return ValidationResult{}, err
		}
		count++
		total += score
		if no == "" || kind == "" {
			result.Issues = append(result.Issues, ValidationIssue{Code: "question_incomplete", Message: "题目缺少题号或题型"})
		}
		if len(area) == 0 || string(area) == "null" {
			result.Issues = append(result.Issues, ValidationIssue{Code: "answer_area_missing", Message: "第" + no + "题缺少答题区域"})
		}
	}
	if count == 0 {
		result.Issues = append(result.Issues, ValidationIssue{Code: "no_questions", Message: "试卷尚未配置题目"})
	}
	if !scoreEqual(total, examTotal) {
		result.Issues = append(result.Issues, ValidationIssue{Code: "total_score_mismatch", Message: fmt.Sprintf("题目总分 %.2f 分与考试总分 %.2f 分不一致", total, examTotal)})
	}
	result.Valid = len(result.Issues) == 0
	return result, rows.Err()
}
