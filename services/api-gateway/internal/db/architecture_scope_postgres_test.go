package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func openArchitecturePostgres(t *testing.T) *sql.DB {
	t.Helper()
	database := stdlib.OpenDB(*architecturePostgresConfig(t))
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func architecturePostgresConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(config.Database, "_test") {
		t.Fatal("architecture fixture requires an explicit isolated test database")
	}
	return config
}

// CI 已迁移共享测试库。普通用例只在自己的事务插入合成数据，失败后也回滚。
func architecturePostgresTransaction(t *testing.T) *sql.Tx {
	t.Helper()
	database := openArchitecturePostgres(t)
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func execArchitectureSQL(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func createArchitectureTenant(t *testing.T, tx *sql.Tx) string {
	t.Helper()
	id := uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'synthetic',$2,'active')`, id, id)
	return id
}

func createArchitectureSchool(t *testing.T, tx *sql.Tx, tenant string) string {
	t.Helper()
	id := uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'synthetic',$3,'active')`, id, tenant, id)
	return id
}

func requireArchitectureFK(t *testing.T, tx *sql.Tx, constraint, query string, args ...any) {
	t.Helper()
	execArchitectureSQL(t, tx, `SAVEPOINT rejected_reference`)
	_, err := tx.ExecContext(t.Context(), query, args...)
	execArchitectureSQL(t, tx, `ROLLBACK TO SAVEPOINT rejected_reference`)
	execArchitectureSQL(t, tx, `RELEASE SAVEPOINT rejected_reference`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != constraint {
		t.Fatalf("want foreign key %s, got %v", constraint, err)
	}
}

func TestArchitecturePostgresExamAndEnrollmentScope(t *testing.T) {
	tx := architecturePostgresTransaction(t)
	tenant, otherTenant := createArchitectureTenant(t, tx), createArchitectureTenant(t, tx)
	school, otherSchool := createArchitectureSchool(t, tx, tenant), createArchitectureSchool(t, tx, tenant)
	otherTenantSchool := createArchitectureSchool(t, tx, otherTenant)
	actor, otherActor := uuid.NewString(), uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status)
VALUES($1,$2,'scope-admin','synthetic','fixture','active'),($3,$4,'scope-admin','synthetic','fixture','active')`, actor, tenant, otherActor, otherTenant)

	year, futureYear, otherYear, cohort, grade, class, student := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at)
VALUES($1,$4,$5,'2025',2025,2026,'2025-09-01','2026-08-31'),
      ($2,$4,$5,'2026',2026,2027,'2026-09-01','2027-08-31'),
      ($3,$4,$6,'2025',2025,2026,'2025-09-01','2026-08-31')`, year, futureYear, otherYear, tenant, school, otherSchool)
	execArchitectureSQL(t, tx, `INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name)
VALUES($1,$2,$3,'junior',2025,2028,'synthetic')`, cohort, tenant, school)
	execArchitectureSQL(t, tx, `INSERT INTO grade(id,tenant_id,school_id,name,level_no,status,academic_year_id,grade_cohort_id)
VALUES($1,$2,$3,'synthetic',7,'active',$4,$5)`, grade, tenant, school, year, cohort)
	execArchitectureSQL(t, tx, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id)
VALUES($1,$2,$3,$4,'synthetic','fixture-class','active',$5,$6)`, class, tenant, school, grade, year, cohort)
	execArchitectureSQL(t, tx, `INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status)
VALUES($1,$2,$3,$4,'synthetic','synthetic','active')`, student, tenant, school, class)

	session, exam, otherExam := uuid.NewString(), uuid.NewString(), uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO exam_session(id,tenant_id,school_id,grade_id,name,exam_type,grading_mode,publish_policy,created_by)
VALUES($1,$2,$3,$4,'synthetic','unit_test','ai_assisted','manual',$5)`, session, tenant, school, grade, actor)
	requireArchitectureFK(t, tx, "fk_exam_session_grade_school", `UPDATE exam_session SET school_id=$1 WHERE id=$2`, otherSchool, session)
	requireArchitectureFK(t, tx, "fk_exam_session_creator_tenant", `UPDATE exam_session SET created_by=$1 WHERE id=$2`, otherActor, session)
	execArchitectureSQL(t, tx, `INSERT INTO exam(id,tenant_id,school_id,exam_session_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
VALUES($1,$2,$3,$4,'synthetic','math','unit_test',100,'draft','ai_assisted','manual',$5),
      ($6,$7,$8,NULL,'synthetic','math','unit_test',100,'draft','ai_assisted','manual',$9)`, exam, tenant, school, session, actor, otherExam, otherTenant, otherTenantSchool, otherActor)

	requireArchitectureFK(t, tx, "fk_exam_archiver_tenant", `UPDATE exam SET archived_by=$1 WHERE id=$2`, otherActor, exam)
	requireArchitectureFK(t, tx, "fk_exam_parent_session_school", `UPDATE exam SET school_id=$1 WHERE id=$2`, otherSchool, exam)
	requireArchitectureFK(t, tx, "fk_exam_blueprint_section_exam_tenant", `INSERT INTO exam_blueprint_section(tenant_id,exam_id,title,question_type,question_count,score_per_question,sort_order)
VALUES($1,$2,'synthetic','single_choice',1,2,1)`, otherTenant, exam)

	// 快照的班级/年级只是当时事实，允许保存已无法回查的历史标识。
	const snapshot = `INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot)
VALUES($1,$2,$3,'synthetic','synthetic',gen_random_uuid(),'past grade',gen_random_uuid(),'past class')`
	execArchitectureSQL(t, tx, snapshot, tenant, exam, student)
	// 名册同步也会拒绝跨租户考试。单独核对迁移新增的、已验证的直接外键。
	var validated bool
	if err := tx.QueryRowContext(t.Context(), `SELECT convalidated FROM pg_constraint
WHERE conrelid='exam_candidate_snapshot'::regclass AND conname='fk_exam_candidate_snapshot_exam_tenant' AND contype='f'`).Scan(&validated); err != nil || !validated {
		t.Fatalf("missing validated candidate exam foreign key: %v", err)
	}
	requireArchitectureFK(t, tx, "fk_exam_candidate_snapshot_student_tenant", snapshot, otherTenant, otherExam, student)

	const enrollment = `INSERT INTO student_enrollment(tenant_id,school_id,student_id,academic_year_id,grade_cohort_id,class_id,status,start_date)
VALUES($1,$2,$3,$4,$5,$6,'enrolled','2025-09-01')`
	execArchitectureSQL(t, tx, enrollment, tenant, school, student, year, cohort, class)
	requireArchitectureFK(t, tx, "fk_grade_academic_year_school", `UPDATE grade SET academic_year_id=$1 WHERE id=$2`, otherYear, grade)
	requireArchitectureFK(t, tx, "fk_student_enrollment_class_scope", enrollment, tenant, school, student, futureYear, cohort, class)
}

func TestArchitecturePostgresPlatformMetricsStatementRefresh(t *testing.T) {
	tx := architecturePostgresTransaction(t)
	left, right := createArchitectureTenant(t, tx), createArchitectureTenant(t, tx)
	leftSchool := createArchitectureSchool(t, tx, left)
	createArchitectureSchool(t, tx, right)
	year, cohort, grade, class := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execArchitectureSQL(t, tx, `INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at)
VALUES($1,$2,$3,'2025',2025,2026,'2025-09-01','2026-08-31')`, year, left, leftSchool)
	execArchitectureSQL(t, tx, `INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name)
VALUES($1,$2,$3,'junior',2025,2028,'synthetic')`, cohort, left, leftSchool)
	execArchitectureSQL(t, tx, `INSERT INTO grade(id,tenant_id,school_id,name,level_no,status,academic_year_id,grade_cohort_id)
VALUES($1,$2,$3,'synthetic',7,'active',$4,$5)`, grade, left, leftSchool, year, cohort)
	execArchitectureSQL(t, tx, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id)
VALUES($1,$2,$3,$4,'synthetic','fixture-class','active',$5,$6)`, class, left, leftSchool, grade, year, cohort)
	// 在投影表计数，既覆盖实际聚合和权限，又无需替换生产函数。
	execArchitectureSQL(t, tx, `CREATE TEMP TABLE metrics_refresh_observation(tenant_id uuid NOT NULL)`)
	execArchitectureSQL(t, tx, `CREATE FUNCTION pg_temp.observe_metrics_refresh() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO pg_temp.metrics_refresh_observation VALUES(NEW.tenant_id); RETURN NEW; END $$`)
	triggerName := pgx.Identifier{"observe_metrics_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	execArchitectureSQL(t, tx, `CREATE TRIGGER `+triggerName+` AFTER INSERT OR UPDATE ON platform_school_metrics
FOR EACH ROW EXECUTE FUNCTION pg_temp.observe_metrics_refresh()`)

	// 与批量导入一样用 INSERT SELECT 写学生，汇总必须看到全部学生且只更新一次。
	execArchitectureSQL(t, tx, `INSERT INTO student(tenant_id,school_id,class_id,student_no,name,status)
SELECT $1::uuid,$2::uuid,$3::uuid,'metrics-'||number,'synthetic','active' FROM generate_series(1,100) number`, left, leftSchool, class)
	var studentCount int
	if err := tx.QueryRowContext(t.Context(), `SELECT student_count FROM platform_school_metrics WHERE tenant_id=$1`, left).Scan(&studentCount); err != nil || studentCount != 100 {
		t.Fatalf("student_count=%d, want 100: %v", studentCount, err)
	}
	requireMetricsProjection(t, tx, left, 0, 1)
	requireMetricsProjection(t, tx, right, 0, 0)
	execArchitectureSQL(t, tx, `TRUNCATE metrics_refresh_observation`)

	execArchitectureSQL(t, tx, `INSERT INTO app_user(tenant_id,username,display_name,password_hash,status)
SELECT $1::uuid,'metrics-'||number,'synthetic','fixture','active' FROM generate_series(1,100) number`, left)
	requireMetricsProjection(t, tx, left, 100, 1)
	requireMetricsProjection(t, tx, right, 0, 0)
	execArchitectureSQL(t, tx, `TRUNCATE metrics_refresh_observation`)

	// 一条语句移动多条记录。旧租户和新租户都只刷新一次，并看到完整语句结果。
	execArchitectureSQL(t, tx, `SELECT set_config('edugrade.tenant_id',$1,true)`, left)
	execArchitectureSQL(t, tx, `UPDATE app_user SET tenant_id=$2 WHERE tenant_id=$1`, left, right)
	requireMetricsProjection(t, tx, left, 0, 1)
	requireMetricsProjection(t, tx, right, 100, 1)
	var restoredScope string
	if err := tx.QueryRowContext(t.Context(), `SELECT current_setting('edugrade.tenant_id',true)`).Scan(&restoredScope); err != nil || restoredScope != left {
		t.Fatalf("tenant scope = %s, want %s: %v", restoredScope, left, err)
	}
	execArchitectureSQL(t, tx, `TRUNCATE metrics_refresh_observation`)
	execArchitectureSQL(t, tx, `UPDATE app_user SET display_name='unused' WHERE false`)
	requireMetricsProjection(t, tx, right, 100, 0)
	execArchitectureSQL(t, tx, `DELETE FROM app_user WHERE tenant_id=$1`, right)
	requireMetricsProjection(t, tx, right, 0, 1)

	// 物理删除当前投影学校时，在同一语句内改用其余学校；删完后移除空投影。
	secondSchool := createArchitectureSchool(t, tx, right)
	execArchitectureSQL(t, tx, `DELETE FROM school WHERE tenant_id=$1 AND id<>$2`, right, secondSchool)
	var school string
	if err := tx.QueryRowContext(t.Context(), `SELECT school_id::text FROM platform_school_metrics WHERE tenant_id=$1`, right).Scan(&school); err != nil || school != secondSchool {
		t.Fatalf("remaining school = %s, want %s: %v", school, secondSchool, err)
	}
	execArchitectureSQL(t, tx, `DELETE FROM school WHERE tenant_id=$1`, right)
	if err := tx.QueryRowContext(t.Context(), `SELECT school_id::text FROM platform_school_metrics WHERE tenant_id=$1`, right).Scan(&school); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no projection without a school: %v", err)
	}
}

func TestArchitecturePostgresConcurrentMetricsRefresh(t *testing.T) {
	database := openArchitectureConcurrentDatabase(t)
	t.Run("existing projection", func(t *testing.T) { testConcurrentMetricsRefresh(t, database, false) })
	t.Run("first projection", func(t *testing.T) { testConcurrentMetricsRefresh(t, database, true) })
}

func testConcurrentMetricsRefresh(t *testing.T, database *sql.DB, firstProjection bool) {
	t.Helper()
	seed, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Rollback()
	tenant := createArchitectureTenant(t, seed)
	if !firstProjection {
		createArchitectureSchool(t, seed, tenant)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	first, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	second, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	var secondPID int
	if err := second.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO app_user(tenant_id,username,display_name,password_hash,status) VALUES($1,$2,'synthetic','fixture','active')`
	if firstProjection {
		// 另一事务还看不到首次建校的投影。并发账号写入必须等到投影提交后再聚合。
		createArchitectureSchool(t, first, tenant)
	}
	execArchitectureSQL(t, first, insert, tenant, "first")
	// 等后写事务实际阻塞在投影锁，再提交前写事务。后写聚合必须同时看到两条记录。
	secondResult := make(chan error, 1)
	go func() {
		_, err := second.ExecContext(ctx, insert, tenant, "second")
		secondResult <- err
	}()
	waitForMetricsProjectionLock(t, ctx, database, secondPID, secondResult)
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(); err != nil {
		t.Fatal(err)
	}
	var accounts int
	if err := database.QueryRowContext(ctx, `SELECT account_count FROM platform_school_metrics WHERE tenant_id=$1`, tenant).Scan(&accounts); err != nil || accounts != 2 {
		t.Fatalf("account_count=%d, want 2: %v", accounts, err)
	}
}

// 并发测试必须提交源记录，会留下不可清除的审计历史，因此只在专用随机库中执行。
func openArchitectureConcurrentDatabase(t *testing.T) *sql.DB {
	t.Helper()
	config := architecturePostgresConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	rootConfig := config.Copy()
	rootConfig.Database = "postgres"
	root := stdlib.OpenDB(*rootConfig)
	t.Cleanup(func() { _ = root.Close() })
	config.Database = "architecture_projection_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedDatabase := pgx.Identifier{config.Database}.Sanitize()
	if _, err := root.ExecContext(ctx, `CREATE DATABASE `+quotedDatabase); err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = database.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := root.ExecContext(cleanupCtx, `DROP DATABASE `+quotedDatabase+` WITH (FORCE)`); err != nil {
			t.Errorf("clean isolated architecture database: %v", err)
		}
	})
	migrations, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find migrations: %v", err)
	}
	for _, path := range migrations {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyArchitectureMigration(ctx, database, string(content)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	return database
}

// 与正式迁移执行器一致，每个版本的变更在同一事务内生效。
func applyArchitectureMigration(ctx context.Context, database *sql.DB, content string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, content); err != nil {
		return err
	}
	return tx.Commit()
}

func waitForMetricsProjectionLock(t *testing.T, ctx context.Context, database *sql.DB, secondPID int, result <-chan error) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := database.QueryRowContext(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, secondPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("competing write returned before waiting on the projection lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func requireMetricsProjection(t *testing.T, tx *sql.Tx, tenant string, accounts, refreshes int) {
	t.Helper()
	var gotAccounts, gotRefreshes int
	err := tx.QueryRowContext(t.Context(), `SELECT account_count,
(SELECT count(*) FROM metrics_refresh_observation WHERE tenant_id=$1) FROM platform_school_metrics WHERE tenant_id=$1`, tenant).Scan(&gotAccounts, &gotRefreshes)
	if err != nil || gotAccounts != accounts || gotRefreshes != refreshes {
		t.Fatalf("accounts=%d (want %d), refreshes=%d (want %d): %v", gotAccounts, accounts, gotRefreshes, refreshes, err)
	}
}
