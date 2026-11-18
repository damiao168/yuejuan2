package modelgovernance

import (
	"crypto/sha256"
	"encoding/json"
	"hash"
	"io"
	"strconv"
)

// agentContextCache 只随一次内部请求传递，保存计数和指纹，不保留另一份正文。
// 外部包仍能修改导出字段，因此只有完整输入指纹相同时才能复用校验与计数。
type agentContextCache struct {
	fingerprint [32]byte
	costs       agentContextCosts
}

func agentContextFingerprint(config ManagedAPIConfig, request AgentRequest, encoding string) ([32]byte, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	if err := encoder.Encode(struct {
		Encoding string
		Adapter  string
		Tools    []AgentTool
		Choice   string
		Thinking bool
		Messages []AgentMessage
	}{encoding, config.AdapterType, request.Tools, request.ToolChoice, request.DisableThinking, request.Messages}); err != nil {
		return [32]byte{}, ErrAgentInvalidRequest
	}
	for _, message := range request.Messages {
		if err := encoder.Encode(struct {
			Preserve bool
			Images   int
			Priority int
		}{message.PreserveContext, len(message.Images), message.ContextPriority}); err != nil {
			return [32]byte{}, ErrAgentInvalidRequest
		}
		for _, image := range message.Images {
			metadata := image
			metadata.Content = ""
			if err := encoder.Encode(metadata); err != nil {
				return [32]byte{}, ErrAgentInvalidRequest
			}
			writeContextFingerprintText(digest, image.Content)
		}
	}
	var fingerprint [32]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, nil
}

func writeContextFingerprintText(digest hash.Hash, value string) {
	// 长度分隔避免两个相邻字段拼成相同字节串；base64 原文直接参与哈希，不再解码图像。
	_, _ = io.WriteString(digest, strconv.Itoa(len(value)))
	_, _ = io.WriteString(digest, ":")
	_, _ = io.WriteString(digest, value)
}

func retainContextCosts(costs agentContextCosts, indices []int) agentContextCosts {
	retained := agentContextCosts{base: costs.base, messages: make([]int, len(indices))}
	for position, index := range indices {
		retained.messages[position] = costs.messages[index]
	}
	return retained
}
