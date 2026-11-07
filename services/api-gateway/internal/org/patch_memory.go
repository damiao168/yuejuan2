package org

import (
	"context"
	"time"
)

func (s *MemoryStore) UpdateGrade(_ context.Context, tenantID, id string, input GradePatch) (Grade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.grades[id]
	if !ok || item.TenantID != tenantID || s.deletedGrades[id] {
		return Grade{}, ErrNotFound
	}
	var err error
	item, err = applyGradePatch(item, input)
	if err != nil {
		return Grade{}, err
	}
	if input.LevelNo != nil {
		rows := []Grade{}
		for _, grade := range s.grades {
			if grade.TenantID == tenantID && grade.SchoolID == item.SchoolID && !s.deletedGrades[grade.ID] {
				rows = append(rows, grade)
			}
		}
		if err = validateCurrentGradeConflict(item, rows, currentSchoolYearStart(time.Now()), false); err != nil {
			return Grade{}, err
		}
	}
	s.grades[id] = item
	return item, nil
}

func (s *MemoryStore) UpdateClass(_ context.Context, tenantID, id string, input ClassPatch) (Class, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.classes[id]
	if !ok || item.TenantID != tenantID || s.deletedClasses[id] {
		return Class{}, ErrNotFound
	}
	if input.Code != nil {
		// 唯一性检查和写入共用一把锁，同级班级不能同时改成相同代码。
		for otherID, other := range s.classes {
			if otherID != id && other.TenantID == tenantID && other.GradeID == item.GradeID && other.Code == *input.Code {
				return Class{}, ErrClassCodeConflict
			}
		}
		item.Code = *input.Code
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	s.classes[id] = item
	return item, nil
}

func (s *MemoryStore) UpdateStudent(_ context.Context, tenantID, id string, input StudentPatch) (Student, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.students[id]
	if !ok || item.TenantID != tenantID {
		return Student{}, ErrNotFound
	}
	if input.StudentNo != nil {
		for otherID, other := range s.students {
			if otherID != id && other.TenantID == tenantID && other.SchoolID == item.SchoolID && other.StudentNo == *input.StudentNo {
				return Student{}, ErrStudentNoConflict
			}
		}
		item.StudentNo = *input.StudentNo
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	if input.Gender != nil {
		item.Gender = *input.Gender
	}
	if input.Status != nil {
		item.Status = *input.Status
	}
	s.students[id] = item
	return item, nil
}
