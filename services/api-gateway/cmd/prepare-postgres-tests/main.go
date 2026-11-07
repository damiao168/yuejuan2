// prepare-postgres-tests 只为 CI 创建随机测试库；不迁移或删除用户传入的数据库。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getenv("EDUGRADE_REQUIRE_PG_TESTS") != "1" {
		return fmt.Errorf("EDUGRADE_REQUIRE_PG_TESTS=1 is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := pgx.ParseConfig(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if err != nil || strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL")) == "" {
		return fmt.Errorf("explicit PostgreSQL test DSN is required")
	}
	target := cfg.Database
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer admin.Close(context.Background())
	if len(os.Args) > 1 && os.Args[1] == "cleanup" {
		if !regexp.MustCompile(`^edugrade_ci_shared_[a-f0-9]{32}$`).MatchString(target) {
			return fmt.Errorf("refusing to remove a database not created by this tool")
		}
		_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)")
		return err
	}
	name := "edugrade_ci_shared_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if _, err := admin.Exec(cleanup, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				fmt.Fprintf(os.Stderr, "cleanup random test database %s failed: %v\n", name, err)
			}
		}
	}()
	cfg.Database = name
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(context.Background())
	paths, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(paths) == 0 {
		return fmt.Errorf("test migrations not found")
	}
	sort.Strings(paths)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(raw)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", filepath.Base(path), err)
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	// pgx 的 ConnString 保留原解析输入，因此显式重写 URL，不能依赖修改 cfg.Database 后的 ConnString。
	result := struct {
		Database string `json:"database"`
	}{name}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	succeeded = true
	return nil
}
