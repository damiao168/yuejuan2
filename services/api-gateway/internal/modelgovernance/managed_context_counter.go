package modelgovernance

import (
	"encoding/json"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

type contextTextCounter func(string) (int, error)

var contextCodecs = map[string]*struct {
	once  sync.Once
	codec tokenizer.Codec
	err   error
}{
	"cl100k_base": {},
	"o200k_base":  {},
}

func resolveContextCounter(encoding string) (contextTextCounter, string, error) {
	if encoding == "" || encoding == "utf8_bytes" {
		return func(value string) (int, error) { return len(value), nil }, "utf8_bytes_and_image_patches", nil
	}
	entry, exists := contextCodecs[encoding]
	if !exists {
		return nil, "", ErrAgentContextPolicy
	}
	// 编码词表编译进依赖中；初始化只加载本地只读词表，不在用户请求中下载数据或按模型名猜编码。
	entry.once.Do(func() { entry.codec, entry.err = tokenizer.Get(tokenizer.Encoding(encoding)) })
	if entry.err != nil {
		return nil, "", ErrAgentContextPolicy
	}
	return entry.codec.Count, encoding + "_with_protocol_and_image_reserve", nil
}

type agentContextCosts struct {
	base     int
	messages []int
}

func measureAgentContext(request AgentRequest, count contextTextCounter) (agentContextCosts, error) {
	tools, err := json.Marshal(agentProviderTools(request.Tools))
	if err != nil {
		return agentContextCosts{}, ErrAgentInvalidRequest
	}
	toolTokens, err := count(string(tools))
	if err != nil {
		return agentContextCosts{}, ErrAgentInvalidRequest
	}
	costs := agentContextCosts{base: 64 + toolTokens + len(request.ToolChoice), messages: make([]int, len(request.Messages))}
	for index, message := range request.Messages {
		plain := message
		plain.Images = nil
		encoded, err := json.Marshal(agentProviderMessages([]AgentMessage{plain}))
		if err != nil {
			return agentContextCosts{}, ErrAgentInvalidRequest
		}
		costs.messages[index], err = measureContextMessage(encoded, message.Images, count)
		if err != nil {
			return agentContextCosts{}, err
		}
	}
	return costs, nil
}

func measureContextMessage(encoded []byte, images []ManagedChatAttachment, count contextTextCounter) (int, error) {
	text, err := count(string(encoded))
	if err != nil {
		return 0, ErrAgentInvalidRequest
	}
	// 文本编码只精确到所选编码；消息信封和图片仍留保护额度，不能把总值当作供应商精确计费。
	total := 32 + text
	for _, image := range images {
		reserve, err := estimateAgentImageTokens(image)
		if err != nil {
			return 0, err
		}
		total += reserve
	}
	return total, nil
}

func (costs agentContextCosts) total(indices []int) int {
	total := costs.base
	for _, index := range indices {
		total += costs.messages[index]
	}
	return total
}
