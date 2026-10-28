package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/opiniondraft"
)

// 同一个写请求在改派后必须重新核验资源，不能从通用收据重放已失去权限的私有正文。
func TestPrivateOpinionDraftRoutesDoNotCacheSensitiveReceipts(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	user := auth.UserWithPassword{User: auth.User{ID: "teacher", TenantID: "tenant", Username: "teacher", Status: "active",
		Roles: []string{"teacher"}, Permissions: []string{"appeal:work"},
		DataScope: map[string]any{"teacher": map[string]any{"scope": "class", "class_id": "class"}}}, SchoolID: "school", SecurityEpoch: 1}
	backendRegressionSession(t, identities, user, "draft-session")
	appeals := stores.Release.Appeal.(*appeal.MemoryStore)
	appeals.AddSubmissionGrade(appeal.SubmissionGradeSeed{ID: "grade", SchoolID: "school", ExamID: "exam", SubmissionID: "submission", StudentID: "student", Status: "published", Locked: true})
	item, err := appeals.CreateAppeal(t.Context(), "tenant", "student", appeal.CreateAppealInput{ExamID: "exam", StudentID: "student", TargetType: "exam", Reason: "合成申请"})
	if err != nil {
		t.Fatal(err)
	}
	item, err = appeals.AssignAppeal(t.Context(), "tenant", item.ID, "admin", appeal.AssignAppealInput{AssignedTo: "teacher", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	identities.AddResourceBoundary("appeal", item.ID, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school", ClassIDs: []string{"class"}, ExamID: "exam"})
	receipts := &assistantRouteIdempotencyStore{Store: stores.Idempotency}
	stores.Idempotency = receipts
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	path := "/api/v1/appeals/" + item.ID + "/private-draft"
	request := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer draft-session")
		req.Header.Set("Idempotency-Key", "same-private-command")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	read := request(http.MethodGet, "")
	var record opiniondraft.Record
	if read.Code != http.StatusOK {
		t.Fatalf("read=%d %s", read.Code, read.Body)
	}
	if err := json.Unmarshal(read.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"source":%q,"source_revision":%d,"expected_revision":0,"values":{"reason":"仅本人可见的合成意见"}}`, record.Source, record.SourceRevision)
	first := request(http.MethodPut, body)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "仅本人可见") || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("save=%d %s", first.Code, first.Body)
	}
	// 相同键不替代草稿 CAS：旧修订重试应冲突，且不返回旧正文。
	stale := request(http.MethodPut, body)
	if stale.Code != http.StatusConflict || strings.Contains(stale.Body.String(), "仅本人可见") {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body)
	}
	_, err = appeals.AssignAppeal(context.Background(), "tenant", item.ID, "admin", appeal.AssignAppealInput{AssignedTo: "other-teacher", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		denied := request(method, body)
		if denied.Code != http.StatusForbidden || strings.Contains(denied.Body.String(), "仅本人可见") || denied.Header().Get("Idempotency-Replayed") == "true" {
			t.Fatalf("reassigned %s=%d %s", method, denied.Code, denied.Body)
		}
	}
	if receipts.begins.Load() != 0 {
		t.Fatalf("private draft entered generic receipt storage %d times", receipts.begins.Load())
	}
}

func TestPrivateOpinionDraftRoutesRequireAuthentication(t *testing.T) {
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, NewMemoryApplicationStores())
	for _, kind := range []string{"appeals", "question-appeals", "arbitration-tasks"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/"+kind+"/record/private-draft", strings.NewReader(`{}`)))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s=%d %s", method, kind, rec.Code, rec.Body)
			}
		}
	}
}
