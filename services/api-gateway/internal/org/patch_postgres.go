package org

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) UpdateGrade(ctx context.Context, tenantID, id string, input GradePatch) (Grade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grade{}, err
	}
	defer tx.Rollback()
	var schoolID string
	if err = tx.QueryRowContext(ctx, `SELECT school_id::text FROM grade WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&schoolID); errors.Is(err, sql.ErrNoRows) {
		return Grade{}, ErrNotFound
	} else if err != nil {
		return Grade{}, err
	}
	schoolYear, err := lockGradeSchool(ctx, tx, tenantID, schoolID)
	if err != nil {
		return Grade{}, err
	}
	var current Grade
	err = tx.QueryRowContext(ctx, `SELECT g.id::text,g.tenant_id::text,g.school_id::text,g.name,COALESCE(g.level_no,0),COALESCE(g.academic_year,''),g.education_stage,g.academic_year_id::text,g.grade_cohort_id::text,g.status,cohort.entry_year FROM grade g JOIN grade_cohort cohort ON cohort.tenant_id=g.tenant_id AND cohort.id=g.grade_cohort_id WHERE g.tenant_id=$1 AND g.id::text=$2 AND g.deleted_at IS NULL FOR UPDATE OF g`, tenantID, id).Scan(&current.ID, &current.TenantID, &current.SchoolID, &current.Name, &current.LevelNo, &current.AcademicYear, &current.EducationStage, &current.AcademicYearID, &current.GradeCohortID, &current.Status, &current.EntryYear)
	if errors.Is(err, sql.ErrNoRows) {
		return Grade{}, ErrNotFound
	}
	if err != nil {
		return Grade{}, err
	}
	out, err := applyGradePatch(current, input)
	if err != nil {
		return Grade{}, err
	}
	if input.LevelNo != nil {
		existing, err := schoolGradesTx(ctx, tx, tenantID, schoolID)
		if err != nil {
			return Grade{}, err
		}
		if err = validateCurrentGradeConflict(out, existing, schoolYear, false); err != nil {
			return Grade{}, err
		}
	}
	// 仅更新代表年级的当前级数和名称，不改届别、学年、班级或考试快照。
	if _, err = tx.ExecContext(ctx, `UPDATE grade SET name=$3,level_no=$4,updated_at=now() WHERE tenant_id=$1 AND id::text=$2`, tenantID, id, out.Name, out.LevelNo); err != nil {
		return Grade{}, err
	}
	if err = tx.Commit(); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (s *PostgresStore) UpdateClass(ctx context.Context, tenantID, id string, input ClassPatch) (Class, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE school_class SET name=COALESCE($3,name),code=COALESCE($4,code),updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL
RETURNING id::text,tenant_id::text,school_id::text,grade_id::text,academic_year_id::text,grade_cohort_id::text,COALESCE(class_no,0),name,code,status`, tenantID, id, input.Name, input.Code)
	var out Class
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.GradeID, &out.AcademicYearID, &out.GradeCohortID, &out.ClassNo, &out.Name, &out.Code, &out.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Class{}, ErrNotFound
		}
		if postgresConstraint(err, "23505", "school_class_tenant_id_grade_id_code_key") {
			return Class{}, ErrClassCodeConflict
		}
		return Class{}, err
	}
	return out, nil
}

func (s *PostgresStore) UpdateStudent(ctx context.Context, tenantID, id string, input StudentPatch) (Student, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Student{}, err
	}
	defer tx.Rollback()
	// 开通、改学号、停用和删除共用学生行锁，避免两个入口同时写出不同的账号身份。
	oldNo, oldName, oldStatus, err := lockStudentAccountIdentity(ctx, tx, tenantID, id)
	if err != nil {
		return Student{}, err
	}
	statusChanged := input.Status != nil && *input.Status != oldStatus
	identityChanged := input.StudentNo != nil && *input.StudentNo != oldNo || statusChanged
	if identityChanged || input.Name != nil && *input.Name != oldName {
		// 歧义旧账号不能自动决定停用或修改谁；先拒绝整次补丁，避免名册显示已停用而旧凭据仍有效。
		legacyAccount, legacyErr := studentHasLegacyAccount(ctx, tx, tenantID, id)
		if legacyErr != nil {
			return Student{}, legacyErr
		}
		if legacyAccount {
			return Student{}, ErrStudentAccountLegacyConflict
		}
	}
	// COALESCE 区分未传与显式空串；此补丁不写 class_id，转班继续保留独立学籍历史。
	_, err = tx.ExecContext(ctx, `UPDATE student SET name=COALESCE($3,name),student_no=COALESCE($4,student_no),
  gender=COALESCE($5,gender),status=COALESCE($6,status),updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, tenantID, id, input.Name, input.StudentNo, input.Gender, input.Status)
	if err != nil {
		// 数据库唯一约束负责并发冲突，不能靠更新前查询排除同校重复学号。
		if postgresConstraint(err, "23505", "student_tenant_id_school_id_student_no_key") {
			return Student{}, ErrStudentNoConflict
		}
		return Student{}, err
	}
	if err = syncStudentLoginAccount(ctx, tx, tenantID, id, statusChanged, identityChanged); err != nil {
		return Student{}, err
	}
	out, err := readStudentProjection(ctx, tx, tenantID, id)
	if err != nil {
		return Student{}, err
	}
	if err = tx.Commit(); err != nil {
		return Student{}, err
	}
	return out, nil
}
