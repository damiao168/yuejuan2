package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"github.com/jackc/pgx/v5/pgconn"
)

const panelPolicyColumns = `id::text,tenant_id::text,policy_version,COALESCE(model_set_reference,''),education_stage,subject_code,archetype_code,
 decision_config,readiness_policy,COALESCE(evaluation_run_id::text,''),readiness_report,status,created_by::text,created_at,
 COALESCE(approved_by::text,''),approved_at,COALESCE(invalidated_by::text,''),invalidated_at,invalidation_reason`

func (s *PostgresStore) CreatePanelPolicy(ctx context.Context, tenantID, actorID string, input CreatePanelPolicyInput) (PanelPolicy, error) {
	if s == nil || s.db == nil || tenantID == "" || actorID == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	input, err := normalizeCreatePanelPolicy(input)
	if err != nil {
		return PanelPolicy{}, err
	}
	decisionConfig, err := json.Marshal(input.DecisionConfig)
	if err != nil {
		return PanelPolicy{}, err
	}
	readinessPolicy, err := json.Marshal(input.ReadinessPolicy)
	if err != nil {
		return PanelPolicy{}, err
	}
	item, err := scanPanelPolicy(s.db.QueryRowContext(ctx, `
INSERT INTO subjective_panel_policy(
 tenant_id,policy_version,model_set_reference,education_stage,subject_code,archetype_code,decision_config,readiness_policy,created_by
) VALUES($1::uuid,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9::uuid)
RETURNING `+panelPolicyColumns,
		tenantID, input.PolicyVersion, input.ModelSetReference, input.EducationStage, input.SubjectCode, input.ArchetypeCode, decisionConfig, readinessPolicy, actorID))
	return item, mapPanelPolicyStoreError(err)
}

func (s *PostgresStore) GetPanelPolicy(ctx context.Context, tenantID, policyID string) (PanelPolicy, error) {
	if s == nil || s.db == nil || tenantID == "" || policyID == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	item, err := scanPanelPolicy(s.db.QueryRowContext(ctx, `SELECT `+panelPolicyColumns+`
FROM subjective_panel_policy WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, policyID))
	return item, mapPanelPolicyStoreError(err)
}

func (s *PostgresStore) FindApprovedPanelPolicy(ctx context.Context, tenantID string, stage assessment.EducationStage, subject assessment.SubjectCode, archetype string) (PanelPolicy, error) {
	if s == nil || s.db == nil || tenantID == "" || !stage.Valid() || !subject.Valid() || archetype == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	item, err := scanPanelPolicy(s.db.QueryRowContext(ctx, `SELECT `+panelPolicyColumns+`
FROM subjective_panel_policy
WHERE tenant_id=$1::uuid AND education_stage=$2 AND subject_code=$3 AND archetype_code=$4 AND status='approved'
  AND EXISTS (
    SELECT 1 FROM grading_evaluation_run evaluation
    WHERE evaluation.tenant_id=subjective_panel_policy.tenant_id
      AND evaluation.id=subjective_panel_policy.evaluation_run_id AND evaluation.status='completed'
  )`,
		tenantID, stage, subject, archetype))
	return item, mapPanelPolicyStoreError(err)
}

// 数据库更新只允许 shadow 到 approved；实际准入还会在查询时确认评估运行已完成。
func (s *PostgresStore) ApprovePanelPolicy(ctx context.Context, tenantID, policyID, actorID, evaluationRunID string, report gradingevaluation.PanelSliceReadiness, at time.Time) (PanelPolicy, error) {
	if s == nil || s.db == nil || tenantID == "" || policyID == "" || actorID == "" || evaluationRunID == "" || !report.Ready {
		return PanelPolicy{}, ErrInvalidInput
	}
	readinessReport, err := json.Marshal(report)
	if err != nil {
		return PanelPolicy{}, err
	}
	item, err := scanPanelPolicy(s.db.QueryRowContext(ctx, `
UPDATE subjective_panel_policy SET
 status='approved',evaluation_run_id=$4::uuid,readiness_report=$5::jsonb,approved_by=$3::uuid,approved_at=$6
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='shadow'
RETURNING `+panelPolicyColumns, tenantID, policyID, actorID, evaluationRunID, readinessReport, at.UTC()))
	return item, mapPanelPolicyStoreError(err)
}

func (s *PostgresStore) InvalidatePanelPolicy(ctx context.Context, tenantID, policyID, actorID, reason string, at time.Time) (PanelPolicy, error) {
	if s == nil || s.db == nil || tenantID == "" || policyID == "" || actorID == "" || reason == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	item, err := scanPanelPolicy(s.db.QueryRowContext(ctx, `
UPDATE subjective_panel_policy SET
 status='invalidated',invalidated_by=$3::uuid,invalidated_at=$5,invalidation_reason=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('shadow','approved')
RETURNING `+panelPolicyColumns, tenantID, policyID, actorID, reason, at.UTC()))
	return item, mapPanelPolicyStoreError(err)
}

func scanPanelPolicy(row panelScanner) (PanelPolicy, error) {
	var item PanelPolicy
	var decisionConfig, readinessPolicy, readinessReport []byte
	var approvedAt, invalidatedAt sql.NullTime
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.PolicyVersion, &item.ModelSetReference, &item.EducationStage, &item.SubjectCode, &item.ArchetypeCode,
		&decisionConfig, &readinessPolicy, &item.EvaluationRunID, &readinessReport, &item.Status, &item.CreatedBy, &item.CreatedAt,
		&item.ApprovedBy, &approvedAt, &item.InvalidatedBy, &invalidatedAt, &item.InvalidationReason,
	); err != nil {
		return PanelPolicy{}, err
	}
	if err := json.Unmarshal(decisionConfig, &item.DecisionConfig); err != nil {
		return PanelPolicy{}, err
	}
	if err := json.Unmarshal(readinessPolicy, &item.ReadinessPolicy); err != nil {
		return PanelPolicy{}, err
	}
	if string(readinessReport) != "{}" && string(readinessReport) != "null" {
		var report gradingevaluation.PanelSliceReadiness
		if err := json.Unmarshal(readinessReport, &report); err != nil {
			return PanelPolicy{}, err
		}
		item.ReadinessReport = &report
	}
	item.CreatedAt = item.CreatedAt.UTC()
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		item.ApprovedAt = &value
	}
	if invalidatedAt.Valid {
		value := invalidatedAt.Time.UTC()
		item.InvalidatedAt = &value
	}
	return item, nil
}

func mapPanelPolicyStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23514":
			return ErrIdempotencyConflict
		case "23503", "22P02":
			return ErrInvalidInput
		}
	}
	return err
}
