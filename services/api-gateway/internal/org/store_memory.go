package org

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

type MemoryStore struct {
	mu       sync.RWMutex
	next     int
	tenants  map[string]Tenant
	schools  map[string]School
	grades   map[string]Grade
	classes  map[string]Class
	students map[string]Student
	bindings map[string]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:     1,
		tenants:  map[string]Tenant{},
		schools:  map[string]School{},
		grades:   map[string]Grade{},
		classes:  map[string]Class{},
		students: map[string]Student{},
		bindings: map[string]bool{},
	}
}

func (s *MemoryStore) CreateTenant(_ context.Context, input TenantProvision) (Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := Tenant{ID: s.id("tenant"), Name: input.Name, Code: input.Code, Status: input.Status}
	if item.Status == "" {
		item.Status = "active"
	}
	s.tenants[item.ID] = item
	return item, nil
}

// 非平台用户只能看到自己的租户；内存实现也保留这个租户隔离规则以对齐生产库。
func (s *MemoryStore) ListTenants(_ context.Context, tenantID string, canListAll bool, filter TenantListFilter) ([]Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Tenant{}
	for _, item := range s.tenants {
		if (canListAll || item.ID == tenantID) && (filter.Query == "" ||
			strings.Contains(strings.ToLower(item.Name), strings.ToLower(filter.Query)) ||
			strings.Contains(strings.ToLower(item.Code), strings.ToLower(filter.Query))) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code == out[j].Code {
			return out[i].ID < out[j].ID
		}
		return out[i].Code < out[j].Code
	})
	if filter.CursorCode != "" && filter.CursorID != "" {
		start := 0
		for start < len(out) && (out[start].Code < filter.CursorCode ||
			(out[start].Code == filter.CursorCode && out[start].ID <= filter.CursorID)) {
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) UpdateTenantStatus(_ context.Context, id string, status string) (Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.tenants[id]
	if !ok {
		return Tenant{}, fmt.Errorf("tenant not found")
	}
	item.Status = status
	s.tenants[id] = item
	return item, nil
}

func (s *MemoryStore) CreateSchool(_ context.Context, tenantID string, input School) (School, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.schools {
		if current.TenantID == tenantID && current.Code == input.Code {
			return School{}, ErrSchoolCodeConflict
		}
	}
	input.ID = s.id("school")
	input.TenantID = tenantID
	if input.Status == "" {
		input.Status = "active"
	}
	s.schools[input.ID] = input
	return input, nil
}

func (s *MemoryStore) ListSchools(_ context.Context, tenantID string) ([]School, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []School{}
	for _, item := range s.schools {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) ListAcademicYears(_ context.Context, tenantID, schoolID string) ([]AcademicYear, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	out := []AcademicYear{}
	for _, grade := range s.grades {
		if grade.TenantID != tenantID || (schoolID != "" && grade.SchoolID != schoolID) || seen[grade.AcademicYear] {
			continue
		}
		seen[grade.AcademicYear] = true
		out = append(out, AcademicYear{ID: grade.AcademicYearID, TenantID: tenantID, SchoolID: grade.SchoolID, Name: grade.AcademicYear, Status: "active"})
	}
	return out, nil
}

func (s *MemoryStore) ListGradeCohorts(_ context.Context, tenantID, schoolID string) ([]GradeCohort, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	out := []GradeCohort{}
	for _, grade := range s.grades {
		key := grade.SchoolID + "|" + grade.GradeCohortID
		if grade.TenantID != tenantID || (schoolID != "" && grade.SchoolID != schoolID) || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, GradeCohort{ID: grade.GradeCohortID, TenantID: tenantID, SchoolID: grade.SchoolID, EducationStage: grade.EducationStage, Name: grade.Name, Status: "active"})
	}
	return out, nil
}

func (s *MemoryStore) CreateGrade(_ context.Context, tenantID string, input Grade) (Grade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input.ID = s.id("grade")
	input.TenantID = tenantID
	if input.Status == "" {
		input.Status = "active"
	}
	if input.EducationStage == "" {
		input.EducationStage = educationStageForLevel(input.LevelNo)
	}
	s.grades[input.ID] = input
	return input, nil
}

func educationStageForLevel(level int) string {
	if level >= 10 {
		return "senior"
	}
	return "junior"
}

func (s *MemoryStore) ListGrades(_ context.Context, tenantID string, schoolID string) ([]Grade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Grade{}
	for _, item := range s.grades {
		if item.TenantID == tenantID && (schoolID == "" || item.SchoolID == schoolID) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateClass(_ context.Context, tenantID string, input Class) (Class, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.classes {
		if current.TenantID == tenantID && current.GradeID == input.GradeID && current.Code == input.Code {
			return Class{}, ErrClassCodeConflict
		}
	}
	input.ID = s.id("class")
	input.TenantID = tenantID
	if input.Status == "" {
		input.Status = "active"
	}
	s.classes[input.ID] = input
	return input, nil
}

func (s *MemoryStore) ListClasses(_ context.Context, tenantID string, gradeID string) ([]Class, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Class{}
	for _, item := range s.classes {
		if item.TenantID == tenantID && (gradeID == "" || item.GradeID == gradeID) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateStudent(_ context.Context, tenantID string, input Student) (Student, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.students {
		if current.TenantID == tenantID && current.SchoolID == input.SchoolID && current.StudentNo == input.StudentNo {
			return Student{}, ErrStudentNoConflict
		}
	}
	if input.ID == "" {
		input.ID = s.id("student")
	}
	input.TenantID = tenantID
	if input.Status == "" {
		input.Status = "active"
	}
	s.students[input.ID] = input
	return input, nil
}

func (s *MemoryStore) ListStudents(_ context.Context, tenantID string, filter StudentListFilter) ([]Student, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make(map[string]struct{}, len(filter.StudentIDs))
	for _, id := range filter.StudentIDs {
		ids[id] = struct{}{}
	}
	out := []Student{}
	for _, item := range s.students {
		if item.TenantID != tenantID || (filter.ClassID != "" && item.ClassID != filter.ClassID) {
			continue
		}
		if filter.RestrictClasses && !slices.Contains(filter.ClassIDs, item.ClassID) && (filter.StudentID == "" || filter.StudentID != item.ID) {
			continue
		}
		if len(ids) > 0 {
			if _, ok := ids[item.ID]; !ok {
				continue
			}
		}
		query := strings.ToLower(filter.Query)
		if query != "" && !strings.Contains(strings.ToLower(item.StudentNo), query) && !strings.Contains(strings.ToLower(item.Name), query) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StudentNo == out[j].StudentNo {
			return out[i].ID < out[j].ID
		}
		return out[i].StudentNo < out[j].StudentNo
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.StudentNo > filter.CursorStudentNo || (item.StudentNo == filter.CursorStudentNo && item.ID > filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) UpdateStudentStatus(_ context.Context, tenantID string, id string, status string) (Student, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.students[id]
	if !ok || item.TenantID != tenantID {
		return Student{}, fmt.Errorf("student not found")
	}
	item.Status = status
	s.students[id] = item
	return item, nil
}

func (s *MemoryStore) TransferStudent(_ context.Context, tenantID, studentID, classID, startDate string) (StudentEnrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	student, ok := s.students[studentID]
	if !ok || student.TenantID != tenantID {
		return StudentEnrollment{}, fmt.Errorf("student not found")
	}
	class, ok := s.classes[classID]
	if !ok || class.TenantID != tenantID || class.SchoolID != student.SchoolID {
		return StudentEnrollment{}, ErrInvalidParent
	}
	student.ClassID = classID
	student.AcademicYearID = class.AcademicYearID
	student.GradeCohortID = class.GradeCohortID
	s.students[studentID] = student
	return StudentEnrollment{ID: s.id("enrollment"), StudentID: studentID, SchoolID: student.SchoolID, AcademicYearID: class.AcademicYearID, GradeCohortID: class.GradeCohortID, ClassID: classID, ClassName: class.Name, Status: "enrolled", StartDate: startDate}, nil
}

func (s *MemoryStore) ListStudentEnrollments(_ context.Context, tenantID, studentID string) ([]StudentEnrollment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	student, ok := s.students[studentID]
	if !ok || student.TenantID != tenantID {
		return nil, fmt.Errorf("student not found")
	}
	class := s.classes[student.ClassID]
	return []StudentEnrollment{{StudentID: studentID, SchoolID: student.SchoolID, AcademicYearID: class.AcademicYearID, GradeCohortID: class.GradeCohortID, ClassID: class.ID, ClassName: class.Name, Status: "enrolled"}}, nil
}

func (s *MemoryStore) BindTeacherClass(_ context.Context, tenantID string, teacherID string, classID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[tenantID+"|"+teacherID+"|"+classID] = true
	return nil
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}
