package subjective

import (
	"context"
	"database/sql"
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

// 完整迁移只应用到本次创建的随机隔离库；运行连接使用真实无 BYPASSRLS 角色。
// 固定租户与单连接让会话故障开关可控，不把超级用户连接当作运行权限验证。
func imageSettlementPostgresDB(t *testing.T) (*sql.DB, *sql.DB, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for execution diagnostics PostgreSQL integration")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	rootCfg := cfg.Copy()
	rootCfg.Database = "postgres"
	root := stdlib.OpenDB(*rootCfg)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	dbName, roleName := "image_settlement_test_"+suffix, "image_settlement_role_"+suffix
	quotedDB, quotedRole := pgx.Identifier{dbName}.Sanitize(), pgx.Identifier{roleName}.Sanitize()
	if _, err := root.Exec(`CREATE DATABASE ` + quotedDB); err != nil {
		root.Close()
		t.Fatal(err)
	}
	ownerCfg := cfg.Copy()
	ownerCfg.Database = dbName
	owner := stdlib.OpenDB(*ownerCfg)
	owner.SetMaxOpenConns(1)
	var runtime *sql.DB
	t.Cleanup(func() {
		if runtime != nil {
			_ = runtime.Close()
		}
		_ = owner.Close()
		if _, err := root.Exec(`DROP DATABASE ` + quotedDB + ` WITH (FORCE)`); err != nil {
			t.Errorf("clean isolated diagnostics database: %v", err)
		}
		if _, err := root.Exec(`DROP ROLE IF EXISTS ` + quotedRole); err != nil {
			t.Errorf("clean isolated diagnostics role: %v", err)
		}
		_ = root.Close()
	})
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migration inventory: %v", err)
	}
	sort.Strings(files)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	var tenantID string
	if err := owner.QueryRow(`SELECT id::text FROM tenant WHERE code='demo'`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole); err != nil {
		t.Fatal(err)
	}
	runtimeCfg := ownerCfg.Copy()
	runtimeCfg.User, runtimeCfg.Password = roleName, ""
	runtimeCfg.RuntimeParams["edugrade.tenant_id"] = tenantID
	runtimeCfg.RuntimeParams["statement_timeout"] = "10000"
	runtimeCfg.RuntimeParams["lock_timeout"] = "3000"
	runtime = stdlib.OpenDB(*runtimeCfg)
	runtime.SetMaxOpenConns(1)
	var actualDB, actualRole string
	var superuser, bypass bool
	if err := runtime.QueryRow(`SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&actualDB, &actualRole, &superuser, &bypass); err != nil || actualDB != dbName || actualRole != roleName || superuser || bypass {
		t.Fatalf("restricted diagnostics login mismatch: db=%s role=%s superuser=%v bypass=%v err=%v", actualDB, actualRole, superuser, bypass, err)
	}
	t.Logf("applied %d migrations through %s; runtime login is NOSUPERUSER NOBYPASSRLS", len(files), filepath.Base(files[len(files)-1]))
	return owner, runtime, tenantID
}
