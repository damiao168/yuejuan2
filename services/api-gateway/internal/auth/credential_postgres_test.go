package auth

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func TestPostgresCredentialIssuanceChecksAuthorityBeforeReplacingToken(t *testing.T) {
	expires := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	failure := errors.New("credential write unavailable")
	actor := User{ID: "administrator", TenantID: "tenant", Roles: []string{"school_admin"}}

	for _, recovery := range []bool{false, true} {
		name, table, status := "activation", "auth_activation", "invited"
		if recovery {
			name, table, status = "recovery", "auth_recovery", "active"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name, target, role, school, failAt string
				want                               error
			}{
				{name: "same school teacher", target: "target", role: "teacher", school: "local"},
				{name: "account missing in tenant", failAt: "missing", want: ErrManagedUserNotFound},
				{name: "account read failure", failAt: "account", want: failure},
				{name: "self-service forbidden", target: actor.ID, want: ErrUserStatusForbidden},
				{name: "role read failure", target: "target", failAt: "role query", want: failure},
				{name: "role stream fails after teacher row", target: "target", role: "teacher", failAt: "role stream", want: failure},
				{name: "malformed role row", target: "target", failAt: "role scan"},
				{name: "higher role forbidden", target: "target", role: "tenant_admin", school: "local", want: ErrUserStatusForbidden},
				{name: "navigation school is not managed", target: "target", role: "teacher", school: "foreign", want: ErrOrganizationScope},
				{name: "old token revocation failure", target: "target", role: "teacher", school: "local", failAt: "revoke", want: failure},
				{name: "new token insert rolls back revocation", target: "target", role: "teacher", school: "local", failAt: "insert", want: failure},
			} {
				t.Run(tc.name, func(t *testing.T) {
					requestedID := tc.target
					if requestedID == "" {
						requestedID = "missing"
					}
					account := authBatch49Step{kind: "query", fragment: "u.status='" + status + "'", args: []any{actor.TenantID, requestedID}, columns: 5}
					if recovery {
						account.columns++
					}
					if tc.target != "" {
						row := []driver.Value{tc.target, "王老师", "S001", tc.school, "13800138000"}
						if recovery {
							row = append(row, int64(4))
						}
						account.rows = [][]driver.Value{row}
					}
					if tc.failAt == "account" {
						account.err = failure
					}
					steps := []authBatch49Step{account}
					if tc.target != "" && tc.target != actor.ID {
						role := authBatch49Step{kind: "query", fragment: "FROM user_role ur", args: []any{actor.TenantID, tc.target}, columns: 1, rows: [][]driver.Value{{tc.role}}}
						switch tc.failAt {
						case "role query":
							role.err = failure
						case "role stream":
							role.rowsErr = failure
						case "role scan":
							role.rows = [][]driver.Value{{nil}}
						}
						steps = append(steps, role)
					}
					if tc.school == "local" && tc.role == "teacher" {
						revoke := authBatch49Step{kind: "exec", fragment: "UPDATE " + table, args: []any{actor.TenantID, tc.target}}
						if tc.failAt == "revoke" {
							revoke.err = failure
						}
						steps = append(steps, revoke)
						if revoke.err == nil {
							args := []any{actor.TenantID, tc.target, "new-token-hash", expires}
							if recovery {
								args = append(args, actor.ID, int64(4))
							}
							insert := authBatch49Step{kind: "exec", fragment: "INSERT INTO " + table, args: args}
							if tc.failAt == "insert" {
								insert.err = failure
							}
							steps = append(steps, insert)
						}
					}
					store, script := openAuthBatch49Store(t, steps)
					// 导航能看到外校班级时，也只能给实际管理学校中的账号发放凭据。
					scope := AccessScope{TenantID: actor.TenantID, SchoolIDs: []string{"local", "foreign"}, schoolWide: true, schoolManagementIDs: []string{"local"}}
					var err error
					if recovery {
						var got RecoveryPreview
						got, err = store.CreateRecovery(context.Background(), actor, scope, requestedID, "new-token-hash", expires)
						want := RecoveryPreview{DisplayName: "王老师", TenantCode: "S001", PhoneMasked: "138****8000", ExpiresAt: expires}
						if tc.want != nil || tc.failAt == "role scan" {
							want = RecoveryPreview{}
						}
						if got != want {
							t.Fatalf("recovery preview = %+v, want %+v", got, want)
						}
					} else {
						var got ActivationPreview
						got, err = store.CreateActivation(context.Background(), actor, scope, requestedID, "new-token-hash", expires)
						want := ActivationPreview{DisplayName: "王老师", TenantCode: "S001", SchoolID: "local", PhoneMasked: "138****8000", ExpiresAt: expires}
						if tc.want != nil || tc.failAt == "role scan" {
							want = ActivationPreview{}
						}
						if got != want {
							t.Fatalf("activation preview = %+v, want %+v", got, want)
						}
					}
					if tc.failAt == "role scan" {
						if err == nil {
							t.Fatal("malformed role row was accepted")
						}
					} else if !errors.Is(err, tc.want) {
						t.Fatalf("error = %v, want %v", err, tc.want)
					}
					end := "commit"
					if tc.want != nil || tc.failAt == "role scan" {
						end = "rollback"
					}
					assertAuthBatch49Script(t, script, end)
				})
			}
		})
	}
}

func TestPostgresCredentialPreviewRejectsInvalidTokenAndMasksContact(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	readFailure := errors.New("credential lookup failed")
	for _, tc := range []struct {
		name     string
		found    bool
		queryErr error
	}{
		{name: "valid privileged account", found: true},
		{name: "expired or consumed"},
		{name: "storage failure", queryErr: readFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activation := authBatch49Step{kind: "query", fragment: "activation.used_at IS NULL", args: []any{"activation-hash", now}, columns: 6, err: tc.queryErr}
			recovery := authBatch49Step{kind: "query", fragment: "recovery.security_epoch=u.security_epoch", args: []any{"recovery-hash", now}, columns: 5, err: tc.queryErr}
			wantActivation, wantRecovery := ActivationPreview{}, RecoveryPreview{}
			wantActivationErr, wantRecoveryErr := tc.queryErr, tc.queryErr
			switch {
			case tc.found:
				activation.rows = [][]driver.Value{{"校管", "S001", "school", "13800138000", expires, int64(12)}}
				recovery.rows = [][]driver.Value{{"校管", "S001", "13800138000", expires, int64(12)}}
				wantActivation = ActivationPreview{DisplayName: "校管", TenantCode: "S001", SchoolID: "school", PhoneMasked: "138****8000", ExpiresAt: expires, PasswordMinLength: 12}
				wantRecovery = RecoveryPreview{DisplayName: "校管", TenantCode: "S001", PhoneMasked: "138****8000", ExpiresAt: expires, PasswordMinLength: 12}
			case tc.queryErr == nil:
				wantActivationErr, wantRecoveryErr = ErrActivationInvalid, ErrRecoveryInvalid
			}
			store, script := openAuthBatch49Store(t, []authBatch49Step{activation, recovery})
			if got, err := store.FindActivation(context.Background(), "activation-hash", now); got != wantActivation || !errors.Is(err, wantActivationErr) {
				t.Fatalf("activation = %+v, %v, want %+v, %v", got, err, wantActivation, wantActivationErr)
			}
			if got, err := store.FindRecovery(context.Background(), "recovery-hash", now); got != wantRecovery || !errors.Is(err, wantRecoveryErr) {
				t.Fatalf("recovery = %+v, %v, want %+v, %v", got, err, wantRecovery, wantRecoveryErr)
			}
			assertAuthBatch49Script(t, script, "")
		})
	}
}

func TestPostgresCredentialCompletionKeepsPasswordAndRevocationsTogether(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	failure := errors.New("credential transaction write failed")
	for _, recovery := range []bool{false, true} {
		name, table, revokeReason, invalid := "activation", "auth_activation", "account_activated", ErrActivationInvalid
		if recovery {
			name, table, revokeReason, invalid = "recovery", "auth_recovery", "credential_recovery", ErrRecoveryInvalid
		}
		t.Run(name, func(t *testing.T) {
			lastWrite := 3
			if recovery {
				lastWrite = 4
			}
			for _, tc := range []struct {
				name       string
				failedStep int
				want       error
			}{
				{"commits together", -1, nil},
				{"invalid or replayed token", -1, invalid},
				{"token lock read fails", 0, failure},
				{"password update fails", 1, failure},
				{"token consumption fails", 2, failure},
				{"last revocation write fails", lastWrite, failure},
			} {
				t.Run(tc.name, func(t *testing.T) {
					steps := []authBatch49Step{
						{kind: "query", fragment: "FOR UPDATE OF " + name + ", u", args: []any{"token-hash", now}, columns: 2, rows: [][]driver.Value{{"owner-tenant", "owner"}}},
						{kind: "exec", fragment: "security_epoch=security_epoch+1", args: []any{"owner-tenant", "owner", "new-password-hash", now}},
						{kind: "exec", fragment: "UPDATE " + table, args: []any{"token-hash", now}},
						{kind: "exec", fragment: "revoke_reason='" + revokeReason + "'", args: []any{"owner-tenant", "owner", now}},
					}
					if recovery {
						steps = append(steps, authBatch49Step{kind: "exec", fragment: "UPDATE auth_trusted_device", args: []any{"owner-tenant", "owner", now}})
					}
					switch {
					case tc.want == invalid:
						steps = steps[:1]
						steps[0].rows = nil
					case tc.failedStep >= 0:
						steps = steps[:tc.failedStep+1]
						steps[tc.failedStep].err = failure
					}
					store, script := openAuthBatch49Store(t, steps)
					// 入口尚未认证。所有写入只能归属数据库锁住的令牌账号。
					var tenantID, userID string
					var err error
					if recovery {
						got, resultErr := store.CompleteRecovery(context.Background(), "token-hash", "new-password-hash", now)
						tenantID, userID, err = got.TenantID, got.UserID, resultErr
					} else {
						got, resultErr := store.ActivateUser(context.Background(), "token-hash", "new-password-hash", now)
						tenantID, userID, err = got.TenantID, got.UserID, resultErr
					}
					if !errors.Is(err, tc.want) {
						t.Fatalf("error = %v, want %v", err, tc.want)
					}
					if tc.want == nil {
						if tenantID != "owner-tenant" || userID != "owner" {
							t.Fatalf("successful result tenant=%q user=%q", tenantID, userID)
						}
						assertAuthBatch49Script(t, script, "commit")
					} else {
						if tenantID != "" || userID != "" {
							t.Fatalf("failed result leaked tenant=%q user=%q", tenantID, userID)
						}
						assertAuthBatch49Script(t, script, "rollback")
					}
				})
			}
		})
	}
}
