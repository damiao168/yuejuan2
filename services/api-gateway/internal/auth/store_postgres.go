package auth

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

var sessionTouchFailureLogger = logger.New(os.Stderr, "error")

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) FindUserByLogin(ctx context.Context, tenantCode string, username string) (UserWithPassword, error) {
	return s.findUserByIdentity(ctx, tenantCode, username, "")
}

// 学生使用学校代码和当前学号；绑定后的内部用户名不再作为公众登录别名。
// 学校代码须定位唯一租户；platform 保留给平台租户，不能被学校代码占用。
func (s *PostgresStore) findUserByIdentity(ctx context.Context, tenantCode, username, userID string) (UserWithPassword, error) {
	tenantID := tenantCode
	if userID == "" {
		// 登录前没有会话租户；先用公开组织代码定位唯一租户，再在该租户内读取受 RLS 保护的学生。
		// 不能使用维护上下文跨租户读取，也不能让重复的学校代码随查询顺序选中一个租户。
		if err := s.db.QueryRowContext(ctx, `
SELECT min(t.id::text)
FROM tenant t
WHERE t.status='active' AND t.deleted_at IS NULL
  AND (t.code=$1 OR ($1<>'platform' AND EXISTS (
    SELECT 1 FROM school sc WHERE sc.tenant_id=t.id AND sc.code=$1 AND sc.status='active' AND sc.deleted_at IS NULL
  )))
HAVING count(*)=1`, tenantCode).Scan(&tenantID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return UserWithPassword{}, ErrInvalidCredentials
			}
			return UserWithPassword{}, err
		}
	}
	ctx = db.WithTenant(ctx, tenantID)
	phone, _ := NormalizePhone(username)
	row := s.db.QueryRowContext(ctx, `
WITH candidates AS (
  SELECT u.id
  FROM app_user u
  JOIN tenant t ON t.id = u.tenant_id
  LEFT JOIN school sc ON sc.tenant_id=u.tenant_id AND sc.id=u.school_id
  LEFT JOIN student st ON st.tenant_id=u.tenant_id AND st.school_id=u.school_id AND st.id=u.student_id
  WHERE (
    ($4 = '' AND (
      (u.student_id IS NULL AND (t.code=$1 OR (sc.code=$1 AND $1<>'platform' AND sc.status='active' AND sc.deleted_at IS NULL)) AND (u.username=$2 OR u.employee_no=$2 OR ($3<>'' AND u.phone_normalized=$3)))
      OR (sc.code=$1 AND $1<>'platform' AND sc.status='active' AND sc.deleted_at IS NULL AND st.student_no=$2 AND st.status='active' AND st.deleted_at IS NULL)
    ))
    OR ($4 <> '' AND t.id::text=$1 AND u.id::text=$4)
  )
    AND u.status = 'active'
    AND u.tenant_id::text = $5
    AND u.deleted_at IS NULL
    AND t.status = 'active'
    AND t.deleted_at IS NULL
)
SELECT
  u.id::text,
  u.tenant_id::text,
  t.code,
  u.username,
  u.display_name,
  u.status,
  u.password_hash,
  u.must_change_password,
	COALESCE(u.phone_normalized, ''),
	COALESCE(u.employee_no, ''),
	u.security_epoch,
	u.activated_at,
	u.last_login_at,
  COALESCE(array_agg(DISTINCT r.code) FILTER (WHERE r.code IS NOT NULL), '{}') AS roles,
  COALESCE(array_agg(DISTINCT p.code) FILTER (WHERE p.code IS NOT NULL), '{}') AS permissions,
  COALESCE(jsonb_object_agg(r.code, ur.data_scope) FILTER (WHERE r.code IS NOT NULL), '{}') AS data_scope
FROM app_user u
JOIN tenant t ON t.id = u.tenant_id
LEFT JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
LEFT JOIN role r ON r.tenant_id = u.tenant_id AND r.id = ur.role_id AND r.deleted_at IS NULL
LEFT JOIN role_permission rp ON rp.tenant_id = u.tenant_id AND rp.role_id = r.id AND rp.deleted_at IS NULL
LEFT JOIN permission p ON p.tenant_id = u.tenant_id AND p.id = rp.permission_id AND p.deleted_at IS NULL
WHERE u.id = (SELECT id FROM candidates LIMIT 1)
  AND (SELECT count(*) FROM candidates) = 1
  AND t.status = 'active'
  AND t.deleted_at IS NULL
  AND u.deleted_at IS NULL
GROUP BY u.id, t.code
`, tenantCode, username, phone, userID, tenantID)

	var user UserWithPassword
	var roles []string
	var permissions []string
	var dataScopeRaw []byte
	var activatedAt, lastLoginAt sql.NullTime
	if err := row.Scan(&user.ID, &user.TenantID, &user.TenantCode, &user.Username, &user.DisplayName, &user.Status, &user.PasswordHash, &user.MustChangePassword, &user.PhoneNormalized, &user.EmployeeNo, &user.SecurityEpoch, &activatedAt, &lastLoginAt, pqArray(&roles), pqArray(&permissions), &dataScopeRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserWithPassword{}, ErrInvalidCredentials
		}
		return UserWithPassword{}, err
	}
	if activatedAt.Valid {
		user.ActivatedAt = activatedAt.Time
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = lastLoginAt.Time
	}
	user.Roles = roles
	user.Permissions = permissions
	user.DataScope = map[string]any{}
	_ = json.Unmarshal(dataScopeRaw, &user.DataScope)
	if user.Status != "active" {
		return UserWithPassword{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *PostgresStore) FindPasswordHash(ctx context.Context, tenantID string, userID string) (string, error) {
	var passwordHash string
	err := s.db.QueryRowContext(ctx, `
SELECT u.password_hash
FROM app_user u
JOIN tenant t ON t.id = u.tenant_id
WHERE u.tenant_id = $1::uuid
  AND u.id = $2::uuid
  AND u.status = 'active'
  AND u.deleted_at IS NULL
  AND t.status = 'active'
  AND t.deleted_at IS NULL
`, tenantID, userID).Scan(&passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidCredentials
	}
	return passwordHash, err
}

func (s *PostgresStore) RecordSuccessfulLogin(ctx context.Context, tenantID, userID, expectedPasswordHash, replacementPasswordHash string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE app_user
SET last_login_at = now(),
    password_hash = CASE WHEN $4 <> '' AND password_hash = $3 THEN $4 ELSE password_hash END,
    updated_at = CASE WHEN $4 <> '' AND password_hash = $3 THEN now() ELSE updated_at END
WHERE tenant_id = $1::uuid AND id = $2::uuid AND password_hash = $3 AND status = 'active' AND deleted_at IS NULL
`, tenantID, userID, expectedPasswordHash, replacementPasswordHash)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

// pqArray 解析 PostgreSQL 文本数组，格式为 lib/pq 兼容驱动返回的简单 {"a","b"} 形式。
func pqArray(target *[]string) any {
	return scannerFunc(func(src any) error {
		if src == nil {
			*target = nil
			return nil
		}
		text := ""
		switch value := src.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		default:
			return nil
		}
		text = strings.Trim(text, "{}")
		if text == "" {
			*target = []string{}
			return nil
		}
		parts := strings.Split(text, ",")
		for i := range parts {
			parts[i] = strings.Trim(parts[i], `"`)
		}
		*target = parts
		return nil
	})
}

type scannerFunc func(src any) error

func (f scannerFunc) Scan(src any) error {
	return f(src)
}
