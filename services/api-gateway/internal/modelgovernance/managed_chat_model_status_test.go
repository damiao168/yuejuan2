package modelgovernance

import (
	"encoding/json"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func TestSchoolChatModelCapacityUsesEffectiveApplicationPolicy(t *testing.T) {
	for _, window := range []int{0, 131072} {
		store, school, first, _ := documentSelectionFixture()
		config := store.managedConfigs[first]
		config.ContextPolicy = AgentContextPolicy{ContextWindowTokens: window}
		store.managedConfigs[first] = config
		policy, err := ResolveAgentContextPolicy(config)
		if err != nil {
			t.Fatal(err)
		}
		handler := NewHandler(store, nil, nil, testBaseline())
		user := auth.User{ID: uuid.NewString(), TenantID: school, Roles: []string{"school_admin"}}
		response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
		var payload struct {
			Model ManagedChatModelStatus `json:"model"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || payload.Model.ID != first || payload.Model.ContextWindowTokens != policy.ContextWindowTokens {
			t.Fatalf("metadata differs from applied context policy: code=%d model=%+v policy=%+v", response.Code, payload.Model, policy)
		}
	}
}
