package org

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) UpdateGrade(ctx context.Context, tenantID, id string, input GradePatch) (Grade, error) {
	// 租户取自认证身份；更新只涉及名称，既有学校、学年和届别关系保持不变。
	row := s.db.QueryRowContext(ctx, `UPDATE grade SET name=COALESCE($3,name),updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL
RETURNING id::text,tenant_id::text,school_id::text,name,COALESCE(level_no,0),COALESCE(academic_year,''),education_stage,academic_year_id::text,grade_cohort_id::text,status`, tenantID, id, input.Name)
	var out Grade
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.LevelNo, &out.AcademicYear, &out.EducationStage, &out.AcademicYearID, &out.GradeCohortID, &out.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grade{}, ErrNotFound
		}
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
	// COALESCE 区分未传与显式空串；空性别可以清除，其他非空限制在请求入口执行。
	// 当前班级仍按学籍记录读取；此补丁不写 class_id，避免跳过转班历史。
	row := s.db.QueryRowContext(ctx, `WITH updated AS (
  UPDATE student SET name=COALESCE($3,name),student_no=COALESCE($4,student_no),
    gender=COALESCE($5,gender),status=COALESCE($6,status),updated_at=now()
  WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL RETURNING *
)
SELECT st.id::text,st.tenant_id::text,st.school_id::text,COALESCE(current_enrollment.class_id::text,st.class_id::text,''),
  COALESCE(current_enrollment.academic_year_id::text,''),COALESCE(current_enrollment.grade_cohort_id::text,''),
  st.student_no,st.name,COALESCE(st.gender,''),st.status,COALESCE(st.admission_year,0)
FROM updated st
LEFT JOIN LATERAL (
  SELECT enrollment.class_id,enrollment.academic_year_id,enrollment.grade_cohort_id
  FROM student_enrollment enrollment
  JOIN academic_year year ON year.tenant_id=enrollment.tenant_id AND year.id=enrollment.academic_year_id
  WHERE enrollment.tenant_id=st.tenant_id AND enrollment.student_id=st.id AND enrollment.deleted_at IS NULL
  ORDER BY year.is_current DESC,year.start_year DESC,(enrollment.status='enrolled' AND enrollment.end_date IS NULL) DESC,enrollment.start_date DESC,enrollment.id DESC LIMIT 1
) current_enrollment ON true`, tenantID, id, input.Name, input.StudentNo, input.Gender, input.Status)
	var out Student
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.ClassID, &out.AcademicYearID, &out.GradeCohortID, &out.StudentNo, &out.Name, &out.Gender, &out.Status, &out.AdmissionYear); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Student{}, ErrNotFound
		}
		// 数据库唯一约束负责并发冲突，不能靠更新前查询排除同校重复学号。
		if postgresConstraint(err, "23505", "student_tenant_id_school_id_student_no_key") {
			return Student{}, ErrStudentNoConflict
		}
		return Student{}, err
	}
	return out, nil
}
