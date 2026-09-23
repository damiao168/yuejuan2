package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestAuthorizationContractPostgres checks the final database state, including
// grants revoked or repaired by later migrations. It intentionally does not
// infer authorization from SQL source text.
func TestAuthorizationContractPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for PostgreSQL authorization contract test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	adminConfig.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to PostgreSQL administration database: %v", err)
	}
	defer admin.Close(context.Background())

	databaseName := "edugrade_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedName := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quotedName); err != nil {
		t.Fatalf("create isolated authorization database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		cleanupAdmin, err := pgx.ConnectConfig(cleanupCtx, adminConfig)
		if err != nil {
			t.Errorf("connect to clean up authorization database: %v", err)
			return
		}
		defer cleanupAdmin.Close(context.Background())
		if _, err := cleanupAdmin.Exec(cleanupCtx, "DROP DATABASE "+quotedName+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated authorization database: %v", err)
		}
	})

	testConfig := adminConfig.Copy()
	testConfig.Database = databaseName
	testConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgx.ConnectConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect to isolated authorization database: %v", err)
	}
	defer db.Close(context.Background())

	migrationFiles, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(migrationFiles) == 0 {
		t.Fatalf("find migrations: %v (count=%d)", err, len(migrationFiles))
	}
	slices.Sort(migrationFiles)
	for _, file := range migrationFiles {
		sqlText, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read migration %s: %v", file, err)
		}
		if _, err := db.Exec(ctx, string(sqlText)); err != nil {
			t.Fatalf("apply migration %s: %v", filepath.Base(file), err)
		}
	}

	contractRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "authorization", "role-matrix.json"))
	if err != nil {
		t.Fatalf("read authorization contract: %v", err)
	}
	contract := map[string]authorizationRoleContract{}
	if err := json.Unmarshal(contractRaw, &contract); err != nil {
		t.Fatalf("decode authorization contract: %v", err)
	}
	for _, tenantCode := range []string{"platform", "demo"} {
		actual := readAuthorizationMatrix(t, ctx, db, tenantCode)
		if differences := compareAuthorizationMatrix(contract, actual); len(differences) > 0 {
			t.Errorf("%s tenant RBAC differs from role-matrix.json:\n%s", tenantCode, strings.Join(differences, "\n"))
		}
	}
}

type databaseRoleContract struct {
	Scope       string
	Permissions []string
}

func readAuthorizationMatrix(t *testing.T, ctx context.Context, db *pgx.Conn, tenantCode string) map[string]databaseRoleContract {
	t.Helper()
	rows, err := db.Query(ctx, `
SELECT r.code, r.scope_type, COALESCE(array_agg(p.code ORDER BY p.code)
  FILTER (WHERE p.code IS NOT NULL), ARRAY[]::text[])
FROM tenant t
JOIN role r ON r.tenant_id=t.id AND r.deleted_at IS NULL
LEFT JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
LEFT JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL
WHERE t.code=$1 AND t.deleted_at IS NULL
GROUP BY r.code, r.scope_type`, tenantCode)
	if err != nil {
		t.Fatalf("read %s RBAC matrix: %v", tenantCode, err)
	}
	defer rows.Close()
	actual := map[string]databaseRoleContract{}
	for rows.Next() {
		var code string
		var role databaseRoleContract
		if err := rows.Scan(&code, &role.Scope, &role.Permissions); err != nil {
			t.Fatalf("scan %s RBAC matrix: %v", tenantCode, err)
		}
		actual[code] = role
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s RBAC matrix: %v", tenantCode, err)
	}
	if len(actual) == 0 {
		t.Fatalf("%s has no active roles after migrations", tenantCode)
	}
	return actual
}

func compareAuthorizationMatrix(contract map[string]authorizationRoleContract, actual map[string]databaseRoleContract) []string {
	differences := []string{}
	for code, expected := range contract {
		role, ok := actual[code]
		if !ok {
			differences = append(differences, fmt.Sprintf("missing database role %s", code))
			continue
		}
		if role.Scope != expected.Scope {
			differences = append(differences, fmt.Sprintf("%s scope: contract=%s database=%s", code, expected.Scope, role.Scope))
		}
		want := append([]string(nil), expected.Permissions...)
		slices.Sort(want)
		for _, permission := range want {
			if !slices.Contains(role.Permissions, permission) {
				differences = append(differences, fmt.Sprintf("%s missing database grant %s", code, permission))
			}
		}
		for _, permission := range role.Permissions {
			if !slices.Contains(want, permission) {
				differences = append(differences, fmt.Sprintf("%s has undocumented database grant %s", code, permission))
			}
		}
	}
	for code := range actual {
		if _, ok := contract[code]; !ok {
			differences = append(differences, fmt.Sprintf("undocumented database role %s", code))
		}
	}
	slices.Sort(differences)
	return differences
}

func TestCompareAuthorizationMatrixDetectsBothDirections(t *testing.T) {
	contract := map[string]authorizationRoleContract{
		"worker": {Scope: "service", Permissions: []string{"orchestrator:manage"}},
	}
	actual := map[string]databaseRoleContract{
		"worker": {Scope: "tenant", Permissions: []string{"file:manage"}},
		"extra":  {Scope: "tenant"},
	}
	differences := strings.Join(compareAuthorizationMatrix(contract, actual), "\n")
	for _, expected := range []string{"worker scope", "worker missing database grant", "worker has undocumented database grant", "undocumented database role extra"} {
		if !strings.Contains(differences, expected) {
			t.Fatalf("missing %q from differences: %s", expected, differences)
		}
	}
}
