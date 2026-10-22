package server

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func documentProbeTextFixture() modelgovernance.ManagedAPIProbeResult {
	return modelgovernance.ManagedAPIProbeResult{
		OK: true, ProbeMode: "text", GeneratedRequest: true,
		TextCheck: modelgovernance.ManagedAPICheckResult{OK: true},
	}
}

// 同一个默认模型可用于聊天；解析另需结构化证据，后续结构化失败不能同时撤销文本能力。
func TestDocumentModelResolverRequiresStructuredEvidenceBeyondDefaultChat(t *testing.T) {
	ctx := context.Background()
	store := modelgovernance.NewMemoryStore()
	text := documentProbeTextFixture()
	input := modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "用途边界测试", AdapterType: "openai_compatible",
		BaseURL: "https://provider.example/v1", ModelName: "school-model", ModelVersion: "v1", Region: "global",
		APIKey: "synthetic-document-key-123456", Status: "active", IsDefault: true, InitialProbe: &text,
	}
	item, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	resolveDocument := newDocumentModelResolver(store)
	if resolveDocument == nil {
		t.Fatal("managed memory store did not provide a document resolver")
	}
	assertUseBoundaries := func(wantDocument bool) {
		t.Helper()
		chat, chatErr := modelgovernance.ResolveDefaultManagedAPI(ctx, store, "school-a")
		if chatErr != nil || chat == nil || chat.Config.ID != item.ID {
			t.Fatalf("text-verified default chat is unavailable: connection=%#v err=%v", chat, chatErr)
		}
		document, documentErr := resolveDocument(ctx, "school-a")
		if !wantDocument {
			if document != nil || !errors.Is(documentErr, modelgovernance.ErrManagedCapabilityRequired) {
				t.Fatalf("document parsing accepted missing structured evidence: config=%#v err=%v", document, documentErr)
			}
			return
		}
		if documentErr != nil || document == nil || document.AdapterType != input.AdapterType ||
			document.ProviderKey != input.ProviderKey || document.BaseURL != input.BaseURL ||
			document.ModelName != input.ModelName || document.ModelVersion != input.ModelVersion || document.APIKey != input.APIKey {
			t.Fatalf("structured-verified model was not forwarded to document parsing: config=%#v err=%v", document, documentErr)
		}
	}
	assertUseBoundaries(false)

	structured := modelgovernance.ManagedAPIProbeResult{
		OK: true, ProbeMode: "structured", GeneratedRequest: true,
		TextCheck: modelgovernance.ManagedAPICheckResult{OK: true}, CapabilityCheck: modelgovernance.ManagedAPICheckResult{OK: true},
	}
	item, err = store.RecordManagedAPIProbe(ctx, "school-a", item.ID, item.UpdatedAt, structured)
	if err != nil || item.LastCapabilityVersion != "structured-json-v4" {
		t.Fatalf("current structured evidence was not recorded: version=%q err=%v", item.LastCapabilityVersion, err)
	}
	assertUseBoundaries(true)

	structured.OK, structured.ErrorCode = false, "schema_mismatch"
	structured.CapabilityCheck = modelgovernance.ManagedAPICheckResult{Code: "schema_mismatch"}
	item, err = store.RecordManagedAPIProbe(ctx, "school-a", item.ID, item.UpdatedAt, structured)
	if err != nil {
		t.Fatal(err)
	}
	assertUseBoundaries(false)
}

func TestDocumentModelResolverKeepsLocalPathWithoutDefault(t *testing.T) {
	ctx := context.Background()
	store := modelgovernance.NewMemoryStore()
	resolveDocument := newDocumentModelResolver(store)
	if resolveDocument == nil {
		t.Fatal("managed memory store did not provide a document resolver")
	}
	if document, err := resolveDocument(ctx, "school-a"); err != nil || document != nil {
		t.Fatalf("unconfigured school did not retain local parsing: config=%#v err=%v", document, err)
	}
	text := documentProbeTextFixture()
	_, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "备用模型", AdapterType: "openai_compatible",
		BaseURL: "https://provider.example/v1", ModelName: "backup-model", ModelVersion: "v1", Region: "global",
		APIKey: "synthetic-backup-key-123456", Status: "active", InitialProbe: &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	if document, err := resolveDocument(ctx, "school-a"); err != nil || document != nil {
		t.Fatalf("backup model replaced the local path without a default: config=%#v err=%v", document, err)
	}
}
