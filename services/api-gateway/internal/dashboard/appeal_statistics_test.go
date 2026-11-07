package dashboard

import (
	"errors"
	"slices"
	"testing"
)

func TestWorkbenchAppealStatisticsRequireAppealManage(t *testing.T) {
	exams, _ := seedWorkbenchExams(t, "grading")
	const warning = "成绩复核申请暂时无法统计"

	t.Run("without appeal:manage the field is omitted, not 0", func(t *testing.T) {
		sources := &workbenchSourcesStub{appeals: AppealStatistics{PendingAssignmentCount: 3, PendingDecisionCount: 1}}
		summary := workbenchSummary(t, exams, reviewSourcesStub{}, sources, "appeal:work", "dashboard:read")
		if sources.appealCalls != 0 || summary.AppealStatistics != nil || slices.Contains(summary.Warnings, warning) {
			t.Fatalf("appeal statistics leaked without appeal:manage: calls=%d statistics=%#v warnings=%#v", sources.appealCalls, summary.AppealStatistics, summary.Warnings)
		}
		if _, present := workbenchJSON(t, summary)["appeal_statistics"]; present {
			t.Fatal("appeal_statistics must be omitted without appeal:manage")
		}
	})

	t.Run("with appeal:manage the store result is returned", func(t *testing.T) {
		sources := &workbenchSourcesStub{appeals: AppealStatistics{PendingAssignmentCount: 3, PendingDecisionCount: 1}}
		summary := workbenchSummary(t, exams, reviewSourcesStub{}, sources, "appeal:manage")
		if sources.appealCalls != 1 || sources.appealScope.TenantID != workbenchTenant || sources.appealScope.ActorID != "admin-1" {
			t.Fatalf("appeal store must receive the request scope once: calls=%d scope=%#v", sources.appealCalls, sources.appealScope)
		}
		statistics, ok := workbenchJSON(t, summary)["appeal_statistics"].(map[string]any)
		if !ok || statistics["pending_assignment_count"] != float64(3) || statistics["pending_decision_count"] != float64(1) || slices.Contains(summary.Warnings, warning) {
			t.Fatalf("appeal statistics=%#v warnings=%#v", statistics, summary.Warnings)
		}
	})

	for _, tc := range []struct {
		name          string
		organizations OrganizationSummaryStore
	}{
		{"store missing", organizationSummaryStub{}},
		{"store error", &workbenchSourcesStub{appealErr: errors.New("appeals unavailable")}},
	} {
		t.Run(tc.name+" omits the field and warns", func(t *testing.T) {
			summary := workbenchSummary(t, exams, reviewSourcesStub{}, tc.organizations, "appeal:manage")
			if _, present := workbenchJSON(t, summary)["appeal_statistics"]; present || summary.AppealStatistics != nil || !slices.Contains(summary.Warnings, warning) {
				t.Fatalf("unavailable appeal statistics=%#v warnings=%#v", summary.AppealStatistics, summary.Warnings)
			}
		})
	}
}
