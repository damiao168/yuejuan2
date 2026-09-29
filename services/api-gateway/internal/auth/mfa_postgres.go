package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var _ MFAStore = (*PostgresStore)(nil)
var _ MFAStore = (*MemoryStore)(nil)

// All MFA mutations lock in this order: principal, session, credential,
// challenge. A stale password check or an in-flight revoked session cannot
// create/consume a grant. Keep this order when adding future command scopes.
func lockMFAPrincipal(ctx context.Context, tx *sql.Tx, tenantID, userID, sessionHash string, now time.Time) (int64, string, error) {
	var epoch int64
	var passwordHash string
	err := tx.QueryRowContext(ctx, `SELECT u.security_epoch, u.password_hash
 FROM app_user u JOIN tenant t ON t.id=u.tenant_id
 WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='active'
 AND u.deleted_at IS NULL AND t.status='active' AND t.deleted_at IS NULL FOR UPDATE OF u`, tenantID, userID).Scan(&epoch, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrMFAInvalid
	}
	if err != nil {
		return 0, "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM auth_session WHERE tenant_id=$1::uuid AND user_id=$2::uuid
 AND token_hash=$3 AND security_epoch=$4 AND revoked_at IS NULL AND locked_at IS NULL AND expires_at>$5::timestamptz FOR UPDATE`, tenantID, userID, sessionHash, epoch, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrMFAInvalid
	}
	return epoch, passwordHash, err
}

const totpSelect = `SELECT id::text,tenant_id::text,user_id::text,secret_ciphertext,secret_nonce,
 enrollment_session_hash,security_epoch,pending_expires_at,enabled_at,last_used_step,failed_attempts FROM auth_totp
 WHERE tenant_id=$1::uuid AND user_id=$2::uuid`

func scanTOTP(row interface{ Scan(...any) error }) (TOTPRecord, error) {
	var record TOTPRecord
	var enabled sql.NullTime
	err := row.Scan(&record.ID, &record.TenantID, &record.UserID, &record.Ciphertext, &record.Nonce, &record.EnrollmentSessionHash, &record.SecurityEpoch, &record.PendingExpiresAt, &enabled, &record.LastUsedStep, &record.FailedAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrMFAInvalid
	}
	if enabled.Valid {
		record.EnabledAt = timePointer(enabled.Time)
	}
	return record, err
}

func (s *PostgresStore) FindTOTP(ctx context.Context, tenantID, userID string) (TOTPRecord, error) {
	record, err := scanTOTP(s.db.QueryRowContext(ctx, totpSelect, tenantID, userID))
	if err != nil {
		return record, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM auth_mfa_recovery_code WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND credential_id=$3::uuid AND used_at IS NULL`, tenantID, userID, record.ID).Scan(&record.RecoveryCodesRemaining)
	return record, err
}

func (s *PostgresStore) SavePendingTOTP(ctx context.Context, record TOTPRecord, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	epoch, passwordHash, err := lockMFAPrincipal(ctx, tx, record.TenantID, record.UserID, record.EnrollmentSessionHash, now)
	if err != nil {
		return err
	}
	if passwordHash != record.ExpectedPasswordHash {
		return ErrMFAInvalid
	}
	old, err := scanTOTP(tx.QueryRowContext(ctx, totpSelect+` FOR UPDATE`, record.TenantID, record.UserID))
	if err != nil && !errors.Is(err, ErrMFAInvalid) {
		return err
	}
	if old.EnabledAt != nil {
		return ErrMFAConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_totp WHERE tenant_id=$1::uuid AND user_id=$2::uuid`, record.TenantID, record.UserID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_totp(id,tenant_id,user_id,secret_ciphertext,secret_nonce,enrollment_session_hash,security_epoch,pending_expires_at,last_used_step)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::timestamptz,-1)`, record.ID, record.TenantID, record.UserID, record.Ciphertext, record.Nonce, record.EnrollmentSessionHash, epoch, record.PendingExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func replaceMFARecoverySQL(ctx context.Context, tx *sql.Tx, record TOTPRecord, hashes []string) error {
	if len(hashes) != 10 {
		return ErrMFAInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_mfa_recovery_code WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND credential_id=$3::uuid`, record.TenantID, record.UserID, record.ID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_mfa_recovery_code(tenant_id,user_id,credential_id,code_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4)`, record.TenantID, record.UserID, record.ID, hash); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) ConfirmTOTP(ctx context.Context, input TOTPRecord, sessionHash string, step int64, hashes []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	epoch, _, err := lockMFAPrincipal(ctx, tx, input.TenantID, input.UserID, sessionHash, now)
	if err != nil {
		return err
	}
	record, err := scanTOTP(tx.QueryRowContext(ctx, totpSelect+` FOR UPDATE`, input.TenantID, input.UserID))
	if err != nil {
		return err
	}
	if record.ID != input.ID || record.EnabledAt != nil || record.EnrollmentSessionHash != sessionHash || record.SecurityEpoch != epoch || !record.PendingExpiresAt.After(now) || record.FailedAttempts >= 5 || step <= record.LastUsedStep {
		return ErrMFAInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE auth_totp SET enabled_at=$4::timestamptz,last_used_step=$5,updated_at=$4::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND id=$3::uuid`, record.TenantID, record.UserID, record.ID, now, step)
	if err != nil {
		return err
	}
	if err = replaceMFARecoverySQL(ctx, tx, record, hashes); err != nil {
		return err
	}
	if err = recordMFASecurityChangeSQL(ctx, tx, record.TenantID, record.UserID, "auth.mfa_enabled", "totp", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) RecordTOTPFailure(ctx context.Context, tenantID, userID, credentialID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_totp SET failed_attempts=LEAST(failed_attempts+1,5),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND id=$3::uuid AND enabled_at IS NULL`, tenantID, userID, credentialID)
	return err
}

func (s *PostgresStore) CreateMFAChallenge(ctx context.Context, challenge MFAChallenge, now time.Time) error {
	if !validMFAOperation(challenge.Operation) {
		return ErrMFAInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	epoch, passwordHash, err := lockMFAPrincipal(ctx, tx, challenge.TenantID, challenge.UserID, challenge.SessionHash, now)
	if err != nil {
		return err
	}
	if passwordHash != challenge.ExpectedPasswordHash {
		return ErrMFAInvalid
	}
	record, err := scanTOTP(tx.QueryRowContext(ctx, totpSelect+` FOR UPDATE`, challenge.TenantID, challenge.UserID))
	if err != nil {
		return err
	}
	if record.EnabledAt == nil || record.ID != challenge.CredentialID {
		return ErrMFAInvalid
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM auth_mfa_challenge WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND (expires_at<=$3::timestamptz OR (session_hash=$4 AND operation=$5))`, challenge.TenantID, challenge.UserID, now, challenge.SessionHash, challenge.Operation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_mfa_challenge(token_hash,tenant_id,user_id,credential_id,session_hash,security_epoch,operation,expires_at) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::timestamptz)`, challenge.TokenHash, challenge.TenantID, challenge.UserID, challenge.CredentialID, challenge.SessionHash, epoch, challenge.Operation, challenge.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const challengeSelect = `SELECT c.token_hash,c.tenant_id::text,c.user_id::text,c.credential_id::text,c.session_hash,c.security_epoch,c.operation,c.attempts,COALESCE(c.verified_method,''),c.verified_at,c.consumed_at,c.expires_at
 FROM auth_mfa_challenge c WHERE c.tenant_id=$1::uuid AND c.user_id=$2::uuid AND c.session_hash=$3 AND c.token_hash=$4`

func scanMFAChallenge(row interface{ Scan(...any) error }) (MFAChallenge, error) {
	var challenge MFAChallenge
	var verified, consumed sql.NullTime
	err := row.Scan(&challenge.TokenHash, &challenge.TenantID, &challenge.UserID, &challenge.CredentialID, &challenge.SessionHash, &challenge.SecurityEpoch, &challenge.Operation, &challenge.Attempts, &challenge.VerifiedMethod, &verified, &consumed, &challenge.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return challenge, ErrMFAInvalid
	}
	if verified.Valid {
		challenge.VerifiedAt = timePointer(verified.Time)
	}
	if consumed.Valid {
		challenge.ConsumedAt = timePointer(consumed.Time)
	}
	return challenge, err
}

func validStoredChallenge(challenge MFAChallenge, record TOTPRecord, epoch int64, now time.Time) bool {
	return record.EnabledAt != nil && record.ID == challenge.CredentialID && challenge.SecurityEpoch == epoch && challenge.ExpiresAt.After(now) && challenge.Attempts < 5 && challenge.ConsumedAt == nil
}

func (s *PostgresStore) FindMFAChallenge(ctx context.Context, tenantID, userID, sessionHash, hash string, now time.Time) (MFAChallenge, error) {
	// A short read transaction also checks revocation under the same lock order.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MFAChallenge{}, err
	}
	defer tx.Rollback()
	epoch, _, err := lockMFAPrincipal(ctx, tx, tenantID, userID, sessionHash, now)
	if err != nil {
		return MFAChallenge{}, err
	}
	record, err := scanTOTP(tx.QueryRowContext(ctx, totpSelect, tenantID, userID))
	if err != nil {
		return MFAChallenge{}, err
	}
	challenge, err := scanMFAChallenge(tx.QueryRowContext(ctx, challengeSelect, tenantID, userID, sessionHash, hash))
	if err != nil {
		return challenge, err
	}
	if !validStoredChallenge(challenge, record, epoch, now) {
		return challenge, ErrMFAInvalid
	}
	return challenge, tx.Commit()
}

func (s *PostgresStore) RecordMFAChallengeFailure(ctx context.Context, tenantID, userID, sessionHash, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_mfa_challenge SET attempts=LEAST(attempts+1,5) WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND session_hash=$3 AND token_hash=$4 AND verified_at IS NULL`, tenantID, userID, sessionHash, hash)
	return err
}

func lockMFACommand(ctx context.Context, tx *sql.Tx, proof MFAProof) (TOTPRecord, MFAChallenge, error) {
	epoch, _, err := lockMFAPrincipal(ctx, tx, proof.TenantID, proof.UserID, proof.SessionHash, proof.Now)
	if err != nil {
		return TOTPRecord{}, MFAChallenge{}, err
	}
	record, err := scanTOTP(tx.QueryRowContext(ctx, totpSelect+` FOR UPDATE`, proof.TenantID, proof.UserID))
	if err != nil {
		return record, MFAChallenge{}, err
	}
	challenge, err := scanMFAChallenge(tx.QueryRowContext(ctx, challengeSelect+` FOR UPDATE`, proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash))
	if err != nil {
		return record, challenge, err
	}
	if !validStoredChallenge(challenge, record, epoch, proof.Now) {
		return record, challenge, ErrMFAInvalid
	}
	return record, challenge, nil
}

func (s *PostgresStore) VerifyMFAChallenge(ctx context.Context, proof MFAProof) error {
	// 消费恢复码/TOTP 时间步与标记挑战已验证同事务，失败时不单独烧掉证明材料。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, challenge, err := lockMFACommand(ctx, tx, proof)
	if err != nil {
		return err
	}
	if challenge.VerifiedAt != nil || proof.CredentialID != record.ID {
		return ErrMFAInvalid
	}
	method := "totp"
	if proof.RecoveryHash != "" {
		if challenge.Operation != MFAOperationDisable {
			return ErrMFAInvalid
		}
		result, err := tx.ExecContext(ctx, `UPDATE auth_mfa_recovery_code SET used_at=$5::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND credential_id=$3::uuid AND code_hash=$4 AND used_at IS NULL`, record.TenantID, record.UserID, record.ID, proof.RecoveryHash, proof.Now)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrMFAInvalid
		}
		method = "recovery_code"
	} else {
		if proof.Step <= record.LastUsedStep {
			return ErrMFAInvalid
		}
		_, err = tx.ExecContext(ctx, `UPDATE auth_totp SET last_used_step=$4,updated_at=$5::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND id=$3::uuid`, record.TenantID, record.UserID, record.ID, proof.Step, proof.Now)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE auth_mfa_challenge SET verified_at=$5::timestamptz,verified_method=$6 WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND session_hash=$3 AND token_hash=$4`, proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash, proof.Now, method)
	if err != nil {
		return err
	}
	if method == "recovery_code" {
		if err = recordMFASecurityChangeSQL(ctx, tx, proof.TenantID, proof.UserID, "auth.mfa_recovery_used", method, proof.Now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) FinishMFACommand(ctx context.Context, proof MFAProof, operation string, hashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, challenge, err := lockMFACommand(ctx, tx, proof)
	if err != nil {
		return err
	}
	if challenge.VerifiedAt == nil || challenge.Operation != operation {
		return ErrMFAInvalid
	}
	switch operation {
	case MFAOperationRotateRecovery:
		if challenge.VerifiedMethod != "totp" {
			return ErrMFAInvalid
		}
		if err = replaceMFARecoverySQL(ctx, tx, record, hashes); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE auth_mfa_challenge SET consumed_at=$5::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND session_hash=$3 AND token_hash=$4`, proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash, proof.Now)
	case MFAOperationDisable:
		// 凭据删除、安全版本更新及会话/设备撤销一起提交，关闭 MFA 不留下旧登录权限。
		_, err = tx.ExecContext(ctx, `DELETE FROM auth_totp WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND id=$3::uuid`, record.TenantID, record.UserID, record.ID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE app_user SET security_epoch=security_epoch+1,updated_at=$3::timestamptz WHERE tenant_id=$1::uuid AND id=$2::uuid`, proof.TenantID, proof.UserID, proof.Now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE auth_session SET revoked_at=$3::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, proof.TenantID, proof.UserID, proof.Now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE auth_trusted_device SET revoked_at=$3::timestamptz,updated_at=$3::timestamptz WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, proof.TenantID, proof.UserID, proof.Now)
	default:
		return ErrMFAInvalid
	}
	if err != nil {
		return err
	}
	eventType := "auth.mfa_disabled"
	if operation == MFAOperationRotateRecovery {
		eventType = "auth.mfa_recovery_rotated"
	}
	if err = recordMFASecurityChangeSQL(ctx, tx, proof.TenantID, proof.UserID, eventType, challenge.VerifiedMethod, proof.Now); err != nil {
		return err
	}
	return tx.Commit()
}
