package schoolassistant

import "edugrade-enterprise/services/api-gateway/internal/modelgovernance"

const conversationPlanningOutputTokens = 1_024
const conversationProbeOutputTokens = 256
const conversationMinimumAnswerTokens = 128

// conversationFinalReserve 按当前已知材料保留一次最短回答的输入和输出，不能预测未来工具结果大小。
// 工具增加材料后每轮重新计算；不足时停止继续规划，避免把全部额度用完才发现无法回答。
func conversationFinalReserve(p preparedConversation) (int64, error) {
	policy, err := modelgovernance.ResolveAgentContextPolicy(p.connection.Config)
	if err != nil {
		return 0, err
	}
	request := modelgovernance.AgentRequest{Messages: p.messages, ToolChoice: "none", MaxOutputTokens: min(512, policy.MaxOutputTokens)}
	_, usage, err := modelgovernance.ApplyAgentContextBudget(p.connection.Config, request)
	if err != nil {
		return 0, err
	}
	return int64(usage.EstimatedInputTokens + usage.MaxOutputTokens), nil
}

func conversationPlanningRequest(p preparedConversation, spent int64) (modelgovernance.AgentRequest, error) {
	reserve, err := conversationFinalReserve(p)
	if err != nil {
		return modelgovernance.AgentRequest{}, err
	}
	remaining := int64(conversationMaxTokens) - spent - reserve
	if remaining <= 0 {
		return modelgovernance.AgentRequest{}, modelgovernance.ErrAgentTurnBudgetExceeded
	}
	return modelgovernance.AgentRequest{Messages: p.messages, Tools: p.tools, ToolChoice: "auto", TokenBudget: remaining, MaxOutputTokens: conversationPlanningOutputTokens}, nil
}

func conversationAnswerRequest(p preparedConversation, spent int64) (modelgovernance.AgentRequest, error) {
	policy, err := modelgovernance.ResolveAgentContextPolicy(p.connection.Config)
	if err != nil {
		return modelgovernance.AgentRequest{}, err
	}
	remaining := int64(conversationMaxTokens) - spent
	if remaining <= 0 {
		return modelgovernance.AgentRequest{}, modelgovernance.ErrAgentTurnBudgetExceeded
	}
	request := modelgovernance.AgentRequest{Messages: p.messages, ToolChoice: "none", TokenBudget: remaining, MaxOutputTokens: conversationMinimumAnswerTokens}
	_, usage, err := modelgovernance.ApplyAgentContextBudget(p.connection.Config, request)
	if err != nil {
		return modelgovernance.AgentRequest{}, err
	}
	output := min(int64(policy.MaxOutputTokens), remaining-int64(usage.EstimatedInputTokens))
	if output < conversationMinimumAnswerTokens {
		return modelgovernance.AgentRequest{}, modelgovernance.ErrAgentTurnBudgetExceeded
	}
	request.TokenBudget, request.MaxOutputTokens = remaining, int(output)
	return request, nil
}
