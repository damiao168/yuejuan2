package modelgovernance

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ValidateManagedProductionReadiness 检查实际分配给生产角色或被选为学校日常模型的模型。
func (s *PostgresStore) ValidateManagedProductionReadiness(ctx context.Context) error {
	if s.credentialCipher == nil {
		return ErrManagedConfigUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT config.tenant_id::text, config.id::text, config.base_url,
       config.model_version, config.region, config.status, config.deleted_at IS NULL,
       config.is_default, config.last_text_status,
       EXISTS (
          SELECT 1 FROM model_role_binding required_binding
          WHERE required_binding.tenant_id=config.tenant_id
            AND required_binding.managed_model_api_config_id=config.id
            AND required_binding.status='active'
       ), config.last_capability_status, config.last_capability_probe_version,
       config.credential_ciphertext, config.credential_nonce
FROM managed_model_api_config config
WHERE config.is_default OR EXISTS (
    SELECT 1 FROM model_role_binding binding
    WHERE binding.tenant_id=config.tenant_id
      AND binding.managed_model_api_config_id=config.id AND binding.status='active'
  )`)
	if err != nil {
		return err
	}
	// 候选校验提前失败时也要归还连接，避免启动重试耗尽连接池。
	defer rows.Close()
	for rows.Next() {
		var tenantID, id, endpoint, version, region, status, textStatus, capability, probeVersion string
		var present, isDefault, structuredRequired bool
		var ciphertext, nonce []byte
		if err := rows.Scan(&tenantID, &id, &endpoint, &version, &region, &status, &present,
			&isDefault, &textStatus, &structuredRequired, &capability, &probeVersion, &ciphertext, &nonce); err != nil {
			return err
		}
		key, decryptErr := s.credentialCipher.Decrypt(ciphertext, nonce, tenantID, id)
		if err := validateManagedProductionCandidate(managedProductionCandidate{
			ID: id, Endpoint: endpoint, ModelVersion: version, Region: region,
			Status: status, Present: present, TextStatus: textStatus,
			IsDefault: isDefault, StructuredRequired: structuredRequired,
			CapabilityStatus: capability, CapabilityVersion: probeVersion,
		}, key, decryptErr); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.validateApprovedPanelScopes(ctx)
}

type managedProductionCandidate struct {
	ID                 string
	Endpoint           string
	ModelVersion       string
	Region             string
	Status             string
	Present            bool
	IsDefault          bool
	TextStatus         string
	StructuredRequired bool
	CapabilityStatus   string
	CapabilityVersion  string
}

// 默认模型只需文本证据；评分角色必须有当前结构化证据。同一模型承担两种用途时，两项都要满足。
// 最近一次 /models 或临时故障不覆盖独立生成证据，生产门槛不能依赖 last_test_status。
func validateManagedProductionCandidate(item managedProductionCandidate, apiKey string, decryptErr error) error {
	parsed, parseErr := url.Parse(item.Endpoint)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		item.Status != "active" || !item.Present ||
		strings.TrimSpace(item.ModelVersion) == "" || strings.TrimSpace(item.Region) == "" ||
		(item.IsDefault && item.TextStatus != "success") ||
		(item.StructuredRequired && (item.CapabilityStatus != "success" || item.CapabilityVersion != ManagedCapabilityProbeVersion)) ||
		decryptErr != nil || strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("%w: managed model %s is not ready", ErrProductionUnsafe, item.ID)
	}
	return nil
}

type approvedPanelScope struct {
	tenant, stage, subject, archetype string
	ready                             bool
}

// 只有已批准作用域才是生产路由；部分配置的影子作用域仍可编辑，不妨碍服务启动。
func (s *PostgresStore) validateApprovedPanelScopes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT policy.tenant_id::text, policy.education_stage, policy.subject_code,
       policy.archetype_code,
       COALESCE(evaluation.status='completed'
         AND policy.readiness_report->>'ready'='true', false)
FROM subjective_panel_policy policy
LEFT JOIN grading_evaluation_run evaluation
  ON evaluation.tenant_id=policy.tenant_id AND evaluation.id=policy.evaluation_run_id
WHERE policy.status='approved'`)
	if err != nil {
		return err
	}
	scopes := []approvedPanelScope{}
	for rows.Next() {
		var item approvedPanelScope
		if err := rows.Scan(&item.tenant, &item.stage, &item.subject, &item.archetype, &item.ready); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range scopes {
		if err := validateApprovedPanelScope(ctx, s, item); err != nil {
			return err
		}
	}
	return nil
}

func validateApprovedPanelScope(ctx context.Context, store ModelRoleBindingStore, item approvedPanelScope) error {
	if !item.ready {
		return fmt.Errorf("%w: approved panel policy for %s/%s/%s/%s lacks completed evidence",
			ErrProductionUnsafe, item.tenant, item.stage, item.subject, item.archetype)
	}
	if _, err := ResolvePanelRoleBindings(ctx, store, item.tenant, item.stage, item.subject, item.archetype); err != nil {
		return fmt.Errorf("%w: approved panel policy for %s/%s/%s/%s lacks a valid A/B/C binding: %v",
			ErrProductionUnsafe, item.tenant, item.stage, item.subject, item.archetype, err)
	}
	return nil
}
