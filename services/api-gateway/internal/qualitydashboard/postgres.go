package qualitydashboard

import (
	"context"
	"database/sql"
)

// PostgresQuestionReader 只读取题目元数据和冻结的评估分类。
// 它与仪表盘服务分开，使测试或未来只读副本可使用其他存储。
type PostgresQuestionReader struct{ db *sql.DB }

func NewPostgresQuestionReader(db *sql.DB) *PostgresQuestionReader {
	return &PostgresQuestionReader{db: db}
}

func (r *PostgresQuestionReader) ListQualityQuestions(ctx context.Context, tenantID, examID string) ([]Question, error) {
	rows, err := r.db.QueryContext(ctx, `
WITH latest_snapshot AS (
  SELECT DISTINCT ON (question_id) question_id, archetype_code, risk_tier
  FROM exam_question_snapshot
  WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
  ORDER BY question_id, snapshot_version DESC
)
SELECT q.id::text, q.question_no,
       COALESCE(snapshot.archetype_code, config.archetype_code, ''),
       COALESCE(snapshot.risk_tier, config.risk_tier, ''),
       q.score::float8
FROM question q
LEFT JOIN latest_snapshot snapshot ON snapshot.question_id=q.id
LEFT JOIN question_assessment_config config
  ON config.tenant_id=q.tenant_id AND config.exam_id=q.exam_id AND config.question_id=q.id
WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL
ORDER BY q.sort_order, q.question_no, q.id`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Question{}
	for rows.Next() {
		var item Question
		if err := rows.Scan(&item.ID, &item.QuestionNo, &item.ArchetypeCode, &item.RiskTier, &item.MaxScore); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// PostgresCalibrationReader 只返回面向管理员的聚合和会话状态。
// 它不读取 Gold 样本清单、尝试分数或评分细则选择，避免此端点泄漏校准答案材料。
type PostgresCalibrationReader struct{ db *sql.DB }

func NewPostgresCalibrationReader(db *sql.DB) *PostgresCalibrationReader {
	return &PostgresCalibrationReader{db: db}
}

func (r *PostgresCalibrationReader) GetCalibrationSummary(ctx context.Context, tenantID, examID, questionID string) (CalibrationSummary, error) {
	result := CalibrationSummary{Graders: []CalibrationGrader{}}
	if err := r.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM grader_calibration_policy
  WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
)`, tenantID, examID, questionID).Scan(&result.Configured); err != nil {
		return CalibrationSummary{}, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT grader_id::text, status,
       COALESCE((metrics_json->>'sample_count')::int, 0)
FROM grader_calibration_session
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
ORDER BY completed_at DESC NULLS LAST, started_at DESC, id DESC`, tenantID, examID, questionID)
	if err != nil {
		return CalibrationSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item CalibrationGrader
		if err := rows.Scan(&item.GraderRef, &item.Status, &item.SampleSize); err != nil {
			return CalibrationSummary{}, err
		}
		result.Graders = append(result.Graders, item)
		if item.Status == "passed" || item.Status == "failed" {
			result.Completed++
			if item.Status == "passed" {
				result.Passed++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return CalibrationSummary{}, err
	}
	result.PassRate = ratio(result.Passed, result.Completed)
	return result, nil
}
