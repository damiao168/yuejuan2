package org

import (
	"context"
	"slices"
	"strings"
)

type OnboardingReadinessSummary struct {
	HasSchool            bool
	HasTeachingStructure bool
	HasStudents          bool
}

type OnboardingReadinessStore interface {
	HasManagedTenant(ctx context.Context) (bool, error)
	OnboardingReadiness(ctx context.Context, tenantID string, schoolIDs []string, tenantWide bool) (OnboardingReadinessSummary, error)
}

func (s *PostgresStore) HasManagedTenant(ctx context.Context) (bool, error) {
	var ready bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM tenant
  WHERE id <> $1::uuid AND code <> 'platform' AND status = 'active' AND deleted_at IS NULL
)`, onboardingPlatformTenantID).Scan(&ready)
	return ready, err
}

func (s *PostgresStore) OnboardingReadiness(ctx context.Context, tenantID string, schoolIDs []string, tenantWide bool) (OnboardingReadinessSummary, error) {
	var summary OnboardingReadinessSummary
	err := s.db.QueryRowContext(ctx, `
WITH allowed_school AS (
  SELECT id
  FROM school
  WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND status = 'active'
    AND ($2 OR id::text = ANY(string_to_array($3, ',')))
)
SELECT
  EXISTS (SELECT 1 FROM allowed_school),
  EXISTS (
    SELECT 1 FROM grade g JOIN allowed_school s ON s.id = g.school_id
    WHERE g.tenant_id = $1::uuid AND g.deleted_at IS NULL AND g.status = 'active'
  ) AND EXISTS (
    SELECT 1 FROM school_class c JOIN allowed_school s ON s.id = c.school_id
    WHERE c.tenant_id = $1::uuid AND c.deleted_at IS NULL AND c.status = 'active'
  ),
  EXISTS (
    SELECT 1 FROM student st JOIN allowed_school s ON s.id = st.school_id
    WHERE st.tenant_id = $1::uuid AND st.deleted_at IS NULL AND st.status = 'active'
  )
`, tenantID, tenantWide, strings.Join(schoolIDs, ",")).Scan(&summary.HasSchool, &summary.HasTeachingStructure, &summary.HasStudents)
	return summary, err
}

func (s *MemoryStore) HasManagedTenant(_ context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tenant := range s.tenants {
		if tenant.ID != onboardingPlatformTenantID && tenant.Code != "platform" && tenant.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) OnboardingReadiness(_ context.Context, tenantID string, schoolIDs []string, tenantWide bool) (OnboardingReadinessSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	allowsSchool := func(schoolID string) bool { return tenantWide || slices.Contains(schoolIDs, schoolID) }
	var summary OnboardingReadinessSummary
	for _, school := range s.schools {
		if school.TenantID == tenantID && school.Status == "active" && allowsSchool(school.ID) {
			summary.HasSchool = true
			break
		}
	}
	hasGrade := false
	for _, grade := range s.grades {
		if grade.TenantID == tenantID && grade.Status == "active" && allowsSchool(grade.SchoolID) {
			hasGrade = true
			break
		}
	}
	hasClass := false
	for _, class := range s.classes {
		if class.TenantID == tenantID && class.Status == "active" && allowsSchool(class.SchoolID) {
			hasClass = true
			break
		}
	}
	summary.HasTeachingStructure = hasGrade && hasClass
	for _, student := range s.students {
		if student.TenantID == tenantID && student.Status == "active" && allowsSchool(student.SchoolID) {
			summary.HasStudents = true
			break
		}
	}
	return summary, nil
}

const onboardingPlatformTenantID = "00000000-0000-0000-0000-000000000001"
