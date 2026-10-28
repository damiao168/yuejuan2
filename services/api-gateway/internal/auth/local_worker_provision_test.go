package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

type localWorkerCreationConnector struct {
	authBatch49Connector
	testing  *testing.T
	password string
}

func (connector localWorkerCreationConnector) Connect(context.Context) (driver.Conn, error) {
	return &localWorkerCreationConn{authBatch49Conn: &authBatch49Conn{script: connector.script}, testing: connector.testing, password: connector.password}, nil
}

type localWorkerCreationConn struct {
	*authBatch49Conn
	testing  *testing.T
	password string
}

func (connection *localWorkerCreationConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "INSERT INTO app_user") {
		hash, ok := args[2].Value.(string)
		if !ok || hash == connection.password || !CheckPassword(hash, connection.password) {
			connection.testing.Fatal("new account must receive only a valid password hash")
		}
		// 哈希盐每次随机；只在核对其验证结果后接受该一个参数，其他SQL与参数仍按脚本严格匹配。
		connection.script.steps[connection.script.next].args[2] = hash
	}
	return connection.authBatch49Conn.QueryContext(ctx, query, args)
}

func TestLocalWorkerCreationCommitsOnlyServiceRoleAndCredentialFreeAudit(t *testing.T) {
	password := strings.Repeat("a", 32)
	steps := localWorkerSteps()
	steps = append(steps,
		authBatch49Step{kind: "query", fragment: "FROM app_user", args: []any{PlatformTenantID, "ocr_worker"}, columns: 5},
		authBatch49Step{kind: "query", fragment: "INSERT INTO app_user", args: []any{PlatformTenantID, "ocr_worker", "salted-hash"}, columns: 1, rows: [][]driver.Value{{"new-worker"}}},
		authBatch49Step{kind: "exec", fragment: `'{"scope":"service"}'::jsonb`, args: []any{PlatformTenantID, "new-worker", "worker-role"}},
		authBatch49Step{kind: "exec", fragment: "auth.local_worker_created", args: []any{PlatformTenantID, "new-worker", auditJSON(map[string]any{"username": "ocr_worker", "role_code": "page_processing_worker"})}})
	script := &authBatch49Script{steps: steps}
	database := sql.OpenDB(localWorkerCreationConnector{authBatch49Connector: authBatch49Connector{script}, testing: t, password: password})
	defer database.Close()
	if err := NewPostgresStore(database).ProvisionLocalWorkers(t.Context(), []LocalWorkerInput{{"ocr_worker", password}}); err != nil {
		t.Fatal(err)
	}
	if script.err != nil || script.end != "commit" || script.next != len(script.steps) {
		t.Fatalf("atomic creation: %+v", script)
	}
}

func localWorkerSteps() []authBatch49Step {
	return []authBatch49Step{
		{kind: "query", fragment: "FOR UPDATE", args: []any{PlatformTenantID}, columns: 1, rows: [][]driver.Value{{PlatformTenantID}}},
		{kind: "query", fragment: "scope_type='service'", args: []any{PlatformTenantID}, columns: 1, rows: [][]driver.Value{{"worker-role"}}},
		{kind: "query", fragment: "FROM role_permission", args: []any{PlatformTenantID, "worker-role"}, columns: 1, rows: [][]driver.Value{{"file:manage"}, {"ocr:manage"}}},
	}
}

func TestLocalWorkersRejectUnexpectedPermissionsBeforeUserWrites(t *testing.T) {
	steps := localWorkerSteps()
	steps[2].rows = append(steps[2].rows, []driver.Value{"score:manage"})
	script := &authBatch49Script{steps: steps}
	database := sql.OpenDB(authBatch49Connector{script})
	defer database.Close()
	if err := NewPostgresStore(database).ProvisionLocalWorkers(t.Context(), []LocalWorkerInput{{"ocr_worker", strings.Repeat("a", 32)}}); err != ErrLocalWorkerProvision {
		t.Fatalf("unexpected privilege accepted: %v", err)
	}
	if script.err != nil || script.end != "rollback" {
		t.Fatalf("must rollback before user write: %+v", script)
	}
}

func TestLocalWorkerExistingIdentityMustBeRestrictedAndPasswordMatch(t *testing.T) {
	password := strings.Repeat("a", 32)
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name       string
		count      int
		restricted bool
		password   string
		pass       bool
	}{{"same worker", 1, true, password, true}, {"human role", 1, false, password, false}, {"extra role", 2, true, password, false}, {"wrong credentials", 1, true, strings.Repeat("b", 32), false}} {
		t.Run(scenario.name, func(t *testing.T) {
			steps := localWorkerSteps()
			steps = append(steps,
				authBatch49Step{kind: "query", fragment: "FROM app_user", args: []any{PlatformTenantID, "ocr_worker"}, columns: 5, rows: [][]driver.Value{{"worker-user", hash, "active", true, true}}},
				authBatch49Step{kind: "query", fragment: "COUNT(*)", args: []any{PlatformTenantID, "worker-user", "worker-role"}, columns: 2, rows: [][]driver.Value{{int64(scenario.count), scenario.restricted}}})
			script := &authBatch49Script{steps: steps}
			database := sql.OpenDB(authBatch49Connector{script})
			defer database.Close()
			err := NewPostgresStore(database).ProvisionLocalWorkers(t.Context(), []LocalWorkerInput{{"ocr_worker", scenario.password}})
			if scenario.pass && err != nil || !scenario.pass && !errors.Is(err, ErrLocalWorkerProvision) {
				t.Fatalf("identity boundary: %v", err)
			}
			expected := "rollback"
			if scenario.pass {
				expected = "commit"
			}
			if script.err != nil || script.end != expected {
				t.Fatalf("no reset or escalation allowed: %+v", script)
			}
		})
	}
}

func TestLocalWorkersRejectDuplicateAccountsBeforeDatabaseAccess(t *testing.T) {
	store := &PostgresStore{}
	if err := store.ProvisionLocalWorkers(t.Context(), []LocalWorkerInput{{"same_worker", strings.Repeat("a", 32)}, {"same_worker", strings.Repeat("b", 32)}}); err != ErrLocalWorkerProvision {
		t.Fatalf("duplicate accounts: %v", err)
	}
}
