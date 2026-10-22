package grading

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type gradingAIPlanHandlerFixture struct {
	*MemoryStore
	detail GradingAIPlanDetail
}

func TestGradingAIStartErrorsKeepActionableAPIErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{ErrGradingAIPlanConfirmationRequired, "grading_ai_plan_confirmation_required"},
		{ErrGradingAIThresholdRequired, "grading_ai_threshold_required"},
		{ErrGradingAIModelRequired, "grading_model_required"},
		{&ScoringReadinessError{Readiness: ScoringReadiness{Checks: []ScoringReadinessCheck{{Code: "grading_ai_plan_confirmation_required", Passed: false, Message: "请学校管理员确认"}}}}, "grading_ai_plan_confirmation_required"},
	} {
		response := httptest.NewRecorder()
		writeStoreError(response, httptest.NewRequest(http.MethodPost, "/scoring-runs", nil), tc.err)
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Code != tc.code || !strings.Contains(body.Error.Message, "学校管理员") {
			t.Fatalf("lost actionable error %s: %d %s", tc.code, response.Code, response.Body.String())
		}
	}
}

func (f *gradingAIPlanHandlerFixture) GetGradingAIPlan(context.Context, string, string) (GradingAIPlanDetail, error) {
	return f.detail, nil
}
func (f *gradingAIPlanHandlerFixture) SaveGradingAIPlan(context.Context, string, string, string, GradingAIPlanInput) (GradingAIPlanDetail, error) {
	return f.detail, nil
}

func TestGradingAIProbeAllowsUnsavedSchoolModelButRejectsUnavailableAndNonAdmin(t *testing.T) {
	store := &gradingAIPlanHandlerFixture{MemoryStore: NewMemoryStore(), detail: GradingAIPlanDetail{Plan: defaultGradingAIPlan(), AvailableModels: []GradingAIModel{{ModelConfigID: "school-unsaved", ModelName: "available-model"}}}}
	handler := NewHandler(store, nil, nil)
	calls := 0
	handler.WithGradingAIProbe(func(_ context.Context, tenant string, model GradingAIModel, _ string, statusOnly bool) (GradingVisionProbe, error) {
		calls++
		if tenant != "school-tenant" || model.ModelConfigID != "school-unsaved" || statusOnly {
			t.Fatal("probe escaped selected school or unexpectedly became status read")
		}
		return GradingVisionProbe{ModelConfigID: model.ModelConfigID, VisionProbeStatus: "passed"}, nil
	})
	for _, tc := range []struct {
		role, model string
		want        int
	}{{"school_admin", "school-unsaved", 200}, {"school_admin", "foreign-or-disabled", 400}, {"teacher", "school-unsaved", 403}} {
		request := httptest.NewRequest(http.MethodPost, "/grading-ai-plan/probe", strings.NewReader(`{"model_config_id":"`+tc.model+`","request_id":"explicit-probe-request"}`))
		request.SetPathValue("examId", "exam")
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: "school-tenant", Roles: []string{tc.role}}))
		response := httptest.NewRecorder()
		handler.ProbeGradingAIPlan(response, request)
		if response.Code != tc.want {
			t.Fatalf("role=%s model=%s status=%d want=%d body=%s", tc.role, tc.model, response.Code, tc.want, response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("unauthorized probe called supplier adapter, calls=%d", calls)
	}
}
