package auth

import (
	"context"
	"database/sql"
	"errors"
)

func (s *MemoryStore) ReadAccountProfile(ctx context.Context, tenantID, userID string) (AccountProfile, error) {
	user, err := s.FindUserByID(ctx, tenantID, userID)
	if err != nil {
		return AccountProfile{}, err
	}
	return AccountProfile{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		PhoneMasked: MaskPhone(user.PhoneNormalized), EmployeeNo: user.EmployeeNo,
		SchoolID: user.SchoolID, TeachingSubjects: append([]string{}, user.TeachingSubjects...)}, nil
}

func (s *PostgresStore) ReadAccountProfile(ctx context.Context, tenantID, userID string) (AccountProfile, error) {
	var profile AccountProfile
	var phone string
	// 本人资料按会话身份直接读取；登录查询没有学科投影，也不应为查看资料多读取密码哈希。
	err := s.db.QueryRowContext(ctx, `
SELECT u.id::text, u.username, u.display_name, COALESCE(u.phone_normalized,''),
       COALESCE(u.employee_no,''), COALESCE(u.school_id::text,''), u.teaching_subjects
FROM app_user u JOIN tenant t ON t.id=u.tenant_id
WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL
  AND t.status='active' AND t.deleted_at IS NULL
`, tenantID, userID).Scan(&profile.ID, &profile.Username, &profile.DisplayName, &phone,
		&profile.EmployeeNo, &profile.SchoolID, pqArray(&profile.TeachingSubjects))
	if errors.Is(err, sql.ErrNoRows) {
		return AccountProfile{}, ErrInvalidCredentials
	}
	if err != nil {
		return AccountProfile{}, err
	}
	profile.PhoneMasked = MaskPhone(phone)
	return profile, nil
}
