package server

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestStory060ProvisioningWithPostgresIsScopedAndIdempotent(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-060 provisioning PostgreSQL regression test")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)

	store := auth.NewPostgresStore(db)
	ctx := context.Background()
	tests := []story060ProvisionCase{
		{
			username:  "story060_provision_school_admin_test",
			roleCode:  "school_admin",
			scope:     "school",
			displayV1: "STORY-060 School Admin V1",
			displayV2: "STORY-060 School Admin V2",
		},
		{
			username:  "story060_provision_worker_test",
			roleCode:  "subjective_grading_worker",
			scope:     "service",
			displayV1: "STORY-060 Worker V1",
			displayV2: "STORY-060 Worker V2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.roleCode, func(t *testing.T) {
			assertStory060ProvisionCase(t, ctx, db, store, tt)
		})
	}
}

type story060ProvisionCase struct {
	username  string
	roleCode  string
	scope     string
	displayV1 string
	displayV2 string
}

// 第二次开通只更新同一用户及角色关系；服务范围和学校范围保持各自原值。
func assertStory060ProvisionCase(t *testing.T, ctx context.Context, db *sql.DB, store *auth.PostgresStore, tt story060ProvisionCase) {
	t.Helper()
	if err := store.ProvisionStory060User(ctx, tt.username, tt.displayV1, tt.roleCode, "hash-v1"); err != nil {
		t.Fatalf("first provisioning: %v", err)
	}
	if err := store.ProvisionStory060User(ctx, tt.username, tt.displayV2, tt.roleCode, "hash-v2"); err != nil {
		t.Fatalf("idempotent provisioning: %v", err)
	}

	var userCount, assignmentCount int
	var displayName, passwordHash, scope string
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), max(display_name), max(password_hash)
FROM app_user
WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
  AND username = $1
  AND status = 'active'
  AND deleted_at IS NULL
`, tt.username).Scan(&userCount, &displayName, &passwordHash); err != nil {
		t.Fatalf("query provisioned user: %v", err)
	}
	if userCount != 1 || displayName != tt.displayV2 || passwordHash != "hash-v2" {
		t.Fatalf("provisioned user was not updated idempotently: count=%d display=%q hash=%q", userCount, displayName, passwordHash)
	}

	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*), max(assignment.data_scope->>'scope')
FROM user_role assignment
JOIN app_user user_account
  ON user_account.tenant_id = assignment.tenant_id
 AND user_account.id = assignment.user_id
JOIN role
  ON role.tenant_id = assignment.tenant_id
 AND role.id = assignment.role_id
WHERE assignment.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
  AND user_account.username = $1
  AND role.code = $2
  AND assignment.deleted_at IS NULL
`, tt.username, tt.roleCode).Scan(&assignmentCount, &scope); err != nil {
		t.Fatalf("query provisioned role assignment: %v", err)
	}
	if assignmentCount != 1 || scope != tt.scope {
		t.Fatalf("unexpected role assignment: count=%d scope=%q, want count=1 scope=%q", assignmentCount, scope, tt.scope)
	}
}
