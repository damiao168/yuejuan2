package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Use the shipped deployment setting and a fresh NOINHERIT application login:
// superuser-backed handler tests cannot catch a missing runtime role switch.
func TestComposeApplicationBootstrapAndLoginWithPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "infra", "docker-compose", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && (key == "EDUGRADE_ENV" || key == "EDUGRADE_POSTGRES_TENANT_RLS") {
			t.Setenv(key, value)
		}
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Postgres.TenantRLSEnabled {
		t.Fatal("Compose application login requires the tenant connector in local deployments too")
	}
	adminDB := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, adminDB)
	roleName := "e2e_compose_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	rolePassword := "Compose" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedRole := pgx.Identifier{roleName}.Sanitize()
	if _, err := adminDB.Exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '` + rolePassword + `'`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := adminDB.Exec(`DROP ROLE ` + quotedRole); err != nil {
			t.Errorf("remove isolated application login: %v", err)
		}
	}()
	if _, err := adminDB.Exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole); err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := adminDB.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	appDSN, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	appDSN.Path = "/" + databaseName
	appDSN.User = url.UserPassword(roleName, rolePassword)
	cfg.Postgres.DSN = appDSN.String()
	appDB, closeDB, err := database.OpenPostgres(cfg.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var runtimeRole, loginRole string
	if err := appDB.QueryRow(`SELECT current_user, session_user`).Scan(&runtimeRole, &loginRole); err != nil || runtimeRole != "edugrade_tenant_runtime" || loginRole != roleName {
		t.Fatalf("application role switch: current=%q session=%q err=%v", runtimeRole, loginRole, err)
	}
	input := auth.BootstrapAdminInput{Username: "fresh_compose_admin", Password: "Compose bootstrap regression 2026!"}
	ctx := database.WithTenantMaintenance(context.Background())
	created, err := auth.BootstrapInitialAdmin(ctx, auth.NewPostgresStore(appDB), input)
	if err != nil || created.TenantCode != "platform" || created.RoleCode != "platform_admin" {
		t.Fatalf("bootstrap using application login: result=%#v err=%v", created, err)
	}
	if _, err := auth.BootstrapInitialAdmin(ctx, auth.NewPostgresStore(appDB), input); !errors.Is(err, auth.ErrBootstrapAlreadyCompleted) {
		t.Fatalf("repeated bootstrap must not replace the administrator: %v", err)
	}
	router := e2ePostgresRouter(appDB)
	token := e2eLoginWithTenant(t, router, "platform", input.Username, input.Password)
	e2eGetJSON(t, router, "/api/v1/auth/me", token, http.StatusOK)
	sessionCookie, deviceCookie := e2eBrowserLogin(t, router, "platform", input.Username, input.Password, nil)
	if sessionCookie == nil || deviceCookie == nil {
		t.Fatal("fresh application login did not persist browser session and device cookies")
	}
}
