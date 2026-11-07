package org

import (
	"context"
	"errors"
)

var (
	ErrInvalidParent                  = errors.New("organization parent does not belong to tenant")
	ErrSchoolCodeConflict             = errors.New("school code already exists")
	ErrClassCodeConflict              = errors.New("class code already exists in this grade")
	ErrStudentNoConflict              = errors.New("student number already exists in this school")
	ErrNotFound                       = errors.New("organization resource not found")
	ErrStudentMustBeInactive          = errors.New("student must be inactive before permanent deletion")
	ErrStudentInUse                   = errors.New("student has linked business records")
	ErrStudentAccountExists           = errors.New("student already has a login account")
	ErrStudentAccountInactive         = errors.New("inactive student cannot open a login account")
	ErrStudentAccountLegacyConflict   = errors.New("student has an unresolved legacy account binding")
	ErrGradeHasClasses                = errors.New("grade still has classes")
	ErrClassHasStudents               = errors.New("class still has students")
	ErrInvalidGradeLevel              = errors.New("grade level does not belong to education stage")
	ErrInvalidEntryYear               = errors.New("grade entry year is invalid")
	ErrInvalidEnrollmentDate          = errors.New("transfer date must not precede an active enrollment")
	ErrGradeCohortExists              = errors.New("grade cohort already exists")
	ErrGradeLevelYearMismatch         = errors.New("grade level does not match entry year")
	ErrGradeLevelConflict             = errors.New("current grade level is occupied by another cohort")
	ErrGradeEntryYearFuture           = errors.New("entry cohort has not started")
	ErrGradeCohortGraduated           = errors.New("entry cohort is no longer a current grade")
	ErrGradeAcademicYearMismatch      = errors.New("grade creation requires the current school year")
	ErrGradeHistoricalRecordImmutable = errors.New("historical annual grade cannot change level")
)

type Tenant struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Status string `json:"status"`
}

type TenantProvision struct {
	Name             string
	Code             string
	Status           string
	AdminUsername    string
	AdminDisplayName string
	PasswordHash     string
}

type School struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id"`
	Name            string   `json:"name"`
	Code            string   `json:"code"`
	EducationStages []string `json:"education_stages"`
	Status          string   `json:"status"`
}

type AcademicYear struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	SchoolID  string `json:"school_id"`
	Name      string `json:"name"`
	StartYear int    `json:"start_year"`
	EndYear   int    `json:"end_year"`
	StartsAt  string `json:"starts_at"`
	EndsAt    string `json:"ends_at"`
	IsCurrent bool   `json:"is_current"`
	Status    string `json:"status"`
}

type GradeCohort struct {
	ID                     string `json:"id"`
	TenantID               string `json:"tenant_id"`
	SchoolID               string `json:"school_id"`
	EducationStage         string `json:"education_stage"`
	EntryYear              int    `json:"entry_year"`
	ExpectedGraduationYear int    `json:"expected_graduation_year"`
	Name                   string `json:"name"`
	Status                 string `json:"status"`
}

type Grade struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	SchoolID       string `json:"school_id"`
	Name           string `json:"name"`
	LevelNo        int    `json:"level_no"`
	EntryYear      int    `json:"entry_year"`
	AcademicYear   string `json:"academic_year"`
	EducationStage string `json:"education_stage"`
	AcademicYearID string `json:"academic_year_id"`
	GradeCohortID  string `json:"grade_cohort_id"`
	Status         string `json:"status"`
}

type Class struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	SchoolID       string `json:"school_id"`
	GradeID        string `json:"grade_id"`
	AcademicYearID string `json:"academic_year_id"`
	GradeCohortID  string `json:"grade_cohort_id"`
	ClassNo        int    `json:"class_no,omitempty"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	Status         string `json:"status"`
}

type Student struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	SchoolID           string `json:"school_id"`
	ClassID            string `json:"class_id"`
	AcademicYearID     string `json:"academic_year_id,omitempty"`
	GradeCohortID      string `json:"grade_cohort_id,omitempty"`
	AdmissionYear      int    `json:"admission_year,omitempty"`
	StudentNo          string `json:"student_no"`
	Name               string `json:"name"`
	Gender             string `json:"gender,omitempty"`
	Status             string `json:"status"`
	AccountStatus      string `json:"account_status"`
	LoginAccount       string `json:"login_account,omitempty"`
	LoginSchoolCode    string `json:"login_school_code,omitempty"`
	MustChangePassword bool   `json:"must_change_password"`
}

// GradePatch 只调整显示名称和本学段的当前年级，固定届别、学校和历史学年不随之重算。
type GradePatch struct {
	Name    *string `json:"name"`
	LevelNo *int    `json:"level_no"`
}

type ClassPatch struct {
	Name *string `json:"name"`
	Code *string `json:"code"`
}

// StudentPatch 未传字段保持原值；Gender 的空串表示主动清空性别。
type StudentPatch struct {
	Name      *string `json:"name"`
	StudentNo *string `json:"student_no"`
	Gender    *string `json:"gender"`
	Status    *string `json:"status"`
}

type StudentEnrollment struct {
	ID               string `json:"id"`
	StudentID        string `json:"student_id"`
	SchoolID         string `json:"school_id"`
	AcademicYearID   string `json:"academic_year_id"`
	AcademicYearName string `json:"academic_year_name"`
	GradeCohortID    string `json:"grade_cohort_id"`
	GradeCohortName  string `json:"grade_cohort_name"`
	ClassID          string `json:"class_id"`
	ClassName        string `json:"class_name"`
	Status           string `json:"status"`
	StartDate        string `json:"start_date"`
	EndDate          string `json:"end_date,omitempty"`
}

type StudentListFilter struct {
	ClassID         string
	StudentIDs      []string
	RestrictClasses bool
	ClassIDs        []string
	StudentID       string
	Query           string
	Limit           int
	CursorStudentNo string
	CursorID        string
}

type TenantListFilter struct {
	Query      string
	Limit      int
	CursorCode string
	CursorID   string
}

type CSVImportError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type CSVImportResult struct {
	Created   int              `json:"created"`
	Skipped   int              `json:"skipped"`
	Committed bool             `json:"committed"`
	Errors    []CSVImportError `json:"errors"`
}

type Store interface {
	CreateTenant(ctx context.Context, input TenantProvision) (Tenant, error)
	ListTenants(ctx context.Context, tenantID string, canListAll bool, filter TenantListFilter) ([]Tenant, error)
	UpdateTenantStatus(ctx context.Context, id string, status string) (Tenant, error)
	CreateSchool(ctx context.Context, tenantID string, input School) (School, error)
	ListSchools(ctx context.Context, tenantID string) ([]School, error)
	ListAcademicYears(ctx context.Context, tenantID string, schoolID string) ([]AcademicYear, error)
	ListGradeCohorts(ctx context.Context, tenantID string, schoolID string) ([]GradeCohort, error)
	CreateGrade(ctx context.Context, tenantID string, input Grade) (Grade, error)
	UpdateGrade(ctx context.Context, tenantID string, id string, input GradePatch) (Grade, error)
	DeleteGrade(ctx context.Context, tenantID string, id string) error
	ListGrades(ctx context.Context, tenantID string, schoolID string) ([]Grade, error)
	CreateClass(ctx context.Context, tenantID string, input Class) (Class, error)
	UpdateClass(ctx context.Context, tenantID string, id string, input ClassPatch) (Class, error)
	DeleteClass(ctx context.Context, tenantID string, id string) error
	ListClasses(ctx context.Context, tenantID string, gradeID string) ([]Class, error)
	CreateStudent(ctx context.Context, tenantID string, input Student) (Student, error)
	UpdateStudent(ctx context.Context, tenantID string, id string, input StudentPatch) (Student, error)
	DeleteStudent(ctx context.Context, tenantID string, id string) error
	ListStudents(ctx context.Context, tenantID string, filter StudentListFilter) ([]Student, error)
	UpdateStudentStatus(ctx context.Context, tenantID string, id string, status string) (Student, error)
	TransferStudent(ctx context.Context, tenantID string, studentID string, classID string, startDate string) (StudentEnrollment, error)
	ListStudentEnrollments(ctx context.Context, tenantID string, studentID string) ([]StudentEnrollment, error)
}
