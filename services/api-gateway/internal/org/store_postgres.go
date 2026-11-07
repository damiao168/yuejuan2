package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// 创建租户同时建立首个管理员，使用事务保证不会出现“有租户却没有管理员”的半成品。
func (s *PostgresStore) CreateTenant(ctx context.Context, input TenantProvision) (Tenant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tenant{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
WITH new_id AS (SELECT gen_random_uuid() AS id)
INSERT INTO tenant (id, tenant_id, name, code, status)
SELECT id, id, $1, $2, COALESCE(NULLIF($3::text, ''), 'active') FROM new_id
RETURNING id::text, name, code, status
`, input.Name, input.Code, input.Status)
	var out Tenant
	if err := row.Scan(&out.ID, &out.Name, &out.Code, &out.Status); err != nil {
		return Tenant{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO permission (tenant_id, code, name, resource, action, description)
SELECT $1::uuid, code, name, resource, action, description
FROM permission
WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
  AND deleted_at IS NULL
ON CONFLICT (tenant_id, code) DO NOTHING
`, out.ID); err != nil {
		return Tenant{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO role (tenant_id, code, name, scope_type, description)
SELECT $1::uuid, code, name, scope_type, description
FROM role
WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
  AND code IN ('tenant_admin', 'school_admin', 'teacher', 'grader', 'auditor', 'arbitrator', 'student', 'page_processing_worker')
  AND deleted_at IS NULL
ON CONFLICT (tenant_id, code) DO NOTHING
`, out.ID); err != nil {
		return Tenant{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO role_permission (tenant_id, role_id, permission_id)
SELECT $1::uuid, target_role.id, target_permission.id
FROM role_permission source_assignment
JOIN role source_role
  ON source_role.tenant_id = source_assignment.tenant_id
 AND source_role.id = source_assignment.role_id
JOIN permission source_permission
  ON source_permission.tenant_id = source_assignment.tenant_id
 AND source_permission.id = source_assignment.permission_id
JOIN role target_role
  ON target_role.tenant_id = $1::uuid
 AND target_role.code = source_role.code
JOIN permission target_permission
  ON target_permission.tenant_id = $1::uuid
 AND target_permission.code = source_permission.code
WHERE source_assignment.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
  AND source_assignment.deleted_at IS NULL
ON CONFLICT (tenant_id, role_id, permission_id) DO NOTHING
`, out.ID); err != nil {
		return Tenant{}, err
	}

	var schoolID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO school (tenant_id, name, code, status)
VALUES ($1::uuid, $2, $3, 'active')
RETURNING id::text
`, out.ID, input.Name, input.Code).Scan(&schoolID); err != nil {
		return Tenant{}, err
	}
	var userID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO app_user (tenant_id, school_id, username, display_name, password_hash, status)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'active')
RETURNING id::text
`, out.ID, schoolID, input.AdminUsername, input.AdminDisplayName, input.PasswordHash).Scan(&userID); err != nil {
		return Tenant{}, err
	}
	roleResult, err := tx.ExecContext(ctx, `
INSERT INTO user_role (tenant_id, user_id, role_id, data_scope)
  SELECT $1::uuid, $2::uuid, role.id,
         jsonb_build_object('scope', 'school', 'school_id', $3::text, 'school_name', $4::text)
FROM role
WHERE tenant_id = $1::uuid AND code = 'school_admin' AND deleted_at IS NULL
`, out.ID, userID, schoolID, input.Name)
	if err != nil {
		return Tenant{}, err
	}
	assigned, err := roleResult.RowsAffected()
	if err != nil || assigned != 1 {
		return Tenant{}, fmt.Errorf("school administrator role provisioning failed")
	}
	if err := tx.Commit(); err != nil {
		return Tenant{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListTenants(ctx context.Context, tenantID string, canListAll bool, filter TenantListFilter) ([]Tenant, error) {
	query := `SELECT id::text, name, code, status FROM tenant WHERE deleted_at IS NULL`
	args := []any{}
	if !canListAll {
		args = append(args, tenantID)
		query += fmt.Sprintf(` AND id = $%d::uuid`, len(args))
	}
	if filter.Query != "" {
		args = append(args, "%"+filter.Query+"%")
		query += fmt.Sprintf(` AND (name ILIKE $%d OR code ILIKE $%d)`, len(args), len(args))
	}
	if filter.CursorCode != "" && filter.CursorID != "" {
		args = append(args, filter.CursorCode, filter.CursorID)
		query += fmt.Sprintf(` AND (code > $%d OR (code = $%d AND id::text > $%d))`, len(args)-1, len(args)-1, len(args))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 51
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY code, id LIMIT $%d`, len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var item Tenant
		if err := rows.Scan(&item.ID, &item.Name, &item.Code, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateTenantStatus(ctx context.Context, id string, status string) (Tenant, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE tenant SET status = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL RETURNING id::text, name, code, status`, id, status)
	var out Tenant
	if err := row.Scan(&out.ID, &out.Name, &out.Code, &out.Status); err != nil {
		return Tenant{}, err
	}
	return out, nil
}

func (s *PostgresStore) CreateSchool(ctx context.Context, tenantID string, input School) (School, error) {
	row := s.db.QueryRowContext(ctx, `INSERT INTO school (tenant_id, name, code, status) VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'active')) RETURNING id::text, tenant_id::text, name, code, education_stages, status`, tenantID, input.Name, input.Code, input.Status)
	var out School
	var stages []byte
	if err := row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Code, &stages, &out.Status); err != nil {
		if postgresConstraint(err, "23505", "school_tenant_id_code_key") {
			return School{}, ErrSchoolCodeConflict
		}
		return School{}, err
	}
	_ = json.Unmarshal(stages, &out.EducationStages)
	return out, nil
}

func (s *PostgresStore) ListSchools(ctx context.Context, tenantID string) ([]School, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, tenant_id::text, name, code, education_stages, status FROM school WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []School{}
	for rows.Next() {
		var item School
		var stages []byte
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.Code, &stages, &item.Status); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(stages, &item.EducationStages)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAcademicYears(ctx context.Context, tenantID, schoolID string) ([]AcademicYear, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,school_id::text,name,start_year,end_year,starts_at::text,ends_at::text,is_current,status FROM academic_year WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2='' OR school_id::text=$2) ORDER BY start_year DESC`, tenantID, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AcademicYear{}
	for rows.Next() {
		var item AcademicYear
		if err := rows.Scan(&item.ID, &item.TenantID, &item.SchoolID, &item.Name, &item.StartYear, &item.EndYear, &item.StartsAt, &item.EndsAt, &item.IsCurrent, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListGradeCohorts(ctx context.Context, tenantID, schoolID string) ([]GradeCohort, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,school_id::text,education_stage,entry_year,expected_graduation_year,name,status FROM grade_cohort WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2='' OR school_id::text=$2) ORDER BY entry_year DESC`, tenantID, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GradeCohort{}
	for rows.Next() {
		var item GradeCohort
		if err := rows.Scan(&item.ID, &item.TenantID, &item.SchoolID, &item.EducationStage, &item.EntryYear, &item.ExpectedGraduationYear, &item.Name, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateGrade(ctx context.Context, tenantID string, input Grade) (Grade, error) {
	if err := validateGradeInput(input); err != nil {
		return Grade{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grade{}, err
	}
	defer tx.Rollback()
	schoolYear, err := lockGradeSchool(ctx, tx, tenantID, input.SchoolID)
	if err != nil {
		return Grade{}, err
	}
	input, err = normalizeCurrentGrade(input, schoolYear)
	if err != nil {
		return Grade{}, err
	}
	existing, err := schoolGradesTx(ctx, tx, tenantID, input.SchoolID)
	if err != nil {
		return Grade{}, err
	}
	if err = validateCurrentGradeConflict(input, existing, schoolYear, true); err != nil {
		return Grade{}, err
	}
	// 固定入学届别只在新建时确定；同校检查与插入共用父锁，不允许并发追加同届年度记录。
	row := tx.QueryRowContext(ctx, `
WITH input_value AS (
  SELECT CASE WHEN $5 ~ '^[0-9]{4}-[0-9]{4}$' THEN split_part($5,'-',1)::int ELSE COALESCE(
    (SELECT start_year FROM academic_year WHERE tenant_id=$1 AND school_id::text=$2 AND is_current AND deleted_at IS NULL ORDER BY start_year DESC LIMIT 1),
    EXTRACT(YEAR FROM (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai'))::int-CASE WHEN EXTRACT(MONTH FROM (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai'))<9 THEN 1 ELSE 0 END
  ) END start_year,
    COALESCE(NULLIF($6,''),CASE WHEN $4>=10 THEN 'senior' ELSE 'junior' END) stage
), parent_school AS (
  SELECT id FROM school WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL
), school_stage AS (
  UPDATE school target
  SET education_stages=(
    SELECT jsonb_agg(stage ORDER BY stage)
    FROM (
      SELECT DISTINCT jsonb_array_elements_text(target.education_stages || jsonb_build_array(input_value.stage)) AS stage
    ) stages
  ),updated_at=now()
  FROM parent_school,input_value
  WHERE target.id=parent_school.id
  RETURNING target.id
), year_row AS (
  INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current)
  SELECT $1,parent_school.id,input_value.start_year::text||'-'||(input_value.start_year+1)::text||'学年',input_value.start_year,input_value.start_year+1,
    make_date(input_value.start_year,9,1),make_date(input_value.start_year+1,8,31),CURRENT_DATE BETWEEN make_date(input_value.start_year,9,1) AND make_date(input_value.start_year+1,8,31)
  FROM parent_school CROSS JOIN input_value CROSS JOIN school_stage
  ON CONFLICT(tenant_id,school_id,start_year) DO UPDATE SET updated_at=now()
  RETURNING id,start_year
), cohort_row AS (
  INSERT INTO grade_cohort(tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name)
  SELECT $1,parent_school.id,input_value.stage,
    COALESCE(NULLIF($8,0),year_row.start_year-CASE WHEN input_value.stage='senior' THEN GREATEST($4-10,0) ELSE GREATEST($4-7,0) END),
    COALESCE(NULLIF($8,0),year_row.start_year-CASE WHEN input_value.stage='senior' THEN GREATEST($4-10,0) ELSE GREATEST($4-7,0) END)+3,
    (COALESCE(NULLIF($8,0),year_row.start_year-CASE WHEN input_value.stage='senior' THEN GREATEST($4-10,0) ELSE GREATEST($4-7,0) END))::text||'级'
  FROM parent_school CROSS JOIN input_value CROSS JOIN year_row
  ON CONFLICT(tenant_id,school_id,education_stage,entry_year) DO UPDATE SET updated_at=now()
  RETURNING id,entry_year
)
, inserted AS (INSERT INTO grade (tenant_id,school_id,name,level_no,academic_year,education_stage,academic_year_id,grade_cohort_id,status)
SELECT $1,parent_school.id,$3,$4,COALESCE(NULLIF($5,''),input_value.start_year::text||'-'||(input_value.start_year+1)::text),input_value.stage,year_row.id,cohort_row.id,COALESCE(NULLIF($7,''),'active')
FROM parent_school CROSS JOIN input_value CROSS JOIN year_row CROSS JOIN cohort_row
RETURNING id::text,tenant_id::text,school_id::text,name,COALESCE(level_no,0),COALESCE(academic_year,''),education_stage,academic_year_id::text,grade_cohort_id::text,status
) SELECT inserted.*,cohort_row.entry_year FROM inserted CROSS JOIN cohort_row
`, tenantID, input.SchoolID, input.Name, input.LevelNo, input.AcademicYear, input.EducationStage, input.Status, input.EntryYear)
	var out Grade
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.LevelNo, &out.AcademicYear, &out.EducationStage, &out.AcademicYearID, &out.GradeCohortID, &out.Status, &out.EntryYear); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grade{}, ErrInvalidParent
		}
		return Grade{}, err
	}
	if err = tx.Commit(); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListGrades(ctx context.Context, tenantID string, schoolID string) ([]Grade, error) {
	query := `SELECT g.id::text,g.tenant_id::text,g.school_id::text,g.name,COALESCE(g.level_no,0),COALESCE(g.academic_year,''),g.education_stage,g.academic_year_id::text,g.grade_cohort_id::text,g.status,cohort.entry_year FROM grade g JOIN grade_cohort cohort ON cohort.tenant_id=g.tenant_id AND cohort.id=g.grade_cohort_id WHERE g.tenant_id=$1 AND g.deleted_at IS NULL`
	args := []any{tenantID}
	if schoolID != "" {
		query += ` AND g.school_id = $2`
		args = append(args, schoolID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grade{}
	for rows.Next() {
		var item Grade
		if err := rows.Scan(&item.ID, &item.TenantID, &item.SchoolID, &item.Name, &item.LevelNo, &item.AcademicYear, &item.EducationStage, &item.AcademicYearID, &item.GradeCohortID, &item.Status, &item.EntryYear); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateClass(ctx context.Context, tenantID string, input Class) (Class, error) {
	// 创建与软删除共用父年级行锁，等待删除提交后重新检查 deleted_at。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Class{}, err
	}
	defer tx.Rollback()
	var cohortID string
	// 与整届删除保持 cohort→grade 的锁顺序，避免跨年度班级插入和删除互相漏检。
	err = tx.QueryRowContext(ctx, `SELECT cohort.id::text FROM grade_cohort cohort JOIN grade g ON g.tenant_id=cohort.tenant_id AND g.grade_cohort_id=cohort.id WHERE g.tenant_id=$1 AND g.id::text=$2 AND g.school_id::text=$3 AND g.deleted_at IS NULL FOR SHARE OF cohort`, tenantID, input.GradeID, input.SchoolID).Scan(&cohortID)
	if errors.Is(err, sql.ErrNoRows) {
		return Class{}, ErrInvalidParent
	}
	if err != nil {
		return Class{}, err
	}
	row := tx.QueryRowContext(ctx, `
WITH parent_school AS (
  SELECT id FROM school WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
),
parent_grade AS (
  SELECT id,academic_year_id,grade_cohort_id FROM grade WHERE tenant_id = $1 AND id::text = $3 AND school_id::text = $2 AND deleted_at IS NULL FOR SHARE
)
INSERT INTO school_class (tenant_id,school_id,grade_id,academic_year_id,grade_cohort_id,class_no,name,code,status)
SELECT $1,parent_school.id,parent_grade.id,parent_grade.academic_year_id,parent_grade.grade_cohort_id,NULLIF($7,0),$4,$5,COALESCE(NULLIF($6,''),'active')
FROM parent_school
CROSS JOIN parent_grade
RETURNING id::text,tenant_id::text,school_id::text,grade_id::text,academic_year_id::text,grade_cohort_id::text,COALESCE(class_no,0),name,code,status
`, tenantID, input.SchoolID, input.GradeID, input.Name, input.Code, input.Status, input.ClassNo)
	var out Class
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.GradeID, &out.AcademicYearID, &out.GradeCohortID, &out.ClassNo, &out.Name, &out.Code, &out.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Class{}, ErrInvalidParent
		}
		if postgresConstraint(err, "23505", "school_class_tenant_id_grade_id_code_key") {
			return Class{}, ErrClassCodeConflict
		}
		return Class{}, err
	}
	if err = tx.Commit(); err != nil {
		return Class{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListClasses(ctx context.Context, tenantID string, gradeID string) ([]Class, error) {
	query := `SELECT id::text,tenant_id::text,school_id::text,grade_id::text,academic_year_id::text,grade_cohort_id::text,COALESCE(class_no,0),name,code,status FROM school_class WHERE tenant_id=$1 AND deleted_at IS NULL`
	args := []any{tenantID}
	if gradeID != "" {
		query += ` AND grade_id = $2`
		args = append(args, gradeID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Class{}
	for rows.Next() {
		var item Class
		if err := rows.Scan(&item.ID, &item.TenantID, &item.SchoolID, &item.GradeID, &item.AcademicYearID, &item.GradeCohortID, &item.ClassNo, &item.Name, &item.Code, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateStudent(ctx context.Context, tenantID string, input Student) (Student, error) {
	return createStudentRecord(ctx, s.db, tenantID, input)
}

type studentQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func createStudentRecord(ctx context.Context, query studentQueryer, tenantID string, input Student) (Student, error) {
	// 低层名册入口供内部数据准备使用；HTTP 新增通过外层事务同时开通账号。
	// 锁住目标班级直到学生和初始学籍一并提交，防止空班删除漏过新学生。
	row := query.QueryRowContext(ctx, `
WITH parent_school AS (
  SELECT id FROM school WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
),
parent_class AS (
  SELECT cls.id,cls.academic_year_id,cls.grade_cohort_id,cohort.entry_year,year.starts_at
  FROM (
    SELECT id,tenant_id,academic_year_id,grade_cohort_id
    FROM school_class WHERE tenant_id = $1 AND id::text = $3 AND school_id::text = $2 AND deleted_at IS NULL FOR SHARE
  ) cls
  JOIN grade_cohort cohort ON cohort.tenant_id=cls.tenant_id AND cohort.id=cls.grade_cohort_id
  JOIN academic_year year ON year.tenant_id=cls.tenant_id AND year.id=cls.academic_year_id
), inserted AS (
  INSERT INTO student (tenant_id,school_id,class_id,student_no,name,gender,status,admission_year)
  SELECT $1,parent_school.id,parent_class.id,$4,$5,$6,COALESCE(NULLIF($7,''),'active'),parent_class.entry_year
  FROM parent_school CROSS JOIN parent_class
  RETURNING *
), enrollment AS (
  INSERT INTO student_enrollment(tenant_id,school_id,student_id,academic_year_id,grade_cohort_id,class_id,status,start_date)
  SELECT inserted.tenant_id,inserted.school_id,inserted.id,parent_class.academic_year_id,parent_class.grade_cohort_id,parent_class.id,'enrolled',parent_class.starts_at
  FROM inserted CROSS JOIN parent_class RETURNING id
)
SELECT inserted.id::text,inserted.tenant_id::text,inserted.school_id::text,parent_class.id::text,
  parent_class.academic_year_id::text,parent_class.grade_cohort_id::text,inserted.student_no,inserted.name,
  COALESCE(inserted.gender,''),inserted.status,COALESCE(inserted.admission_year,0)
FROM inserted CROSS JOIN parent_class CROSS JOIN enrollment
`, tenantID, input.SchoolID, input.ClassID, input.StudentNo, input.Name, input.Gender, input.Status)
	var out Student
	if err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.ClassID, &out.AcademicYearID, &out.GradeCohortID, &out.StudentNo, &out.Name, &out.Gender, &out.Status, &out.AdmissionYear); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Student{}, ErrInvalidParent
		}
		if postgresConstraint(err, "23505", "student_tenant_id_school_id_student_no_key") {
			return Student{}, ErrStudentNoConflict
		}
		return Student{}, err
	}
	out.AccountStatus = "not_provisioned"
	return out, nil
}

func postgresConstraint(err error, code string, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

func (s *PostgresStore) ListStudents(ctx context.Context, tenantID string, filter StudentListFilter) ([]Student, error) {
	// 当前在读关系优先于存档学年；跨年转班后不能因为旧学年仍标为 current 而回显已转出的班级。
	query := studentProjectionSQL + ` WHERE st.tenant_id=$1 AND st.deleted_at IS NULL
  AND ($2='' OR current_enrollment.class_id::text=$2)
  AND ($3='' OR st.student_no ILIKE '%'||$3||'%' OR st.name ILIKE '%'||$3||'%')
  AND ($5='' OR st.student_no>$4 OR (st.student_no=$4 AND st.id::text>$5))
  AND (NOT $6 OR current_enrollment.class_id::text = ANY(string_to_array($7, ',')) OR ($8<>'' AND st.id::text=$8))`
	args := []any{tenantID, filter.ClassID, filter.Query, filter.CursorStudentNo, filter.CursorID, filter.RestrictClasses, strings.Join(filter.ClassIDs, ","), filter.StudentID}
	if len(filter.StudentIDs) > 0 {
		placeholders := make([]string, len(filter.StudentIDs))
		for index, id := range filter.StudentIDs {
			placeholders[index] = fmt.Sprintf("$%d::uuid", len(args)+1)
			args = append(args, id)
		}
		query += ` AND st.id IN (` + strings.Join(placeholders, ",") + `)`
	}
	args = append(args, filter.Limit)
	query += fmt.Sprintf(" ORDER BY st.student_no ASC, st.id::text ASC LIMIT NULLIF($%d, 0)", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Student{}
	for rows.Next() {
		item, err := scanStudentProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateStudentStatus(ctx context.Context, tenantID string, id string, status string) (Student, error) {
	return s.UpdateStudent(ctx, tenantID, id, StudentPatch{Status: &status})
}
