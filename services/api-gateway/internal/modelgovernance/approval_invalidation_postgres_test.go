package modelgovernance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresInvalidationRevokesDependentApprovalsAtomically(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "approval_evidence_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"model_evaluation_run", "model_evaluation_candidate", "model_approval"} {
		exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)")
	}
	exec(`CREATE TRIGGER approval_immutable BEFORE UPDATE ON model_approval FOR EACH ROW EXECUTE FUNCTION public.enforce_model_approval_immutable()`)
	tenant, actor := uuid.NewString(), uuid.NewString()
	newRun := func() string {
		id := uuid.NewString()
		exec(`INSERT INTO model_evaluation_run(id,tenant_id,run_key,display_name,dataset_reference,dataset_sha256,authorization_reference,evidence_class,subject,grade,question_type,modality,sample_count,repeat_count,status,completed_at) VALUES($1::uuid,$2::uuid,$1::text,'synthetic','dataset',$3,'authorization','authorized_frozen_set','math','grade','short_answer','text',10,1,'completed',now())`, id, tenant, strings.Repeat("a", 64))
		return id
	}
	insertApproval := `INSERT INTO model_approval(id,tenant_id,evaluation_run_id,evaluation_candidate_id,deployment_id,provider_key,deployment_key,model_name,model_version,prompt_version,rubric_version,dataset_reference,dataset_sha256,authorization_reference,subject,grade,question_type,modality,manual_review_rate,decision_reference,expires_at) VALUES($1::uuid,$2,$3,$4,$5,'provider','deployment','model','v1','p1','r1','dataset',$6,'authorization','math','grade','short_answer','text',.5,$1::text,now()+interval '1 hour')`
	newApproval := func(run string) string {
		id := uuid.NewString()
		exec(insertApproval, id, tenant, run, uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64))
		return id
	}
	stale := newRun()
	oldApproval := newApproval(stale)
	exec(`UPDATE model_evaluation_run SET status='invalidated',invalidated_at=now(),invalidated_by=$2,invalidation_reason='historical withdrawal' WHERE id=$1`, stale, actor)
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000212_model_approval_evidence_lifecycle.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	assertRevoked := func(id string, want bool) {
		t.Helper()
		var revoked bool
		if err := db.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM model_approval WHERE id=$1`, id).Scan(&revoked); err != nil || revoked != want {
			t.Fatalf("revoked=%v want=%v err=%v", revoked, want, err)
		}
	}
	assertRevoked(oldApproval, true)
	run := newRun()
	approval := newApproval(run)
	unrelated := newApproval(newRun())
	store := NewPostgresStore(db)
	if _, err := store.InvalidateEvaluationRun(ctx, tenant, actor, run, "authorization withdrawn"); err != nil {
		t.Fatal(err)
	}
	assertRevoked(approval, true)
	assertRevoked(unrelated, false)
	if _, err := db.ExecContext(ctx, insertApproval, uuid.NewString(), tenant, run, uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)); err == nil {
		t.Fatal("new approval accepted invalidated source")
	}
	rollbackRun := newRun()
	rollbackApproval := newApproval(rollbackRun)
	exec(`CREATE FUNCTION reject_approval_revoke() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic revoke failure'; END $$; CREATE TRIGGER reject_revoke BEFORE UPDATE ON model_approval FOR EACH ROW EXECUTE FUNCTION reject_approval_revoke()`)
	if _, err := store.InvalidateEvaluationRun(ctx, tenant, actor, rollbackRun, "synthetic failure"); err == nil {
		t.Fatal("failure injection did not fail")
	}
	assertRevoked(rollbackApproval, false)
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM model_evaluation_run WHERE id=$1`, rollbackRun).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("source changed without dependent revocation: %s %v", status, err)
	}
	exec(`DROP TRIGGER reject_revoke ON model_approval`)
	// 并发创建持有来源共享锁；作废须等待创建提交，然后撤销该笔新批准。
	concurrentRun := newRun()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	concurrentApproval := uuid.NewString()
	if _, err = tx.ExecContext(ctx, insertApproval, concurrentApproval, tenant, concurrentRun, uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["application_name"] = schema
	writer := stdlib.OpenDB(*cfg)
	defer writer.Close()
	result := make(chan error, 1)
	go func() {
		_, err := NewPostgresStore(writer).InvalidateEvaluationRun(ctx, tenant, actor, concurrentRun, "concurrent withdrawal")
		result <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, schema).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("invalidation passed source lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertRevoked(concurrentApproval, true)
	items, err := store.ListModelApprovals(ctx, tenant)
	if err != nil || len(items) != 5 {
		t.Fatalf("approval history lost: %d %v", len(items), err)
	}
}
