package auth

import (
	"context"
	"strings"
)

func (s *PostgresStore) ResolveAccessScope(ctx context.Context, user User) (AccessScope, error) {
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		return AccessScope{}, err
	}
	if scope.IsPlatform || scope.TenantWide {
		return scope, nil
	}

	kinds := declaredRelationshipKinds(user.DataScope)
	if IsReviewTeacherRoles(user.Roles) {
		kinds = map[string]bool{"exam_task": true}
	}
	if kinds["school"] {
		var schoolIDs, gradeIDs, classIDs, examIDs []string
		err = s.db.QueryRowContext(ctx, `
WITH scoped_school AS (
  SELECT u.school_id::text AS id FROM app_user u
  WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.school_id IS NOT NULL AND u.deleted_at IS NULL
  UNION SELECT unnest(string_to_array(NULLIF($3,''), ','))
)
SELECT
  COALESCE((SELECT array_agg(DISTINCT id) FROM scoped_school WHERE id<>''), '{}'),
  COALESCE((SELECT array_agg(DISTINCT g.id::text) FROM grade g JOIN scoped_school ss ON ss.id=g.school_id::text WHERE g.tenant_id=$1::uuid AND g.deleted_at IS NULL), '{}'),
  COALESCE((SELECT array_agg(DISTINCT c.id::text) FROM school_class c JOIN scoped_school ss ON ss.id=c.school_id::text WHERE c.tenant_id=$1::uuid AND c.deleted_at IS NULL), '{}'),
  COALESCE((SELECT array_agg(DISTINCT e.id::text) FROM exam e JOIN scoped_school ss ON ss.id=e.school_id::text WHERE e.tenant_id=$1::uuid AND e.deleted_at IS NULL), '{}')
`, user.TenantID, user.ID, strings.Join(scope.schoolManagementIDs, ",")).Scan(pqArray(&schoolIDs), pqArray(&gradeIDs), pqArray(&classIDs), pqArray(&examIDs))
		if err != nil {
			return AccessScope{}, err
		}
		scope.schoolManagementIDs = append(scope.schoolManagementIDs, schoolIDs...)
		scope.SchoolIDs = append(scope.SchoolIDs, schoolIDs...)
		scope.GradeIDs = append(scope.GradeIDs, gradeIDs...)
		scope.ClassIDs = append(scope.ClassIDs, classIDs...)
		scope.ExamIDs = append(scope.ExamIDs, examIDs...)
	}
	if (kinds["assigned"] || kinds["exam_task"]) && !scope.RestrictsToManagedSchools() {
		var reviewTaskIDs, arbitrationTaskIDs, examIDs, submissionIDs []string
		err = s.db.QueryRowContext(ctx, `
SELECT
  COALESCE((SELECT array_agg(rt.id::text) FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.assigned_to=$2::uuid AND rt.status IN ('assigned','in_progress','returned') AND rt.deleted_at IS NULL), '{}'),
  COALESCE((SELECT array_agg(at.id::text) FROM arbitration_task at WHERE at.tenant_id=$1::uuid AND at.assigned_to=$2::uuid AND at.status IN ('assigned','in_progress') AND at.deleted_at IS NULL), '{}'),
  COALESCE((SELECT array_agg(DISTINCT x.exam_id) FROM (SELECT rt.exam_id::text exam_id FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.assigned_to=$2::uuid AND rt.status IN ('assigned','in_progress','returned') AND rt.deleted_at IS NULL UNION SELECT at.exam_id::text FROM arbitration_task at WHERE at.tenant_id=$1::uuid AND at.assigned_to=$2::uuid AND at.status IN ('assigned','in_progress') AND at.deleted_at IS NULL) x), '{}'),
  COALESCE((SELECT array_agg(DISTINCT x.submission_id) FROM (SELECT rt.submission_id::text submission_id FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.assigned_to=$2::uuid AND rt.status IN ('assigned','in_progress','returned') AND rt.deleted_at IS NULL UNION SELECT at.submission_id::text FROM arbitration_task at WHERE at.tenant_id=$1::uuid AND at.assigned_to=$2::uuid AND at.status IN ('assigned','in_progress') AND at.deleted_at IS NULL) x), '{}')
`, user.TenantID, user.ID).Scan(pqArray(&reviewTaskIDs), pqArray(&arbitrationTaskIDs), pqArray(&examIDs), pqArray(&submissionIDs))
		if err != nil {
			return AccessScope{}, err
		}
		scope.ReviewTaskIDs = append(scope.ReviewTaskIDs, reviewTaskIDs...)
		scope.ArbitrationTaskIDs = append(scope.ArbitrationTaskIDs, arbitrationTaskIDs...)
		scope.ExamIDs = append(scope.ExamIDs, examIDs...)
		scope.SubmissionIDs = append(scope.SubmissionIDs, submissionIDs...)
	}
	return scope.normalized(), nil
}

func declaredRelationshipKinds(dataScope map[string]any) map[string]bool {
	out := map[string]bool{}
	var visit func(map[string]any)
	visit = func(scope map[string]any) {
		if kind, ok := scope["scope"].(string); ok {
			out[strings.TrimSpace(kind)] = true
		}
		for _, raw := range scope {
			if nested, ok := raw.(map[string]any); ok {
				visit(nested)
			}
		}
	}
	visit(dataScope)
	return out
}
