package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 测试只在随机隔离库应用全部正式迁移；业务连接是无 BYPASSRLS 角色，不连接阅卷业务库。
func gradingAIPlanTestDB(t *testing.T) (*sql.DB, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for isolated AI plan PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	root := stdlib.OpenDB(*config)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	databaseName, roleName := "grading_plan_test_"+suffix, "grading_plan_role_"+suffix
	quotedDB, quotedRole := pgx.Identifier{databaseName}.Sanitize(), pgx.Identifier{roleName}.Sanitize()
	if _, err := root.Exec("CREATE DATABASE " + quotedDB); err != nil {
		root.Close()
		t.Fatal(err)
	}
	ownerConfig := config.Copy()
	ownerConfig.Database = databaseName
	owner := stdlib.OpenDB(*ownerConfig)
	var runtime *sql.DB
	t.Cleanup(func() {
		if runtime != nil {
			_ = runtime.Close()
		}
		_ = owner.Close()
		if _, err := root.Exec("DROP DATABASE " + quotedDB + " WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		if _, err := root.Exec("DROP ROLE IF EXISTS " + quotedRole); err != nil {
			t.Error(err)
		}
		_ = root.Close()
	})
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	var tenant string
	if err := owner.QueryRow(`SELECT id::text FROM tenant WHERE code='demo'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("CREATE ROLE " + quotedRole + " LOGIN NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("GRANT edugrade_tenant_runtime TO " + quotedRole); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := ownerConfig.Copy()
	runtimeConfig.User, runtimeConfig.Password = roleName, ""
	runtimeConfig.RuntimeParams["edugrade.tenant_id"] = tenant
	runtime = stdlib.OpenDB(*runtimeConfig)
	runtime.SetMaxOpenConns(2)
	return owner, runtime, tenant
}

func TestPostgresGradingAIPlanConfirmationFreezeAndTenantBoundary(t *testing.T) {
	owner, db, tenant := gradingAIPlanTestDB(t)
	ctx := t.Context()
	var actor, school string
	if err := db.QueryRow(`SELECT id::text FROM app_user WHERE tenant_id=$1::uuid AND username='teacher'`, tenant).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO school(tenant_id,name,code,status) VALUES($1::uuid,'Synthetic AI plan school',$2,'active') RETURNING id::text`, tenant, uuid.NewString()).Scan(&school); err != nil {
		t.Fatal(err)
	}
	exam, run := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'synthetic grading plan','physics','quiz',12,'draft','ai_assisted','manual_after_confirmation',$4::uuid)`, exam, tenant, school, actor); err != nil {
		t.Fatal(err)
	}
	modelA, modelB := uuid.NewString(), uuid.NewString()
	for index, id := range []string{modelA, modelB} {
		if _, err := db.Exec(`INSERT INTO managed_model_api_config(id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,credential_ciphertext,credential_nonce) VALUES($1::uuid,$2::uuid,'custom',$3,'openai_compatible','https://synthetic.invalid/v1',$3,'v1',decode(repeat('ab',16),'hex'),decode(repeat('cd',12),'hex'))`, id, tenant, []string{"model-a", "model-b"}[index]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tenant_grading_model_selection(tenant_id,mode,managed_model_api_config_id,updated_by) VALUES($1::uuid,'model',$2::uuid,$3::uuid) ON CONFLICT(tenant_id) DO UPDATE SET mode='model',managed_model_api_config_id=EXCLUDED.managed_model_api_config_id,revision=gen_random_uuid()`, tenant, modelA, actor); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	file, submission, page, profile := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat('a',64),'test','synthetic-ai-plan','tenant',$4::uuid)`, []any{file, tenant, exam, actor}},
		{`INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,'manual_import','created',$4::uuid)`, []any{submission, tenant, exam, actor}},
		{`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, []any{page, tenant, submission, file}},
		{`INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,'synthetic-ai-plan','junior','physics',1)`, []any{profile, tenant}},
	} {
		if _, err := db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	fillSegment := ""
	for index, kind := range []string{"calculation", "fill_blank"} {
		question, segment := uuid.NewString(), uuid.NewString()
		if kind == "fill_blank" {
			fillSegment = segment
		}
		mode := "AI_ASSIST"
		if kind == "fill_blank" {
			mode = "RULE_AUTO"
		}
		if _, err := db.Exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,12,$6,'active')`, question, tenant, exam, kind, kind, index+1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'{}','manual','accepted')`, segment, tenant, submission, page, question, kind); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'structured_steps','[]','R1','{}','{}','{}',jsonb_build_object('mode',$5::text),repeat('a',64))`, tenant, exam, question, profile, mode); err != nil {
			t.Fatal(err)
		}
	}
	checkStart := func(segment string, expected error) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = validateGradingAIPlanForStartTx(ctx, tx, tenant, exam, segment)
		_ = tx.Rollback()
		if !errors.Is(err, expected) {
			t.Fatalf("start requirement for segment %q got %v want %v", segment, err, expected)
		}
	}
	checkStart("", ErrGradingAIPlanConfirmationRequired)
	checkStart(fillSegment, nil)
	initial, err := store.GetGradingAIPlan(ctx, tenant, exam)
	if err != nil || initial.Plan.EffectiveMode != "single" || initial.Plan.SingleModel.ModelConfigID != modelA {
		t.Fatalf("initial single plan: %+v %v", initial, err)
	}
	input := GradingAIPlanInput{ExpectedRevision: "absent", FallbackModelConfigID: modelA, Roles: map[string]GradingAIRoleSelection{}, DecisionConfig: initial.Plan.DecisionConfig}
	emptyInput := input
	emptyInput.FallbackModelConfigID = ""
	if _, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, emptyInput); !errors.Is(err, ErrGradingAIModelRequired) {
		t.Fatalf("empty plan was confirmable: %v", err)
	}
	confirmed, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, input)
	if err != nil || confirmed.Plan.ConfirmedAt == nil {
		t.Fatalf("confirm: %+v %v", confirmed, err)
	}
	checkStart("", nil)
	if _, err := db.Exec(`INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,started_by) VALUES($1::uuid,$2::uuid,$3::uuid,'synthetic-plan-freeze','processing',$4::uuid)`, run, tenant, exam, actor); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := freezeScoringRunRulesTx(ctx, tx, tenant, exam, run); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision, input.FallbackModelConfigID = confirmed.Plan.Revision, modelB
	updated, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, input)
	if err != nil || updated.Plan.SingleModel.ModelConfigID != modelB {
		t.Fatalf("update: %+v %v", updated, err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT rule_snapshot->'ai_plan' FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenant, run).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var frozen GradingAIPlan
	if err := json.Unmarshal(raw, &frozen); err != nil || frozen.Revision != confirmed.Plan.Revision || frozen.SingleModel.ModelConfigID != modelA {
		t.Fatalf("later settings rewrote running plan: %+v %v", frozen, err)
	}
	if strings.Contains(string(raw), "synthetic.invalid") || strings.Contains(string(raw), "credential") {
		t.Fatal("private connection data entered public snapshot")
	}
	if _, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, input); !errors.Is(err, ErrGradingAIPlanChanged) {
		t.Fatalf("stale confirmation accepted: %v", err)
	}
	input.ExpectedRevision = updated.Plan.Revision
	input.Roles = map[string]GradingAIRoleSelection{"primary_a": {ModelConfigID: modelA, StrengthRank: 1}, "primary_b": {ModelConfigID: modelB, StrengthRank: 1}}
	if _, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, input); !errors.Is(err, ErrGradingAIThresholdRequired) {
		t.Fatalf("dual plan accepted missing manual differences: %v", err)
	}
	input.DecisionConfig.PrimaryAllowedDiffPoints, input.DecisionConfig.TeacherAllowedDiffPoints = pointDifference(0), pointDifference(0)
	updated, err = store.SaveGradingAIPlan(ctx, tenant, exam, actor, input)
	if err != nil || updated.Plan.EffectiveMode != "dual" {
		t.Fatalf("explicit zero-difference dual plan rejected: %+v %v", updated, err)
	}
	checkStart("", nil)
	var hidden int
	if err := db.QueryRow(`SELECT count(*) FROM exam_grading_ai_plan WHERE tenant_id<>$1::uuid`, tenant).Scan(&hidden); err != nil || hidden != 0 {
		t.Fatalf("tenant plan leaked: %d %v", hidden, err)
	}
	if _, err := owner.Exec(`UPDATE exam SET archived_at=now(),archived_by=$2::uuid WHERE id=$1::uuid`, exam, actor); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = updated.Plan.Revision
	if _, err := store.SaveGradingAIPlan(ctx, tenant, exam, actor, input); err == nil {
		t.Fatal("archived exam accepted configuration mutation")
	}
}
