package auth

// IsPlatformWorker 识别平台所有、可处理学校租户后台任务的服务账户。产品用户不会获得此角色，平台管理员也不例外。
func IsPlatformWorker(user User) bool {
	return user.TenantID == PlatformTenantID && HasRole(user, "page_processing_worker")
}

// ScopedStudentID 从内存测试使用的扁平 data_scope 或 PostgreSQL RBAC 关联返回的按角色索引 data_scope 中取得学生 ID。
func ScopedStudentID(user User) (string, bool) {
	return studentIDFromScope(user.DataScope)
}

func studentIDFromScope(scope map[string]any) (string, bool) {
	if len(scope) == 0 {
		return "", false
	}
	if value, ok := scope["student_id"]; ok {
		if studentID, ok := value.(string); ok && studentID != "" {
			return studentID, true
		}
	}
	for _, value := range scope {
		nested, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if studentID, ok := studentIDFromScope(nested); ok {
			return studentID, true
		}
	}
	return "", false
}
