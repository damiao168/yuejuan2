package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
)

func recordFailedImagePanelCallsInTx(ctx context.Context, tx *sql.Tx, tenantID string, run GradingRun, public any) error {
	var contextRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT image_context_json FROM subjective_grading_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, run.ID).Scan(&contextRaw); err != nil {
		return err
	}
	var value Context
	if json.Unmarshal(contextRaw, &value) != nil || value.AIPlan == nil {
		return ErrInvalidModelOutput
	}
	value.AIPlan = imagePlanForQuestion(value.AIPlan, value.Question.QuestionType)
	raw, err := json.Marshal(public)
	if err != nil {
		return err
	}
	var execution ImagePanelExecution
	if json.Unmarshal(raw, &execution) != nil || execution.ConfiguredMode != value.AIPlan.EffectiveMode {
		return ErrInvalidModelOutput
	}
	for _, role := range execution.RoleResults {
		model, ok := imagePlanRoleModel(value.AIPlan, role.Role)
		if !ok || model != role.Model || role.Status != "failed" {
			return ErrInvalidModelOutput
		}
		if role.Output != nil && (role.Output.RequestID != run.RequestID+":"+role.Role || role.Output.Telemetry.Provider != model.ProviderKey || role.Output.Telemetry.Adapter != model.AdapterType || role.Output.Telemetry.Deployment != model.ConfigRevision) {
			return ErrInvalidModelOutput
		}
	}
	return recordImagePanelCallsInTx(ctx, tx, tenantID, Grade{QuestionID: run.QuestionID, AnswerSegmentID: run.AnswerSegmentID, RubricVersion: run.RubricVersion, AdapterRequestID: run.RequestID, RawOutput: map[string]any{"panel_execution": public}})
}

// recordImagePanelCallsInTx 与父建议同时提交每个实际请求，不能把多家用量都记到主评 A。
// 缺失用量保留在角色 diagnostics，不向旧账本补造零 token；明确未发请求的配置失败也不算调用。
func recordImagePanelCallsInTx(ctx context.Context, tx *sql.Tx, tenantID string, grade Grade) error {
	raw, err := json.Marshal(grade.RawOutput["panel_execution"])
	if err != nil {
		return err
	}
	var execution ImagePanelExecution
	if err := json.Unmarshal(raw, &execution); err != nil {
		return err
	}
	for _, role := range execution.RoleResults {
		if role.Output == nil || role.Output.Telemetry.Attempts == 0 {
			continue
		}
		output := role.Output
		if d := output.Telemetry.ExecutionDiagnostics; d != nil && d.UsageStatus != "reported" {
			continue
		}
		usage, telemetry := output.Telemetry.Usage, output.Telemetry
		status := "succeeded"
		if role.Status != "succeeded" {
			status = "failed"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_usage_event(tenant_id,school_id,request_id,feature,agent_role,provider_key,model_name,request_count,input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,total_tokens,status,metadata)
SELECT $1::uuid,exam.school_id,$2,'subjective_grading',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
jsonb_build_object('deployment_key',$13::text,'question_id',$14::uuid,'answer_segment_id',$15::uuid,'parent_request_id',$16::text)
FROM answer_segment segment JOIN submission ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id JOIN exam ON exam.tenant_id=submission.tenant_id AND exam.id=submission.exam_id WHERE segment.tenant_id=$1::uuid AND segment.id=$15::uuid
ON CONFLICT(tenant_id,request_id,feature) DO NOTHING`, tenantID, output.RequestID, role.Role, role.Model.ProviderKey, role.Model.ModelName,
			telemetry.Attempts, usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens, usage.ReasoningTokens, usage.TotalTokens,
			status, telemetry.Deployment, grade.QuestionID, grade.AnswerSegmentID, grade.AdapterRequestID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_call_fact(tenant_id,request_id,answer_segment_id,question_id,provider_key,deployment_key,adapter_type,model_version,managed_model_api_config_id,model_name,prompt_version,rubric_version,capability_profile,deployment_region,route_mode,route_reason,status,attempts,latency_ms,input_units,output_units,error_code)
VALUES($1::uuid,$2,$3::uuid,$4::uuid,$5,$6,$7,$8,$9::uuid,$10,$11,$12,$13,$14,'local_only','frozen exam image grading role',$15,$16,$17,$18,$19,$20)
ON CONFLICT(tenant_id,request_id) DO NOTHING`, tenantID, output.RequestID, grade.AnswerSegmentID, grade.QuestionID, role.Model.ProviderKey,
			telemetry.Deployment, role.Model.AdapterType, role.Model.ModelVersion, role.Model.ModelConfigID, role.Model.ModelName,
			output.PromptVersion, grade.RubricVersion, output.CapabilityProfile, telemetry.Region, status, telemetry.Attempts,
			telemetry.ElapsedMS, usage.InputTokens, usage.OutputTokens, role.ErrorCode); err != nil {
			return err
		}
	}
	return nil
}
