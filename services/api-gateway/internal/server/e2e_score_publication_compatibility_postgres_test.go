package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

func TestLegacyScorePublicationUsesCurrentSnapshotWithPostgresTestDatabase(t *testing.T) {
	db, router, admin, fixture, answers, _ := repairFixture(t)
	// 使用接入真实质量看板的路由。发布如果额外借用连接，单连接池会一直等待自身释放。
	db.SetMaxOpenConns(1)
	application := router
	router = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/publish") {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		application.ServeHTTP(w, r)
	})
	answer := answers[0]
	tenant, exam := fixture.TenantID, fixture.ExamID
	// 已取消的批次不阻断发布，但仍会让质量看板读取批次详情和明细统计。
	if _, err := db.ExecContext(t.Context(), `
INSERT INTO backmark_batch(tenant_id,exam_id,question_id,source_incident_id,status,created_by)
VALUES($1::uuid,$2::uuid,$3::uuid,'single-connection publication','cancelled',$4::uuid)
`, tenant, exam, fixture.QuestionIDs["true_false"], fixture.AdminID); err != nil {
		t.Fatal(err)
	}
	// 已回滚的答案组仍需读取组详情、成员和决定，便于检验嵌套查询能否复用连接。
	if _, err := db.ExecContext(t.Context(), `
INSERT INTO answer_group(tenant_id,exam_id,question_id,exam_question_snapshot_id,build_input_hash,algorithm_version,representation_version,
 member_count,representative_submission_id,homogeneity,minimum_sample,status,created_by)
VALUES($1::uuid,$2::uuid,$3::uuid,(SELECT id FROM exam_question_snapshot WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 ORDER BY snapshot_version DESC LIMIT 1),
 repeat('0',64),'test','test',1,$4::uuid,1,1,'rolled_back',$5::uuid)
`, tenant, exam, fixture.QuestionIDs["true_false"], answer.submission, fixture.AdminID); err != nil {
		t.Fatal(err)
	}
	var schoolCode, studentNo string
	var appealEnabled bool
	if err := db.QueryRowContext(t.Context(), `SELECT school.code,c.student_no_snapshot,exam.appeal_enabled FROM school JOIN exam ON exam.tenant_id=school.tenant_id AND exam.school_id=school.id JOIN exam_candidate_snapshot c ON c.tenant_id=exam.tenant_id AND c.exam_id=exam.id WHERE school.id=$1 AND exam.id=$2 AND c.student_id=$3`, fixture.SchoolID, exam, answer.student).Scan(&schoolCode, &studentNo, &appealEnabled); err != nil {
		t.Fatal(err)
	}
	if !appealEnabled {
		t.Fatal("fixture must allow question appeals before publishing")
	}
	student := e2eLoginProvisionedStudent(t, router, schoolCode, studentNo, "SyntheticStudent123!")
	path := "/api/v1/exams/" + exam
	legacyStudent := "/api/v1/students/" + answer.student + "/exams/" + exam + "/grade"
	studentResult := "/api/v1/student/exams/" + exam + "/result"
	setWorkingTotal := func(total int) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `UPDATE submission_grade SET total_score=$4 WHERE tenant_id=$1 AND exam_id=$2 AND submission_id=$3`, tenant, exam, answer.submission, total); err != nil {
			t.Fatal(err)
		}
	}
	versions := func() (published, all int) {
		t.Helper()
		if err := db.QueryRowContext(t.Context(), `SELECT count(*) FILTER(WHERE status='published'),count(*) FROM score_release WHERE tenant_id=$1 AND exam_id=$2`, tenant, exam).Scan(&published, &all); err != nil {
			t.Fatal(err)
		}
		return
	}

	// 可变总分与已确认题分不符时，兼容接口也必须通过正式门禁，且不能提前公开学生结果。
	setWorkingTotal(2)
	blocked := e2eExpectStatus(t, router, http.MethodPost, path+"/publish", admin, `{"reason":"reject wrong working total"}`, http.StatusConflict)
	if !strings.Contains(blocked.Body.String(), "score_integrity_mismatch") {
		t.Fatalf("expected total mismatch blocker, got %s", blocked.Body.String())
	}
	if published, _ := versions(); published != 0 {
		t.Fatalf("blocked request published %d versions", published)
	}
	e2eExpectStatus(t, router, http.MethodGet, path+"/score-releases/current-published", admin, "", http.StatusNotFound)
	e2eExpectStatus(t, router, http.MethodGet, legacyStudent, student, "", http.StatusNotFound)
	e2eExpectStatus(t, router, http.MethodGet, studentResult, student, "", http.StatusNotFound)
	setWorkingTotal(0)

	key := map[string]string{"Idempotency-Key": uuid.NewString()}
	const body = `{"reason":"release all verified answers"}`
	first := e2eExpectStatus(t, router, http.MethodPost, path+"/publish", admin, body, http.StatusOK, key)
	var initialResult score.PublishResult
	if err := json.Unmarshal(first.Body.Bytes(), &initialResult); err != nil {
		t.Fatal(err)
	}
	originalCurrent := e2eGetJSON(t, router, path+"/score-releases/current-published", admin, http.StatusOK)["release"].(map[string]any)
	original := e2eString(t, originalCurrent, "id")
	if originalCurrent["version"] != float64(1) || originalCurrent["status"] != "published" || initialResult.ScoreRelease == nil || initialResult.ScoreRelease.ID != original || initialResult.Status != "published" || !initialResult.Quality.Passed || len(initialResult.SubmissionGrades) != 2 {
		t.Fatalf("compatibility response did not publish the official initial release: %s current=%#v", first.Body.String(), originalCurrent)
	}
	if initialResult.ScoreRelease.GateSnapshot.Counts["gold_coverage_incomplete"] == 0 {
		t.Fatal("published gate snapshot must retain the quality dashboard findings")
	}
	if !initialResult.ScoreRelease.AppealWindow.Enabled || originalCurrent["appeal_window"].(map[string]any)["enabled"] != true {
		t.Fatalf("compatibility publication dropped the enabled appeal window: %#v", originalCurrent)
	}
	if published, all := versions(); published != 1 || all != 1 {
		t.Fatalf("expected a single initial release, published=%d all=%d", published, all)
	}
	var evidence, audit int
	if err := db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*) FROM release_gate_evidence WHERE tenant_id=$1 AND exam_id=$2 AND release_id=$3 AND phase='publish'),
 (SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action='score_release.published' AND target_type='score_release' AND target_id=$3 AND after_value->>'exam_id'=$2::uuid::text AND after_value->>'version'='1')`, tenant, exam, original).Scan(&evidence, &audit); err != nil || evidence != 1 || audit != 1 {
		t.Fatalf("legacy initial publication lost versioned evidence/audit: evidence=%d audit=%d err=%v", evidence, audit, err)
	}
	assertRead := func(releaseID string, version int, total float64) {
		t.Helper()
		legacy := e2eGetJSON(t, router, legacyStudent, student, http.StatusOK)["grade"].(map[string]any)
		modern := e2eGetJSON(t, router, studentResult, student, http.StatusOK)["result"].(map[string]any)
		if legacy["id"] != releaseID || modern["release_id"] != releaseID || legacy["revision"] != float64(version) || modern["release_version"] != float64(version) || legacy["total_score"] != total || modern["total_score"] != total {
			t.Fatalf("student APIs diverged from current release: legacy=%#v current=%#v", legacy, modern)
		}
		appealWindow := modern["appeal_window"].(map[string]any)
		if appealWindow["state"] != "open" || appealWindow["open"] != true {
			t.Fatalf("published student appeal window is not open: %#v", appealWindow)
		}
		oldQuestions := legacy["items"].([]any)
		newQuestions := modern["questions"].([]any)
		if len(oldQuestions) != 3 || len(newQuestions) != len(oldQuestions) {
			t.Fatalf("student APIs diverged on question coverage: %v %v", oldQuestions, newQuestions)
		}
		for i, item := range oldQuestions {
			oldQuestion, newQuestion := item.(map[string]any), newQuestions[i].(map[string]any)
			if oldQuestion["question_id"] != newQuestion["question_id"] || oldQuestion["score"] != newQuestion["score"] || oldQuestion["id"] != "" || oldQuestion["source"] != "" {
				t.Fatalf("student question score or visibility mismatch: legacy=%#v current=%#v", oldQuestion, newQuestion)
			}
		}
		csvResponse := e2eExpectStatus(t, router, http.MethodGet, path+"/score-releases/export?format=csv", admin, "", http.StatusOK)
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(csvResponse.Body.String(), "\ufeff"))).ReadAll()
		if err != nil || len(rows) != 3 {
			t.Fatalf("invalid formal CSV: rows=%v err=%v", rows, err)
		}
		for _, row := range rows[1:] {
			if row[2] != studentNo {
				continue
			}
			var scoreCell, releaseCell, versionCell string
			for i, column := range rows[0] {
				switch {
				case strings.HasSuffix(column, "·成绩"):
					scoreCell = row[i]
				case strings.HasSuffix(column, "·发布版本ID"):
					releaseCell = row[i]
				case strings.HasSuffix(column, "·发布版本"):
					versionCell = row[i]
				}
			}
			if releaseCell != releaseID || scoreCell != fmt.Sprintf("%.2f", total) || versionCell != fmt.Sprint(version) {
				t.Fatalf("formal CSV did not use the same published version and score: %v", row)
			}
			return
		}
		t.Fatalf("student %q missing from formal CSV", studentNo)
	}
	assertRead(original, 1, 0)

	// 复评事实由既有存储构造，正式发布仍经过新 HTTP 入口。旧命令重放只能返回原回执。
	question := fixture.QuestionIDs["single_choice"]
	service := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil))
	corrected, err := service.CreateFromRegrade(t.Context(), tenant, exam, fixture.AdminID, scorerelease.CreateRegradeInput{
		SourceReleaseID: original, QuestionID: question, Reason: "verified correction", IdempotencyKey: uuid.NewString(),
		Changes: []scorerelease.RegradeChange{{SubmissionID: answer.submission, QuestionID: question, Score: 1, MaxScore: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/score-releases/"+corrected.ID+"/publish", admin, `{}`, http.StatusOK)
	replay := e2eExpectStatus(t, router, http.MethodPost, path+"/publish", admin, body, http.StatusOK, key)
	if !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("legacy replay changed the original receipt: first=%s replay=%s", first.Body.String(), replay.Body.String())
	}
	receiptResponse := e2eExpectStatus(t, router, http.MethodGet, "/api/v1/score-commands/"+key["Idempotency-Key"], admin, "", http.StatusOK)
	var receipt commandreceipt.Receipt
	var recovered score.PublishResult
	if err := json.Unmarshal(receiptResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(receipt.Result, &recovered); err != nil || receipt.Status != "succeeded" || receipt.Operation != "score.publish" || recovered.ScoreRelease == nil || recovered.ScoreRelease.ID != original {
		t.Fatalf("recovery endpoint did not preserve the original release: %+v %v", receipt, err)
	}
	e2eExpectStatus(t, router, http.MethodPost, path+"/publish", admin, `{"reason":"changed legacy request"}`, http.StatusConflict, key)
	fresh := e2ePostJSON(t, router, http.MethodPost, path+"/publish", admin, body, http.StatusOK)
	if fresh["score_release"].(map[string]any)["id"] != corrected.ID {
		t.Fatalf("fresh legacy request did not recognize the current official version: %#v", fresh)
	}
	current := e2eGetJSON(t, router, path+"/score-releases/current-published", admin, http.StatusOK)["release"].(map[string]any)
	if current["id"] != corrected.ID || current["version"] != float64(2) {
		t.Fatalf("legacy retry rewound the newer release: %#v", current)
	}
	if published, all := versions(); published != 2 || all != 2 {
		t.Fatalf("legacy retry created extra versions, published=%d all=%d", published, all)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action='score_release.published' AND target_type='score_release' AND target_id IN ($2,$3)`, tenant, original, corrected.ID).Scan(&audit); err != nil || audit != 2 {
		t.Fatalf("legacy replay or current-version lookup added a duplicate publication audit: count=%d err=%v", audit, err)
	}
	assertRead(corrected.ID, 2, 1)
	questionAppeal := e2ePostJSON(t, router, http.MethodPost, "/api/v1/student/exams/"+exam+"/question-appeals", student, story056JSON(t, appeal.CreatePublishedQuestionAppealInput{
		ExamID: exam, SourceReleaseID: corrected.ID, QuestionID: question, ReasonCode: appeal.ReasonCalculationError, Reason: "please verify the published question score",
	}), http.StatusCreated)["appeal"].(map[string]any)
	if questionAppeal["source_release_id"] != corrected.ID || questionAppeal["source_release_version"] != float64(2) || questionAppeal["source_score"] != float64(1) {
		t.Fatalf("new question appeal did not freeze the displayed version and score: %#v", questionAppeal)
	}
}
