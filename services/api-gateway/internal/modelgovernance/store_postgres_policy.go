package modelgovernance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *PostgresStore) GetPolicy(ctx context.Context, tenantID string) (TenantPolicy, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, policy_key, display_name, mode,
       external_enabled, text_export_enabled, image_export_enabled,
       allowed_deployments, max_cost_micros_per_question, max_cost_micros_per_exam,
       fallback_mode, status, version, updated_at
FROM tenant_model_policy
WHERE tenant_id = $1 AND policy_key = 'default' AND deleted_at IS NULL
`, tenantID)
	item, err := scanPolicy(row)
	if err != nil {
		return TenantPolicy{}, mapStoreError(err)
	}
	item.AllowedModelConfigIDs, err = s.policyModelConfigIDs(ctx, tenantID, item.ID)
	if err != nil {
		return TenantPolicy{}, err
	}
	return item, nil
}

func (s *PostgresStore) policyModelConfigIDs(ctx context.Context, tenantID, policyID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT managed_model_api_config_id::text
FROM tenant_model_policy_model
WHERE tenant_id = $1 AND policy_id::text = $2 AND enabled
ORDER BY managed_model_api_config_id
`, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SQL 更新同时校验租户、期望版本和托管配置能力状态；并发修改会返回版本冲突而不是覆盖。
func (s *PostgresStore) UpdatePolicy(ctx context.Context, tenantID string, _ string, input PolicyUpdateInput) (TenantPolicy, error) {
	if input.AllowedDeployments == nil {
		input.AllowedDeployments = []string{}
	}
	if input.ExpectedVersion < 1 || strings.TrimSpace(input.Reason) == "" || ValidateTenantPolicy(PolicyFromUpdate(input)) != nil {
		return TenantPolicy{}, ErrInvalidPolicy
	}
	allowed, err := json.Marshal(input.AllowedDeployments)
	if err != nil {
		return TenantPolicy{}, ErrInvalidPolicy
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TenantPolicy{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE tenant_model_policy
SET display_name = COALESCE(NULLIF($2, ''), display_name),
    mode = $3,
    external_enabled = $4,
    text_export_enabled = $5,
    image_export_enabled = $6,
    allowed_deployments = $7,
    max_cost_micros_per_question = $8,
    max_cost_micros_per_exam = $9,
    fallback_mode = $10,
    version = version + 1,
    updated_at = now()
WHERE tenant_id = $1
  AND policy_key = 'default'
  AND version = $11
  AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, policy_key, display_name, mode,
          external_enabled, text_export_enabled, image_export_enabled,
          allowed_deployments, max_cost_micros_per_question, max_cost_micros_per_exam,
          fallback_mode, status, version, updated_at
`, tenantID, input.DisplayName, input.Mode, input.ExternalEnabled,
		input.TextExportEnabled, input.ImageExportEnabled, allowed,
		input.MaxCostMicrosPerQuestion, input.MaxCostMicrosPerExam,
		input.FallbackMode, input.ExpectedVersion)
	item, err := scanPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TenantPolicy{}, ErrConflict
	}
	if err != nil {
		return TenantPolicy{}, mapStoreError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tenant_model_policy_model WHERE tenant_id = $1 AND policy_id::text = $2`, tenantID, item.ID); err != nil {
		return TenantPolicy{}, mapStoreError(err)
	}
	for _, id := range input.AllowedModelConfigIDs {
		result, err := tx.ExecContext(ctx, `
INSERT INTO tenant_model_policy_model (tenant_id, policy_id, managed_model_api_config_id)
SELECT $1::uuid, $2::uuid, config.id
FROM managed_model_api_config config
WHERE config.tenant_id = $1::uuid AND config.id::text = $3
  AND config.deleted_at IS NULL AND config.status = 'active'
  AND config.last_capability_status = 'success'
  AND config.last_capability_probe_version = 'structured-json-v4'
`, tenantID, item.ID, id)
		if err != nil {
			return TenantPolicy{}, mapStoreError(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return TenantPolicy{}, ErrInvalidPolicy
		}
	}
	if err := tx.Commit(); err != nil {
		return TenantPolicy{}, mapStoreError(err)
	}
	item.AllowedModelConfigIDs = append([]string{}, input.AllowedModelConfigIDs...)
	return item, nil
}

func scanPolicy(row rowScanner) (TenantPolicy, error) {
	var item TenantPolicy
	var allowed []byte
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.PolicyKey, &item.DisplayName, &item.Mode,
		&item.ExternalEnabled, &item.TextExportEnabled, &item.ImageExportEnabled,
		&allowed, &item.MaxCostMicrosPerQuestion, &item.MaxCostMicrosPerExam,
		&item.FallbackMode, &item.Status, &item.Version, &item.UpdatedAt,
	); err != nil {
		return TenantPolicy{}, err
	}
	if err := json.Unmarshal(allowed, &item.AllowedDeployments); err != nil {
		return TenantPolicy{}, err
	}
	return item, nil
}
