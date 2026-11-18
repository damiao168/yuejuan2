package modelgovernance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"strings"
)

var (
	ErrAgentContextExceeded    = errors.New("managed agent context exceeds the configured budget")
	ErrAgentContextPolicy      = errors.New("invalid managed agent context policy")
	ErrAgentTurnBudgetExceeded = errors.New("managed agent turn budget exhausted")
)

// AgentContextPolicy 是管理员核对供应商文档后填写的应用预算，不代表接口自动发现的模型能力。
// 零值字段采用产品默认值；输入、输出和内部安全余量必须同时容纳于窗口。
type AgentContextPolicy struct {
	ContextWindowTokens int `json:"context_window_tokens"`
	MaxInputTokens      int `json:"max_input_tokens"`
	MaxOutputTokens     int `json:"max_output_tokens"`
	// Tokenizer 必须与部署模型的编码相符；不支持的模型沿用 utf8_bytes 保守估算。
	Tokenizer string `json:"tokenizer"`
}

// AgentContextUsage 仅含计数，不包含消息正文；估算值和供应商返回的实际 usage 必须分开统计。
type AgentContextUsage struct {
	EstimatedInputTokens  int    `json:"estimated_input_tokens"`
	InputBudgetTokens     int    `json:"input_budget_tokens"`
	MaxOutputTokens       int    `json:"max_output_tokens"`
	ContextWindowTokens   int    `json:"context_window_tokens"`
	DroppedMessages       int    `json:"dropped_messages"`
	EstimationMethod      string `json:"estimation_method"`
	DroppedMemoryMessages int    `json:"dropped_memory_messages"`
}

// NormalizeAgentContextPolicy 校验上下文预算；不按第三方模型名称猜测窗口，未知模型沿用保守产品默认值。
func NormalizeAgentContextPolicy(policy AgentContextPolicy) (AgentContextPolicy, error) {
	if policy.Tokenizer == "" {
		policy.Tokenizer = "utf8_bytes"
	}
	if policy.Tokenizer != "utf8_bytes" && policy.Tokenizer != "cl100k_base" && policy.Tokenizer != "o200k_base" {
		return AgentContextPolicy{}, ErrAgentContextPolicy
	}
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = 32_768
	}
	if policy.ContextWindowTokens < 2_048 || policy.ContextWindowTokens > 2_097_152 {
		return AgentContextPolicy{}, ErrAgentContextPolicy
	}
	if policy.MaxOutputTokens == 0 {
		policy.MaxOutputTokens = min(4_096, policy.ContextWindowTokens/4)
	}
	if policy.MaxInputTokens == 0 {
		policy.MaxInputTokens = policy.ContextWindowTokens - policy.MaxOutputTokens - agentContextSafetyTokens(policy)
	}
	if policy.MaxInputTokens < 256 || policy.MaxInputTokens > 2_097_152 || policy.MaxOutputTokens < 128 || policy.MaxOutputTokens > 32_768 ||
		policy.MaxInputTokens+policy.MaxOutputTokens+agentContextSafetyTokens(policy) > policy.ContextWindowTokens {
		return AgentContextPolicy{}, ErrAgentContextPolicy
	}
	return policy, nil
}

func agentContextSafetyTokens(policy AgentContextPolicy) int {
	return min(1_024, max(128, policy.ContextWindowTokens/16))
}

// ResolveAgentContextPolicy 优先使用存储层投影的显式配置，旧记录缺少预算时采用默认值。
// 持久化预算畸形时拒绝生成，避免默默换成比管理员期望更大的窗口。
func ResolveAgentContextPolicy(config ManagedAPIConfig) (AgentContextPolicy, error) {
	if config.ContextPolicy != (AgentContextPolicy{}) {
		return NormalizeAgentContextPolicy(config.ContextPolicy)
	}
	if value, ok := config.DataPolicy["context_budget"]; ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return AgentContextPolicy{}, ErrAgentContextPolicy
		}
		var policy AgentContextPolicy
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if string(encoded) == "null" || decoder.Decode(&policy) != nil {
			return AgentContextPolicy{}, ErrAgentContextPolicy
		}
		return NormalizeAgentContextPolicy(policy)
	}
	return NormalizeAgentContextPolicy(AgentContextPolicy{})
}

// ApplyAgentContextBudget 在发送前估算完整输入，只删除最早的完整用户轮次。
// system、最新用户问题及其后的工具批次始终保留；这些内容仍过大时拒绝发送，不截断业务证据。
func ApplyAgentContextBudget(config ManagedAPIConfig, request AgentRequest) (AgentRequest, AgentContextUsage, error) {
	policy, err := ResolveAgentContextPolicy(config)
	if err != nil {
		return AgentRequest{}, AgentContextUsage{}, err
	}
	fingerprint, err := agentContextFingerprint(config, request, policy.Tokenizer)
	if err != nil {
		return AgentRequest{}, AgentContextUsage{}, err
	}
	cacheValid := request.contextCache != nil && request.contextCache.fingerprint == fingerprint
	if !cacheValid {
		if err := validateAgentRequest(request); err != nil {
			return AgentRequest{}, AgentContextUsage{}, err
		}
	}
	hasMemory, hasQuestion := false, false
	for _, message := range request.Messages {
		if message.PreserveContext && (message.Role != "user" || len(message.Images) != 0 || len(message.ToolCalls) != 0 || message.ToolCallID != "" || message.Name != "") {
			return AgentRequest{}, AgentContextUsage{}, ErrAgentInvalidRequest
		}
		hasMemory = hasMemory || message.PreserveContext
		hasQuestion = hasQuestion || message.Role == "user" && !message.PreserveContext
	}
	if hasMemory && !hasQuestion {
		return AgentRequest{}, AgentContextUsage{}, ErrAgentInvalidRequest
	}
	count, method, err := resolveContextCounter(policy.Tokenizer)
	if err != nil {
		return AgentRequest{}, AgentContextUsage{}, err
	}
	var costs agentContextCosts
	if cacheValid {
		costs = request.contextCache.costs
	} else {
		costs, err = measureAgentContext(request, count)
		if err != nil {
			return AgentRequest{}, AgentContextUsage{}, err
		}
	}
	outputTokens := policy.MaxOutputTokens
	if request.MaxOutputTokens > 0 {
		outputTokens = min(outputTokens, request.MaxOutputTokens)
	}
	usage := AgentContextUsage{InputBudgetTokens: policy.MaxInputTokens, MaxOutputTokens: outputTokens,
		ContextWindowTokens: policy.ContextWindowTokens, EstimationMethod: method}
	indices := make([]int, len(request.Messages))
	for index := range indices {
		indices[index] = index
	}
	// 先独立验证模型容量，再检查单轮额度；不能把费用预算不足提示成附件或模型窗口过大。
	fit := func(limit int) bool {
		for costs.total(indices) > limit {
			next := trimAgentContextIndices(request.Messages, indices)
			if len(next) == len(indices) {
				// 记忆只是辅助材料；当前问题和系统规则仍放不下时，才按优先级省略记忆。
				next = dropAgentMemoryIndex(request.Messages, indices)
			}
			if len(next) == len(indices) {
				return false
			}
			indices = next
		}
		return true
	}
	finishUsage := func() {
		usage.EstimatedInputTokens = costs.total(indices)
		usage.DroppedMessages = len(request.Messages) - len(indices)
		retained := make(map[int]bool, len(indices))
		for _, index := range indices {
			retained[index] = true
		}
		for index, message := range request.Messages {
			if message.PreserveContext && !retained[index] {
				usage.DroppedMemoryMessages++
			}
		}
	}
	if !fit(policy.MaxInputTokens) {
		finishUsage()
		return AgentRequest{}, usage, ErrAgentContextExceeded
	}
	if request.TokenBudget != 0 {
		usage.InputBudgetTokens = int(max(int64(0), min(int64(policy.MaxInputTokens), request.TokenBudget-int64(outputTokens))))
		if request.TokenBudget < 0 || !fit(usage.InputBudgetTokens) {
			finishUsage()
			return AgentRequest{}, usage, ErrAgentTurnBudgetExceeded
		}
	}
	finishUsage()
	request.MaxOutputTokens = outputTokens
	prepared := make([]AgentMessage, 0, len(indices))
	for _, index := range indices {
		prepared = append(prepared, request.Messages[index])
	}
	request.Messages = prepared
	if usage.DroppedMessages == 0 && cacheValid {
		return request, usage, nil
	}
	preparedFingerprint := fingerprint
	if usage.DroppedMessages > 0 {
		preparedFingerprint, err = agentContextFingerprint(config, request, policy.Tokenizer)
		if err != nil {
			return AgentRequest{}, usage, err
		}
	}
	request.contextCache = &agentContextCache{fingerprint: preparedFingerprint, costs: retainContextCosts(costs, indices)}
	return request, usage, nil
}

func trimAgentContextIndices(messages []AgentMessage, indices []int) []int {
	first, next := -1, -1
	for position, index := range indices {
		message := messages[index]
		if message.Role == "user" && !message.PreserveContext {
			if first < 0 {
				first = position
			} else {
				next = position
				break
			}
		}
	}
	if next < 0 && first > 0 {
		next = first
	}
	if next < 0 {
		return indices
	}
	result := make([]int, 0, len(indices))
	for position, index := range indices {
		message := messages[index]
		if position >= next || message.Role == "system" || message.PreserveContext {
			result = append(result, index)
		}
	}
	return result
}

func dropAgentMemoryIndex(messages []AgentMessage, indices []int) []int {
	selected := -1
	for position, index := range indices {
		if messages[index].PreserveContext && (selected < 0 || messages[index].ContextPriority < messages[indices[selected]].ContextPriority) {
			selected = position
		}
	}
	if selected >= 0 {
		result := append([]int(nil), indices[:selected]...)
		return append(result, indices[selected+1:]...)
	}
	return indices
}

func estimateAgentImageTokens(attachment ManagedChatAttachment) (int, error) {
	prefix := "data:" + attachment.MediaType + ";base64,"
	data, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(attachment.Content, prefix))
	if err != nil {
		return 0, ErrManagedChatInvalidImage
	}
	dimensions, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, ErrManagedChatInvalidImage
	}
	return 1_024 + ((dimensions.Width+27)/28)*((dimensions.Height+27)/28), nil
}

func managedContextDataPolicy(existing map[string]any, policy AgentContextPolicy) map[string]any {
	result := make(map[string]any, len(existing)+1)
	for key, value := range existing {
		result[key] = value
	}
	result["context_budget"] = policy
	return result
}

func managedUpdatedContextPolicy(current ManagedAPIConfig, input ManagedAPIConfigUpdateInput) (AgentContextPolicy, error) {
	if input.CredentialOnly {
		return ResolveAgentContextPolicy(current)
	}
	if input.ContextPolicy != nil {
		return NormalizeAgentContextPolicy(*input.ContextPolicy)
	}
	// 模型身份改变时，不把旧模型的大窗口继承给未经核对的新模型；仅轮换凭据保留原预算。
	if current.AdapterType != input.AdapterType || current.BaseURL != input.BaseURL || current.ModelName != input.ModelName ||
		current.ModelVersion != input.ModelVersion || current.Region != input.Region {
		return NormalizeAgentContextPolicy(AgentContextPolicy{})
	}
	return ResolveAgentContextPolicy(current)
}
