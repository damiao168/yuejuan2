package modelgovernance

import "encoding/json"

// prepareManagedChatContext 只把估算用的模型视图与显示历史分开，不把模型视图转换回客户端消息。
// 裁剪直接选择原消息的完整轮次，保留附件正文、文件名、类型和大小；最后一个用户问题不能被删掉。
func prepareManagedChatContext(config ManagedAPIConfig, messages []ManagedChatMessage) ([]ManagedChatMessage, AgentContextUsage, error) {
	copyMessages := append([]ManagedChatMessage(nil), messages...)
	for index := range copyMessages {
		copyMessages[index].Attachments = append([]ManagedChatAttachment(nil), messages[index].Attachments...)
	}
	normalized, err := normalizeManagedChatMessages(copyMessages)
	if err != nil {
		return nil, AgentContextUsage{}, err
	}
	policy, err := ResolveAgentContextPolicy(config)
	if err != nil {
		return nil, AgentContextUsage{}, err
	}
	count, method, err := resolveContextCounter(policy.Tokenizer)
	if err != nil {
		return nil, AgentContextUsage{}, err
	}
	usage := AgentContextUsage{InputBudgetTokens: policy.MaxInputTokens, MaxOutputTokens: policy.MaxOutputTokens,
		ContextWindowTokens: policy.ContextWindowTokens, EstimationMethod: method}
	costs, err := measureManagedChatInput(normalized, config.AdapterType == "dashscope_native", count)
	if err != nil {
		return nil, usage, err
	}
	usage.EstimatedInputTokens = 64
	for _, cost := range costs {
		usage.EstimatedInputTokens += cost
	}
	for {
		if usage.EstimatedInputTokens <= usage.InputBudgetTokens {
			return normalized, usage, nil
		}
		cut := oldestManagedChatTurnEnd(normalized)
		if cut <= 0 {
			return nil, usage, ErrAgentContextExceeded
		}
		usage.DroppedMessages += cut
		for _, cost := range costs[:cut] {
			usage.EstimatedInputTokens -= cost
		}
		costs = costs[cut:]
		normalized = normalized[cut:]
	}
}

func oldestManagedChatTurnEnd(messages []ManagedChatMessage) int {
	first := -1
	for index, message := range messages {
		if message.Role == "user" {
			if first < 0 {
				first = index
			} else {
				return index
			}
		}
	}
	// 兼容传入历史以旧 assistant 开头的情况，只去掉当前用户问题之前的前缀。
	if first > 0 {
		return first
	}
	return 0
}

func measureManagedChatInput(messages []ManagedChatMessage, native bool, count contextTextCounter) ([]int, error) {
	costs := make([]int, 0, len(messages))
	// 与实际发送共用消息构造器；服务端以后增加固定规则时，也必须经过这里一并计入预算。
	for _, message := range managedChatProviderMessages(messages, native) {
		images := message.Images
		message.Images = nil
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, ErrManagedChatInvalidRequest
		}
		cost, err := measureContextMessage(encoded, images, count)
		if err != nil {
			return nil, err
		}
		costs = append(costs, cost)
	}
	return costs, nil
}

func managedChatProviderPayload(config ManagedAPIConfig, messages []ManagedChatMessage, stream, dashscope bool, maxOutputTokens int) map[string]any {
	parameters := map[string]any{"temperature": 0.7, "max_tokens": maxOutputTokens}
	if dashscope {
		parameters["result_format"] = "message"
		if stream {
			parameters["incremental_output"] = true
		}
		return map[string]any{"model": config.ModelName, "input": map[string]any{"messages": managedChatProviderMessages(messages, true)}, "parameters": parameters}
	}
	parameters["model"] = config.ModelName
	parameters["messages"] = managedChatProviderMessages(messages)
	parameters["stream"] = stream
	if stream {
		parameters["stream_options"] = map[string]any{"include_usage": true}
	}
	return parameters
}
