package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const mfaTestSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func mfaTestMasterKey() string {
	return base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
}

func TestMFACipherRequiresIndependent256BitKeyAndBindsOwner(t *testing.T) {
	for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := newMFACipher(key); !errors.Is(err, ErrMFAUnavailable) {
			t.Fatal("invalid key accepted")
		}
	}
	cipher, err := newMFACipher(mfaTestMasterKey())
	if err != nil {
		t.Fatal(err)
	}
	record := TOTPRecord{ID: "credential-1", TenantID: "tenant-1", UserID: "user-1"}
	if err = cipher.encrypt(mfaTestSecret, &record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record.Ciphertext), mfaTestSecret) {
		t.Fatal("plaintext secret persisted")
	}
	if secret, err := cipher.decrypt(record); err != nil || secret != mfaTestSecret {
		t.Fatal("round trip failed")
	}
	for _, field := range []string{"tenant", "user", "credential", "ciphertext", "nonce"} {
		t.Run(field, func(t *testing.T) {
			copy := record
			switch field {
			case "tenant":
				copy.TenantID = "other"
			case "user":
				copy.UserID = "other"
			case "credential":
				copy.ID = "other"
			case "ciphertext":
				copy.Ciphertext = append([]byte(nil), record.Ciphertext...)
				copy.Ciphertext[0] ^= 1
			case "nonce":
				copy.Nonce = nil
			}
			if _, err := cipher.decrypt(copy); !errors.Is(err, ErrMFAUnavailable) {
				t.Fatal("tampering or cross-owner substitution accepted")
			}
		})
	}
}

func TestTOTPMatchesRFC6238WithBoundedSkew(t *testing.T) {
	// RFC 6238 附录 B 的 SHA-1 测试向量，按配置缩减为六位数字。
	for _, vector := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		step, err := matchTOTPStep(vector.code, mfaTestSecret, time.Unix(vector.unix, 0))
		if err != nil || step != vector.unix/30 {
			t.Fatalf("RFC vector at %d failed: step=%d err=%v", vector.unix, step, err)
		}
	}
	if step, err := matchTOTPStep("287082", mfaTestSecret, time.Unix(89, 0)); err != nil || step != 1 {
		t.Fatal("previous step skew must work")
	}
	for _, code := range []string{"287082", " 287082", "2870820", "１２３４５６", "12345a"} {
		if _, err := matchTOTPStep(code, mfaTestSecret, time.Unix(120, 0)); !errors.Is(err, ErrMFAInvalid) {
			t.Fatal("expired or malformed code accepted")
		}
	}
}

func TestMFARecoveryCodesAreRandomHashedAndNormalize(t *testing.T) {
	codes, hashes, err := newMFARecoveryCodes()
	if err != nil || len(codes) != 10 || len(hashes) != 10 {
		t.Fatal("generation failed")
	}
	seen := map[string]bool{}
	for i, code := range codes {
		hash, err := recoveryCodeHash(strings.ToUpper(code))
		if err != nil || hash != hashes[i] || seen[hash] || strings.Contains(hash, code) {
			t.Fatal("bad recovery code")
		}
		seen[hash] = true
	}
	if _, err := recoveryCodeHash("00000000-00000000-00000000-nothex00"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("malformed code accepted")
	}
}

func mfaMemoryFixture(t *testing.T) (*MemoryStore, TOTPRecord, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	store := NewMemoryStore()
	user := UserWithPassword{User: User{ID: "user-1", TenantID: "tenant-1", TenantCode: "demo", Username: "teacher", Status: "active"}, PasswordHash: "current-hash", SecurityEpoch: 3}
	store.AddUser(user)
	for _, hash := range []string{"session-1", "session-2"} {
		store.sessions[hash] = memorySession{ID: hash, TenantID: user.TenantID, UserID: user.ID, SecurityEpoch: 3, ExpiresAt: now.Add(time.Hour)}
	}
	record := TOTPRecord{ID: "credential-1", TenantID: user.TenantID, UserID: user.ID, ExpectedPasswordHash: user.PasswordHash, EnrollmentSessionHash: "session-1", PendingExpiresAt: now.Add(MFAEnrollmentTTL), LastUsedStep: -1}
	cipher, _ := newMFACipher(mfaTestMasterKey())
	if err := cipher.encrypt(mfaTestSecret, &record); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePendingTOTP(context.Background(), record, now); err != nil {
		t.Fatal(err)
	}
	return store, record, now
}

func mfaEnabledFixture(t *testing.T) (*MemoryStore, TOTPRecord, time.Time, []string) {
	t.Helper()
	store, record, now := mfaMemoryFixture(t)
	_, hashes, err := newMFARecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ConfirmTOTP(context.Background(), record, "session-1", now.Unix()/30, hashes, now); err != nil {
		t.Fatal(err)
	}
	record, err = store.FindTOTP(context.Background(), record.TenantID, record.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return store, record, now, hashes
}

func mfaCreateTestChallenge(t *testing.T, store *MemoryStore, record TOTPRecord, now time.Time, hash, session, operation string) MFAProof {
	t.Helper()
	challenge := MFAChallenge{TokenHash: hash, TenantID: record.TenantID, UserID: record.UserID, CredentialID: record.ID, SessionHash: session, ExpectedPasswordHash: "current-hash", Operation: operation, ExpiresAt: now.Add(MFAChallengeTTL)}
	if err := store.CreateMFAChallenge(context.Background(), challenge, now); err != nil {
		t.Fatal(err)
	}
	return MFAProof{TenantID: record.TenantID, UserID: record.UserID, CredentialID: record.ID, SessionHash: session, ChallengeHash: hash, Now: now, Step: now.Unix()/30 + 1}
}

func TestMFAEnrollmentRequiresSameLiveSessionAndEpoch(t *testing.T) {
	for _, mode := range []string{"other-session", "expired", "locked", "revoked", "epoch", "attempts", "wrong-id"} {
		t.Run(mode, func(t *testing.T) {
			assertMFAEnrollmentRejectsInvalidSession(t, mode)
		})
	}
	store, record, now := mfaMemoryFixture(t)
	record.ExpectedPasswordHash = "old-hash"
	if err := store.SavePendingTOTP(context.Background(), record, now); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("stale password accepted")
	}
}

func TestMFAChallengeRejectsScopeExpiryReplayAndPasswordOnlyPromotion(t *testing.T) {
	for _, mode := range []string{"tenant", "user", "session", "credential", "expired", "revoked", "epoch", "attempts", "totp-replay"} {
		t.Run(mode, func(t *testing.T) {
			store, record, now, _ := mfaEnabledFixture(t)
			proof := mfaCreateTestChallenge(t, store, record, now, "challenge", "session-1", MFAOperationRotateRecovery)
			switch mode {
			case "tenant":
				proof.TenantID = "other"
			case "user":
				proof.UserID = "other"
			case "session":
				proof.SessionHash = "session-2"
			case "credential":
				proof.CredentialID = "other"
			case "expired":
				proof.Now = now.Add(MFAChallengeTTL)
			case "revoked":
				delete(store.sessions, proof.SessionHash)
			case "epoch":
				for key, user := range store.users {
					user.SecurityEpoch++
					store.users[key] = user
				}
			case "attempts":
				for range 5 {
					_ = store.RecordMFAChallengeFailure(context.Background(), proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash)
				}
			case "totp-replay":
				proof.Step = record.LastUsedStep
			}
			if err := store.VerifyMFAChallenge(context.Background(), proof); !errors.Is(err, ErrMFAInvalid) {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}

func TestMFAConcurrentOTPAndCommandConsumptionExactlyOnce(t *testing.T) {
	store, record, now, _ := mfaEnabledFixture(t)
	proofs := []MFAProof{mfaCreateTestChallenge(t, store, record, now, "rotate", "session-1", MFAOperationRotateRecovery), mfaCreateTestChallenge(t, store, record, now, "disable", "session-2", MFAOperationDisable)}
	var verified atomic.Int32
	var wg sync.WaitGroup
	var winner MFAProof
	var mu sync.Mutex
	for _, proof := range proofs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.VerifyMFAChallenge(context.Background(), proof); err == nil {
				verified.Add(1)
				mu.Lock()
				winner = proof
				mu.Unlock()
			} else if !errors.Is(err, ErrMFAInvalid) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if verified.Load() != 1 {
		t.Fatal("same time step consumed twice")
	}
	// 若禁用操作先完成，则刻意在新时间步选择轮换授权。
	if winner.ChallengeHash != "rotate" {
		winner = proofs[0]
		winner.Step++
		if err := store.VerifyMFAChallenge(context.Background(), winner); err != nil {
			t.Fatal(err)
		}
	}
	_, hashes, _ := newMFARecoveryCodes()
	var finished atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.FinishMFACommand(context.Background(), winner, MFAOperationRotateRecovery, hashes); err == nil {
				finished.Add(1)
			} else if !errors.Is(err, ErrMFAInvalid) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if finished.Load() != 1 {
		t.Fatal("grant consumed twice")
	}
	user, err := store.FindUserBySession(context.Background(), "session-1", now)
	if err != nil || user.CurrentAuthLevel != 1 {
		t.Fatal("command proof promoted entire session")
	}
}

func TestMFARecoveryCanOnlyDisableAndIsConsumedOnce(t *testing.T) {
	store, record, now, hashes := mfaEnabledFixture(t)
	proof := mfaCreateTestChallenge(t, store, record, now, "rotate", "session-1", MFAOperationRotateRecovery)
	proof.RecoveryHash = hashes[0]
	if err := store.VerifyMFAChallenge(context.Background(), proof); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("recovery granted rotation")
	}
	proof = mfaCreateTestChallenge(t, store, record, now, "disable", "session-1", MFAOperationDisable)
	proof.RecoveryHash = hashes[0]
	if err := store.VerifyMFAChallenge(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	second := mfaCreateTestChallenge(t, store, record, now, "second", "session-2", MFAOperationDisable)
	second.RecoveryHash = hashes[0]
	if err := store.VerifyMFAChallenge(context.Background(), second); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("recovery code reused")
	}
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationRotateRecovery, nil); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("wrong command consumed grant")
	}
	store.devices["device"] = memoryDeviceBinding{TenantID: record.TenantID, UserID: record.UserID}
	if err := store.FinishMFACommand(context.Background(), proof, MFAOperationDisable, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.sessions) != 0 || len(store.devices) != 0 || len(store.mfaChallenges) != 0 {
		t.Fatal("disable must revoke all sessions/devices/grants")
	}
	if _, err := store.FindTOTP(context.Background(), record.TenantID, record.UserID); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("credential survived disable")
	}
}

func TestMFAUnavailablePublicAndServiceSessionsFailClosed(t *testing.T) {
	store := NewMemoryStore()
	for _, mode := range []string{"disabled", "invalid-key", "public", "service", "service-type"} {
		t.Run(mode, func(t *testing.T) {
			options := HandlerOptions{MFAEnabled: true, MFAMasterKey: mfaTestMasterKey()}
			user := User{ID: "user", TenantID: "tenant", CurrentSessionType: SessionTypeStandard}
			want := http.StatusServiceUnavailable
			switch mode {
			case "disabled":
				options.MFAEnabled = false
			case "invalid-key":
				options.MFAMasterKey = ""
			case "public":
				user.CurrentSessionType = SessionTypePublicDevice
				want = http.StatusForbidden
			case "service":
				user.Roles = []string{"page_processing_worker"}
				want = http.StatusForbidden
			case "service-type":
				user.CurrentSessionType = SessionTypeService
				want = http.StatusForbidden
			}
			handler := NewHandler(store, time.Hour, options)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/totp/enroll", strings.NewReader(`{"password":"never-read"}`))
			req = req.WithContext(WithUser(req.Context(), user))
			response := httptest.NewRecorder()
			handler.EnrollTOTP(response, req)
			if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("wrong failure boundary: %d", response.Code)
			}
		})
	}
}

func TestMFAPasswordDoesNotClearOTPFailuresAndAuditPreservesRisk(t *testing.T) {
	store, record, _ := mfaMemoryFixture(t)
	hash, err := HashPassword("CurrentPrivatePassphrase")
	if err != nil {
		t.Fatal(err)
	}
	for key, stored := range store.users {
		stored.PasswordHash = hash
		store.users[key] = stored
	}
	user := User{ID: record.UserID, TenantID: record.TenantID, TenantCode: "demo", Username: "teacher", CurrentRiskLevel: RiskLevelHigh, CurrentRiskAction: RiskActionStepUp, RiskPolicyVersion: RiskPolicyVersionV1}
	h := NewHandler(store, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/step-up/start", nil)
	req = req.WithContext(WithUser(req.Context(), user))
	attempt := h.mfaAttempt(user, req)
	for range 3 {
		h.loginGuard.RegisterFailure(req.Context(), attempt, time.Now().UTC())
	}
	if _, ok := h.mfaPassword(httptest.NewRecorder(), req, user, "CurrentPrivatePassphrase"); !ok {
		t.Fatal("valid password failed before limit")
	}
	for range 2 {
		h.loginGuard.RegisterFailure(req.Context(), attempt, time.Now().UTC())
	}
	response := httptest.NewRecorder()
	if h.mfaGuard(response, req, user) || response.Code != http.StatusTooManyRequests {
		t.Fatal("correct password cleared accumulated OTP failures")
	}
	h.mfaAudit(req, user, "auth.mfa_enabled", "totp")
	events, err := store.ListAudits(req.Context(), user.TenantID, AuditFilter{ActorID: user.ID, Limit: 10, ScopeMode: "tenant"})
	if err != nil || len(events) != 1 || securityEventRisk(events[0].AfterValue) != "high" || events[0].AfterValue["risk_action"] != string(RiskActionStepUp) {
		t.Fatal("MFA audit implied reduced dynamic risk")
	}
}

func assertMFAEnrollmentRejectsInvalidSession(t *testing.T, mode string) {
	t.Helper()
	store, record, now := mfaMemoryFixture(t)
	session := "session-1"
	switch mode {
	case "other-session":
		session = "session-2"
	case "expired":
		now = now.Add(MFAEnrollmentTTL)
	case "locked":
		s := store.sessions[session]
		s.LockedAt = now
		store.sessions[session] = s
	case "revoked":
		delete(store.sessions, session)
	case "epoch":
		for key, user := range store.users {
			user.SecurityEpoch++
			store.users[key] = user
		}
	case "attempts":
		for range 5 {
			_ = store.RecordTOTPFailure(context.Background(), record.TenantID, record.UserID, record.ID)
		}
	case "wrong-id":
		record.ID = "other"
	}
	_, hashes, _ := newMFARecoveryCodes()
	if err := store.ConfirmTOTP(context.Background(), record, session, now.Unix()/30, hashes, now); !errors.Is(err, ErrMFAInvalid) {
		t.Fatal("invalid enrollment accepted")
	}
}
