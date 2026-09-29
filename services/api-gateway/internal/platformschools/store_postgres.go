package platformschools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// 运营汇总来自多个租户表；查询必须保留租户边界，避免把学校数据串到别的租户。
func (s *PostgresStore) ListSummaries(ctx context.Context, windowDays int) ([]PlatformSchoolSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  t.id::text,s.id::text,t.name,t.code,t.status,t.created_at,metrics.last_activity_at,metrics.consecutive_ai_failures,
  COALESCE(metrics.admin_id::text,''),metrics.admin_display_name,metrics.admin_username,metrics.admin_count,metrics.admin_last_login_at,
  metrics.account_count,metrics.active_account_count,metrics.admin_count,metrics.teacher_count,metrics.grader_count,metrics.student_count,metrics.class_count,metrics.exam_count,
  usage.input_tokens,usage.output_tokens,usage.cached_input_tokens,usage.reasoning_tokens,usage.total_tokens,usage.request_count,usage.arbitration_requests,usage.estimated_cost_microusd,
  COALESCE(metrics.model_config_id::text,''),metrics.model_status,metrics.model_display_name,metrics.model_provider_key,metrics.model_name,metrics.model_credential_hint,
  metrics.model_test_status,metrics.model_test_message,metrics.model_capability_status,metrics.model_capability_version,metrics.model_capability_message,
  metrics.model_test_latency_ms,metrics.model_tested_at,metrics.model_capability_tested_at
FROM tenant t
JOIN platform_school_metrics metrics ON metrics.tenant_id=t.id
JOIN school s ON s.tenant_id=t.id AND s.id=metrics.school_id AND s.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT
    COALESCE(sum(d.input_tokens),0)::bigint input_tokens,
    COALESCE(sum(d.output_tokens),0)::bigint output_tokens,
    COALESCE(sum(d.cached_input_tokens),0)::bigint cached_input_tokens,
    COALESCE(sum(d.reasoning_tokens),0)::bigint reasoning_tokens,
    COALESCE(sum(d.total_tokens),0)::bigint total_tokens,
    COALESCE(sum(d.request_count),0)::bigint request_count,
    COALESCE(sum(d.request_count) FILTER (WHERE d.agent_role='arbiter'),0)::bigint arbitration_requests,
    COALESCE(sum(d.estimated_cost_microusd),0)::bigint estimated_cost_microusd
  FROM model_usage_daily d
  WHERE d.tenant_id=t.id AND d.usage_date >= (now() AT TIME ZONE 'UTC')::date-($1::int-1)
) usage ON true
WHERE t.deleted_at IS NULL AND t.code<>'platform'
ORDER BY t.created_at DESC,t.id`, windowDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlatformSchoolSummary{}
	for rows.Next() {
		var item PlatformSchoolSummary
		var adminLast, lastActivity, lastTested, lastCapability sql.NullTime
		if err := rows.Scan(
			&item.TenantID, &item.SchoolID, &item.Name, &item.Code, &item.Status, &item.CreatedAt, &lastActivity, &item.ConsecutiveAIFailures,
			&item.Administrator.ID, &item.Administrator.DisplayName, &item.Administrator.Username, &item.Administrator.AdminCount, &adminLast,
			&item.Members.Accounts, &item.Members.ActiveAccounts, &item.Members.Administrators, &item.Members.Teachers, &item.Members.Graders, &item.Members.Students, &item.Members.Classes, &item.ExamCount,
			&item.Usage.InputTokens, &item.Usage.OutputTokens, &item.Usage.CachedInputTokens, &item.Usage.ReasoningTokens, &item.Usage.TotalTokens, &item.Usage.Requests, &item.Usage.ArbitrationRequests, &item.Usage.EstimatedCostMicroUSD,
			&item.ModelHealth.ConfigID, &item.ModelHealth.ConfigStatus, &item.ModelHealth.DisplayName, &item.ModelHealth.ProviderKey, &item.ModelHealth.ModelName, &item.ModelHealth.CredentialHint,
			&item.ModelHealth.ConnectionStatus, &item.ModelHealth.ConnectionMessage, &item.ModelHealth.CapabilityStatus, &item.ModelHealth.CapabilityVersion, &item.ModelHealth.CapabilityMessage,
			&item.ModelHealth.LatencyMS, &lastTested, &lastCapability,
		); err != nil {
			return nil, err
		}
		item.LastActivityAt = nullableTime(lastActivity)
		item.Administrator.LastLoginAt = nullableTime(adminLast)
		item.ModelHealth.LastTestedAt = nullableTime(lastTested)
		item.ModelHealth.LastCapabilityTestedAt = nullableTime(lastCapability)
		item.ModelHealth.Status = modelHealthStatus(item.ModelHealth)
		item.Usage.WindowDays = windowDays
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListMembers(ctx context.Context, tenantID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT u.id::text,u.username,u.display_name,
  CASE WHEN COALESCE(u.phone,'')='' THEN '' WHEN char_length(u.phone)<=7 THEN repeat('*',char_length(u.phone)) ELSE left(u.phone,3)||repeat('*',char_length(u.phone)-7)||right(u.phone,4) END,
  COALESCE(u.employee_no,''),u.status,COALESCE(u.school_id::text,''),u.created_at,u.activated_at,u.last_login_at,
  COALESCE((SELECT jsonb_agg(DISTINCT r.code ORDER BY r.code) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.deleted_at IS NULL),'[]'::jsonb)
FROM app_user u
WHERE u.tenant_id=$1::uuid AND u.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1
    FROM user_role hidden_ur
    JOIN role hidden_role
      ON hidden_role.tenant_id=hidden_ur.tenant_id
     AND hidden_role.id=hidden_ur.role_id
     AND hidden_role.deleted_at IS NULL
    WHERE hidden_ur.tenant_id=u.tenant_id
      AND hidden_ur.user_id=u.id
      AND hidden_ur.deleted_at IS NULL
      AND (hidden_role.code='student' OR right(hidden_role.code,7)='_worker')
  )
ORDER BY u.created_at,u.id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Member{}
	for rows.Next() {
		var item Member
		var activated, lastLogin sql.NullTime
		var roles []byte
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.PhoneMasked, &item.EmployeeNo, &item.Status, &item.SchoolID, &item.CreatedAt, &activated, &lastLogin, &roles); err != nil {
			return nil, err
		}
		item.ActivatedAt = nullableTime(activated)
		item.LastLoginAt = nullableTime(lastLogin)
		_ = json.Unmarshal(roles, &item.Roles)
		if item.Roles == nil {
			item.Roles = []string{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetUsage(ctx context.Context, tenantID string, usageRange UsageRange) (UsageResult, error) {
	result := UsageResult{Range: usageRange, Trend: []UsageTrendPoint{}, ByFeature: []UsageBreakdown{}, ByModel: []UsageBreakdown{}, StartDate: usageRange.Start.Format("2006-01-02"), EndDate: usageRange.End.Format("2006-01-02")}
	result.Summary.WindowDays = usageRange.Days
	if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cached_input_tokens),0),COALESCE(sum(reasoning_tokens),0),COALESCE(sum(total_tokens),0),COALESCE(sum(request_count),0),COALESCE(sum(request_count) FILTER (WHERE agent_role='arbiter'),0),COALESCE(sum(estimated_cost_microusd),0)
FROM model_usage_daily WHERE tenant_id=$1::uuid AND usage_date BETWEEN $2::date AND $3::date`, tenantID, usageRange.Start, usageRange.End).Scan(
		&result.Summary.InputTokens, &result.Summary.OutputTokens, &result.Summary.CachedInputTokens, &result.Summary.ReasoningTokens, &result.Summary.TotalTokens, &result.Summary.Requests, &result.Summary.ArbitrationRequests, &result.Summary.EstimatedCostMicroUSD); err != nil {
		return UsageResult{}, err
	}
	trendRows, err := s.db.QueryContext(ctx, `
SELECT usage_date::text,sum(total_tokens),sum(input_tokens),sum(output_tokens),sum(request_count)
FROM model_usage_daily WHERE tenant_id=$1::uuid AND usage_date BETWEEN $2::date AND $3::date
GROUP BY usage_date ORDER BY usage_date`, tenantID, usageRange.Start, usageRange.End)
	if err != nil {
		return UsageResult{}, err
	}
	for trendRows.Next() {
		var item UsageTrendPoint
		if err = trendRows.Scan(&item.Date, &item.TotalTokens, &item.InputTokens, &item.OutputTokens, &item.Requests); err != nil {
			trendRows.Close()
			return UsageResult{}, err
		}
		result.Trend = append(result.Trend, item)
	}
	if err = trendRows.Close(); err != nil {
		return UsageResult{}, err
	}
	result.ByFeature, err = s.usageBreakdown(ctx, tenantID, usageRange, "feature")
	if err != nil {
		return UsageResult{}, err
	}
	result.ByModel, err = s.usageBreakdown(ctx, tenantID, usageRange, "model_name")
	if err != nil {
		return UsageResult{}, err
	}
	applyShares(result.ByFeature, result.Summary.TotalTokens)
	applyShares(result.ByModel, result.Summary.TotalTokens)
	return result, nil
}

func (s *PostgresStore) usageBreakdown(ctx context.Context, tenantID string, usageRange UsageRange, dimension string) ([]UsageBreakdown, error) {
	if dimension != "feature" && dimension != "model_name" {
		return nil, fmt.Errorf("unsupported usage dimension")
	}
	query := fmt.Sprintf(`SELECT %s,sum(total_tokens),sum(request_count),sum(estimated_cost_microusd) FROM model_usage_daily WHERE tenant_id=$1::uuid AND usage_date BETWEEN $2::date AND $3::date GROUP BY %s ORDER BY sum(total_tokens) DESC,%s`, dimension, dimension, dimension)
	rows, err := s.db.QueryContext(ctx, query, tenantID, usageRange.Start, usageRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UsageBreakdown{}
	for rows.Next() {
		var item UsageBreakdown
		if err = rows.Scan(&item.Key, &item.TotalTokens, &item.Requests, &item.EstimatedCostMicroUSD); err != nil {
			return nil, err
		}
		item.Label = usageLabel(dimension, item.Key)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetModelHealth(ctx context.Context, tenantID string) (ModelHealthResult, error) {
	result := ModelHealthResult{Roles: []ModelRole{}}
	var lastTested, lastCapability sql.NullTime
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(id::text,''),COALESCE(status,''),COALESCE(display_name,''),COALESCE(provider_key,''),COALESCE(model_name,''),COALESCE(credential_hint,''),
       COALESCE(last_test_status,''),COALESCE(last_test_message,''),COALESCE(last_capability_status,''),COALESCE(last_capability_probe_version,''),COALESCE(last_capability_message,''),COALESCE(last_test_latency_ms,0),last_tested_at,last_capability_tested_at
FROM managed_model_api_config WHERE tenant_id=$1::uuid AND deleted_at IS NULL AND is_default ORDER BY updated_at DESC LIMIT 1`, tenantID).Scan(
		&result.DefaultModel.ConfigID, &result.DefaultModel.ConfigStatus, &result.DefaultModel.DisplayName, &result.DefaultModel.ProviderKey, &result.DefaultModel.ModelName, &result.DefaultModel.CredentialHint,
		&result.DefaultModel.ConnectionStatus, &result.DefaultModel.ConnectionMessage, &result.DefaultModel.CapabilityStatus, &result.DefaultModel.CapabilityVersion, &result.DefaultModel.CapabilityMessage, &result.DefaultModel.LatencyMS, &lastTested, &lastCapability)
	if err != nil && !errorsIsNoRows(err) {
		return ModelHealthResult{}, err
	}
	if err == nil {
		result.DefaultModel.LastTestedAt = nullableTime(lastTested)
		result.DefaultModel.LastCapabilityTestedAt = nullableTime(lastCapability)
	}
	result.DefaultModel.Status = modelHealthStatus(result.DefaultModel)
	rows, queryErr := s.db.QueryContext(ctx, `SELECT agent_role,model_name,provider_key,health_status FROM platform_school_model_role WHERE tenant_id=$1::uuid ORDER BY CASE agent_role WHEN 'primary_a' THEN 1 WHEN 'primary_b' THEN 2 ELSE 3 END`, tenantID)
	if queryErr != nil {
		return ModelHealthResult{}, queryErr
	}
	defer rows.Close()
	for rows.Next() {
		var item ModelRole
		if err = rows.Scan(&item.AgentRole, &item.ModelName, &item.ProviderKey, &item.Status); err != nil {
			return ModelHealthResult{}, err
		}
		result.Roles = append(result.Roles, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) GetActivity(ctx context.Context, tenantID string, limit int) (ActivityResult, error) {
	result := ActivityResult{Activities: []ActivityItem{}}
	rows, err := s.db.QueryContext(ctx, `
SELECT ae.id::text,ae.event_type,ae.severity,ae.title,ae.summary,COALESCE(u.display_name,''),ae.happened_at
FROM activity_event ae LEFT JOIN app_user u ON u.tenant_id=ae.tenant_id AND u.id=ae.actor_id
WHERE ae.tenant_id=$1::uuid ORDER BY ae.happened_at DESC,ae.id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return ActivityResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ActivityItem
		if err = rows.Scan(&item.ID, &item.EventType, &item.Severity, &item.Title, &item.Summary, &item.ActorName, &item.HappenedAt); err != nil {
			return ActivityResult{}, err
		}
		result.Activities = append(result.Activities, item)
	}
	if err = rows.Err(); err != nil {
		return ActivityResult{}, err
	}
	var lastLogin sql.NullTime
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(metrics.active_admin_count,0),COALESCE(metrics.mfa_enabled_admin_count,0),COALESCE(metrics.active_session_count,0),
  metrics.last_login_at
FROM platform_school_metrics metrics WHERE metrics.tenant_id=$1::uuid`, tenantID).Scan(&result.Security.ActiveAdmins, &result.Security.MFAEnabled, &result.Security.ActiveSessions, &lastLogin)
	if err != nil {
		return ActivityResult{}, err
	}
	result.Security.LastLoginAt = nullableTime(lastLogin)
	return result, nil
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time
	return &v
}
func modelHealthStatus(model ModelHealth) string {
	if model.ConfigID == "" {
		return ModelUnconfigured
	}
	if model.ConfigStatus == "active" && model.ConnectionStatus == "success" && model.CapabilityStatus == "success" && model.CapabilityVersion == "structured-json-v3" {
		return ModelHealthy
	}
	return ModelWarning
}
func applyShares(items []UsageBreakdown, total int64) {
	if total <= 0 {
		return
	}
	for index := range items {
		items[index].Share = float64(items[index].TotalTokens) / float64(total)
	}
}
func usageLabel(dimension, key string) string {
	if dimension == "model_name" {
		return key
	}
	labels := map[string]string{"paper_import": "试卷识别", "school_ai_chat": "AI 助手", "subjective_grading": "主观题阅卷", "model_probe": "模型检测", "model_evaluation": "模型评估", "other": "其他"}
	if label := labels[key]; label != "" {
		return label
	}
	return key
}
func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }

var _ Store = (*PostgresStore)(nil)
