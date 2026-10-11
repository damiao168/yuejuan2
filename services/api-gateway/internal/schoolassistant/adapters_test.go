package schoolassistant

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

func TestReadWorkspaceDistinguishesZeroCountsFromUnavailableSources(t *testing.T) {
	for _, test := range []struct {
		name      string
		warning   string
		available bool
	}{
		{name: "healthy_zero_is_available", available: true},
		{name: "ordinary_warning_keeps_counts", warning: "processing_backlog", available: true},
		{name: "unknown_unavailable_source_hides_counts", warning: "future_source_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := workspace.Projection{ExamID: toolTestExam, Revision: 3, Counts: workspace.Counts{QuestionCount: 7}}
			if test.warning != "" {
				projection.Warnings = []workspace.Notice{{Code: test.warning}}
			}
			reader := &toolTestWorkspace{projection: projection}
			service := NewService(Dependencies{Workspaces: reader})
			source := Source{ID: "workspace", Status: "available"}
			result := Result{Status: "completed"}
			service.readWorkspace(context.Background(), preparedQuery{
				scope: Scope{ExamID: toolTestExam}, request: QueryRequest{PresetIntent: IntentExamProgress},
			}, &source, &result)
			if reader.calls != 1 || source.Revision != "3" || len(result.Facts) != 8 {
				t.Fatalf("workspace projection was not preserved: source=%+v result=%+v", source, result)
			}
			assertWorkspaceCountFacts(t, source.ID, result.Facts, test.available)
			if !test.available && (source.Status != "partial" || result.Status != "partial" || !strings.Contains(result.Blocks[0].Text, "部分来源暂不可用")) {
				t.Fatalf("unavailable source did not degrade summary: source=%+v result=%+v", source, result)
			}
		})
	}
}

func TestWorkspaceNoticeRowsPreserveOrderAndSafeActionDeduplication(t *testing.T) {
	result := Result{}
	rows := workspaceNoticeRows(&result, toolTestExam, "workspace", []workspace.Notice{
		{Code: "blocked", Title: "Blocked", Severity: "blocker", ActionRoute: "scores", ActionLabel: "Open scores"},
		{Code: "warning", Severity: "warning", ActionRoute: "/exams/" + toolTestExam + "/scores"},
		{Code: "future", Severity: "future", ActionRoute: "/exams/" + toolTestForeign + "/scores"},
	})
	if len(rows) != 3 || !reflect.DeepEqual([]string{rows[0].Code, rows[1].Code, rows[2].Code}, []string{"blocked", "warning", "future"}) {
		t.Fatalf("notice order changed: %+v", rows)
	}
	if rows[0].Label != "Blocked" || rows[1].Label != "warning" || rows[2].Severity != "info" {
		t.Fatalf("notice display normalization changed: %+v", rows)
	}
	if len(result.Actions) != 1 || rows[0].ActionID == "" || rows[0].ActionID != rows[1].ActionID || rows[2].ActionID != "" {
		t.Fatalf("action scope or deduplication changed: rows=%+v actions=%+v", rows, result.Actions)
	}
}

// 不可用来源使用空值和原因说明；可用来源保留真实零值，二者不能混为同一事实。
func assertWorkspaceCountFacts(t *testing.T, sourceID string, facts []Fact, available bool) {
	t.Helper()
	for index, fact := range facts {
		if fact.SourceID != sourceID || fact.Available != available {
			t.Fatalf("fact lost source availability: %+v", fact)
		}
		if !available {
			assertUnavailableWorkspaceFact(t, fact)
			continue
		}
		want := float64(0)
		if index == 1 {
			want = 7
		}
		if fact.Value == nil || *fact.Value != want || fact.Reason != "" {
			t.Fatalf("available count changed: %+v want=%v", fact, want)
		}
	}
}

func assertUnavailableWorkspaceFact(t *testing.T, fact Fact) {
	t.Helper()
	if fact.Value != nil || fact.Reason == "" {
		t.Fatalf("unavailable counts became numeric facts: %+v", fact)
	}
}
