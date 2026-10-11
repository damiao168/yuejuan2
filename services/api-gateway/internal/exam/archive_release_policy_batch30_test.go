package exam

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestBatch30ArchiveKeepsOfficialReleaseBlockerOrder(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		worked, published, active bool
		allowed                   bool
		want                      []string
	}{
		{"unused whole exam", false, false, false, true, []string{}},
		{"worked exam needs every release", true, false, false, false, []string{"official_release_required:first", "official_release_required:second"}},
		{"published whole exam", true, true, false, true, []string{}},
		{"existing blockers precede release blockers", true, false, true, false, []string{"unfinished_work:first", "appeal_window_open:first", "official_release_required:first", "official_release_required:second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 复用第29批查询替身验证原checker入口的排序与参数，不证明真实PG事务并发。
			steps := archiveReleasePolicySteps(tc.worked, tc.published, tc.active)
			store, script := openExamBatch29Store(t, steps)
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			item := ManagementExam{CanManage: true, Exams: []Exam{{ID: "first", Subject: "math", Status: "draft"}, {ID: "second", Subject: "chinese", Status: "draft"}}}
			check, err := checkManagementArchiveWithActiveTx(context.Background(), tx, "tenant", item, map[string]bool{})
			got := []string{}
			for _, blocker := range check.Blockers {
				got = append(got, blocker.Code+":"+blocker.ExamID)
			}
			if err != nil || check.Allowed != tc.allowed || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("check=%+v err=%v want=%v", check, err, tc.want)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertExamBatch29Script(t, script, "rollback")
		})
	}
}

// 只在第一场考试注入已有工作和开放申诉窗，验证既有阻塞项排在正式发布要求前面。
func archiveReleasePolicySteps(worked, published, active bool) []examBatch29Step {
	steps := []examBatch29Step{}
	for i, id := range []string{"first", "second"} {
		window := []byte(`{"enabled":false}`)
		if active && i == 0 {
			window = []byte(`{"enabled":true}`)
		}
		steps = append(steps,
			examBatch29Step{kind: "query", fragment: "EXISTS(SELECT 1 FROM submission", columns: 3, args: map[int]any{0: "tenant", 1: id}, rows: [][]driver.Value{{worked && i == 0, published, active && i == 0}}},
			examBatch29Step{kind: "query", fragment: "SELECT appeal_window FROM score_release", columns: 1, args: map[int]any{0: "tenant", 1: id}, rows: [][]driver.Value{{window}}})
	}
	return steps
}
