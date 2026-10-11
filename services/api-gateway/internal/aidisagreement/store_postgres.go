package aidisagreement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// LoadActualComparison 从租户范围内的持久化事实派生全部分数和证据摘要。
// 仅接受成功、非模拟且由服务端关联到单轮人工评分的主观题 AI 输出，调用方不能用提交的 JSON 伪造分歧。
func (s *PostgresStore) LoadActualComparison(ctx context.Context, tenantID string, input CaptureInput) (ActualComparison, error) {
	if s.db == nil {
		return ActualComparison{}, ErrInvalidInput
	}
	var item ActualComparison
	err := s.db.QueryRowContext(ctx, `
SELECT rt.exam_id::text,rt.question_id::text,rt.submission_id::text,rt.answer_segment_id::text,
       ai.id::text,hg.id::text,ai.suggested_score::float8,hg.score::float8,hg.max_score::float8,
       ai.confidence::float8,jsonb_array_length(ai.evidence),jsonb_array_length(ai.matched_points),
       jsonb_array_length(hg.rubric_selections),COALESCE(snapshot.risk_tier,'R2')
FROM human_grade hg
JOIN review_task rt ON rt.tenant_id=hg.tenant_id AND rt.id=hg.review_task_id AND rt.deleted_at IS NULL
JOIN ai_grade ai ON ai.tenant_id=hg.tenant_id AND ai.id=hg.ai_grade_id
LEFT JOIN LATERAL (
  SELECT risk_tier FROM exam_question_snapshot
  WHERE tenant_id=rt.tenant_id AND exam_id=rt.exam_id AND question_id=rt.question_id
  ORDER BY snapshot_version DESC LIMIT 1
) snapshot ON true
WHERE hg.tenant_id=$1::uuid AND hg.id=$2::uuid AND ai.id=$3::uuid
  AND hg.deleted_at IS NULL AND ai.deleted_at IS NULL
  AND hg.grade_round='single' AND ai.status='succeeded' AND ai.mock=false
  AND ai.grader_type='llm_subjective' AND ai.answer_segment_id=rt.answer_segment_id
  AND ai.question_id=rt.question_id AND ai.max_score=hg.max_score`, tenantID, input.HumanGradeID, input.AICandidateID).Scan(
		&item.ExamID, &item.QuestionID, &item.SubmissionID, &item.AnswerSegmentID, &item.AICandidateID, &item.HumanGradeID,
		&item.AISuggestedScore, &item.HumanScore, &item.MaxScore, &item.AIConfidence, &item.AIEvidenceCount,
		&item.AIMatchedCriterionCount, &item.HumanCriterionCount, &item.RiskTier,
	)
	return item, mapStoreError(err)
}

func (s *PostgresStore) Upsert(ctx context.Context, tenantID string, item Disagreement) (Disagreement, bool, error) {
	if s.db == nil {
		return Disagreement{}, false, ErrInvalidInput
	}
	rules, err := json.Marshal(item.TriggerRules)
	if err != nil {
		return Disagreement{}, false, err
	}
	summary, err := json.Marshal(item.EvidenceSummary)
	if err != nil {
		return Disagreement{}, false, err
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO ai_human_disagreement(
 id,tenant_id,exam_id,question_id,submission_id,answer_segment_id,ai_candidate_id,human_grade_id,
 ai_candidate_score,human_score,max_score,delta,absolute_delta,difference_type,severity,risk_tier,
 trigger_rules_json,evidence_summary_json,status,revision,created_at,updated_at
) VALUES (
 $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8::uuid,
 $9,$10,$11,$12,$13,$14,$15,$16,$17::jsonb,$18::jsonb,'needs_review',1,$19,$19
)
ON CONFLICT (tenant_id,ai_candidate_id,human_grade_id) DO NOTHING
RETURNING `+disagreementColumns,
		item.ID, tenantID, item.ExamID, item.QuestionID, item.SubmissionID, item.AnswerSegmentID, item.AICandidateID, item.HumanGradeID,
		item.AICandidateScore, item.HumanScore, item.MaxScore, item.Delta, item.AbsoluteDelta, item.DifferenceType, item.Severity,
		item.RiskTier, rules, summary, item.CreatedAt.UTC())
	stored, err := scanDisagreement(row)
	if errors.Is(err, sql.ErrNoRows) {
		existing, getErr := s.getBySource(ctx, tenantID, item.AICandidateID, item.HumanGradeID)
		return existing, false, getErr
	}
	return stored, true, mapStoreError(err)
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id string) (Disagreement, error) {
	if s.db == nil {
		return Disagreement{}, ErrInvalidInput
	}
	item, err := scanDisagreement(s.db.QueryRowContext(ctx, `SELECT `+disagreementColumns+` FROM ai_human_disagreement WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id))
	return item, mapStoreError(err)
}

func (s *PostgresStore) getBySource(ctx context.Context, tenantID, aiID, humanID string) (Disagreement, error) {
	item, err := scanDisagreement(s.db.QueryRowContext(ctx, `SELECT `+disagreementColumns+` FROM ai_human_disagreement WHERE tenant_id=$1::uuid AND ai_candidate_id=$2::uuid AND human_grade_id=$3::uuid`, tenantID, aiID, humanID))
	return item, mapStoreError(err)
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, filter Filter) ([]Disagreement, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	query := `SELECT ` + disagreementColumns + ` FROM ai_human_disagreement WHERE tenant_id=$1::uuid`
	args := []any{tenantID}
	appendFilter := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND %s=$%d", column, len(args))
	}
	appendFilter("exam_id", filter.ExamID)
	appendFilter("question_id", filter.QuestionID)
	appendFilter("status", string(filter.Status))
	appendFilter("severity", string(filter.Severity))
	args = append(args, filter.Limit)
	query += fmt.Sprintf(" ORDER BY CASE severity WHEN 'severe' THEN 0 ELSE 1 END,created_at ASC,id ASC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	items := []Disagreement{}
	for rows.Next() {
		item, scanErr := scanDisagreement(rows)
		if scanErr != nil {
			return nil, mapStoreError(scanErr)
		}
		items = append(items, item)
	}
	return items, mapStoreError(rows.Err())
}

func (s *PostgresStore) Classify(ctx context.Context, tenantID, id, reviewerID string, input ClassifyInput, at time.Time) (Disagreement, error) {
	if s.db == nil {
		return Disagreement{}, ErrInvalidInput
	}
	item, err := scanDisagreement(s.db.QueryRowContext(ctx, `
UPDATE ai_human_disagreement
SET status='classified',taxonomy=$4,notes=$5,reviewer_id=$3::uuid,reviewed_at=$6,revision=revision+1,updated_at=$6
WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$7 AND status IN ('needs_review','classified')
RETURNING `+disagreementColumns, tenantID, id, reviewerID, input.Taxonomy, strings.TrimSpace(input.Notes), at.UTC(), input.ExpectedRevision))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.Get(ctx, tenantID, id); getErr == nil {
			return Disagreement{}, ErrConflict
		}
	}
	return item, mapStoreError(err)
}

func (s *PostgresStore) Route(ctx context.Context, tenantID, id, actorID string, input RouteInput, at time.Time) (Disagreement, error) {
	if s.db == nil {
		return Disagreement{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Disagreement{}, err
	}
	defer tx.Rollback()
	item, err := scanDisagreement(tx.QueryRowContext(ctx, `SELECT `+disagreementColumns+` FROM ai_human_disagreement WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, id))
	if err != nil {
		return Disagreement{}, mapStoreError(err)
	}
	if item.Revision != input.ExpectedRevision {
		return Disagreement{}, ErrConflict
	}
	if item.Status != StatusNeedsReview && item.Status != StatusClassified {
		return Disagreement{}, ErrInvalidState
	}
	// 目标任务必须是同一考试、题目、提交和答题片段的纯人工分歧复核，防止路由到其他答案。
	var target string
	err = tx.QueryRowContext(ctx, `
SELECT id::text FROM review_task
WHERE tenant_id=$1::uuid AND id=$2::uuid AND exam_id=$3::uuid AND question_id=$4::uuid
  AND submission_id=$5::uuid AND answer_segment_id=$6::uuid AND source='ai_human_disagreement'
  AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL`,
		tenantID, input.ReviewTaskID, item.ExamID, item.QuestionID, item.SubmissionID, item.AnswerSegmentID).Scan(&target)
	if err != nil {
		return Disagreement{}, mapStoreError(err)
	}
	updated, err := scanDisagreement(tx.QueryRowContext(ctx, `
UPDATE ai_human_disagreement
SET status='routed',routed_review_task_id=$3::uuid,routed_by=$4::uuid,revision=revision+1,updated_at=$5
WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$6 AND status IN ('needs_review','classified')
RETURNING `+disagreementColumns, tenantID, id, target, actorID, at.UTC(), input.ExpectedRevision))
	if err != nil {
		return Disagreement{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return Disagreement{}, mapStoreError(err)
	}
	return updated, nil
}

const disagreementColumns = `id::text,tenant_id::text,exam_id::text,question_id::text,submission_id::text,answer_segment_id::text,
ai_candidate_id::text,human_grade_id::text,ai_candidate_score::float8,human_score::float8,max_score::float8,delta::float8,
absolute_delta::float8,difference_type,severity,risk_tier,trigger_rules_json,evidence_summary_json,status,COALESCE(taxonomy,''),
COALESCE(reviewer_id::text,''),reviewed_at,notes,COALESCE(routed_review_task_id::text,''),COALESCE(routed_by::text,''),revision,created_at,updated_at`

type disagreementScanner interface{ Scan(...any) error }

func scanDisagreement(row disagreementScanner) (Disagreement, error) {
	var item Disagreement
	var triggers, summary []byte
	var reviewedAt sql.NullTime
	err := row.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.QuestionID, &item.SubmissionID, &item.AnswerSegmentID,
		&item.AICandidateID, &item.HumanGradeID, &item.AICandidateScore, &item.HumanScore, &item.MaxScore, &item.Delta,
		&item.AbsoluteDelta, &item.DifferenceType, &item.Severity, &item.RiskTier, &triggers, &summary, &item.Status,
		&item.Taxonomy, &item.ReviewerID, &reviewedAt, &item.Notes, &item.RoutedReviewTaskID, &item.RoutedBy, &item.Revision,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Disagreement{}, err
	}
	if err = json.Unmarshal(triggers, &item.TriggerRules); err != nil {
		return Disagreement{}, err
	}
	if err = json.Unmarshal(summary, &item.EvidenceSummary); err != nil {
		return Disagreement{}, err
	}
	if reviewedAt.Valid {
		value := reviewedAt.Time.UTC()
		item.ReviewedAt = &value
	}
	return item, nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505":
		return ErrConflict
	case "23514", "23502", "23503", "22P02":
		return ErrInvalidInput
	default:
		return err
	}
}
