package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedModelContextPolicyContractCoversAllConfigurationEntrypoints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	for _, name := range []string{"CreateManagedModelAPIInput", "AutoManagedModelAPIInput", "ManagedModelAPIConfigUpdateInput", "ManagedModelAPIConfig"} {
		property := assertProperty(t, object(t, schemas, name), "context_policy")
		if property["$ref"] != "#/components/schemas/ManagedModelContextPolicy" {
			t.Fatalf("%s lost the shared capacity policy", name)
		}
	}
	policy := object(t, schemas, "ManagedModelContextPolicy")
	if policy["additionalProperties"] != false {
		t.Fatal("capacity configuration must reject misspelled policy fields")
	}
	for name, minimum := range map[string]float64{"context_window_tokens": 2048, "max_input_tokens": 256, "max_output_tokens": 128} {
		field := assertProperty(t, policy, name)
		alternatives, ok := field["anyOf"].([]any)
		if !ok || len(alternatives) != 2 || alternatives[0].(map[string]any)["const"] != float64(0) || alternatives[1].(map[string]any)["minimum"] != minimum {
			t.Fatalf("%s must accept zero/default or the supported nonzero range", name)
		}
	}
	assertEnumContains(t, assertProperty(t, policy, "tokenizer"), "utf8_bytes")
	assertEnumContains(t, assertProperty(t, policy, "tokenizer"), "cl100k_base")
	assertEnumContains(t, assertProperty(t, policy, "tokenizer"), "o200k_base")
	request := object(t, schemas, "SchoolConversationRequest")
	if assertProperty(t, request, "memory")["$ref"] != "#/components/schemas/SchoolConversationMemory" {
		t.Fatal("source memory is missing from the authoritative conversation contract")
	}
	memory := object(t, schemas, "SchoolConversationMemory")
	if memory["additionalProperties"] != false || assertProperty(t, memory, "version")["const"] != float64(2) {
		t.Fatal("memory contract must remain bounded and explicitly versioned")
	}
	entry := object(t, schemas, "SchoolConversationMemoryEntry")
	assertRequired(t, entry, []string{"source_id", "order", "content"})
	found := false
	for _, value := range object(t, schemas, "SchoolConversationEvent")["oneOf"].([]any) {
		event := value.(map[string]any)
		if assertProperty(t, event, "type")["const"] == "context" {
			found = true
			assertRequired(t, event, []string{"type", "request_id", "seq", "context_budget"})
		}
	}
	if !found {
		t.Fatal("actual context omission feedback is missing from the event contract")
	}
}
