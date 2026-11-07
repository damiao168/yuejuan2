package capture

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 用真实 PostgreSQL 执行合并前的加锁谓词；随机 schema 只保留本项需要的列，不能视为全流程集成。
func TestAuditMergeRejectsOtherBatchExamOrDeletedSubmissionPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("PostgreSQL test URL required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "merge_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	cfg = cfg.Copy()
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE submission(id uuid PRIMARY KEY,tenant_id uuid,exam_id uuid,deleted_at timestamptz);CREATE TABLE capture_batch(id uuid PRIMARY KEY,tenant_id uuid,exam_id uuid,deleted_at timestamptz);CREATE TABLE capture_page(id uuid PRIMARY KEY,tenant_id uuid,submission_id uuid,capture_batch_id uuid,status text,deleted_at timestamptz)`)
	tenant, batch, exam, source, target, otherBatch, otherExam := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO capture_batch(id,tenant_id,exam_id)VALUES($1,$2,$3),($4,$2,$3)`, batch, tenant, exam, otherBatch)
	exec(`INSERT INTO submission(id,tenant_id,exam_id)VALUES($1,$2,$3),($4,$2,$3)`, source, tenant, exam, target)
	exec(`INSERT INTO capture_page(id,tenant_id,submission_id,capture_batch_id,status)VALUES($1,$2,$3,$4,'needs_review'),($5,$2,$6,$4,'needs_review')`, uuid.NewString(), tenant, source, batch, uuid.NewString(), target)
	for _, scenario := range []string{"valid", "other_batch", "other_exam", "deleted", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			must := func(query string, args ...any) {
				t.Helper()
				if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			id := target
			switch scenario {
			case "other_batch":
				must(`UPDATE capture_page SET capture_batch_id=$1 WHERE submission_id=$2`, otherBatch, target)
			case "other_exam":
				must(`UPDATE submission SET exam_id=$1 WHERE id=$2`, otherExam, target)
			case "deleted":
				must(`UPDATE submission SET deleted_at=now() WHERE id=$1`, target)
			case "unknown":
				id = uuid.NewString()
			}
			err = lockMergeSubmissionsTx(t.Context(), tx, tenant, batch, source, id)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("unexpected merge admission %v", err)
			}
		})
	}
}
