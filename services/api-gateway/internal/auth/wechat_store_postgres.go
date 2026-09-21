package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) CreateWechatLoginChallenge(ctx context.Context, input CreateWechatLoginChallengeInput) error {
	_, err := s.db.ExecContext(ctx, `
WITH expired AS (
  DELETE FROM auth_wechat_login_challenge WHERE expires_at < now() - interval '1 day' RETURNING id
)
INSERT INTO auth_wechat_login_challenge (
  id, state_hash, poll_token_hash, tenant_code, status,
  remember_device, public_device, expires_at
) VALUES ($1::uuid, $2, $3, $4, 'pending', $5, $6, $7)
`, input.ID, input.StateHash, input.PollTokenHash, input.TenantCode, input.RememberDevice, input.PublicDevice, input.ExpiresAt)
	return err
}

func scanWechatChallenge(row interface{ Scan(...any) error }) (WechatLoginChallenge, error) {
	var challenge WechatLoginChallenge
	var tenantID, userID sql.NullString
	var errorCode sql.NullString
	var authorizedAt, consumedAt sql.NullTime
	err := row.Scan(
		&challenge.ID, &challenge.StateHash, &challenge.PollTokenHash,
		&challenge.TenantCode, &challenge.Status, &errorCode,
		&tenantID, &userID, &challenge.RememberDevice, &challenge.PublicDevice,
		&challenge.ExpiresAt, &authorizedAt, &consumedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WechatLoginChallenge{}, ErrInvalidCredentials
	}
	if err != nil {
		return WechatLoginChallenge{}, err
	}
	challenge.ErrorCode = errorCode.String
	challenge.TenantID, challenge.UserID = tenantID.String, userID.String
	if authorizedAt.Valid {
		challenge.AuthorizedAt = authorizedAt.Time
	}
	if consumedAt.Valid {
		challenge.ConsumedAt = consumedAt.Time
	}
	return challenge, nil
}

const wechatChallengeProjection = `
SELECT id::text, state_hash, poll_token_hash, tenant_code, status, error_code,
       tenant_id::text, user_id::text, remember_device, public_device,
       expires_at, authorized_at, consumed_at
FROM auth_wechat_login_challenge`

func (s *PostgresStore) FindWechatLoginChallengeByState(ctx context.Context, stateHash string, now time.Time) (WechatLoginChallenge, error) {
	return scanWechatChallenge(s.db.QueryRowContext(ctx, wechatChallengeProjection+`
WHERE state_hash=$1 AND expires_at>$2 AND consumed_at IS NULL
`, stateHash, now))
}

func (s *PostgresStore) FindWechatLoginChallenge(ctx context.Context, id, pollTokenHash string, now time.Time) (WechatLoginChallenge, error) {
	challenge, err := scanWechatChallenge(s.db.QueryRowContext(ctx, wechatChallengeProjection+`
WHERE id=$1::uuid AND poll_token_hash=$2
`, id, pollTokenHash))
	if err == nil && challenge.Status == "pending" && !challenge.ExpiresAt.After(now) {
		challenge.Status = "expired"
	}
	return challenge, err
}

func (s *PostgresStore) FindUserByWechatIdentity(ctx context.Context, tenantCode, appID, unionID, openID string) (UserWithPassword, error) {
	var username string
	err := s.db.QueryRowContext(ctx, `
SELECT u.username
FROM wechat_identity wi
JOIN tenant t ON t.id=wi.tenant_id AND t.status='active' AND t.deleted_at IS NULL
JOIN app_user u ON u.tenant_id=wi.tenant_id AND u.id=wi.user_id AND u.status='active' AND u.deleted_at IS NULL
WHERE t.code=$1 AND wi.app_id=$2 AND wi.deleted_at IS NULL
  AND (($3 <> '' AND wi.union_id=$3) OR ($4 <> '' AND wi.open_id=$4))
ORDER BY CASE WHEN $3 <> '' AND wi.union_id=$3 THEN 0 ELSE 1 END
LIMIT 1
`, tenantCode, appID, unionID, openID).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return UserWithPassword{}, ErrWechatIdentityUnbound
	}
	if err != nil {
		return UserWithPassword{}, err
	}
	return s.FindUserByLogin(ctx, tenantCode, username)
}

func (s *PostgresStore) FindUserByID(ctx context.Context, tenantID, userID string) (UserWithPassword, error) {
	var tenantCode, username string
	err := s.db.QueryRowContext(ctx, `
SELECT t.code, u.username
FROM app_user u JOIN tenant t ON t.id=u.tenant_id
WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL
  AND t.status='active' AND t.deleted_at IS NULL
`, tenantID, userID).Scan(&tenantCode, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return UserWithPassword{}, ErrInvalidCredentials
	}
	if err != nil {
		return UserWithPassword{}, err
	}
	return s.FindUserByLogin(ctx, tenantCode, username)
}

func (s *PostgresStore) AuthorizeWechatLoginChallenge(ctx context.Context, id, tenantID, userID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_wechat_login_challenge
SET status='authorized', tenant_id=$2::uuid, user_id=$3::uuid, authorized_at=$4, updated_at=$4
WHERE id=$1::uuid AND status='pending' AND expires_at>$4
`, id, tenantID, userID, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *PostgresStore) FailWechatLoginChallenge(ctx context.Context, id, code string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_wechat_login_challenge
SET status='failed', error_code=$2, updated_at=$3
WHERE id=$1::uuid AND status='pending' AND expires_at>$3
`, id, code, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *PostgresStore) ConsumeWechatLoginChallenge(ctx context.Context, id, pollTokenHash string, now time.Time) (WechatLoginChallenge, error) {
	return scanWechatChallenge(s.db.QueryRowContext(ctx, `
UPDATE auth_wechat_login_challenge
SET status='consumed', consumed_at=$3, updated_at=$3
WHERE id=$1::uuid AND poll_token_hash=$2 AND status='authorized' AND expires_at>$3
RETURNING id::text, state_hash, poll_token_hash, tenant_code, status, error_code,
          tenant_id::text, user_id::text, remember_device, public_device,
          expires_at, authorized_at, consumed_at
`, id, pollTokenHash, now))
}
