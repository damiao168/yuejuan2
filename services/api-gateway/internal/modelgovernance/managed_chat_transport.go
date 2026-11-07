package modelgovernance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func (c *HTTPManagedAPIChatter) StreamChat(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage, emit func(ManagedChatEvent) error) error {
	var endpoint string
	var payload []byte
	var err error
	dashscope := connection.Config.AdapterType == "dashscope_native"
	if dashscope {
		endpoint = strings.TrimRight(connection.Config.BaseURL, "/") + "/" + managedChatGenerationPath(messages)
		payload, _ = json.Marshal(map[string]any{
			"model":      connection.Config.ModelName,
			"input":      map[string]any{"messages": managedChatProviderMessages(messages, true)},
			"parameters": map[string]any{"result_format": "message", "incremental_output": true, "temperature": 0.7},
		})
	} else {
		endpoint, err = managedCompletionEndpoint(connection.Config.BaseURL)
		if err != nil {
			return err
		}
		payload, _ = json.Marshal(map[string]any{
			"model": connection.Config.ModelName, "messages": managedChatProviderMessages(messages),
			"stream": true, "stream_options": map[string]any{"include_usage": true}, "temperature": 0.7,
		})
	}
	transport := &http.Transport{DialContext: safePublicDialContext, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: c.timeout, IdleConnTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(managedChatContentTypeHeader, managedChatJSONContentType)
	req.Header.Set("User-Agent", "EduGrade-Admin-Chat/1.0")
	if dashscope {
		req.Header.Set("X-DashScope-SSE", "enable")
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, managedProbeResponseLimit))
		return ManagedChatProviderError{StatusCode: response.StatusCode}
	}
	return consumeManagedChatStream(response.Body, connection, dashscope, emit)
}

type managedChatStreamChunk struct {
	Choices []struct {
		Delta        agentProviderMessage `json:"delta"`
		Message      agentProviderMessage `json:"message"`
		FinishReason string               `json:"finish_reason"`
	} `json:"choices"`
	Output struct {
		Choices []struct {
			Message      agentProviderMessage `json:"message"`
			FinishReason string               `json:"finish_reason"`
		} `json:"choices"`
	} `json:"output"`
	Usage json.RawMessage `json:"usage"`
}

type managedChatStreamState struct {
	content, reasoning, finishReason string
	usage                            ManagedAPIProbeUsage
}

func consumeManagedChatStream(reader io.Reader, connection ManagedAPIConnection, dashscope bool, emit func(ManagedChatEvent) error) error {
	var state managedChatStreamState
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk managedChatStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if err := state.consumeChunk(chunk, dashscope, emit); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	completion, err := managedChatCompletion(connection, state.content, state.finishReason, state.usage)
	if err != nil {
		return err
	}
	completion.Message.ReasoningContent = state.reasoning
	return emit(ManagedChatEvent{Type: "done", Completion: &completion})
}

// 一帧中先发送思考、再发送正文、最后更新用量；回调失败时不能处理同帧后续数据。
func (state *managedChatStreamState) consumeChunk(chunk managedChatStreamChunk, dashscope bool, emit func(ManagedChatEvent) error) error {
	var answerDelta, reasoningDelta string
	if dashscope && len(chunk.Output.Choices) > 0 {
		choice := chunk.Output.Choices[0]
		answerDelta, reasoningDelta, state.finishReason = choice.Message.Content, choice.Message.ReasoningContent, choice.FinishReason
	} else if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		answerDelta, reasoningDelta = choice.Delta.Content, choice.Delta.ReasoningContent
		if choice.FinishReason != "" {
			state.finishReason = choice.FinishReason
		}
	}
	if reasoningDelta != "" {
		state.reasoning += reasoningDelta
		if err := emit(ManagedChatEvent{Type: "reasoning", Delta: reasoningDelta}); err != nil {
			return err
		}
	}
	if answerDelta != "" {
		state.content += answerDelta
		if err := emit(ManagedChatEvent{Type: "content", Delta: answerDelta}); err != nil {
			return err
		}
	}
	state.updateUsage(chunk.Usage, dashscope)
	return nil
}

func (state *managedChatStreamState) updateUsage(raw json.RawMessage, dashscope bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	if dashscope {
		var value struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		}
		if json.Unmarshal(raw, &value) == nil {
			state.usage = ManagedAPIProbeUsage{InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens}
		}
	} else {
		var value managedOpenAIProbeUsage
		if json.Unmarshal(raw, &value) == nil {
			state.usage = value.normalized()
		}
	}
}

func (c *HTTPManagedAPIChatter) chatOpenAICompatible(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	endpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"model": connection.Config.ModelName, "messages": managedChatProviderMessages(messages),
		"temperature": 0.7, "stream": false,
	})
	body, status, err := c.request(ctx, endpoint, connection, payload)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	if status < 200 || status >= 300 {
		return ManagedChatCompletion{}, ManagedChatProviderError{StatusCode: status}
	}
	var response struct {
		Choices []struct {
			Message      agentProviderMessage `json:"message"`
			FinishReason string               `json:"finish_reason"`
		} `json:"choices"`
		Usage managedOpenAIProbeUsage `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Choices) == 0 {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	completion, err := managedChatCompletion(connection, response.Choices[0].Message.Content, response.Choices[0].FinishReason, response.Usage.normalized())
	completion.Message.ReasoningContent = response.Choices[0].Message.ReasoningContent
	return completion, err
}

func (c *HTTPManagedAPIChatter) chatDashScope(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	endpoint := strings.TrimRight(connection.Config.BaseURL, "/") + "/" + managedChatGenerationPath(messages)
	payload, _ := json.Marshal(map[string]any{
		"model":      connection.Config.ModelName,
		"input":      map[string]any{"messages": managedChatProviderMessages(messages, true)},
		"parameters": map[string]any{"result_format": "message", "temperature": 0.7},
	})
	body, status, err := c.request(ctx, endpoint, connection, payload)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	if status < 200 || status >= 300 {
		return ManagedChatCompletion{}, ManagedChatProviderError{StatusCode: status}
	}
	var response struct {
		Output struct {
			Choices []struct {
				Message      agentProviderMessage `json:"message"`
				FinishReason string               `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Output.Choices) == 0 {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	usage := ManagedAPIProbeUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, TotalTokens: response.Usage.TotalTokens}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	choice := response.Output.Choices[0]
	completion, err := managedChatCompletion(connection, choice.Message.Content, choice.FinishReason, usage)
	completion.Message.ReasoningContent = choice.Message.ReasoningContent
	return completion, err
}

func (c *HTTPManagedAPIChatter) request(ctx context.Context, endpoint string, connection ManagedAPIConnection, payload []byte) ([]byte, int, error) {
	transport := &http.Transport{
		DialContext: safePublicDialContext, TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: c.timeout, IdleConnTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", managedChatJSONContentType)
	req.Header.Set(managedChatContentTypeHeader, managedChatJSONContentType)
	req.Header.Set("User-Agent", "EduGrade-Admin-Chat/1.0")
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := readManagedChatResponse(response.Body)
	return body, response.StatusCode, err
}

func readManagedChatResponse(body io.Reader) ([]byte, error) {
	// 多读一个字节判断超限，不能把截断的供应商 JSON 当成完整响应解析。
	raw, err := io.ReadAll(io.LimitReader(body, managedAgentMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > managedAgentMaxResponseBytes {
		return nil, ErrManagedChatResponseLimit
	}
	return raw, nil
}

func managedChatCompletion(connection ManagedAPIConnection, content, finishReason string, usage ManagedAPIProbeUsage) (ManagedChatCompletion, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	return ManagedChatCompletion{
		Message:   ManagedChatMessage{Role: "assistant", Content: content},
		ModelName: connection.Config.ModelName, DisplayName: connection.Config.DisplayName,
		ProviderKey: connection.Config.ProviderKey, FinishReason: strings.TrimSpace(finishReason), Usage: usage,
	}, nil
}
