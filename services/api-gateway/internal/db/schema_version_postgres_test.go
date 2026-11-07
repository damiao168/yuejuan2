package db

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestGatewaySchemaVersionPostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || (!strings.HasPrefix(cfg.Database, "edugrade_ci_shared_") && !strings.HasSuffix(cfg.Database, "_test")) {
		t.Fatal("schema-version fixture requires an explicit isolated test database")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "gateway_version_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = database.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = database.ExecContext(context.Background(), `SET search_path=public`)
		if _, err := database.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("clean isolated schema: %v", err)
		}
	}()
	// 只在随机 schema 构造迁移账本，不能修改真实 public.schema_migration。
	if _, err = database.ExecContext(ctx, `SET search_path=`+schema); err != nil {
		t.Fatal(err)
	}
	if err = VerifySchemaVersion(ctx, database); err == nil {
		t.Fatal("accepted missing migration ledger")
	}
	if _, err = database.ExecContext(ctx, `CREATE TABLE schema_migration(filename text)`); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "000001", RequiredSchemaVersion, "999999"} {
		if _, err = database.ExecContext(ctx, `TRUNCATE schema_migration`); err != nil {
			t.Fatal(err)
		}
		if version != "" {
			if _, err = database.ExecContext(ctx, `INSERT INTO schema_migration VALUES ($1)`, version+"_fixture.sql"); err != nil {
				t.Fatal(err)
			}
		}
		err = VerifySchemaVersion(ctx, database)
		if (err == nil) != (version == RequiredSchemaVersion) {
			t.Fatalf("version %q: %v", version, err)
		}
	}
}
