package modelgovernance

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Stream 在最终回答轮输出文本与思考片段，携带工具时须设置 tool_choice=none。
// 断流或消费方取消时返回已收集的内容和用量，同时保留失败结果。
func (m *HTTPAgentModel) Stream(ctx context.Context, connection ManagedAPIConnection, request AgentRequest, emit func(AgentEvent) error) (AgentResponse, error) {
	if emit == nil || (len(request.Tools) > 0 && request.ToolChoice != "none") {
		return AgentResponse{}, ErrAgentInvalidRequest
	}
	response, err := m.send(ctx, connection, request, true)
	if err != nil {
		return AgentResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, managedProbeResponseLimit))
		return AgentResponse{}, agentProviderError(response.StatusCode, body, len(request.Tools) > 0)
	}
	state := agentStreamState{dashscope: connection.Config.AdapterType == "dashscope_native"}
	err = state.read(response.Body, emit)
	return state.result(connection, err)
}

func (s *agentStreamState) read(body io.Reader, emit func(AgentEvent) error) error {
	reader := &agentCountingReader{Reader: io.LimitReader(body, managedAgentMaxResponseBytes+1)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), managedProbeResponseLimit)
	for scanner.Scan() {
		if reader.ReadBytes > managedAgentMaxResponseBytes {
			return ErrAgentInvalidResponse
		}
		if err := s.consumeLine(scanner.Text(), emit); err != nil {
			return err
		}
		if s.done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := s.consumeEvent(emit); err != nil {
		return err
	}
	if reader.ReadBytes > managedAgentMaxResponseBytes || s.finishReason == "" {
		// 断流不能把已输出的部分文字当成成功答案；调用方保留失败状态并允许重试。
		return ErrAgentInvalidResponse
	}
	return nil
}

func (s *agentStreamState) consumeLine(line string, emit func(AgentEvent) error) error {
	if line == "" {
		return s.consumeEvent(emit)
	}
	if strings.HasPrefix(line, "data:") {
		s.eventData = append(s.eventData, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
	}
	return nil
}

func (s *agentStreamState) consumeEvent(emit func(AgentEvent) error) error {
	if len(s.eventData) == 0 {
		return nil
	}
	value := strings.Join(s.eventData, "\n")
	s.eventData = nil
	if value == "[DONE]" {
		s.done = true
		return nil
	}
	return s.consume([]byte(value), emit)
}

type agentCountingReader struct {
	io.Reader
	ReadBytes int
}

func (r *agentCountingReader) Read(body []byte) (int, error) {
	count, err := r.Reader.Read(body)
	r.ReadBytes += count
	return count, err
}

type agentStreamState struct {
	dashscope    bool
	done         bool
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	usage        ManagedAPIProbeUsage
	eventData    []string
}

func (s *agentStreamState) result(connection ManagedAPIConnection, prior error) (AgentResponse, error) {
	response, err := agentResponse(connection, AgentMessage{
		Role: "assistant", Content: s.content.String(), ReasoningContent: s.reasoning.String(),
	}, s.finishReason, s.usage)
	if prior != nil {
		return response, prior
	}
	return response, err
}

type agentStreamChunk struct {
	Choices []struct {
		Delta        agentProviderMessage `json:"delta"`
		FinishReason string               `json:"finish_reason"`
	} `json:"choices"`
	Output struct {
		Choices []agentProviderChoice `json:"choices"`
	} `json:"output"`
	Usage   json.RawMessage `json:"usage"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

func (s *agentStreamState) consume(data []byte, emit func(AgentEvent) error) error {
	var chunk agentStreamChunk
	if json.Unmarshal(data, &chunk) != nil {
		return ErrAgentInvalidResponse
	}
	if chunk.Code != "" || (len(chunk.Error) > 0 && string(chunk.Error) != "null") {
		return ManagedChatProviderError{StatusCode: http.StatusBadGateway}
	}
	if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
		s.usage = parseAgentUsage(chunk.Usage, s.dashscope)
	}
	message, reason, found := chunk.message(s.dashscope)
	if !found {
		return nil
	}
	if reason != "" {
		s.finishReason = reason
	}
	if len(message.ToolCalls) > 0 || s.finishReason == "tool_calls" || s.finishReason == "function_call" {
		// 即使供应商忽略 tool_choice=none，也不能把新调用当作最终文本或执行它。
		return ErrAgentInvalidResponse
	}
	return s.emitMessage(message, emit)
}

func (c agentStreamChunk) message(dashscope bool) (agentProviderMessage, string, bool) {
	if dashscope {
		if len(c.Output.Choices) == 0 {
			return agentProviderMessage{}, "", false
		}
		choice := c.Output.Choices[0]
		reason := choice.FinishReason
		if reason == "null" {
			reason = ""
		}
		return choice.Message, reason, true
	}
	if len(c.Choices) == 0 {
		return agentProviderMessage{}, "", false
	}
	choice := c.Choices[0]
	return choice.Delta, choice.FinishReason, true
}

func (s *agentStreamState) emitMessage(message agentProviderMessage, emit func(AgentEvent) error) error {
	if message.ReasoningContent != "" {
		s.reasoning.WriteString(message.ReasoningContent)
		if err := emit(AgentEvent{Type: "reasoning", Delta: message.ReasoningContent}); err != nil {
			return err
		}
	}
	if message.Content != "" {
		s.content.WriteString(message.Content)
		if err := emit(AgentEvent{Type: "content", Delta: message.Content}); err != nil {
			return err
		}
	}
	return nil
}
