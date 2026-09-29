package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) LoadContext(ctx context.Context, tenantID string, gradeID string) (Context, error) {
	// 查询同时带租户和成绩删除状态，避免验证任务跨租户读取已删除的答卷或评分依据。
	row := s.db.QueryRowContext(ctx, `
SELECT
  g.id::text, g.tenant_id::text, g.answer_segment_id::text, g.question_id::text, g.question_no, g.question_type,
  g.suggested_score::float8, g.max_score::float8, g.matched_points, g.missing_points, g.evidence, g.risk_flags,
  g.needs_human_review, g.status,
  seg.bbox,
  COALESCE(ans.answer_text, ''), ans.confidence::float8,
  COALESCE(qr.id::text, ''), COALESCE(qr.question_id::text, ''), COALESCE(rv.version, ''),
  COALESCE(qr.status, ''), COALESCE(qr.max_score::float8, 0), COALESCE(qr.points, '[]'::jsonb),
  COALESCE(qr.deductions, '[]'::jsonb), COALESCE(qr.examples, '[]'::jsonb)
FROM ai_grade g
JOIN answer_segment seg ON seg.tenant_id = g.tenant_id AND seg.id = g.answer_segment_id
LEFT JOIN LATERAL (
  SELECT answer_text, confidence
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT qr.id, qr.question_id, qr.status, qr.max_score, qr.points, qr.deductions, qr.examples, qr.rubric_version_id
  FROM question_rubric qr
  WHERE qr.tenant_id = g.tenant_id AND qr.question_id = g.question_id AND qr.deleted_at IS NULL
  ORDER BY qr.created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = g.tenant_id AND rv.id = qr.rubric_version_id
WHERE g.tenant_id = $1 AND g.id::text = $2 AND g.deleted_at IS NULL AND seg.deleted_at IS NULL
`, tenantID, gradeID)
	var out Context
	var matchedRaw, missingRaw, evidenceRaw, risksRaw, segmentBBoxRaw []byte
	var answerConfidence sql.NullFloat64
	var rubric paper.Rubric
	var pointsRaw, deductionsRaw, examplesRaw []byte
	if err := row.Scan(
		&out.Grade.ID,
		&out.Grade.TenantID,
		&out.Grade.AnswerSegmentID,
		&out.Grade.QuestionID,
		&out.Grade.QuestionNo,
		&out.Grade.QuestionType,
		&out.Grade.SuggestedScore,
		&out.Grade.MaxScore,
		&matchedRaw,
		&missingRaw,
		&evidenceRaw,
		&risksRaw,
		&out.Grade.NeedsHumanReview,
		&out.Grade.Status,
		&segmentBBoxRaw,
		&out.AnswerText,
		&answerConfidence,
		&rubric.ID,
		&rubric.QuestionID,
		&rubric.Version,
		&rubric.Status,
		&rubric.MaxScore,
		&pointsRaw,
		&deductionsRaw,
		&examplesRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	_ = json.Unmarshal(matchedRaw, &out.Grade.MatchedPoints)
	_ = json.Unmarshal(missingRaw, &out.Grade.MissingPoints)
	_ = json.Unmarshal(evidenceRaw, &out.Grade.Evidence)
	_ = json.Unmarshal(risksRaw, &out.Grade.RiskFlags)
	_ = json.Unmarshal(segmentBBoxRaw, &out.AnswerSegmentBBox)
	if answerConfidence.Valid {
		value := answerConfidence.Float64
		out.OCRConfidence = &value
	}
	_ = json.Unmarshal(pointsRaw, &rubric.Points)
	_ = json.Unmarshal(deductionsRaw, &rubric.Deductions)
	_ = json.Unmarshal(examplesRaw, &rubric.Examples)
	out.Rubric = rubric
	return out, nil
}

func (s *PostgresStore) CreateJob(ctx context.Context, tenantID string, actorID string, gradeID string, result VerificationResult) (AgentJob, error) {
	// 验证结果作为审计快照落库；状态只由结果决定，后续人工处理不改变原始 AI 成绩。
	raw, _ := json.Marshal(result)
	row := s.db.QueryRowContext(ctx, `
INSERT INTO agent_job (tenant_id, job_type, target_type, target_id, status, result, needs_human_review, created_by)
VALUES ($1, 'evidence_check', 'ai_grade', $2, $3, $4, $5, $6)
RETURNING id::text, tenant_id::text, job_type, target_type, target_id::text, status, result, needs_human_review, created_by::text, created_at
`, tenantID, gradeID, statusFromResult(result), raw, result.NeedsHumanReview, actorID)
	var out AgentJob
	var resultRaw []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.JobType,
		&out.TargetType,
		&out.TargetID,
		&out.Status,
		&resultRaw,
		&out.NeedsHumanReview,
		&out.CreatedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentJob{}, ErrNotFound
		}
		return AgentJob{}, err
	}
	_ = json.Unmarshal(resultRaw, &out.Result)
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}
