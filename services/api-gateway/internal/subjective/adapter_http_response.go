package subjective

import (
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
)

// 先核对契约、交付方式和评分边界，再接受遥测与证据；长文本题仍只能进入影子评审。
func (a *HTTPAdapter) validateResponseContract(input AdapterInput, response gradingAgentResponse) error {
	if response.SchemaVersion != gradingAgentSchemaVersion ||
		response.RequestID != input.RequestID ||
		response.Status != "suggestion" ||
		!response.NeedsHumanReview ||
		response.Mock ||
		response.ModelVersion != a.policy.ModelVersion ||
		response.PromptVersion != a.policy.PromptVersion ||
		response.RubricVersion != input.Rubric.Version ||
		response.CapabilityProfile != a.capabilityProfile {
		return &GradingAgentError{Code: "agent_response_contract_mismatch"}
	}
	if response.Delivery != "teacher_suggestion" && response.Delivery != "shadow_only" {
		return &GradingAgentError{Code: "agent_delivery_invalid"}
	}
	if (input.Question.QuestionType == "essay" || input.Question.QuestionType == "discussion") && response.Delivery != "shadow_only" {
		return &GradingAgentError{Code: "agent_delivery_invalid"}
	}
	if len(response.Deductions) != 0 {
		return &GradingAgentError{Code: "agent_deductions_not_allowed"}
	}
	if response.SuggestedScore < 0 || response.SuggestedScore > input.Question.Score || !almostEqual(response.MaxScore, input.Question.Score) || response.Confidence != 0 {
		return &GradingAgentError{Code: "agent_score_invalid"}
	}
	return nil
}

func (a *HTTPAdapter) mapResponse(input AdapterInput, response gradingAgentResponse) (AdapterOutput, error) {
	if err := validateSuccessDiagnostics(response.Telemetry); err != nil {
		return AdapterOutput{}, err
	}
	if err := a.validateResponseContract(input, response); err != nil {
		return AdapterOutput{}, err
	}
	if response.Telemetry.Adapter != a.adapterType ||
		response.Telemetry.Provider != a.providerKey ||
		response.Telemetry.Deployment != a.deploymentKey ||
		response.Telemetry.Region != a.deploymentRegion ||
		response.Telemetry.Attempts < 1 ||
		response.Telemetry.Attempts > 2 ||
		response.Telemetry.ElapsedMS < 0 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_telemetry_invalid"}
	}
	allowedRisks := map[string]bool{
		"ocr_low_confidence": true, "ocr_text_empty_review_required": true, "ambiguous_answer": true,
		"insufficient_evidence": true, "possible_off_topic": true, "score_needs_review": true,
		"schema_repaired": true, "prompt_injection_suspected": true, "human_review_required": true,
	}
	for _, risk := range response.RiskFlags {
		if !allowedRisks[risk] {
			return AdapterOutput{}, &GradingAgentError{Code: "agent_risk_flag_invalid"}
		}
	}
	matched := make([]grading.PointResult, 0, len(response.MatchedPoints))
	for _, point := range response.MatchedPoints {
		matched = append(matched, grading.PointResult{Code: point.RubricPointID, Label: point.Label, Score: point.Score, EvidenceIDs: nonNilStrings(point.EvidenceIDs)})
	}
	missing := make([]grading.PointResult, 0, len(response.MissingPoints))
	for _, point := range response.MissingPoints {
		missing = append(missing, grading.PointResult{Code: point.RubricPointID, Label: point.Label, Reason: point.Reason})
	}
	evidence := make([]grading.Evidence, 0, len(response.Evidence))
	for _, item := range response.Evidence {
		evidence = append(evidence, grading.Evidence{
			Type:          "answer_text",
			EvidenceID:    item.EvidenceID,
			RubricPointID: item.RubricPointID,
			AnswerSegment: input.SegmentID,
			AnswerText:    item.TextExcerpt,
			Location:      item.Location,
			Confidence:    item.Confidence,
		})
	}
	telemetry := AdapterTelemetry{
		Adapter:              response.Telemetry.Adapter,
		Provider:             response.Telemetry.Provider,
		Deployment:           response.Telemetry.Deployment,
		Region:               response.Telemetry.Region,
		Attempts:             response.Telemetry.Attempts,
		RepairAttempted:      response.Telemetry.RepairAttempted,
		PriorErrorCodes:      nonNilStrings(response.Telemetry.PriorErrorCodes),
		ElapsedMS:            response.Telemetry.ElapsedMS,
		Usage:                response.Telemetry.Usage,
		ExecutionDiagnostics: cloneExecutionDiagnostics(response.Telemetry.ExecutionDiagnostics),
	}
	return AdapterOutput{
		RequestID:         response.RequestID,
		SuggestedScore:    response.SuggestedScore,
		Confidence:        response.Confidence,
		MatchedPoints:     matched,
		MissingPoints:     missing,
		Evidence:          evidence,
		RiskFlags:         nonNilStrings(response.RiskFlags),
		NeedsHumanReview:  true,
		StudentFeedback:   response.StudentFeedback,
		TeacherNote:       response.TeacherNote,
		ModelVersion:      response.ModelVersion,
		PromptVersion:     response.PromptVersion,
		RubricVersion:     response.RubricVersion,
		DeliveryMode:      response.Delivery,
		CapabilityProfile: response.CapabilityProfile,
		Telemetry:         telemetry,
		RawOutput: map[string]any{
			"adapter":            a.Name(),
			"schema_version":     response.SchemaVersion,
			"request_id":         response.RequestID,
			"delivery":           response.Delivery,
			"capability_profile": response.CapabilityProfile,
			"provider":           response.Telemetry.Provider,
			"deployment":         response.Telemetry.Deployment,
			"region":             response.Telemetry.Region,
			"telemetry":          telemetry,
		},
		Mock: false,
	}, nil
}

func (a *HTTPAdapter) failureOutput(requestID string, err error) AdapterOutput {
	code := "adapter_error"
	var agentErr *GradingAgentError
	if errors.As(err, &agentErr) && strings.TrimSpace(agentErr.Code) != "" {
		code = agentErr.Code
	}
	output := AdapterOutput{
		RequestID:        requestID,
		NeedsHumanReview: true,
		Telemetry: AdapterTelemetry{
			Adapter:         a.adapterType,
			Provider:        a.providerKey,
			Deployment:      a.deploymentKey,
			Region:          a.deploymentRegion,
			PriorErrorCodes: []string{},
		},
		RawOutput: map[string]any{
			"adapter":    a.adapterType,
			"provider":   a.providerKey,
			"deployment": a.deploymentKey,
			"region":     a.deploymentRegion,
			"request_id": requestID,
			"error_code": code,
		},
	}
	if agentErr != nil && agentErr.ExecutionDiagnostics != nil {
		output.Telemetry.ExecutionDiagnostics = cloneExecutionDiagnostics(agentErr.ExecutionDiagnostics)
		output.Telemetry.Attempts = agentErr.ExecutionDiagnostics.Attempts
		output.RawOutput["execution_diagnostics"] = output.Telemetry.ExecutionDiagnostics
	}
	return output
}

func agentErrorRetryable(err error) bool {
	var agentErr *GradingAgentError
	return errors.As(err, &agentErr) && agentErr.Retryable && agentErr.Code != "model_timeout" && diagnosticsAllowRetry(agentErr.ExecutionDiagnostics)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple json values")
		}
		return err
	}
	return nil
}

func almostEqual(left, right float64) bool {
	return math.Abs(left-right) <= 0.000001
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilAny(values []any) []any {
	if values == nil {
		return []any{}
	}
	return values
}
