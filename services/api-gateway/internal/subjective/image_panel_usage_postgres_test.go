package subjective

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"github.com/google/uuid"
)

func TestPostgresImagePanelRoleUsageRollsBackAndReplaysWithParent(t *testing.T) {
	_, db, tenant := imageSettlementPostgresDB(t)
	store, runtime, actor, prepared, input := seedRemediationCompletion(t, db, tenant)
	ctx := t.Context()
	prepared.Policy.PromptVersion, prepared.Run.PromptVersion = ImageComparisonPromptVersion, ImageComparisonPromptVersion
	if _, err := db.Exec(`UPDATE subjective_grading_run SET prompt_version=$2 WHERE id=$1::uuid`, prepared.Run.ID, prepared.Policy.PromptVersion); err != nil {
		t.Fatal(err)
	}
	value := prepared.Context
	observation := imageTestObservation(value.Question.ID, "synthetic-crop")
	observation.ModelBinding = "synthetic-source-binding"
	value.InputVersionID, value.CropSHA256, value.Observation = observation.InputVersionID, observation.CropSHA256, &observation
	value.AIPlan = &grading.GradingAIPlan{Version: 1, EffectiveMode: "dual_conditional_arbiter", ResolvedRoles: map[string]grading.GradingAIModel{}, DecisionConfig: grading.GradingAIDecisionConfig{PrimaryAllowedDiffPoints: floatPtr(.5), TeacherAllowedDiffPoints: floatPtr(5), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"}}
	execution := &ImagePanelExecution{ConfiguredMode: value.AIPlan.EffectiveMode}
	models := []grading.GradingAIModel{}
	for _, role := range []string{"primary_a", "primary_b", "arbiter"} {
		version := role + "-v1"
		if role == "primary_a" {
			version = prepared.Run.ModelVersion
		}
		model := grading.GradingAIModel{ModelConfigID: uuid.NewString(), ModelName: "model-" + role, ModelVersion: version, AdapterType: "openai_compatible", ProviderKey: "custom", ConfigRevision: "revision-" + role}
		value.AIPlan.ResolvedRoles[role] = model
		models = append(models, model)
		judgment := ImageCriterionJudgment{CriterionID: value.Rubric.Points[0].ID, Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "合成学生步骤支持采分点"}
		if role == "primary_a" {
			judgment.Status, judgment.EvidenceIDs = "contradicted", nil
		}
		output := AdapterOutput{SchemaVersion: ImageProtocolVersion, RequestID: prepared.Run.RequestID + ":" + role, ModelVersion: model.ModelVersion, PromptVersion: ImageComparisonPromptVersion, RubricVersion: value.Rubric.Version, DeliveryMode: "teacher_suggestion", CapabilityProfile: "fixture", NeedsHumanReview: true, StudentFeedback: "合成反馈", TeacherNote: "合成说明", Observation: &observation, ImageJudgments: []ImageCriterionJudgment{judgment}, RawOutput: map[string]any{}, Telemetry: AdapterTelemetry{Adapter: model.AdapterType, Provider: model.ProviderKey, Deployment: model.ConfigRevision, Region: "managed", Attempts: 1, Usage: ModelTokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}}
		execution.RoleResults = append(execution.RoleResults, ImageRoleResult{Role: role, Status: "succeeded", Model: model, Output: &output})
	}
	insertModel := func(model grading.GradingAIModel) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO managed_model_api_config(id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,credential_ciphertext,credential_nonce) VALUES($1::uuid,$2::uuid,$3,$4,$5,'https://synthetic.invalid/v1',$4,$6,decode(repeat('ab',16),'hex'),decode(repeat('cd',12),'hex'))`, model.ModelConfigID, tenant, model.ProviderKey, model.ModelName, model.AdapterType, model.ModelVersion); err != nil {
			t.Fatal(err)
		}
	}
	insertModel(models[0])
	insertModel(models[1])
	value.modelConfigID, value.modelName = models[0].ModelConfigID, models[0].ModelName
	prepared.Context = value
	output := AdapterOutput{SchemaVersion: ImageProtocolVersion, RequestID: prepared.Run.RequestID, ModelVersion: prepared.Policy.ModelVersion, PromptVersion: prepared.Policy.PromptVersion, PanelExecution: execution}
	if err := settleImagePanelOutput(value, prepared.Policy, &output); err != nil {
		t.Fatal(err)
	}
	input.Output = output
	service := NewWorkerCompletionService(store, runtime)
	// 第三个调用身份不存在，让前两个分录写入后失败；父成绩和全部角色分录必须一起回滚。
	if _, err := service.Complete(ctx, tenant, actor, prepared, input, output); err == nil {
		t.Fatal("missing third managed identity did not reject completion")
	}
	for _, table := range []string{"ai_grade", "model_usage_event", "model_call_fact"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE tenant_id=$1::uuid", tenant).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s persisted count=%d err=%v", table, count, err)
		}
	}
	insertModel(models[2])
	accepted, err := service.Complete(ctx, tenant, actor, prepared, input, output)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Grade.SuggestedScore != imageCeilFinal(.75*value.Rubric.Points[0].Score, value.Question.Score) {
		t.Fatal("parent did not persist weighted score")
	}
	for _, table := range []string{"model_usage_event", "model_call_fact"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE tenant_id=$1::uuid", tenant).Scan(&count); err != nil || count != 3 {
			t.Fatalf("%s role count=%d err=%v", table, count, err)
		}
	}
	if _, replayed, err := service.Replay(ctx, tenant, actor, prepared.Run.ID, input); err != nil || !replayed {
		t.Fatalf("replay failed: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM model_usage_event WHERE tenant_id=$1::uuid`, tenant).Scan(&count); err != nil || count != 3 {
		t.Fatalf("replay duplicated role usage %d %v", count, err)
	}
}
