package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 复用随机隔离库和正式完成事务，先复现缺身份导致成绩、用量、任务一起回滚，
// 再从服务端恢复身份并完成同一租约；不调用供应商，也不写业务库。
func TestPostgresImageSettlementManagedIdentityRollbackAndReplay(t *testing.T) {
	owner, db, tenantID := imageSettlementPostgresDB(t)
	store, runtime, actorID, prepared, input := seedRemediationCompletion(t, db, tenantID)
	ctx := t.Context()
	model := settlementIdentityModel()
	model.ConfigID, model.ModelVersion = uuid.NewString(), prepared.Run.ModelVersion
	if _, err := owner.ExecContext(ctx, `INSERT INTO managed_model_api_config
  (id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,credential_ciphertext,credential_nonce)
VALUES($1::uuid,$2::uuid,$3,'synthetic image settlement',$4,$5,$6,$7,decode(repeat('ab',16),'hex'),decode(repeat('cd',12),'hex'))`,
		model.ConfigID, tenantID, model.ProviderKey, model.AdapterType, model.BaseURL, model.ModelName, model.ModelVersion); err != nil {
		t.Fatal(err)
	}
	observation := imageTestObservation(prepared.Context.Question.ID, "synthetic-crop")
	observation.ModelBinding = imageModelBinding(model)
	prepared.Context.InputVersionID, prepared.Context.CropSHA256, prepared.Context.Observation = observation.InputVersionID, observation.CropSHA256, &observation
	output := input.Output
	output.Mock, output.SchemaVersion, output.CapabilityProfile = false, ImageProtocolVersion, "image-comparison"
	output.DeliveryMode, output.StudentFeedback, output.TeacherNote = "teacher_suggestion", "合成反馈", "合成复核说明"
	output.Telemetry = AdapterTelemetry{Adapter: model.AdapterType, Provider: model.ProviderKey, Deployment: model.ConfigRevision, Region: "managed", Attempts: 1,
		Usage: ModelTokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
	output.ImageJudgments = []ImageCriterionJudgment{{CriterionID: "p1", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "合成证据支持采分点"}}
	input.Output = output
	if err := NewGradeSettlementPipeline(nil, nil).Settle(ctx, GradeSettlementInput{TenantID: tenantID, RunID: prepared.Run.ID, Context: prepared.Context, Policy: prepared.Policy}, &output); err != nil {
		t.Fatal(err)
	}
	service := NewWorkerCompletionService(store, runtime)
	if _, err := service.Complete(ctx, tenantID, actorID, prepared, input, output); err == nil {
		t.Fatal("OpenAI compatible settlement without a managed identity bypassed the real constraint")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "chk_model_call_fact_identity" {
			t.Fatalf("did not reproduce the production identity failure: %v", err)
		}
	}
	assertSettlementRows := func(want int) {
		t.Helper()
		for _, table := range []string{"ai_grade", "model_usage_event", "model_call_fact"} {
			column := "request_id"
			if table == "ai_grade" {
				column = "adapter_request_id"
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1::uuid AND "+column+"=$2", tenantID, prepared.Run.RequestID).Scan(&count); err != nil || count != want {
				t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
			}
		}
	}
	assertSettlementRows(0)
	task, taskErr := runtime.Get(ctx, tenantID, input.TaskID)
	run, runErr := store.GetRun(ctx, tenantID, prepared.Run.ID)
	if taskErr != nil || runErr != nil || task.Status != workerruntime.StatusLeased || run.Status != RunProcessing || run.GradeID != "" {
		t.Fatalf("identity constraint failure left a partial completion: task=%s run=%s grade=%s errors=%v/%v", task.Status, run.Status, run.GradeID, taskErr, runErr)
	}
	handler := &Handler{imageModelResolver: func(_ context.Context, tenant string) (*ImageModelConfig, error) {
		if tenant != tenantID {
			t.Fatalf("resolved another tenant: %s", tenant)
		}
		return model, nil
	}}
	if err := handler.bindWorkerImageGradeModel(ctx, tenantID, &prepared.Context, prepared.Policy); err != nil {
		t.Fatal(err)
	}
	accepted, err := service.Complete(ctx, tenantID, actorID, prepared, input, output)
	if err != nil || accepted.Run.Status != RunSucceeded || accepted.Task.Status != workerruntime.StatusSucceeded || accepted.Run.GradeID != accepted.Grade.ID {
		t.Fatalf("managed identity could not settle the same lease: run=%s task=%s err=%v", accepted.Run.Status, accepted.Task.Status, err)
	}
	var configID, modelName string
	if err := db.QueryRowContext(ctx, `SELECT managed_model_api_config_id::text,model_name FROM model_call_fact WHERE tenant_id=$1::uuid AND request_id=$2`, tenantID, prepared.Run.RequestID).Scan(&configID, &modelName); err != nil || configID != model.ConfigID || modelName != model.ModelName {
		t.Fatalf("model fact lost trusted identity: config=%s name=%s err=%v", configID, modelName, err)
	}
	replayed, replay, err := service.Replay(ctx, tenantID, actorID, prepared.Run.ID, input)
	if err != nil || !replay || replayed.Grade.ID != accepted.Grade.ID {
		t.Fatalf("identical completion failed to replay: replay=%v err=%v", replay, err)
	}
	assertSettlementRows(1)
}
