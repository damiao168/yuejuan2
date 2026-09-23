package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPAdapterV2 struct{ base *HTTPAdapter }

func NewHTTPAdapterV2(cfg HTTPAdapterConfig) *HTTPAdapterV2 {
	return &HTTPAdapterV2{base: NewHTTPAdapter(cfg)}
}

func (a *HTTPAdapterV2) Name() string                 { return "governed_math_grading_agent_http_v2" }
func (a *HTTPAdapterV2) Policy() ModelPolicy          { return a.base.Policy() }
func (a *HTTPAdapterV2) RuntimeStatus() RuntimeStatus { return a.base.RuntimeStatus() }
func (a *HTTPAdapterV2) SupportsPanelMathV2() bool    { return true }

type gradingAgentV2Response struct {
	SchemaVersion                string                    `json:"schema_version"`
	RequestID                    string                    `json:"request_id"`
	Status                       string                    `json:"status"`
	Delivery                     string                    `json:"delivery"`
	CriterionCandidates          []gradingAgentV2Candidate `json:"criterion_candidates"`
	AlternativeSolutionCandidate bool                      `json:"alternative_solution_candidate"`
	RiskFlags                    []string                  `json:"risk_flags"`
	NeedsHumanReview             bool                      `json:"needs_human_review"`
	ModelVersion                 string                    `json:"model_version"`
	PromptVersion                string                    `json:"prompt_version"`
	RubricVersion                string                    `json:"rubric_version"`
	CapabilityProfile            string                    `json:"capability_profile"`
	Mock                         bool                      `json:"mock"`
	Telemetry                    gradingAgentTelemetry     `json:"telemetry"`
}

type gradingAgentV2Candidate struct {
	RubricPointID string   `json:"rubric_point_id"`
	Status        string   `json:"status"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Confidence    float64  `json:"confidence"`
	ReasonCode    string   `json:"reason_code"`
}

func (a *HTTPAdapterV2) Grade(ctx context.Context, input AdapterInput) (AdapterOutput, error) {
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		requestID = newAdapterRequestID()
	}
	input.RequestID = requestID
	if input.ActiveCrop == nil {
		err := &GradingAgentError{Code: "math_active_crop_unavailable"}
		return a.base.failureOutput(requestID, err), err
	}
	built, err := BuildGradingAgentV2Request(requestID, input, *input.ActiveCrop, a.base.policy)
	if err != nil {
		return a.base.failureOutput(requestID, err), err
	}
	defer built.Clear()
	var lastErr error
	for attempt := 0; attempt <= a.base.maxRetries; attempt++ {
		response, requestErr := a.request(ctx, requestID, built.Body)
		if requestErr == nil {
			output, mapErr := a.mapResponse(input, response)
			if mapErr != nil {
				return a.base.failureOutput(requestID, mapErr), mapErr
			}
			return output, nil
		}
		lastErr = requestErr
		if attempt >= a.base.maxRetries || !agentErrorRetryable(requestErr) {
			break
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			lastErr = ctx.Err()
			attempt = a.base.maxRetries
		case <-timer.C:
		}
	}
	return a.base.failureOutput(requestID, lastErr), lastErr
}

func (a *HTTPAdapterV2) request(ctx context.Context, requestID string, body []byte) (gradingAgentV2Response, error) {
	if a.base.baseURL == "" || len(a.base.token) < 32 {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "adapter_not_configured"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base.baseURL+"/grading/grade-v2", bytes.NewReader(body))
	if err != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "request_creation_failed", cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.base.token)
	req.Header.Set("Idempotency-Key", requestID)
	resp, err := a.base.client.Do(req)
	if err != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "adapter_transport_error", Retryable: true, cause: err}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxGradingAgentResponseBytes+1))
	if readErr != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "agent_response_read_failed", Retryable: true, cause: readErr}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var remote gradingAgentErrorResponse
		_ = json.Unmarshal(responseBody, &remote)
		code := strings.TrimSpace(remote.Error.Code)
		if code == "" {
			code = "agent_http_error"
		}
		return gradingAgentV2Response{}, &GradingAgentError{Code: code, Status: resp.StatusCode, Retryable: remote.Error.Retryable || resp.StatusCode >= 500}
	}
	if int64(len(responseBody)) > maxGradingAgentResponseBytes {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "agent_response_too_large"}
	}
	var response gradingAgentV2Response
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	if err := validateMathV2RequiredFields(responseBody); err != nil {
		return gradingAgentV2Response{}, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	return response, nil
}

// Go zero values must not make an omitted contract field (notably mock,
// confidence or the alternative-solution flag) look like a valid assertion.
func validateMathV2RequiredFields(body []byte) error {
	fields, err := mathV2RequiredObject(body, "schema_version", "request_id", "status", "delivery", "criterion_candidates", "alternative_solution_candidate", "risk_flags", "needs_human_review", "model_version", "prompt_version", "rubric_version", "capability_profile", "mock", "telemetry")
	if err != nil {
		return err
	}
	if _, err := mathV2RequiredObject(fields["telemetry"], "adapter", "provider", "deployment", "region", "attempts", "repair_attempted", "prior_error_codes", "elapsed_ms"); err != nil {
		return err
	}
	var candidates []json.RawMessage
	if err := json.Unmarshal(fields["criterion_candidates"], &candidates); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if _, err := mathV2RequiredObject(candidate, "rubric_point_id", "status", "evidence_ids", "confidence", "reason_code"); err != nil {
			return err
		}
	}
	return nil
}

func mathV2RequiredObject(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, name := range names {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("missing or null v2 field: %s", name)
		}
	}
	return fields, nil
}

func (a *HTTPAdapterV2) mapResponse(input AdapterInput, response gradingAgentV2Response) (AdapterOutput, error) {
	if input.MathEvidence == nil || response.SchemaVersion != gradingAgentV2BuilderSchemaVersion || response.RequestID != input.RequestID ||
		response.Status != "candidate_mapping" || response.Delivery != "teacher_suggestion" || !response.NeedsHumanReview || response.Mock ||
		response.ModelVersion != a.base.policy.ModelVersion || response.PromptVersion != a.base.policy.PromptVersion ||
		response.RubricVersion != input.Rubric.Version || response.CapabilityProfile != a.base.capabilityProfile {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_response_contract_mismatch"}
	}
	if response.Telemetry.Adapter != a.base.adapterType || response.Telemetry.Provider != a.base.providerKey ||
		response.Telemetry.Deployment != a.base.deploymentKey || response.Telemetry.Region != a.base.deploymentRegion ||
		response.Telemetry.Attempts < 1 || response.Telemetry.Attempts > 2 || response.Telemetry.ElapsedMS < 0 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_telemetry_invalid"}
	}
	if response.Telemetry.PriorErrorCodes == nil || len(response.Telemetry.PriorErrorCodes) > 2 || response.CriterionCandidates == nil || len(response.CriterionCandidates) > 100 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_response_contract_mismatch"}
	}
	for _, code := range response.Telemetry.PriorErrorCodes {
		if len(code) == 0 || len(code) > 128 {
			return AdapterOutput{}, &GradingAgentError{Code: "agent_telemetry_invalid"}
		}
	}
	knownPoints, knownEvidence := map[string]bool{}, map[string]bool{}
	for _, point := range input.Rubric.Points {
		knownPoints[point.ID] = true
	}
	for _, step := range input.MathEvidence.Steps {
		knownEvidence[step.ID] = true
	}
	for _, formula := range input.MathEvidence.Formulas {
		knownEvidence[formula.ID] = true
	}
	seen := map[string]bool{}
	candidates := make([]MathCriterionCandidate, 0, len(response.CriterionCandidates))
	confidence := 1.0
	for _, candidate := range response.CriterionCandidates {
		if !knownPoints[candidate.RubricPointID] || seen[candidate.RubricPointID] ||
			(candidate.Status != "supported" && candidate.Status != "contradicted" && candidate.Status != "uncertain") ||
			candidate.Confidence < 0 || candidate.Confidence > 1 || strings.TrimSpace(candidate.ReasonCode) == "" || len(candidate.ReasonCode) > 256 || candidate.EvidenceIDs == nil || len(candidate.EvidenceIDs) > 100 {
			return AdapterOutput{}, &GradingAgentError{Code: "agent_math_candidate_invalid"}
		}
		seen[candidate.RubricPointID] = true
		seenEvidence := map[string]bool{}
		for _, id := range candidate.EvidenceIDs {
			if !knownEvidence[id] || seenEvidence[id] {
				return AdapterOutput{}, &GradingAgentError{Code: "agent_math_candidate_invalid"}
			}
			seenEvidence[id] = true
		}
		if (candidate.Status == "supported" || candidate.Status == "contradicted") && len(candidate.EvidenceIDs) == 0 {
			return AdapterOutput{}, &GradingAgentError{Code: "agent_math_candidate_invalid"}
		}
		if candidate.Confidence < confidence {
			confidence = candidate.Confidence
		}
		candidates = append(candidates, MathCriterionCandidate{RubricPointID: candidate.RubricPointID, Status: candidate.Status, EvidenceIDs: nonNilStrings(candidate.EvidenceIDs), Confidence: candidate.Confidence, ReasonCode: candidate.ReasonCode})
	}
	if len(candidates) == 0 {
		confidence = 0
	}
	allowedRisks := map[string]bool{"alternative_solution_candidate": true, "schema_repaired": true, "prompt_injection_suspected": true, "human_review_required": true}
	if response.RiskFlags == nil || len(response.RiskFlags) > 20 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_risk_flag_invalid"}
	}
	seenRisks := map[string]bool{}
	for _, flag := range response.RiskFlags {
		if !allowedRisks[flag] || seenRisks[flag] {
			return AdapterOutput{}, &GradingAgentError{Code: "agent_risk_flag_invalid"}
		}
		seenRisks[flag] = true
	}
	if response.AlternativeSolutionCandidate && !containsString(response.RiskFlags, "alternative_solution_candidate") {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_risk_flag_invalid"}
	}
	telemetry := AdapterTelemetry{Adapter: response.Telemetry.Adapter, Provider: response.Telemetry.Provider, Deployment: response.Telemetry.Deployment, Region: response.Telemetry.Region, Attempts: response.Telemetry.Attempts, RepairAttempted: response.Telemetry.RepairAttempted, PriorErrorCodes: nonNilStrings(response.Telemetry.PriorErrorCodes), ElapsedMS: response.Telemetry.ElapsedMS, Usage: response.Telemetry.Usage}
	return AdapterOutput{SchemaVersion: response.SchemaVersion, RequestID: response.RequestID, Confidence: confidence, RiskFlags: nonNilStrings(response.RiskFlags), NeedsHumanReview: true,
		StudentFeedback: "数学评分建议须由教师确认。", TeacherNote: "模型只提供语义候选映射；分值由服务器依据冻结评分规则计算。",
		ModelVersion: response.ModelVersion, PromptVersion: response.PromptVersion, RubricVersion: response.RubricVersion, DeliveryMode: response.Delivery,
		CapabilityProfile: response.CapabilityProfile, Telemetry: telemetry, MathCandidates: candidates, AlternativeSolutionCandidate: response.AlternativeSolutionCandidate,
		RawOutput: map[string]any{"adapter": a.Name(), "schema_version": response.SchemaVersion, "criterion_candidates": candidates, "alternative_solution_candidate": response.AlternativeSolutionCandidate, "risk_flags": nonNilStrings(response.RiskFlags), "telemetry": telemetry}, Mock: false}, nil
}

var _ LLMGradingAdapter = (*HTTPAdapterV2)(nil)
var _ GovernedPolicyProvider = (*HTTPAdapterV2)(nil)
var _ RuntimeStatusProvider = (*HTTPAdapterV2)(nil)

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
