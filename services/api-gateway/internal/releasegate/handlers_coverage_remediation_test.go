package releasegate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type remediationHTTPAudit struct{ events []auth.AuditEvent }

func (a *remediationHTTPAudit) Audit(_ context.Context, event auth.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

func remediationHTTPRequest(mux *http.ServeMux, method, path, body string, user *auth.User) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *user))
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func remediationHTTPResource[T any](t *testing.T, response *httptest.ResponseRecorder, status int, key string) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var resource T
	if err := json.Unmarshal(envelope[key], &resource); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return resource
}

func remediationHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body httpx.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || body.Error.Code != code || response.Header().Get(httpx.ErrorCodeHeader) != code {
		t.Fatalf("status=%d body=%s error header=%q; want=%d/%s", response.Code, response.Body.String(), response.Header().Get(httpx.ErrorCodeHeader), status, code)
	}
}

func TestReleaseGateHTTPWaiverLifecycleKeepsTenantAndDecisionBoundaries(t *testing.T) {
	store := NewMemoryStore()
	base := fakeBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true,
		Warnings: []scorerelease.GateIssue{{Code: "quality_warning", Count: 1}}}}
	service := NewService(store, base)
	audit := &remediationHTTPAudit{}
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(service, audit), func(handler http.HandlerFunc) http.Handler { return handler })
	manager := &auth.User{ID: "manager", TenantID: "tenant-a"}
	principal := &auth.User{ID: "principal", TenantID: "tenant-a"}
	otherTenant := &auth.User{ID: "manager", TenantID: "tenant-b"}

	policy := remediationHTTPResource[Policy](t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-a/release-gate/policies",
		`{"version":"school-2026.1","require_warning_acknowledgement":true,"waivable_warning_codes":["quality_warning"]}`, manager), http.StatusCreated, "release_gate_policy")
	preview := remediationHTTPResource[Evidence](t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-a/release-gate/preview",
		`{"release_id":"release-a"}`, manager), http.StatusCreated, "release_gate_evidence")
	if preview.Evaluation.Passed || preview.Evaluation.Policy.ID != policy.ID || preview.CreatedBy != manager.ID {
		t.Fatalf("warning acknowledgement missing from preview: %#v", preview)
	}

	// 另一租户不能读取证据；另一考试不能用该证据申请豁免。拒绝路径不产生成功审计。
	remediationHTTPError(t, remediationHTTPRequest(mux, "GET", "/api/v1/release-gate-evidence/"+preview.ID, "", otherTenant), http.StatusNotFound, "release_gate_not_found")
	waiverBody := `{"evidence_id":"` + preview.ID + `","issue_code":"quality_warning","reason":"核对扫描质量后申请确认"}`
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-b/release-gate/waivers", waiverBody, manager), http.StatusUnprocessableEntity, "release_gate_waiver_forbidden")
	if len(audit.events) != 2 {
		t.Fatalf("rejected reads or writes produced successful audit: %#v", audit.events)
	}
	waiver := remediationHTTPResource[Waiver](t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-a/release-gate/waivers", waiverBody, manager), http.StatusCreated, "release_gate_waiver")
	decisionPath := "/api/v1/release-gate-waivers/" + waiver.ID + "/decision"
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", decisionPath, `{"approve":true,"reason":"确认"}`, otherTenant), http.StatusNotFound, "release_gate_not_found")
	approved := remediationHTTPResource[Waiver](t, remediationHTTPRequest(mux, "POST", decisionPath, `{"approve":true,"reason":"已核对非阻断问题"}`, principal), http.StatusOK, "release_gate_waiver")
	if approved.Status != WaiverApproved || approved.DecidedBy != principal.ID || approved.DecidedAt == nil {
		t.Fatalf("unexpected decision: %#v", approved)
	}

	// 已决定的申请不能再次改变决定；重新预览可以放行，但旧证据保持当时的阻断状态。
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", decisionPath, `{"approve":false,"reason":"再次决定"}`, principal), http.StatusUnprocessableEntity, "release_gate_waiver_forbidden")
	current := remediationHTTPResource[Evidence](t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-a/release-gate/preview", `{"release_id":"release-a"}`, manager), http.StatusCreated, "release_gate_evidence")
	if !current.Evaluation.Passed || len(current.Evaluation.AppliedWaiverIDs) != 1 || current.Evaluation.AppliedWaiverIDs[0] != waiver.ID {
		t.Fatalf("approved warning was not applied: %#v", current.Evaluation)
	}
	saved := remediationHTTPResource[Evidence](t, remediationHTTPRequest(mux, "GET", "/api/v1/release-gate-evidence/"+preview.ID, "", manager), http.StatusOK, "release_gate_evidence")
	if saved.Hash != preview.Hash || saved.Evaluation.Passed || current.ID == preview.ID {
		t.Fatalf("new preview changed original evidence: original=%#v current=%#v", saved, current)
	}
	expected := []struct{ action, target, actor string }{
		{"release_gate.policy_created", policy.ID, manager.ID},
		{"release_gate.previewed", preview.ID, manager.ID},
		{"release_gate.waiver_requested", waiver.ID, manager.ID},
		{"release_gate.waiver_decided", waiver.ID, principal.ID},
		{"release_gate.previewed", current.ID, manager.ID},
	}
	if len(audit.events) != len(expected) {
		t.Fatalf("audit count=%d want=%d", len(audit.events), len(expected))
	}
	for index, want := range expected {
		event := audit.events[index]
		if event.Action != want.action || event.TargetID != want.target || event.ActorID != want.actor || event.TenantID != manager.TenantID || event.TargetType != "release_gate" || event.AfterValue["exam_id"] != "exam-a" {
			t.Fatalf("audit %d has wrong resource or identity: %#v", index, event)
		}
	}
}

func TestReleaseGateHTTPRejectsUnauthenticatedAndMalformedRequests(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store, fakeBaseGate{gate: scorerelease.Gate{Passed: true}})
	audit := &remediationHTTPAudit{}
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(service, audit), func(handler http.HandlerFunc) http.Handler { return handler })
	manager := &auth.User{ID: "manager", TenantID: "tenant-a"}
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/exams/exam-a/release-gate/policies"},
		{"POST", "/api/v1/exams/exam-a/release-gate/preview"},
		{"GET", "/api/v1/release-gate-evidence/missing"},
		{"POST", "/api/v1/exams/exam-a/release-gate/waivers"},
		{"POST", "/api/v1/release-gate-waivers/missing/decision"},
	}
	for _, route := range routes {
		t.Run("unauthenticated "+route.path, func(t *testing.T) {
			remediationHTTPError(t, remediationHTTPRequest(mux, route.method, route.path, "{}", nil), http.StatusUnauthorized, "unauthenticated")
		})
	}
	for _, route := range routes {
		if route.method != "POST" {
			continue
		}
		t.Run("unknown fields "+route.path, func(t *testing.T) {
			remediationHTTPError(t, remediationHTTPRequest(mux, route.method, route.path, `{"unknown":true}`, manager), http.StatusBadRequest, "invalid_json")
		})
	}
	for _, body := range []string{"", "{", "{} {}", "{} trailing", `{"release_id":"` + strings.Repeat("x", 1<<20) + `"}`} {
		remediationHTTPError(t, remediationHTTPRequest(mux, "POST", "/api/v1/exams/exam-a/release-gate/preview", body, manager), http.StatusBadRequest, "invalid_json")
	}
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", routes[0].path, `{"version":"x"}`, manager), http.StatusBadRequest, "invalid_release_gate_input")
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", routes[3].path, `{"evidence_id":"missing","issue_code":"warning","reason":"核对"}`, manager), http.StatusNotFound, "release_gate_not_found")
	remediationHTTPError(t, remediationHTTPRequest(mux, "POST", routes[4].path, `{"approve":true,"reason":"核对"}`, manager), http.StatusNotFound, "release_gate_not_found")
	remediationHTTPError(t, remediationHTTPRequest(mux, "GET", routes[2].path, "", manager), http.StatusNotFound, "release_gate_not_found")
	if len(audit.events) != 0 || len(store.policies) != 0 || len(store.evidence) != 0 || len(store.waivers) != 0 {
		t.Fatal("rejected requests changed release gate state or emitted successful audit")
	}
}

func TestReleaseGateHTTPRegistersManagementGuardOnEveryRoute(t *testing.T) {
	mux := http.NewServeMux()
	wrapped, called := 0, 0
	RegisterRoutes(mux, NewHandler(nil, nil), func(http.HandlerFunc) http.Handler {
		wrapped++
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called++
			httpx.Error(w, r, http.StatusForbidden, "forbidden", "management permission required")
		})
	})
	// 只验证本模块把所有路由交给宿主守卫；真实权限和作用域解析由 auth/server 测试验证。
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/exams/exam-a/release-gate/policies"},
		{"POST", "/api/v1/exams/exam-a/release-gate/preview"},
		{"GET", "/api/v1/release-gate-evidence/evidence-a"},
		{"POST", "/api/v1/exams/exam-a/release-gate/waivers"},
		{"POST", "/api/v1/release-gate-waivers/waiver-a/decision"},
	} {
		remediationHTTPError(t, remediationHTTPRequest(mux, route.method, route.path, "{}", nil), http.StatusForbidden, "forbidden")
	}
	if wrapped != 5 || called != 5 {
		t.Fatalf("management guard registration=%d calls=%d want 5 each", wrapped, called)
	}
}
