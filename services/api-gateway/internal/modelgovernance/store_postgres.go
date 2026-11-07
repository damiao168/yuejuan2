package modelgovernance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db               *sql.DB
	credentialCipher *CredentialCipher
}

func NewPostgresStore(db *sql.DB, credentialCiphers ...*CredentialCipher) *PostgresStore {
	var credentialCipher *CredentialCipher
	if len(credentialCiphers) > 0 {
		credentialCipher = credentialCiphers[0]
	}
	return &PostgresStore{db: db, credentialCipher: credentialCipher}
}

func (s *PostgresStore) ValidateProductionReadiness(ctx context.Context, secrets SecretReferenceResolver) error {
	providers, deployments, err := s.productionInventory(ctx)
	if err != nil {
		return err
	}
	return ValidateProductionInventory(providers, deployments, secrets)
}

func (s *PostgresStore) EnsureLocalBaseline(ctx context.Context, tenantID string, baseline LocalBaseline) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  credential_ref, region, data_policy, status
)
SELECT
  tenant.id, $2, $3, 'local', $4,
  '', $5, '{"training_allowed":false,"retention_mode":"no_store"}', 'active'
FROM tenant
WHERE tenant.deleted_at IS NULL
  AND ($1 = '' OR tenant.id::text = $1)
ON CONFLICT (tenant_id, provider_key) DO UPDATE
SET display_name = EXCLUDED.display_name,
    adapter_type = EXCLUDED.adapter_type,
    region = EXCLUDED.region,
    updated_at = now()
`, tenantID, baseline.ProviderKey, baseline.ProviderName, baseline.AdapterType, baseline.Region); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO model_deployment (
  tenant_id, provider_id, deployment_key, model_name, model_version,
  region, capability_profile, modalities, capability_policy, pricing_policy,
  health_state, status
)
SELECT
  provider.tenant_id, provider.id, $2, $3, $4,
  $5, $6, '["text"]', '{}', '{"meter":"local_compute"}',
  'unverified', 'shadow_only'
FROM model_provider provider
WHERE provider.provider_key = $7
  AND provider.deleted_at IS NULL
  AND ($1 = '' OR provider.tenant_id::text = $1)
ON CONFLICT (tenant_id, deployment_key) DO UPDATE
SET provider_id = EXCLUDED.provider_id,
    model_name = EXCLUDED.model_name,
    model_version = EXCLUDED.model_version,
    region = EXCLUDED.region,
    capability_profile = EXCLUDED.capability_profile,
    updated_at = now()
`, tenantID, baseline.DeploymentKey, baseline.ModelName, baseline.ModelVersion,
		baseline.Region, baseline.CapabilityProfile, baseline.ProviderKey); err != nil {
		return err
	}
	return tx.Commit()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func mapStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (s *PostgresStore) productionInventory(ctx context.Context) ([]Provider, []Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, provider_key, display_name, provider_kind,
       adapter_type, credential_ref, region, data_policy, status, created_at, updated_at
FROM model_provider
WHERE deleted_at IS NULL
ORDER BY tenant_id, provider_key
`)
	if err != nil {
		return nil, nil, err
	}
	providers := []Provider{}
	for rows.Next() {
		var item Provider
		var dataPolicy []byte
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Key, &item.DisplayName, &item.Kind,
			&item.AdapterType, &item.CredentialRef, &item.Region, &dataPolicy,
			&item.Status, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if err := json.Unmarshal(dataPolicy, &item.DataPolicy); err != nil {
			rows.Close()
			return nil, nil, err
		}
		providers = append(providers, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	deploymentRows, err := s.db.QueryContext(ctx, `
SELECT deployment.id::text, deployment.tenant_id::text, deployment.provider_id::text,
       provider.provider_key, deployment.deployment_key, deployment.model_name,
       deployment.model_version, deployment.region, deployment.capability_profile,
       deployment.modalities, deployment.capability_policy, deployment.pricing_policy,
       deployment.status, deployment.health_state, deployment.created_at, deployment.updated_at
FROM model_deployment deployment
JOIN model_provider provider
  ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
WHERE deployment.deleted_at IS NULL
ORDER BY deployment.tenant_id, deployment.deployment_key
`)
	if err != nil {
		return nil, nil, err
	}
	defer deploymentRows.Close()
	deployments := []Deployment{}
	for deploymentRows.Next() {
		item, err := scanDeployment(deploymentRows)
		if err != nil {
			return nil, nil, err
		}
		deployments = append(deployments, item)
	}
	if err := deploymentRows.Err(); err != nil {
		return nil, nil, err
	}
	return providers, deployments, nil
}
