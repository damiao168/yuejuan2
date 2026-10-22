package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func backendRemediationPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for backend PostgreSQL regressions")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid backend PostgreSQL test configuration")
	}
	// 定向回归只连接明确的 backend_test 库；不使用另建数据库的通用 E2E 帮助器。
	if !strings.HasSuffix(config.Database, "_backend_test") {
		t.Fatal("backend regressions require a dedicated database ending in _backend_test")
	}
	db := stdlib.OpenDB(*config)
	// 临时表只存在于此连接；连接关闭后自动清理，不触碰迁移表和业务数据。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("backend PostgreSQL test database is unavailable")
	}
	return db
}

func TestBackendManagedProductionReadinessWithPostgres(t *testing.T) {
	db := backendRemediationPostgres(t)
	_, err := db.Exec(`
CREATE TEMP TABLE managed_model_api_config (
 tenant_id uuid, id uuid, base_url text, model_version text, region text, status text,
 deleted_at timestamptz, last_test_status text, last_text_status text NOT NULL DEFAULT 'untested', last_capability_status text,
 last_capability_probe_version text, credential_ciphertext bytea, credential_nonce bytea, is_default boolean);
CREATE TEMP TABLE model_role_binding (tenant_id uuid, managed_model_api_config_id uuid, status text);
CREATE TEMP TABLE subjective_panel_policy (
 tenant_id uuid, education_stage text, subject_code text, archetype_code text,
 evaluation_run_id uuid, readiness_report jsonb, status text);
CREATE TEMP TABLE grading_evaluation_run (tenant_id uuid, id uuid, status text);`)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := modelgovernance.NewCredentialCipher("synthetic-backend-regression-master-key-over-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	store := modelgovernance.NewPostgresStore(db, cipher)
	if err := store.ValidateManagedProductionReadiness(context.Background()); err != nil {
		t.Fatalf("empty production candidate SQL failed: %v", err)
	}
	tenant, configID := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`INSERT INTO managed_model_api_config (
 tenant_id,id,base_url,model_version,region,status,last_test_status,last_capability_status,last_capability_probe_version,is_default)
 VALUES ($1,$2,'http://unsafe.example','v1','global','active','success','success',$3,false)`, tenant, configID, modelgovernance.ManagedCapabilityProbeVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO model_role_binding VALUES($1,$2,'active')`, tenant, configID); err != nil {
		t.Fatal(err)
	}
	// 有活动角色绑定的非默认模型也必须拒绝；提前返回后唯一连接必须可继续使用。
	if err := store.ValidateManagedProductionReadiness(context.Background()); !errors.Is(err, modelgovernance.ErrProductionUnsafe) {
		t.Fatalf("unsafe role-bound production model error=%v", err)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("candidate rejection retained a database connection")
	}
	if _, err := db.Exec(`DELETE FROM model_role_binding`); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateManagedProductionReadiness(context.Background()); err != nil {
		t.Fatalf("unassigned shadow model blocked readiness: %v", err)
	}
}

func TestBackendMigratedSchemaReadinessAndAppealScopeWithPostgres(t *testing.T) {
	db := backendRemediationPostgres(t)
	cipher, err := modelgovernance.NewCredentialCipher("synthetic-backend-regression-master-key-over-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	// 使用完整历史迁移建立的真实表，补充 TEMP 夹具不能发现的字段和类型兼容问题。
	if err := modelgovernance.NewPostgresStore(db, cipher).ValidateManagedProductionReadiness(context.Background()); err != nil {
		t.Fatalf("migrated production readiness query failed: %v", err)
	}
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: uuid.NewString(), TenantID: uuid.NewString(), Roles: []string{"school_admin"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": uuid.NewString()}}})
	if err != nil {
		t.Fatal(err)
	}
	store := appeal.NewPostgresStore(db)
	items, err := store.ListAppeals(context.Background(), scope.TenantID, appeal.ListFilter{AccessScope: &scope, Limit: 1})
	if err != nil || len(items) != 0 {
		t.Fatalf("migrated scoped collection count=%d err=%v", len(items), err)
	}
	stats, err := store.Statistics(context.Background(), scope.TenantID, appeal.StatisticsFilter{AccessScope: &scope})
	if err != nil || stats.Total != 0 || stats.ScoreAdjustedCount != 0 {
		t.Fatalf("migrated scoped statistics=%+v err=%v", stats, err)
	}
}

func TestBackendAppealScopeWithPostgres(t *testing.T) {
	db := backendRemediationPostgres(t)
	backendAppealTemporaryTables(t, db)
	tenant, schoolA, schoolB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	backendSeedAppeal(t, db, tenant, schoolA, "own", 2, time.Now().UTC().Add(-time.Hour))
	foreignExam := backendSeedAppeal(t, db, tenant, schoolB, "foreign", 100, time.Now().UTC())
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"school_admin"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": schoolA}}})
	if err != nil {
		t.Fatal(err)
	}
	store := appeal.NewPostgresStore(db)
	items, err := store.ListAppeals(context.Background(), tenant, appeal.ListFilter{AccessScope: &scope, Limit: 1})
	if err != nil || len(items) != 1 || items[0].Reason != "own reason" {
		t.Fatalf("PostgreSQL scoped pagination=%v err=%v", items, err)
	}
	stats, err := store.Statistics(context.Background(), tenant, appeal.StatisticsFilter{AccessScope: &scope})
	if err != nil || stats.Total != 1 || stats.ByStatus["score_adjusted"] != 1 || stats.ScoreAdjustedCount != 1 || stats.AverageHandleHours != 2 {
		t.Fatalf("PostgreSQL scoped aggregation=%+v err=%v", stats, err)
	}
	foreign, err := store.Statistics(context.Background(), tenant, appeal.StatisticsFilter{AccessScope: &scope, ExamID: foreignExam})
	if err != nil || foreign.Total != 0 || foreign.ScoreAdjustedCount != 0 || foreign.AverageHandleHours != 0 {
		t.Fatalf("PostgreSQL foreign aggregation=%+v err=%v", foreign, err)
	}
	tenantScope := auth.AccessScope{TenantID: tenant, TenantWide: true}
	all, err := store.ListAppeals(context.Background(), tenant, appeal.ListFilter{AccessScope: &tenantScope})
	if err != nil || len(all) != 2 {
		t.Fatalf("PostgreSQL tenant collection=%v err=%v", all, err)
	}
}

func backendAppealTemporaryTables(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
CREATE TEMP TABLE exam (id uuid,tenant_id uuid,school_id uuid,name text,subject text);
CREATE TEMP TABLE exam_class (tenant_id uuid,exam_id uuid,class_id uuid,deleted_at timestamptz);
CREATE TEMP TABLE submission_grade (id uuid,tenant_id uuid,anonymous_code text);
CREATE TEMP TABLE appeal (
 id uuid,tenant_id uuid,exam_id uuid,submission_id uuid,submission_grade_id uuid,student_id uuid,
 target_type text DEFAULT 'exam',final_grade_id uuid,question_id uuid,question_no text DEFAULT '',deduction_point_id text DEFAULT '',
 reason text,attachment jsonb DEFAULT '{}',status text DEFAULT 'score_adjusted',result_reason text DEFAULT '',
 assigned_to uuid,teacher_recommendation text DEFAULT '',teacher_recommendation_reason text DEFAULT '',
 teacher_recommended_score float8,teacher_recommendation_by uuid,teacher_recommendation_at timestamptz,
 reviewed_by uuid,reviewed_at timestamptz,closed_by uuid,closed_at timestamptz,revision bigint DEFAULT 1,
 created_by uuid,created_at timestamptz,updated_at timestamptz DEFAULT now(),deleted_at timestamptz);
CREATE TEMP TABLE score_adjustment (id uuid,tenant_id uuid,appeal_id uuid,deleted_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
}

func backendSeedAppeal(t *testing.T, db *sql.DB, tenant, school, label string, handleHours float64, created time.Time) string {
	t.Helper()
	examID, gradeID, appealID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO exam VALUES($1,$2,$3,$4,'math')`, examID, tenant, school, label); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO submission_grade VALUES($1,$2,$3)`, gradeID, tenant, label); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO appeal(id,tenant_id,exam_id,submission_id,submission_grade_id,student_id,reason,created_by,created_at,reviewed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, appealID, tenant, examID, uuid.NewString(), gradeID, uuid.NewString(),
		label+" reason", uuid.NewString(), created, created.Add(time.Duration(handleHours)*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO score_adjustment VALUES($1,$2,$3,NULL)`, uuid.NewString(), tenant, appealID); err != nil {
		t.Fatal(err)
	}
	return examID
}
