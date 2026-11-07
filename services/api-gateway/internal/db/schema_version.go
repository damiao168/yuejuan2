package db

import (
	"context"
	"database/sql"
	"fmt"
)

// VerifySchemaVersion 拒绝缺迁移或来自其他结构版本的库。
// 使用编译时版本而非可覆盖的部署变量，避免旧镜像在删列迁移后被重新启动。
func VerifySchemaVersion(ctx context.Context, database *sql.DB) error {
	var actual string
	if err := database.QueryRowContext(ctx, `SELECT COALESCE(max(left(filename, 6)), '') FROM schema_migration`).Scan(&actual); err != nil {
		return fmt.Errorf("read database migration version before startup: %w", err)
	}
	return validateSchemaVersion(actual)
}

func validateSchemaVersion(actual string) error {
	if actual != RequiredSchemaVersion {
		return fmt.Errorf("database schema version %q does not match gateway %s; stop application services and complete the matching release migration before startup", actual, RequiredSchemaVersion)
	}
	return nil
}
