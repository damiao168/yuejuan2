package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func imageProbeHandler(url string, model *ImageModelConfig) *Handler {
	h := NewHandler(nil, NewHTTPAdapter(HTTPAdapterConfig{BaseURL: url, Token: strings.Repeat("t", 32)}), nil)
	h.WithImageModelResolver(func(_ context.Context, tenant string) (*ImageModelConfig, error) {
		if tenant != "tenant-1" {
			return nil, ErrWorkerResultVersionConflict
		}
		return model, nil
	})
	h.WithImageModelByIDResolver(func(_ context.Context, tenant, id, revision string) (*ImageModelConfig, error) {
		if tenant != "tenant-1" || id != model.ConfigID || revision != "school-selection-v1" {
			return nil, ErrWorkerResultVersionConflict
		}
		return model, nil
	})
	return h
}

func imageProbeResponse(model *ImageModelConfig, requestID string, supported bool) map[string]any {
	return map[string]any{"schema_version": ImageProtocolVersion, "request_id": requestID, "capability": map[string]any{"model_binding": imageModelBinding(model), "visual_supported": supported}}
}

func TestImageModelProbeSendsOnlySelectedInternalModelAndValidatesResponseIdentity(t *testing.T) {
	for _, boundary := range []string{"valid", "unsupported", "schema", "request", "binding", "malformed", "oversize", "remote error"} {
		t.Run(boundary, func(t *testing.T) {
			model := coordinatorModel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					RequestID  string           `json:"request_id"`
					StatusOnly bool             `json:"status_only"`
					Model      ImageModelConfig `json:"managed_model"`
				}
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if decoder.Decode(&request) != nil || request.StatusOnly || request.RequestID != "probe-request" || request.Model.APIKey != model.APIKey || request.Model.BaseURL != model.BaseURL || request.Model.ModelName != model.ModelName || request.Model.ConfigID != "" {
					t.Error("probe included student input or lost the internally selected model")
				}
				if r.Method != http.MethodPost || r.URL.Path != "/grading/probe-image-v3" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) || r.Header.Get("Idempotency-Key") != request.RequestID || r.Header.Get("Content-Type") != "application/json" {
					t.Error("probe lost authentication or request identity")
				}
				response := imageProbeResponse(model, request.RequestID, true)
				switch boundary {
				case "unsupported":
					response["capability"].(map[string]any)["visual_supported"] = false
				case "schema":
					response["schema_version"] = "another-protocol"
				case "request":
					response["request_id"] = "another-request"
				case "binding":
					response["capability"].(map[string]any)["model_binding"] = "another-model-binding"
				case "malformed":
					_, _ = w.Write([]byte("{"))
					return
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat("x", int(maxGradingAgentResponseBytes)+1)))
					return
				case "remote error":
					w.WriteHeader(http.StatusBadGateway)
					response = map[string]any{"schema_version": ImageProtocolVersion, "request_id": request.RequestID, "error": map[string]any{"code": "visual_model_unavailable", "retryable": true}}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			raw, err := imageProbeHandler(server.URL, model).ProbeImageModel(t.Context(), "tenant-1", "probe-request")
			if calls.Load() != 1 {
				t.Fatalf("probe did not issue exactly one request: %d", calls.Load())
			}
			if boundary == "valid" {
				if err != nil || !strings.Contains(string(raw), imageModelBinding(model)) || strings.Contains(string(raw), model.APIKey) || strings.Contains(string(raw), model.BaseURL) {
					t.Fatalf("valid probe lost binding or exposed internal connection: %s err=%v", raw, err)
				}
			} else if boundary == "remote error" {
				var failure *GradingAgentError
				if !errors.As(err, &failure) || failure.Code != "visual_model_unavailable" || failure.Retryable || raw != nil {
					t.Fatalf("provider configuration failure became retryable or successful: %v", err)
				}
			} else if !errors.Is(err, ErrInvalidModelOutput) || raw != nil {
				t.Fatalf("invalid probe response was accepted: %s err=%v", raw, err)
			}
		})
	}
}

func TestExamImageModelProbePreservesStatusOnlyAndPublicStatus(t *testing.T) {
	for _, scenario := range []struct {
		name, status, reason            string
		statusOnly, supported, cacheHit bool
		wantStatus, wantReason          string
	}{
		{"active supported", "", "", false, true, false, "passed", ""},
		{"cached failed", "failed", "visual_input_rejected", true, false, true, "failed", "visual_input_rejected"},
		{"not checked", "", "", true, false, false, "not_checked", "visual_model_probe_required"},
		{"active unsupported", "", "", false, false, false, "failed", "visual_model_probe_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			model := coordinatorModel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					RequestID  string `json:"request_id"`
					StatusOnly bool   `json:"status_only"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.StatusOnly != scenario.statusOnly {
					t.Error("cached status lookup changed to an active model probe")
				}
				response := imageProbeResponse(model, request.RequestID, scenario.supported)
				response["vision_probe_status"], response["vision_probe_reason"], response["cache_hit"] = scenario.status, scenario.reason, scenario.cacheHit
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			identity := *coordinatorPlan(model).SingleModel
			identity.BindingRevision = "school-selection-v1"
			result, err := imageProbeHandler(server.URL, model).ProbeExamImageModel(t.Context(), "tenant-1", identity, "exam-probe", scenario.statusOnly)
			if err != nil || result.ModelConfigID != model.ConfigID || result.VisionProbeStatus != scenario.wantStatus || result.VisionProbeReason != scenario.wantReason || result.CacheHit != (scenario.cacheHit && scenario.wantReason != "visual_model_probe_failed") {
				t.Fatalf("probe status mapping changed: %+v err=%v", result, err)
			}
			public := string(imageJSON(t, result))
			if strings.Contains(public, model.APIKey) || strings.Contains(public, model.BaseURL) {
				t.Fatal("public probe status included connection credentials")
			}
		})
	}
}

func TestExamImageModelProbeRejectsChangedSelectionBeforeHTTP(t *testing.T) {
	for _, boundary := range []string{"no resolver", "resolver failure", "missing model", "changed revision", "non HTTP adapter"} {
		t.Run(boundary, func(t *testing.T) {
			model := coordinatorModel()
			h := imageProbeHandler("http://no-probe.invalid", model)
			identity := *coordinatorPlan(model).SingleModel
			identity.BindingRevision = "school-selection-v1"
			wantReason := "visual_model_changed"
			switch boundary {
			case "no resolver":
				h.imageModelByIDResolver = nil
				wantReason = "visual_service_unavailable"
			case "resolver failure":
				identity.BindingRevision = "stale-school-selection"
			case "missing model":
				h.WithImageModelByIDResolver(func(context.Context, string, string, string) (*ImageModelConfig, error) { return nil, nil })
			case "changed revision":
				identity.ConfigRevision = "previous-revision"
			case "non HTTP adapter":
				h.adapter = NewMockLLMAdapter()
				wantReason = "visual_service_unavailable"
			}
			result, err := h.ProbeExamImageModel(t.Context(), "tenant-1", identity, "no-dispatch", false)
			if err != nil || result.VisionProbeStatus != "unavailable" || result.VisionProbeReason != wantReason || result.CacheHit {
				t.Fatalf("changed selection was probed: %+v err=%v", result, err)
			}
		})
	}
}

func TestImageModelProbeUnavailableAndCancelledRequestsReturnNoCapability(t *testing.T) {
	for _, boundary := range []string{"no resolver", "resolver failure", "missing credentials", "invalid URL", "cancelled transport"} {
		t.Run(boundary, func(t *testing.T) {
			model := coordinatorModel()
			h := imageProbeHandler("http://no-probe.invalid", model)
			ctx := t.Context()
			switch boundary {
			case "no resolver":
				h.imageModelResolver = nil
			case "resolver failure":
				h.WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) {
					return nil, errors.New("selection unavailable")
				})
			case "missing credentials":
				model.APIKey = ""
			case "invalid URL":
				h.adapter.(*HTTPAdapter).baseURL = "://invalid"
			case "cancelled transport":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			raw, err := h.ProbeImageModel(ctx, "tenant-1", "unavailable-probe")
			if raw != nil || err == nil {
				t.Fatalf("unavailable probe returned a capability: %s err=%v", raw, err)
			}
			if boundary == "cancelled transport" {
				var failure *GradingAgentError
				if !errors.As(err, &failure) || failure.Code != "visual_model_probe_unavailable" || !errors.Is(err, context.Canceled) {
					t.Fatalf("transport cancellation lost its cause: %v", err)
				}
			}
		})
	}
}
