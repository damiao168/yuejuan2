package modelgovernance

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	managedProbeResponseLimit     = 1 << 20
	managedCapabilityMaxTokens    = 1024
	ManagedCapabilityProbeVersion = "structured-json-v4"
	managedProbeInvalidEndpoint   = "接口地址无效"
	managedProbeAPIKeyValid       = "API Key 有效"
	managedProbeCapabilityOK      = "结构化输出兼容检测通过"
	// 生成检测需要等待供应商排队和完整输出，不能沿用模型列表查询的短超时。
	managedCapabilityTimeout = 60 * time.Second
)

type HTTPManagedAPIProber struct {
	timeout              time.Duration
	httpClient           *http.Client
	capabilityHTTPClient *http.Client
	capabilityFlights    singleflight.Group
}

// NewHTTPManagedAPIProber 的 timeout 只控制模型列表查询；生成检测使用独立的 60 秒预算。
func NewHTTPManagedAPIProber(timeout time.Duration) *HTTPManagedAPIProber {
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 8 * time.Second
	}
	prober := &HTTPManagedAPIProber{timeout: timeout}
	prober.httpClient = newManagedProbeHTTPClient(timeout)
	prober.capabilityHTTPClient = newManagedProbeHTTPClient(managedCapabilityTimeout)
	return prober
}

// 同一配置和能力版本的并发探测合并为一次请求，避免管理员重复点击造成重复生成和费用。
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
	defer func() {
		result.LatencyMS = time.Since(started).Milliseconds()
		// 此处只在单次实际检测内执行一次，合并等待者得到同一计量身份，失败生成也计一次。
		if result.GeneratedRequest {
			result.generationRequestID = "model-probe:" + uuid.NewString()
		}
	}()
	if connection.Config.AdapterType == "dashscope_native" {
		return p.probeDashScope(ctx, p.capabilityHTTPClient, connection, result)
	}
	return p.probeOpenAICompatible(ctx, p.client(), connection, result)
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
	result.TextCheck = ManagedAPICheckResult{Code: "not_run", Message: "本次为零生成 Token 快速检查，未调用模型生成"}
	if connection.Config.LastCapabilityStatus == "success" && connection.Config.LastCapabilityVersion == ManagedCapabilityProbeVersion {
		result.CapabilityCheck = ManagedAPICheckResult{OK: true, Code: "reused", Message: "沿用此前的结构化能力验证结果"}
	}
	result.Message = "连接检查成功，本次未发送模型生成请求"
	return result
}

// ProbeText 只验证模型能否返回非空文本；普通聊天不依赖 JSON 输出，因此不发送 response_format。
func (p *HTTPManagedAPIProber) ProbeText(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	// 普通回复也会消耗 Token；同一配置的并发点击复用同一次请求，并与结构化检测分开合并。
	value, _, shared := p.capabilityFlights.Do("text:"+managedCapabilityFlightKey(connection), func() (any, error) {
		return p.probeText(ctx, connection), nil
	})
	result := value.(ManagedAPIProbeResult)
	result.Coalesced = shared
	return result
}

func (p *HTTPManagedAPIProber) probeText(ctx context.Context, connection ManagedAPIConnection) (result ManagedAPIProbeResult) {
	started := time.Now()
	result = ManagedAPIProbeResult{
		ProbeMode: string(ManagedAPIProbeText), Provider: connection.Config.ProviderKey,
		Model: connection.Config.ModelName, Message: "文本生成检测失败",
	}
	defer func() {
		result.LatencyMS = time.Since(started).Milliseconds()
		if result.GeneratedRequest {
			result.generationRequestID = "model-probe:" + uuid.NewString()
		}
	}()
	if connection.Config.AdapterType == "dashscope_native" {
		return p.probeDashScopeText(ctx, p.capabilityHTTPClient, connection, result)
	}
	return p.probeOpenAICompatibleText(ctx, connection, result)
}

func (p *HTTPManagedAPIProber) ListModels(ctx context.Context, connection ManagedAPIConnection) (list ManagedAPIModelListResult, result ManagedAPIProbeResult) {
	started := time.Now()
	list.Provider = connection.Config.ProviderKey
	// 供应商可能成功返回空列表；保持空数组，避免 JSON null 被前端当成可遍历的模型列表。
	list.Models = []string{}
	result = ManagedAPIProbeResult{ProbeMode: "quick", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName, Message: "获取模型列表失败"}
	defer func() {
		latency := time.Since(started).Milliseconds()
		list.LatencyMS = latency
		result.LatencyMS = latency
	}()
	endpoint, err := managedModelsEndpoint(connection.Config.BaseURL)
	if err != nil {
		return list, failedManagedProbe(result, "invalid_endpoint", managedProbeInvalidEndpoint, "credential")
	}
	client := p.client()
	response, err := p.doReadOnlyRequest(ctx, client, endpoint, connection, &result)
	if err != nil {
		return list, networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return list, bodyManagedProbeFailure(result, readErr, "credential")
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
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: managedProbeAPIKeyValid}
	return list, result
}

var _ ManagedAPIProber = (*HTTPManagedAPIProber)(nil)

var _ ManagedAPIModelLister = (*HTTPManagedAPIProber)(nil)

var _ ManagedAPIQuickProber = (*HTTPManagedAPIProber)(nil)
