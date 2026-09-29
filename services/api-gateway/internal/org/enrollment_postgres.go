package org

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 转班会结束旧的在读记录、创建新记录并更新学生当前班级；整组操作必须原子提交。
func (s *PostgresStore) TransferStudent(ctx context.Context, tenantID, studentID, classID, startDate string) (StudentEnrollment, error) {
	if startDate == "" {
		startDate = time.Now().UTC().Format("2006-01-02")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StudentEnrollment{}, err
	}
	defer tx.Rollback()
	var schoolID, yearID, cohortID string
	err = tx.QueryRowContext(ctx, `SELECT st.school_id::text,cls.academic_year_id::text,cls.grade_cohort_id::text FROM student st JOIN school_class cls ON cls.tenant_id=st.tenant_id AND cls.school_id=st.school_id WHERE st.tenant_id=$1 AND st.id=$2::uuid AND cls.id=$3::uuid AND st.deleted_at IS NULL AND cls.deleted_at IS NULL FOR UPDATE OF st`, tenantID, studentID, classID).Scan(&schoolID, &yearID, &cohortID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentEnrollment{}, ErrInvalidParent
	}
	if err != nil {
		return StudentEnrollment{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE student_enrollment SET status='transferred',end_date=GREATEST(start_date,$4::date),updated_at=now() WHERE tenant_id=$1 AND student_id=$2::uuid AND academic_year_id=$3::uuid AND status='enrolled' AND end_date IS NULL AND deleted_at IS NULL`, tenantID, studentID, yearID, startDate); err != nil {
		return StudentEnrollment{}, err
	}
	var out StudentEnrollment
	err = tx.QueryRowContext(ctx, `INSERT INTO student_enrollment(tenant_id,school_id,student_id,academic_year_id,grade_cohort_id,class_id,status,start_date) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'enrolled',$7::date) RETURNING id::text,student_id::text,school_id::text,academic_year_id::text,grade_cohort_id::text,class_id::text,status,start_date::text,COALESCE(end_date::text,'')`, tenantID, schoolID, studentID, yearID, cohortID, classID, startDate).Scan(&out.ID, &out.StudentID, &out.SchoolID, &out.AcademicYearID, &out.GradeCohortID, &out.ClassID, &out.Status, &out.StartDate, &out.EndDate)
	if err != nil {
		return StudentEnrollment{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE student SET class_id=$3::uuid,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, studentID, classID); err != nil {
		return StudentEnrollment{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT ay.name,cohort.name,cls.name FROM academic_year ay JOIN grade_cohort cohort ON cohort.tenant_id=ay.tenant_id AND cohort.id=$3::uuid JOIN school_class cls ON cls.tenant_id=ay.tenant_id AND cls.id=$4::uuid WHERE ay.tenant_id=$1 AND ay.id=$2::uuid`, tenantID, yearID, cohortID, classID).Scan(&out.AcademicYearName, &out.GradeCohortName, &out.ClassName); err != nil {
		return StudentEnrollment{}, err
	}
	if err = tx.Commit(); err != nil {
		return StudentEnrollment{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListStudentEnrollments(ctx context.Context, tenantID, studentID string) ([]StudentEnrollment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT enrollment.id::text,enrollment.student_id::text,enrollment.school_id::text,enrollment.academic_year_id::text,year.name,enrollment.grade_cohort_id::text,cohort.name,enrollment.class_id::text,cls.name,enrollment.status,enrollment.start_date::text,COALESCE(enrollment.end_date::text,'') FROM student_enrollment enrollment JOIN academic_year year ON year.tenant_id=enrollment.tenant_id AND year.id=enrollment.academic_year_id JOIN grade_cohort cohort ON cohort.tenant_id=enrollment.tenant_id AND cohort.id=enrollment.grade_cohort_id JOIN school_class cls ON cls.tenant_id=enrollment.tenant_id AND cls.id=enrollment.class_id WHERE enrollment.tenant_id=$1 AND enrollment.student_id=$2::uuid AND enrollment.deleted_at IS NULL ORDER BY year.start_year DESC,(enrollment.status='enrolled' AND enrollment.end_date IS NULL) DESC,enrollment.start_date DESC,enrollment.id DESC`, tenantID, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudentEnrollment{}
	for rows.Next() {
		var item StudentEnrollment
		if err := rows.Scan(&item.ID, &item.StudentID, &item.SchoolID, &item.AcademicYearID, &item.AcademicYearName, &item.GradeCohortID, &item.GradeCohortName, &item.ClassID, &item.ClassName, &item.Status, &item.StartDate, &item.EndDate); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
