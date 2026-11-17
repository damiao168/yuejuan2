package score

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type scorePublicationTestPublisher struct {
	coordinator *releasegate.PublicationCoordinator
	calls       int
}

func (p *scorePublicationTestPublisher) PublishPublication(ctx context.Context, tenant, exam, release, actor string) (scorerelease.Release, error) {
	p.calls++
	result, _, err := p.coordinator.Publish(ctx, tenant, exam, release, actor)
	return result, err
}

// 显式提供场景中的冻结事实，使用正式内存发布器和门禁协调器；不复制旧工作表发布算法。
func attachScorePublication(store *MemoryStore, exam string, facts ...scorerelease.SubmissionFact) (*scorerelease.MemoryStore, *scorerelease.Service, *scorePublicationTestPublisher) {
	releases := scorerelease.NewMemoryStore()
	releases.SeedFacts(exam, facts)
	service := scorerelease.NewService(releases)
	gates := releasegate.NewService(releasegate.NewMemoryStore(), service)
	publisher := &scorePublicationTestPublisher{coordinator: releasegate.NewPublicationCoordinator(gates, service)}
	store.WithPublication(service, publisher, nil)
	return releases, service, publisher
}

func scorePublicationFact(submission, student string, scores ...float64) scorerelease.SubmissionFact {
	fact := scorerelease.SubmissionFact{SubmissionID: submission, StudentID: student, Status: "confirmed"}
	for index, score := range scores {
		question := strconv.Itoa(index + 1)
		fact.TotalScore += score
		fact.MaxScore += 5
		fact.Questions = append(fact.Questions, scorerelease.QuestionFact{QuestionID: "question-" + question, QuestionNo: "Q" + question, FinalGradeID: "final-" + submission + "-" + question, Score: score, MaxScore: 5, SourceType: "single_review", SourceID: "private-review-" + question})
	}
	return fact
}

func TestLegacyPublicationCommandReplayCannotReplaceNewerFormalResult(t *testing.T) {
	store := NewMemoryStore()
	_, releases, publisher := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4))
	ctx := commandreceipt.WithID(t.Context(), "legacy-command-1")
	input := PublishInput{Reason: "initial formal publication"}
	first, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin-1", input)
	if err != nil || first.ScoreRelease == nil || first.ScoreRelease.Version != 1 || first.SubmissionGrades[0].TotalScore != 4 {
		t.Fatalf("legacy command did not create a formal version: %+v %v", first, err)
	}
	corrected, err := releases.CreateFromRegrade(t.Context(), tenantID, "exam-1", "admin-1", scorerelease.CreateRegradeInput{SourceReleaseID: first.ScoreRelease.ID, QuestionID: "question-1", Reason: "approved correction", IdempotencyKey: "correction-1", Changes: []scorerelease.RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 5, MaxScore: 5, ReviewedGradeID: "private-reviewed-correction"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.PublishPublication(t.Context(), tenantID, "exam-1", corrected.ID, "admin-1"); err != nil {
		t.Fatal(err)
	}
	calls := publisher.calls
	replay, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin-1", input)
	firstJSON, firstJSONErr := json.Marshal(first)
	replayJSON, replayJSONErr := json.Marshal(replay)
	// 回执采用 JSON，比较调用方实际接收的内容，忽略反序列化后 Go 时间值的内部表示。
	if err != nil || firstJSONErr != nil || replayJSONErr != nil || string(replayJSON) != string(firstJSON) || publisher.calls != calls {
		t.Fatalf("completed command was reissued or rewritten after correction: first=%s replay=%s errors=%v %v %v", firstJSON, replayJSON, err, firstJSONErr, replayJSONErr)
	}
	if _, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin-1", PublishInput{Reason: "changed request"}); !errors.Is(err, commandreceipt.ErrConflict) || publisher.calls != calls {
		t.Fatalf("changed command body did not conflict before publication: %v", err)
	}
	latest, err := store.PublishGrades(commandreceipt.WithID(t.Context(), "new-legacy-command"), tenantID, "exam-1", "admin-1", PublishInput{Reason: "confirm latest"})
	if err != nil || latest.ScoreRelease == nil || latest.ScoreRelease.ID != corrected.ID || latest.SubmissionGrades[0].TotalScore != 5 {
		t.Fatalf("legacy command rebuilt or downgraded current publication: %+v %v", latest, err)
	}
	all, err := releases.List(t.Context(), tenantID, "exam-1")
	current, currentErr := releases.CurrentPublished(t.Context(), tenantID, "exam-1")
	if err != nil || currentErr != nil || len(all) != 2 || current.Release.ID != corrected.ID {
		t.Fatalf("replay changed release history or current pointer: %+v %+v %v %v", all, current, err, currentErr)
	}
}

func TestLegacyPublicationAcceptsEmptyReasonWithoutIdempotencyHeader(t *testing.T) {
	for _, body := range []string{`{}`, `{"reason":""}`, `{"reason":"  "}`} {
		t.Run(body, func(t *testing.T) {
			store := NewMemoryStore()
			_, releases, _ := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4))
			handler := NewHandler(store, auth.NewMemoryStore())
			for attempt := 0; attempt < 2; attempt++ {
				r := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam-1/publish", strings.NewReader(body))
				r.SetPathValue("examId", "exam-1")
				r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "admin-1", TenantID: tenantID}))
				w := httptest.NewRecorder()
				handler.PublishGrades(w, r)
				var result PublishResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.ScoreRelease == nil || result.ScoreRelease.Version != 1 || result.ScoreRelease.Reason != "兼容旧客户端发布成绩" {
					t.Fatalf("old client request failed or created another version: %d %s %v", w.Code, w.Body.String(), err)
				}
			}
			all, err := releases.List(t.Context(), tenantID, "exam-1")
			if err != nil || len(all) != 1 {
				t.Fatalf("headerless retry created multiple versions: %+v %v", all, err)
			}
		})
	}
}

func TestBlockedLegacyPublicationCanChangeReasonOnlyWithoutExplicitCommandID(t *testing.T) {
	for _, explicitID := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicitID), func(t *testing.T) {
			store := NewMemoryStore()
			backing, releases, _ := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4))
			ctx := t.Context()
			if explicitID {
				ctx = commandreceipt.WithID(ctx, "blocked-explicit-command")
			}
			backing.SetGateIssues("exam-1", []scorerelease.GateIssue{{Code: "unfinished_review_tasks", Blocking: true, Count: 1}})
			if _, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin", PublishInput{Reason: "first attempt"}); !errors.Is(err, ErrQualityGateFailed) {
				t.Fatalf("control request did not reach the formal gate: %v", err)
			}
			backing.SetGateIssues("exam-1", nil)
			result, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin", PublishInput{Reason: "fixed and checked again"})
			if explicitID {
				if !errors.Is(err, commandreceipt.ErrConflict) {
					t.Fatalf("explicit command accepted another body after a blocked attempt: %+v %v", result, err)
				}
				if _, err := releases.CurrentPublished(t.Context(), tenantID, "exam-1"); !errors.Is(err, scorerelease.ErrNotFound) {
					t.Fatalf("conflicting body exposed a current result: %v", err)
				}
			} else if err != nil || result.ScoreRelease == nil || result.ScoreRelease.Reason != "fixed and checked again" || result.SubmissionGrades[0].TotalScore != 4 {
				t.Fatalf("headerless client could not retry with corrected reason: %+v %v", result, err)
			}
		})
	}
}

func TestLegacyStudentGetKeepsHiddenQuestionsAndIgnoresWorkingGrades(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(t.Context(), tenantID, "exam-1", "manager"); err != nil {
		t.Fatal(err)
	}
	_, releases, publisher := attachScorePublication(store, "exam-1", scorePublicationFact("frozen-submission", "student-1", 4))
	draft, err := releases.Create(t.Context(), tenantID, "exam-1", "admin", scorerelease.CreateInput{Reason: "total only", IdempotencyKey: "total-only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.PublishPublication(t.Context(), tenantID, "exam-1", draft.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	for id, grade := range store.submissions {
		grade.TotalScore, grade.Status = 999, "published"
		store.submissions[id] = grade
	}
	store.mu.Unlock()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/students/student-1/exams/exam-1/grade", nil)
	r.SetPathValue("studentId", "student-1")
	r.SetPathValue("examId", "exam-1")
	r = r.WithContext(auth.WithUser(r.Context(), scopedUser("student-user", map[string]any{"student_id": "student-1"}, "student:grade:read")))
	w := httptest.NewRecorder()
	NewHandler(store, auth.NewMemoryStore()).GetStudentGrade(w, r)
	var result struct {
		Grade SubmissionGrade `json:"grade"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Grade.ID != draft.ID || result.Grade.SubmissionID != "frozen-submission" || result.Grade.TotalScore != 4 || len(result.Grade.Items) != 0 || strings.Contains(w.Body.String(), `"items"`) || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "999") {
		t.Fatalf("legacy student GET mixed working data or restored hidden facts: %d %s %v", w.Code, w.Body.String(), err)
	}
}

func TestLegacyPublicationWithoutCanonicalConfigurationFailsClosed(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.PublishGrades(t.Context(), tenantID, "exam-1", "admin", PublishInput{}); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatalf("unconfigured publication fell back to working grades: %v", err)
	}
	if _, err := store.GetStudentGrade(t.Context(), tenantID, "student-1", "exam-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unconfigured student read returned working grades: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam-1/publish", strings.NewReader(`{}`))
	r.SetPathValue("examId", "exam-1")
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "admin", TenantID: tenantID}))
	w := httptest.NewRecorder()
	NewHandler(store, auth.NewMemoryStore()).PublishGrades(w, r)
	if w.Code != http.StatusServiceUnavailable || len(store.finals) != 0 || len(store.submissions) != 0 {
		t.Fatalf("unconfigured HTTP publish wrote a working result: %d %s", w.Code, w.Body.String())
	}
}

func TestLegacyPublicationUsesFormalSnapshotGateInsteadOfWorkingTableState(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "missing questions", "mismatched total"} {
		t.Run(scenario, func(t *testing.T) {
			store := NewMemoryStore()
			fact := scorePublicationFact("submission-1", "student-1", 4)
			code := "grades_not_confirmed"
			switch scenario {
			case "unconfirmed":
				fact.Status = "pending_confirmation"
			case "missing questions":
				fact.Questions, code = nil, "release_snapshot_incomplete"
			case "mismatched total":
				fact.TotalScore, code = 3, "score_integrity_mismatch"
			}
			_, releases, _ := attachScorePublication(store, "exam-1", fact)
			result, err := store.PublishGrades(t.Context(), tenantID, "exam-1", "admin", PublishInput{Reason: "must pass canonical gate"})
			if !errors.Is(err, ErrQualityGateFailed) || result.Status != "blocked" || result.Quality.Passed || !hasIssue(result.Quality.Issues, code) {
				t.Fatalf("canonical blocker lost in legacy response: %+v %v", result, err)
			}
			if _, err := releases.CurrentPublished(t.Context(), tenantID, "exam-1"); !errors.Is(err, scorerelease.ErrNotFound) {
				t.Fatalf("blocked publication exposed a current version: %v", err)
			}
		})
	}
}

func TestLegacyHistoricalCommandReceiptReplaysWithoutPublishingAgain(t *testing.T) {
	store := NewMemoryStore()
	_, _, publisher := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4))
	ctx := commandreceipt.WithID(t.Context(), "historical-command")
	input := PublishInput{Reason: "historical publication"}
	historical := PublishResult{Status: "published", Quality: QualityReport{Passed: true}, SubmissionGrades: []SubmissionGrade{{ID: "historical-grade", SubmissionID: "submission-1", TotalScore: 4, Status: "published"}}}
	if err := store.receipts.Save(ctx, tenantID, "admin", "score.publish", "exam-1", input, historical); err != nil {
		t.Fatal(err)
	}
	result, err := store.PublishGrades(ctx, tenantID, "exam-1", "admin", input)
	if err != nil || !reflect.DeepEqual(result, historical) || publisher.calls != 0 {
		t.Fatalf("historical receipt triggered a new publication: %+v %v calls=%d", result, err, publisher.calls)
	}
}
