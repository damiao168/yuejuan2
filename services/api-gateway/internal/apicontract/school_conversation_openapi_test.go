package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchoolConversationContractKeepsClientMessagesUntrustedAndSourcesExplicit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	operation := object(t, object(t, object(t, document, "paths"), "/api/v1/school-assistant/conversations/stream"), "post")
	if operation["operationId"] != "streamSchoolConversation" {
		t.Fatal("unified conversation route missing")
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	request := object(t, schemas, "SchoolConversationRequest")
	assertRequired(t, request, []string{"messages"})
	assertPropertyRef(t, request, "messages", "#/components/schemas/ManagedChatMessage")
	assertEnumContains(t, assertProperty(t, object(t, schemas, "ManagedChatMessage"), "role"), "user")
	assertEnumContains(t, assertProperty(t, object(t, schemas, "ManagedChatMessage"), "role"), "assistant")
	roles := assertProperty(t, object(t, schemas, "ManagedChatMessage"), "role")["enum"].([]any)
	if len(roles) != 2 {
		t.Fatal("client contract must not accept system/tool messages")
	}
	assertEnumContains(t, assertProperty(t, object(t, schemas, "SchoolAssistantSource"), "kind"), "exam_search")
	events := object(t, schemas, "SchoolConversationEvent")["oneOf"].([]any)
	found := false
	for _, value := range events {
		event := value.(map[string]any)
		if assertProperty(t, event, "type")["const"] == "sources" {
			found = true
			assertRequired(t, event, []string{"type", "request_id", "seq", "sources", "actions"})
			assertPropertyRef(t, event, "sources", "#/components/schemas/SchoolAssistantSource")
		}
	}
	if !found {
		t.Fatal("source events missing")
	}
}
