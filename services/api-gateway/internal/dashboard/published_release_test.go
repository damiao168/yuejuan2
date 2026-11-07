package dashboard

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

// 生产首页靠类型断言取得发布读取能力；签名漂移时在这里编译失败，而不是静默退回按状态统计。
var _ publishedExamReader = (*exam.PostgresStore)(nil)

// releaseExamStore 只实现首页摘要会读取的两项；嵌入的 Store 为 nil，误调其他方法会直接失败。
type releaseExamStore struct {
	exam.Store
	items     []exam.Exam
	published map[string]bool
	err       error
	requested []string
}

func (s *releaseExamStore) ListExams(context.Context, auth.AccessScope, exam.ListFilter) ([]exam.Exam, error) {
	return append([]exam.Exam(nil), s.items...), nil
}

func (s *releaseExamStore) PublishedExamIDs(_ context.Context, tenantID string, examIDs []string) (map[string]bool, error) {
	if tenantID != "tenant-school" {
		return nil, errors.New("release read used another tenant")
	}
	s.requested = append([]string(nil), examIDs...)
	return s.published, s.err
}

func releaseDashboardSummary(t *testing.T, store *releaseExamStore) Summary {
	t.Helper()
	created := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	for index := range store.items {
		store.items[index].TenantID = "tenant-school"
		store.items[index].CreatedAt = created.Add(time.Duration(index) * time.Minute)
	}
	service := NewService(Dependencies{
		Exams: store, Submissions: submission.NewMemoryStore(), Reviews: review.NewMemoryStore(), Audits: auth.NewMemoryStore(),
		Now: func() time.Time { return created.Add(time.Hour) },
	})
	summary, err := service.Summary(context.Background(), auth.User{ID: "admin", TenantID: "tenant-school", Roles: []string{"tenant_admin"}},
		auth.AccessScope{TenantID: "tenant-school", ActorID: "admin", TenantWide: true})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

// 学生端发布不改 exam.status：仍是 finalized 或 grading 的已发布考试不能再算进行中或待发布；
// 已是 published、archived 的考试不再查询发布记录。
func TestSummaryCountsStudentReleasedExamsAsPublished(t *testing.T) {
	store := &releaseExamStore{
		items: []exam.Exam{
			{ID: "released-finalized", Status: "finalized"}, {ID: "released-grading", Status: "grading"},
			{ID: "waiting-release", Status: "finalized"}, {ID: "collecting", Status: "collecting"},
			{ID: "legacy-published", Status: "published"}, {ID: "archived", Status: "archived"},
		},
		published: map[string]bool{"released-finalized": true, "released-grading": true},
	}
	summary := releaseDashboardSummary(t, store)
	if want := []string{"released-finalized", "released-grading", "waiting-release", "collecting"}; !reflect.DeepEqual(store.requested, want) {
		t.Fatalf("release lookup ids=%v, want %v", store.requested, want)
	}
	stats := summary.Statistics
	if stats.ActiveExamCount != 2 || stats.FinalizedExamCount != 1 || stats.CollectingExamCount != 1 || stats.GradingExamCount != 0 {
		t.Fatalf("released exams still counted as in progress or awaiting release: %#v", stats)
	}
	active := []string{}
	for _, item := range summary.ActiveExams {
		active = append(active, item.ID)
	}
	slices.Sort(active)
	if !reflect.DeepEqual(active, []string{"collecting", "waiting-release"}) {
		t.Fatalf("active exams=%v", active)
	}
	if slices.Contains(summary.Warnings, "成绩发布状态暂时无法确认") {
		t.Fatalf("successful release read produced a warning: %#v", summary.Warnings)
	}
}

// 发布记录读取失败时按考试状态统计并告警；未发布考试不会因此从待办中消失。
func TestSummaryKeepsStatusCountsAndWarnsWhenReleaseReadFails(t *testing.T) {
	store := &releaseExamStore{
		items: []exam.Exam{{ID: "finalized", Status: "finalized"}, {ID: "grading", Status: "grading"}},
		err:   errors.New("release source unavailable"),
	}
	summary := releaseDashboardSummary(t, store)
	stats := summary.Statistics
	if stats.ActiveExamCount != 2 || stats.FinalizedExamCount != 1 || stats.GradingExamCount != 1 || len(summary.ActiveExams) != 2 {
		t.Fatalf("degraded counts=%#v rows=%d", stats, len(summary.ActiveExams))
	}
	if !slices.Contains(summary.Warnings, "成绩发布状态暂时无法确认") {
		t.Fatalf("missing release warning: %#v", summary.Warnings)
	}
}
