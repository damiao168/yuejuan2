package scorerelease

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

func TestReleaseReasonUnicodeContractMatchesPostgres(t *testing.T) {
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
	schema := "reason_length_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
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
	if _, err = db.ExecContext(ctx, `CREATE TABLE score_release (LIKE public.score_release INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000208_score_release_reason_characters.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, reason string
		valid        bool
	}{
		{"Chinese past old byte limit", strings.Repeat("中", 667), true},
		{"Chinese at limit", strings.Repeat("中", 1000), true},
		{"non BMP at limit", strings.Repeat("𠮷", 1000), true},
		{"ASCII at limit", strings.Repeat("a", 1000), true},
		{"over limit", strings.Repeat("中", 1001), false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			create := CreateInput{Reason: tc.reason, IdempotencyKey: "synthetic-key"}
			rollback := RollbackInput{Reason: tc.reason, SourceReleaseID: "source", IdempotencyKey: "synthetic-key"}
			regrade := CreateRegradeInput{Reason: tc.reason, SourceReleaseID: "source", QuestionID: "question", IdempotencyKey: "synthetic-key", Changes: []RegradeChange{{SubmissionID: "submission", QuestionID: "question", Score: 1, MaxScore: 5}}}
			if normalizeCreate(&create) != tc.valid || normalizeRollback(&rollback) != tc.valid || normalizeRegrade(&regrade) != tc.valid {
				t.Fatal("service reason contract mismatch")
			}
			for _, source := range []string{SourceInitial, SourceRollback, SourceRegrade} {
				_, err := db.ExecContext(ctx, `INSERT INTO score_release(tenant_id,exam_id,version,source,reason,idempotency_key,created_by,source_release_id) VALUES($1,$2,1,$3,$4,$5,$6,$7)`, uuid.NewString(), uuid.NewString(), source, tc.reason, uuid.NewString(), uuid.NewString(), uuid.NewString())
				if (err == nil) != tc.valid {
					t.Fatalf("source=%s valid=%v err=%v", source, tc.valid, err)
				}
			}
		})
	}
}
