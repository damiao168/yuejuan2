package appeal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func opinionInput(revision int64) SubmitQuestionAppealReviewOpinionInput {
	return SubmitQuestionAppealReviewOpinionInput{Opinion: QuestionAppealOpinionSupport, Explanation: "核对原卷第二步，应补过程分", ExpectedRevision: revision}
}

func TestPublishedQuestionAppealReviewOpinionPreservesScoreAndWaitsForManager(t *testing.T) {
	for _, choice := range []string{QuestionAppealOpinionSupport, QuestionAppealOpinionDoNotSupport, QuestionAppealOpinionNeedsEvidence} {
		t.Run(choice, func(t *testing.T) {
			assertQuestionAppealOpinionPreservesFrozenGrade(t, choice)
		})
	}
}

func TestPublishedQuestionAppealReviewOpinionRejectsInvalidOrStaleWorkWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PublishedQuestionAppealMemoryStore, *PublishedQuestionAppeal, *SubmitQuestionAppealReviewOpinionInput, *string, *string)
		want   error
	}{
		{"unassigned actor", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, _ *SubmitQuestionAppealReviewOpinionInput, _ *string, actor *string) {
			*actor = "another-teacher"
		}, ErrForbidden},
		{"other tenant", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, _ *SubmitQuestionAppealReviewOpinionInput, tenant *string, _ *string) {
			*tenant = "tenant-other"
		}, ErrNotFound},
		{"stale revision", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			input.ExpectedRevision++
		}, ErrRevisionConflict},
		{"unsupported choice", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			input.Opinion = "reject"
		}, ErrInvalidInput},
		{"empty explanation", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			input.Explanation = "  "
		}, ErrInvalidInput},
		{"long unicode explanation", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			input.Explanation = strings.Repeat("证", 4001)
		}, ErrInvalidInput},
		{"above question maximum", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			score := 9.0
			input.SuggestedScore = &score
		}, ErrInvalidInput},
		{"negative score", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			score := -1.0
			input.SuggestedScore = &score
		}, ErrInvalidInput},
		{"nonfinite score", func(_ *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, input *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			score := math.NaN()
			input.SuggestedScore = &score
		}, ErrInvalidInput},
		{"original grader", func(store *PublishedQuestionAppealMemoryStore, item *PublishedQuestionAppeal, _ *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			store.sourceReviewers[item.TenantID+"\x00"+item.SourceFinalGradeID] = []string{item.AssignedTo}
		}, ErrForbidden},
		{"new published version", func(store *PublishedQuestionAppealMemoryStore, _ *PublishedQuestionAppeal, _ *SubmitQuestionAppealReviewOpinionInput, _ *string, _ *string) {
			source := publishedQuestionWorkflowSource()
			source.ReleaseID, source.ReleaseVersion = "release-2", 2
			store.SeedSource(source)
		}, ErrSourceRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, service, item := publishedQuestionWorkflowFixture(t)
			item = startPublishedQuestionWorkflowReview(t, service, item)
			input, tenant, actor := opinionInput(item.Revision), item.TenantID, item.AssignedTo
			tc.mutate(store, &item, &input, &tenant, &actor)
			if _, err := service.SubmitReviewOpinion(t.Context(), tenant, item.ID, actor, input); !errors.Is(err, tc.want) {
				t.Fatalf("error: %v, want %v", err, tc.want)
			}
			assertPublishedQuestionWorkflowUnchanged(t, store, item, 2)
		})
	}
}

func TestPublishedQuestionAppealReviewOpinionSerializesConcurrentRevisions(t *testing.T) {
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.SubmitReviewOpinion(context.Background(), item.TenantID, item.ID, item.AssignedTo, opinionInput(item.Revision))
			results <- err
		}()
	}
	group.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatalf("concurrent opinion results: %d passed %d conflicts", passed, conflicts)
	}
	events, _ := store.ListPublishedQuestionAppealEvents(t.Context(), item.TenantID, item.ID)
	if len(events) != 3 {
		t.Fatalf("duplicate opinion event: %+v", events)
	}
}

func TestPublishedQuestionAppealReviewOpinionHandlerScopesAndPrivateResponse(t *testing.T) {
	for _, tc := range []questionAppealOpinionHandlerCase{
		{"assigned teacher", "independent-reviewer", `{"opinion":"support_adjustment","suggested_score":7,"explanation":"应补过程分","expected_revision":2}`, []string{appealWorkPermission}, http.StatusOK},
		{"different assignee", "other-teacher", `{"opinion":"needs_evidence","explanation":"缺原卷","expected_revision":2}`, []string{appealWorkPermission}, http.StatusForbidden},
		{"manager without work assignment", "manager-1", `{"opinion":"needs_evidence","explanation":"缺原卷","expected_revision":2}`, []string{appealManagePermission}, http.StatusForbidden},
		{"student cannot supply grade", "independent-reviewer", `{"opinion":"support_adjustment","explanation":"应补分","expected_revision":2}`, []string{"student:grade:read"}, http.StatusForbidden},
		{"injected score mutation", "independent-reviewer", `{"opinion":"support_adjustment","explanation":"应补分","expected_revision":2,"score":7}`, []string{appealWorkPermission}, http.StatusBadRequest},
		{"stale response", "independent-reviewer", `{"opinion":"needs_evidence","explanation":"缺原卷","expected_revision":1}`, []string{appealWorkPermission}, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertQuestionAppealOpinionHandlerScope(t, tc)
		})
	}
}

func TestPublishedQuestionAppealWorkerCannotBypassOpinionWithDecisionOrResolution(t *testing.T) {
	_, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore())
	for _, action := range []http.HandlerFunc{handler.Decide, handler.Resolve} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/question-appeals/"+item.ID, strings.NewReader(`{"decision":"reject","public_response":"维持原成绩","expected_revision":2}`))
		request.SetPathValue("id", item.ID)
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: item.AssignedTo, TenantID: item.TenantID, Permissions: []string{appealWorkPermission}}))
		response := httptest.NewRecorder()
		action(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("worker bypassed manager final decision: %d %s", response.Code, response.Body)
		}
	}
}

func TestPublishedQuestionPostgresOpinionChecksCurrentReleaseBeforeAnyWrite(t *testing.T) {
	item := publishedQuestionPostgresAppealFixture()
	item.Status, item.AssignedTo = QuestionAppealUnderReview, "teacher-independent"
	scenario := &publishedQuestionPostgresScenario{queries: []publishedQuestionPostgresQuery{
		{fragments: []string{"FOR UPDATE"}, args: []any{item.TenantID, item.ID}, row: publishedQuestionPostgresAppealRow(item)},
		{fragments: []string{"user_row.status = 'active'", "permission_row.code = 'appeal:work'"}, args: []any{item.TenantID, item.AssignedTo}, row: []driver.Value{true}},
		{fragments: []string{"grade.id = $2::uuid", "human.reviewer_id = $3::uuid"}, args: []any{item.TenantID, item.SourceFinalGradeID, item.AssignedTo}, row: []driver.Value{false}},
		{fragments: []string{"current_release.tenant_id=$1", "current_release.exam_id=$2::uuid", "release.status='published'", "FOR SHARE OF current_release"}, args: []any{item.TenantID, item.ExamID}, row: []driver.Value{"release-current-other"}},
	}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	_, err := NewPublishedQuestionAppealPostgresStore(db).SubmitPublishedQuestionAppealReviewOpinion(t.Context(), item.TenantID, item.ID, item.AssignedTo, opinionInput(item.Revision))
	if !errors.Is(err, ErrSourceRelease) || len(scenario.queries) != 0 || !scenario.rolledBack || scenario.committed {
		t.Fatalf("obsolete source opinion: %v %+v", err, scenario)
	}
}

func TestPublishedQuestionPostgresOpinionCommitsRevisionAndEventTogether(t *testing.T) {
	for _, failEvent := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted handoff", true: "event failure rolls back revision"}[failEvent], func(t *testing.T) {
			assertPostgresOpinionRevisionAndEventAtomicity(t, failEvent)
		})
	}
}

func TestPublishedQuestionPostgresOpinionProjectionRecoversPersistedZeroScore(t *testing.T) {
	item := publishedQuestionPostgresAppealFixture()
	zero := 0.0
	item.ReviewOpinion = &QuestionAppealReviewOpinion{Opinion: QuestionAppealOpinionDoNotSupport, SuggestedScore: &zero, Explanation: "按原发布要点核对", SubmittedAt: item.UpdatedAt}
	scenario := &publishedQuestionPostgresScenario{queries: []publishedQuestionPostgresQuery{{fragments: []string{"event.event_type='review_opinion_submitted'", "ORDER BY (event.payload->>'revision')::bigint DESC", "event.tenant_id=question_appeal.tenant_id"}, args: []any{item.TenantID, item.ID}, row: publishedQuestionPostgresAppealRow(item)}}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	got, err := NewPublishedQuestionAppealPostgresStore(db).GetPublishedQuestionAppeal(t.Context(), item.TenantID, item.ID)
	if err != nil || got.ReviewOpinion == nil || got.ReviewOpinion.SuggestedScore == nil || *got.ReviewOpinion.SuggestedScore != 0 || got.ReviewOpinion.Explanation != item.ReviewOpinion.Explanation {
		t.Fatalf("persisted opinion projection: %+v %v", got, err)
	}
}

func assertQuestionAppealOpinionPreservesFrozenGrade(t *testing.T, choice string) {
	t.Helper()
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	input := opinionInput(item.Revision)
	zero := 0.0
	input.Opinion, input.SuggestedScore = choice, &zero
	updated, err := service.SubmitReviewOpinion(t.Context(), item.TenantID, item.ID, item.AssignedTo, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != QuestionAppealUnderReview || updated.Revision != item.Revision+1 || updated.SourceScore != item.SourceScore || updated.Decision != "" || updated.RegradeJobID != "" || updated.PublicResponse != "" || updated.NewReleaseID != "" {
		t.Fatalf("opinion changed a score or final decision: %+v", updated)
	}
	if updated.ReviewOpinion == nil || updated.ReviewOpinion.SuggestedScore == nil || *updated.ReviewOpinion.SuggestedScore != 0 || updated.ReviewOpinion.Opinion != choice {
		t.Fatalf("missing independent opinion: %+v", updated.ReviewOpinion)
	}
	// 请求及返回对象不能通过共享指针改写已经移交的意见；经理读取同一持久化记录。
	zero = 8
	*updated.ReviewOpinion.SuggestedScore = 9
	queue, err := service.List(t.Context(), item.TenantID, QuestionAppealFilter{ExamID: item.ExamID})
	if err != nil || len(queue) != 1 || *queue[0].ReviewOpinion.SuggestedScore != 0 {
		t.Fatalf("opinion was mutated after handoff: %+v %v", queue, err)
	}
	public, _ := json.Marshal(studentQuestionAppealView(queue[0]))
	for _, secret := range []string{"review_opinion", "suggested_score", input.Explanation, item.AssignedTo} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("student saw private opinion: %s", public)
		}
	}
	events, _ := store.ListPublishedQuestionAppealEvents(t.Context(), item.TenantID, item.ID)
	if len(events) != 3 || events[2].Type != "review_opinion_submitted" || events[2].Payload["revision"] != updated.Revision {
		t.Fatalf("missing durable opinion event: %+v", events)
	}
}

type questionAppealOpinionHandlerCase struct {
	name, actor, body string
	permissions       []string
	status            int
}

func assertQuestionAppealOpinionHandlerScope(t *testing.T, tc questionAppealOpinionHandlerCase) {
	t.Helper()
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/question-appeals/"+item.ID+"/review-opinion", strings.NewReader(tc.body))
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: tc.actor, TenantID: item.TenantID, Permissions: tc.permissions}))
	response := httptest.NewRecorder()
	handler.SubmitReviewOpinion(response, request)
	if response.Code != tc.status {
		t.Fatalf("response %d: %s", response.Code, response.Body)
	}
	if tc.status != http.StatusOK {
		assertPublishedQuestionWorkflowUnchanged(t, store, item, 2)
		return
	}
	var body struct {
		Appeal PublishedQuestionAppeal `json:"appeal"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Appeal.ReviewOpinion == nil || body.Appeal.Status != QuestionAppealUnderReview || body.Appeal.SourceScore != item.SourceScore {
		t.Fatalf("invalid handoff response: %+v", body)
	}
	for _, secret := range []string{item.StudentID, item.SubmissionID, item.CreatedBy} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("worker response identity leak: %s", response.Body)
		}
	}
}

func assertPostgresOpinionRevisionAndEventAtomicity(t *testing.T, failEvent bool) {
	t.Helper()
	item := publishedQuestionPostgresAppealFixture()
	item.Status, item.AssignedTo = QuestionAppealUnderReview, "teacher-independent"
	zero := 0.0
	input := opinionInput(item.Revision)
	input.SuggestedScore = &zero
	var eventErr error
	if failEvent {
		eventErr = errors.New("event unavailable")
	}
	payload, _ := json.Marshal(map[string]any{"opinion": "support_adjustment", "suggested_score": 0, "explanation": input.Explanation, "submitted_at": item.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), "revision": item.Revision + 1})
	scenario := &publishedQuestionPostgresScenario{allowCommit: true, queries: []publishedQuestionPostgresQuery{
		{fragments: []string{"FOR UPDATE"}, args: []any{item.TenantID, item.ID}, row: publishedQuestionPostgresAppealRow(item)},
		{fragments: []string{"permission_row.code = 'appeal:work'"}, args: []any{item.TenantID, item.AssignedTo}, row: []driver.Value{true}},
		{fragments: []string{"grade.id = $2::uuid"}, args: []any{item.TenantID, item.SourceFinalGradeID, item.AssignedTo}, row: []driver.Value{false}},
		{fragments: []string{"FOR SHARE OF current_release"}, args: []any{item.TenantID, item.ExamID}, row: []driver.Value{item.SourceReleaseID}},
		{fragments: []string{"UPDATE question_appeal SET updated_at=now(), revision=revision+1", "tenant_id=$1 AND id=$2::uuid AND revision=$3"}, args: []any{item.TenantID, item.ID, item.Revision}, row: []driver.Value{item.UpdatedAt, item.Revision + 1}},
	}, execs: []publishedQuestionPostgresExec{{fragments: []string{"INSERT INTO question_appeal_event"}, args: []any{item.TenantID, item.ID, "review_opinion_submitted", item.AssignedTo, string(payload)}, err: eventErr}}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	updated, err := NewPublishedQuestionAppealPostgresStore(db).SubmitPublishedQuestionAppealReviewOpinion(t.Context(), item.TenantID, item.ID, item.AssignedTo, input)
	if len(scenario.queries) != 0 || len(scenario.execs) != 0 {
		t.Fatalf("transaction omitted required checks: %+v", scenario)
	}
	if failEvent {
		if !errors.Is(err, eventErr) || scenario.committed || !scenario.rolledBack {
			t.Fatalf("event failure committed revision: %v %+v", err, scenario)
		}
	} else if err != nil || !scenario.committed || updated.ReviewOpinion == nil || *updated.ReviewOpinion.SuggestedScore != 0 || updated.SourceScore != item.SourceScore || updated.Decision != "" || updated.Status != QuestionAppealUnderReview {
		t.Fatalf("handoff result: %+v %v %+v", updated, err, scenario)
	}
}
