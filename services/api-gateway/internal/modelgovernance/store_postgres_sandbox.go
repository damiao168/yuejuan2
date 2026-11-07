package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"
)

func (s *PostgresStore) ListSandboxApprovals(ctx context.Context, tenantID string) ([]SandboxApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT approval.id::text, approval.tenant_id::text,
       COALESCE(approval.provider_id::text, ''), COALESCE(approval.deployment_id::text, ''),
       COALESCE(NULLIF(approval.provider_key, ''), provider.provider_key, ''), COALESCE(deployment.deployment_key, ''),
       COALESCE(approval.managed_model_api_config_id::text, ''), approval.model_name, approval.model_version,
       approval.protocol, approval.approval_reference, approval.approved_region,
       approval.sandbox_account, approval.contract_reviewed,
       approval.retention_reviewed, approval.data_residency_reviewed,
       approval.pricing_reviewed, approval.synthetic_data_only,
       approval.image_export_reviewed, approval.expires_at,
       approval.revoked_at, approval.created_at
FROM model_sandbox_approval approval
LEFT JOIN model_provider provider
  ON provider.tenant_id = approval.tenant_id AND provider.id = approval.provider_id
LEFT JOIN model_deployment deployment
  ON deployment.tenant_id = approval.tenant_id AND deployment.id = approval.deployment_id
WHERE approval.tenant_id = $1
ORDER BY approval.created_at DESC, approval.id
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxApproval{}
	for rows.Next() {
		item, err := scanSandboxApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// 数据库审批写入同时绑定租户、供应商和部署；托管模型路径不会混用旧版 provider/deployment 外键。
func (s *PostgresStore) CreateSandboxApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	input SandboxApprovalInput,
) (SandboxApproval, error) {
	if ValidateSandboxApprovalInput(input, time.Now().UTC()) != nil {
		return SandboxApproval{}, ErrInvalidApproval
	}
	if input.ModelConfigID != "" {
		return s.createManagedSandboxApproval(ctx, tenantID, actorID, input)
	}
	row := s.db.QueryRowContext(ctx, `
WITH inventory AS (
  SELECT provider.id AS provider_id, provider.provider_key,
         deployment.id AS deployment_id, deployment.deployment_key
  FROM model_provider provider
  JOIN model_deployment deployment
    ON deployment.tenant_id = provider.tenant_id
   AND deployment.provider_id = provider.id
  WHERE provider.tenant_id = $1
    AND provider.id::text = $2
    AND deployment.id::text = $3
    AND provider.deleted_at IS NULL
    AND deployment.deleted_at IS NULL
    AND provider.provider_kind = 'external'
    AND provider.adapter_type = 'dashscope_native'
    AND provider.region = $6
    AND deployment.region = $6
),
inserted AS (
  INSERT INTO model_sandbox_approval (
    tenant_id, provider_id, deployment_id, protocol,
    approval_reference, approved_region, sandbox_account,
    contract_reviewed, retention_reviewed, data_residency_reviewed,
    pricing_reviewed, synthetic_data_only, image_export_reviewed,
    expires_at, created_by
  )
  SELECT
    $1, inventory.provider_id, inventory.deployment_id, $4,
    $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13,
    $14, NULLIF($15, '')::uuid
  FROM inventory
  RETURNING *
)
SELECT inserted.id::text, inserted.tenant_id::text,
       inserted.provider_id::text, inserted.deployment_id::text,
       inventory.provider_key, inventory.deployment_key,
       '', '', '',
       inserted.protocol, inserted.approval_reference, inserted.approved_region,
       inserted.sandbox_account, inserted.contract_reviewed,
       inserted.retention_reviewed, inserted.data_residency_reviewed,
       inserted.pricing_reviewed, inserted.synthetic_data_only,
       inserted.image_export_reviewed, inserted.expires_at,
       inserted.revoked_at, inserted.created_at
FROM inserted
JOIN inventory
  ON inventory.provider_id = inserted.provider_id
 AND inventory.deployment_id = inserted.deployment_id
`, tenantID, input.ProviderID, input.DeploymentID, input.Protocol,
		input.ApprovalReference, input.ApprovedRegion, input.SandboxAccount,
		input.ContractReviewed, input.RetentionReviewed, input.DataResidencyReviewed,
		input.PricingReviewed, input.SyntheticDataOnly, input.ImageExportReviewed,
		input.ExpiresAt.UTC(), actorID)
	item, err := scanSandboxApproval(row)
	if err != nil {
		return SandboxApproval{}, mapSandboxApprovalStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) createManagedSandboxApproval(
	ctx context.Context, tenantID, actorID string, input SandboxApprovalInput,
) (SandboxApproval, error) {
	row := s.db.QueryRowContext(ctx, `
WITH config AS (
  SELECT id, provider_key, model_name, model_version
  FROM managed_model_api_config
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL
    AND status = 'active' AND provider_key = 'aliyun' AND region = $5
), inserted AS (
  INSERT INTO model_sandbox_approval (
    tenant_id, managed_model_api_config_id, provider_key, model_name, model_version,
    protocol, approval_reference, approved_region, sandbox_account,
    contract_reviewed, retention_reviewed, data_residency_reviewed,
    pricing_reviewed, synthetic_data_only, image_export_reviewed,
    expires_at, created_by
  )
  SELECT $1::uuid, config.id, config.provider_key, config.model_name, config.model_version,
    $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, '')::uuid
  FROM config
  RETURNING *
)
SELECT inserted.id::text, inserted.tenant_id::text, '', '',
       inserted.provider_key, '', inserted.managed_model_api_config_id::text,
       inserted.model_name, inserted.model_version,
       inserted.protocol, inserted.approval_reference, inserted.approved_region,
       inserted.sandbox_account, inserted.contract_reviewed,
       inserted.retention_reviewed, inserted.data_residency_reviewed,
       inserted.pricing_reviewed, inserted.synthetic_data_only,
       inserted.image_export_reviewed, inserted.expires_at,
       inserted.revoked_at, inserted.created_at
FROM inserted
`, tenantID, input.ModelConfigID, input.Protocol, input.ApprovalReference,
		input.ApprovedRegion, input.SandboxAccount, input.ContractReviewed,
		input.RetentionReviewed, input.DataResidencyReviewed, input.PricingReviewed,
		input.SyntheticDataOnly, input.ImageExportReviewed, input.ExpiresAt.UTC(), actorID)
	item, err := scanSandboxApproval(row)
	if err != nil {
		return SandboxApproval{}, mapSandboxApprovalStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) RevokeSandboxApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	id string,
	reason string,
) (SandboxApproval, error) {
	if strings.TrimSpace(reason) == "" {
		return SandboxApproval{}, ErrInvalidApproval
	}
	row := s.db.QueryRowContext(ctx, `
WITH revoked AS (
  UPDATE model_sandbox_approval
  SET revoked_at = now(),
      revoked_by = NULLIF($3, '')::uuid,
      revoke_reason = $4
  WHERE tenant_id = $1 AND id::text = $2 AND revoked_at IS NULL
  RETURNING *
)
SELECT revoked.id::text, revoked.tenant_id::text,
       COALESCE(revoked.provider_id::text, ''), COALESCE(revoked.deployment_id::text, ''),
       COALESCE(NULLIF(revoked.provider_key, ''), provider.provider_key, ''), COALESCE(deployment.deployment_key, ''),
       COALESCE(revoked.managed_model_api_config_id::text, ''), revoked.model_name, revoked.model_version,
       revoked.protocol, revoked.approval_reference, revoked.approved_region,
       revoked.sandbox_account, revoked.contract_reviewed,
       revoked.retention_reviewed, revoked.data_residency_reviewed,
       revoked.pricing_reviewed, revoked.synthetic_data_only,
       revoked.image_export_reviewed, revoked.expires_at,
       revoked.revoked_at, revoked.created_at
FROM revoked
LEFT JOIN model_provider provider
  ON provider.tenant_id = revoked.tenant_id AND provider.id = revoked.provider_id
LEFT JOIN model_deployment deployment
  ON deployment.tenant_id = revoked.tenant_id AND deployment.id = revoked.deployment_id
`, tenantID, id, actorID, reason)
	item, err := scanSandboxApproval(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var revoked bool
			checkErr := s.db.QueryRowContext(ctx, `
SELECT revoked_at IS NOT NULL
FROM model_sandbox_approval
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, id).Scan(&revoked)
			if checkErr == nil && revoked {
				return SandboxApproval{}, ErrConflict
			}
			if checkErr != nil && !errors.Is(checkErr, sql.ErrNoRows) {
				return SandboxApproval{}, checkErr
			}
		}
		return SandboxApproval{}, mapSandboxApprovalStoreError(err)
	}
	return item, nil
}

func scanSandboxApproval(row rowScanner) (SandboxApproval, error) {
	var item SandboxApproval
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.ProviderID, &item.DeploymentID,
		&item.ProviderKey, &item.DeploymentKey,
		&item.ModelConfigID, &item.ModelName, &item.ModelVersion, &item.Protocol,
		&item.ApprovalReference, &item.ApprovedRegion, &item.SandboxAccount,
		&item.ContractReviewed, &item.RetentionReviewed,
		&item.DataResidencyReviewed, &item.PricingReviewed,
		&item.SyntheticDataOnly, &item.ImageExportReviewed,
		&item.ExpiresAt, &item.RevokedAt, &item.CreatedAt,
	); err != nil {
		return SandboxApproval{}, err
	}
	return item, nil
}

func mapSandboxApprovalStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidApproval
		}
	}
	return err
}
