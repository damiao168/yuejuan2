package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const panelColumns = `id::text, tenant_id::text, answer_segment_id::text, answer_version, question_id::text,
  rubric_version, policy_version, COALESCE(primary_a_run_id::text,''), COALESCE(primary_b_run_id::text,''),
  COALESCE(arbiter_run_id::text,''), score_a::float8, score_b::float8, score_c::float8, max_score::float8,
  score_gap::float8, criterion_gap::float8, required_point_conflict, evidence_conflict, confidence_conflict,
  trigger_codes, decision_config, status, resolved_score::float8, COALESCE(resolution_source,''),
  COALESCE(review_task_id::text,''), created_by::text, created_at, updated_at, completed_at`

func (s *PostgresStore) GetOrCreatePanel(ctx context.Context, tenantID, actorID string, input CreatePanelInput) (GradingPanel, error) {
	input.DecisionConfig = NormalizePanelDecisionConfig(input.DecisionConfig)
	if tenantID == "" || actorID == "" || validateCreatePanel(input) != nil {
		return GradingPanel{}, ErrInvalidInput
	}
	config, err := json.Marshal(input.DecisionConfig)
	if err != nil {
		return GradingPanel{}, err
	}
	panel, err := scanPanel(s.db.QueryRowContext(ctx, `
INSERT INTO subjective_grading_panel(
  tenant_id,answer_segment_id,answer_version,question_id,rubric_version,policy_version,max_score,decision_config,created_by
) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7,$8::jsonb,$9::uuid)
ON CONFLICT (tenant_id,answer_segment_id,answer_version,rubric_version)
  WHERE deleted_at IS NULL
DO UPDATE SET updated_at=subjective_grading_panel.updated_at
RETURNING `+panelColumns, tenantID, input.AnswerSegmentID, input.AnswerVersion, input.QuestionID,
		input.RubricVersion, input.DecisionConfig.PolicyVersion, input.MaxScore, config, actorID))
	if err != nil {
		return GradingPanel{}, err
	}
	if panel.QuestionID != input.QuestionID || panel.MaxScore != input.MaxScore {
		return GradingPanel{}, ErrIdempotencyConflict
	}
	return panel, nil
}

func (s *PostgresStore) GetPanel(ctx context.Context, tenantID, panelID string) (GradingPanel, error) {
	return scanPanel(s.db.QueryRowContext(ctx, `SELECT `+panelColumns+` FROM subjective_grading_panel
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, panelID))
}

func (s *PostgresStore) UpdatePanel(ctx context.Context, tenantID, panelID string, input UpdatePanelInput) (GradingPanel, error) {
	if !validPanelStatus(input.Status) {
		return GradingPanel{}, ErrInvalidInput
	}
	triggers, err := json.Marshal(nonNilStrings(input.TriggerCodes))
	if err != nil {
		return GradingPanel{}, err
	}
	panel, err := scanPanel(s.db.QueryRowContext(ctx, `
UPDATE subjective_grading_panel SET
  primary_a_run_id=COALESCE(NULLIF($4,'')::uuid,primary_a_run_id),
  primary_b_run_id=COALESCE(NULLIF($5,'')::uuid,primary_b_run_id),
  arbiter_run_id=COALESCE(NULLIF($6,'')::uuid,arbiter_run_id),
  score_a=COALESCE($7,score_a), score_b=COALESCE($8,score_b), score_c=COALESCE($9,score_c),
  score_gap=COALESCE($10,score_gap), criterion_gap=COALESCE($11,criterion_gap),
  required_point_conflict=$12, evidence_conflict=$13, confidence_conflict=$14,
  trigger_codes=$15::jsonb, status=$3,
  resolved_score=COALESCE($16,resolved_score),
  resolution_source=COALESCE(NULLIF($17,''),resolution_source),
  review_task_id=COALESCE(NULLIF($18,'')::uuid,review_task_id),
  completed_at=CASE WHEN $3='resolved' THEN COALESCE(completed_at,now()) ELSE completed_at END,
  updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
RETURNING `+panelColumns, tenantID, panelID, input.Status, input.PrimaryARunID, input.PrimaryBRunID,
		input.ArbiterRunID, input.ScoreA, input.ScoreB, input.ScoreC, input.ScoreGap, input.CriterionGap,
		input.RequiredPointConflict, input.EvidenceConflict, input.ConfidenceConflict, triggers,
		input.ResolvedScore, input.ResolutionSource, input.ReviewTaskID))
	if panelPersistenceConflict(err) {
		return GradingPanel{}, ErrIdempotencyConflict
	}
	return panel, err
}

func panelPersistenceConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code == "23514" && (pgErr.Message == "subjective panel run snapshot mismatch" ||
		pgErr.Message == "subjective panel run binding is immutable" ||
		pgErr.Message == "invalid subjective panel status transition" ||
		pgErr.Message == "subjective panel primary roles are incomplete" ||
		pgErr.Message == "subjective panel arbiter role is incomplete" ||
		pgErr.Message == "invalid primary_a panel run" || pgErr.Message == "invalid primary_b panel run" ||
		pgErr.Message == "invalid arbiter panel run") {
		return true
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "uq_subjective_grading_run_panel_role"
}

func (s *PostgresStore) RoutePanelHumanReview(ctx context.Context, tenantID, actorID string, panel GradingPanel) (string, error) {
	if panel.TenantID != tenantID || actorID == "" || panel.AnswerSegmentID == "" {
		return "", ErrInvalidInput
	}
	for range 2 {
		var id string
		err := s.db.QueryRowContext(ctx, `
INSERT INTO review_task(
  tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,
  source,status,priority,grade_round,reason_code,created_by,subjective_panel_id
)
SELECT p.tenant_id,q.exam_id,p.question_id,q.question_no,p.answer_segment_id,seg.submission_id,
  COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),'ai_panel_disagreement','pending',200,'single',
  'panel_unresolved',$3::uuid,p.id
FROM subjective_grading_panel p
JOIN answer_segment seg ON seg.tenant_id=p.tenant_id AND seg.id=p.answer_segment_id
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
JOIN question q ON q.tenant_id=p.tenant_id AND q.id=p.question_id
WHERE p.tenant_id=$1::uuid AND p.id=$2::uuid AND p.deleted_at IS NULL
ON CONFLICT (tenant_id,subjective_panel_id)
  WHERE subjective_panel_id IS NOT NULL
    AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
DO NOTHING
RETURNING id::text`, tenantID, panel.ID, actorID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		err = s.db.QueryRowContext(ctx, `
SELECT id::text FROM review_task
WHERE tenant_id=$1::uuid AND subjective_panel_id=$2::uuid AND source='ai_panel_disagreement'
  AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1`, tenantID, panel.ID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", ErrIdempotencyConflict
}

type panelScanner interface{ Scan(...any) error }

func scanPanel(row panelScanner) (GradingPanel, error) {
	var out GradingPanel
	var scoreA, scoreB, scoreC, scoreGap, criterionGap, resolved sql.NullFloat64
	var completed sql.NullTime
	var triggers, config []byte
	if err := row.Scan(
		&out.ID, &out.TenantID, &out.AnswerSegmentID, &out.AnswerVersion, &out.QuestionID, &out.RubricVersion, &out.PolicyVersion,
		&out.PrimaryARunID, &out.PrimaryBRunID, &out.ArbiterRunID, &scoreA, &scoreB, &scoreC, &out.MaxScore,
		&scoreGap, &criterionGap, &out.RequiredPointConflict, &out.EvidenceConflict, &out.ConfidenceConflict,
		&triggers, &config, &out.Status, &resolved, &out.ResolutionSource, &out.ReviewTaskID,
		&out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &completed,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GradingPanel{}, ErrNotFound
		}
		return GradingPanel{}, err
	}
	assignNullableFloat := func(source sql.NullFloat64, target **float64) {
		if source.Valid {
			value := source.Float64
			*target = &value
		}
	}
	assignNullableFloat(scoreA, &out.ScoreA)
	assignNullableFloat(scoreB, &out.ScoreB)
	assignNullableFloat(scoreC, &out.ScoreC)
	assignNullableFloat(scoreGap, &out.ScoreGap)
	assignNullableFloat(criterionGap, &out.CriterionGap)
	assignNullableFloat(resolved, &out.ResolvedScore)
	if err := json.Unmarshal(triggers, &out.TriggerCodes); err != nil {
		return GradingPanel{}, err
	}
	if err := json.Unmarshal(config, &out.DecisionConfig); err != nil {
		return GradingPanel{}, err
	}
	if completed.Valid {
		value := completed.Time.UTC()
		out.CompletedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
