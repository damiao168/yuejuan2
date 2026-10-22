package auth

import (
	"context"
	"errors"
	"fmt"
)

// ExamCollectionAccess 将认证范围带到无路径对象 ID 的集合查询；客户端筛选只能缩小这个范围。
// Scope 为空只留给可信内部维护调用，HTTP 集合必须显式派生；内存存储可使用身份存储的边界解析器。
type ExamCollectionAccess struct {
	Scope    *AccessScope
	Resolver ResourceBoundaryResolver
}

func (a ExamCollectionAccess) Validate(tenantID string) error {
	if a.Scope != nil && (a.Scope.TenantID != tenantID || a.Scope.ActorID == "") {
		return ErrForbidden
	}
	return nil
}

func (a ExamCollectionAccess) Allows(ctx context.Context, tenantID, resourceType, resourceID, examID string) (bool, error) {
	if err := a.Validate(tenantID); err != nil {
		return false, err
	}
	if a.Scope == nil {
		return true, nil
	}
	boundary := ResourceBoundary{TenantID: tenantID, ResourceType: resourceType, ResourceID: resourceID, ExamID: examID}
	if a.Resolver != nil {
		resolved, err := a.Resolver.ResolveResourceBoundary(ctx, *a.Scope, resourceType, resourceID)
		if errors.Is(err, ErrResourceBoundaryNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		boundary = resolved
	}
	return AllowsResourceBoundary(*a.Scope, boundary), nil
}

// SQLArgs 的四项由认证 Scope 取得，不能合入请求 exam_id；在排序及 LIMIT 前使用同一谓词。
func (a ExamCollectionAccess) SQLArgs(tenantID string) ([]any, error) {
	if err := a.Validate(tenantID); err != nil {
		return nil, err
	}
	if a.Scope == nil {
		return []any{true, []string{}, []string{}, []string{}}, nil
	}
	s := *a.Scope
	wide := AllowsResourceBoundary(s, ResourceBoundary{TenantID: tenantID})
	schools := []string{}
	if s.IsSchoolWide() {
		schools = append(schools, s.SchoolIDs...)
	}
	return []any{wide, schools, append([]string{}, s.ClassIDs...), append([]string{}, s.ExamIDs...)}, nil
}

// ExamCollectionPredicate 的列名和占位起点由源码固定，不接收客户端表达式。
func ExamCollectionPredicate(examColumn string, start int) string {
	return fmt.Sprintf(`($%d OR EXISTS(SELECT 1 FROM exam collection_exam WHERE collection_exam.tenant_id=$1::uuid AND collection_exam.id=%s AND collection_exam.deleted_at IS NULL AND (collection_exam.school_id::text=ANY($%d::text[]) OR collection_exam.id::text=ANY($%d::text[]) OR EXISTS(SELECT 1 FROM exam_class collection_class WHERE collection_class.tenant_id=collection_exam.tenant_id AND collection_class.exam_id=collection_exam.id AND collection_class.deleted_at IS NULL AND collection_class.class_id::text=ANY($%d::text[])))))`, start, examColumn, start+1, start+3, start+2)
}
