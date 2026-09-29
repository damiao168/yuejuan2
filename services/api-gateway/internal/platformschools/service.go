package platformschools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidFilter = errors.New("invalid platform school filter")
	ErrNotFound      = errors.New("platform school not found")
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// 平台列表先统一过滤和排序，再用游标切页；游标只代表当前结果集中的偏移位置。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	filter = normalizeFilter(filter)
	if filter.Limit < 1 || filter.Limit > 200 || !validListFilter(filter) {
		return ListResult{}, ErrInvalidFilter
	}
	items, err := s.store.ListSummaries(ctx, filter.UsageDays)
	if err != nil {
		return ListResult{}, err
	}
	now := time.Now().UTC()
	filtered := make([]PlatformSchoolSummary, 0, len(items))
	for _, item := range items {
		item.AttentionReasons = attentionReasons(item, now)
		if matchesSchool(item, filter, now) {
			filtered = append(filtered, item)
		}
	}
	sortSchools(filtered, filter.Sort, filter.Order)
	summary := FleetSummary{Total: len(filtered)}
	for _, item := range filtered {
		if item.Status == "active" {
			summary.Active++
		} else {
			summary.Disabled++
		}
	}
	offset, err := decodeCursor(filter.Cursor)
	if err != nil || offset > len(filtered) {
		return ListResult{}, ErrInvalidFilter
	}
	end := offset + filter.Limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[offset:end]
	result := ListResult{Schools: page, Summary: summary, HasMore: end < len(filtered)}
	if result.HasMore {
		result.NextCursor = encodeCursor(end)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, tenantID string) (PlatformSchoolSummary, error) {
	items, err := s.store.ListSummaries(ctx, 30)
	if err != nil {
		return PlatformSchoolSummary{}, err
	}
	for _, item := range items {
		if item.TenantID == tenantID {
			item.AttentionReasons = attentionReasons(item, time.Now().UTC())
			return item, nil
		}
	}
	return PlatformSchoolSummary{}, ErrNotFound
}

func (s *Service) Members(ctx context.Context, tenantID, query, role, status string) (MembersResult, error) {
	if _, err := s.Get(ctx, tenantID); err != nil {
		return MembersResult{}, err
	}
	if !oneOf(status, "", "active", "disabled") || (role != "" && !oneOf(role, "school_admin", "tenant_admin", "teacher", "grader", "arbitrator", "auditor")) {
		return MembersResult{}, ErrInvalidFilter
	}
	items, err := s.store.ListMembers(ctx, tenantID)
	if err != nil {
		return MembersResult{}, err
	}
	if items == nil {
		items = []Member{}
	}
	result := MembersResult{Members: []Member{}}
	for _, item := range items {
		result.Summary.Total++
		if item.Status == "active" {
			result.Summary.Active++
		} else {
			result.Summary.Disabled++
		}
		if hasAnyRole(item.Roles, "school_admin", "tenant_admin") {
			result.Summary.Admins++
		}
		if hasAnyRole(item.Roles, "teacher") {
			result.Summary.Teachers++
		}
		if hasAnyRole(item.Roles, "grader", "arbitrator") {
			result.Summary.Graders++
		}
		if query != "" && !strings.Contains(strings.ToLower(item.DisplayName+" "+item.Username+" "+item.EmployeeNo), strings.ToLower(query)) {
			continue
		}
		if role != "" && !hasAnyRole(item.Roles, role) {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		result.Members = append(result.Members, item)
	}
	return result, nil
}

func (s *Service) Usage(ctx context.Context, tenantID string, usageRange UsageRange) (UsageResult, error) {
	if _, err := s.Get(ctx, tenantID); err != nil {
		return UsageResult{}, err
	}
	if usageRange.Start.IsZero() || usageRange.End.IsZero() || usageRange.End.Before(usageRange.Start) || usageRange.End.Sub(usageRange.Start) > 366*24*time.Hour {
		return UsageResult{}, ErrInvalidFilter
	}
	return s.store.GetUsage(ctx, tenantID, usageRange)
}

func (s *Service) ModelHealth(ctx context.Context, tenantID string) (ModelHealthResult, error) {
	if _, err := s.Get(ctx, tenantID); err != nil {
		return ModelHealthResult{}, err
	}
	return s.store.GetModelHealth(ctx, tenantID)
}

func (s *Service) Activity(ctx context.Context, tenantID string, limit int) (ActivityResult, error) {
	if _, err := s.Get(ctx, tenantID); err != nil {
		return ActivityResult{}, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return ActivityResult{}, ErrInvalidFilter
	}
	return s.store.GetActivity(ctx, tenantID, limit)
}

func normalizeFilter(filter ListFilter) ListFilter {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	filter.Activity = strings.ToLower(strings.TrimSpace(filter.Activity))
	filter.ModelHealth = strings.ToLower(strings.TrimSpace(filter.ModelHealth))
	filter.Sort = strings.ToLower(strings.TrimSpace(filter.Sort))
	filter.Order = strings.ToLower(strings.TrimSpace(filter.Order))
	if filter.UsageDays == 0 {
		filter.UsageDays = 30
	}
	if filter.Sort == "" {
		filter.Sort = "created_at"
	}
	if filter.Order == "" {
		filter.Order = "desc"
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	return filter
}

func validListFilter(filter ListFilter) bool {
	return oneOf(filter.Status, "", "active", "disabled") &&
		oneOf(filter.Activity, "", "today", "7d", "30d", "inactive_30d", "never") &&
		oneOf(filter.ModelHealth, "", ModelHealthy, ModelWarning, ModelUnconfigured) &&
		oneOf(filter.Sort, "created_at", "last_activity", "token_usage", "student_count") &&
		oneOf(filter.Order, "asc", "desc") &&
		(filter.UsageDays == 1 || filter.UsageDays == 7 || filter.UsageDays == 30 || filter.UsageDays == 90)
}

func matchesSchool(item PlatformSchoolSummary, filter ListFilter, now time.Time) bool {
	if filter.Status != "" && item.Status != filter.Status {
		return false
	}
	if filter.ModelHealth != "" && item.ModelHealth.Status != filter.ModelHealth {
		return false
	}
	if filter.Query != "" {
		haystack := strings.ToLower(strings.Join([]string{item.Name, item.Code, item.Administrator.DisplayName, item.Administrator.Username}, " "))
		if !strings.Contains(haystack, strings.ToLower(filter.Query)) {
			return false
		}
	}
	if filter.Activity == "never" {
		return item.LastActivityAt == nil
	}
	if filter.Activity == "inactive_30d" {
		return item.LastActivityAt != nil && item.LastActivityAt.Before(now.AddDate(0, 0, -30))
	}
	if filter.Activity != "" {
		if item.LastActivityAt == nil {
			return false
		}
		days := map[string]int{"today": 1, "7d": 7, "30d": 30}[filter.Activity]
		return !item.LastActivityAt.Before(now.AddDate(0, 0, -days))
	}
	return true
}

func attentionReasons(item PlatformSchoolSummary, now time.Time) []string {
	reasons := []string{}
	if item.Administrator.AdminCount == 0 {
		reasons = append(reasons, "未配置学校管理员")
	}
	if item.ModelHealth.Status == ModelWarning {
		reasons = append(reasons, "默认模型检测异常")
	}
	if item.ModelHealth.Status == ModelUnconfigured {
		reasons = append(reasons, "尚未配置默认模型")
	}
	if item.ConsecutiveAIFailures >= 3 {
		reasons = append(reasons, fmt.Sprintf("AI 调用已连续失败 %d 次", item.ConsecutiveAIFailures))
	}
	if item.LastActivityAt == nil {
		reasons = append(reasons, "学校从未产生业务活动")
	} else if item.LastActivityAt.Before(now.AddDate(0, 0, -30)) {
		reasons = append(reasons, "已超过 30 天无业务活动")
	}
	return reasons
}

func sortSchools(items []PlatformSchoolSummary, field, order string) {
	less := func(i, j int) bool {
		left, right := items[i], items[j]
		if field == "last_activity" && (left.LastActivityAt == nil || right.LastActivityAt == nil) {
			if left.LastActivityAt == nil && right.LastActivityAt == nil {
				return false
			}
			return right.LastActivityAt == nil
		}
		var result bool
		switch field {
		case "last_activity":
			result = left.LastActivityAt.Before(*right.LastActivityAt)
		case "token_usage":
			result = left.Usage.TotalTokens < right.Usage.TotalTokens
		case "student_count":
			result = left.Members.Students < right.Members.Students
		default:
			result = left.CreatedAt.Before(right.CreatedAt)
		}
		if order == "desc" {
			return !result && !equalSortValue(left, right, field)
		}
		return result
	}
	sort.SliceStable(items, less)
}

func equalSortValue(a, b PlatformSchoolSummary, field string) bool {
	switch field {
	case "last_activity":
		return (a.LastActivityAt == nil && b.LastActivityAt == nil) || (a.LastActivityAt != nil && b.LastActivityAt != nil && a.LastActivityAt.Equal(*b.LastActivityAt))
	case "token_usage":
		return a.Usage.TotalTokens == b.Usage.TotalTokens
	case "student_count":
		return a.Members.Students == b.Members.Students
	default:
		return a.CreatedAt.Equal(b.CreatedAt)
	}
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func hasAnyRole(roles []string, wanted ...string) bool {
	for _, role := range roles {
		for _, target := range wanted {
			if role == target {
				return true
			}
		}
	}
	return false
}
func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(string(raw))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return value, nil
}
