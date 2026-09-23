package platformschools

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestListFiltersSortsAndPaginatesPlatformSchools(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	created := now.AddDate(0, 0, -10)
	store.PutSchool(PlatformSchoolSummary{
		TenantID: "a", Name: "北京第一中学", Code: "bjyz", Status: "active", CreatedAt: created,
		LastActivityAt: &now, Administrator: AdministratorSummary{DisplayName: "张老师", Username: "zhangsan", AdminCount: 1},
		Usage: UsageSummary{TotalTokens: 100}, ModelHealth: ModelHealth{Status: ModelHealthy},
	})
	store.PutSchool(PlatformSchoolSummary{
		TenantID: "b", Name: "实验中学", Code: "syzx", Status: "active", CreatedAt: created.Add(time.Hour),
		LastActivityAt: &now, Administrator: AdministratorSummary{DisplayName: "李老师", AdminCount: 0},
		Usage: UsageSummary{TotalTokens: 300}, ModelHealth: ModelHealth{Status: ModelWarning}, ConsecutiveAIFailures: 4,
	})
	store.PutSchool(PlatformSchoolSummary{
		TenantID: "c", Name: "停用学校", Code: "disabled", Status: "disabled", CreatedAt: created.Add(2 * time.Hour),
		ModelHealth: ModelHealth{Status: ModelUnconfigured},
	})
	service := NewService(store)
	first, err := service.List(context.Background(), ListFilter{Sort: "token_usage", Order: "desc", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary != (FleetSummary{Total: 3, Active: 2, Disabled: 1}) || !first.HasMore || first.NextCursor == "" || len(first.Schools) != 1 || first.Schools[0].TenantID != "b" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if !reflect.DeepEqual(first.Schools[0].AttentionReasons, []string{"未配置学校管理员", "默认模型检测异常", "AI 调用已连续失败 4 次"}) {
		t.Fatalf("attention reasons = %v", first.Schools[0].AttentionReasons)
	}
	second, err := service.List(context.Background(), ListFilter{Sort: "token_usage", Order: "desc", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Schools) != 1 || second.Schools[0].TenantID != "a" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	searched, err := service.List(context.Background(), ListFilter{Query: "ZHANGSAN", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if searched.Summary.Total != 1 || searched.Schools[0].TenantID != "a" {
		t.Fatalf("administrator search failed: %+v", searched)
	}
	never, err := service.List(context.Background(), ListFilter{Activity: "never", ModelHealth: ModelUnconfigured})
	if err != nil {
		t.Fatal(err)
	}
	if never.Summary.Total != 1 || never.Schools[0].TenantID != "c" {
		t.Fatalf("activity/model filter failed: %+v", never)
	}
}

func TestPlatformSchoolListRejectsInvalidFiltersAndCursor(t *testing.T) {
	service := NewService(NewMemoryStore())
	for _, filter := range []ListFilter{
		{Limit: -1}, {Limit: 201}, {UsageDays: 2}, {Status: "archived"}, {Activity: "all-time"},
		{ModelHealth: "unknown"}, {Sort: "name"}, {Order: "sideways"}, {Cursor: "not-a-cursor"},
		{Cursor: encodeCursor(1)},
	} {
		if _, err := service.List(context.Background(), filter); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("filter %+v: got %v, want ErrInvalidFilter", filter, err)
		}
	}
}

func TestPlatformSchoolMembersKeepWholeSchoolSummaryWhenFiltered(t *testing.T) {
	store := NewMemoryStore()
	store.PutSchool(PlatformSchoolSummary{TenantID: "school-a", Status: "active"})
	store.PutMembers("school-a", []Member{
		{ID: "1", Username: "admin", DisplayName: "张老师", Status: "active", Roles: []string{"school_admin"}},
		{ID: "2", Username: "teacher", DisplayName: "李老师", Status: "active", Roles: []string{"teacher"}},
		{ID: "3", Username: "grader", DisplayName: "王老师", Status: "disabled", Roles: []string{"grader", "teacher"}},
	})
	result, err := NewService(store).Members(context.Background(), "school-a", "王", "grader", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Members) != 1 || result.Members[0].ID != "3" {
		t.Fatalf("filtered members = %+v", result.Members)
	}
	want := MemberSummary{Total: 3, Active: 2, Disabled: 1, Admins: 1, Teachers: 2, Graders: 1}
	if result.Summary != want {
		t.Fatalf("summary = %+v, want %+v", result.Summary, want)
	}
}

func TestPlatformSchoolDetailErrorsAndUsageRanges(t *testing.T) {
	store := NewMemoryStore()
	store.PutSchool(PlatformSchoolSummary{TenantID: "any", Status: "active"})
	service := NewService(store)
	if _, err := service.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	if _, err := service.Members(context.Background(), "missing", "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Members missing: %v", err)
	}
	if _, err := service.ModelHealth(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ModelHealth missing: %v", err)
	}
	if _, err := service.Activity(context.Background(), "missing", 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Activity missing: %v", err)
	}
	if _, err := service.Usage(context.Background(), "missing", UsageRange{Start: time.Now(), End: time.Now()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Usage missing: %v", err)
	}
	if _, err := service.Members(context.Background(), "any", "", "owner", ""); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("Members invalid role: %v", err)
	}
	if _, err := service.Activity(context.Background(), "any", 201); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("Activity limit: %v", err)
	}
	for _, usageRange := range []UsageRange{
		{},
		{Start: time.Now(), End: time.Now().Add(-time.Hour)},
		{Start: time.Now(), End: time.Now().Add(367 * 24 * time.Hour)},
	} {
		if _, err := service.Usage(context.Background(), "any", usageRange); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("range %+v: got %v, want ErrInvalidFilter", usageRange, err)
		}
	}
}
