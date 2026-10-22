package platformschools

import "testing"

func TestDefaultModelHealthRequiresTextGeneration(t *testing.T) {
	for _, test := range []struct {
		name  string
		model ModelHealth
		want  string
	}{
		{name: "no default model", model: ModelHealth{}, want: ModelUnconfigured},
		{name: "access check alone is insufficient", model: ModelHealth{ConfigID: "model", ConfigStatus: "active", ConnectionStatus: "success"}, want: ModelWarning},
		{name: "legacy structured result is not independent text evidence", model: ModelHealth{ConfigID: "model", ConfigStatus: "active", ConnectionStatus: "success", CapabilityStatus: "success", CapabilityVersion: "structured-json-v3"}, want: ModelWarning},
		{name: "structured failure does not block conversation", model: ModelHealth{ConfigID: "model", ConfigStatus: "active", ConnectionStatus: "success", TextStatus: "success", CapabilityStatus: "failed"}, want: ModelHealthy},
		{name: "transient connection check preserves text evidence", model: ModelHealth{ConfigID: "model", ConfigStatus: "active", ConnectionStatus: "temporary_unavailable", TextStatus: "success"}, want: ModelHealthy},
		{name: "failed text cannot use structured evidence", model: ModelHealth{ConfigID: "model", ConfigStatus: "active", TextStatus: "failed", CapabilityStatus: "success", CapabilityVersion: "structured-json-v4"}, want: ModelWarning},
		{name: "disabled configuration cannot chat", model: ModelHealth{ConfigID: "model", ConfigStatus: "disabled", TextStatus: "success"}, want: ModelWarning},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := modelHealthStatus(test.model); got != test.want {
				t.Fatalf("modelHealthStatus() = %q, want %q", got, test.want)
			}
		})
	}
}
