package modelgovernance

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) ListProviders(ctx context.Context, tenantID string) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, provider_key, display_name, provider_kind,
       adapter_type, credential_ref <> '', split_part(credential_ref, '://', 1),
       region, data_policy, status, created_at, updated_at
FROM model_provider
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY provider_key
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		item, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateProvider(ctx context.Context, tenantID string, actorID string, input ProviderInput) (Provider, error) {
	provider := ProviderFromInput(input)
	if provider.Status == "" {
		provider.Status = "unverified"
		input.Status = provider.Status
	}
	if strings.TrimSpace(input.DisplayName) == "" || ValidateProvider(provider) != nil {
		return Provider{}, ErrInvalidProvider
	}
	if provider.Kind == ProviderExternal && provider.Status != "unverified" && provider.Status != "disabled" {
		return Provider{}, ErrConflict
	}
	dataPolicy, err := json.Marshal(input.DataPolicy)
	if err != nil {
		return Provider{}, ErrInvalidProvider
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  credential_ref, region, data_policy, status, created_by
)
VALUES (
  $1, $2, $3, $4, $5,
  $6, $7, $8, COALESCE(NULLIF($9, ''), 'unverified'), NULLIF($10, '')::uuid
)
RETURNING id::text, tenant_id::text, provider_key, display_name, provider_kind,
          adapter_type, credential_ref <> '', split_part(credential_ref, '://', 1),
          region, data_policy, status, created_at, updated_at
`, tenantID, input.Key, input.DisplayName, input.Kind, input.AdapterType,
		input.CredentialRef, input.Region, dataPolicy, input.Status, actorID)
	item, err := scanProvider(row)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) UpdateProviderStatus(ctx context.Context, tenantID string, id string, input ProviderStatusInput) (Provider, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE model_provider
SET status = $3, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND (provider_kind = 'local' OR $3 IN ('unverified', 'disabled'))
RETURNING id::text, tenant_id::text, provider_key, display_name, provider_kind,
          adapter_type, credential_ref <> '', split_part(credential_ref, '://', 1),
          region, data_policy, status, created_at, updated_at
`, tenantID, id, input.Status)
	item, err := scanProvider(row)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) ListDeployments(ctx context.Context, tenantID string) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT deployment.id::text, deployment.tenant_id::text, deployment.provider_id::text,
       provider.provider_key, deployment.deployment_key, deployment.model_name,
       deployment.model_version, deployment.region, deployment.capability_profile,
       deployment.modalities, deployment.capability_policy, deployment.pricing_policy,
       deployment.status, deployment.health_state, deployment.created_at, deployment.updated_at
FROM model_deployment deployment
JOIN model_provider provider
  ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
WHERE deployment.tenant_id = $1 AND deployment.deleted_at IS NULL
ORDER BY deployment.deployment_key
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		item, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateDeployment(ctx context.Context, tenantID string, actorID string, input DeploymentInput) (Deployment, error) {
	modalities, err := json.Marshal(input.Modalities)
	if err != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	capabilityPolicy, err := json.Marshal(nonNilMap(input.CapabilityPolicy))
	if err != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	pricingPolicy, err := json.Marshal(nonNilMap(input.PricingPolicy))
	if err != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	row := s.db.QueryRowContext(ctx, `
WITH provider AS (
  SELECT id, provider_key, provider_kind
  FROM model_provider
  WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
)
INSERT INTO model_deployment (
  tenant_id, provider_id, deployment_key, model_name, model_version,
  region, capability_profile, modalities, capability_policy, pricing_policy,
  health_state, status, created_by
)
SELECT
  $1, provider.id, $3, $4, $5,
  $6, $7, $8, $9, $10,
  COALESCE(NULLIF($11, ''), 'unverified'),
  COALESCE(NULLIF($12, ''), 'unverified'),
  NULLIF($13, '')::uuid
FROM provider
WHERE provider.provider_kind = 'local'
   OR (
     COALESCE(NULLIF($12, ''), 'unverified') = 'unverified'
     AND COALESCE(NULLIF($11, ''), 'unverified') = 'unverified'
   )
RETURNING id::text, tenant_id::text, provider_id::text,
          (SELECT provider_key FROM provider), deployment_key, model_name,
          model_version, region, capability_profile, modalities,
          capability_policy, pricing_policy, status, health_state, created_at, updated_at
`, tenantID, input.ProviderID, input.Key, input.ModelName, input.ModelVersion,
		input.Region, input.CapabilityProfile, modalities, capabilityPolicy, pricingPolicy,
		input.HealthState, input.Status, actorID)
	item, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) UpdateDeploymentState(ctx context.Context, tenantID string, id string, input DeploymentStateInput) (Deployment, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE model_deployment deployment
SET status = $3, health_state = $4, updated_at = now()
FROM model_provider provider
WHERE deployment.tenant_id = $1
  AND deployment.id::text = $2
  AND deployment.deleted_at IS NULL
  AND provider.tenant_id = deployment.tenant_id
  AND provider.id = deployment.provider_id
  AND (
    provider.provider_kind = 'local'
    OR (
      $3 IN ('unverified', 'disabled')
      AND $4 IN ('unverified', 'unavailable', 'disabled')
    )
  )
RETURNING deployment.id::text, deployment.tenant_id::text, deployment.provider_id::text,
          provider.provider_key, deployment.deployment_key, deployment.model_name,
          deployment.model_version, deployment.region, deployment.capability_profile,
          deployment.modalities, deployment.capability_policy, deployment.pricing_policy,
          deployment.status, deployment.health_state, deployment.created_at, deployment.updated_at
`, tenantID, id, input.Status, input.HealthState)
	item, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, mapStoreError(err)
	}
	return item, nil
}

// 扫描器把数据库中的密钥引用转成脱敏 Provider；查询层不应把 secret 内容映射到公开字段。
func scanProvider(row rowScanner) (Provider, error) {
	var item Provider
	var dataPolicy []byte
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.Key, &item.DisplayName, &item.Kind,
		&item.AdapterType, &item.CredentialConfigured, &item.CredentialScheme,
		&item.Region, &dataPolicy, &item.Status, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return Provider{}, err
	}
	if err := json.Unmarshal(dataPolicy, &item.DataPolicy); err != nil {
		return Provider{}, err
	}
	if !item.CredentialConfigured {
		item.CredentialScheme = ""
	}
	return item, nil
}

func scanDeployment(row rowScanner) (Deployment, error) {
	var item Deployment
	var modalities, capabilityPolicy, pricingPolicy []byte
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.ProviderID, &item.ProviderKey,
		&item.Key, &item.ModelName, &item.ModelVersion, &item.Region,
		&item.CapabilityProfile, &modalities, &capabilityPolicy, &pricingPolicy,
		&item.Status, &item.HealthState, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return Deployment{}, err
	}
	if err := json.Unmarshal(modalities, &item.Modalities); err != nil {
		return Deployment{}, err
	}
	if err := json.Unmarshal(capabilityPolicy, &item.CapabilityPolicy); err != nil {
		return Deployment{}, err
	}
	if err := json.Unmarshal(pricingPolicy, &item.PricingPolicy); err != nil {
		return Deployment{}, err
	}
	return item, nil
}
