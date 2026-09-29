package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type PostgresOrganizationSummaryStore struct {
	db *sql.DB
}

func NewPostgresOrganizationSummaryStore(db *sql.DB) *PostgresOrganizationSummaryStore {
	return &PostgresOrganizationSummaryStore{db: db}
}

func (s *PostgresOrganizationSummaryStore) DashboardOrganizationSummary(ctx context.Context, tenantID string, scope auth.AccessScope) (OrganizationStatistics, error) {
	if s == nil || s.db == nil {
		return OrganizationStatistics{}, fmt.Errorf("dashboard organization database is unavailable")
	}
	result := OrganizationStatistics{}
	// 每个组织计数都先带租户条件，再追加学校/年级/班级范围，避免跨范围统计。
queries := []struct {
		target *int
		base   string
		filter scopedColumns
	}{
		{&result.ActiveStudentCount, `SELECT COUNT(*) FROM student s JOIN school_class c ON c.tenant_id=s.tenant_id AND c.id=s.class_id WHERE s.tenant_id=$1::uuid AND s.deleted_at IS NULL AND s.status='active'`, scopedColumns{"s.school_id", "c.grade_id", "s.class_id"}},
		{&result.GradeCount, `SELECT COUNT(DISTINCT g.id) FROM grade g LEFT JOIN school_class c ON c.tenant_id=g.tenant_id AND c.grade_id=g.id AND c.deleted_at IS NULL WHERE g.tenant_id=$1::uuid AND g.deleted_at IS NULL AND g.status='active'`, scopedColumns{"g.school_id", "g.id", "c.id"}},
		{&result.ClassCount, `SELECT COUNT(*) FROM school_class c WHERE c.tenant_id=$1::uuid AND c.deleted_at IS NULL AND c.status='active'`, scopedColumns{"c.school_id", "c.grade_id", "c.id"}},
		{&result.EmptyClassCount, `SELECT COUNT(*) FROM school_class c WHERE c.tenant_id=$1::uuid AND c.deleted_at IS NULL AND c.status='active' AND NOT EXISTS (SELECT 1 FROM student s WHERE s.tenant_id=c.tenant_id AND s.class_id=c.id AND s.deleted_at IS NULL AND s.status='active')`, scopedColumns{"c.school_id", "c.grade_id", "c.id"}},
	}
	for _, query := range queries {
		statement, args := applyScope(query.base, tenantID, scope, query.filter)
		if err := s.db.QueryRowContext(ctx, statement, args...).Scan(query.target); err != nil {
			return OrganizationStatistics{}, err
		}
	}

	var err error
	result.TeacherCount, err = s.countUsersWithRole(ctx, tenantID, scope, "teacher", false)
	if err != nil {
		return OrganizationStatistics{}, err
	}
	result.GraderCount, err = s.countUsersWithRole(ctx, tenantID, scope, "grader", false)
	if err != nil {
		return OrganizationStatistics{}, err
	}
	result.UnassignedTeacherCount, err = s.countUsersWithRole(ctx, tenantID, scope, "teacher", true)
	if err != nil {
		return OrganizationStatistics{}, err
	}
	return result, nil
}

type scopedColumns struct {
	school string
	grade  string
	class  string
}

func applyScope(base string, tenantID string, scope auth.AccessScope, columns scopedColumns) (string, []any) {
	args := []any{tenantID}
	return appendScope(base, scope, columns, args)
}

func appendScope(base string, scope auth.AccessScope, columns scopedColumns, args []any) (string, []any) {
	statement := base
	if columns.school != "" && len(scope.SchoolIDs) > 0 {
		statement += " AND " + inClause(columns.school, scope.SchoolIDs, &args)
	}
	if columns.grade != "" && len(scope.GradeIDs) > 0 {
		statement += " AND " + inClause(columns.grade, scope.GradeIDs, &args)
	}
	if columns.class != "" && len(scope.ClassIDs) > 0 {
		statement += " AND " + inClause(columns.class, scope.ClassIDs, &args)
	}
	return statement, args
}

func inClause(column string, values []string, args *[]any) string {
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		*args = append(*args, value)
		placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(*args)))
	}
	return column + " IN (" + strings.Join(placeholders, ",") + ")"
}

func (s *PostgresOrganizationSummaryStore) countUsersWithRole(ctx context.Context, tenantID string, scope auth.AccessScope, role string, unassignedOnly bool) (int, error) {
	base := `SELECT COUNT(DISTINCT u.id) FROM app_user u JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL WHERE u.tenant_id=$1::uuid AND u.deleted_at IS NULL AND u.status='active' AND r.code=$2`
	args := []any{tenantID, role}
	if unassignedOnly {
		base += ` AND NOT EXISTS (SELECT 1 FROM teacher_class utc WHERE utc.tenant_id=u.tenant_id AND utc.teacher_id=u.id AND utc.deleted_at IS NULL)`
	}

	classScope := scopedColumns{"c.school_id", "c.grade_id", "c.id"}
	if len(scope.GradeIDs) > 0 || len(scope.ClassIDs) > 0 {
		inner, scopedArgs := appendScope(`SELECT 1 FROM teacher_class tc JOIN school_class c ON c.tenant_id=tc.tenant_id AND c.id=tc.class_id WHERE tc.tenant_id=u.tenant_id AND tc.teacher_id=u.id AND tc.deleted_at IS NULL AND c.deleted_at IS NULL`, scope, classScope, args)
		args = scopedArgs
		base += " AND EXISTS (" + inner + ")"
	} else if len(scope.SchoolIDs) > 0 {
		schoolUser := inClause("u.school_id", scope.SchoolIDs, &args)
		inner, scopedArgs := appendScope(`SELECT 1 FROM teacher_class tc JOIN school_class c ON c.tenant_id=tc.tenant_id AND c.id=tc.class_id WHERE tc.tenant_id=u.tenant_id AND tc.teacher_id=u.id AND tc.deleted_at IS NULL AND c.deleted_at IS NULL`, scope, classScope, args)
		args = scopedArgs
		base += " AND (" + schoolUser + " OR EXISTS (" + inner + "))"
	}

	var count int
	if err := s.db.QueryRowContext(ctx, base, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
