package modelgovernance

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func governanceLifecycleRequest(t *testing.T, mux *http.ServeMux, user auth.User, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func governanceLifecycleJSON[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("got HTTP %d, want %d: %s", response.Code, status, response.Body)
	}
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func governanceLifecycleError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || response.Header().Get(httpx.ErrorCodeHeader) != code {
		t.Fatalf("got HTTP %d %q, want %d %q: %s", response.Code, response.Header().Get(httpx.ErrorCodeHeader), status, code, response.Body)
	}
}

func TestModelApprovalHTTPPreservesFrozenEvidenceAndRejectsStaleRevocation(t *testing.T) {
	store, run, candidate, input := routingApprovalFixture()
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, nil, testBaseline())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/model-approvals", handler.ListModelApprovals)
	mux.HandleFunc("POST /api/v1/model-approvals", handler.CreateModelApproval)
	mux.HandleFunc("POST /api/v1/model-approvals/{id}/revoke", handler.RevokeModelApproval)
	user := auth.User{ID: "reviewer", TenantID: run.TenantID}
	const path = "/api/v1/model-approvals"
	invalid := input
	invalid.Reason = ""
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, user, "POST", path, invalid), http.StatusBadRequest, "invalid_model_approval")
	invalid = input
	invalid.TenantID = "other-school"
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, user, "POST", path, invalid), http.StatusForbidden, "tenant_scope_forbidden")
	if len(store.modelApprovals) != 0 {
		t.Fatal("rejected approval changed the decision history")
	}
	type approvalResponse struct {
		Approval ModelApproval `json:"model_approval"`
	}
	created := governanceLifecycleJSON[approvalResponse](t, governanceLifecycleRequest(t, mux, user, "POST", path, input), http.StatusCreated).Approval
	if created.ID == "" || created.Revision != 1 || created.EvaluationCandidateID != candidate.ID ||
		created.ModelVersion != candidate.ModelVersion || created.PromptVersion != candidate.PromptVersion ||
		created.RubricVersion != candidate.RubricVersion || created.DatasetSHA256 != run.DatasetSHA256 || created.AuthorizationRef != run.AuthorizationRef {
		t.Fatalf("approval response lost its frozen evidence: %+v", created)
	}
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, user, "POST", path, input), http.StatusConflict, "model_governance_conflict")
	revoke := path + "/" + created.ID + "/revoke"
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, user, "POST", revoke, ModelApprovalRevokeInput{Reason: "policy withdrawn", ExpectedRevision: 2}), http.StatusConflict, "resource_version_conflict")
	if stored := store.modelApprovals[created.ID]; stored.RevokedAt != nil || stored.Revision != created.Revision {
		t.Fatalf("stale revocation changed approval: %+v", stored)
	}

	// 撤销使用审批的当前修订号。保留原版本证据和历史记录，同时停止授权后续模型使用。
	revoked := governanceLifecycleJSON[approvalResponse](t, governanceLifecycleRequest(t, mux, user, "POST", revoke, ModelApprovalRevokeInput{Reason: "policy withdrawn", ExpectedRevision: created.Revision}), http.StatusOK).Approval
	if revoked.RevokedAt == nil || revoked.Revision != 2 || revoked.ModelVersion != created.ModelVersion || revoked.DatasetSHA256 != created.DatasetSHA256 {
		t.Fatalf("revocation changed frozen evidence or did not revoke: %+v", revoked)
	}
	type listResponse struct {
		Approvals []ModelApproval `json:"model_approvals"`
	}
	listed := governanceLifecycleJSON[listResponse](t, governanceLifecycleRequest(t, mux, user, "GET", path, nil), http.StatusOK).Approvals
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].RevokedAt == nil {
		t.Fatalf("revoked decision missing from history: %+v", listed)
	}
	if _, err := RequireActiveModelApproval(listed, ModelApprovalScope{
		ModelConfigID: candidate.ModelConfigID, ModelVersion: candidate.ModelVersion, PromptVersion: candidate.PromptVersion,
		RubricVersion: candidate.RubricVersion, Subject: run.Subject, Grade: run.Grade, QuestionType: run.QuestionType, Modality: run.Modality,
	}, time.Now().UTC()); !errors.Is(err, ErrModelNotApproved) {
		t.Fatalf("revoked approval still authorizes model use: %v", err)
	}
	records, err := audits.ListAudits(t.Context(), run.TenantID, auth.AuditFilter{Limit: 10})
	if err != nil || len(records) != 2 {
		t.Fatalf("expected create/revoke audits only: count=%d err=%v", len(records), err)
	}
	actions := map[string]bool{}
	for _, record := range records {
		if record.TargetID != created.ID || record.ActorID != user.ID {
			t.Fatalf("audit lost the decision or reviewer identity: %+v", record)
		}
		actions[record.Action] = true
	}
	if !actions["model.approval_created"] || !actions["model.approval_revoked"] {
		t.Fatalf("approval audit actions missing: %+v", actions)
	}
}

func TestPlatformChatAccessHTTPRequiresExplicitListAndCurrentRevision(t *testing.T) {
	store, school, first, second := documentSelectionFixture()
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, nil, testBaseline())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/chat-model-access", handler.GetPlatformChatModelAccess)
	mux.HandleFunc("PUT /api/v1/platform/chat-model-access", handler.SavePlatformChatModelAccess)
	platform := auth.User{ID: "platform-reviewer", TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:read", "model:managed_api:manage"}}
	const path = "/api/v1/platform/chat-model-access"
	type accessResponse struct{ Access ChatModelAccess }
	initial := governanceLifecycleJSON[accessResponse](t, governanceLifecycleRequest(t, mux, platform, "GET", path+"?tenant_id="+school, nil), http.StatusOK).Access
	if initial.Revision != "absent" || len(initial.ModelIDs) != 1 || initial.ModelIDs[0] != first {
		t.Fatalf("unexpected inherited default: %+v", initial)
	}
	for _, body := range []map[string]any{
		{"tenant_id": school, "expected_revision": initial.Revision},
		{"tenant_id": school, "expected_revision": initial.Revision, "model_ids": nil},
	} {
		governanceLifecycleError(t, governanceLifecycleRequest(t, mux, platform, "PUT", path, body), http.StatusBadRequest, "invalid_managed_model_api")
	}
	studentAdmin := auth.User{ID: "school-admin", TenantID: school, Roles: []string{"school_admin"}, Permissions: []string{"model:managed_api:manage"}}
	grant := map[string]any{"tenant_id": school, "expected_revision": initial.Revision, "model_ids": []string{first, second}}
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, studentAdmin, "PUT", path, grant), http.StatusForbidden, "forbidden")
	unchanged, err := store.GetChatModelAccess(t.Context(), school)
	if err != nil || unchanged.Revision != initial.Revision || len(unchanged.ModelIDs) != 1 || unchanged.ModelIDs[0] != first {
		t.Fatalf("invalid request revoked or enlarged access: %+v %v", unchanged, err)
	}
	updated := governanceLifecycleJSON[accessResponse](t, governanceLifecycleRequest(t, mux, platform, "PUT", path, grant), http.StatusOK).Access
	if updated.Revision == initial.Revision || !contains(updated.ModelIDs, first) || !contains(updated.ModelIDs, second) {
		t.Fatalf("explicit grant not saved with a new revision: %+v", updated)
	}
	for _, id := range []string{first, second} {
		connection, err := ResolveChatManagedAPI(t.Context(), store, school, id)
		if err != nil || connection == nil || connection.Config.ID != id {
			t.Fatalf("granted model %s unavailable: %v", id, err)
		}
	}
	clear := map[string]any{"tenant_id": school, "expected_revision": initial.Revision, "model_ids": []string{}}
	governanceLifecycleError(t, governanceLifecycleRequest(t, mux, platform, "PUT", path, clear), http.StatusConflict, "chat_model_access_changed")
	if connection, err := ResolveChatManagedAPI(t.Context(), store, school, second); err != nil || connection == nil {
		t.Fatalf("stale form revoked the current grant: %v", err)
	}

	// 只有当前版本的显式空列表才撤销全部聊天授权。撤销后不能重新继承解析默认模型。
	clear["expected_revision"] = updated.Revision
	cleared := governanceLifecycleJSON[accessResponse](t, governanceLifecycleRequest(t, mux, platform, "PUT", path, clear), http.StatusOK).Access
	if len(cleared.ModelIDs) != 0 || cleared.Revision == updated.Revision {
		t.Fatalf("explicit revocation did not clear current access: %+v", cleared)
	}
	if connection, err := ResolveChatManagedAPI(t.Context(), store, school, ""); err != nil || connection != nil {
		t.Fatalf("empty access silently inherited a model: %+v %v", connection, err)
	}
	for _, id := range []string{first, second} {
		if connection, err := ResolveChatManagedAPI(t.Context(), store, school, id); !errors.Is(err, ErrManagedConfigUnavailable) || connection != nil {
			t.Fatalf("revoked model %s remained callable: %+v %v", id, connection, err)
		}
	}
	if !store.managedConfigs[first].IsDefault || store.managedConfigs[second].IsDefault {
		t.Fatal("chat grants changed the parsing default")
	}
	records, err := audits.ListAudits(t.Context(), auth.PlatformTenantID, auth.AuditFilter{Limit: 10})
	if err != nil || len(records) != 2 {
		t.Fatalf("expected successful grant/revocation audits only: count=%d err=%v", len(records), err)
	}
	for _, record := range records {
		if record.Action != "model.chat_access_updated" || record.TargetID != school || record.ActorID != platform.ID {
			t.Fatalf("access audit lost platform actor or target school: %+v", record)
		}
	}
}
