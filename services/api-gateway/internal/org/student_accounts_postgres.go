package org

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// 账户投影只读账号状态和登录标识；初始密码及哈希不会进入学生响应。
// 当前在读学籍优先于存档学年，账户始终按稳定 student_id 绑定，不能借改学号换绑成绩。
const studentProjectionSQL = `SELECT st.id::text,st.tenant_id::text,st.school_id::text,COALESCE(current_enrollment.class_id::text,st.class_id::text,''),
  COALESCE(current_enrollment.academic_year_id::text,''),COALESCE(current_enrollment.grade_cohort_id::text,''),
  st.student_no,st.name,COALESCE(st.gender,''),st.status,COALESCE(st.admission_year,0),
  CASE WHEN account.id IS NULL THEN 'not_provisioned' WHEN account.status='active' THEN 'active' ELSE 'disabled' END,
  CASE WHEN account.id IS NULL THEN '' ELSE st.student_no END,
  CASE WHEN account.id IS NULL THEN '' ELSE COALESCE(login_school.code,'') END,
  COALESCE(account.must_change_password,false)
FROM student st
LEFT JOIN LATERAL (
  SELECT enrollment.class_id,enrollment.academic_year_id,enrollment.grade_cohort_id
  FROM student_enrollment enrollment
  JOIN academic_year year ON year.tenant_id=enrollment.tenant_id AND year.id=enrollment.academic_year_id
  WHERE enrollment.tenant_id=st.tenant_id AND enrollment.student_id=st.id AND enrollment.deleted_at IS NULL
  ORDER BY (enrollment.status='enrolled' AND enrollment.end_date IS NULL) DESC,year.is_current DESC,year.start_year DESC,enrollment.start_date DESC,enrollment.id DESC LIMIT 1
) current_enrollment ON true
LEFT JOIN app_user account ON account.tenant_id=st.tenant_id AND account.student_id=st.id AND account.deleted_at IS NULL
LEFT JOIN school login_school ON login_school.tenant_id=st.tenant_id AND login_school.id=st.school_id`

type studentRowScanner interface {
	Scan(...any) error
}

func scanStudentProjection(row studentRowScanner) (Student, error) {
	var out Student
	err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.ClassID, &out.AcademicYearID, &out.GradeCohortID,
		&out.StudentNo, &out.Name, &out.Gender, &out.Status, &out.AdmissionYear,
		&out.AccountStatus, &out.LoginAccount, &out.LoginSchoolCode, &out.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return Student{}, ErrNotFound
	}
	return out, err
}

func readStudentProjection(ctx context.Context, query studentQueryer, tenantID, studentID string) (Student, error) {
	return scanStudentProjection(query.QueryRowContext(ctx, studentProjectionSQL+` WHERE st.tenant_id=$1 AND st.id::text=$2 AND st.deleted_at IS NULL`, tenantID, studentID))
}

// CreateStudentWithAccount 的任一步失败都会回滚名册、初始学籍和账户，不留下没有登录凭据的新增学生。
func (s *PostgresStore) CreateStudentWithAccount(ctx context.Context, tenantID string, input StudentAccountProvision) (Student, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Student{}, err
	}
	defer tx.Rollback()
	input.Student.Status = "active"
	student, err := createStudentRecord(ctx, tx, tenantID, input.Student)
	if err != nil {
		return Student{}, err
	}
	if err = createStudentLoginAccount(ctx, tx, student, input.PasswordHash); err != nil {
		return Student{}, err
	}
	out, err := readStudentProjection(ctx, tx, tenantID, student.ID)
	if err != nil {
		return Student{}, err
	}
	if err = tx.Commit(); err != nil {
		return Student{}, err
	}
	return out, nil
}

// OpenStudentAccount 与编辑、停用、删除先锁同一条学生记录，再读取账户，串行决定是否允许开通。
// 这条路径不接收角色、账户 ID 或历史学生 ID，不会把已有成绩重新绑定给其他身份。
func (s *PostgresStore) OpenStudentAccount(ctx context.Context, tenantID, studentID, passwordHash string) (Student, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Student{}, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStudentAccountIdentity(ctx, tx, tenantID, studentID); err != nil {
		return Student{}, err
	}
	student, err := readStudentProjection(ctx, tx, tenantID, studentID)
	if err != nil {
		return Student{}, err
	}
	if student.AccountStatus != "not_provisioned" {
		return Student{}, ErrStudentAccountExists
	}
	if student.Status != "active" {
		return Student{}, ErrStudentAccountInactive
	}
	// 旧账号可能只有 JSON 本人作用域，尚未进入 student_id 投影；不能把“未绑定”当作没有账号。
	// 不擅自修复或重置存量凭据，先阻止重复开通，交由管理员核对原身份。
	legacyAccount, err := studentHasLegacyAccount(ctx, tx, tenantID, studentID)
	if err != nil {
		return Student{}, err
	}
	if legacyAccount {
		return Student{}, ErrStudentAccountLegacyConflict
	}
	if err = createStudentLoginAccount(ctx, tx, student, passwordHash); err != nil {
		return Student{}, err
	}
	out, err := readStudentProjection(ctx, tx, tenantID, studentID)
	if err != nil {
		return Student{}, err
	}
	if err = tx.Commit(); err != nil {
		return Student{}, err
	}
	return out, nil
}

func studentHasLegacyAccount(ctx context.Context, tx *sql.Tx, tenantID, studentID string) (bool, error) {
	var legacyAccount bool
	// 先锁绑定账户再核对有效角色，与职工授权编辑共用账户锁；混合职责不能被学生停用入口整体禁用。
	// 加锁后另起查询快照，等待过其他授权编辑时不能仍用等待前的角色结果；只有唯一的固定本人绑定可自动同步。
	if _, err := tx.ExecContext(ctx, `SELECT id FROM app_user WHERE tenant_id=$1 AND student_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE`, tenantID, studentID); err != nil {
		return false, err
	}
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM app_user u
  JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
  JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='student' AND r.deleted_at IS NULL
  WHERE u.tenant_id=$1 AND u.deleted_at IS NULL AND u.student_id IS DISTINCT FROM $2::uuid
    AND jsonb_path_exists(ur.data_scope,'$.**.student_id ? (@ == $student_id)',jsonb_build_object('student_id',$2::text))
) OR EXISTS(
  SELECT 1 FROM app_user account
  WHERE account.tenant_id=$1 AND account.student_id=$2::uuid AND account.deleted_at IS NULL
    AND ((SELECT count(*) FROM user_role ur
    JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
    WHERE ur.tenant_id=$1 AND ur.user_id=account.id AND ur.deleted_at IS NULL) <> 1
    OR NOT EXISTS(SELECT 1 FROM user_role ur
      JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
      WHERE ur.tenant_id=$1 AND ur.user_id=account.id AND ur.deleted_at IS NULL
        AND r.code='student' AND r.scope_type='self'
        AND ur.data_scope=jsonb_build_object('scope','self','student_id',$2::text)))
)`, tenantID, studentID).Scan(&legacyAccount)
	return legacyAccount, err
}

func lockStudentAccountIdentity(ctx context.Context, tx *sql.Tx, tenantID, studentID string) (studentNo, name, status string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT student_no,name,status FROM student WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL FOR UPDATE`, tenantID, studentID).Scan(&studentNo, &name, &status)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func createStudentLoginAccount(ctx context.Context, tx *sql.Tx, student Student, passwordHash string) error {
	if passwordHash == "" {
		return errors.New("student password hash is required")
	}
	var roleID string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM role WHERE tenant_id=$1 AND code='student' AND scope_type='self' AND deleted_at IS NULL FOR SHARE`, student.TenantID).Scan(&roleID); err != nil {
		return err
	}
	// 用户名仅是稳定的内部键；学生实际用学校代码和当前学号登录，换班或改学号不更换账户。
	var userID string
	err := tx.QueryRowContext(ctx, `INSERT INTO app_user(tenant_id,school_id,student_id,username,display_name,password_hash,status,activated_at,password_changed_at,must_change_password)
VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,'active',now(),now(),true) RETURNING id::text`,
		student.TenantID, student.SchoolID, student.ID, "student_"+strings.ReplaceAll(student.ID, "-", ""), student.Name, passwordHash).Scan(&userID)
	if err != nil {
		if postgresConstraint(err, "23505", "uq_app_user_active_student") {
			return ErrStudentAccountExists
		}
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
VALUES($1,$2::uuid,$3::uuid,jsonb_build_object('scope','self','student_id',$4::text))`, student.TenantID, userID, roleID, student.ID)
	return err
}

// 学号和在籍状态会影响登录；更新与撤销凭据在同一事务中，旧会话不能在停用后继续访问成绩。
func syncStudentLoginAccount(ctx context.Context, tx *sql.Tx, tenantID, studentID string, statusChanged, identityChanged bool) error {
	rows, err := tx.QueryContext(ctx, `UPDATE app_user account SET display_name=st.name,
  status=CASE WHEN $3 THEN CASE WHEN st.status='active' THEN 'active' ELSE 'disabled' END ELSE account.status END,
  security_epoch=account.security_epoch+CASE WHEN $4 THEN 1 ELSE 0 END,updated_at=now()
FROM student st WHERE st.tenant_id=$1 AND st.id::text=$2 AND account.tenant_id=st.tenant_id AND account.student_id=st.id AND account.deleted_at IS NULL
RETURNING account.id::text`, tenantID, studentID, statusChanged, identityChanged)
	if err != nil {
		return err
	}
	userIDs := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !identityChanged {
		return nil
	}
	for _, userID := range userIDs {
		if _, err = tx.ExecContext(ctx, `UPDATE auth_session SET revoked_at=now(),revoke_reason='student_identity_changed',updated_at=now() WHERE tenant_id=$1 AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, userID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE auth_trusted_device SET revoked_at=now(),updated_at=now() WHERE tenant_id=$1 AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, userID); err != nil {
			return err
		}
	}
	return nil
}
