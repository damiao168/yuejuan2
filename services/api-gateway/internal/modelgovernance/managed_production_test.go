package modelgovernance

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateManagedProductionCandidate(t *testing.T) {
	valid := managedProductionCandidate{
		ID: "model-1", Endpoint: "https://api.example.com", ModelVersion: "v1",
		Region: "global", Status: "active", Present: true,
		ConnectionStatus: "success", CapabilityStatus: "success",
		CapabilityVersion: ManagedCapabilityProbeVersion,
	}
	tests := []struct {
		name       string
		change     func(*managedProductionCandidate)
		apiKey     string
		decryptErr error
		wantUnsafe bool
	}{
		{name: "ready", apiKey: "secret"},
		{name: "http endpoint", change: func(c *managedProductionCandidate) { c.Endpoint = "http://api.example.com" }, apiKey: "secret", wantUnsafe: true},
		{name: "invalid URL", change: func(c *managedProductionCandidate) { c.Endpoint = "://bad" }, apiKey: "secret", wantUnsafe: true},
		{name: "missing host", change: func(c *managedProductionCandidate) { c.Endpoint = "https://" }, apiKey: "secret", wantUnsafe: true},
		{name: "disabled", change: func(c *managedProductionCandidate) { c.Status = "disabled" }, apiKey: "secret", wantUnsafe: true},
		{name: "deleted", change: func(c *managedProductionCandidate) { c.Present = false }, apiKey: "secret", wantUnsafe: true},
		{name: "missing model version", change: func(c *managedProductionCandidate) { c.ModelVersion = " " }, apiKey: "secret", wantUnsafe: true},
		{name: "missing region", change: func(c *managedProductionCandidate) { c.Region = " " }, apiKey: "secret", wantUnsafe: true},
		{name: "failed connection", change: func(c *managedProductionCandidate) { c.ConnectionStatus = "failed" }, apiKey: "secret", wantUnsafe: true},
		{name: "failed capability", change: func(c *managedProductionCandidate) { c.CapabilityStatus = "failed" }, apiKey: "secret", wantUnsafe: true},
		{name: "untested capability", change: func(c *managedProductionCandidate) { c.CapabilityStatus = "untested" }, apiKey: "secret", wantUnsafe: true},
		{name: "stale probe", change: func(c *managedProductionCandidate) { c.CapabilityVersion = "structured-json-v2" }, apiKey: "secret", wantUnsafe: true},
		{name: "decrypt failure", apiKey: "secret", decryptErr: ErrManagedConfigUnavailable, wantUnsafe: true},
		{name: "empty API key", wantUnsafe: true},
		{name: "blank API key", apiKey: "   ", wantUnsafe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := valid
			if tt.change != nil {
				tt.change(&candidate)
			}
			err := validateManagedProductionCandidate(candidate, tt.apiKey, tt.decryptErr)
			if tt.wantUnsafe {
				if !errors.Is(err, ErrProductionUnsafe) || !strings.Contains(err.Error(), candidate.ID) {
					t.Fatalf("expected unsafe model %q, got %v", candidate.ID, err)
				}
			} else if err != nil {
				t.Fatalf("ready model rejected: %v", err)
			}
		})
	}
}

func TestManagedProductionReadinessRequiresCredentialCipher(t *testing.T) {
	store := NewPostgresStore(nil)
	if err := store.ValidateManagedProductionReadiness(context.Background()); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("expected missing credential cipher before database access, got %v", err)
	}
}

func TestApprovedPanelScopeRequiresEvidenceAndValidRoleBindings(t *testing.T) {
	store := NewMemoryStore()
	scope := approvedPanelScope{
		tenant: "tenant-1", stage: "senior", subject: "mathematics",
		archetype: "structured_steps", ready: false,
	}
	if err := validateApprovedPanelScope(context.Background(), store, scope); !errors.Is(err, ErrProductionUnsafe) || !strings.Contains(err.Error(), "completed evidence") {
		t.Fatalf("approved policy without completed evidence must fail: %v", err)
	}
	scope.ready = true
	if err := validateApprovedPanelScope(context.Background(), store, scope); !errors.Is(err, ErrProductionUnsafe) || !strings.Contains(err.Error(), "A/B/C binding") {
		t.Fatalf("approved policy without three valid roles must fail: %v", err)
	}
	for _, id := range []string{"model-a", "model-b", "model-c"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: scope.tenant, Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}
	for _, binding := range []struct {
		role string
		id   string
		rank int
	}{
		{ModelRolePrimaryA, "model-a", 1},
		{ModelRolePrimaryB, "model-b", 2},
		{ModelRoleArbiter, "model-c", 3},
	} {
		_, err := store.SaveModelRoleBinding(context.Background(), scope.tenant, "actor-1", SaveModelRoleBindingInput{
			EducationStage: scope.stage, SubjectCode: scope.subject, ArchetypeCode: scope.archetype,
			AgentRole: binding.role, ManagedModelAPIConfigID: binding.id,
			PromptVersion: "p1", StrengthRank: binding.rank,
		})
		if err != nil {
			t.Fatalf("save %s binding: %v", binding.role, err)
		}
	}
	if err := validateApprovedPanelScope(context.Background(), store, scope); err != nil {
		t.Fatalf("ready approved scope with strong independent arbiter rejected: %v", err)
	}
}
