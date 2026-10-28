package calibration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type schoolTemplatePGFixture struct {
	db                                          *sql.DB
	store                                       *PostgresStore
	tenant, school, exam, source, target, actor string
	policy                                      Policy
	targetFacts                                 SchoolTemplateTarget
}

// 随机schema应用正式策略表和本次219迁移，验证真实SQL与锁顺序，不借用学校业务数据或声称完整部署验收。
func schoolTemplatePostgresFixture(t *testing.T) schoolTemplatePGFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "school_calibration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	cfg.RuntimeParams["application_name"] = schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE tenant(id uuid PRIMARY KEY);CREATE TABLE school(id uuid,tenant_id uuid,UNIQUE(tenant_id,id));
CREATE TABLE exam(id uuid,tenant_id uuid,school_id uuid,subject text,deleted_at timestamptz,UNIQUE(tenant_id,id));
CREATE TABLE question(id uuid,tenant_id uuid,exam_id uuid,assessment_archetype text,score numeric,status text,deleted_at timestamptz,UNIQUE(tenant_id,id));
CREATE TABLE app_user(id uuid,tenant_id uuid,UNIQUE(tenant_id,id));
CREATE TABLE question_assessment_config(tenant_id uuid,exam_id uuid,question_id uuid,archetype_code text);
CREATE TABLE exam_question_snapshot(tenant_id uuid,exam_id uuid,question_id uuid,snapshot_version int,archetype_snapshot_json jsonb);
CREATE FUNCTION edugrade_tenant_matches(uuid) RETURNS boolean LANGUAGE sql AS 'SELECT true';`)
	policyMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000079_storyA09_grader_calibration.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.SplitN(string(policyMigration), "CREATE TABLE grader_calibration_session", 2)[0])
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000219_school_calibration_templates.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	f := schoolTemplatePGFixture{db: db, store: NewPostgresStore(db), tenant: uuid.NewString(), school: uuid.NewString(), exam: uuid.NewString(), source: uuid.NewString(), target: uuid.NewString(), actor: uuid.NewString()}
	exec(`INSERT INTO tenant VALUES($1)`, f.tenant)
	exec(`INSERT INTO school VALUES($1,$2)`, f.school, f.tenant)
	exec(`INSERT INTO app_user VALUES($1,$2)`, f.actor, f.tenant)
	exec(`INSERT INTO exam VALUES($1,$2,$3,'数学',NULL)`, f.exam, f.tenant, f.school)
	for _, q := range []string{f.source, f.target} {
		exec(`INSERT INTO question VALUES($1,$2,$3,'structured_steps',10,'active',NULL)`, q, f.tenant, f.exam)
	}
	f.targetFacts = SchoolTemplateTarget{SchoolID: f.school, Subject: "数学", ArchetypeCode: "structured_steps", MaxScore: 10}
	f.policy, err = f.store.PutPolicy(t.Context(), f.tenant, f.exam, f.source, PutPolicyInput{ArchetypeCode: "structured_steps", MaxScore: 10, MinimumSamples: 3, MaximumMAE: 1, MinimumExactAgreement: 0.6, MinimumWithinOneAgreement: 0.9, MaximumSevereRate: 0, SevereErrorThreshold: 3, QualificationValidityDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPostgresSchoolCalibrationTemplateLifecycle(t *testing.T) {
	f := schoolTemplatePostgresFixture(t)
	input := ApproveSchoolTemplateInput{Name: "学校通过标准", SourcePolicyRevision: 1}
	item, err := f.store.ApproveSchoolTemplate(t.Context(), f.tenant, f.actor, f.exam, f.source, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.store.ApproveSchoolTemplate(t.Context(), f.tenant, f.actor, f.exam, f.source, input)
	if err != nil || retry.ID != item.ID {
		t.Fatal("duplicate", retry, err)
	}
	p, err := f.store.ApplySchoolTemplate(t.Context(), f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1, MinimumSamples: 999})
	if err != nil || p.MinimumSamples != 3 || p.SchoolTemplateID != item.ID {
		t.Fatal(p, err)
	}
	if _, err = f.store.ApplySchoolTemplate(t.Context(), f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale target accepted", err)
	}
	if _, err = f.db.ExecContext(t.Context(), `UPDATE school_calibration_template SET name='篡改' WHERE id=$1`, item.ID); err == nil {
		t.Fatal("template history changed")
	}
	withdrawn, err := f.store.WithdrawSchoolTemplate(t.Context(), f.tenant, f.actor, f.targetFacts, item.ID, WithdrawSchoolTemplateInput{ExpectedRevision: 1, Reason: "替换学校标准"})
	if err != nil || withdrawn.Status != "withdrawn" {
		t.Fatal(withdrawn, err)
	}
	list, err := f.store.ListSchoolTemplates(t.Context(), f.tenant, f.targetFacts)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = f.store.ApplySchoolTemplate(t.Context(), f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1, ExpectedRevision: p.Revision}); !errors.Is(err, ErrTemplateUnavailable) {
		t.Fatal("withdrawn applied", err)
	}
	saved, err := f.store.GetPolicy(t.Context(), f.tenant, f.exam, f.target)
	if err != nil || saved.Revision != p.Revision || saved.SchoolTemplateID != item.ID {
		t.Fatal("withdrawal changed saved policy", saved, err)
	}
}

func TestPostgresSchoolCalibrationTemplateWithdrawalWinsBeforeApply(t *testing.T) {
	f := schoolTemplatePostgresFixture(t)
	item, err := f.store.ApproveSchoolTemplate(t.Context(), f.tenant, f.actor, f.exam, f.source, ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(t.Context(), `SELECT id FROM school_calibration_template WHERE id=$1 FOR UPDATE`, item.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.store.ApplySchoolTemplate(ctx, f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("apply crossed held template lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = tx.ExecContext(t.Context(), `UPDATE school_calibration_template SET status='withdrawn',revision=2,withdrawn_by=$2,withdrawn_at=now(),withdraw_reason='标准撤回' WHERE id=$1`, item.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrTemplateUnavailable) {
		t.Fatal("late apply revived withdrawn template", err)
	}
}

func TestPostgresSchoolCalibrationTemplateApplyCommitsBeforeWithdrawal(t *testing.T) {
	f := schoolTemplatePostgresFixture(t)
	item, err := f.store.ApproveSchoolTemplate(t.Context(), f.tenant, f.actor, f.exam, f.source, ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	initial := templatePolicyInput(item, 0)
	initial.SchoolTemplateID = ""
	initial.SchoolTemplateRevision = 0
	p, err := f.store.PutPolicy(t.Context(), f.tenant, f.exam, f.target, initial)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(t.Context(), `SELECT id FROM grader_calibration_policy WHERE tenant_id=$1 AND question_id=$2 FOR UPDATE`, f.tenant, f.target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	applied := make(chan error, 1)
	go func() {
		_, err := f.store.ApplySchoolTemplate(ctx, f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1, ExpectedRevision: p.Revision})
		applied <- err
	}()
	// 等到应用持有模板共享锁并阻塞在目标CAS，随后撤回必须等待该真实事务完成。
	deadline := time.Now().Add(4 * time.Second)
	for {
		var waiting bool
		err = f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO grader_calibration_policy%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("apply never reached target CAS lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	withdrawn := make(chan error, 1)
	go func() {
		_, err := f.store.WithdrawSchoolTemplate(ctx, f.tenant, f.actor, f.targetFacts, item.ID, WithdrawSchoolTemplateInput{ExpectedRevision: 1, Reason: "标准撤回"})
		withdrawn <- err
	}()
	select {
	case err := <-withdrawn:
		t.Fatalf("withdraw crossed applying template lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-applied; err != nil {
		t.Fatal(err)
	}
	if err = <-withdrawn; err != nil {
		t.Fatal(err)
	}
	saved, err := f.store.GetPolicy(t.Context(), f.tenant, f.exam, f.target)
	if err != nil || saved.SchoolTemplateID != item.ID || saved.SchoolTemplateRevision != 1 {
		t.Fatal(saved, err)
	}
}

func TestPostgresSchoolCalibrationTemplateRechecksAuthorizedTarget(t *testing.T) {
	f := schoolTemplatePostgresFixture(t)
	item, err := f.store.ApproveSchoolTemplate(t.Context(), f.tenant, f.actor, f.exam, f.source, ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: f.actor, TenantID: f.tenant, Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": f.school}}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	base := auth.WithAccessScope(auth.WithUser(t.Context(), user), scope)
	beforeSource := withExpectedTemplateTarget(base, f.exam, f.source, f.targetFacts)
	beforeTarget := withExpectedTemplateTarget(base, f.exam, f.target, f.targetFacts)
	if _, err = f.db.ExecContext(t.Context(), `UPDATE exam SET subject='变化后的学科' WHERE id=$1`, f.exam); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveSchoolTemplate(beforeSource, f.tenant, f.actor, f.exam, f.source, ApproveSchoolTemplateInput{Name: "新标准", SourcePolicyRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("approval ignored current facts", err)
	}
	if _, err = f.store.ApplySchoolTemplate(beforeTarget, f.tenant, f.exam, f.target, PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("application ignored current facts", err)
	}
	if _, err = f.store.WithdrawSchoolTemplate(beforeSource, f.tenant, f.actor, f.targetFacts, item.ID, WithdrawSchoolTemplateInput{ExpectedRevision: 1, Reason: "撤回"}); !errors.Is(err, ErrConflict) {
		t.Fatal("withdrawal ignored current facts", err)
	}
}
