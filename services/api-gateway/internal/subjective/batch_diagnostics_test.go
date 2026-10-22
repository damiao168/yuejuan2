package subjective

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBatchDiagnosticsKeepUnknownCountsAndAggregateUsage(t *testing.T) {
	known := truncatedDiagnostics()
	partial := truncatedDiagnostics()
	partial.UsageStatus, partial.Usage.TotalTokens = "partial", nil
	invalid := truncatedDiagnostics()
	invalid.UsageStatus = "invalid"
	transportUnknown := &ExecutionDiagnostics{FinishOutcome: "unavailable", Attempts: 1, ProviderRequestCount: 1, UsageStatus: "unavailable"}
	for _, tc := range []struct {
		name, status string
		diagnostics  []*ExecutionDiagnostics
		known        bool
	}{
		{"reported", "reported", []*ExecutionDiagnostics{known, known}, true},
		{"partial", "partial", []*ExecutionDiagnostics{known, partial}, true},
		{"invalid dominates", "invalid", []*ExecutionDiagnostics{known, invalid}, true},
		{"legacy unknown", "unavailable", []*ExecutionDiagnostics{nil}, false},
		{"legacy mixed with known call count", "unavailable", []*ExecutionDiagnostics{nil, transportUnknown}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := []GradingRun{{Status: RunSucceeded, ExecutionDiagnostics: known}, {Status: RunProcessing, ExecutionDiagnostics: known}}
			for _, d := range tc.diagnostics {
				runs = append(runs, GradingRun{Status: RunFailed, ExecutionDiagnostics: d})
			}
			got := summarizeBatchFailures(runs)
			if len(got) != 1 || got[0].Count != len(tc.diagnostics) || got[0].UsageStatus != tc.status || (got[0].Attempts != nil) != tc.known || (got[0].ProviderRequestCount != nil) != tc.known {
				t.Fatalf("summary lost unknown/usage state: %#v", got)
			}
			if tc.known && (*got[0].Attempts != len(tc.diagnostics) || *got[0].ProviderRequestCount != len(tc.diagnostics)) {
				t.Fatalf("actual upstream counts not aggregated: %#v", got[0])
			}
		})
	}
}

func TestBatchDiagnosticsOnlyWhitelistedKindAndTenantAuthorizedBatches(t *testing.T) {
	store := NewMemoryStore()
	safe := truncatedDiagnostics()
	unsafe := truncatedDiagnostics()
	unsafe.FailureKind = "https://private-provider.invalid/private-body"
	store.runs["safe"] = GradingRun{ID: "safe", TenantID: "tenant", BatchID: "authorized", Status: RunFailed, ExecutionDiagnostics: safe}
	store.runs["unsafe"] = GradingRun{ID: "unsafe", TenantID: "tenant", BatchID: "authorized", Status: RunConflict, ExecutionDiagnostics: unsafe}
	store.runs["other tenant"] = GradingRun{TenantID: "other", BatchID: "authorized", Status: RunFailed, ExecutionDiagnostics: safe}
	store.runs["other batch"] = GradingRun{TenantID: "tenant", BatchID: "not-requested", Status: RunFailed, ExecutionDiagnostics: safe}
	batches := []GradingBatch{{ID: "authorized", TenantID: "tenant"}, {ID: "other", TenantID: "other"}}
	if err := attachBatchFailureSummaries(context.Background(), store, "tenant", batches); err != nil {
		t.Fatal(err)
	}
	if len(batches[0].FailureSummary) != 2 || len(batches[1].FailureSummary) != 0 || batches[0].FailureSummary[0].FailureKind != "output_incomplete" || batches[0].FailureSummary[1].FailureKind != "output_truncated" {
		t.Fatalf("summary scope/order/whitelist: %#v", batches)
	}
	raw, _ := json.Marshal(batches[0].FailureSummary)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "safe") {
		t.Fatalf("summary exposed non-whitelisted detail: %s", raw)
	}
}
