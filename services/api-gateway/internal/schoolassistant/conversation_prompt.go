package schoolassistant

import "edugrade-enterprise/services/api-gateway/internal/modelgovernance"

// conversationPromptMessages 把稳定规则和历史放在前面，避免秒级时间变化使后续历史失去前缀缓存机会。
// 动态信息仍由服务端作为 system 提供；客户端历史或摘录不能借此取得指令和业务证据的地位。
func conversationPromptMessages(instructions string, history []modelgovernance.AgentMessage, dynamic string) []modelgovernance.AgentMessage {
	messages := make([]modelgovernance.AgentMessage, 0, len(history)+2)
	messages = append(messages, modelgovernance.AgentMessage{Role: "system", Content: instructions})
	if len(history) == 0 {
		return append(messages, modelgovernance.AgentMessage{Role: "system", Content: dynamic})
	}
	messages = append(messages, history[:len(history)-1]...)
	messages = append(messages, modelgovernance.AgentMessage{Role: "system", Content: dynamic}, history[len(history)-1])
	return messages
}
