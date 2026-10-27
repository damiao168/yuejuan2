package regrade

import "edugrade-enterprise/services/api-gateway/internal/auth"

// 省略范围仅保留可信进程内调用；HTTP List 必须传入已认证作用域。
func jobCollectionAccess(accesses []auth.ExamCollectionAccess) auth.ExamCollectionAccess {
	if len(accesses) > 0 {
		return accesses[0]
	}
	return auth.ExamCollectionAccess{}
}
