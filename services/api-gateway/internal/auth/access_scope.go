package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

var (
	ErrAccessScopeMissing = errors.New("access scope is missing")
	ErrAccessScopeInvalid = errors.New("access scope is invalid")
)

// AccessScope 是服务端为已认证主体派生的数据边界，不得向其中加入来自用户请求头或请求体的 ID。
type AccessScope struct {
	TenantID           string
	ActorID            string
	IsPlatform         bool
	TenantWide         bool
	AssignedOnly       bool
	SchoolIDs          []string
	GradeIDs           []string
	ClassIDs           []string
	ExamIDs            []string
	SubmissionIDs      []string
	FileIDs            []string
	ReviewTaskIDs      []string
	ArbitrationTaskIDs []string
	StudentID          string
	// schoolWide 记录可信的学校级角色，并与 SchoolIDs 分开；班级级角色也会扩展到所属学校用于读取筛选，但不能因此取得学校级任务管理权。
	schoolWide bool
	// syntheticUnbounded 仅供显式声明的内存测试夹具使用。
	syntheticUnbounded bool
}

func (s AccessScope) OrganizationScope() OrganizationScope {
	normalized := s.normalized()
	return OrganizationScope{
		TenantWide: normalized.TenantWide || normalized.IsPlatform,
		SchoolIDs:  normalized.SchoolIDs,
		GradeIDs:   normalized.GradeIDs,
		ClassIDs:   normalized.ClassIDs,
	}
}

func (s AccessScope) AllowsSchool(id string) bool {
	return s.TenantWide || containsID(s.SchoolIDs, id)
}

func (s AccessScope) AllowsGrade(id string) bool {
	return s.TenantWide || containsID(s.GradeIDs, id)
}

func (s AccessScope) AllowsClass(id string) bool {
	return s.TenantWide || containsID(s.ClassIDs, id)
}

func (s AccessScope) AllowsExam(id string) bool {
	return s.TenantWide || containsID(s.ExamIDs, id)
}

func (s AccessScope) AllowsSubmission(id string) bool {
	return s.TenantWide || containsID(s.SubmissionIDs, id)
}

func (s AccessScope) AllowsFile(id string) bool {
	return s.TenantWide || containsID(s.FileIDs, id)
}

func (s AccessScope) AllowsReviewTask(id string) bool {
	return s.TenantWide || containsID(s.ReviewTaskIDs, id)
}

func (s AccessScope) AllowsArbitrationTask(id string) bool {
	return s.TenantWide || containsID(s.ArbitrationTaskIDs, id)
}

func (s AccessScope) AllowsStudent(id string) bool {
	return s.TenantWide || (s.StudentID != "" && s.StudentID == id)
}

func (s AccessScope) HasDataAccess() bool {
	return s.IsPlatform || s.TenantWide || s.AssignedOnly || s.StudentID != "" ||
		len(s.SchoolIDs) > 0 || len(s.GradeIDs) > 0 || len(s.ClassIDs) > 0 ||
		len(s.ExamIDs) > 0 || len(s.SubmissionIDs) > 0 || len(s.FileIDs) > 0 ||
		len(s.ReviewTaskIDs) > 0 || len(s.ArbitrationTaskIDs) > 0
}

// IsSchoolWide 判断作用域是否来自可信的学校级角色。它不暴露底层字段，避免调用方从请求数据伪造全校作用域。
func (s AccessScope) IsSchoolWide() bool { return s.schoolWide }

// QueryMode 返回仓储进行集合筛选时使用的粗粒度模式。集合端点除直接资源边界中间件外，还必须应用此模式，否则受限身份仍可能从列表取得其他学校的记录。
func (s AccessScope) QueryMode() string {
	if s.IsPlatform {
		return "platform"
	}
	if s.TenantWide {
		return "tenant"
	}
	if s.schoolWide {
		return "school"
	}
	if s.AssignedOnly {
		return "assigned"
	}
	if len(s.ClassIDs) > 0 || len(s.ExamIDs) > 0 {
		return "class"
	}
	return "none"
}

func (s AccessScope) normalized() AccessScope {
	s.SchoolIDs = uniqueSortedIDs(s.SchoolIDs)
	s.GradeIDs = uniqueSortedIDs(s.GradeIDs)
	s.ClassIDs = uniqueSortedIDs(s.ClassIDs)
	s.ExamIDs = uniqueSortedIDs(s.ExamIDs)
	s.SubmissionIDs = uniqueSortedIDs(s.SubmissionIDs)
	s.FileIDs = uniqueSortedIDs(s.FileIDs)
	s.ReviewTaskIDs = uniqueSortedIDs(s.ReviewTaskIDs)
	s.ArbitrationTaskIDs = uniqueSortedIDs(s.ArbitrationTaskIDs)
	return s
}

// ResolveDeclaredAccessScope 严格解析持久化 RBAC data_scope；
// 通过关联关系派生的 ID 由 Store 解析器添加，不能由调用方添加。
func ResolveDeclaredAccessScope(user User) (AccessScope, error) {
	if strings.TrimSpace(user.TenantID) == "" || strings.TrimSpace(user.ID) == "" {
		return AccessScope{}, ErrAccessScopeMissing
	}
	out := AccessScope{TenantID: user.TenantID, ActorID: user.ID}
	if len(user.DataScope) == 0 {
		return AccessScope{}, ErrAccessScopeMissing
	}

	scopes, err := declaredRoleScopes(user)
	if err != nil {
		return AccessScope{}, err
	}
	if len(scopes) == 0 {
		return AccessScope{}, ErrAccessScopeMissing
	}

	for _, scope := range scopes {
		if err := mergeDeclaredScope(&out, scope); err != nil {
			return AccessScope{}, err
		}
	}
	if out.IsPlatform && (user.TenantID != PlatformTenantID || !HasRole(user, "platform_admin")) {
		return AccessScope{}, fmt.Errorf("%w: platform scope requires platform administrator", ErrAccessScopeInvalid)
	}
	return out.normalized(), nil
}

// 角色作用域只能来自账户已持有的角色，且必须符合该角色的标准作用域类型。
func declaredRoleScopes(user User) ([]map[string]any, error) {
	if _, flat := user.DataScope["scope"]; flat {
		return []map[string]any{user.DataScope}, nil
	}
	roles := make(map[string]struct{}, len(user.Roles))
	for _, role := range user.Roles {
		roles[role] = struct{}{}
	}
	scopes := make([]map[string]any, 0, len(user.DataScope))
	for role, raw := range user.DataScope {
		if _, ok := roles[role]; !ok {
			return nil, fmt.Errorf("%w: unknown role scope %q", ErrAccessScopeInvalid, role)
		}
		nested, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: role scope %q is not an object", ErrAccessScopeInvalid, role)
		}
		if !declaredRoleScopeAllowed(role, nested) {
			return nil, fmt.Errorf("%w: role %q has non-canonical scope", ErrAccessScopeInvalid, role)
		}
		scopes = append(scopes, nested)
	}
	return scopes, nil
}

func declaredRoleScopeAllowed(role string, scope map[string]any) bool {
	kind, _ := scope["scope"].(string)
	kind = strings.TrimSpace(kind)
	if kind == "none" {
		return true
	}
	policy := RolePolicy(role)
	return policy.CanonicalScope != "" && kind == policy.CanonicalScope
}

func mergeDeclaredScope(out *AccessScope, raw map[string]any) error {
	allowed := map[string]bool{
		"scope": true, "school_id": true, "school_ids": true, "school_name": true,
		"grade_id": true, "grade_ids": true, "class_id": true, "class_ids": true,
		"exam_id": true, "exam_ids": true, "review_task_id": true, "review_task_ids": true,
		"arbitration_task_id": true, "arbitration_task_ids": true, "student_id": true,
		"synthetic": true,
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("%w: unknown field %q", ErrAccessScopeInvalid, key)
		}
	}
	kind, ok := raw["scope"].(string)
	if !ok || strings.TrimSpace(kind) == "" {
		return ErrAccessScopeMissing
	}
	switch strings.TrimSpace(kind) {
	case "platform":
		out.IsPlatform = true
	case "tenant":
		out.TenantWide = true
	case "school":
		out.schoolWide = true
		// PostgreSQL 解析器添加可信的 app_user/teacher_class 学校关系；显式 ID 仍用于迁移和内存测试。
	case "grade", "class", "exam":
	case "assigned", "exam_task":
		out.AssignedOnly = true
	case "service":
	case "self":
		studentID, _ := raw["student_id"].(string)
		if strings.TrimSpace(studentID) == "" {
			return ErrAccessScopeMissing
		}
		out.StudentID = strings.TrimSpace(studentID)
	case "none":
	default:
		return fmt.Errorf("%w: unsupported scope %q", ErrAccessScopeInvalid, kind)
	}

	var err error
	if out.SchoolIDs, err = appendScopeIDs(out.SchoolIDs, raw, "school_id", "school_ids"); err != nil {
		return err
	}
	if out.GradeIDs, err = appendScopeIDs(out.GradeIDs, raw, "grade_id", "grade_ids"); err != nil {
		return err
	}
	if out.ClassIDs, err = appendScopeIDs(out.ClassIDs, raw, "class_id", "class_ids"); err != nil {
		return err
	}
	if out.ExamIDs, err = appendScopeIDs(out.ExamIDs, raw, "exam_id", "exam_ids"); err != nil {
		return err
	}
	if out.ReviewTaskIDs, err = appendScopeIDs(out.ReviewTaskIDs, raw, "review_task_id", "review_task_ids"); err != nil {
		return err
	}
	if out.ArbitrationTaskIDs, err = appendScopeIDs(out.ArbitrationTaskIDs, raw, "arbitration_task_id", "arbitration_task_ids"); err != nil {
		return err
	}
	return nil
}

func appendScopeIDs(current []string, raw map[string]any, singular, plural string) ([]string, error) {
	if value, exists := raw[singular]; exists {
		id, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be a string", ErrAccessScopeInvalid, singular)
		}
		if strings.TrimSpace(id) != "" {
			current = append(current, strings.TrimSpace(id))
		}
	}
	if value, exists := raw[plural]; exists {
		switch ids := value.(type) {
		case []string:
			current = append(current, ids...)
		case []any:
			for _, value := range ids {
				id, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%w: %s must contain strings", ErrAccessScopeInvalid, plural)
				}
				current = append(current, id)
			}
		default:
			return nil, fmt.Errorf("%w: %s must be an array", ErrAccessScopeInvalid, plural)
		}
	}
	return current, nil
}

func containsID(ids []string, id string) bool {
	id = strings.TrimSpace(id)
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func uniqueSortedIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

type accessScopeContextKey struct{}

func WithAccessScope(ctx context.Context, scope AccessScope) context.Context {
	return context.WithValue(ctx, accessScopeContextKey{}, scope)
}

func AccessScopeFromContext(ctx context.Context) (AccessScope, bool) {
	scope, ok := ctx.Value(accessScopeContextKey{}).(AccessScope)
	return scope, ok
}

// RequireScopedResource 对路由 ID 强制应用服务端派生的 AccessScope。它补充存储层筛选，适用于 URL 中资源类型明确的路由。
func RequireScopedResource(resource string, pathParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, ok := AccessScopeFromContext(r.Context())
			id := strings.TrimSpace(r.PathValue(pathParam))
			if !ok || id == "" {
				httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "resource is outside the current data access scope")
				return
			}
			allowed := false
			switch resource {
			case "exam":
				allowed = scope.IsPlatform || scope.AllowsExam(id)
			case "review_task":
				allowed = scope.IsPlatform || scope.AllowsReviewTask(id)
			case "arbitration_task":
				allowed = scope.IsPlatform || scope.AllowsArbitrationTask(id)
			case "student":
				allowed = scope.IsPlatform || scope.AllowsStudent(id)
			}
			if !allowed {
				httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "resource is outside the current data access scope")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
