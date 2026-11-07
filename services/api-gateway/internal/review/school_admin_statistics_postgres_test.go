package review

import (
	"testing"

	"github.com/google/uuid"
)

func TestPostgresSchoolAdministratorPersonalStatisticsExcludeForeignHistory(t *testing.T) {
	f := newSubjectAssignmentFixture(t)
	store := NewPostgresStore(f.db)
	actor := uuid.NewString()
	for _, foreign := range []bool{false, true} {
		examID, segmentID := f.segment("math")
		if foreign {
			f.exec(`UPDATE exam SET school_id=$1 WHERE id=$2`, uuid.NewString(), examID)
		}
		var question, submission string
		if err := f.db.QueryRowContext(t.Context(), `SELECT question_id::text,submission_id::text FROM answer_segment WHERE id=$1`, segmentID).Scan(&question, &submission); err != nil {
			t.Fatal(err)
		}
		task := uuid.NewString()
		// 模拟收紧学校边界前留下的外校本人正式评分；历史事实保留，但不能重新扩大管理员读取范围。
		f.insert("review_task", map[string]any{"id": task, "tenant_id": f.tenant, "exam_id": examID, "question_id": question, "question_no": "Q1", "answer_segment_id": segmentID, "submission_id": submission, "assigned_to": actor, "status": "completed"})
		f.insert("human_grade", map[string]any{"id": uuid.NewString(), "tenant_id": f.tenant, "review_task_id": task, "reviewer_id": actor, "score": 2.25, "max_score": 5})
		filter := ListFilter{ExamID: examID, AssignedTo: actor, ScopeActorID: actor, ScopeMode: "school", ScopeSchoolIDs: []string{f.school}, ScopeExamIDs: []string{examID}}
		statistics, err := store.reviewStatistics(t.Context(), f.tenant, filter)
		want := 1
		if foreign {
			want = 0
		}
		if err != nil || statistics == nil || statistics.GradedCount != want {
			t.Fatalf("foreign=%v statistics=%#v err=%v", foreign, statistics, err)
		}
		filter.ScopeMode = "assigned"
		statistics, err = store.reviewStatistics(t.Context(), f.tenant, filter)
		if err != nil || statistics == nil || statistics.GradedCount != 1 {
			t.Fatalf("ordinary assigned teacher lost personal statistics: %#v %v", statistics, err)
		}
	}
}
