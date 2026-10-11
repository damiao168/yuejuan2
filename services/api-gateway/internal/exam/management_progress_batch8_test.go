package exam

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func logManagementProgressProof(t *testing.T, name string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MANAGEMENT_PROGRESS_PROOF %s %s", name, encoded)
}

func TestManagementProgressBatch8StagePrecedence(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []string
		facts     []managementSubjectFacts
		archived  bool
		status    string
		label     string
		ready     int
		published int
		available bool
	}{
		{name: "empty", status: "unavailable", label: "暂无法获取进度", available: true},
		{name: "empty_archived", archived: true, status: "archived", label: "已归档", available: true},
		{name: "archive_over_publication", statuses: []string{"published"}, facts: []managementSubjectFacts{{Published: true, Available: true}}, archived: true, status: "archived", label: "已归档", published: 1, available: true},
		{name: "confirmed_publication_over_draft", statuses: []string{"draft", "draft"}, facts: []managementSubjectFacts{{Published: true, Available: true}, {Published: true, Available: true}}, status: "published", label: "已发布", published: 2, available: true},
		{name: "partial_publication_over_preparation", statuses: []string{"ready", "configured"}, facts: []managementSubjectFacts{{Published: true, Ready: true, Available: true}, {Available: true}}, status: "partially_published", label: "部分已发布 · 1/2 科", ready: 1, published: 1, available: true},
		{name: "draft_without_facts", statuses: []string{"draft", "draft"}, status: "draft", label: "草稿"},
		{name: "all_ready_confirmed", statuses: []string{"ready", "configured"}, facts: []managementSubjectFacts{{Ready: true, Available: true}, {Ready: true, Available: true}}, status: "ready", label: "准备完成", ready: 2, available: true},
		{name: "ready_facts_missing", statuses: []string{"ready", "configured"}, status: "unavailable", label: "暂无法确认准备进度"},
		{name: "configured_facts_missing", statuses: []string{"configured"}, status: "configured", label: "准备中 · 0/1 科准备完成"},
		{name: "partial_preparation", statuses: []string{"ready", "configured"}, facts: []managementSubjectFacts{{Ready: true, Available: true}, {Available: true}}, status: "configured", label: "准备中 · 1/2 科准备完成", ready: 1, available: true},
		{name: "publication_status_is_not_receipt", statuses: []string{"published"}, facts: []managementSubjectFacts{{Available: true}}, status: "unavailable", label: "暂无法确认发布进度", available: true},
		{name: "uniform_grading", statuses: []string{"grading", "grading"}, status: "grading", label: "阅卷中"},
		{name: "unknown_uniform", statuses: []string{"unknown"}, status: "unavailable", label: "暂无法获取进度"},
		{name: "unknown_and_known", statuses: []string{"unknown", "reviewing"}, status: "ongoing", label: "进行中 · 1 科复核中"},
		{name: "mixed_phase_order", statuses: []string{"finalized", "collecting", "draft"}, status: "ongoing", label: "进行中 · 1 科草稿、1 科答卷导入中、1 科已定稿"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exams := make([]Exam, 0, len(tc.statuses))
			facts := map[string]managementSubjectFacts{}
			counts := map[string]int{}
			for index, status := range tc.statuses {
				id := fmt.Sprintf("subject-%d", index)
				exams = append(exams, Exam{ID: id, Status: status})
				counts[status]++
				if index < len(tc.facts) {
					facts[id] = tc.facts[index]
				}
			}
			want := ManagementProgress{Status: tc.status, Label: tc.label, TotalCount: len(exams), ReadyCount: tc.ready, PublishedCount: tc.published, Counts: counts, FactsAvailable: tc.available}
			got := managementProgress(exams, facts, tc.archived)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("进度分支或计数改变：got=%#v want=%#v", got, want)
			}
			logManagementProgressProof(t, tc.name, got)
		})
	}
}

func TestManagementProgressBatch8CompleteScopeAndOrdering(t *testing.T) {
	archivedAt := time.Unix(1700000000, 0).UTC()
	cases := []struct {
		name             string
		scope            auth.AccessScope
		total            int
		archivedAt       *time.Time
		legacy           bool
		archivedSubjects []string
		canManage        bool
		canEdit          bool
		canRestore       bool
	}{
		{name: "whole_tenant", scope: auth.AccessScope{TenantWide: true}, total: 2, canManage: true, canEdit: true},
		{name: "partial_read_is_not_write", scope: auth.AccessScope{TenantWide: true}, total: 3},
		{name: "one_candidate_class_missing", scope: auth.AccessScope{ClassIDs: []string{"class-a"}}, total: 2},
		{name: "all_candidate_classes", scope: auth.AccessScope{ClassIDs: []string{"class-a", "class-b"}}, total: 2, canManage: true, canEdit: true},
		{name: "assigned_read_scope", scope: auth.AccessScope{AssignedOnly: true, ExamIDs: []string{"z", "a"}}, total: 2},
		{name: "archived_restore", scope: auth.AccessScope{TenantWide: true}, total: 2, archivedAt: &archivedAt, canManage: true, canRestore: true},
		{name: "legacy_archive", scope: auth.AccessScope{TenantWide: true}, total: 2, legacy: true, canManage: true},
		{name: "archived_subject_blocks_edit", scope: auth.AccessScope{TenantWide: true}, total: 2, archivedSubjects: []string{"z"}, canManage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ManagementExam{Exams: []Exam{
				{ID: "z", Subject: "math", Status: "draft", ClassIDs: []string{"class-b", "class-a", "class-b"}},
				{ID: "a", Subject: "math", Status: "draft", ClassIDs: []string{"class-a"}},
			}, LegacyArchived: tc.legacy, ArchivedSubjectIDs: tc.archivedSubjects, InheritedSubjectIDs: []string{"stale"}}
			out.ArchivedAt = tc.archivedAt
			// 两科展示顺序相反，继承科目仍按输入顺序；先排序会改变此数组的身份顺序。
			completeManagementExam(&out, tc.scope, tc.total, nil, nil)
			if out.CanManage != tc.canManage || out.CanEdit != tc.canEdit || out.CanRestore != tc.canRestore || out.ScopeComplete != (tc.total == 2) {
				t.Fatalf("整场权限或归档边界改变：%#v", out)
			}
			if !reflect.DeepEqual(out.ClassIDs, []string{"class-a", "class-b"}) || !reflect.DeepEqual(out.DefaultClassIDs, []string{"class-a", "class-b"}) || !reflect.DeepEqual(out.InheritedSubjectIDs, []string{"z", "a"}) || out.Exams[0].ID != "a" || out.Exams[1].ID != "z" || out.ArchivedSubjectIDs == nil {
				t.Fatalf("班级去重、继承顺序或空数组改变：%#v", out)
			}
			logManagementProgressProof(t, tc.name, out)
		})
	}
}

func TestManagementProgressBatch8IndependentRosterAndEmptyArrays(t *testing.T) {
	out := ManagementExam{Exams: []Exam{
		{ID: "independent", Subject: "english", Status: "draft", ClassIDs: []string{"class-b"}},
		{ID: "inherited", Subject: "math", Status: "draft", ClassIDs: []string{"class-a"}},
	}}
	completeManagementExam(&out, auth.AccessScope{TenantWide: true}, 2, map[string]string{"independent": "subject_selected_classes"}, nil)
	if !reflect.DeepEqual(out.DefaultClassIDs, []string{"class-a"}) || !reflect.DeepEqual(out.InheritedSubjectIDs, []string{"inherited"}) {
		t.Fatalf("独立科目的班级被纳入默认范围：%#v", out)
	}
	logManagementProgressProof(t, "independent_roster", out)
	empty := ManagementExam{InheritedSubjectIDs: []string{"stale"}}
	completeManagementExam(&empty, auth.AccessScope{TenantWide: true}, 0, nil, nil)
	if empty.CanManage || empty.CanEdit || empty.CanRestore || !empty.ScopeComplete || empty.ClassIDs == nil || empty.DefaultClassIDs == nil || empty.InheritedSubjectIDs == nil || empty.ArchivedSubjectIDs == nil || empty.Progress.Status != "unavailable" {
		t.Fatalf("空场次权限或公开空数组改变：%#v", empty)
	}
	logManagementProgressProof(t, "empty_arrays", empty)
}
