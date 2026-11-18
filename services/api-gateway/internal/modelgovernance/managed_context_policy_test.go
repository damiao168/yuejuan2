package modelgovernance

import (
	"context"
	"errors"
	"testing"
)

func verifyManagedContextPolicyLifecycle(t *testing.T, store ManagedAPIConfigStore, tenantID, actorID string) {
	t.Helper()
	ctx := context.Background()
	custom, _ := NormalizeAgentContextPolicy(AgentContextPolicy{ContextWindowTokens: 131_072, MaxOutputTokens: 8_192, Tokenizer: "o200k_base"})
	item, err := store.CreateManagedAPIConfig(ctx, tenantID, actorID, ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "context test", AdapterType: "openai_compatible", BaseURL: "https://models.example.test/v1",
		ModelName: "model-a", ModelVersion: "v1", Region: "global", APIKey: "synthetic-context-test-secret", Status: "active", ContextPolicy: &custom,
	})
	if err != nil || item.ContextPolicy != custom {
		t.Fatalf("create did not retain the typed policy: %+v %v", item.ContextPolicy, err)
	}
	connection, err := store.GetManagedAPIConnection(ctx, tenantID, item.ID)
	if err != nil || connection.Config.ContextPolicy != custom || connection.Config.DataPolicy["training_allowed"] != false || connection.Config.DataPolicy["retention_mode"] != "no_store" {
		t.Fatalf("stored context or privacy policy changed: %+v %v", connection.Config, err)
	}
	update := ManagedAPIConfigUpdateInput{DisplayName: item.DisplayName, AdapterType: item.AdapterType, BaseURL: item.BaseURL,
		ModelName: item.ModelName, ModelVersion: item.ModelVersion, Region: item.Region, Status: item.Status}
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.ContextPolicy != custom {
		t.Fatalf("ordinary metadata edit lost the existing policy: %+v %v", item.ContextPolicy, err)
	}
	// 密钥轮换不能夹带修改上下文策略；即使客户端发来另一组合法预算，也保留服务端原值。
	defaultPolicy, _ := NormalizeAgentContextPolicy(AgentContextPolicy{})
	update.CredentialOnly, update.APIKey, update.ContextPolicy = true, "synthetic-new-context-test-secret", &defaultPolicy
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.ContextPolicy != custom {
		t.Fatalf("credential rotation changed context policy: %+v %v", item.ContextPolicy, err)
	}
	update.CredentialOnly, update.APIKey, update.ContextPolicy = false, "", nil
	update.ModelName = "smaller-model"
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.ContextPolicy != defaultPolicy {
		t.Fatalf("new model inherited the prior large window: %+v %v", item.ContextPolicy, err)
	}
	update.ContextPolicy = &custom
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.ContextPolicy != custom {
		t.Fatalf("explicit policy edit was not saved: %+v %v", item.ContextPolicy, err)
	}
	items, err := store.ListManagedAPIConfigs(ctx, tenantID)
	if err != nil || len(items) != 1 || items[0].ContextPolicy != custom {
		t.Fatalf("list did not expose the saved policy: %+v %v", items, err)
	}
}

func TestMemoryManagedContextPolicyLifecycle(t *testing.T) {
	verifyManagedContextPolicyLifecycle(t, NewMemoryStore(), "school-context", "actor")
}

func TestPostgresManagedContextPolicyLifecycle(t *testing.T) {
	store, tenant, _, actor := newAnswerSheetSelectionPostgresFixture(t)
	verifyManagedContextPolicyLifecycle(t, store, tenant, actor)
}

func TestAutoManagedContextPolicyIncludesCredentialReuseAndRejectsBeforeProbe(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	calls := 0
	service := NewAutoManagedAPIConfigService(store, countingManagedProber{calls: &calls, result: successfulManagedProbe()})
	custom, _ := NormalizeAgentContextPolicy(AgentContextPolicy{ContextWindowTokens: 65_536, Tokenizer: "cl100k_base"})
	first, _, _, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{Provider: "deepseek", ModelName: "deepseek-model-a",
		APIKey: "synthetic-context-credential", ContextPolicy: &custom})
	if err != nil || first.ContextPolicy != custom {
		t.Fatalf("automatic create discarded policy: %+v %v", first.ContextPolicy, err)
	}
	second, _, _, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{Provider: "deepseek", ModelName: "deepseek-model-b",
		CredentialSourceID: first.ID, ContextPolicy: &custom})
	if err != nil || second.ContextPolicy != custom || calls != 2 {
		t.Fatalf("credential reuse discarded explicit policy: %+v calls=%d err=%v", second.ContextPolicy, calls, err)
	}
	invalid := AgentContextPolicy{ContextWindowTokens: 4_096, MaxInputTokens: 4_096}
	_, _, _, err = service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{Provider: "deepseek", ModelName: "deepseek-model-c",
		APIKey: "synthetic-context-credential", ContextPolicy: &invalid})
	if !errors.Is(err, ErrAgentContextPolicy) || calls != 2 {
		t.Fatalf("invalid budget reached the probe: calls=%d err=%v", calls, err)
	}
}
