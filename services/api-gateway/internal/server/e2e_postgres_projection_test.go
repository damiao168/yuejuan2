package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func e2eProjectProcessingUntilCurrent(t *testing.T, db *sql.DB, store *processing.PostgresStore, tenantID, examID string) {
	t.Helper()
	projector := processing.NewProjector(store, processing.ProjectorOptions{
		Owner: "e2e-processing-projector-" + uuid.NewString(), PollInterval: time.Millisecond,
	})
	for attempt := 0; attempt < 50; attempt++ {
		var current bool
		if err := db.QueryRow(`
SELECT projected_version >= requested_version
FROM processing_projection_cursor
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
`, tenantID, examID).Scan(&current); err != nil {
			t.Fatalf("read processing projection cursor: %v", err)
		}
		if current {
			return
		}
		worked, err := projector.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("run durable processing projector: %v", err)
		}
		if !worked {
			t.Fatalf("processing projection remained dirty without a claimable job")
		}
	}
	t.Fatal("processing projection did not catch up to its requested source version")
}

func e2eAssertSchoolAdminRBACBackfill(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const legacyTenantCode = "rbac-backfill-legacy"
	if _, err := db.ExecContext(ctx, `
WITH new_id AS (SELECT gen_random_uuid() AS id)
INSERT INTO tenant (id, tenant_id, name, code, status)
SELECT id, id, 'RBAC Backfill Legacy Tenant', $1, 'active' FROM new_id
`, legacyTenantCode); err != nil {
		t.Fatalf("seed legacy RBAC tenant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO role (tenant_id, code, name, scope_type, description)
SELECT target.id, source.code, source.name, source.scope_type, source.description
FROM tenant target
JOIN role source
  ON source.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
 AND source.code = 'school_admin'
 AND source.deleted_at IS NULL
WHERE target.code = $1
`, legacyTenantCode); err != nil {
		t.Fatalf("seed legacy school administrator role: %v", err)
	}

	applyBackfill := func() {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000112_backfill_school_admin_rbac.sql"))
		if err != nil {
			t.Fatalf("read school administrator RBAC backfill migration: %v", err)
		}
		for _, statement := range e2eSplitSQLStatements(string(raw)) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("apply school administrator RBAC backfill: %v", err)
			}
		}
	}
	applyBackfill()

	if _, err := db.ExecContext(ctx, `
INSERT INTO permission (tenant_id, code, name, resource, action, description)
SELECT id, 'tenant-custom:manage', 'Tenant custom permission', 'tenant-custom', 'manage', 'must survive canonical backfill'
FROM tenant WHERE code = $1
`, legacyTenantCode); err != nil {
		t.Fatalf("seed tenant-specific permission: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO role_permission (tenant_id, role_id, permission_id)
SELECT role.tenant_id, role.id, permission.id
FROM role
JOIN permission ON permission.tenant_id = role.tenant_id
JOIN tenant ON tenant.id = role.tenant_id
WHERE tenant.code = $1 AND role.code = 'school_admin' AND permission.code = 'tenant-custom:manage'
`, legacyTenantCode); err != nil {
		t.Fatalf("seed tenant-specific school administrator grant: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin RBAC cleanup transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
WITH canonical_codes AS (
  SELECT source_permission.code
  FROM role source_role
  JOIN role_permission source_assignment
    ON source_assignment.tenant_id = source_role.tenant_id
   AND source_assignment.role_id = source_role.id
   AND source_assignment.deleted_at IS NULL
  JOIN permission source_permission
    ON source_permission.tenant_id = source_assignment.tenant_id
   AND source_permission.id = source_assignment.permission_id
   AND source_permission.deleted_at IS NULL
  WHERE source_role.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
    AND source_role.code = 'school_admin'
    AND source_role.deleted_at IS NULL
)
UPDATE role_permission assignment
SET deleted_at = now()
FROM role, permission, tenant
WHERE assignment.tenant_id = role.tenant_id
  AND assignment.role_id = role.id
  AND assignment.tenant_id = permission.tenant_id
  AND assignment.permission_id = permission.id
  AND tenant.id = assignment.tenant_id
  AND tenant.code = $1
  AND role.code = 'school_admin'
  AND permission.code IN (SELECT code FROM canonical_codes)
	`, legacyTenantCode); err != nil {
		t.Fatalf("soft-delete legacy RBAC rows: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE permission
SET deleted_at = now()
FROM tenant
WHERE permission.tenant_id = tenant.id
  AND tenant.code = $1
  AND permission.code = 'exam:manage'
`, legacyTenantCode); err != nil {
		t.Fatalf("soft-delete legacy exam permission: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit RBAC cleanup transaction: %v", err)
	}
	applyBackfill()

	var missingCanonical int
	if err := db.QueryRowContext(ctx, `
WITH canonical AS (
  SELECT source_permission.code
  FROM role source_role
  JOIN role_permission source_assignment
    ON source_assignment.tenant_id = source_role.tenant_id
   AND source_assignment.role_id = source_role.id
   AND source_assignment.deleted_at IS NULL
  JOIN permission source_permission
    ON source_permission.tenant_id = source_assignment.tenant_id
   AND source_permission.id = source_assignment.permission_id
   AND source_permission.deleted_at IS NULL
  WHERE source_role.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
    AND source_role.code = 'school_admin'
    AND source_role.deleted_at IS NULL
)
SELECT COUNT(*)
FROM canonical
JOIN tenant target_tenant ON target_tenant.code = $1
LEFT JOIN permission target_permission
  ON target_permission.tenant_id = target_tenant.id
 AND target_permission.code = canonical.code
 AND target_permission.deleted_at IS NULL
LEFT JOIN role target_role
  ON target_role.tenant_id = target_tenant.id
 AND target_role.code = 'school_admin'
 AND target_role.deleted_at IS NULL
LEFT JOIN role_permission target_assignment
  ON target_assignment.tenant_id = target_tenant.id
 AND target_assignment.role_id = target_role.id
 AND target_assignment.permission_id = target_permission.id
 AND target_assignment.deleted_at IS NULL
WHERE target_permission.id IS NULL OR target_assignment.id IS NULL
`, legacyTenantCode).Scan(&missingCanonical); err != nil {
		t.Fatalf("verify canonical school administrator RBAC: %v", err)
	}
	if missingCanonical != 0 {
		t.Fatalf("legacy school administrator is missing %d canonical permissions after backfill", missingCanonical)
	}

	var customGrantCount int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM tenant
JOIN role ON role.tenant_id = tenant.id AND role.code = 'school_admin' AND role.deleted_at IS NULL
JOIN permission ON permission.tenant_id = tenant.id AND permission.code = 'tenant-custom:manage' AND permission.deleted_at IS NULL
JOIN role_permission assignment
  ON assignment.tenant_id = tenant.id
 AND assignment.role_id = role.id
 AND assignment.permission_id = permission.id
 AND assignment.deleted_at IS NULL
WHERE tenant.code = $1
`, legacyTenantCode).Scan(&customGrantCount); err != nil {
		t.Fatalf("verify tenant-specific school administrator grant: %v", err)
	}
	if customGrantCount != 1 {
		t.Fatalf("tenant-specific school administrator grant must survive backfill, got %d rows", customGrantCount)
	}
}
