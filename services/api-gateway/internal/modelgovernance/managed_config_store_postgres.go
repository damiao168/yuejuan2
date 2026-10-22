package modelgovernance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

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

// 密钥先按租户和新配置 ID 加密，再在同一事务内切换默认配置，避免半保存状态。
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
		if err = preserveInheritedChatAccessPostgres(ctx, tx, tenantID, id, true); err != nil {
			return ManagedAPIConfig{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config SET is_default=false,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE tenant_id=$1::uuid AND is_default AND deleted_at IS NULL`, tenantID); err != nil {
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
  config_source,provider_registry_version,created_by,
  last_text_status,last_text_message,last_text_tested_at,last_text_usage,last_text_diagnostic
)
VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::timestamptz,CASE WHEN $15='success' THEN $18::timestamptz ELSE NULL END,$19,$20,$21,$22,$23,$24::jsonb,$25::jsonb,$26,$27,NULLIF($28,'')::uuid,$29,$30,$31,$32::jsonb,$33::jsonb)
RETURNING `+managedAPIConfigColumns+`
`, id, tenantID, normalized.ProviderKey, normalized.DisplayName, normalized.AdapterType,
		normalized.BaseURL, normalized.ModelName, normalized.ModelVersion, normalized.Region,
		ciphertext, nonce, credentialHint(normalized.APIKey), normalized.Status, normalized.IsDefault,
		probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt, probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic,
		normalized.ConfigSource, normalized.ProviderRegistryVersion, actorID,
		probe.TextStatus, probe.TextMessage, probe.TextTestedAt, probe.TextUsage, probe.TextDiagnostic)
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
	if normalized.InitialProbe != nil && normalized.ExpectedUpdatedAt.IsZero() {
		return ManagedAPIConfig{}, ErrManagedProbeStale
	}
	var expectedUpdatedAt any
	if !normalized.ExpectedUpdatedAt.IsZero() {
		expectedUpdatedAt = normalized.ExpectedUpdatedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	defer tx.Rollback()
	if err = preserveInheritedChatAccessPostgres(ctx, tx, tenantID, id, normalized.IsDefault); err != nil {
		return ManagedAPIConfig{}, err
	}
	if normalized.IsDefault {
		if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config SET is_default=false,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE tenant_id=$1::uuid AND id<>$2::uuid AND is_default AND deleted_at IS NULL`, tenantID, id); err != nil {
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
	// 更新原子核对检测时的版本；冲突时事务回滚，先前的默认项切换也不会生效。
	// 更新时间至少前进一微秒，避免等待行锁或系统时钟回退造成版本重复。
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
    last_text_status=CASE WHEN $28 THEN $29 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_text_status ELSE 'untested' END,
    last_text_message=CASE WHEN $28 THEN $30 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_text_message ELSE '' END,
    last_text_tested_at=CASE WHEN $28 THEN $31 WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_text_tested_at ELSE NULL END,
    last_text_usage=CASE WHEN $28 THEN $32::jsonb WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_text_usage ELSE '{}'::jsonb END,
    last_text_diagnostic=CASE WHEN $28 THEN $33::jsonb WHEN $9::bytea IS NULL AND adapter_type=$4 AND base_url=$5 AND model_name=$6 AND model_version=$7 AND region=$8 THEN last_text_diagnostic ELSE '{}'::jsonb END,
    updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond')
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
  AND ($27::timestamptz IS NULL OR updated_at=$27::timestamptz)
RETURNING `+managedAPIConfigColumns+`
`, tenantID, id, normalized.DisplayName, normalized.AdapterType, normalized.BaseURL,
		normalized.ModelName, normalized.ModelVersion, normalized.Region,
		nullBytes(ciphertext), nullBytes(nonce), credentialHint(normalized.APIKey), normalized.Status, normalized.IsDefault,
		probe.HasProbe, probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.UpdateCapability, probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt,
		probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic, expectedUpdatedAt,
		probe.UpdateText, probe.TextStatus, probe.TextMessage, probe.TextTestedAt, probe.TextUsage, probe.TextDiagnostic)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		if expectedUpdatedAt != nil && errors.Is(err, sql.ErrNoRows) {
			return ManagedAPIConfig{}, ErrManagedProbeStale
		}
		return ManagedAPIConfig{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return ManagedAPIConfig{}, err
	}
	return item, nil
}

func (s *PostgresStore) DeleteManagedAPIConfig(ctx context.Context, tenantID, actorID, id string) error {
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
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM model_role_binding
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND status='active'
) OR EXISTS(
  SELECT 1 FROM model_approval
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND revoked_at IS NULL AND expires_at > now()
) OR EXISTS(
  SELECT 1 FROM model_sandbox_approval
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND revoked_at IS NULL AND expires_at > now()
) OR EXISTS(
  SELECT 1 FROM tenant_model_policy_model
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND enabled
) OR EXISTS(
  SELECT 1 FROM tenant_answer_sheet_model_selection
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND mode='model'
) OR EXISTS(
  SELECT 1 FROM tenant_grading_model_selection
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND mode='model'
) OR EXISTS(
  SELECT 1 FROM tenant_document_model_selection
  WHERE tenant_id=$1::uuid AND managed_model_api_config_id=$2::uuid AND mode='model'
)`, tenantID, id).Scan(&inUse); err != nil {
		return mapStoreError(err)
	}
	if inUse {
		return ErrManagedConfigInUse
	}
	if _, err = tx.ExecContext(ctx, `UPDATE managed_model_api_config
SET deleted_at=now(),deleted_by=$3::uuid,deletion_reason='removed_by_platform_admin',
    status='disabled',is_default=false,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id, actorID); err != nil {
		return mapStoreError(err)
	}
	return tx.Commit()
}

func (s *PostgresStore) GetManagedAPIConnection(ctx context.Context, tenantID, id string) (ManagedAPIConnection, error) {
	if s.credentialCipher == nil {
		return ManagedAPIConnection{}, ErrManagedConfigUnavailable
	}
	// 配置和加密密钥由同一条查询读取，不能把两次查询期间不同版本的内容拼成连接。
	var ciphertext, nonce []byte
	row := s.db.QueryRowContext(ctx, `SELECT `+managedAPIConfigColumns+`,credential_ciphertext,credential_nonce
FROM managed_model_api_config WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id)
	item, err := scanManagedAPIConfig(row, &ciphertext, &nonce)
	if err != nil {
		return ManagedAPIConnection{}, mapStoreError(err)
	}
	apiKey, err := s.credentialCipher.Decrypt(ciphertext, nonce, tenantID, id)
	if err != nil {
		return ManagedAPIConnection{}, err
	}
	return ManagedAPIConnection{Config: item, APIKey: apiKey}, nil
}

// 探测结果必须匹配请求开始时的 updated_at；配置已变更时返回 stale，调用方不能覆盖新凭据的证据。
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
    last_capability_diagnostic=CASE WHEN $9 THEN $15::jsonb ELSE last_capability_diagnostic END,
    last_text_status=CASE WHEN $16 THEN $17 ELSE last_text_status END,
    last_text_message=CASE WHEN $16 THEN $18 ELSE last_text_message END,
    last_text_tested_at=CASE WHEN $16 THEN $19 ELSE last_text_tested_at END,
    last_text_usage=CASE WHEN $16 THEN $20::jsonb ELSE last_text_usage END,
    last_text_diagnostic=CASE WHEN $16 THEN $21::jsonb ELSE last_text_diagnostic END
WHERE tenant_id=$1::uuid AND id=$2::uuid AND updated_at=$3 AND deleted_at IS NULL
RETURNING `+managedAPIConfigColumns+`
`, tenantID, id, expectedUpdatedAt, probe.Status, probe.Message, probe.Latency, probe.TestedAt, probe.Mode,
		probe.UpdateCapability, probe.CapabilityStatus, probe.CapabilityMessage, probe.CapabilityTestedAt,
		probe.CapabilityVersion, probe.CapabilityUsage, probe.CapabilityDiagnostic,
		probe.UpdateText, probe.TextStatus, probe.TextMessage, probe.TextTestedAt, probe.TextUsage, probe.TextDiagnostic)
	item, err := scanManagedAPIConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ManagedAPIConfig{}, ErrManagedProbeStale
		}
		return ManagedAPIConfig{}, mapStoreError(err)
	}
	return item, nil
}

const managedAPIConfigColumns = `id::text,tenant_id::text,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
       true,credential_hint,status,is_default,last_test_status,last_test_message,last_test_latency_ms,last_tested_at,last_successful_tested_at,
       last_probe_mode,last_capability_status,last_capability_message,last_capability_tested_at,last_capability_probe_version,last_capability_usage,
       last_capability_diagnostic,
       last_text_status,last_text_message,last_text_tested_at,last_text_usage,last_text_diagnostic,
       modalities,capability_profile,pricing_policy,data_policy,health_state,
       config_source,provider_registry_version,created_at,updated_at`

const managedAPIConfigSelect = `SELECT ` + managedAPIConfigColumns + ` FROM managed_model_api_config `

func scanManagedAPIConfig(row rowScanner, extra ...any) (ManagedAPIConfig, error) {
	var item ManagedAPIConfig
	var lastTestLatency sql.NullInt64
	var textUsage, textDiagnostic []byte
	var capabilityUsage []byte
	var capabilityDiagnostic []byte
	var modalities, pricingPolicy, dataPolicy []byte
	fields := []any{
		&item.ID, &item.TenantID, &item.ProviderKey, &item.DisplayName, &item.AdapterType,
		&item.BaseURL, &item.ModelName, &item.ModelVersion, &item.Region,
		&item.CredentialConfigured, &item.CredentialHint, &item.Status, &item.IsDefault,
		&item.LastTestStatus, &item.LastTestMessage, &lastTestLatency, &item.LastTestedAt, &item.LastSuccessfulTestedAt,
		&item.LastProbeMode, &item.LastCapabilityStatus, &item.LastCapabilityMessage,
		&item.LastCapabilityTestedAt, &item.LastCapabilityVersion, &capabilityUsage, &capabilityDiagnostic,
		&item.LastTextStatus, &item.LastTextMessage, &item.LastTextTestedAt, &textUsage, &textDiagnostic,
		&modalities, &item.CapabilityProfile, &pricingPolicy, &dataPolicy, &item.HealthState,
		&item.ConfigSource, &item.ProviderRegistryVersion, &item.CreatedAt, &item.UpdatedAt,
	}
	if err := row.Scan(append(fields, extra...)...); err != nil {
		return ManagedAPIConfig{}, err
	}
	if lastTestLatency.Valid {
		item.LastTestLatencyMS = lastTestLatency.Int64
	}
	if len(capabilityUsage) > 0 {
		_ = json.Unmarshal(capabilityUsage, &item.LastCapabilityUsage)
	}
	if len(textUsage) > 0 {
		_ = json.Unmarshal(textUsage, &item.LastTextUsage)
	}
	if len(textDiagnostic) > 0 {
		_ = json.Unmarshal(textDiagnostic, &item.LastTextDiagnostic)
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
	UpdateText           bool
	TextStatus           string
	TextMessage          string
	TextTestedAt         any
	TextUsage            string
	TextDiagnostic       string
	UpdateCapability     bool
	CapabilityStatus     string
	CapabilityMessage    string
	CapabilityTestedAt   any
	CapabilityVersion    string
	CapabilityUsage      string
	CapabilityDiagnostic string
}

// 与内存存储共用证据规则，避免两种存储对 quick、普通回复和临时故障的判定不同。
func managedProbePersistence(result *ManagedAPIProbeResult) managedProbePersistenceValues {
	values := managedProbePersistenceValues{
		Status: "untested", TextStatus: "untested", TextUsage: `{}`, TextDiagnostic: `{}`,
		CapabilityStatus: "untested", CapabilityUsage: `{}`, CapabilityDiagnostic: `{}`,
	}
	if result == nil {
		return values
	}
	item := ManagedAPIConfig{LastTextStatus: "untested", LastCapabilityStatus: "untested"}
	applyManagedProbe(&item, *result, time.Now().UTC())
	values.HasProbe = true
	values.Status, values.Message, values.Latency = item.LastTestStatus, boundedManagedProbeMessage(item.LastTestMessage), item.LastTestLatencyMS
	values.TestedAt, values.Mode = item.LastTestedAt, item.LastProbeMode
	values.UpdateText = item.LastTextTestedAt != nil
	if values.UpdateText {
		values.TextStatus, values.TextMessage, values.TextTestedAt = item.LastTextStatus, item.LastTextMessage, item.LastTextTestedAt
		usage, _ := json.Marshal(item.LastTextUsage)
		diagnostic, _ := json.Marshal(item.LastTextDiagnostic)
		values.TextUsage, values.TextDiagnostic = string(usage), string(diagnostic)
	}
	values.UpdateCapability = item.LastCapabilityTestedAt != nil
	if values.UpdateCapability {
		values.CapabilityStatus, values.CapabilityMessage = item.LastCapabilityStatus, boundedManagedProbeMessage(item.LastCapabilityMessage)
		values.CapabilityTestedAt, values.CapabilityVersion = item.LastCapabilityTestedAt, item.LastCapabilityVersion
		usage, _ := json.Marshal(item.LastCapabilityUsage)
		diagnostic, _ := json.Marshal(item.LastCapabilityDiagnostic)
		values.CapabilityUsage, values.CapabilityDiagnostic = string(usage), string(diagnostic)
	}
	return values
}

func boundedManagedProbeMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 256 {
		message = message[:256]
		// 保留既有字节上限，同时避免截断中文字符后写入 PostgreSQL 失败。
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
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
