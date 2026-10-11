package scorerelease

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
)

type failingDashboardReader struct{ err error }

func (r failingDashboardReader) Get(context.Context, string, string) (qualitydashboard.Dashboard, error) {
	return qualitydashboard.Dashboard{}, r.err
}

func TestMissingQualityQuestionsAreReleaseBlocker(t *testing.T) {
	// 草稿没有题目时仍能读取门禁；包括被依赖包装后的空看板错误，但始终禁止发布。
	for _, err := range []error{qualitydashboard.ErrNotFound, fmt.Errorf("quality reader: %w", qualitydashboard.ErrNotFound)} {
		store := NewPostgresStore(nil, failingDashboardReader{err: err})
		issues, gotErr := store.dashboardIssues(t.Context(), "tenant", "exam")
		if gotErr != nil {
			t.Fatalf("empty dashboard returned error: %v", gotErr)
		}
		if len(issues) != 1 || issues[0].Code != "quality_questions_missing" || !issues[0].Blocking || issues[0].Count != 1 || issues[0].ActionRoute != "questions" {
			t.Fatalf("missing questions must remain actionable release blocker: %#v", issues)
		}
	}
}

func TestQualityDashboardDependencyErrorRemainsError(t *testing.T) {
	// 真实依赖故障不能按“没有题目”降级，否则会掩盖质量检查失败。
	dependencyError := errors.New("database unavailable")
	store := NewPostgresStore(nil, failingDashboardReader{err: dependencyError})
	issues, err := store.dashboardIssues(t.Context(), "tenant", "exam")
	if !errors.Is(err, dependencyError) || len(issues) != 0 {
		t.Fatalf("dependency failure was hidden: issues=%#v err=%v", issues, err)
	}
}
