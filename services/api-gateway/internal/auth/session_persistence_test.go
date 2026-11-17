package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPostgresSessionLookupHonorsLockAndCredentialVersion(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	reauthenticatedAt := now.Add(-time.Minute)
	failure := errors.New("session database unavailable")
	for _, tc := range []struct {
		name, status            string
		reauth                  bool
		readErr, touchErr, want error
	}{
		{name: "locked cannot authenticate", want: ErrUnauthenticated},
		{name: "locked allowed for reauthentication", status: "active", reauth: true},
		{name: "disabled still denied in reauthentication", status: "disabled", reauth: true, want: ErrUnauthenticated},
		{name: "storage read failure", readErr: failure, want: failure},
		{name: "last seen refresh failure keeps verified identity", status: "active", touchErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := authBatch49Step{kind: "query", fragment: "AND ($3 OR s.locked_at IS NULL)", args: []any{"session-hash", now, tc.reauth}, columns: 16, err: tc.readErr}
			if tc.status != "" {
				query.rows = [][]driver.Value{{"actor", "tenant", "S001", "login", "王老师", tc.status, false, "public_device", int64(1), reauthenticatedAt, "medium", "step_up", "risk-v2", "{teacher}", "{review.task.read}", []byte(`{"teacher":{"assigned":true}}`)}}
			}
			steps := []authBatch49Step{query}
			if tc.status == "active" {
				steps = append(steps, authBatch49Step{kind: "exec", fragment: "AND last_seen_at < $3", args: []any{"session-hash", now, now.Add(-5 * time.Minute)}, err: tc.touchErr})
			}
			store, script := openAuthBatch49Store(t, steps)
			load := store.FindUserBySession
			if tc.reauth {
				load = store.FindUserBySessionForReauthentication
			}
			got, err := load(context.Background(), "session-hash", now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if !reflect.DeepEqual(got, User{}) {
					t.Fatalf("denied lookup leaked identity: %+v", got)
				}
			} else if got.ID != "actor" || got.TenantID != "tenant" || got.CurrentSessionType != "public_device" ||
				!got.ReauthenticatedAt.Equal(reauthenticatedAt) || got.CurrentAuthLevel != 1 || got.CurrentRiskAction != "step_up" ||
				!reflect.DeepEqual(got.Roles, []string{"teacher"}) || !reflect.DeepEqual(got.Permissions, []string{"review.task.read"}) ||
				!reflect.DeepEqual(got.DataScope, map[string]any{"teacher": map[string]any{"assigned": true}}) {
				t.Fatalf("identity lost session scope or metadata: %+v", got)
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresSessionCreateSendsCredentialVersionAndBoundsRiskScore(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	for _, stale := range []bool{false, true} {
		name := "verified credential still current"
		if stale {
			name = "password changed during login"
		}
		t.Run(name, func(t *testing.T) {
			step := authBatch49Step{
				kind: "query", fragment: "security_epoch=$10::bigint",
				args:    []any{"tenant", "actor", "token-hash", "public_device", "room-pc", "教室电脑", "ua-hash", "192.0.2.0/24", expires, int64(7), RiskLevelLow, RiskActionAllow, 100, now, "policy-1", RiskEvidenceLimited, "wechat"},
				columns: 6,
			}
			if !stale {
				step.rows = [][]driver.Value{{"session", "public_device", "教室电脑", now, now, expires}}
			}
			store, script := openAuthBatch49Store(t, []authBatch49Step{step})
			got, err := store.CreateSession(context.Background(), CreateSessionInput{
				TenantID: "tenant", UserID: "actor", TokenHash: "token-hash", SessionType: "public_device", DeviceID: "room-pc",
				DeviceName: "教室电脑", UserAgentHash: "ua-hash", IPPrefix: "192.0.2.0/24", ExpiresAt: expires, SecurityEpoch: 7,
				RiskScore: 150, RiskEvaluatedAt: now, RiskPolicyVersion: " policy-1 ", AuthMethod: "wechat",
			})
			if stale {
				if !errors.Is(err, ErrInvalidCredentials) || got != (DeviceSession{}) {
					t.Fatalf("stale credential created a session: %+v, error=%v", got, err)
				}
			} else {
				want := DeviceSession{ID: "session", SessionType: "public_device", DeviceName: "教室电脑", CreatedAt: now, LastSeenAt: now, ExpiresAt: expires, Current: true}
				if err != nil || got != want {
					t.Fatalf("session = %+v, error=%v, want %+v", got, err, want)
				}
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresListSessionsKeepsCurrentDeviceAndReturnsReadErrors(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	failure := errors.New("device list storage failure")
	current := []driver.Value{"current", "public_device", "教室电脑", now, now, expires, true}
	other := []driver.Value{"other", "personal_device", "办公室电脑", now, now, expires, false}
	for _, tc := range []struct {
		name              string
		rows              [][]driver.Value
		queryErr, rowsErr error
	}{
		{name: "current and other device", rows: [][]driver.Value{current, other}},
		{name: "query failure", queryErr: failure},
		{name: "stream failure after first device", rows: [][]driver.Value{current}, rowsErr: failure},
		{name: "malformed device row", rows: [][]driver.Value{{"invalid", "", "", nil, now, expires, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, script := openAuthBatch49Store(t, []authBatch49Step{{
				kind: "query", fragment: "AND expires_at > $3", args: []any{"tenant", "actor", now, "current-hash"},
				columns: 7, rows: tc.rows, err: tc.queryErr, rowsErr: tc.rowsErr,
			}})
			got, err := store.ListSessions(context.Background(), "tenant", "actor", "current-hash", now)
			switch tc.name {
			case "current and other device":
				if err != nil || len(got) != 2 || got[0].ID != "current" || !got[0].Current || got[1].ID != "other" || got[1].Current || !got[1].ExpiresAt.Equal(expires) {
					t.Fatalf("sessions = %+v, error=%v", got, err)
				}
			case "stream failure after first device":
				if !errors.Is(err, failure) || len(got) != 1 || got[0].ID != "current" {
					t.Fatalf("stream result = %+v, error=%v", got, err)
				}
			case "query failure":
				if !errors.Is(err, failure) || got != nil {
					t.Fatalf("query result = %+v, error=%v", got, err)
				}
			default:
				if err == nil || got != nil {
					t.Fatalf("invalid row accepted: %+v, error=%v", got, err)
				}
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresPasswordChangeRevokesDevicesOnlyAfterCredentialCompareAndSwap(t *testing.T) {
	failure := errors.New("credential write unavailable")
	for _, tc := range []struct {
		name     string
		affected int64
		failAt   int
		want     error
	}{
		{"compare and swap then revoke", 1, -1, nil},
		{"stale old password", 0, -1, nil},
		{"password update fails", 1, 0, failure},
		{"session update fails", 1, 1, failure},
		{"trusted device update rolls back password and sessions", 1, 2, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []authBatch49Step{
				{kind: "exec", fragment: "AND password_hash=$3", args: []any{"tenant", "actor", "old-hash", "new-hash"}},
				{kind: "exec", fragment: "revoke_reason='password_changed'", args: []any{"tenant", "actor"}},
				{kind: "exec", fragment: "UPDATE auth_trusted_device", args: []any{"tenant", "actor"}},
			}
			if tc.failAt >= 0 {
				steps = steps[:tc.failAt+1]
				steps[tc.failAt].err = failure
			} else if tc.affected == 0 {
				steps = steps[:1]
			}
			script := &authBatch49Script{steps: steps}
			db := sql.OpenDB(passwordChangeConnector{authBatch49Connector{script}, tc.affected})
			t.Cleanup(func() { _ = db.Close() })
			store := NewPostgresStore(db)
			got, count, err := store.UpdatePasswordAndRevokeSessions(context.Background(), "tenant", "actor", "old-hash", "new-hash")
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && tc.affected == 1 {
				if !got || count != 1 {
					t.Fatalf("successful update = %v, revoked=%d", got, count)
				}
				assertAuthBatch49Script(t, script, "commit")
			} else {
				if got || count != 0 {
					t.Fatalf("failed update = %v, revoked=%d", got, count)
				}
				assertAuthBatch49Script(t, script, "rollback")
			}
		})
	}
}

// 脚本原有的写入固定影响一行。这里仅覆盖凭据比较失败时的零行更新。
type passwordChangeConnector struct {
	authBatch49Connector
	affected int64
}

func (c passwordChangeConnector) Connect(context.Context) (driver.Conn, error) {
	return &passwordChangeConn{authBatch49Conn{c.script}, c.affected}, nil
}

type passwordChangeConn struct {
	authBatch49Conn
	affected int64
}

func (c passwordChangeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	result, err := c.authBatch49Conn.ExecContext(ctx, query, args)
	if c.script.next == 1 && err == nil {
		return driver.RowsAffected(c.affected), nil
	}
	return result, err
}
