package subjective

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/observability"
)

func TestHTTPAdaptersObserveActualV1AndV2Calls(t *testing.T) {
	registry := observability.NewRegistry()
	v1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAgentSuccess(t, w, "sg-test-request-0001")
	}))
	defer v1Server.Close()
	v1 := testHTTPAdapter(v1Server.URL, 0)
	v1.requestObserver = registry
	if _, err := v1.Grade(context.Background(), validHTTPAdapterInput()); err != nil {
		t.Fatal(err)
	}

	v2Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer v2Server.Close()
	v2 := testHTTPAdapterV2(v2Server.URL)
	v2.base.requestObserver = registry
	v2Input := validMathV2AdapterInput()
	v2Input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
	if _, err := v2.Grade(context.Background(), v2Input); err == nil {
		t.Fatal("invalid v2 response unexpectedly accepted")
	}

	// A local validation failure never calls the agent and must not inflate
	// the count used to decide whether a protocol still has live traffic.
	badInput := validHTTPAdapterInput()
	badInput.GradeLevel = ""
	if _, err := v1.Grade(context.Background(), badInput); err == nil {
		t.Fatal("incomplete v1 input unexpectedly accepted")
	}

	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`edugrade_ai_grading_requests_total{ai_contract_version="v1",outcome="success"} 1`,
		`edugrade_ai_grading_requests_total{ai_contract_version="v2",outcome="error"} 1`,
		`edugrade_ai_grading_requests_total{ai_contract_version="v1",outcome="error"} 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing actual adapter usage %q: %s", expected, body)
		}
	}
}
