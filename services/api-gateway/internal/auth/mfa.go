package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

var (
	ErrMFAInvalid     = errors.New("MFA verification invalid or expired")
	ErrMFAConflict    = errors.New("MFA authenticator already enabled")
	ErrMFAUnavailable = errors.New("MFA unavailable")
)

const (
	MFAEnrollmentTTL           = 10 * time.Minute
	MFAChallengeTTL            = 5 * time.Minute
	MFAOperationDisable        = "mfa.disable"
	MFAOperationRotateRecovery = "mfa.recovery.rotate"
)

type TOTPRecord struct {
	ID, TenantID, UserID, EnrollmentSessionHash string
	ExpectedPasswordHash                        string `json:"-"`
	Ciphertext, Nonce                           []byte `json:"-"`
	SecurityEpoch                               int64
	PendingExpiresAt                            time.Time
	EnabledAt                                   *time.Time
	LastUsedStep                                int64
	FailedAttempts                              int
	RecoveryCodesRemaining                      int
}

type MFAChallenge struct {
	TokenHash, TenantID, UserID, CredentialID, SessionHash, Operation string
	SecurityEpoch                                                     int64
	ExpectedPasswordHash                                              string `json:"-"`
	Attempts                                                          int
	VerifiedMethod                                                    string
	VerifiedAt, ConsumedAt                                            *time.Time
	ExpiresAt                                                         time.Time
}

type MFAProof struct {
	TenantID, UserID, SessionHash, ChallengeHash, CredentialID, RecoveryHash string
	Step                                                                     int64
	Now                                                                      time.Time
}

// MFAStore mutations must serialize on the user/session/credential and commit
// OTP or recovery-code consumption atomically with the challenge transition.
// Successful enable, recovery-code use, rotation and disable also commit their
// redacted audit, semantic outbox fact and independent-notification intent.
type MFAStore interface {
	FindTOTP(context.Context, string, string) (TOTPRecord, error)
	SavePendingTOTP(context.Context, TOTPRecord, time.Time) error
	ConfirmTOTP(context.Context, TOTPRecord, string, int64, []string, time.Time) error
	RecordTOTPFailure(context.Context, string, string, string) error
	CreateMFAChallenge(context.Context, MFAChallenge, time.Time) error
	FindMFAChallenge(context.Context, string, string, string, string, time.Time) (MFAChallenge, error)
	RecordMFAChallengeFailure(context.Context, string, string, string, string) error
	VerifyMFAChallenge(context.Context, MFAProof) error
	FinishMFACommand(context.Context, MFAProof, string, []string) error
}

type mfaCipher struct{ aead cipher.AEAD }

func newMFACipher(masterKey string) (*mfaCipher, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterKey))
	if err != nil || len(key) != 32 {
		return nil, ErrMFAUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrMFAUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrMFAUnavailable
	}
	return &mfaCipher{aead: aead}, nil
}

// 将密文绑定到租户、用户及凭据记录，复制密文到另一条记录也无法通过解密校验。
func mfaAAD(record TOTPRecord) []byte {
	return []byte("edugrade:totp:v1:" + record.TenantID + ":" + record.UserID + ":" + record.ID)
}

func (c *mfaCipher) encrypt(secret string, record *TOTPRecord) error {
	if c == nil {
		return ErrMFAUnavailable
	}
	record.Nonce = make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(record.Nonce); err != nil {
		return err
	}
	record.Ciphertext = c.aead.Seal(nil, record.Nonce, []byte(secret), mfaAAD(*record))
	return nil
}

func (c *mfaCipher) decrypt(record TOTPRecord) (string, error) {
	if c == nil || len(record.Nonce) != c.aead.NonceSize() {
		return "", ErrMFAUnavailable
	}
	plaintext, err := c.aead.Open(nil, record.Nonce, record.Ciphertext, mfaAAD(record))
	if err != nil {
		return "", ErrMFAUnavailable
	}
	return string(plaintext), nil
}

// Use the reviewed RFC 4226 implementation, returning the exact matched step
// so concurrent requests cannot both consume a time-based code (RFC 6238 §5.2).
func matchTOTPStep(code, secret string, now time.Time) (int64, error) {
	if len(code) != 6 {
		return -1, ErrMFAInvalid
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return -1, ErrMFAInvalid
		}
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step < 0 {
			continue
		}
		valid, err := hotp.ValidateCustom(code, uint64(step), secret, hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return -1, ErrMFAUnavailable
		}
		if valid {
			return step, nil
		}
	}
	return -1, ErrMFAInvalid
}

func newMFARecoveryCodes() ([]string, []string, error) {
	codes, hashes := make([]string, 10), make([]string, 10)
	for index := range codes {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		value := hex.EncodeToString(raw[:])
		codes[index] = value[:8] + "-" + value[8:16] + "-" + value[16:24] + "-" + value[24:]
		hashes[index] = HashToken(value)
	}
	return codes, hashes, nil
}

func recoveryCodeHash(code string) (string, error) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(value) != 32 {
		return "", ErrMFAInvalid
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrMFAInvalid
	}
	return HashToken(value), nil
}

func validMFAOperation(operation string) bool {
	return operation == MFAOperationDisable || operation == MFAOperationRotateRecovery
}
