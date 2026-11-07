package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"github.com/google/uuid"
)

func TestLiveReviewStatisticsAndHistoryWithPostgresTestDatabase(t *testing.T) {
	db, router, adminToken, fixture, answers, _ := repairFixture(t)
	graderID := e2eLookupUserID(t, db, "demo", "grader")
	graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	// 正式成绩还未发布；客观题没有 review_task，也必须进入管理员实时统计。
	if _, err := db.Exec(`UPDATE question_grade SET score=1 WHERE id=$1`, answers[0].grade); err != nil {
		t.Fatal(err)
	}
	var releaseCount int
	if err := db.QueryRow(`SELECT count(*) FROM score_release WHERE tenant_id=$1 AND exam_id=$2`, fixture.TenantID, fixture.ExamID).Scan(&releaseCount); err != nil || releaseCount != 0 {
		t.Fatalf("live fixture must be unpublished: %d %v", releaseCount, err)
	}
	read := func(token, query string) review.TaskAggregate {
		t.Helper()
		response := e2eGetJSON(t, router, "/api/v1/review-tasks?exam_id="+fixture.ExamID+query, token, http.StatusOK)
		encoded, err := json.Marshal(response["aggregate"])
		if err != nil {
			t.Fatal(err)
		}
		var aggregate review.TaskAggregate
		if err := json.Unmarshal(encoded, &aggregate); err != nil {
			t.Fatal(err)
		}
		return aggregate
	}
	if got := read(adminToken, "&statistics=true&status=active&limit=1"); got.TotalCount != 0 || got.Statistics == nil || got.Statistics.Scope != "exam" || got.Statistics.GradedCount != 6 || got.Statistics.FullScoreCount != 1 || math.Abs(*got.Statistics.CorrectRate-1.0/6) > 0.000001 {
		t.Fatalf("unpublished objective grades were omitted: %+v", got)
	}
	adminStatistics := read(adminToken, "&statistics=true&limit=1").Statistics
	for _, question := range adminStatistics.Questions {
		var expected float64
		if err := db.QueryRow(`SELECT avg(score)::float8 FROM question_grade WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 AND is_current AND status='confirmed' AND deleted_at IS NULL`, fixture.TenantID, fixture.ExamID, question.QuestionID).Scan(&expected); err != nil {
			t.Fatal(err)
		}
		if question.AverageScore == nil || math.Abs(*question.AverageScore-expected) > 0.000001 {
			t.Fatalf("administrator average mixed question scores: question=%s average=%v want=%v", question.QuestionID, question.AverageScore, expected)
		}
	}
	if got := read(adminToken, "&limit=1"); got.Statistics != nil {
		t.Fatal("ordinary task lists must not run a live statistics query")
	}

	// 同一片段有多轮或双评意见时，本人统计只取自己的最新正式意见，不能读另一位教师的分数。
	tasks := []string{}
	for index, status := range []string{"submitted", "completed"} {
		var taskID string
		err := db.QueryRow(`INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by)
SELECT $1,$2,s.question_id,s.question_no,s.id,s.submission_id,$4,'manual_sample',$5,$6,$7
FROM answer_segment s WHERE s.tenant_id=$1 AND s.submission_id=$3 AND s.question_id=$8 RETURNING id::text`,
			fixture.TenantID, fixture.ExamID, answers[index].submission, "live-history", status, graderID, fixture.AdminID, fixture.QuestionIDs["single_choice"]).Scan(&taskID)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, taskID)
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := db.Exec(`INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round,created_at)
SELECT $1,id,answer_segment_id,$3,$4,1,'single',now()+make_interval(secs=>$5) FROM review_task WHERE tenant_id=$1 AND id=$2`,
				fixture.TenantID, taskID, graderID, index, attempt); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round,created_at)
SELECT $1,id,answer_segment_id,$3,1,1,'single',now()+interval '10 seconds' FROM review_task WHERE tenant_id=$1 AND id=$2`, fixture.TenantID, taskID, fixture.AdminID); err != nil {
			t.Fatal(err)
		}
	}
	got := read(graderToken, "&statistics=true&status=active&assigned_to="+fixture.AdminID+"&limit=1")
	if got.TotalCount != 0 || got.Statistics == nil || got.Statistics.Scope != "personal" || got.Statistics.GradedCount != 2 || got.Statistics.FullScoreCount != 1 || *got.Statistics.CorrectRate != 0.5 || *got.Statistics.AverageScoreRate != 0.5 {
		t.Fatalf("personal statistics leaked another opinion or counted duplicate history: %+v", got)
	}
	if len(got.Statistics.Questions) != 1 || got.Statistics.Questions[0].QuestionID != fixture.QuestionIDs["single_choice"] || got.Statistics.Questions[0].AverageScore == nil || *got.Statistics.Questions[0].AverageScore != 0.5 {
		t.Fatalf("personal question average leaked another opinion or counted duplicate history: %+v", got.Statistics.Questions)
	}
	first := e2eGetJSON(t, router, "/api/v1/review-tasks?exam_id="+fixture.ExamID+"&status=reviewed&limit=1", graderToken, http.StatusOK)
	second := e2eGetJSON(t, router, "/api/v1/review-tasks?exam_id="+fixture.ExamID+"&status=reviewed&limit=1&cursor="+url.QueryEscape(first["next_cursor"].(string)), graderToken, http.StatusOK)
	if first["has_more"] != true || second["has_more"] != false || len(first["tasks"].([]any)) != 1 || len(second["tasks"].([]any)) != 1 || first["tasks"].([]any)[0].(map[string]any)["id"] == second["tasks"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("submitted/completed history lost pagination: first=%+v second=%+v", first, second)
	}
	context := e2eGetJSON(t, router, "/api/v1/review-tasks/"+tasks[0]+"/context", graderToken, http.StatusOK)["context"].(map[string]any)
	if context["claim"].(map[string]any)["can_renew"] != false || context["human_grade"].(map[string]any)["score"] != float64(0) {
		t.Fatalf("history context lost own zero score or became editable: %+v", context)
	}

	handler := review.NewHandler(review.NewPostgresStore(db), nil, nil, nil)
	for _, tc := range []struct {
		name, school string
		count        int
		navigation   bool
	}{
		{"own school", fixture.SchoolID, 6, false},
		{"foreign school", uuid.NewString(), 0, false},
		{"foreign school present only for navigation", uuid.NewString(), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := auth.User{ID: fixture.AdminID, TenantID: fixture.TenantID, Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": tc.school}}}
			scope, err := auth.ResolveDeclaredAccessScope(user)
			if err != nil {
				t.Fatal(err)
			}
			if tc.navigation {
				scope.SchoolIDs = append(scope.SchoolIDs, fixture.SchoolID)
				scope.ExamIDs = append(scope.ExamIDs, fixture.ExamID)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?exam_id="+fixture.ExamID+"&statistics=true&limit=1", nil)
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
			response := httptest.NewRecorder()
			handler.ListTasks(response, request)
			var body struct {
				Aggregate review.TaskAggregate `json:"aggregate"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Aggregate.Statistics == nil || body.Aggregate.Statistics.GradedCount != tc.count {
				t.Fatalf("school statistics escaped scope: %d %s", response.Code, response.Body.String())
			}
			if tc.count == 0 && (body.Aggregate.Statistics.CorrectRate != nil || body.Aggregate.Statistics.AverageScoreRate != nil) {
				t.Fatal("no authorized samples must not appear as zero correctness")
			}
		})
	}
	t.Run("mixed school administrator cannot read external review or statistics", func(t *testing.T) {
		// 保留本人及他人的遗留外校任务；学校管理员本校限制必须同时覆盖个人筛选和整场队列。
		if _, err := db.Exec(`INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by)
SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,'other-reviewer','score_anomaly','completed',$3,$3
FROM review_task WHERE tenant_id=$1 AND id=$2`, fixture.TenantID, tasks[0], fixture.AdminID); err != nil {
			t.Fatal(err)
		}
		user := auth.User{ID: graderID, TenantID: fixture.TenantID, Roles: []string{"school_admin", "teacher"}, Permissions: []string{"review:manage", "review:work"}, DataScope: map[string]any{
			"school_admin": map[string]any{"scope": "school", "school_id": uuid.NewString()},
			"teacher":      map[string]any{"scope": "class", "class_id": fixture.ClassID},
		}}
		scope, err := auth.ResolveDeclaredAccessScope(user)
		if err != nil {
			t.Fatal(err)
		}
		scope.SchoolIDs, scope.ExamIDs = append(scope.SchoolIDs, fixture.SchoolID), []string{fixture.ExamID}
		for _, tc := range []struct {
			query, mode  string
			tasks, count int
		}{
			{"", "exam", 0, 0},
			{"&assigned_to=" + graderID, "personal", 0, 0},
			{"&assigned_to=" + fixture.AdminID, "personal", 0, 0},
		} {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?exam_id="+fixture.ExamID+"&statistics=true&limit=1"+tc.query, nil)
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
			response := httptest.NewRecorder()
			handler.ListTasks(response, request)
			var body struct {
				Aggregate review.TaskAggregate `json:"aggregate"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Aggregate.TotalCount != tc.tasks || body.Aggregate.Statistics == nil || body.Aggregate.Statistics.Scope != tc.mode || body.Aggregate.Statistics.GradedCount != tc.count {
				t.Fatalf("mixed-role scope query=%s: %d %s", tc.query, response.Code, response.Body.String())
			}
		}
	})
}
