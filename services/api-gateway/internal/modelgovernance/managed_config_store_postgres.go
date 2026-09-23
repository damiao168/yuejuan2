package modelgovernance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *PostgresStore) ListManagedAPIConfigs(ctx context.Context, tenantID string) ([]ManagedAPIConfig, error) {
	rows, err := s.db.QueryContext(ctx, managedAPIConfigSelect+`
WHERE tenant_id=$1::uuid AND deleted_at IS NULL
ORDER BY is_default DESC, updated_at DESC, provider_key`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ManagedAPIConfig{}
	for rows.Next() {
		item, scanErr := scanManagedAPIConfig(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateManagedAPIConfig(ctx context.Context, tenantID, actorID string, input ManagedAPIConfigInput) (ManagedAPIConfig, error) {
	if s.credentialCipher == nil {
		return ManagedAPIConfig{}, ErrManagedConfigUnavailable
	}
	input.TenantID = tenantID
	normalized, err := normalizeManagedAPIInput(input, true)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	id := uuid.NewString()
	ciphertext, nonce, err := s.credentialCipher.Encrypt(normalized.APIKey, tenantID, id)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	defer tx.Rollback()
	if normalized.IsDefault {
		if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config SET is_default=false,updated_at=now() WHERE tenant_id=$1::uuid AND is_default AND deleted_at IS NULL`, tenantID); err != nil {
			return ManagedAPIConfig{}, err
		}
	}
	probe := managedProbePersistence(normalized.InitialProbe)
	row := tx.QueryRowContext(ctx, `
INSERT INTO managed_model_api_config(
  id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
  credential_ciphertext,credential_nonce,credential_hint,status,is_default,last_test_status,last_test_message,
  last_test_latency_ms,last_tested_at,last_successful_tested_at,last_probe_mode,last_capability_status,last_capability_message,
  last_capability_tested_at,last_capability_probe_version,last_capability_usage,last_capability_diagnostic,
  config_source,provider_registry_version,created_by
)
VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,CASE WHEN $15='success' THEN $18 ELSE NULL END,$19,$20,$21,$22,$23,$24::jsonb,$25::jsonb,$26,$27,NULLIF($28,'')::uuid)
RETURNING id::text,tenant_id::text,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
          true,credential_hint,status,is_default,last_test_status,last_test_message,last_test_latency_ms,last_tested_at,last_successful_tested_at,
          last_probe_mode,last_capability_status,last_capability_message,last_capability_tested_at,last_capability_probe_version,last_capability_usage,last_capability_diagnostic,
          modalities,capability_profile,pricing_policy,data_policy,health_state,
          config_source,provider_registry_version,created_at,updated_at
`, id, tenantID, normalized.ProviderKey, normalized.DisplayName, normalized.AdapterType,
		normalized.BaseURL, normalized.ModelName, normalized.ModelVersion, normalized.Region,
		ciphertext, nonce, credentialHint(normalized.APIKey), normalized.Status, normalized.IsDefault,
		probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt, probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic,
		normalized.ConfigSource, normalized.ProviderRegistryVersion, actorID)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		return ManagedAPIConfig{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return ManagedAPIConfig{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateManagedAPIConfig(ctx context.Context, tenantID, id string, input ManagedAPIConfigUpdateInput) (ManagedAPIConfig, error) {
	if s.credentialCipher == nil {
		return ManagedAPIConfig{}, ErrManagedConfigUnavailable
	}
	normalized, err := normalizeManagedAPIUpdate(input)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	defer tx.Rollback()
	if normalized.IsDefault {
		if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config SET is_default=false,updated_at=now() WHERE tenant_id=$1::uuid AND id<>$2::uuid AND is_default AND deleted_at IS NULL`, tenantID, id); err != nil {
			return ManagedAPIConfig{}, err
		}
	}
	var ciphertext, nonce []byte
	if normalized.APIKey != "" {
		ciphertext, nonce, err = s.credentialCipher.Encrypt(normalized.APIKey, tenantID, id)
		if err != nil {
			return ManagedAPIConfig{}, err
		}
	}
	probe := managedProbePersistence(normalized.InitialProbe)
	row := tx.QueryRowContext(ctx, `
UPDATE managed_model_api_config
SET display_name=$3,adapter_type=$4,base_url=$5,model_name=$6,model_version=$7,region=$8,
    credential_ciphertext=CASE WHEN $9::bytea IS NULL THEN credential_ciphertext ELSE $9::bytea END,
    credential_nonce=CASE WHEN $10::bytea IS NULL THEN credential_nonce ELSE $10::bytea END,
    credential_hint=CASE WHEN $9::bytea IS NULL THEN credential_hint ELSE $11 END,
    status=$12,is_default=$13,
    last_test_status=CASE WHEN $14 THEN $15 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_test_status ELSE 'untested' END,
    last_test_message=CASE WHEN $14 THEN $16 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_test_message ELSE '' END,
    last_test_latency_ms=CASE WHEN $14 THEN $17 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_test_latency_ms ELSE NULL END,
    last_tested_at=CASE WHEN $14 THEN $18 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_tested_at ELSE NULL END,
    last_successful_tested_at=CASE WHEN $14 AND $15='success' THEN $18 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_successful_tested_at ELSE NULL END,
    last_probe_mode=CASE WHEN $14 THEN $19 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_probe_mode ELSE '' END,
    last_capability_status=CASE WHEN $20 THEN $21 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_status ELSE 'untested' END,
    last_capability_message=CASE WHEN $20 THEN $22 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_message ELSE '' END,
    last_capability_tested_at=CASE WHEN $20 THEN $23 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_tested_at ELSE NULL END,
    last_capability_probe_version=CASE WHEN $20 THEN $24 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_probe_version ELSE '' END,
    last_capability_usage=CASE WHEN $20 THEN $25::jsonb WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_usage ELSE '{}'::jsonb END,
    last_capability_diagnostic=CASE WHEN $20 THEN $26::jsonb WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_capability_diagnostic ELSE '{}'::jsonb END,
    updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
RETURNING id::text,tenant_id::text,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
          true,credential_hint,status,is_default,last_test_status,last_test_message,last_test_latency_ms,last_tested_at,last_successful_tested_at,
          last_probe_mode,last_capability_status,last_capability_message,last_capability_tested_at,last_capability_probe_version,last_capability_usage,last_capability_diagnostic,
          modalities,capability_profile,pricing_policy,data_policy,health_state,
          config_source,provider_registry_version,created_at,updated_at
`, tenantID, id, normalized.DisplayName, normalized.AdapterType, normalized.BaseURL,
		normalized.ModelName, normalized.ModelVersion, normalized.Region,
		nullBytes(ciphertext), nullBytes(nonce), credentialHint(normalized.APIKey), normalized.Status, normalized.IsDefault,
		probe.HasProbe, probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.UpdateCapability, probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt,
		probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		return ManagedAPIConfig{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return ManagedAPIConfig{}, err
	}
	return item, nil
}

func (s *PostgresStore) DeleteManagedAPIConfig(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var isDefault bool
	if err = tx.QueryRowContext(ctx, `SELECT is_default FROM managed_model_api_config WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&isDefault); err != nil {
		return mapStoreError(err)
	}
	if isDefault {
		return ErrManagedDefaultMutation
	}
	var inUse bool
	if err = tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM model_role_binding WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND status='active')
    OR EXISTS(SELECT 1 FROM model_approval WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND revoked_at IS NULL AND expires_at > now())
    OR EXISTS(SELECT 1 FROM model_sandbox_approval WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND revoked_at IS NULL AND expires_at > now())
    OR EXISTS(SELECT 1 FROM tenant_model_policy_model WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND enabled)
`, tenantID, id).Scan(&inUse); err != nil {
		return mapStoreError(err)
	}
	if inUse {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config SET status='disabled',is_default=false,deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id); err != nil {
		return mapStoreError(err)
	}
	return tx.Commit()
}

func (s *PostgresStore) GetManagedAPIConnection(ctx context.Context, tenantID, id string) (ManagedAPIConnection, error) {
	if s.credentialCipher == nil {
		return ManagedAPIConnection{}, ErrManagedConfigUnavailable
	}
	row := s.db.QueryRowContext(ctx, managedAPIConfigSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		return ManagedAPIConnection{}, mapStoreError(err)
	}
	var ciphertext, nonce []byte
	if err = s.db.QueryRowContext(ctx, `SELECT credential_ciphertext,credential_nonce FROM managed_model_api_config WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id).Scan(&ciphertext, &nonce); err != nil {
		return ManagedAPIConnection{}, mapStoreError(err)
	}
	apiKey, err := s.credentialCipher.Decrypt(ciphertext, nonce, tenantID, id)
	if err != nil {
		return ManagedAPIConnection{}, err
	}
	return ManagedAPIConnection{Config: item, APIKey: apiKey}, nil
}

func (s *PostgresStore) RecordManagedAPIProbe(ctx context.Context, tenantID, id string, expectedUpdatedAt time.Time, result ManagedAPIProbeResult) (ManagedAPIConfig, error) {
	probe := managedProbePersistence(&result)
	row := s.db.QueryRowContext(ctx, `
UPDATE managed_model_api_config
SET last_test_status=$4,last_test_message=$5,last_test_latency_ms=$6,last_tested_at=$7,last_probe_mode=$8,
    last_successful_tested_at=CASE WHEN $4='success' THEN $7 ELSE last_successful_tested_at END,
    last_capability_status=CASE WHEN $9 THEN $10 ELSE last_capability_status END,
    last_capability_message=CASE WHEN $9 THEN $11 ELSE last_capability_message END,
    last_capability_tested_at=CASE WHEN $9 THEN $12 ELSE last_capability_tested_at END,
    last_capability_probe_version=CASE WHEN $9 THEN $13 ELSE last_capability_probe_version END,
    last_capability_usage=CASE WHEN $9 THEN $14::jsonb ELSE last_capability_usage END,
    last_capability_diagnostic=CASE WHEN $9 THEN $15::jsonb ELSE last_capability_diagnostic END
WHERE tenant_id=$1::uuid AND id=$2::uuid AND updated_at=$3 AND deleted_at IS NULL
RETURNING id::text,tenant_id::text,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
          true,credential_hint,status,is_default,last_test_status,last_test_message,last_test_latency_ms,last_tested_at,last_successful_tested_at,
          last_probe_mode,last_capability_status,last_capability_message,last_capability_tested_at,last_capability_probe_version,last_capability_usage,last_capability_diagnostic,
          modalities,capability_profile,pricing_policy,data_policy,health_state,
          config_source,provider_registry_version,created_at,updated_at
`, tenantID, id, expectedUpdatedAt, probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.UpdateCapability, probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt,
		probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ManagedAPIConfig{}, ErrManagedProbeStale
		}
		return ManagedAPIConfig{}, mapStoreError(err)
	}
	return item, nil
}

const managedAPIConfigSelect = `
SELECT id::text,tenant_id::text,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
       true,credential_hint,status,is_default,last_test_status,last_test_message,last_test_latency_ms,last_tested_at,last_successful_tested_at,
       last_probe_mode,last_capability_status,last_capability_message,last_capability_tested_at,last_capability_probe_version,last_capability_usage,
       last_capability_diagnostic,
       modalities,capability_profile,pricing_policy,data_policy,health_state,
       config_source,provider_registry_version,created_at,updated_at
FROM managed_model_api_config
`

func scanManagedAPIConfig(row rowScanner) (ManagedAPIConfig, error) {
	var item ManagedAPIConfig
	var lastTestLatency sql.NullInt64
	var capabilityUsage []byte
	var capabilityDiagnostic []byte
	var modalities, pricingPolicy, dataPolicy []byte
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.ProviderKey, &item.DisplayName, &item.AdapterType,
		&item.BaseURL, &item.ModelName, &item.ModelVersion, &item.Region,
		&item.CredentialConfigured, &item.CredentialHint, &item.Status, &item.IsDefault,
		&item.LastTestStatus, &item.LastTestMessage, &lastTestLatency, &item.LastTestedAt, &item.LastSuccessfulTestedAt,
		&item.LastProbeMode, &item.LastCapabilityStatus, &item.LastCapabilityMessage,
		&item.LastCapabilityTestedAt, &item.LastCapabilityVersion, &capabilityUsage, &capabilityDiagnostic,
		&modalities, &item.CapabilityProfile, &pricingPolicy, &dataPolicy, &item.HealthState,
		&item.ConfigSource, &item.ProviderRegistryVersion, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return ManagedAPIConfig{}, err
	}
	if lastTestLatency.Valid {
		item.LastTestLatencyMS = lastTestLatency.Int64
	}
	if len(capabilityUsage) > 0 {
		_ = json.Unmarshal(capabilityUsage, &item.LastCapabilityUsage)
	}
	if len(capabilityDiagnostic) > 0 {
		_ = json.Unmarshal(capabilityDiagnostic, &item.LastCapabilityDiagnostic)
	}
	if err := json.Unmarshal(modalities, &item.Modalities); err != nil {
		return ManagedAPIConfig{}, err
	}
	if err := json.Unmarshal(pricingPolicy, &item.PricingPolicy); err != nil {
		return ManagedAPIConfig{}, err
	}
	if err := json.Unmarshal(dataPolicy, &item.DataPolicy); err != nil {
		return ManagedAPIConfig{}, err
	}
	return item, nil
}

type managedProbePersistenceValues struct {
	HasProbe             bool
	Status               string
	Message              string
	Latency              int64
	TestedAt             any
	Mode                 string
	UpdateCapability     bool
	CapabilityStatus     string
	CapabilityMessage    string
	CapabilityTestedAt   any
	CapabilityVersion    string
	CapabilityUsage      string
	CapabilityDiagnostic string
}

func managedProbePersistence(result *ManagedAPIProbeResult) managedProbePersistenceValues {
	values := managedProbePersistenceValues{
		Status: "untested", CapabilityStatus: "untested", CapabilityUsage: `{}`, CapabilityDiagnostic: `{}`,
	}
	if result == nil {
		return values
	}
	values.HasProbe = true
	values.Status = "failed"
	connectionOK := result.OK || (result.ProbeMode == "capability" && result.CredentialCheck.OK && result.ModelCheck.OK)
	transientFailure := !result.OK && transientManagedProbeFailure(*result)
	if transientFailure {
		values.Status = "temporary_unavailable"
	} else if connectionOK {
		values.Status = "success"
	}
	if connectionOK && !result.OK && !transientFailure {
		values.Message = "连接正常，结构化能力检测未通过"
	} else {
		values.Message = boundedManagedProbeMessage(result.Message)
	}
	values.Latency = result.LatencyMS
	now := time.Now().UTC()
	values.TestedAt = now
	values.Mode = result.ProbeMode
	if values.Mode == "" {
		values.Mode = "capability"
	}
	values.UpdateCapability = values.Mode == "capability" && !result.Reused && !transientFailure &&
		(result.GeneratedRequest || result.CapabilityCheck.OK || result.CapabilityCheck.Code != "")
	if values.UpdateCapability {
		values.CapabilityStatus = "failed"
		if result.CapabilityCheck.OK {
			values.CapabilityStatus = "success"
		}
		values.CapabilityMessage = boundedManagedProbeMessage(result.CapabilityCheck.Message)
		if values.CapabilityMessage == "" {
			values.CapabilityMessage = values.Message
		}
		values.CapabilityTestedAt = now
		values.CapabilityVersion = ManagedCapabilityProbeVersion
		if usage, err := json.Marshal(result.Usage); err == nil {
			values.CapabilityUsage = string(usage)
		}
		diagnostic := result.Diagnostic
		diagnostic.ContentPreview = ""
		if encoded, err := json.Marshal(diagnostic); err == nil {
			values.CapabilityDiagnostic = string(encoded)
		}
	}
	return values
}

func boundedManagedProbeMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 256 {
		return message[:256]
	}
	return message
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

var _ ManagedAPIConfigStore = (*PostgresStore)(nil)
