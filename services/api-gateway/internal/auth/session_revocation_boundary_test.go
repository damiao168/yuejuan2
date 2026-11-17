package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type sessionBoundaryConnector struct {
	authBatch49Connector
	fragments   []string
	affected    int64
	affectedErr error
}

func (c sessionBoundaryConnector) Connect(context.Context) (driver.Conn, error) {
	return &sessionBoundaryConnection{authBatch49Conn: authBatch49Conn{c.script}, options: c}, nil
}

type sessionBoundaryConnection struct {
	authBatch49Conn
	options sessionBoundaryConnector
}

func (c *sessionBoundaryConnection) check(query string) error {
	for _, fragment := range c.options.fragments {
		if !strings.Contains(query, fragment) {
			c.script.err = fmt.Errorf("session SQL omitted %q", fragment)
			return c.script.err
		}
	}
	return nil
}

func (c *sessionBoundaryConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.check(query); err != nil {
		return nil, err
	}
	return c.authBatch49Conn.QueryContext(ctx, query, args)
}

func (c *sessionBoundaryConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.check(query); err != nil {
		return nil, err
	}
	_, err := c.authBatch49Conn.ExecContext(ctx, query, args)
	return sessionBoundaryResult{affected: c.options.affected, err: c.options.affectedErr}, err
}

type sessionBoundaryResult struct {
	affected int64
	err      error
}

func (sessionBoundaryResult) LastInsertId() (int64, error) { return 0, nil }
func (r sessionBoundaryResult) RowsAffected() (int64, error) {
	return r.affected, r.err
}

func sessionBoundaryStore(t *testing.T, steps []authBatch49Step, fragments []string, affected int64, affectedErr error) (*PostgresStore, *authBatch49Script) {
	t.Helper()
	script := &authBatch49Script{steps: steps}
	db := sql.OpenDB(sessionBoundaryConnector{
		authBatch49Connector: authBatch49Connector{script}, fragments: fragments, affected: affected, affectedErr: affectedErr,
	})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func TestPostgresSessionLookupKeepsExpiryRevocationAndCredentialFilters(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	for _, reauthenticate := range []bool{false, true} {
		t.Run(fmt.Sprintf("reauthentication=%t", reauthenticate), func(t *testing.T) {
			// 重新认证只放宽锁屏条件，过期、撤销和旧安全版本仍由同一查询拒绝。
			fragments := []string{
				"s.expires_at > $2", "s.revoked_at IS NULL", "s.security_epoch = u.security_epoch",
				"t.status = 'active'", "t.deleted_at IS NULL", "u.deleted_at IS NULL", "AND ($3 OR s.locked_at IS NULL)",
			}
			store, script := sessionBoundaryStore(t, []authBatch49Step{{
				kind: "query", fragment: "WHERE s.token_hash = $1", args: []any{"invalidated-session", now, reauthenticate}, columns: 16,
			}}, fragments, 0, nil)
			load := store.FindUserBySession
			if reauthenticate {
				load = store.FindUserBySessionForReauthentication
			}
			got, err := load(t.Context(), "invalidated-session", now)
			if !errors.Is(err, ErrUnauthenticated) || got.ID != "" || got.TenantID != "" || got.Permissions != nil {
				t.Fatalf("invalid session returned identity=%+v, error=%v", got, err)
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresSessionLockAndReauthenticationRequireCurrentScopedIdentity(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	started := now.Add(-time.Minute)
	failure := errors.New("session update unavailable")
	countFailure := errors.New("session affected row count unavailable")
	for _, unlock := range []bool{false, true} {
		name := "lock"
		if unlock {
			name = "reauthenticate"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name               string
				affected           int64
				writeErr, countErr error
			}{
				{name: "current session updated", affected: 1},
				{name: "revoked stale or foreign session cannot update"},
				{name: "write failure", writeErr: failure},
				{name: "unavailable affected count", countErr: countFailure},
			} {
				t.Run(tc.name, func(t *testing.T) {
					fragments := []string{"s.tenant_id = $1::uuid", "s.user_id = $2::uuid", "s.token_hash = $3",
						"s.revoked_at IS NULL", "s.expires_at > $4", "u.security_epoch = s.security_epoch", "u.status = 'active'",
						"t.status = 'active'", "u.tenant_id = s.tenant_id", "u.id = s.user_id"}
					args := []any{"tenant", "actor", "session-hash", now}
					fragment := "SET locked_at = COALESCE(locked_at, $4)"
					if unlock {
						args = append(args, started)
						fragment = "SET reauthenticated_at = $4, locked_at = NULL"
						fragments = append(fragments, "AND (s.locked_at IS NULL OR s.locked_at <= $5)")
					} else {
						fragments = append(fragments, "s.session_type = 'public_device'")
					}
					store, script := sessionBoundaryStore(t, []authBatch49Step{{
						kind: "exec", fragment: fragment, args: args, err: tc.writeErr,
					}}, fragments, tc.affected, tc.countErr)
					var updated bool
					var err error
					if unlock {
						updated, err = store.MarkSessionReauthenticated(t.Context(), "tenant", "actor", "session-hash", started, now)
					} else {
						updated, err = store.LockSession(t.Context(), "tenant", "actor", "session-hash", now)
					}
					wantErr := tc.writeErr
					if wantErr == nil {
						wantErr = tc.countErr
					}
					if !errors.Is(err, wantErr) || updated != (tc.affected == 1 && tc.writeErr == nil) {
						t.Fatalf("session mutation updated=%t, error=%v; affected=%d, want error %v", updated, err, tc.affected, wantErr)
					}
					assertAuthBatch49Script(t, script, "")
				})
			}
		})
	}
}

func TestPostgresRevokeAllSessionsKeepsTrustedDeviceRevocationInTransaction(t *testing.T) {
	failure := errors.New("revocation storage unavailable")
	for _, tc := range []struct {
		name     string
		failAt   int
		affected int64
	}{
		{name: "sessions and devices revoked together", failAt: -1, affected: 3},
		{name: "no remaining sessions still revokes trusted devices", failAt: -1},
		{name: "session write fails", failAt: 0},
		{name: "trusted device failure rolls back session revocation", failAt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []authBatch49Step{
				{kind: "exec", fragment: "revoke_reason = NULLIF($3, '')", args: []any{"tenant", "actor", "operator_revoked"}},
				{kind: "exec", fragment: "UPDATE auth_trusted_device", args: []any{"tenant", "actor"}},
			}
			if tc.failAt >= 0 {
				steps = steps[:tc.failAt+1]
				steps[tc.failAt].err = failure
			}
			store, script := sessionBoundaryStore(t, steps, nil, tc.affected, nil)
			script.requireTransaction = true
			count, err := store.RevokeAllSessions(t.Context(), "tenant", "actor", "operator_revoked")
			if tc.failAt >= 0 {
				if !errors.Is(err, failure) || count != 0 {
					t.Fatalf("failed revocation count=%d, error=%v", count, err)
				}
				assertAuthBatch49Script(t, script, "rollback")
			} else {
				if err != nil || count != int(tc.affected) {
					t.Fatalf("revocation count=%d, error=%v; want count=%d", count, err, tc.affected)
				}
				assertAuthBatch49Script(t, script, "commit")
			}
		})
	}
}

func TestPostgresLoginPasswordReadRejectsInactiveOrForeignIdentity(t *testing.T) {
	failure := errors.New("credential read unavailable")
	for _, tc := range []struct {
		name              string
		rows              [][]driver.Value
		queryErr, wantErr error
	}{
		{name: "active tenant account", rows: [][]driver.Value{{"stored-hash"}}},
		{name: "inactive missing or foreign account", wantErr: ErrInvalidCredentials},
		{name: "database error", queryErr: failure, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, script := sessionBoundaryStore(t, []authBatch49Step{{
				kind: "query", fragment: "SELECT u.password_hash", args: []any{"tenant", "actor"}, columns: 1, rows: tc.rows, err: tc.queryErr,
			}}, []string{"u.tenant_id = $1::uuid", "u.id = $2::uuid", "u.status = 'active'", "u.deleted_at IS NULL", "t.status = 'active'", "t.deleted_at IS NULL"}, 0, nil)
			got, err := store.FindPasswordHash(t.Context(), "tenant", "actor")
			wantHash := ""
			if tc.wantErr == nil {
				wantHash = "stored-hash"
			}
			if !errors.Is(err, tc.wantErr) || got != wantHash {
				t.Fatalf("password read = %q, %v; want %q, %v", got, err, wantHash, tc.wantErr)
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresSuccessfulLoginCannotOverwriteAChangedPassword(t *testing.T) {
	writeFailure := errors.New("login write unavailable")
	countFailure := errors.New("login affected count unavailable")
	for _, tc := range []struct {
		name                        string
		affected                    int64
		writeErr, countErr, wantErr error
	}{
		{name: "verified password still current", affected: 1},
		{name: "password changed or account disabled while authenticating", wantErr: ErrInvalidCredentials},
		{name: "unexpected affected count", affected: 2, wantErr: ErrInvalidCredentials},
		{name: "write failure", writeErr: writeFailure, wantErr: writeFailure},
		{name: "affected count unavailable", countErr: countFailure, wantErr: countFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 替换旧密码哈希之前必须再次比较验证过的原值，避免覆盖并发改密。
			store, script := sessionBoundaryStore(t, []authBatch49Step{{
				kind: "exec", fragment: "SET last_login_at = now()", args: []any{"tenant", "actor", "verified-hash", "upgraded-hash"}, err: tc.writeErr,
			}}, []string{"tenant_id = $1::uuid AND id = $2::uuid AND password_hash = $3", "status = 'active' AND deleted_at IS NULL",
				"password_hash = CASE WHEN $4 <> '' AND password_hash = $3 THEN $4 ELSE password_hash END"}, tc.affected, tc.countErr)
			if err := store.RecordSuccessfulLogin(t.Context(), "tenant", "actor", "verified-hash", "upgraded-hash"); !errors.Is(err, tc.wantErr) {
				t.Fatalf("login record error=%v; want %v", err, tc.wantErr)
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}
