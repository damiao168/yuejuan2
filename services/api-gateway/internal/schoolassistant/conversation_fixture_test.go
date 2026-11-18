package schoolassistant

import "edugrade-enterprise/services/api-gateway/internal/modelgovernance"

// 提示词允许在历史后插入当前轮服务端信息；测试按角色找当前问题，避免把 system 误当用户材料。
func lastTestUserMessage(request modelgovernance.AgentRequest) modelgovernance.AgentMessage {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			return request.Messages[index]
		}
	}
	return modelgovernance.AgentMessage{}
}

func validTestAgentRequest() modelgovernance.AgentRequest {
	return modelgovernance.AgentRequest{Messages: []modelgovernance.AgentMessage{{Role: "user", Content: "测试当前问题"}}}
}
