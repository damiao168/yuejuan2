package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type authBatch49Step struct {
	kind, fragment         string
	args                   []any
	columns                int
	rows                   [][]driver.Value
	err, rowsErr, closeErr error
}

// SQL 脚本只核对本入口的参数、执行顺序和提交/回滚，不代替真实数据库的并发锁测试。
type authBatch49Script struct {
	steps                []authBatch49Step
	next, opened, closed int
	end                  string
	err                  error
}

func (s *authBatch49Script) take(kind, query string, args []driver.NamedValue) (authBatch49Step, error) {
	if s.next >= len(s.steps) {
		s.err = fmt.Errorf("unexpected %s: %s", kind, query)
		return authBatch49Step{}, s.err
	}
	step := s.steps[s.next]
	s.next++
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = arg.Value
	}
	if kind != step.kind || !strings.Contains(query, step.fragment) || !reflect.DeepEqual(values, step.args) {
		s.err = fmt.Errorf("step %d: %s %q args=%v, want %s %q args=%v", s.next, kind, query, values, step.kind, step.fragment, step.args)
		return step, s.err
	}
	return step, step.err
}

type authBatch49Connector struct{ script *authBatch49Script }

func (c authBatch49Connector) Connect(context.Context) (driver.Conn, error) {
	return &authBatch49Conn{c.script}, nil
}
func (c authBatch49Connector) Driver() driver.Driver { return c }
func (authBatch49Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type authBatch49Conn struct{ script *authBatch49Script }

func (*authBatch49Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*authBatch49Conn) Close() error                             { return nil }
func (*authBatch49Conn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (c *authBatch49Conn) Begin() (driver.Tx, error)              { return c, nil }
func (c *authBatch49Conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c, nil
}
func (c *authBatch49Conn) Commit() error   { c.script.end = "commit"; return nil }
func (c *authBatch49Conn) Rollback() error { c.script.end = "rollback"; return nil }
func (c *authBatch49Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c *authBatch49Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.script.opened++
	return &authBatch49Rows{script: c.script, step: step}, nil
}

type authBatch49Rows struct {
	script *authBatch49Script
	step   authBatch49Step
	next   int
}

func (r *authBatch49Rows) Columns() []string { return make([]string, r.step.columns) }
func (r *authBatch49Rows) Close() error      { r.script.closed++; return r.step.closeErr }
func (r *authBatch49Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		if r.step.rowsErr != nil {
			return r.step.rowsErr
		}
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

func openAuthBatch49Store(t *testing.T, steps []authBatch49Step) (*PostgresStore, *authBatch49Script) {
	t.Helper()
	script := &authBatch49Script{steps: steps}
	db := sql.OpenDB(authBatch49Connector{script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func assertAuthBatch49Script(t *testing.T, script *authBatch49Script, end string) {
	t.Helper()
	if script.err != nil || script.next != len(script.steps) || script.opened != script.closed || script.end != end {
		t.Fatalf("script error=%v steps=%d/%d rows=%d/%d end=%s, want %s", script.err, script.next, len(script.steps), script.closed, script.opened, script.end, end)
	}
}

func authBatch49AccountStep() authBatch49Step {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	return authBatch49Step{kind: "query", fragment: "FOR UPDATE", args: []any{"tenant", "target"}, columns: 10, rows: [][]driver.Value{{"target", "target-login", "target-name", "active", "school", "13800138000", "employee", nil, nil, now}}}
}

func authBatch49RoleStep(roles [][]driver.Value) authBatch49Step {
	return authBatch49Step{kind: "query", fragment: "ORDER BY r.code", args: []any{"tenant", "target"}, columns: 1, rows: roles}
}

func TestBatch49ManagedStatusRoleReadFailuresPreventAllWrites(t *testing.T) {
	failure := errors.New("role source failure")
	for _, tc := range []struct {
		name     string
		roleStep authBatch49Step
		want     error
	}{
		{"query failure", authBatch49Step{kind: "query", fragment: "ORDER BY r.code", args: []any{"tenant", "target"}, err: failure}, failure},
		{"later row failure", authBatch49Step{kind: "query", fragment: "ORDER BY r.code", args: []any{"tenant", "target"}, columns: 1, rows: [][]driver.Value{{"teacher"}}, rowsErr: failure}, failure},
		{"close failure", authBatch49Step{kind: "query", fragment: "ORDER BY r.code", args: []any{"tenant", "target"}, columns: 1, rows: [][]driver.Value{{"teacher"}}, closeErr: failure}, failure},
		{"invalid role row", authBatch49RoleStep([][]driver.Value{{nil}}), nil},
		{"empty role set", authBatch49RoleStep(nil), ErrUserStatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, script := openAuthBatch49Store(t, []authBatch49Step{authBatch49AccountStep(), tc.roleStep})
			got, previous, err := store.UpdateManagedUserStatus(context.Background(), User{ID: "actor", TenantID: "tenant", Roles: []string{"tenant_admin"}}, AccessScope{TenantID: "tenant", TenantWide: true}, "target", "disabled")
			if err == nil || got.ID != "" || previous != "" {
				t.Fatalf("failed role read returned account: %+v previous=%q error=%v", got, previous, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("lost role error: %v, want %v", err, tc.want)
			}
			assertAuthBatch49Script(t, script, "rollback")
		})
	}
}

func authBatch49DisableSteps(role string, activeAdmins int, writeErr error) []authBatch49Step {
	steps := []authBatch49Step{authBatch49AccountStep(), authBatch49RoleStep([][]driver.Value{{role}})}
	if role == "school_admin" {
		steps = append(steps,
			authBatch49Step{kind: "exec", fragment: "pg_advisory_xact_lock", args: []any{"tenant|school|school-admin-status"}},
			authBatch49Step{kind: "query", fragment: "count(DISTINCT u.id)", args: []any{"tenant", "school"}, columns: 1, rows: [][]driver.Value{{int64(activeAdmins)}}})
		if activeAdmins <= 1 {
			return steps
		}
	}
	return append(steps,
		authBatch49Step{kind: "exec", fragment: "UPDATE app_user", args: []any{"tenant", "target", "disabled"}},
		authBatch49Step{kind: "exec", fragment: "UPDATE auth_session", args: []any{"tenant", "target"}},
		authBatch49Step{kind: "exec", fragment: "UPDATE auth_trusted_device", args: []any{"tenant", "target"}, err: writeErr})
}

func TestBatch49ManagedStatusKeepsSchoolLockAndRevocationTransaction(t *testing.T) {
	writeFailure := errors.New("trusted-device update failed")
	for _, tc := range []struct {
		name, role     string
		admins         int
		writeErr, want error
	}{
		{"teacher disable", "teacher", 0, nil, nil},
		{"school admin disable", "school_admin", 2, nil, nil},
		{"last school admin", "school_admin", 1, nil, ErrLastSchoolAdmin},
		{"late revocation failure", "teacher", 0, writeFailure, writeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, script := openAuthBatch49Store(t, authBatch49DisableSteps(tc.role, tc.admins, tc.writeErr))
			got, previous, err := store.UpdateManagedUserStatus(context.Background(), User{ID: "actor", TenantID: "tenant", Roles: []string{"tenant_admin"}}, AccessScope{TenantID: "tenant", TenantWide: true}, "target", "disabled")
			if tc.want != nil {
				if !errors.Is(err, tc.want) || got.ID != "" || previous != "" {
					t.Fatalf("status failure result=%+v previous=%q error=%v", got, previous, err)
				}
				assertAuthBatch49Script(t, script, "rollback")
				return
			}
			if err != nil || got.Status != "disabled" || previous != "active" || !reflect.DeepEqual(got.Roles, []string{tc.role}) || got.PhoneMasked == "13800138000" {
				t.Fatalf("status result=%+v previous=%q error=%v", got, previous, err)
			}
			assertAuthBatch49Script(t, script, "commit")
		})
	}
}
