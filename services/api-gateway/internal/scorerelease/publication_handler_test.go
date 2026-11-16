package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

const (
	releaseTestExamPath = "/api/v1/exams/" + exam + "/score-releases"
	releaseTestCurrent  = releaseTestExamPath + "/current-published"
	releaseTestStudent  = "/api/v1/student/exams/" + exam
)

func publicationTestMux(h *Handler) http.Handler {
	guard := func(handler http.HandlerFunc) http.Handler { return handler }
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, RouteGuards{
		ExamManage: guard, ReleaseManage: guard, CriticalRelease: guard, CriticalExam: guard, StudentRead: guard,
	})
	return mux
}

func requestPublication(h http.Handler, user *auth.User, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *user))
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func requirePublicationStatus(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	if code != "" && response.Header().Get(httpx.ErrorCodeHeader) != code {
		t.Fatalf("code=%q want=%q body=%s", response.Header().Get(httpx.ErrorCodeHeader), code, response.Body.String())
	}
}

func readPublicationResponse[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	requirePublicationStatus(t, response, status, "")
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	return result
}

type publicationResponse struct {
	Release Release `json:"score_release"`
}

func TestPublicationRoutesKeepCurrentSnapshotDuringBlockedPublishAndRollback(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	audit := &formalAuditSink{}
	handler := publicationTestMux(NewHandler(service, audit))
	manager := &auth.User{ID: actor, TenantID: tenant}

	request := func(method, path, body string) *httptest.ResponseRecorder {
		return requestPublication(handler, manager, method, path, body)
	}
	create := func(body string) Release {
		return readPublicationResponse[publicationResponse](t, request(http.MethodPost, releaseTestExamPath, body), http.StatusCreated).Release
	}
	publish := func(id string) Release {
		response := request(http.MethodPost, "/api/v1/score-releases/"+id+"/publish", "")
		return readPublicationResponse[publicationResponse](t, response, http.StatusOK).Release
	}
	current := func(status int) *httptest.ResponseRecorder {
		response := request(http.MethodGet, releaseTestCurrent, "")
		requirePublicationStatus(t, response, status, "")
		return response
	}

	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	first := create(`{"reason":"first approved release","idempotency_key":"handler-release-1","visibility_policy":{"show_question_scores":true}}`)
	if first.Status != StatusDraft || audit.event.Action != "score_release.created" || audit.event.TargetID != first.ID {
		t.Fatalf("unexpected draft or audit: release=%+v audit=%+v", first, audit.event)
	}
	current(http.StatusNotFound)
	publish(first.ID)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(5)})
	second := create(`{"source":"appeal","reason":"approved correction","idempotency_key":"handler-release-2","visibility_policy":{"show_question_scores":true}}`)
	secondPath := "/api/v1/score-releases/" + second.ID

	diff := readPublicationResponse[struct {
		Diff Diff `json:"diff"`
	}](t, request(http.MethodGet, secondPath+"/diff?base="+first.ID, ""), http.StatusOK).Diff
	if len(diff.Questions) != 1 || diff.Questions[0].OldScore != 4 || diff.Questions[0].NewScore != 5 {
		t.Fatalf("unexpected correction diff: %+v", diff)
	}
	preview := readPublicationResponse[struct {
		Result StudentResult `json:"result"`
	}](t, request(http.MethodGet, secondPath+"/student-preview?student_id=student-1", ""), http.StatusOK).Result
	if preview.ReleaseID != second.ID || preview.TotalScore != 5 {
		t.Fatalf("preview is not pinned to the draft: %+v", preview)
	}

	// 草稿创建后新出现的复核阻断必须在发布时重新检查，学生仍读取上一版正式成绩。
	store.SetGateIssues(exam, []GateIssue{{Code: "unresolved_review", Blocking: true, Count: 1}})
	blocked := readPublicationResponse[struct {
		Code string `json:"code"`
		Gate Gate   `json:"release_gate"`
	}](t, request(http.MethodPost, secondPath+"/publish", ""), http.StatusConflict)
	if blocked.Code != "score_release_gate_blocked" || blocked.Gate.Passed || len(blocked.Gate.Blocking) != 1 ||
		blocked.Gate.Blocking[0].Code != "unresolved_review" || audit.event.Action != "score_release.created" {
		t.Fatalf("unexpected blocking response or publish audit: gate=%+v audit=%+v", blocked, audit.event)
	}
	detail := readPublicationResponse[Detail](t, current(http.StatusOK), http.StatusOK)
	if detail.Release.ID != first.ID || len(detail.Items) != 1 || detail.Items[0].TotalScore != 4 {
		t.Fatalf("blocked publish moved the current snapshot: %+v", detail)
	}

	store.SetGateIssues(exam, nil)
	if published := publish(second.ID); published.SupersedesReleaseID != first.ID || published.PublishedAt == nil ||
		audit.event.Action != "score_release.published" || audit.event.TargetID != second.ID || audit.event.AfterValue["exam_id"] != exam {
		t.Fatalf("unexpected published release or audit: release=%+v audit=%+v", published, audit.event)
	}
	rollbackBody := fmt.Sprintf(`{"source_release_id":%q,"reason":"restore approved version","idempotency_key":"handler-rollback-1"}`, first.ID)
	rollback := readPublicationResponse[publicationResponse](t, request(http.MethodPost, releaseTestExamPath+"/rollback", rollbackBody), http.StatusCreated).Release
	if rollback.SourceReleaseID != first.ID || rollback.Status != StatusDraft || audit.event.Action != "score_release.rollback_drafted" {
		t.Fatalf("rollback draft does not retain its source: %+v", rollback)
	}
	restored := publish(rollback.ID)
	if restored.Version != 3 || restored.SupersedesReleaseID != second.ID {
		t.Fatalf("rollback must create a separate version: %+v", restored)
	}
	detail = readPublicationResponse[Detail](t, current(http.StatusOK), http.StatusOK)
	if detail.Release.ID != restored.ID || len(detail.Items) != 1 || detail.Items[0].TotalScore != 4 {
		t.Fatalf("wrong restored published snapshot: %+v", detail)
	}
	list := readPublicationResponse[struct {
		Releases []Release `json:"score_releases"`
	}](t, request(http.MethodGet, releaseTestExamPath, ""), http.StatusOK)
	if len(list.Releases) != 3 || list.Releases[0].ID != rollback.ID || list.Releases[2].ID != first.ID {
		t.Fatalf("release history changed: %+v", list.Releases)
	}
}

type publicationTestPublisher func(context.Context, string, string, string, string) (Release, error)

func (p publicationTestPublisher) PublishPublication(ctx context.Context, tenantID, examID, releaseID, actorID string) (Release, error) {
	return p(ctx, tenantID, examID, releaseID, actorID)
}

func TestPublicationRoutesRejectInvalidDraftsAndRespectTenantScopedPublisher(t *testing.T) {
	store := NewMemoryStore()
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	service := NewService(store)
	manager := &auth.User{ID: actor, TenantID: tenant}
	audit := &formalAuditSink{}

	requestCount := 0
	publisherError := ErrGateBlocked
	publisher := publicationTestPublisher(func(ctx context.Context, tenantID, examID, releaseID, actorID string) (Release, error) {
		requestCount++
		if tenantID != tenant || examID != exam || releaseID == "" || actorID != actor {
			t.Errorf("publisher received incorrect scope: tenant=%q exam=%q release=%q actor=%q", tenantID, examID, releaseID, actorID)
		}
		if publisherError != nil {
			return Release{}, publisherError
		}
		return service.PublishGuarded(ctx, tenantID, releaseID, actorID, func(_ context.Context, _ *sql.Tx, draft Release, gate Gate) error {
			if draft.ExamID != exam || !gate.Passed {
				t.Errorf("guard received incorrect draft or gate: draft=%+v gate=%+v", draft, gate)
			}
			return nil
		})
	})
	handler := publicationTestMux(NewHandler(service, audit).WithPublicationPublisher(publisher))

	requirePublicationStatus(t, requestPublication(handler, nil, http.MethodGet, releaseTestCurrent, ""), http.StatusUnauthorized, "unauthenticated")
	for _, body := range []string{`{"unknown":true}`, `{} {}`, `{"reason":"` + strings.Repeat("x", 1<<20) + `"}`} {
		requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, releaseTestExamPath, body), http.StatusBadRequest, "invalid_json")
	}
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, releaseTestExamPath, `{}`), http.StatusBadRequest, "invalid_score_release_input")
	requirePublicationStatus(t, requestPublication(handler, manager, http.MethodPost, releaseTestExamPath+"/rollback", `{"reason":"missing source","idempotency_key":"handler-rollback-invalid"}`), http.StatusBadRequest, "invalid_score_release_input")
	if audit.event.Action != "" || requestCount != 0 {
		t.Fatalf("invalid drafts reached publisher or audit: calls=%d audit=%+v", requestCount, audit.event)
	}
	gate := readPublicationResponse[struct {
		Gate Gate `json:"release_gate"`
	}](t, requestPublication(handler, manager, http.MethodGet, "/api/v1/exams/"+exam+"/release-gate", ""), http.StatusOK).Gate
	if !gate.Passed {
		t.Fatalf("confirmed grades unexpectedly failed preflight: %+v", gate)
	}

	created := requestPublication(handler, manager, http.MethodPost, releaseTestExamPath, `{"reason":"approved","idempotency_key":"handler-policy-gate"}`)
	draft := readPublicationResponse[publicationResponse](t, created, http.StatusCreated).Release
	draftPath := "/api/v1/score-releases/" + draft.ID
	otherTenant := &auth.User{ID: actor, TenantID: "other-tenant"}
	requirePublicationStatus(t, requestPublication(handler, otherTenant, http.MethodGet, draftPath, ""), http.StatusNotFound, "score_release_not_found")
	requirePublicationStatus(t, requestPublication(handler, otherTenant, http.MethodPost, draftPath+"/publish", ""), http.StatusNotFound, "score_release_not_found")
	if requestCount != 0 {
		t.Fatalf("foreign release reached publisher: calls=%d", requestCount)
	}

	// 正式策略拒绝时，基础门禁可能已通过。响应不能附带会误导管理员的通过结果。
	blocked := requestPublication(handler, manager, http.MethodPost, draftPath+"/publish", "")
	requirePublicationStatus(t, blocked, http.StatusConflict, "score_release_gate_blocked")
	if strings.Contains(blocked.Body.String(), `"release_gate"`) || audit.event.Action != "score_release.created" {
		t.Fatalf("policy denial leaked inconsistent gate or success audit: body=%s audit=%+v", blocked.Body.String(), audit.event)
	}

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrStaleSource, http.StatusConflict, "score_release_stale_source"},
		{ErrInvalidTransition, http.StatusConflict, "score_release_invalid_transition"},
		{ErrAnonymousPaperUnavailable, http.StatusConflict, "anonymous_high_score_paper_unavailable"},
		{errors.New("internal test dependency failed"), http.StatusInternalServerError, "score_release_operation_failed"},
	} {
		publisherError = tc.err
		response := requestPublication(handler, manager, http.MethodPost, draftPath+"/publish", "")
		requirePublicationStatus(t, response, tc.status, tc.code)
		if strings.Contains(response.Body.String(), "internal test dependency failed") {
			t.Fatalf("internal dependency error was exposed: %s", response.Body.String())
		}
	}
	publisherError = nil
	published := requestPublication(handler, manager, http.MethodPost, draftPath+"/publish", "")
	requirePublicationStatus(t, published, http.StatusOK, "")
	if requestCount != 6 || audit.event.Action != "score_release.published" {
		t.Fatalf("unexpected publisher calls or audit: calls=%d audit=%+v", requestCount, audit.event)
	}
}

type publicationImageReader struct {
	cropID string
	pageID string
	err    error
}

func (r *publicationImageReader) ReadCropImage(_ context.Context, tenantID, segmentID string) (binaryresource.Resource, error) {
	r.cropID = tenantID + "/" + segmentID
	return publicationTestImage("crop"), r.err
}

func (r *publicationImageReader) ReadPageImage(_ context.Context, tenantID, segmentID string) (binaryresource.Resource, error) {
	r.pageID = tenantID + "/" + segmentID
	return publicationTestImage("page"), r.err
}

func publicationTestImage(data string) binaryresource.Resource {
	return binaryresource.Resource{
		ContentType: "image/png", Size: int64(len(data)), CacheControl: "private, no-store",
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(data)), nil
		},
	}
}

func TestPublishedStudentImageRoutesOnlyReadActiveScopedEvidence(t *testing.T) {
	store := NewMemoryStore()
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	store.SeedStudentQuestionImage(tenant, exam, "student-1", "question-1", "frozen-segment")
	service := NewService(store)
	draft, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{
		Reason: "approved", IdempotencyKey: "handler-student-image", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(t.Context(), tenant, draft.ID, actor); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(service, nil)
	reader := &publicationImageReader{}
	student := &auth.User{ID: "student-account", TenantID: tenant, Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-1"}}
	studentScopeMissing := &auth.User{ID: student.ID, TenantID: tenant, Permissions: student.Permissions}
	question := releaseTestStudent + "/questions/question-1"
	cropPath := question + "/answer-image"
	pagePath := question + "/page-image"
	mux := publicationTestMux(handler)

	requirePublicationStatus(t, requestPublication(mux, nil, http.MethodGet, question, ""), http.StatusUnauthorized, "unauthenticated")
	requirePublicationStatus(t, requestPublication(mux, studentScopeMissing, http.MethodGet, cropPath, ""), http.StatusForbidden, "student_score_scope_required")
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, releaseTestStudent+"/questions/missing-question", ""), http.StatusNotFound, "student_question_unavailable")
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, cropPath, ""), http.StatusNotFound, "student_answer_image_unavailable")
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, pagePath, ""), http.StatusNotFound, "student_paper_page_unavailable")

	handler.WithStudentQuestionImage(reader).WithStudentPaperPageImage(reader)
	// 只有当前版本、当前学生和当前题目的分段能传给实际图像读取器。
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, cropPath+"?expected_release_id=old", ""), http.StatusConflict, "student_release_changed")
	other := *student
	other.DataScope = map[string]any{"student_id": "another-student"}
	requirePublicationStatus(t, requestPublication(mux, &other, http.MethodGet, pagePath, ""), http.StatusNotFound, "score_release_not_found")
	if reader.cropID != "" || reader.pageID != "" {
		t.Fatalf("rejected request reached the image reader: %+v", reader)
	}

	for _, tc := range []struct{ path, body string }{{cropPath, "crop"}, {pagePath, "page"}} {
		response := requestPublication(mux, student, http.MethodGet, tc.path+"?expected_release_id="+draft.ID, "")
		requirePublicationStatus(t, response, http.StatusOK, "")
		if response.Body.String() != tc.body || response.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("incorrect image for %s: %s, headers=%v", tc.path, response.Body.String(), response.Header())
		}
	}
	if reader.cropID != tenant+"/frozen-segment" || reader.pageID != tenant+"/frozen-segment" {
		t.Fatalf("unexpected frozen image ownership: %+v", reader)
	}
	reader.err = segment.ErrEvidenceUnavailable
	for _, path := range []string{cropPath, pagePath} {
		requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, path, ""), http.StatusConflict, "answer_segment_evidence_unavailable")
	}
}

type selectedPaperTestStore struct {
	*MemoryStore
	requested []string
	source    StudentQuestionImageSource
}

func (s *selectedPaperTestStore) StudentSelectedHighScorePaperPageImage(_ context.Context, tenantID, examID, studentID, questionID, paperID, expectedReleaseID string) (StudentQuestionImageSource, error) {
	s.requested = []string{tenantID, examID, studentID, questionID, paperID, expectedReleaseID}
	return s.source, nil
}

type publicationAnonymousPaperManager struct {
	read    []string
	revoked []string
	readErr error
}

func (*publicationAnonymousPaperManager) Prepare(context.Context, string, string, string) error {
	return ErrAnonymousPaperUnavailable
}

func (m *publicationAnonymousPaperManager) ReadAnonymousPage(_ context.Context, tenantID, releaseID, pageID, assetID string) (binaryresource.Resource, error) {
	m.read = []string{tenantID, releaseID, pageID, assetID}
	return publicationTestImage("anonymous page"), m.readErr
}

func (m *publicationAnonymousPaperManager) Revoke(_ context.Context, tenantID, releaseID, actorID string) error {
	m.revoked = []string{tenantID, releaseID, actorID}
	return nil
}

func TestSelectedAnonymousPaperRouteUsesOnlyTheAnonymizedAsset(t *testing.T) {
	store := &selectedPaperTestStore{MemoryStore: NewMemoryStore()}
	manager := &publicationAnonymousPaperManager{}
	audit := &formalAuditSink{}
	service := NewService(store).WithHighScorePaper(manager)
	mux := publicationTestMux(NewHandler(service, audit).WithHighScorePaper(manager))
	student := &auth.User{ID: "student-account", TenantID: tenant, Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-1"}}
	const paperID = "55d17814-6c50-4a4a-a8fd-d9d85243c252"
	const releaseID = "current-release"
	page := releaseTestStudent + "/questions/question-1/page-image"

	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, page+"?paper_id="+paperID, ""), http.StatusBadRequest, "invalid_score_release_input")
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, page+"?variant=high_score&paper_id=invalid", ""), http.StatusBadRequest, "invalid_score_release_input")
	if len(store.requested) != 0 || len(manager.read) != 0 {
		t.Fatal("invalid selector reached the store")
	}
	selected := page + "?variant=high_score&paper_id=" + strings.ToUpper(paperID) + "&expected_release_id=" + releaseID
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, selected, ""), http.StatusNotFound, "student_paper_page_unavailable")
	if len(manager.read) != 0 {
		t.Fatal("missing anonymized asset reached the reader")
	}
	// 原始分段保留在内部模型中，也不能替换匿名资产的发布、页面和文件标识。
	store.source = StudentQuestionImageSource{
		ReleaseID: releaseID, AnswerSegmentID: "private-segment", AnonymousPageID: "public-page", AnonymousFileAssetID: "public-asset",
	}
	manager.readErr = ErrNotFound
	requirePublicationStatus(t, requestPublication(mux, student, http.MethodGet, selected, ""), http.StatusNotFound, "score_release_not_found")
	manager.readErr = nil
	response := requestPublication(mux, student, http.MethodGet, selected, "")
	requirePublicationStatus(t, response, http.StatusOK, "")
	if response.Body.String() != "anonymous page" ||
		strings.Join(store.requested, "/") != tenant+"/"+exam+"/student-1/question-1/"+paperID+"/"+releaseID ||
		strings.Join(manager.read, "/") != tenant+"/"+releaseID+"/public-page/public-asset" {
		t.Fatalf("anonymous response fetched the wrong resource: selector=%v asset=%v response=%s", store.requested, manager.read, response.Body.String())
	}

	admin := &auth.User{ID: actor, TenantID: tenant}
	revokePath := "/api/v1/score-releases/" + releaseID + "/high-score-paper/revoke"
	requirePublicationStatus(t, requestPublication(mux, admin, http.MethodPost, revokePath, ""), http.StatusNoContent, "")
	if audit.event.Action != "score_release.high_score_paper_revoked" || audit.event.TargetID != releaseID ||
		strings.Join(manager.revoked, "/") != tenant+"/"+releaseID+"/"+actor {
		t.Fatalf("revoke changed release identity or missed audit: request=%v event=%+v", manager.revoked, audit.event)
	}
}
