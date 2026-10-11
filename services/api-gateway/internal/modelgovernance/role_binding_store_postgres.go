package modelgovernance

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

const roleBindingColumns = `binding.id::text,binding.tenant_id::text,binding.education_stage,binding.subject_code,
  binding.archetype_code,binding.agent_role,binding.managed_model_api_config_id::text,binding.prompt_version,
  binding.strength_rank,binding.status,binding.created_by::text,binding.created_at,binding.updated_at,binding.revision::text`

func (s *PostgresStore) SaveModelRoleBinding(ctx context.Context, tenantID, actorID string, input SaveModelRoleBindingInput) (ModelRoleBinding, error) {
	input, err := normalizeRoleBinding(input)
	if err != nil || tenantID == "" || actorID == "" {
		return ModelRoleBinding{}, ErrInvalidManagedConfig
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelRoleBinding{}, err
	}
	defer tx.Rollback()
	// 与模型删除共用先锁模型的顺序，能力核实后到绑定提交前不能撤销该模型。
	var configID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM managed_model_api_config
WHERE tenant_id=$1::uuid AND id=$2::uuid
  AND ($3='disabled' OR (deleted_at IS NULL AND status='active' AND last_capability_status='success' AND last_capability_probe_version=$4))
FOR SHARE`, tenantID, input.ManagedModelAPIConfigID, input.Status, ManagedCapabilityProbeVersion).Scan(&configID)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelRoleBinding{}, ErrManagedCapabilityRequired
	}
	if err != nil {
		return ModelRoleBinding{}, err
	}
	var row *sql.Row
	// 新建不覆盖竞争者；更新在同一条 SQL 中比较修订，不能拆成先读版本再无条件写入。
	if input.ExpectedRevision == ModelRoleBindingAbsent {
		row = tx.QueryRowContext(ctx, `INSERT INTO model_role_binding AS binding(
  id,tenant_id,education_stage,subject_code,archetype_code,agent_role,managed_model_api_config_id,
  prompt_version,strength_rank,status,created_by,revision
) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid,$8,$9,$10,$11::uuid,$12::uuid)
ON CONFLICT(tenant_id,education_stage,subject_code,archetype_code,agent_role) DO NOTHING
RETURNING `+roleBindingColumns, uuid.NewString(), tenantID, input.EducationStage, input.SubjectCode, input.ArchetypeCode,
			input.AgentRole, configID, input.PromptVersion, input.StrengthRank, input.Status, actorID, uuid.NewString())
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE model_role_binding AS binding
SET managed_model_api_config_id=$7::uuid,prompt_version=$8,strength_rank=$9,status=$10,revision=$11::uuid,updated_at=now()
WHERE tenant_id=$1::uuid AND education_stage=$2 AND subject_code=$3 AND archetype_code=$4 AND agent_role=$5
  AND revision=$6::uuid
RETURNING `+roleBindingColumns, tenantID, input.EducationStage, input.SubjectCode, input.ArchetypeCode,
			input.AgentRole, input.ExpectedRevision, configID, input.PromptVersion, input.StrengthRank, input.Status, uuid.NewString())
	}
	item, err := scanRoleBinding(row)
	if errors.Is(err, ErrNotFound) {
		return ModelRoleBinding{}, ErrModelRoleBindingChanged
	}
	if err != nil {
		return ModelRoleBinding{}, err
	}
	if err = tx.Commit(); err != nil {
		return ModelRoleBinding{}, err
	}
	return item, nil
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
		&out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &out.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModelRoleBinding{}, ErrNotFound
		}
		return ModelRoleBinding{}, err
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}
