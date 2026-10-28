package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"github.com/google/uuid"
)

func TestSchoolCalibrationTemplateRoutesApproveApplyAndWithdraw(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	policies := stores.Grading.Calibration.(*calibration.MemoryStore)
	tenant, school, exam, source, target, actor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	user := auth.UserWithPassword{User: auth.User{ID: actor, TenantID: tenant, Username: "school-manager", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"review:manage", "review:work"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}}, SchoolID: school, SecurityEpoch: 1}
	backendRegressionSession(t, identities, user, "template-session")
	facts := calibration.SchoolTemplateTarget{SchoolID: school, Subject: "数学", ArchetypeCode: "structured_steps", MaxScore: 10}
	for _, question := range []string{source, target} {
		policies.SetSchoolTemplateTarget(tenant, exam, question, facts)
		identities.AddResourceBoundary("question", question, auth.ResourceBoundary{TenantID: tenant, SchoolID: school, ExamID: exam})
	}
	identities.AddResourceBoundary("exam", exam, auth.ResourceBoundary{TenantID: tenant, SchoolID: school, ExamID: exam})
	_, err := policies.PutPolicy(t.Context(), tenant, exam, source, calibration.PutPolicyInput{ArchetypeCode: "structured_steps", MaxScore: 10, MinimumSamples: 3, MaximumMAE: 1, MinimumExactAgreement: 0.6, MinimumWithinOneAgreement: 0.9, MaximumSevereRate: 0, SevereErrorThreshold: 3, QualificationValidityDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	request := func(method, path, body, key, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	prefix := "/api/v1/exams/" + exam + "/questions/"
	for _, route := range []struct{ method, path string }{{http.MethodGet, prefix + source + "/calibration-templates"}, {http.MethodPost, prefix + source + "/calibration-templates"}, {http.MethodPost, prefix + source + "/calibration-templates/" + uuid.NewString() + "/withdraw"}} {
		denied := request(route.method, route.path, `{}`, "anonymous", "")
		if denied.Code != 401 {
			t.Fatal("anonymous route", denied.Code, denied.Body)
		}
	}
	approved := request(http.MethodPost, prefix+source+"/calibration-templates", `{"name":"学校试评标准","source_policy_revision":1}`, "approve-template", "template-session")
	var receipt struct {
		Template calibration.SchoolTemplate `json:"template"`
	}
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body)
	}
	if err = json.Unmarshal(approved.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	listed := request(http.MethodGet, prefix+target+"/calibration-templates", "", "", "template-session")
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), receipt.Template.ID) || !strings.Contains(listed.Body.String(), `"can_approve":true`) {
		t.Fatal(listed.Code, listed.Body)
	}
	applyBody := `{"school_template_id":"` + receipt.Template.ID + `","school_template_revision":1,"expected_revision":0,"minimum_samples":999}`
	applied := request(http.MethodPut, prefix+target+"/calibration-policy", applyBody, "apply-template", "template-session")
	if applied.Code != 200 || !strings.Contains(applied.Body.String(), `"minimum_samples":3`) || !strings.Contains(applied.Body.String(), `"school_template_revision":1`) {
		t.Fatal(applied.Code, applied.Body)
	}
	withdrawn := request(http.MethodPost, prefix+source+"/calibration-templates/"+receipt.Template.ID+"/withdraw", `{"expected_revision":1,"reason":"更新学校通过标准"}`, "withdraw-template", "template-session")
	if withdrawn.Code != 200 || !strings.Contains(withdrawn.Body.String(), `"status":"withdrawn"`) {
		t.Fatal(withdrawn.Code, withdrawn.Body)
	}
	applyBody = strings.Replace(applyBody, `"expected_revision":0`, `"expected_revision":1`, 1)
	blocked := request(http.MethodPut, prefix+target+"/calibration-policy", applyBody, "apply-after-withdrawal", "template-session")
	if blocked.Code != 409 || !strings.Contains(blocked.Body.String(), "calibration_template_unavailable") {
		t.Fatal("withdrawn applied", blocked.Code, blocked.Body)
	}
}
