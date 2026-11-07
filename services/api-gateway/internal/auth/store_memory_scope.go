package auth

import (
	"context"
)

func (s *MemoryStore) AddResourceBoundary(resourceType, resourceID string, boundary ResourceBoundary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boundary.ResourceType = resourceType
	boundary.ResourceID = resourceID
	s.boundaries[resourceType+"|"+resourceID] = boundary
}

func (s *MemoryStore) ResolveResourceBoundary(_ context.Context, scope AccessScope, resourceType, resourceID string) (ResourceBoundary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if boundary, ok := s.boundaries[resourceType+"|"+resourceID]; ok {
		return boundary, nil
	}
	boundary := ResourceBoundary{ResourceType: resourceType, ResourceID: resourceID, TenantID: scope.TenantID}
	if scope.syntheticUnbounded {
		return boundary, nil
	}
	if !allowsMemoryResource(scope, resourceType, resourceID) {
		return ResourceBoundary{}, ErrResourceBoundaryNotFound
	}
	switch resourceType {
	case "exam":
		boundary.ExamID = resourceID
	case "submission":
		boundary.SubmissionID = resourceID
	case "review_task", "arbitration_task":
		boundary.AssignedTo = scope.ActorID
	case "student":
		boundary.StudentID = resourceID
	}
	return boundary, nil
}

func allowsMemoryResource(scope AccessScope, resourceType, resourceID string) bool {
	if scope.TenantWide {
		return true
	}
	switch resourceType {
	case "exam":
		return scope.AllowsExam(resourceID)
	case "submission":
		return scope.AllowsSubmission(resourceID)
	case "file_asset":
		return scope.AllowsFile(resourceID)
	case "review_task":
		return scope.AllowsReviewTask(resourceID)
	case "arbitration_task":
		return scope.AllowsArbitrationTask(resourceID)
	case "student":
		return scope.AllowsStudent(resourceID)
	default:
		return false
	}
}

func (s *MemoryStore) ResolveAccessScope(_ context.Context, user User) (AccessScope, error) {
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		return AccessScope{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if scope.schoolWide {
		// 与数据库解析器一致，只把账户绑定的学校加入管理范围；分派与班级导航不会升级为学校管理权。
		for _, stored := range s.users {
			if stored.TenantID == user.TenantID && stored.ID == user.ID && stored.Status != "deleted" && stored.SchoolID != "" {
				scope.schoolManagementIDs = append(scope.schoolManagementIDs, stored.SchoolID)
				scope.SchoolIDs = append(scope.SchoolIDs, stored.SchoolID)
			}
		}
	}
	if scope.RestrictsToManagedSchools() {
		// 内存模式只从已登记的可信对象边界补齐本校资源；旧角色中的外校导航不再参与读取。
		scope.SchoolIDs = scope.ManagedSchoolIDs()
		for _, boundary := range s.boundaries {
			if boundary.TenantID != user.TenantID || !scope.ManagesSchool(boundary.SchoolID) {
				continue
			}
			if boundary.ExamID != "" {
				scope.ExamIDs = append(scope.ExamIDs, boundary.ExamID)
			}
			if boundary.SubmissionID != "" {
				scope.SubmissionIDs = append(scope.SubmissionIDs, boundary.SubmissionID)
			}
			if boundary.ResourceType == "review_task" {
				scope.ReviewTaskIDs = append(scope.ReviewTaskIDs, boundary.ResourceID)
			}
			if boundary.ResourceType == "arbitration_task" {
				scope.ArbitrationTaskIDs = append(scope.ArbitrationTaskIDs, boundary.ResourceID)
			}
		}
	}
	// 合成单元测试夹具可显式请求租户范围的内存边界；生产作用域不会取得这一测试专用扩展。
	if scopeDeclaresSynthetic(user.DataScope) {
		scope.syntheticUnbounded = true
	}
	return scope.normalized(), nil
}

func scopeDeclaresSynthetic(scope map[string]any) bool {
	for key, raw := range scope {
		if key == "synthetic" {
			value, _ := raw.(bool)
			if value {
				return true
			}
		}
		if nested, ok := raw.(map[string]any); ok && scopeDeclaresSynthetic(nested) {
			return true
		}
	}
	return false
}

func firstScopeID(scope map[string]any, key string) string {
	if value, ok := scope[key].(string); ok {
		return value
	}
	for _, raw := range scope {
		if nested, ok := raw.(map[string]any); ok {
			if value := firstScopeID(nested, key); value != "" {
				return value
			}
		}
	}
	return ""
}
