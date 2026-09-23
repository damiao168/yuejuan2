package modelgovernance

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
  AND config.last_capability_probe_version = 'structured-json-v3'
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

func (s *PostgresStore) ListEvaluationRuns(ctx context.Context, tenantID string, filter EvaluationListFilter) ([]EvaluationRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_key, display_name,
       dataset_reference, dataset_sha256, authorization_reference, evidence_class,
       subject, grade, question_type, modality,
       sample_count, repeat_count, status,
       completed_at, invalidated_at, created_at
FROM model_evaluation_run
WHERE tenant_id = $1
  AND ($3 = '' OR created_at < $2 OR (created_at = $2 AND id::text < $3))
ORDER BY created_at DESC, id::text DESC
LIMIT NULLIF($4, 0)
`, tenantID, filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	runs := []EvaluationRun{}
	runIndex := map[string]int{}
	for rows.Next() {
		item, scanErr := scanEvaluationRun(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		runIndex[item.ID] = len(runs)
		runs = append(runs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(runs) == 0 {
		return runs, nil
	}
	placeholders := make([]string, len(runs))
	args := make([]any, 0, len(runs)+1)
	args = append(args, tenantID)
	for index, run := range runs {
		placeholders[index] = fmt.Sprintf("$%d::uuid", index+2)
		args = append(args, run.ID)
	}
	candidateRows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
       serious_error_samples, evidence_valid_samples,
       repeat_comparisons, stable_repeat_samples,
       p95_latency_ms, total_cost_micros, created_at
FROM model_evaluation_candidate
WHERE tenant_id = $1 AND run_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY model_name, deployment_key, id
`, args...)
	if err != nil {
		return nil, err
	}
	defer candidateRows.Close()
	for candidateRows.Next() {
		item, scanErr := scanEvaluationCandidate(candidateRows)
		if scanErr != nil {
			return nil, scanErr
		}
		index, ok := runIndex[item.RunID]
		if ok {
			runs[index].Candidates = append(runs[index].Candidates, item)
		}
	}
	if err := candidateRows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *PostgresStore) CreateEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	input EvaluationRunInput,
) (EvaluationRun, error) {
	if ValidateEvaluationRunInput(input) != nil {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_run (
  tenant_id, run_key, display_name, dataset_reference, dataset_sha256, authorization_reference,
  evidence_class, subject, grade, question_type, modality,
  sample_count, repeat_count, created_by
)
VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, NULLIF($14, '')::uuid
)
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, input.Key, strings.TrimSpace(input.DisplayName),
		input.DatasetReference, input.DatasetSHA256, strings.TrimSpace(input.AuthorizationRef),
		input.EvidenceClass,
		strings.TrimSpace(input.Subject), strings.TrimSpace(input.Grade),
		input.QuestionType, input.Modality, input.SampleCount, input.RepeatCount, actorID)
	item, err := scanEvaluationRun(row)
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) AddEvaluationCandidate(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	input EvaluationCandidateInput,
) (EvaluationCandidate, error) {
	run, err := s.evaluationRun(ctx, tenantID, runID)
	if err != nil {
		return EvaluationCandidate{}, err
	}
	if run.Status != EvaluationStatusDraft {
		return EvaluationCandidate{}, ErrConflict
	}
	if ValidateEvaluationCandidateInput(input, run) != nil {
		return EvaluationCandidate{}, ErrInvalidEvaluation
	}
	if input.ModelConfigID != "" {
		return s.addManagedEvaluationCandidate(ctx, tenantID, actorID, runID, input)
	}
	deployments, err := s.ListDeployments(ctx, tenantID)
	if err != nil {
		return EvaluationCandidate{}, err
	}
	var deployment Deployment
	var found bool
	for _, item := range deployments {
		if item.ID == input.DeploymentID {
			deployment = item
			found = true
			break
		}
	}
	if !found || !contains(deployment.Modalities, run.Modality) {
		return EvaluationCandidate{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_candidate (
  tenant_id, run_id, deployment_id,
  provider_key, deployment_key, model_name, model_version, prompt_version, rubric_version,
  evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
  serious_error_samples, evidence_valid_samples,
  repeat_comparisons, stable_repeat_samples,
  p95_latency_ms, total_cost_micros, created_by
)
SELECT
  $1, evaluation.id, deployment.id,
  provider.provider_key, deployment.deployment_key, deployment.model_name, deployment.model_version, $4, $5,
  $6, $7, $8,
  $9, $10,
  $11, $12,
  $13, $14, NULLIF($15, '')::uuid
FROM model_evaluation_run evaluation
JOIN model_deployment deployment
  ON deployment.tenant_id = evaluation.tenant_id
 AND deployment.id::text = $3
 AND deployment.deleted_at IS NULL
JOIN model_provider provider
  ON provider.tenant_id = deployment.tenant_id
 AND provider.id = deployment.provider_id
 AND provider.deleted_at IS NULL
WHERE evaluation.tenant_id = $1
  AND evaluation.id::text = $2
  AND evaluation.status = 'draft'
RETURNING id::text, tenant_id::text, run_id::text, deployment_id::text,
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          deployment_key, model_name, model_version, prompt_version, rubric_version,
          evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
          serious_error_samples, evidence_valid_samples,
          repeat_comparisons, stable_repeat_samples,
          p95_latency_ms, total_cost_micros, created_at
`, tenantID, runID, input.DeploymentID,
		strings.TrimSpace(input.PromptVersion), strings.TrimSpace(input.RubricVersion),
		input.EvaluatedSamples, input.TeacherReviewedSamples, input.TeacherAcceptedSamples,
		input.SeriousErrorSamples, input.EvidenceValidSamples,
		input.RepeatComparisons, input.StableRepeatSamples,
		input.P95LatencyMS, input.TotalCostMicros, actorID)
	item, err := scanEvaluationCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationCandidate{}, ErrConflict
	}
	if err != nil {
		return EvaluationCandidate{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) addManagedEvaluationCandidate(
	ctx context.Context, tenantID, actorID, runID string, input EvaluationCandidateInput,
) (EvaluationCandidate, error) {
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_candidate (
  tenant_id, run_id, managed_model_api_config_id,
  provider_key, model_name, model_version, prompt_version, rubric_version,
  evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
  serious_error_samples, evidence_valid_samples, repeat_comparisons,
  stable_repeat_samples, p95_latency_ms, total_cost_micros, created_by
)
SELECT evaluation.tenant_id, evaluation.id, config.id,
       config.provider_key, config.model_name, config.model_version, $4, $5,
       $6, $7, $8, $9, $10, $11, $12, $13, $14, NULLIF($15, '')::uuid
FROM model_evaluation_run evaluation
JOIN managed_model_api_config config
  ON config.tenant_id = evaluation.tenant_id
 AND config.id::text = $3
 AND config.deleted_at IS NULL
 AND config.status = 'active'
 AND config.last_capability_status = 'success'
 AND config.last_capability_probe_version = 'structured-json-v3'
 AND config.modalities ? evaluation.modality
WHERE evaluation.tenant_id = $1 AND evaluation.id::text = $2
  AND evaluation.status = 'draft'
RETURNING id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
          managed_model_api_config_id::text, provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
          serious_error_samples, evidence_valid_samples, repeat_comparisons,
          stable_repeat_samples, p95_latency_ms, total_cost_micros, created_at
`, tenantID, runID, input.ModelConfigID,
		strings.TrimSpace(input.PromptVersion), strings.TrimSpace(input.RubricVersion),
		input.EvaluatedSamples, input.TeacherReviewedSamples, input.TeacherAcceptedSamples,
		input.SeriousErrorSamples, input.EvidenceValidSamples,
		input.RepeatComparisons, input.StableRepeatSamples,
		input.P95LatencyMS, input.TotalCostMicros, actorID)
	item, err := scanEvaluationCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationCandidate{}, ErrNotFound
	}
	if err != nil {
		return EvaluationCandidate{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) CompleteEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationRun{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE model_evaluation_run
SET status = 'completed',
    completed_at = now(),
    completed_by = NULLIF($3, '')::uuid
WHERE tenant_id = $1
  AND id::text = $2
  AND status = 'draft'
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, runID, actorID)
	item, err := scanEvaluationRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return EvaluationRun{}, rollbackErr
		}
		return EvaluationRun{}, s.evaluationTransitionError(ctx, tenantID, runID)
	}
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return s.evaluationRunWithCandidates(ctx, tenantID, item.ID)
}

func (s *PostgresStore) InvalidateEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_evaluation_run
SET status = 'invalidated',
    invalidated_at = now(),
    invalidated_by = NULLIF($3, '')::uuid,
    invalidation_reason = $4
WHERE tenant_id = $1
  AND id::text = $2
  AND status <> 'invalidated'
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, runID, actorID, reason)
	item, err := scanEvaluationRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationRun{}, s.evaluationTransitionError(ctx, tenantID, runID)
	}
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return s.evaluationRunWithCandidates(ctx, tenantID, item.ID)
}

func (s *PostgresStore) evaluationRun(ctx context.Context, tenantID string, runID string) (EvaluationRun, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, run_key, display_name,
       dataset_reference, dataset_sha256, authorization_reference, evidence_class,
       subject, grade, question_type, modality,
       sample_count, repeat_count, status,
       completed_at, invalidated_at, created_at
FROM model_evaluation_run
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, runID)
	item, err := scanEvaluationRun(row)
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) evaluationRunWithCandidates(
	ctx context.Context,
	tenantID string,
	runID string,
) (EvaluationRun, error) {
	item, err := s.evaluationRun(ctx, tenantID, runID)
	if err != nil {
		return EvaluationRun{}, err
	}
	candidates, err := s.evaluationCandidates(ctx, tenantID, runID)
	if err != nil {
		return EvaluationRun{}, err
	}
	item.Candidates = candidates
	return item, nil
}

func (s *PostgresStore) evaluationCandidates(ctx context.Context, tenantID string, runID string) ([]EvaluationCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
       serious_error_samples, evidence_valid_samples,
       repeat_comparisons, stable_repeat_samples,
       p95_latency_ms, total_cost_micros, created_at
FROM model_evaluation_candidate
WHERE tenant_id = $1 AND run_id::text = $2
ORDER BY model_name, deployment_key, id
`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EvaluationCandidate{}
	for rows.Next() {
		item, scanErr := scanEvaluationCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) evaluationTransitionError(ctx context.Context, tenantID string, runID string) error {
	var status string
	err := s.db.QueryRowContext(ctx, `
SELECT status
FROM model_evaluation_run
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func (s *PostgresStore) ListModelApprovals(ctx context.Context, tenantID string) ([]ModelApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, evaluation_run_id::text,
       evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       dataset_reference, dataset_sha256, authorization_reference,
       subject, grade, question_type, modality, manual_review_rate,
       decision_reference, expires_at, revoked_at, revision, created_at
FROM model_approval
WHERE tenant_id = $1
ORDER BY created_at DESC, id
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelApproval{}
	for rows.Next() {
		item, scanErr := scanModelApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateModelApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	input ModelApprovalInput,
) (ModelApproval, error) {
	now := time.Now().UTC()
	if ValidateModelApprovalInput(input, now) != nil {
		return ModelApproval{}, ErrInvalidPromotion
	}
	run, err := s.evaluationRunWithCandidates(ctx, tenantID, input.EvaluationRunID)
	if err != nil {
		return ModelApproval{}, err
	}
	if run.Status != EvaluationStatusCompleted ||
		run.EvidenceClass != EvaluationEvidenceAuthorizedFrozenSet {
		return ModelApproval{}, ErrInvalidPromotion
	}
	var candidate EvaluationCandidate
	found := false
	for _, item := range run.Candidates {
		if (input.ModelConfigID != "" && item.ModelConfigID == input.ModelConfigID) ||
			(input.DeploymentID != "" && item.DeploymentID == input.DeploymentID) {
			candidate = item
			found = true
			break
		}
	}
	if !found {
		return ModelApproval{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelApproval{}, err
	}
	defer tx.Rollback()
	lockKey := strings.Join([]string{
		tenantID, candidate.ModelConfigID, candidate.DeploymentID, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.Subject, run.Grade, run.QuestionType, run.Modality,
	}, "\x1f")
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return ModelApproval{}, err
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM model_approval
  WHERE tenant_id = $1
    AND ((NULLIF($2, '') IS NOT NULL AND managed_model_api_config_id::text = $2)
         OR (NULLIF($2, '') IS NULL AND deployment_id::text = $3))
    AND model_version = $4
    AND prompt_version = $5
    AND rubric_version = $6
    AND subject = $7
    AND grade = $8
    AND question_type = $9
    AND modality = $10
    AND revoked_at IS NULL
    AND expires_at > now()
)
`, tenantID, candidate.ModelConfigID, candidate.DeploymentID, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.Subject, run.Grade, run.QuestionType, run.Modality).Scan(&duplicate); err != nil {
		return ModelApproval{}, err
	}
	if duplicate {
		return ModelApproval{}, ErrConflict
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO model_approval (
  tenant_id, evaluation_run_id, evaluation_candidate_id, deployment_id,
  managed_model_api_config_id, provider_key, deployment_key, model_name,
  model_version, prompt_version, rubric_version,
  dataset_reference, dataset_sha256, authorization_reference,
  subject, grade, question_type, modality,
  manual_review_rate, decision_reference, expires_at, created_by
)
VALUES (
  $1, $2, $3, NULLIF($4, '')::uuid,
  NULLIF($5, '')::uuid, $6, NULLIF($7, ''), $8,
  $9, $10, $11,
  $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, NULLIF($22, '')::uuid
)
RETURNING id::text, tenant_id::text, evaluation_run_id::text,
          evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          dataset_reference, dataset_sha256, authorization_reference,
          subject, grade, question_type, modality, manual_review_rate,
          decision_reference, expires_at, revoked_at, revision, created_at
`, tenantID, run.ID, candidate.ID, candidate.DeploymentID,
		candidate.ModelConfigID, candidate.ProviderKey, candidate.DeploymentKey,
		candidate.ModelName, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.DatasetReference, run.DatasetSHA256, run.AuthorizationRef,
		run.Subject, run.Grade, run.QuestionType, run.Modality,
		input.ManualReviewRate, strings.TrimSpace(input.DecisionReference),
		input.ExpiresAt.UTC(), actorID)
	item, err := scanModelApproval(row)
	if err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) RevokeModelApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	id string,
	input ModelApprovalRevokeInput,
) (ModelApproval, error) {
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedRevision < 1 {
		return ModelApproval{}, ErrInvalidPromotion
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_approval
SET revoked_at = now(),
    revoked_by = NULLIF($3, '')::uuid,
    revocation_reason = $4,
    revision = revision + 1
WHERE tenant_id = $1
  AND id::text = $2
  AND revoked_at IS NULL
  AND revision = $5
RETURNING id::text, tenant_id::text, evaluation_run_id::text,
          evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          dataset_reference, dataset_sha256, authorization_reference,
          subject, grade, question_type, modality, manual_review_rate,
          decision_reference, expires_at, revoked_at, revision, created_at
`, tenantID, id, actorID, strings.TrimSpace(input.Reason), input.ExpectedRevision)
	item, err := scanModelApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		var currentRevision int64
		var revokedAt sql.NullTime
		checkErr := s.db.QueryRowContext(ctx,
			`SELECT revision, revoked_at FROM model_approval WHERE tenant_id = $1 AND id::text = $2`,
			tenantID, id,
		).Scan(&currentRevision, &revokedAt)
		if errors.Is(checkErr, sql.ErrNoRows) {
			return ModelApproval{}, ErrNotFound
		}
		if checkErr != nil {
			return ModelApproval{}, checkErr
		}
		if currentRevision != input.ExpectedRevision {
			return ModelApproval{}, ErrRevisionConflict
		}
		if revokedAt.Valid {
			return ModelApproval{}, ErrConflict
		}
		return ModelApproval{}, ErrConflict
	}
	if err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

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

func scanEvaluationRun(row rowScanner) (EvaluationRun, error) {
	var item EvaluationRun
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.Key, &item.DisplayName,
		&item.DatasetReference, &item.DatasetSHA256, &item.AuthorizationRef, &item.EvidenceClass,
		&item.Subject, &item.Grade, &item.QuestionType, &item.Modality,
		&item.SampleCount, &item.RepeatCount, &item.Status,
		&item.CompletedAt, &item.InvalidatedAt, &item.CreatedAt,
	); err != nil {
		return EvaluationRun{}, err
	}
	item.Candidates = []EvaluationCandidate{}
	return item, nil
}

func scanEvaluationCandidate(row rowScanner) (EvaluationCandidate, error) {
	var item EvaluationCandidate
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.RunID, &item.DeploymentID,
		&item.ModelConfigID, &item.ProviderKey, &item.DeploymentKey,
		&item.ModelName, &item.ModelVersion,
		&item.PromptVersion, &item.RubricVersion,
		&item.EvaluatedSamples, &item.TeacherReviewedSamples,
		&item.TeacherAcceptedSamples, &item.SeriousErrorSamples,
		&item.EvidenceValidSamples, &item.RepeatComparisons,
		&item.StableRepeatSamples, &item.P95LatencyMS,
		&item.TotalCostMicros, &item.CreatedAt,
	); err != nil {
		return EvaluationCandidate{}, err
	}
	return PopulateEvaluationMetrics(item), nil
}

func scanModelApproval(row rowScanner) (ModelApproval, error) {
	var item ModelApproval
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.EvaluationRunID,
		&item.EvaluationCandidateID, &item.DeploymentID,
		&item.ModelConfigID, &item.ProviderKey, &item.DeploymentKey,
		&item.ModelName, &item.ModelVersion,
		&item.PromptVersion, &item.RubricVersion,
		&item.DatasetReference, &item.DatasetSHA256, &item.AuthorizationRef,
		&item.Subject, &item.Grade, &item.QuestionType, &item.Modality,
		&item.ManualReviewRate, &item.DecisionReference,
		&item.ExpiresAt, &item.RevokedAt, &item.Revision, &item.CreatedAt,
	); err != nil {
		return ModelApproval{}, err
	}
	return item, nil
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

func mapEvaluationStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidEvaluation
		}
	}
	return err
}

func mapModelApprovalStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidPromotion
		}
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
