package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

const (
	assistantRouteTenant = "11111111-1111-4111-8111-111111111111"
	assistantRouteSchool = "22222222-2222-4222-8222-222222222222"
	assistantRouteExam   = "33333333-3333-4333-8333-333333333333"
	assistantOtherExam   = "44444444-4444-4444-8444-444444444444"
	assistantOtherSchool = "55555555-5555-4555-8555-555555555555"
)

// 读替身故意不按学校过滤，用来验证请求体考试 ID 的资源检查确实发生，不能只依赖前端选择器。
type assistantRouteExamReader struct {
	exam.Store
	items map[string]exam.Exam
}

func (s *assistantRouteExamReader) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	item, ok := s.items[id]
	if !ok || item.TenantID != scope.TenantID {
		return exam.Exam{}, exam.ErrNotFound
	}
	return item, nil
}

type assistantRouteReleaseStore struct {
	scorerelease.Store
	gateReads atomic.Int32
}

func (s *assistantRouteReleaseStore) Gate(ctx context.Context, tenantID, examID string) (scorerelease.Gate, error) {
	s.gateReads.Add(1)
	return s.Store.Gate(ctx, tenantID, examID)
}

type assistantRouteEvidenceStore struct {
	releasegate.Store
	writes atomic.Int32
}

func (s *assistantRouteEvidenceStore) AppendEvidence(ctx context.Context, evidence releasegate.Evidence) (releasegate.Evidence, error) {
	s.writes.Add(1)
	return s.Store.AppendEvidence(ctx, evidence)
}

type assistantRouteIdempotencyStore struct {
	idempotency.Store
	begins atomic.Int32
}

func (s *assistantRouteIdempotencyStore) Begin(ctx context.Context, input idempotency.BeginInput) (idempotency.Record, bool, error) {
	s.begins.Add(1)
	return s.Store.Begin(ctx, input)
}

type assistantRouteFixture struct {
	router   http.Handler
	auth     *auth.MemoryStore
	releases *assistantRouteReleaseStore
	evidence *assistantRouteEvidenceStore
	receipts *assistantRouteIdempotencyStore
}

func newAssistantRouteFixture(t *testing.T, roles, permissions []string, dataScope map[string]any) assistantRouteFixture {
	t.Helper()
	stores := NewMemoryApplicationStores()
	authStore := stores.Identity.Auth.(*auth.MemoryStore)
	passwordHash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatal(err)
	}
	authStore.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID: "66666666-6666-4666-8666-666666666666", TenantID: assistantRouteTenant,
			TenantCode: "demo", Username: "assistant_manager", DisplayName: "助手测试管理员",
			Status: "active", Roles: roles, Permissions: permissions, DataScope: dataScope,
		},
		PasswordHash: passwordHash,
	})
	items := map[string]exam.Exam{}
	for id, school := range map[string]string{assistantRouteExam: assistantRouteSchool, assistantOtherExam: assistantOtherSchool} {
		items[id] = exam.Exam{ID: id, TenantID: assistantRouteTenant, SchoolID: school,
			Name: "集成测试数学", Subject: "math", Status: "draft", Revision: 1,
			TotalScore: 100, UpdatedAt: time.Now().UTC(), ClassIDs: []string{}}
		authStore.AddResourceBoundary("exam", id, auth.ResourceBoundary{TenantID: assistantRouteTenant, SchoolID: school, ExamID: id})
	}
	stores.Exam.Exam = &assistantRouteExamReader{Store: stores.Exam.Exam, items: items}
	scoreStore := stores.Release.ScoreRelease.(*scorerelease.MemoryStore)
	scoreStore.SetGateIssues(assistantRouteExam, []scorerelease.GateIssue{{
		Code: "new_business_blocker", Message: "新增业务条件尚未满足", Blocking: true, Count: 2,
	}})
	releases := &assistantRouteReleaseStore{Store: scoreStore}
	evidence := &assistantRouteEvidenceStore{Store: stores.Release.ReleaseGate}
	receipts := &assistantRouteIdempotencyStore{Store: stores.Idempotency}
	stores.Release.ScoreRelease, stores.Release.ReleaseGate, stores.Idempotency = releases, evidence, receipts
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	return assistantRouteFixture{router: router, auth: authStore, releases: releases, evidence: evidence, receipts: receipts}
}

func assistantSchoolDataScope() map[string]any {
	return map[string]any{"scope": "school", "school_id": assistantRouteSchool}
}

func assistantRouteQuery(intent, examID string) string {
	raw, _ := json.Marshal(map[string]any{
		"request_id":    "77777777-7777-4777-8777-777777777777",
		"question":      "私人提问内容不应写入审计",
		"preset_intent": intent,
		"context_hint":  map[string]any{"exam_id": examID, "class_ids": []string{}, "release_channel": "student_portal"},
	})
	return string(raw)
}

func TestSchoolAssistantRoutesReadBusinessFactsWithoutPublicationWritesOrChatReceipts(t *testing.T) {
	f := newAssistantRouteFixture(t, []string{"school_admin"}, []string{"score:manage", "dashboard:read", "exam:manage", "submission:manage"}, assistantSchoolDataScope())
	token := reviewLogin(t, f.router, "assistant_manager")
	receiptsBefore := f.receipts.begins.Load()
	for _, intent := range []string{"exam_progress", "publish_diagnosis"} {
		r := reviewAuthedRequest(http.MethodPost, "/api/v1/school-assistant/queries/stream", assistantRouteQuery(intent, assistantRouteExam), token)
		r.Header.Set("Idempotency-Key", "must-not-persist-school-chat")
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%s stream: %d %s", intent, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"type":"result"`) || !strings.Contains(w.Body.String(), `"type":"done"`) || !strings.Contains(w.Body.String(), assistantRouteExam) {
			t.Fatalf("%s missing complete result: %s", intent, w.Body.String())
		}
		if intent == "publish_diagnosis" && !strings.Contains(w.Body.String(), "new_business_blocker") {
			t.Fatalf("new domain blocker must remain visible: %s", w.Body.String())
		}
	}
	if f.releases.gateReads.Load() != 1 || f.evidence.writes.Load() != 0 || f.receipts.begins.Load() != receiptsBefore {
		t.Fatalf("read-only assistant crossed write boundary: gate=%d evidence=%d receipt begins=%d/%d",
			f.releases.gateReads.Load(), f.evidence.writes.Load(), f.receipts.begins.Load(), receiptsBefore)
	}
	audits, err := f.auth.ListAudits(context.Background(), assistantRouteTenant, auth.AuditFilter{Limit: 50, ScopeMode: "tenant"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(audits)
	if strings.Contains(string(raw), "私人提问内容") || strings.Contains(string(raw), "new_business_blocker") {
		t.Fatalf("audit must not persist question or full business result: %s", raw)
	}
}

func TestSchoolAssistantRoutesDenyRolePermissionAndBodyResourceBeforeStreaming(t *testing.T) {
	cases := []struct {
		name        string
		roles       []string
		permissions []string
		scope       map[string]any
		examID      string
		wantStatus  int
	}{
		{"other role", []string{"teacher"}, []string{"score:manage"}, assistantSchoolDataScope(), assistantRouteExam, http.StatusForbidden},
		{"missing business permission", []string{"school_admin"}, []string{"dashboard:read"}, assistantSchoolDataScope(), assistantRouteExam, http.StatusForbidden},
		{"other school in body", []string{"school_admin"}, []string{"score:manage"}, assistantSchoolDataScope(), assistantOtherExam, http.StatusNotFound},
		{"unknown exam in body", []string{"school_admin"}, []string{"score:manage"}, assistantSchoolDataScope(), "88888888-8888-4888-8888-888888888888", http.StatusNotFound},
		{"class-only cannot read full exam", []string{"school_admin"}, []string{"score:manage"}, map[string]any{"scope": "class", "class_id": "99999999-9999-4999-8999-999999999999"}, assistantRouteExam, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAssistantRouteFixture(t, tc.roles, tc.permissions, tc.scope)
			token := reviewLogin(t, f.router, "assistant_manager")
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, reviewAuthedRequest(http.MethodPost, "/api/v1/school-assistant/queries/stream", assistantRouteQuery("publish_diagnosis", tc.examID), token))
			if w.Code != tc.wantStatus || strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") || f.releases.gateReads.Load() != 0 {
				t.Fatalf("authorization must precede stream/data reads: status=%d want=%d gate=%d body=%s", w.Code, tc.wantStatus, f.releases.gateReads.Load(), w.Body.String())
			}
		})
	}
}

func TestSchoolAssistantCapabilitiesUseCurrentBusinessPermissions(t *testing.T) {
	f := newAssistantRouteFixture(t, []string{"school_admin"}, []string{"dashboard:read"}, assistantSchoolDataScope())
	token := reviewLogin(t, f.router, "assistant_manager")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, reviewAuthedRequest(http.MethodGet, "/api/v1/school-assistant/capabilities", "", token))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "exam_progress") {
		t.Fatalf("capabilities did not expose permitted query: %d %s", w.Code, w.Body.String())
	}
	// 功能可见性不能代替请求时鉴权；缺少 score:manage 的身份即便自行构造请求也必须被拒绝。
	w = httptest.NewRecorder()
	f.router.ServeHTTP(w, reviewAuthedRequest(http.MethodPost, "/api/v1/school-assistant/queries/stream", assistantRouteQuery("publish_diagnosis", assistantRouteExam), token))
	if w.Code != http.StatusForbidden {
		t.Fatalf("capability hint bypassed business permission: %d %s", w.Code, w.Body.String())
	}
}
