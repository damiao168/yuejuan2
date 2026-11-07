package subjective

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const gradingAgentSchemaVersion = "grading-agent-v1"

const maxGradingAgentResponseBytes int64 = 1 << 20

type HTTPAdapterConfig struct {
	BaseURL           string
	Token             string
	Timeout           time.Duration
	MaxRetries        int
	ModelVersion      string
	PromptVersion     string
	MinConfidence     float64
	ProviderKey       string
	DeploymentKey     string
	AdapterType       string
	DeploymentRegion  string
	CapabilityProfile string
	Client            *http.Client
	RequestObserver   AIGradingRequestObserver
}

type AIGradingRequestObserver interface {
	ObserveAIGradingRequest(version string, success bool)
}

type HTTPAdapter struct {
	baseURL           string
	token             string
	maxRetries        int
	policy            ModelPolicy
	providerKey       string
	deploymentKey     string
	adapterType       string
	deploymentRegion  string
	capabilityProfile string
	client            *http.Client
	requestObserver   AIGradingRequestObserver
}

type GradingAgentError struct {
	Code                 string
	Status               int
	Retryable            bool
	ExecutionDiagnostics *ExecutionDiagnostics
	cause                error
}

func (e *GradingAgentError) Error() string {
	return "grading agent request failed: " + e.Code
}

func (e *GradingAgentError) Unwrap() error {
	return e.cause
}

func NewHTTPAdapter(cfg HTTPAdapterConfig) *HTTPAdapter {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 250 * time.Second
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 || maxRetries > 1 {
		maxRetries = 1
	}
	minConfidence := cfg.MinConfidence
	if minConfidence <= 0 || minConfidence > 1 {
		minConfidence = 0.8
	}
	return &HTTPAdapter{
		baseURL:           strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		token:             cfg.Token,
		maxRetries:        maxRetries,
		providerKey:       firstConfigured(cfg.ProviderKey, "local"),
		deploymentKey:     firstConfigured(cfg.DeploymentKey, "local-qwen3-4b-q4-k-m"),
		adapterType:       firstConfigured(cfg.AdapterType, "local_llama_cpp"),
		deploymentRegion:  firstConfigured(cfg.DeploymentRegion, "on_premise"),
		capabilityProfile: firstConfigured(cfg.CapabilityProfile, "local-pilot-v1"),
		policy: ModelPolicy{
			ModelVersion:  strings.TrimSpace(cfg.ModelVersion),
			PromptVersion: strings.TrimSpace(cfg.PromptVersion),
			MinConfidence: minConfidence,
		},
		client:          client,
		requestObserver: cfg.RequestObserver,
	}
}

func (a *HTTPAdapter) Name() string {
	return "governed_grading_agent_http"
}

func (a *HTTPAdapter) Policy() ModelPolicy {
	return a.policy
}

func (a *HTTPAdapter) RuntimeStatus() RuntimeStatus {
	available := a.baseURL != "" && len(a.token) >= 32
	errorCode := ""
	if !available {
		errorCode = "ai_service_not_configured"
	}
	return RuntimeStatus{
		Enabled:       true,
		Available:     available,
		Mode:          "real",
		ErrorCode:     errorCode,
		ModelVersion:  a.policy.ModelVersion,
		PromptVersion: a.policy.PromptVersion,
	}
}

// 每次重试复用同一个请求 ID 和请求体，保证服务端幂等；只有明确可重试的代理错误才会再次发送。
func (a *HTTPAdapter) Grade(ctx context.Context, input AdapterInput) (result AdapterOutput, gradeErr error) {
	if input.InputVersionID != "" {
		return a.gradeImage(ctx, input)
	}
	attempted := false
	defer func() {
		if attempted && a.requestObserver != nil {
			a.requestObserver.ObserveAIGradingRequest("v1", gradeErr == nil)
		}
	}()
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		requestID = newAdapterRequestID()
	}
	input.RequestID = requestID
	request, err := a.buildRequest(requestID, input)
	if err != nil {
		return a.failureOutput(requestID, err), err
	}
	body, err := json.Marshal(request)
	if err != nil {
		wrapped := &GradingAgentError{Code: "request_encoding_failed", cause: err}
		return a.failureOutput(requestID, wrapped), wrapped
	}

	var lastErr error
	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		attempted = true
		response, requestErr := a.request(ctx, requestID, body)
		if requestErr == nil {
			output, mapErr := a.mapResponse(input, response)
			if mapErr != nil {
				mapErr = attachResponseDiagnostics(mapErr, response.Telemetry)
				return a.failureOutput(requestID, mapErr), mapErr
			}
			return output, nil
		}
		lastErr = requestErr
		if attempt >= a.maxRetries || !agentErrorRetryable(requestErr) {
			break
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			lastErr = ctx.Err()
			attempt = a.maxRetries
		case <-timer.C:
		}
	}
	return a.failureOutput(requestID, lastErr), lastErr
}

// 外部响应受大小和 JSON 结构双重限制；解析失败只返回脱敏错误，不把模型原文直接暴露给接口调用者。
func (a *HTTPAdapter) request(ctx context.Context, requestID string, body []byte) (gradingAgentResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/grading/grade", bytes.NewReader(body))
	if err != nil {
		return gradingAgentResponse{}, &GradingAgentError{Code: "request_creation_failed", cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Idempotency-Key", requestID)
	resp, err := a.client.Do(req)
	if err != nil {
		return gradingAgentResponse{}, &GradingAgentError{Code: "adapter_transport_error", Retryable: true, cause: err}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxGradingAgentResponseBytes+1))
	if readErr != nil {
		return gradingAgentResponse{}, &GradingAgentError{Code: "agent_response_read_failed", Retryable: true, cause: readErr}
	}
	if int64(len(responseBody)) > maxGradingAgentResponseBytes {
		return gradingAgentResponse{}, &GradingAgentError{Code: "agent_response_too_large"}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return gradingAgentResponse{}, parseAgentError(responseBody, resp.StatusCode, gradingAgentSchemaVersion, requestID)
	}
	if int64(len(responseBody)) > maxGradingAgentResponseBytes {
		return gradingAgentResponse{}, &GradingAgentError{Code: "agent_response_too_large"}
	}
	var response gradingAgentResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return gradingAgentResponse{}, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return gradingAgentResponse{}, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	return response, nil
}

func newAdapterRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("sg-%d", time.Now().UTC().UnixNano())
	}
	return "sg-" + hex.EncodeToString(raw)
}

func firstConfigured(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
