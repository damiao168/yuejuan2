package modelgovernance

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

const roleBindingColumns = `binding.id::text,binding.tenant_id::text,binding.education_stage,binding.subject_code,
  binding.archetype_code,binding.agent_role,binding.managed_model_api_config_id::text,binding.prompt_version,
  binding.strength_rank,binding.status,binding.created_by::text,binding.created_at,binding.updated_at`

func (s *PostgresStore) SaveModelRoleBinding(ctx context.Context, tenantID, actorID string, input SaveModelRoleBindingInput) (ModelRoleBinding, error) {
	input, err := normalizeRoleBinding(input)
	if err != nil || tenantID == "" || actorID == "" {
		return ModelRoleBinding{}, ErrInvalidManagedConfig
	}
	id := uuid.NewString()
	item, err := scanRoleBinding(s.db.QueryRowContext(ctx, `
INSERT INTO model_role_binding AS binding(
  id,tenant_id,education_stage,subject_code,archetype_code,agent_role,managed_model_api_config_id,
  prompt_version,strength_rank,status,created_by
)
SELECT $1::uuid,$2::uuid,$3,$4,$5,$6,config.id,$8,$9,$10,$11::uuid
FROM managed_model_api_config config
WHERE config.tenant_id=$2::uuid AND config.id=$7::uuid
  AND ($10='disabled' OR (config.deleted_at IS NULL AND config.status='active' AND config.last_capability_status='success' AND config.last_capability_probe_version=$12))
FOR SHARE OF config
ON CONFLICT(tenant_id,education_stage,subject_code,archetype_code,agent_role)
DO UPDATE SET managed_model_api_config_id=EXCLUDED.managed_model_api_config_id,prompt_version=EXCLUDED.prompt_version,
  strength_rank=EXCLUDED.strength_rank,status=EXCLUDED.status,updated_at=now()
RETURNING `+roleBindingColumns, id, tenantID, input.EducationStage, input.SubjectCode, input.ArchetypeCode,
		input.AgentRole, input.ManagedModelAPIConfigID, input.PromptVersion, input.StrengthRank, input.Status, actorID, ManagedCapabilityProbeVersion))
	if errors.Is(err, ErrNotFound) {
		return ModelRoleBinding{}, ErrManagedCapabilityRequired
	}
	return item, err
}

func (s *PostgresStore) ListConfiguredModelRoleBindings(ctx context.Context, tenantID, stage, subject, archetype string) ([]ModelRoleBinding, error) {
	stage, subject, archetype, err := normalizeRoleScope(stage, subject, archetype)
	if err != nil || tenantID == "" {
		return nil, ErrInvalidManagedConfig
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+roleBindingColumns+`
FROM model_role_binding binding
WHERE binding.tenant_id=$1::uuid AND binding.education_stage=$2 AND binding.subject_code=$3 AND binding.archetype_code=$4
ORDER BY binding.agent_role`, tenantID, stage, subject, archetype)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelRoleBinding{}
	for rows.Next() {
		item, scanErr := scanRoleBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListModelRoleBindings(ctx context.Context, tenantID, stage, subject, archetype string) ([]ModelRoleBinding, error) {
	stage, subject, archetype, err := normalizeRoleScope(stage, subject, archetype)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+roleBindingColumns+`
FROM model_role_binding binding
JOIN managed_model_api_config config ON config.tenant_id=binding.tenant_id AND config.id=binding.managed_model_api_config_id
WHERE binding.tenant_id=$1::uuid AND binding.education_stage=$2 AND binding.subject_code=$3
  AND binding.archetype_code IN ($4,'*') AND binding.status='active'
  AND config.deleted_at IS NULL AND config.status='active' AND config.last_capability_status='success'
  AND config.last_capability_probe_version=$5
ORDER BY (binding.archetype_code=$4) DESC,binding.agent_role`, tenantID, stage, subject, archetype, ManagedCapabilityProbeVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelRoleBinding{}
	for rows.Next() {
		item, scanErr := scanRoleBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type roleBindingScanner interface{ Scan(...any) error }

func scanRoleBinding(row roleBindingScanner) (ModelRoleBinding, error) {
	var out ModelRoleBinding
	if err := row.Scan(&out.ID, &out.TenantID, &out.EducationStage, &out.SubjectCode, &out.ArchetypeCode,
		&out.AgentRole, &out.ManagedModelAPIConfigID, &out.PromptVersion, &out.StrengthRank, &out.Status,
		&out.CreatedBy, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModelRoleBinding{}, ErrNotFound
		}
		return ModelRoleBinding{}, err
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}
