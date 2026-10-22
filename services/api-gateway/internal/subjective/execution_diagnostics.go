package subjective

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

// ExecutionDiagnostics 只保存协议白名单事实；模型正文和供应商任意错误文本不能进入运行诊断。
// token 使用指针区分已知的零和未知；partial/invalid 仍可保留已知非负字段。
type ExecutionDiagnostics struct {
	FailureKind           string                `json:"failure_kind,omitempty"`
	FinishOutcome         string                `json:"finish_outcome"`
	Attempts              int                   `json:"attempts"`
	ProviderRequestCount  int                   `json:"provider_request_count"`
	UsageStatus           string                `json:"usage_status"`
	Usage                 *DiagnosticTokenUsage `json:"usage,omitempty"`
	RecoveryAttempted     bool                  `json:"recovery_attempted"`
	RecoveryBlockedReason string                `json:"recovery_blocked_reason,omitempty"`
	OutputTokenLimit      *int64                `json:"output_token_limit,omitempty"`
}

type DiagnosticTokenUsage struct {
	InputTokens       *int64 `json:"input_tokens,omitempty"`
	CachedInputTokens *int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      *int64 `json:"output_tokens,omitempty"`
	ReasoningTokens   *int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens       *int64 `json:"total_tokens,omitempty"`
}

var diagnosticFailureKinds = map[string]bool{
	"output_truncated": true, "finish_reason_missing": true, "finish_reason_unknown": true,
	"provider_refused": true, "content_filtered": true, "unsupported_generation": true,
	"schema_invalid": true, "evidence_invalid": true, "capability_unavailable": true,
	"response_envelope_invalid": true,
}

func (d *ExecutionDiagnostics) UnmarshalJSON(raw []byte) error {
	type plain ExecutionDiagnostics
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if _, err := mathV2RequiredObject(raw, "finish_outcome", "attempts", "provider_request_count", "usage_status", "recovery_attempted"); err != nil {
		return err
	}
	*d = ExecutionDiagnostics(value)
	return validateExecutionDiagnostics(d)
}

func validateExecutionDiagnostics(d *ExecutionDiagnostics) error {
	if d == nil {
		return nil
	}
	allowed := map[string]bool{"completed": true, "output_limit": true, "filtered_or_refused": true, "unsupported_generation": true, "finish_missing": true, "finish_unknown": true, "envelope_invalid": true, "unavailable": true}
	if !allowed[d.FinishOutcome] || (d.FailureKind != "" && !diagnosticFailureKinds[d.FailureKind]) || d.Attempts < 0 || d.Attempts > 2 || d.ProviderRequestCount != d.Attempts || d.RecoveryAttempted || (d.RecoveryBlockedReason != "" && d.RecoveryBlockedReason != "not_enabled") || (d.OutputTokenLimit != nil && *d.OutputTokenLimit <= 0) {
		return ErrInvalidInput
	}
	if d.UsageStatus != "reported" && d.UsageStatus != "partial" && d.UsageStatus != "unavailable" && d.UsageStatus != "invalid" {
		return ErrInvalidInput
	}
	if d.UsageStatus == "unavailable" && d.Usage != nil {
		return ErrInvalidInput
	}
	if d.Usage != nil {
		u := d.Usage
		known := 0
		for _, v := range []*int64{u.InputTokens, u.CachedInputTokens, u.OutputTokens, u.ReasoningTokens, u.TotalTokens} {
			if v != nil {
				if *v < 0 {
					return ErrInvalidInput
				}
				known++
			}
		}
		if known == 0 {
			return ErrInvalidInput
		}
	}
	if d.UsageStatus == "reported" && (d.Usage == nil || d.Usage.InputTokens == nil || d.Usage.OutputTokens == nil || d.Usage.TotalTokens == nil) {
		return ErrInvalidInput
	}
	if d.UsageStatus == "partial" && d.Usage == nil {
		return ErrInvalidInput
	}
	expected := map[string][]string{"completed": {"", "schema_invalid", "evidence_invalid"}, "output_limit": {"output_truncated"}, "finish_missing": {"finish_reason_missing"}, "finish_unknown": {"finish_reason_unknown"}, "filtered_or_refused": {"provider_refused", "content_filtered"}, "unsupported_generation": {"unsupported_generation", "capability_unavailable"}, "envelope_invalid": {"response_envelope_invalid"}, "unavailable": {""}}
	if kinds, ok := expected[d.FinishOutcome]; ok && !containsString(kinds, d.FailureKind) {
		return ErrInvalidInput
	}
	return nil
}

func cloneExecutionDiagnostics(d *ExecutionDiagnostics) *ExecutionDiagnostics {
	if d == nil {
		return nil
	}
	raw, _ := json.Marshal(d)
	var out ExecutionDiagnostics
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}

func validateSuccessDiagnostics(telemetry gradingAgentTelemetry) error {
	d := telemetry.ExecutionDiagnostics
	if d == nil {
		return nil
	} // 旧协议响应可暂不携带诊断；新诊断一旦出现，必须与成功遥测一致。
	if validateExecutionDiagnostics(d) != nil || d.FinishOutcome != "completed" || d.FailureKind != "" || d.Attempts != telemetry.Attempts {
		return &GradingAgentError{Code: "agent_execution_diagnostics_invalid"}
	}
	if d.Usage != nil {
		for _, p := range []struct {
			known *int64
			value int64
		}{{d.Usage.InputTokens, telemetry.Usage.InputTokens}, {d.Usage.CachedInputTokens, telemetry.Usage.CachedInputTokens}, {d.Usage.OutputTokens, telemetry.Usage.OutputTokens}, {d.Usage.ReasoningTokens, telemetry.Usage.ReasoningTokens}, {d.Usage.TotalTokens, telemetry.Usage.TotalTokens}} {
			if p.known != nil && *p.known != p.value {
				return &GradingAgentError{Code: "agent_execution_diagnostics_invalid"}
			}
		}
	} else if telemetry.Usage != (ModelTokenUsage{}) {
		return &GradingAgentError{Code: "agent_execution_diagnostics_invalid"}
	}
	return nil
}

func diagnosticsFromOutput(output AdapterOutput, failureKind string) *ExecutionDiagnostics {
	d := cloneExecutionDiagnostics(output.Telemetry.ExecutionDiagnostics)
	if d != nil && d.FinishOutcome == "completed" && failureKind != "" {
		d.FailureKind = failureKind
	}
	return d
}

func diagnosticsFromError(err error, output AdapterOutput) *ExecutionDiagnostics {
	var agentErr *GradingAgentError
	if errors.As(err, &agentErr) && agentErr.ExecutionDiagnostics != nil {
		return cloneExecutionDiagnostics(agentErr.ExecutionDiagnostics)
	}
	return diagnosticsFromOutput(output, "")
}

func diagnosticsFromFailureDetail(detail map[string]any) (*ExecutionDiagnostics, error) {
	value, ok := detail["execution_diagnostics"]
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidInput
	}
	var d *ExecutionDiagnostics
	if json.Unmarshal(raw, &d) != nil || d == nil {
		return nil, ErrInvalidInput
	}
	return d, nil
}

func executionFailureDetail(d *ExecutionDiagnostics) map[string]any {
	if d == nil {
		return nil
	}
	return map[string]any{"execution_diagnostics": cloneExecutionDiagnostics(d)}
}

func validateOutputExecutionDiagnostics(output AdapterOutput) error {
	d := output.Telemetry.ExecutionDiagnostics
	if d == nil {
		return nil
	}
	if validateSuccessDiagnostics(gradingAgentTelemetry{Attempts: output.Telemetry.Attempts, Usage: output.Telemetry.Usage, ExecutionDiagnostics: d}) != nil {
		return ErrInvalidModelOutput
	}
	return nil
}

func gradeUsageReported(grade Grade) bool {
	// 旧调用方没有状态字段，沿用原用量流程；新协议明确未知时不能触发旧账本的零值回退。
	raw, err := json.Marshal(grade.RawOutput)
	if err != nil {
		return false
	}
	var envelope struct {
		Diagnostics *ExecutionDiagnostics `json:"execution_diagnostics"`
		Telemetry   struct {
			Diagnostics *ExecutionDiagnostics `json:"execution_diagnostics"`
		} `json:"telemetry"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	d := envelope.Diagnostics
	if d == nil {
		d = envelope.Telemetry.Diagnostics
	}
	return d == nil || d.UsageStatus == "reported"
}

// 代理明确拒绝的非正常生成不能因 HTTP 502/503 被再次推理；传输层重试仍遵循原配置。
func parseAgentError(body []byte, status int, schema, requestID string) error {
	var remote gradingAgentErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&remote) != nil || ensureJSONEOF(decoder) != nil || remote.SchemaVersion != schema || remote.RequestID != requestID || remote.Error.Code == "" || len(remote.Error.Code) > 128 || remote.Error.Retryable == nil {
		return &GradingAgentError{Code: "agent_error_response_invalid", Status: status}
	}
	code := remote.Error.Code
	allowedCode := map[string]bool{"model_unavailable": true, "model_timeout": true, "model_rate_limited": true, "model_output_invalid": true, "capability_not_supported": true, "invalid_request": true, "unauthorized": true, "forbidden": true, "model_context_exceeded": true, "model_auth_failed": true, "model_parameter_unsupported": true, "model_request_rejected": true, "model_response_invalid": true}
	if schema == ImageProtocolVersion {
		allowedCode["visual_model_unavailable"], allowedCode["model_dispatch_unknown"] = true, true
	}
	if !allowedCode[code] {
		code = "agent_http_error"
	}
	retryable := *remote.Error.Retryable && status >= http.StatusInternalServerError
	if !diagnosticsAllowRetry(remote.ExecutionDiagnostics) {
		retryable = false
	}
	if code == "capability_not_supported" || code == "unauthorized" || code == "forbidden" || code == "model_auth_failed" || code == "model_context_exceeded" || code == "model_parameter_unsupported" {
		retryable = false
	}
	if code == "visual_model_unavailable" || code == "model_dispatch_unknown" {
		retryable = false
	}
	return &GradingAgentError{Code: code, Status: status, Retryable: retryable, ExecutionDiagnostics: remote.ExecutionDiagnostics}
}

// 已知请求已经发给供应商时，重发相同 Python 请求可能重开一次内部重试循环。
// P0/P1 尚无跨层预算账本，只允许明确未派发或旧协议无诊断的传输重试。
func diagnosticsAllowRetry(d *ExecutionDiagnostics) bool {
	return d == nil || (d.ProviderRequestCount == 0 && d.FailureKind == "")
}

func attachResponseDiagnostics(err error, telemetry gradingAgentTelemetry) error {
	var agentErr *GradingAgentError
	if !errors.As(err, &agentErr) {
		return err
	}
	copy := *agentErr
	copy.ExecutionDiagnostics = cloneExecutionDiagnostics(telemetry.ExecutionDiagnostics)
	if copy.ExecutionDiagnostics != nil && copy.ExecutionDiagnostics.FailureKind == "" {
		copy.ExecutionDiagnostics.FailureKind = "schema_invalid"
	}
	return &copy
}
