package modelgovernance

import "testing"

func TestPublicDecisionThinkingPolicyDoesNotGuessThirdPartyProtocols(t *testing.T) {
	for _, test := range []struct {
		name, provider, model string
		disable               bool
		wantPolicy            bool
	}{
		{"known hybrid decision", "deepseek", "deepseek-flash", true, true},
		{"ordinary final answer", "deepseek", "deepseek-flash", false, false},
		{"custom provider model name", "custom", "deepseek-v4.1-flash", true, false},
		{"dedicated reasoning model", "deepseek", "deepseek-reasoner", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := agentProviderPayload(ManagedAPIConfig{ProviderKey: test.provider, ModelName: test.model}, AgentRequest{DisableThinking: test.disable}, true, false)
			_, hasThinking := payload["thinking"]
			_, hasEnable := payload["enable_thinking"]
			if hasThinking != test.wantPolicy || hasEnable {
				t.Fatalf("unexpected provider thinking policy: thinking=%v enable=%v", hasThinking, hasEnable)
			}
		})
	}
}
