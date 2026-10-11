package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func concurrencyRoleFixture() (*MemoryStore, SaveModelRoleBindingInput) {
	store := NewMemoryStore()
	for _, id := range []string{"model-a", "model-b"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "school", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}
	return store, SaveModelRoleBindingInput{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "*", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "model-a", PromptVersion: "p1", StrengthRank: 1, Status: "active"}
}

func TestRoleBindingStaleEditorAndRepeatedTokenCannotOverwrite(t *testing.T) {
	store, input := concurrencyRoleFixture()
	ctx := context.Background()
	initial, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = initial.Revision
	// 两人读取相同修订：B 先换模型，A 只改提示词，A 的旧草稿不能还原 B 的模型。
	newer := input
	newer.ManagedModelAPIConfigID = "model-b"
	newer.PromptVersion = "p2"
	current, err := store.SaveModelRoleBinding(ctx, "school", "B", newer)
	if err != nil {
		t.Fatal(err)
	}
	input.PromptVersion = "p3"
	for _, stale := range []SaveModelRoleBindingInput{input, newer} {
		if _, err = store.SaveModelRoleBinding(ctx, "school", "A", stale); !errors.Is(err, ErrModelRoleBindingChanged) {
			t.Fatalf("stale write accepted: %v", err)
		}
	}
	items, _ := store.ListConfiguredModelRoleBindings(ctx, "school", "senior", "mathematics", "*")
	if len(items) != 1 || items[0].ManagedModelAPIConfigID != "model-b" || items[0].Revision != current.Revision {
		t.Fatalf("newer save lost: %#v", items)
	}
	input.ExpectedRevision = current.Revision
	updated, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
	if err != nil || updated.ID != initial.ID || updated.CreatedBy != "A" || updated.Revision == current.Revision {
		t.Fatalf("fresh revision failed: %#v %v", updated, err)
	}
}

func TestRoleBindingRevisionRejectsABAAndCannotCreateFromUpdateToken(t *testing.T) {
	store, input := concurrencyRoleFixture()
	ctx := context.Background()
	original, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = original.Revision
	input.PromptVersion = "p2"
	middle, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = middle.Revision
	input.PromptVersion = "p1"
	returned, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
	if err != nil || returned.Revision == original.Revision {
		t.Fatalf("ABA reused revision: %v", err)
	}
	input.ExpectedRevision = original.Revision
	if _, err = store.SaveModelRoleBinding(ctx, "school", "B", input); !errors.Is(err, ErrModelRoleBindingChanged) {
		t.Fatalf("ABA accepted: %v", err)
	}
	input.AgentRole = ModelRolePrimaryB
	input.ExpectedRevision = returned.Revision
	if _, err = store.SaveModelRoleBinding(ctx, "school", "B", input); !errors.Is(err, ErrModelRoleBindingChanged) {
		t.Fatalf("update token created missing role: %v", err)
	}
}

func TestRoleBindingConcurrentCreateAndUpdateHaveSingleWinner(t *testing.T) {
	for _, create := range []bool{true, false} {
		store, input := concurrencyRoleFixture()
		ctx := context.Background()
		if !create {
			initial, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
			if err != nil {
				t.Fatal(err)
			}
			input.ExpectedRevision = initial.Revision
		}
		start := make(chan struct{})
		results := make(chan error, 20)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := store.SaveModelRoleBinding(ctx, "school", "A", input)
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		winners := 0
		for err := range results {
			if err == nil {
				winners++
			} else if !errors.Is(err, ErrModelRoleBindingChanged) {
				t.Fatalf("unexpected save error: %v", err)
			}
		}
		if winners != 1 {
			t.Fatalf("create=%v winners=%d", create, winners)
		}
	}
}

func TestRoleBindingMissingOrInvalidPreconditionNeverWrites(t *testing.T) {
	for _, revision := range []string{"", " ", "unknown"} {
		store, input := concurrencyRoleFixture()
		input.ExpectedRevision = revision
		if _, err := store.SaveModelRoleBinding(context.Background(), "school", "A", input); !errors.Is(err, ErrInvalidManagedConfig) {
			t.Fatalf("invalid revision accepted: %q %v", revision, err)
		}
		if len(store.roleBindings) != 0 {
			t.Fatal("invalid request changed bindings")
		}
		input.ExpectedRevision = ModelRoleBindingAbsent
		initial, err := store.SaveModelRoleBinding(context.Background(), "school", "A", input)
		if err != nil {
			t.Fatal(err)
		}
		input.ExpectedRevision = revision
		input.PromptVersion = "overwrite-without-precondition"
		if _, err = store.SaveModelRoleBinding(context.Background(), "school", "B", input); !errors.Is(err, ErrInvalidManagedConfig) {
			t.Fatalf("existing binding accepted missing revision: %v", err)
		}
		items, _ := store.ListConfiguredModelRoleBindings(context.Background(), "school", "senior", "mathematics", "*")
		if len(items) != 1 || items[0].Revision != initial.Revision || items[0].PromptVersion != "p1" {
			t.Fatal("missing precondition overwrote existing binding")
		}
	}
}

func TestPlatformRoleBindingHTTPRejectsMissingAndStaleRevision(t *testing.T) {
	store := NewMemoryStore()
	const school = "00000000-0000-0000-0000-000000000020"
	const model = "00000000-0000-0000-0000-000000000042"
	store.managedConfigs[model] = ManagedAPIConfig{ID: model, TenantID: school, Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	handler := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	input := savePlatformPanelRoleInput{TenantID: school, SaveModelRoleBindingInput: SaveModelRoleBindingInput{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: model, PromptVersion: "p1", StrengthRank: 1, Status: "active"}}
	send := func() (int, string) {
		body, _ := json.Marshal(input)
		response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(body), handler.SavePlatformPanelRoleBinding)
		return response.Code, response.Body.String()
	}
	if code, _ := send(); code != http.StatusBadRequest {
		t.Fatalf("missing precondition: %d", code)
	}
	input.ExpectedRevision = ModelRoleBindingAbsent
	code, body := send()
	if code != http.StatusOK || !strings.Contains(body, `"revision":`) {
		t.Fatalf("create response lacks revision: %d %s", code, body)
	}
	if code, body = send(); code != http.StatusConflict || !strings.Contains(body, "panel_model_binding_changed") {
		t.Fatalf("stale create not rejected: %d %s", code, body)
	}
}

func TestRoleBindingRevisionNormalizationMatchesPostgresUUIDComparison(t *testing.T) {
	store, input := concurrencyRoleFixture()
	initial, err := store.SaveModelRoleBinding(context.Background(), "school", "A", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = strings.ToUpper(initial.Revision)
	input.PromptVersion = "p2"
	if _, err = store.SaveModelRoleBinding(context.Background(), "school", "A", input); err != nil {
		t.Fatalf("canonical UUID rejected: %v", err)
	}
}
