package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func e2ePostgresRouter(db *sql.DB, authOptions ...config.AuthConfig) http.Handler {
	return e2ePostgresRouterWithObjects(db, files.NewMemoryObjectStorage(), authOptions...)
}

func e2ePostgresRouterWithObjects(db *sql.DB, objectStore *files.MemoryObjectStorage, authOptions ...config.AuthConfig) http.Handler {
	if err := e2eSeedWorkerAccount(db); err != nil {
		panic(err)
	}
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "api-gateway-postgres-e2e-test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
		Barcode: config.BarcodeConfig{
			ActiveKeyID: e2eBarcodeKeyring().ActiveKeyID,
			HMACKeys:    e2eBarcodeKeyring().Keys,
		},
		Files: config.FileConfig{
			Bucket:            "edugrade-story041-e2e",
			MaxUploadBytes:    2 * 1024 * 1024,
			AllowedExtensions: []string{".pdf", ".png", ".jpg", ".jpeg", ".csv", ".docx"},
		},
		Security: config.SecurityConfig{MaxRequestBodyBytes: 2 * 1024 * 1024},
		AIService: config.AIServiceConfig{
			ProviderKey: "local", DeploymentKey: "postgres-e2e", AdapterType: "local_llama_cpp",
			ModelVersion: "postgres-e2e-model", DeploymentRegion: "on_premise", CapabilityProfile: "test",
		},
		ModelSecrets: config.ModelSecretConfig{
			MasterKey:         "postgres-e2e-model-credential-master-key",
			ReviewEvidenceKey: "postgres-e2e-independent-review-evidence-key",
		},
	}
	if len(authOptions) > 0 {
		cfg.Auth = authOptions[0]
	}
	stores, err := NewPostgresApplicationStores(&Infrastructure{Config: cfg, DB: db, ObjectStore: objectStore})
	if err != nil {
		panic(fmt.Sprintf("build production PostgreSQL application stores: %v", err))
	}
	modules, err := NewTransactionalApplicationModules(ApplicationDependencies{
		Config: cfg, ObjectStore: objectStore, DB: db,
	}, stores)
	if err != nil {
		panic(fmt.Sprintf("build production PostgreSQL application modules: %v", err))
	}
	return NewRouterComplete(RouterDependencies{
		Config: cfg, Logger: logger.New(io.Discard, "error"), Modules: modules,
	})
}

func e2eBarcodeKeyring() capture.BarcodeKeyring {
	return capture.BarcodeKeyring{
		ActiveKeyID: "story060-e2e",
		Keys:        map[string][]byte{"story060-e2e": []byte(strings.Repeat("s", 32))},
	}
}

func e2eOpenPostgresTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test database URL: %v", err)
	}
	adminConfig := config.Copy()
	adminConfig.Database = "postgres"
	adminDB := stdlib.OpenDB(*adminConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping PostgreSQL administration database: %v", err)
	}
	databaseName := "edugrade_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedDatabaseName := pgx.Identifier{databaseName}.Sanitize()
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+quotedDatabaseName); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create isolated PostgreSQL E2E database: %v", err)
	}
	testConfig := config.Copy()
	testConfig.Database = databaseName
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_, _ = adminDB.ExecContext(ctx, "DROP DATABASE "+quotedDatabaseName+" WITH (FORCE)")
		_ = adminDB.Close()
		t.Fatalf("ping PostgreSQL test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		// 随机库包含完整迁移；并行测试下删除目录可能超过一次业务请求的时限，清理单独限时。
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := adminDB.ExecContext(cleanupCtx, "DROP DATABASE "+quotedDatabaseName+" WITH (FORCE)"); cleanupErr != nil {
			t.Errorf("drop isolated PostgreSQL E2E database %s: %v", databaseName, cleanupErr)
		}
		_ = adminDB.Close()
	})
	return db
}

func e2eApplyPostgresMigrations(t *testing.T, db *sql.DB) {
	e2eApplyPostgresMigrationsThrough(t, db, "")
}

func e2eApplyPostgresMigrationsThrough(t *testing.T, db *sql.DB, lastMigration string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found for PostgreSQL E2E")
	}
	sort.Strings(files)
	// 此处创建完整历史结构（123 次以上迁移），不只是运行一条查询。
	// 为初始化设置合理时限，避免把磁盘竞争误判为业务操作性能回归。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS edugrade_e2e_applied_migration (
  name TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		t.Fatalf("create E2E migration ledger: %v", err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		if lastMigration != "" && name > lastMigration {
			continue
		}
		var applied bool
		err := db.QueryRowContext(ctx, `SELECT true FROM edugrade_e2e_applied_migration WHERE name=$1`, name).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			t.Fatalf("read E2E migration ledger for %s: %v", name, err)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read migration %s: %v", file, err)
		}
		// 正式部署使用 psql -1：一份迁移及其台账必须同事务，表锁和失败回滚才有相同语义。
		if err := e2eApplyMigrationFile(ctx, db, name, string(raw)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func e2eApplyMigrationFile(ctx context.Context, db *sql.DB, name, source string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range e2eSplitSQLStatements(source) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %q: %w", statement, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO edugrade_e2e_applied_migration (name) VALUES ($1)`, name); err != nil {
		return err
	}
	return tx.Commit()
}
