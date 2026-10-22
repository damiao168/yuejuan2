package subjective

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func truncatedDiagnostics() *ExecutionDiagnostics {
	in, out, total, limit := int64(11), int64(7), int64(18), int64(7)
	return &ExecutionDiagnostics{FailureKind: "output_truncated", FinishOutcome: "output_limit", Attempts: 1, ProviderRequestCount: 1,
		UsageStatus: "reported", Usage: &DiagnosticTokenUsage{InputTokens: &in, OutputTokens: &out, TotalTokens: &total}, RecoveryBlockedReason: "not_enabled", OutputTokenLimit: &limit}
}

func TestHTTPAdaptersDoNotRetryIncompleteGenerationAndKeepKnownUsage(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		for _, kind := range []struct{ outcome, kind string }{{"output_limit", "output_truncated"}, {"finish_missing", "finish_reason_missing"}, {"finish_unknown", "finish_reason_unknown"}, {"filtered_or_refused", "provider_refused"}, {"filtered_or_refused", "content_filtered"}, {"unsupported_generation", "unsupported_generation"}, {"unsupported_generation", "capability_unavailable"}, {"envelope_invalid", "response_envelope_invalid"}} {
			t.Run(version+"/"+kind.kind, func(t *testing.T) {
				var calls atomic.Int32
				d := truncatedDiagnostics()
				d.FinishOutcome, d.FailureKind = kind.outcome, kind.kind
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusBadGateway)
					_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "grading-agent-" + version, "request_id": "sg-test-request-0001", "error": map[string]any{"code": "model_output_invalid", "message": "private unfinished JSON must never escape", "retryable": true}, "execution_diagnostics": d})
				}))
				defer server.Close()
				var output AdapterOutput
				var err error
				if version == "v1" {
					output, err = testHTTPAdapter(server.URL, 1).Grade(context.Background(), validHTTPAdapterInput())
				} else {
					input := validMathV2AdapterInput()
					input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
					a := testHTTPAdapterV2(server.URL)
					a.base.maxRetries = 1
					output, err = a.Grade(context.Background(), input)
				}
				if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "private") {
					t.Fatalf("calls=%d err=%v", calls.Load(), err)
				}
				got := output.Telemetry.ExecutionDiagnostics
				if got == nil || got.FailureKind != kind.kind || got.Usage == nil || got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 7 {
					t.Fatalf("lost failure usage: %#v", got)
				}
				if len(output.MatchedPoints) != 0 || len(output.MathCandidates) != 0 {
					t.Fatal("failure created score candidates")
				}
			})
		}
	}
}

func TestAgentErrorEnvelopeMustMatchRequestAndBoundedDiagnostics(t *testing.T) {
	base := map[string]any{"schema_version": "grading-agent-v1", "request_id": "sg-test-request-0001", "error": map[string]any{"code": "model_output_invalid", "message": "private", "retryable": false}, "execution_diagnostics": truncatedDiagnostics()}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong request", func(v map[string]any) { v["request_id"] = "other" }},
		{"wrong version", func(v map[string]any) { v["schema_version"] = "grading-agent-v2" }},
		{"unknown top field", func(v map[string]any) { v["provider_body"] = "private" }},
		{"unknown diagnostics", func(v map[string]any) { v["execution_diagnostics"].(map[string]any)["provider_body"] = "private" }},
		{"missing required zero", func(v map[string]any) { delete(v["execution_diagnostics"].(map[string]any), "recovery_attempted") }},
		{"invalid count", func(v map[string]any) { v["execution_diagnostics"].(map[string]any)["provider_request_count"] = 3 }},
		{"negative known usage", func(v map[string]any) {
			v["execution_diagnostics"].(map[string]any)["usage"].(map[string]any)["output_tokens"] = -1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			tc.mutate(value)
			raw, _ = json.Marshal(value)
			err := parseAgentError(raw, 502, gradingAgentSchemaVersion, "sg-test-request-0001")
			if err == nil || !strings.Contains(err.Error(), "agent_error_response_invalid") || agentErrorRetryable(err) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestHTTPAdaptersRejectIncompleteDiagnosticsEvenWithValidSuccessJSON(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec := httptest.NewRecorder()
				if version == "v1" {
					writeAgentSuccess(t, rec, "sg-test-request-0001")
				} else {
					writeMathAgentV2Success(t, rec, nil)
				}
				var payload map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &payload)
				payload["telemetry"].(map[string]any)["execution_diagnostics"] = truncatedDiagnostics()
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer server.Close()
			var err error
			if version == "v1" {
				_, err = testHTTPAdapter(server.URL, 0).Grade(context.Background(), validHTTPAdapterInput())
			} else {
				input := validMathV2AdapterInput()
				input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
				_, err = testHTTPAdapterV2(server.URL).Grade(context.Background(), input)
			}
			if err == nil {
				t.Fatal("valid JSON bypassed finish protection")
			}
		})
	}
}

func TestWorkerFailureDiagnosticsAreAtomicWithCurrentLease(t *testing.T) {
	f := newWorkerServiceFixture(t, CreateRunInput{}, "")
	service := NewWorkerCompletionService(f.store, f.runtime)
	input := WorkerFailureInput{TaskID: f.task.ID, LeaseToken: "stale", Retryable: true, ErrorCode: "ai_output_incomplete", ErrorDetail: executionFailureDetail(truncatedDiagnostics())}
	if _, err := service.Fail(context.Background(), workerServiceTenant, f.run.ID, input); err == nil {
		t.Fatal("stale lease accepted diagnostics")
	}
	run, _ := f.store.GetRun(context.Background(), workerServiceTenant, f.run.ID)
	if run.ExecutionDiagnostics != nil {
		t.Fatal("stale diagnostic polluted current run")
	}
	input.LeaseToken = f.task.LeaseToken
	result, err := service.Fail(context.Background(), workerServiceTenant, f.run.ID, input)
	if err != nil || result.Run.ExecutionDiagnostics == nil || result.Run.ExecutionDiagnostics.Usage == nil || result.Run.Status != RunFailed || result.Task.Status != workerruntime.StatusFailed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(f.store.grades) != 0 {
		t.Fatal("failed generation created a grade")
	}
}

func TestUnknownUsageStaysUnknownAndNeverEntersLegacyZeroLedger(t *testing.T) {
	for _, status := range []string{"unavailable", "partial", "invalid"} {
		d := truncatedDiagnostics()
		d.UsageStatus = status
		if status == "unavailable" {
			d.Usage = nil
		}
		if status == "partial" {
			d.Usage.InputTokens = nil
			d.Usage.TotalTokens = nil
		}
		if validateExecutionDiagnostics(d) != nil {
			t.Fatalf("valid known facts rejected for %s", status)
		}
		grade := Grade{RawOutput: map[string]any{"execution_diagnostics": d}}
		if gradeUsageReported(grade) {
			t.Fatalf("%s became a legacy zero ledger row", status)
		}
	}
}

func TestUnavailableDiagnosticsAreBoundedAndBlockKnownDispatchRetry(t *testing.T) {
	d := truncatedDiagnostics()
	d.FinishOutcome = "unavailable"
	d.FailureKind = ""
	d.UsageStatus = "unavailable"
	d.Usage = nil
	if err := validateExecutionDiagnostics(d); err != nil {
		t.Fatalf("unavailable transport fact should be accepted: %v", err)
	}
	if diagnosticsAllowRetry(d) {
		t.Fatal("a request with a known provider dispatch must not be replayed by the outer adapter")
	}
	zero := *d
	zero.Attempts = 0
	zero.ProviderRequestCount = 0
	if !diagnosticsAllowRetry(&zero) {
		t.Fatal("an unavailable request with no dispatch may retain transport retry")
	}
}
