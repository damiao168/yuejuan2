package modelgovernance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"
)

const (
	managedProbeResponseLimit     = 1 << 20
	managedCapabilityMaxTokens    = 1024
	ManagedCapabilityProbeVersion = "structured-json-v3"
)

type HTTPManagedAPIProber struct {
	timeout           time.Duration
	httpClient        *http.Client
	capabilityFlights singleflight.Group
}

func NewHTTPManagedAPIProber(timeout time.Duration) *HTTPManagedAPIProber {
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 8 * time.Second
	}
	prober := &HTTPManagedAPIProber{timeout: timeout}
	prober.httpClient = prober.newClient()
	return prober
}

func (p *HTTPManagedAPIProber) Probe(ctx context.Context, connection ManagedAPIConnection) (result ManagedAPIProbeResult) {
	value, _, shared := p.capabilityFlights.Do(managedCapabilityFlightKey(connection), func() (any, error) {
		return p.probeCapability(ctx, connection), nil
	})
	result = value.(ManagedAPIProbeResult)
	result.Coalesced = shared
	return result
}

func (p *HTTPManagedAPIProber) probeCapability(ctx context.Context, connection ManagedAPIConnection) (result ManagedAPIProbeResult) {
	started := time.Now()
	result = ManagedAPIProbeResult{
		ProbeMode: "capability", Provider: connection.Config.ProviderKey,
		Model: connection.Config.ModelName, Message: "结构化输出兼容检测失败",
	}
	defer func() { result.LatencyMS = time.Since(started).Milliseconds() }()
	client := p.client()
	if connection.Config.AdapterType == "dashscope_native" {
		return p.probeDashScope(ctx, client, connection, result)
	}
	return p.probeOpenAICompatible(ctx, client, connection, result)
}

func (p *HTTPManagedAPIProber) ProbeQuick(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	list, result := p.ListModels(ctx, connection)
	result.ProbeMode = "quick"
	result.GeneratedRequest = false
	if !result.OK {
		return result
	}
	if !slices.Contains(list.Models, connection.Config.ModelName) {
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", connection.Config.ModelName), "model")
	}
	result.Model = connection.Config.ModelName
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}
	result.CapabilityCheck = ManagedAPICheckResult{Code: "not_run", Message: "本次为零生成 Token 快速检查，未调用模型生成"}
	if connection.Config.LastCapabilityStatus == "success" && connection.Config.LastCapabilityVersion == ManagedCapabilityProbeVersion {
		result.CapabilityCheck = ManagedAPICheckResult{OK: true, Code: "reused", Message: "沿用此前的结构化能力验证结果"}
	}
	result.Message = "连接检查成功，本次未发送模型生成请求"
	return result
}

func (p *HTTPManagedAPIProber) ListModels(ctx context.Context, connection ManagedAPIConnection) (list ManagedAPIModelListResult, result ManagedAPIProbeResult) {
	started := time.Now()
	list.Provider = connection.Config.ProviderKey
	result = ManagedAPIProbeResult{ProbeMode: "quick", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName, Message: "获取模型列表失败"}
	defer func() {
		latency := time.Since(started).Milliseconds()
		list.LatencyMS = latency
		result.LatencyMS = latency
	}()
	endpoint, err := managedModelsEndpoint(connection.Config.BaseURL)
	if err != nil {
		return list, failedManagedProbe(result, "invalid_endpoint", "接口地址无效", "credential")
	}
	client := p.client()
	response, err := p.doReadOnlyRequest(ctx, client, endpoint, connection, &result)
	if err != nil {
		return list, networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return list, failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法读取的响应", "credential")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return list, statusManagedProbeFailure(result, response.StatusCode, "credential")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		return list, failedManagedProbe(result, "provider_invalid_response", "供应商模型列表格式异常", "model")
	}
	seen := make(map[string]bool, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			list.Models = append(list.Models, id)
		}
	}
	slices.Sort(list.Models)
	result.OK = true
	result.Message = "已获取可用模型"
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: "API Key 有效"}
	return list, result
}

func (p *HTTPManagedAPIProber) probeOpenAICompatible(ctx context.Context, client *http.Client, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	modelsEndpoint, err := managedModelsEndpoint(connection.Config.BaseURL)
	if err != nil {
		return failedManagedProbe(result, "invalid_endpoint", "接口地址无效", "credential")
	}
	response, err := p.doReadOnlyRequest(ctx, client, modelsEndpoint, connection, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法读取的响应", "credential")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "credential")
	}
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: "API Key 有效"}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &models); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商模型列表格式异常", "model")
	}
	modelFound := false
	for _, model := range models.Data {
		if model.ID == connection.Config.ModelName {
			modelFound = true
			break
		}
	}
	if !modelFound {
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", connection.Config.ModelName), "model")
	}
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}

	completionEndpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if err != nil {
		return failedManagedProbe(result, "invalid_endpoint", "接口地址无效", "capability")
	}
	payload, _ := json.Marshal(managedCapabilityPayload(connection))
	result.Diagnostic.ResponseFormat = "json_object"
	result.GeneratedRequest = true
	response, err = p.doRequest(ctx, client, http.MethodPost, completionEndpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "capability")
	}
	result.StatusCode = response.StatusCode
	body, readErr = readManagedProbeBody(response)
	if readErr != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法读取的响应", "capability")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return capabilityStatusManagedProbeFailure(result, response.StatusCode, body)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage managedOpenAIProbeUsage `json:"usage"`
	}
	if err = json.Unmarshal(body, &completion); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "capability")
	}
	result.Usage = completion.Usage.normalized()
	if len(completion.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "capability")
	}
	result.Diagnostic = managedCapabilityDiagnostic(completion.Choices[0].Message.Content, completion.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if code, message := validateManagedCapabilityJSON(completion.Choices[0].Message.Content, completion.Choices[0].FinishReason); code != "" {
		return failedManagedProbe(result, code, message, "capability")
	}
	result.CapabilityCheck = ManagedAPICheckResult{OK: true, Message: "结构化输出兼容检测通过"}
	result.OK = true
	result.ErrorCode = ""
	result.Message = "结构化输出兼容检测通过"
	return result
}

func (p *HTTPManagedAPIProber) probeDashScope(ctx context.Context, client *http.Client, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	if connection.Config.BaseURL != "https://dashscope.aliyuncs.com/api/v1" {
		return failedManagedProbe(result, "invalid_endpoint", "DashScope 原生接口地址无效", "credential")
	}
	parameters := map[string]any{"max_tokens": managedCapabilityMaxTokens, "result_format": "message", "temperature": 0}
	if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
		parameters["enable_thinking"] = false
	}
	payload, _ := json.Marshal(map[string]any{
		"model":      connection.Config.ModelName,
		"input":      map[string]any{"messages": managedCapabilityMessages()},
		"parameters": parameters,
	})
	endpoint := connection.Config.BaseURL + "/services/aigc/text-generation/generation"
	result.GeneratedRequest = true
	result.Diagnostic.ResponseFormat = "prompt_only"
	response, err := p.doRequest(ctx, client, http.MethodPost, endpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法读取的响应", "credential")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "credential")
	}
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: "API Key 有效"}
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}
	var completion struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(body, &completion); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "capability")
	}
	result.Usage = ManagedAPIProbeUsage{InputTokens: completion.Usage.InputTokens, OutputTokens: completion.Usage.OutputTokens, TotalTokens: completion.Usage.TotalTokens}
	if result.Usage.TotalTokens == 0 {
		result.Usage.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	if len(completion.Output.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "capability")
	}
	result.Diagnostic = managedCapabilityDiagnostic(completion.Output.Choices[0].Message.Content, completion.Output.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if code, message := validateManagedCapabilityJSON(completion.Output.Choices[0].Message.Content, completion.Output.Choices[0].FinishReason); code != "" {
		return failedManagedProbe(result, code, message, "capability")
	}
	result.CapabilityCheck = ManagedAPICheckResult{OK: true, Message: "结构化输出兼容检测通过"}
	result.OK = true
	result.Message = "结构化输出兼容检测通过"
	return result
}

func (p *HTTPManagedAPIProber) client() *http.Client {
	return p.httpClient
}

func (p *HTTPManagedAPIProber) newClient() *http.Client {
	transport := &http.Transport{
		DialContext: safePublicDialContext, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: p.timeout, IdleConnTimeout: 30 * time.Second,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 4,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   p.timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p *HTTPManagedAPIProber) doReadOnlyRequest(ctx context.Context, client *http.Client, endpoint string, connection ManagedAPIConnection, result *ManagedAPIProbeResult) (*http.Response, error) {
	return retryManagedReadOnlyRequest(ctx, p.timeout, func(attemptCtx context.Context) (*http.Response, error) {
		return p.doRequest(attemptCtx, client, http.MethodGet, endpoint, connection, nil, result)
	})
}

func retryManagedReadOnlyRequest(ctx context.Context, timeout time.Duration, request func(context.Context) (*http.Response, error)) (*http.Response, error) {
	// Two bounded attempts only for the idempotent model-list request. Never retry
	// completion requests, which may consume tokens and duplicate work.
	jitterLimit := min(timeout/10, 100*time.Millisecond)
	pause := min(timeout/10, 150*time.Millisecond)
	if jitterLimit > 0 {
		pause += time.Duration(rand.Int63n(int64(jitterLimit)))
	}
	// A fully timed-out first attempt gets only a short recovery window; the UI
	// must not wait twice as long as the original probe budget.
	totalBudget := min(2*timeout+pause, timeout+4*time.Second)
	boundedCtx, cancel := context.WithTimeout(ctx, totalBudget)
	defer cancel()
	response, err := request(boundedCtx)
	if err == nil || boundedCtx.Err() != nil || !retryableManagedProbeError(err) {
		return response, err
	}
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-boundedCtx.Done():
		return nil, boundedCtx.Err()
	case <-timer.C:
	}
	return request(boundedCtx)
}

func retryableManagedProbeError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidManagedConfig) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return true
	}
	var networkErr *net.OpError
	if errors.As(err, &networkErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

func (p *HTTPManagedAPIProber) doRequest(ctx context.Context, client *http.Client, method, endpoint string, connection ManagedAPIConnection, body []byte, result *ManagedAPIProbeResult) (*http.Response, error) {
	timing := newManagedRequestTiming()
	ctx = context.WithValue(ctx, managedRequestTimingKey{}, timing)
	ctx = httptrace.WithClientTrace(ctx, timing.clientTrace())
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if connection.Config.ProviderKey == "anthropic" {
		req.Header.Set("X-Api-Key", connection.APIKey)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	req.Header.Set("User-Agent", "EduGrade-API-Probe/2.0")
	response, err := client.Do(req)
	if result != nil {
		diagnostic, failureStage := timing.snapshot()
		result.ConnectionDiagnostic.Attempts++
		result.ConnectionDiagnostic.DNSMS += diagnostic.DNSMS
		result.ConnectionDiagnostic.ConnectMS += diagnostic.ConnectMS
		result.ConnectionDiagnostic.TLSMS += diagnostic.TLSMS
		result.ConnectionDiagnostic.TTFBMS += diagnostic.TTFBMS
		if err != nil {
			result.ConnectionDiagnostic.FailureStage = failureStage
		} else {
			result.ConnectionDiagnostic.FailureStage = ""
		}
	}
	return response, err
}

func readManagedProbeBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	return io.ReadAll(io.LimitReader(response.Body, managedProbeResponseLimit))
}

type managedOpenAIProbeUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
}

func (usage managedOpenAIProbeUsage) normalized() ManagedAPIProbeUsage {
	cached := usage.PromptTokensDetails.CachedTokens
	if usage.PromptCacheHitTokens > cached {
		cached = usage.PromptCacheHitTokens
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	return ManagedAPIProbeUsage{
		InputTokens: usage.PromptTokens, CachedInputTokens: cached,
		OutputTokens: usage.CompletionTokens, ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens: total,
	}
}

func managedCapabilityPayload(connection ManagedAPIConnection) map[string]any {
	payload := map[string]any{
		"model":           connection.Config.ModelName,
		"messages":        managedCapabilityMessages(),
		"max_tokens":      managedCapabilityMaxTokens,
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0,
		"stream":          false,
	}
	switch NewProviderRegistry().ProbePolicy(connection.Config.ProviderKey) {
	case "deepseek_hybrid":
		if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
			payload["thinking"] = map[string]string{"type": "disabled"}
		}
	case "qwen_hybrid":
		if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
			payload["enable_thinking"] = false
		}
	}
	return payload
}

func managedCapabilityMessages() []map[string]string {
	return []map[string]string{
		{"role": "system", "content": "只输出合法 JSON，不要解释，不要使用 Markdown。"},
		{"role": "user", "content": "将“第1题，满分2分”转换为 JSON；question_no 必须是字符串，max_score 必须是数字。"},
	}
}

func managedModelSupportsThinkingDisable(providerKey, modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch providerKey {
	case "deepseek":
		return !strings.Contains(modelName, "reasoner")
	case "aliyun":
		return strings.HasPrefix(modelName, "qwen3") && !strings.Contains(modelName, "thinking")
	default:
		return false
	}
}

func managedCapabilityFlightKey(connection ManagedAPIConnection) string {
	mac := hmac.New(sha256.New, []byte(connection.APIKey))
	_, _ = io.WriteString(mac, strings.Join([]string{
		connection.Config.ProviderKey, connection.Config.AdapterType,
		strings.TrimRight(connection.Config.BaseURL, "/"), connection.Config.ModelName,
		ManagedCapabilityProbeVersion,
	}, "\x00"))
	return hex.EncodeToString(mac.Sum(nil))
}

func managedCapabilityDiagnostic(content, finishReason, responseFormat string) ManagedAPIProbeDiagnostic {
	trimmed := strings.TrimSpace(content)
	hash := sha256.Sum256([]byte(trimmed))
	return ManagedAPIProbeDiagnostic{
		FinishReason: strings.TrimSpace(finishReason), ResponseFormat: responseFormat,
		ContentLength: utf8.RuneCountInString(trimmed), ContentSHA256: hex.EncodeToString(hash[:]),
		ContentPreview: boundedManagedContentPreview(trimmed),
	}
}

func boundedManagedContentPreview(content string) string {
	content = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return -1
		}
		return value
	}, content)
	runes := []rune(content)
	if len(runes) > 256 {
		return string(runes[:256]) + "…"
	}
	return content
}

func normalizeManagedCapabilityJSON(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") && strings.HasSuffix(content, "```") {
		if newline := strings.IndexByte(content, '\n'); newline >= 0 {
			content = strings.TrimSpace(strings.TrimSuffix(content[newline+1:], "```"))
		}
	}
	return content
}

func validateManagedCapabilityJSON(content, finishReason string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens":
		return "output_truncated", "检测输出被截断，请提高输出上限或更换模型"
	case "content_filter", "content_filtered":
		return "content_filtered", "检测请求被模型内容策略拦截"
	}
	content = normalizeManagedCapabilityJSON(content)
	if content == "" {
		return "empty_content", "模型返回内容为空"
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return "invalid_json", "模型返回内容不是合法 JSON"
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "invalid_json", "模型返回内容包含 JSON 之外的额外文本"
	}
	questionNo, questionExists := payload["question_no"]
	maxScore, scoreExists := payload["max_score"]
	questionText, questionTypeOK := questionNo.(string)
	scoreNumber, scoreTypeOK := maxScore.(json.Number)
	if !questionExists || !scoreExists || !questionTypeOK || !scoreTypeOK {
		return "schema_mismatch", "JSON 缺少必要字段或字段类型不正确"
	}
	score, scoreErr := scoreNumber.Float64()
	if strings.TrimSpace(questionText) != "1" || scoreErr != nil || score != 2 {
		return "semantic_mismatch", "JSON 字段值与测试文本不一致"
	}
	return "", ""
}

func capabilityStatusManagedProbeFailure(result ManagedAPIProbeResult, statusCode int, body []byte) ManagedAPIProbeResult {
	result.StatusCode = statusCode
	if statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity {
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "response_format") || strings.Contains(lower, "json_object") || strings.Contains(lower, "json mode") {
			return failedManagedProbe(result, "response_format_unsupported", "当前模型接口不支持 JSON 输出模式", "capability")
		}
	}
	return statusManagedProbeFailure(result, statusCode, "capability")
}

func failedManagedProbe(result ManagedAPIProbeResult, code, message, stage string) ManagedAPIProbeResult {
	result.OK = false
	result.ErrorCode = code
	result.Message = message
	check := ManagedAPICheckResult{OK: false, Code: code, Message: message}
	switch stage {
	case "credential":
		result.CredentialCheck = check
	case "model":
		result.ModelCheck = check
	case "capability":
		result.CapabilityCheck = check
	}
	return result
}

func networkManagedProbeFailure(result ManagedAPIProbeResult, err error, stage string) ManagedAPIProbeResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return failedManagedProbe(result, "provider_timeout", "模型服务连接超时，请稍后重试", stage)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return failedManagedProbe(result, "provider_timeout", "模型服务连接超时，请稍后重试", stage)
	}
	return failedManagedProbe(result, "provider_unavailable", "无法连接模型供应商", stage)
}

func statusManagedProbeFailure(result ManagedAPIProbeResult, statusCode int, stage string) ManagedAPIProbeResult {
	result.StatusCode = statusCode
	switch statusCode {
	case http.StatusUnauthorized:
		return failedManagedProbe(result, "credential_invalid", "API Key 无效或已被禁用", "credential")
	case http.StatusForbidden:
		return failedManagedProbe(result, "model_permission_denied", "API Key 没有访问该模型的权限", stage)
	case http.StatusNotFound:
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", result.Model), "model")
	case http.StatusTooManyRequests:
		return failedManagedProbe(result, "provider_rate_limited", "供应商额度不足或请求频率受限", stage)
	default:
		return failedManagedProbe(result, "provider_error", fmt.Sprintf("供应商返回 HTTP %d", statusCode), stage)
	}
}

func managedModelsEndpoint(baseURL string) (string, error) {
	return managedEndpoint(baseURL, "models")
}

func managedCompletionEndpoint(baseURL string) (string, error) {
	return managedEndpoint(baseURL, "chat/completions")
}

type managedRequestTimingKey struct{}

type managedRequestTiming struct {
	mu         sync.Mutex
	started    time.Time
	dnsStarted time.Time
	connectAt  time.Time
	tlsStarted time.Time
	stage      string
	diagnostic ManagedAPIConnectionDiagnostic
}

func newManagedRequestTiming() *managedRequestTiming {
	return &managedRequestTiming{started: time.Now(), stage: "response"}
}

func (timing *managedRequestTiming) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		TLSHandshakeStart: func() {
			timing.mu.Lock()
			timing.tlsStarted = time.Now()
			timing.stage = "tls"
			timing.mu.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			timing.mu.Lock()
			if !timing.tlsStarted.IsZero() {
				timing.diagnostic.TLSMS += time.Since(timing.tlsStarted).Milliseconds()
			}
			if err == nil {
				timing.stage = "response"
			}
			timing.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			timing.mu.Lock()
			timing.diagnostic.TTFBMS = time.Since(timing.started).Milliseconds()
			timing.stage = "body"
			timing.mu.Unlock()
		},
	}
}

func (timing *managedRequestTiming) beginDNS() {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.dnsStarted = time.Now()
	timing.stage = "dns"
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) endDNS(err error) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.diagnostic.DNSMS += time.Since(timing.dnsStarted).Milliseconds()
	if err == nil {
		timing.stage = "connect"
	}
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) beginConnect() {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.connectAt = time.Now()
	timing.stage = "connect"
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) endConnect(err error) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.diagnostic.ConnectMS += time.Since(timing.connectAt).Milliseconds()
	if err == nil {
		timing.stage = "tls"
	}
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) snapshot() (ManagedAPIConnectionDiagnostic, string) {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	return timing.diagnostic, timing.stage
}

func managedEndpoint(baseURL, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidManagedConfig
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + suffix
	return parsed.String(), nil
}

func safePublicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrInvalidManagedConfig
	}
	timing, _ := ctx.Value(managedRequestTimingKey{}).(*managedRequestTiming)
	timing.beginDNS()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	timing.endDNS(err)
	if err != nil || len(addresses) == 0 {
		return nil, errorsOrInvalid(err)
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 10 * time.Second}
	return dialPublicProviderAddresses(ctx, network, port, addresses, dialer.DialContext, timing)
}

func dialPublicProviderAddresses(ctx context.Context, network, port string, addresses []net.IPAddr,
	dial func(context.Context, string, string) (net.Conn, error), timing *managedRequestTiming) (net.Conn, error) {
	// Reject the entire DNS answer before any connection. Fallback must never
	// turn a mixed public/private answer into an SSRF bypass.
	for _, address := range addresses {
		if !publicProviderIP(address.IP) {
			return nil, ErrInvalidManagedConfig
		}
	}
	var lastErr error
	for _, address := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timing.beginConnect()
		connection, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
		timing.endConnect(err)
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, errorsOrInvalid(lastErr)
}

func publicProviderIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func errorsOrInvalid(err error) error {
	if err != nil {
		return err
	}
	return ErrInvalidManagedConfig
}

var _ ManagedAPIProber = (*HTTPManagedAPIProber)(nil)
var _ ManagedAPIModelLister = (*HTTPManagedAPIProber)(nil)
var _ ManagedAPIQuickProber = (*HTTPManagedAPIProber)(nil)
