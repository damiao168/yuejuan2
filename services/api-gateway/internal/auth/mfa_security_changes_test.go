package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMFASecurityChangeAllowlist(t *testing.T) {
	for _, test := range []struct{ event, method, operation string }{
		{"auth.mfa_enabled", "totp", "mfa.totp.enable"},
		{"auth.mfa_recovery_rotated", "totp", MFAOperationRotateRecovery},
		{"auth.mfa_recovery_used", "recovery_code", MFAOperationDisable},
		{"auth.mfa_disabled", "totp", MFAOperationDisable},
		{"auth.mfa_disabled", "recovery_code", MFAOperationDisable},
	} {
		t.Run(test.event+"/"+test.method, func(t *testing.T) {
			change, err := newMFASecurityChange("tenant-1", "user-1", test.event, test.method, time.Now())
			if err != nil || change.Operation != test.operation || change.SchemaVersion != 1 || !change.NotificationRequired || change.ActorUserID != change.SubjectUserID || change.OccurredAt.Location() != time.UTC {
				t.Fatal("security fact contract invalid")
			}
			if _, err = uuid.Parse(change.EventID); err != nil {
				t.Fatal("event ID is not an opaque UUID")
			}
		})
	}
	for _, test := range []struct{ event, method string }{
		{"auth.login_failed", "totp"}, {"auth.mfa_enabled", "recovery_code"},
		{"auth.mfa_recovery_rotated", "recovery_code"}, {"auth.mfa_recovery_used", "totp"},
		{"auth.mfa_disabled", "password"}, {"auth.mfa_disabled", ""},
	} {
		if _, err := newMFASecurityChange("tenant-1", "user-1", test.event, test.method, time.Now()); !errors.Is(err, ErrMFAInvalid) {
			t.Fatal("unsupported security fact accepted")
		}
	}
	if _, err := newMFASecurityChange("", "user-1", "auth.mfa_enabled", "totp", time.Now()); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("missing subject scope accepted")
	}
	if _, err := newMFASecurityChange("tenant-1", "user-1", "auth.mfa_enabled", "totp", time.Time{}); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("missing occurrence time accepted")
	}
}

func assertMemoryMFANotificationFacts(t *testing.T, store *MemoryStore, expected ...string) {
	t.Helper()
	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.mfaNotificationIntents) != len(expected) {
		t.Fatalf("notification intent count: got %d want %d", len(store.mfaNotificationIntents), len(expected))
	}
	seen := map[string]bool{}
	for index, intent := range store.mfaNotificationIntents {
		change := intent.Change
		if intent.Status != notificationAwaitingChannel || change.EventType != expected[index] || !change.NotificationRequired || seen[change.EventID] {
			t.Fatal("incorrect or duplicated pending security fact")
		}
		seen[change.EventID] = true
		assertMFASecurityFactHasSingleMatchingAudit(t, store.audits, change)
		payload, err := json.Marshal(change)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{mfaTestSecret, "current-hash", "session-1", "challenge", "code_hash", "ciphertext", "user_agent", "ip_address", "qr_code", "password", "token_hash"} {
			if strings.Contains(string(payload), forbidden) {
				t.Fatal("credential or source data entered notification payload")
			}
		}
	}
}

func TestMFANotificationEnrollmentFactOnlyAfterConfirmation(t *testing.T) {
	store, record, now := mfaMemoryFixture(t)
	assertMemoryMFANotificationFacts(t, store)
	_, hashes, _ := newMFARecoveryCodes()
	if err := store.ConfirmTOTP(context.Background(), record, "session-2", now.Unix()/30, hashes, now); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("invalid confirmation accepted")
	}
	assertMemoryMFANotificationFacts(t, store)
	if err := store.ConfirmTOTP(context.Background(), record, "session-1", now.Unix()/30, hashes, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmTOTP(context.Background(), record, "session-1", now.Unix()/30, hashes, now); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("confirmation replay accepted")
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled")
}

func TestMFANotificationRotationRequiresExecutionNotJustVerification(t *testing.T) {
	store, record, now, hashes := mfaEnabledFixture(t)
	proof := mfaCreateTestChallenge(t, store, record, now, "challenge", "session-1", MFAOperationRotateRecovery)
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationRotateRecovery, hashes); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("unverified command accepted")
	}
	if err := store.VerifyMFAChallenge(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled")
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationRotateRecovery, hashes); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationRotateRecovery, hashes); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("rotation replay accepted")
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled", "auth.mfa_recovery_rotated")
}

func TestMFANotificationRecoveryUseIsRecordedEvenWithoutDisable(t *testing.T) {
	store, record, now, hashes := mfaEnabledFixture(t)
	proof := mfaCreateTestChallenge(t, store, record, now, "challenge", "session-1", MFAOperationDisable)
	proof.RecoveryHash = hashes[0]
	if err := store.VerifyMFAChallenge(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled", "auth.mfa_recovery_used")
	if !store.mfaRecovery[record.ID][hashes[0]] {
		t.Fatal("recovery-use fact did not accompany consumption")
	}
	if err := store.VerifyMFAChallenge(context.Background(), proof); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("recovery replay accepted")
	}
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationDisable, nil); err != nil {
		t.Fatal(err)
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled", "auth.mfa_recovery_used", "auth.mfa_disabled")
	if _, err := store.FindTOTP(context.Background(), record.TenantID, record.UserID); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("disabled factor remains")
	}
	if store.mfaNotificationIntents[2].Change.AuthMethod != "recovery_code" {
		t.Fatal("disable attributed to wrong proof method")
	}
}

func TestMFANotificationRejectedProofsNeverEmitFacts(t *testing.T) {
	for _, mode := range []string{"tenant", "expired", "locked"} {
		t.Run(mode, func(t *testing.T) {
			store, record, now, hashes := mfaEnabledFixture(t)
			proof := mfaCreateTestChallenge(t, store, record, now, "challenge", "session-1", MFAOperationDisable)
			proof.RecoveryHash = hashes[0]
			switch mode {
			case "tenant":
				proof.TenantID = "other-tenant"
			case "expired":
				proof.Now = now.Add(MFAChallengeTTL)
			case "locked":
				session := store.sessions[proof.SessionHash]
				session.LockedAt = now
				store.sessions[proof.SessionHash] = session
			}
			if err := store.VerifyMFAChallenge(context.Background(), proof); !errors.Is(err, ErrMFAInvalid) {
				t.Fatal("invalid proof accepted")
			}
			assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled")
			if store.mfaRecovery[record.ID][hashes[0]] {
				t.Fatal("rejected proof consumed recovery code")
			}
		})
	}
}

func TestMFANotificationConcurrentConsumptionEmitsOneFact(t *testing.T) {
	store, record, now, hashes := mfaEnabledFixture(t)
	proof := mfaCreateTestChallenge(t, store, record, now, "challenge", "session-1", MFAOperationDisable)
	proof.RecoveryHash = hashes[0]
	run := func(command func() error) {
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() { results <- command() })
		}
		wg.Wait()
		close(results)
		winners := 0
		for err := range results {
			if err == nil {
				winners++
			} else if !errors.Is(err, ErrMFAInvalid) {
				t.Fatal(err)
			}
		}
		if winners != 1 {
			t.Fatal("expected one credential/command winner")
		}
	}
	run(func() error { return store.VerifyMFAChallenge(context.Background(), proof) })
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled", "auth.mfa_recovery_used")
	run(func() error { return store.FinishMFACommand(context.Background(), proof, MFAOperationDisable, nil) })
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled", "auth.mfa_recovery_used", "auth.mfa_disabled")
}

func TestMFASecurityAuditMetadataCannotEnterNotificationPayload(t *testing.T) {
	store, record, now := mfaMemoryFixture(t)
	user := User{TenantID: record.TenantID, ID: record.UserID, CurrentRiskLevel: RiskLevelHigh, CurrentRiskAction: RiskActionStepUp, RiskPolicyVersion: RiskPolicyVersionV1}
	ctx := WithUser(context.Background(), user)
	ctx = NewHandler(store, time.Hour).mfaSecurityContext(ctx, "203.0.113.42", "spoofable-device-description")
	_, hashes, _ := newMFARecoveryCodes()
	if err := store.ConfirmTOTP(ctx, record, "session-1", now.Unix()/30, hashes, now); err != nil {
		t.Fatal(err)
	}
	assertMemoryMFANotificationFacts(t, store, "auth.mfa_enabled")
	audit := store.audits[0]
	if audit.IPAddress != "203.0.113.42" || audit.UserAgent != "spoofable-device-description" || securityEventRisk(audit.AfterValue) != "high" || audit.AfterValue["risk_action"] != string(RiskActionStepUp) {
		t.Fatal("existing audit source hints/risk evidence lost or downgraded")
	}
	payload, _ := json.Marshal(store.mfaNotificationIntents[0].Change)
	if strings.Contains(string(payload), "203.0.113.42") || strings.Contains(string(payload), audit.UserAgent) {
		t.Fatal("raw audit metadata leaked into notification")
	}
}

// 调用方仍持有内存存储读锁；通知与审计必须来自同一次安全变更，不能在这里重新取锁。
func assertMFASecurityFactHasSingleMatchingAudit(t *testing.T, audits []AuditRecord, change MFASecurityChange) {
	t.Helper()
	matchingAudits := 0
	for _, audit := range audits {
		if audit.AfterValue["event_id"] == change.EventID {
			matchingAudits++
			if audit.Action != change.EventType || audit.ActorID != change.SubjectUserID || audit.TenantID != change.TenantID || !audit.CreatedAt.Equal(change.OccurredAt) {
				t.Fatal("audit and notification fact disagree")
			}
		}
	}
	if matchingAudits != 1 {
		t.Fatal("fact missing its single atomic audit")
	}
}
