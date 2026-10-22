package org

import "context"

func (s *MemoryStore) UpdateGrade(_ context.Context, tenantID, id string, input GradePatch) (Grade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.grades[id]
	if !ok || item.TenantID != tenantID {
		return Grade{}, ErrNotFound
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	s.grades[id] = item
	return item, nil
}

func (s *MemoryStore) UpdateClass(_ context.Context, tenantID, id string, input ClassPatch) (Class, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.classes[id]
	if !ok || item.TenantID != tenantID {
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
