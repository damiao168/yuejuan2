package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type gradingAIPlanQueryer interface {
	scoringReadinessQueryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func defaultGradingAIPlan() GradingAIPlan {
	return GradingAIPlan{Version: 1, Revision: "absent", Roles: map[string]GradingAIRoleSelection{},
		DecisionConfig: GradingAIDecisionConfig{AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"}}
}

func loadGradingAIPlan(ctx context.Context, q gradingAIPlanQueryer, tenantID, examID string) (GradingAIPlanDetail, error) {
	plan := defaultGradingAIPlan()
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL)`, tenantID, examID).Scan(&exists); err != nil {
		return GradingAIPlanDetail{}, err
	}
	if !exists {
		return GradingAIPlanDetail{}, ErrNotFound
	}
	var raw []byte
	var confirmedAt time.Time
	err := q.QueryRowContext(ctx, `SELECT revision::text,configuration,confirmed_by::text,confirmed_at FROM exam_grading_ai_plan WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, examID).Scan(&plan.Revision, &raw, &plan.ConfirmedBy, &confirmedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GradingAIPlanDetail{}, err
	}
	if err == nil {
		var saved GradingAIPlanInput
		if err := json.Unmarshal(raw, &saved); err != nil {
			return GradingAIPlanDetail{}, err
		}
		plan.FallbackModelConfigID, plan.Roles, plan.DecisionConfig = saved.FallbackModelConfigID, saved.Roles, saved.DecisionConfig
		plan.ConfirmedAt = &confirmedAt
	}
	// 学校默认项只给尚未确认的考试提供起点；确认后不随平台换选偷偷改写本场备用模型。
	selectionRevision, defaultModel := "absent", ""
	err = q.QueryRowContext(ctx, `SELECT revision::text,COALESCE(managed_model_api_config_id::text,'') FROM tenant_grading_model_selection WHERE tenant_id=$1::uuid AND mode='model'`, tenantID).Scan(&selectionRevision, &defaultModel)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GradingAIPlanDetail{}, err
	}
	if plan.Revision == "absent" {
		plan.FallbackModelConfigID = defaultModel
	}
	models, err := loadGradingAIModels(ctx, q, tenantID, selectionRevision)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	return GradingAIPlanDetail{Plan: resolveGradingAIPlan(plan, models), AvailableModels: models}, nil
}

func loadGradingAIModels(ctx context.Context, q gradingAIPlanQueryer, tenantID, bindingRevision string) ([]GradingAIModel, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text,display_name,provider_key,adapter_type,base_url,model_name,model_version,updated_at FROM managed_model_api_config WHERE tenant_id=$1::uuid AND status='active' AND deleted_at IS NULL ORDER BY display_name,id FOR SHARE`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := []GradingAIModel{}
	for rows.Next() {
		var model GradingAIModel
		var baseURL string
		var updatedAt time.Time
		if err := rows.Scan(&model.ModelConfigID, &model.DisplayName, &model.ProviderKey, &model.AdapterType, &baseURL, &model.ModelName, &model.ModelVersion, &updatedAt); err != nil {
			return nil, err
		}
		model.BindingRevision = bindingRevision
		model.ConfigRevision = GradingModelConfigRevision(tenantID, bindingRevision, model.ModelConfigID, model.AdapterType, baseURL, model.ModelName, model.ModelVersion, updatedAt)
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *PostgresStore) GetGradingAIPlan(ctx context.Context, tenantID, examID string) (GradingAIPlanDetail, error) {
	return loadGradingAIPlan(ctx, s.db, tenantID, examID)
}

func (s *PostgresStore) SaveGradingAIPlan(ctx context.Context, tenantID, examID, actorID string, input GradingAIPlanInput) (GradingAIPlanDetail, error) {
	input, err := normalizeGradingAIPlanInput(input)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	defer tx.Rollback()
	// 与启动运行共用考试锁：确认的方案要么整份进入本轮快照，要么留给下一轮。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, examID+":scoring-run"); err != nil {
		return GradingAIPlanDetail{}, err
	}
	current, err := loadGradingAIPlan(ctx, tx, tenantID, examID)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	if current.Plan.Revision != input.ExpectedRevision {
		return GradingAIPlanDetail{}, ErrGradingAIPlanChanged
	}
	available := map[string]bool{}
	for _, model := range current.AvailableModels {
		available[model.ModelConfigID] = true
	}
	selected := []string{input.FallbackModelConfigID}
	for _, role := range input.Roles {
		selected = append(selected, role.ModelConfigID)
	}
	for _, id := range selected {
		if id != "" && !available[id] {
			return GradingAIPlanDetail{}, ErrInvalidInput
		}
	}
	resolved := resolveGradingAIPlan(GradingAIPlan{FallbackModelConfigID: input.FallbackModelConfigID, Roles: input.Roles, DecisionConfig: input.DecisionConfig}, current.AvailableModels)
	if resolved.EffectiveMode == "unconfigured" {
		return GradingAIPlanDetail{}, ErrGradingAIModelRequired
	}
	if err := gradingAIPlanThresholdRequirement(resolved); err != nil {
		return GradingAIPlanDetail{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO exam_grading_ai_plan(tenant_id,exam_id,revision,configuration,confirmed_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::jsonb,$5::uuid) ON CONFLICT(tenant_id,exam_id) DO UPDATE SET revision=EXCLUDED.revision,configuration=EXCLUDED.configuration,confirmed_by=EXCLUDED.confirmed_by,confirmed_at=clock_timestamp()`, tenantID, examID, uuid.NewString(), raw, actorID); err != nil {
		return GradingAIPlanDetail{}, err
	}
	detail, err := loadGradingAIPlan(ctx, tx, tenantID, examID)
	if err != nil {
		return GradingAIPlanDetail{}, err
	}
	return detail, tx.Commit()
}
