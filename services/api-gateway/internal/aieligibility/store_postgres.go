package aieligibility

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) PutPolicy(ctx context.Context, tenantID string, input PutPolicyInput) (Policy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Policy{}, err
	}
	defer tx.Rollback()
	var latest int
	err = tx.QueryRowContext(ctx, `SELECT version FROM ai_eligibility_policy
WHERE tenant_id=$1 AND subject_code=$2 AND education_stage=$3 AND archetype_code=$4 AND risk_tier=$5
ORDER BY version DESC LIMIT 1 FOR UPDATE`, tenantID, input.SubjectCode, input.EducationStage, input.ArchetypeCode, input.RiskTier).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return Policy{}, mapStoreError(err)
	}
	current := latest
	if input.ExpectedVersion != current {
		return Policy{}, ErrPolicyConflict
	}
	modes, _ := json.Marshal(input.AllowedModes)
	// The version append-only model makes a decision's policy_version stable.
	// A disabled version intentionally removes its matching active policy.
	row := tx.QueryRowContext(ctx, `INSERT INTO ai_eligibility_policy (
 id,tenant_id,subject_code,education_stage,archetype_code,risk_tier,min_ocr_quality,min_parser_quality,min_eval_n,max_severe_error_rate,allowed_modes_json,version,status
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
RETURNING id::text,tenant_id::text,subject_code,education_stage,archetype_code,risk_tier,min_ocr_quality::float8,min_parser_quality::float8,min_eval_n,max_severe_error_rate::float8,allowed_modes_json,version,status,created_at`, uuid.NewString(), tenantID, input.SubjectCode, input.EducationStage, input.ArchetypeCode, input.RiskTier, input.MinOCRQuality, input.MinParserQuality, input.MinEvalN, input.MaxSevereErrorRate, modes, current+1, input.Status)
	item, err := scanPolicy(row)
	if err != nil {
		return Policy{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return Policy{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) GetActivePolicy(ctx context.Context, tenantID string, subject assessment.SubjectCode, stage assessment.EducationStage, archetype string, risk assessment.RiskTier) (Policy, error) {
	item, err := scanPolicy(s.db.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,subject_code,education_stage,archetype_code,risk_tier,min_ocr_quality::float8,min_parser_quality::float8,min_eval_n,max_severe_error_rate::float8,allowed_modes_json,version,status,created_at
FROM ai_eligibility_policy WHERE tenant_id=$1 AND subject_code=$2 AND education_stage=$3 AND archetype_code=$4 AND risk_tier=$5
ORDER BY version DESC LIMIT 1`, tenantID, subject, stage, archetype, risk))
	if err != nil {
		return item, mapStoreError(err)
	}
	if item.Status != PolicyActive {
		return Policy{}, ErrNotFound
	}
	return item, nil
}

func (s *PostgresStore) CreateOrGetDecision(ctx context.Context, tenantID string, input Decision) (Decision, error) {
	// 决定以租户和运行项唯一；并发写入的后来者读取已接受的结果，不能覆写历史准入依据。
	inputJSON, _ := json.Marshal(input.InputSnapshot)
	reasonsJSON, _ := json.Marshal(input.Reasons)
	constraintJSON, _ := json.Marshal(input.OutputConstraint)
	row := s.db.QueryRowContext(ctx, `INSERT INTO ai_eligibility_decision (
id,tenant_id,run_item_id,policy_id,policy_version,input_snapshot_json,decision,reasons_json,external_ai_allowed,output_constraint_json
) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10)
ON CONFLICT (tenant_id,run_item_id) DO NOTHING
RETURNING id::text,tenant_id::text,run_item_id,COALESCE(policy_id::text,''),policy_version,input_snapshot_json,decision,reasons_json,external_ai_allowed,output_constraint_json,created_at`, input.ID, tenantID, input.RunItemID, input.PolicyID, input.PolicyVersion, inputJSON, input.Decision, reasonsJSON, input.ExternalAIAllowed, constraintJSON)
	item, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s.GetDecision(ctx, tenantID, input.RunItemID)
	}
	return item, mapStoreError(err)
}
func (s *PostgresStore) GetDecision(ctx context.Context, tenantID, runItemID string) (Decision, error) {
	item, err := scanDecision(s.db.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,run_item_id,COALESCE(policy_id::text,''),policy_version,input_snapshot_json,decision,reasons_json,external_ai_allowed,output_constraint_json,created_at
FROM ai_eligibility_decision WHERE tenant_id=$1 AND run_item_id=$2`, tenantID, runItemID))
	return item, mapStoreError(err)
}

type scanner interface{ Scan(...any) error }

func scanPolicy(row scanner) (Policy, error) {
	var item Policy
	var modes []byte
	err := row.Scan(&item.ID, &item.TenantID, &item.SubjectCode, &item.EducationStage, &item.ArchetypeCode, &item.RiskTier, &item.MinOCRQuality, &item.MinParserQuality, &item.MinEvalN, &item.MaxSevereErrorRate, &modes, &item.Version, &item.Status, &item.CreatedAt)
	if err != nil {
		return Policy{}, err
	}
	if err = json.Unmarshal(modes, &item.AllowedModes); err != nil {
		return Policy{}, err
	}
	return item, nil
}
func scanDecision(row scanner) (Decision, error) {
	var item Decision
	var input, reasons, constraint []byte
	err := row.Scan(&item.ID, &item.TenantID, &item.RunItemID, &item.PolicyID, &item.PolicyVersion, &input, &item.Decision, &reasons, &item.ExternalAIAllowed, &constraint, &item.CreatedAt)
	if err != nil {
		return Decision{}, err
	}
	if err = json.Unmarshal(input, &item.InputSnapshot); err != nil {
		return Decision{}, err
	}
	if err = json.Unmarshal(reasons, &item.Reasons); err != nil {
		return Decision{}, err
	}
	if err = json.Unmarshal(constraint, &item.OutputConstraint); err != nil {
		return Decision{}, err
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
	switch {
	case pgErr.Code == "23505":
		return ErrPolicyConflict
	case pgErr.Code == "23514" || pgErr.Code == "23502" || pgErr.Code == "23503":
		return ErrInvalidInput
	case strings.Contains(strings.ToLower(pgErr.Message), "not found"):
		return ErrNotFound
	default:
		return err
	}
}
