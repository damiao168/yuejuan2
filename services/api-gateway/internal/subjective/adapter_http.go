package subjective

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
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
	Code      string
	Status    int
	Retryable bool
	cause     error
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

func (a *HTTPAdapter) Grade(ctx context.Context, input AdapterInput) (result AdapterOutput, gradeErr error) {
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

func (a *HTTPAdapter) buildRequest(requestID string, input AdapterInput) (gradingAgentRequest, error) {
	if a.baseURL == "" || len(a.token) < 32 || a.policy.ModelVersion == "" || a.policy.PromptVersion == "" {
		return gradingAgentRequest{}, &GradingAgentError{Code: "adapter_not_configured"}
	}
	if strings.TrimSpace(input.SegmentID) == "" || strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.GradeLevel) == "" || strings.TrimSpace(input.Question.ID) == "" || strings.TrimSpace(input.Question.Stem) == "" || strings.TrimSpace(input.AnswerText) == "" {
		return gradingAgentRequest{}, &GradingAgentError{Code: "grading_context_incomplete"}
	}
	if strings.TrimSpace(input.Rubric.ID) == "" || strings.TrimSpace(input.Rubric.Version) == "" || len(input.Rubric.Points) == 0 {
		return gradingAgentRequest{}, &GradingAgentError{Code: "rubric_context_incomplete"}
	}
	ocrConfidence := 0.0
	if input.OCRConfidence != nil {
		ocrConfidence = *input.OCRConfidence
	}
	points := make([]gradingAgentRubricPoint, 0, len(input.Rubric.Points))
	for _, point := range input.Rubric.Points {
		points = append(points, gradingAgentRubricPoint{
			ID:               point.ID,
			Description:      point.Description,
			Score:            point.Score,
			Required:         point.Required,
			Aliases:          []string{},
			EvidenceRequired: true,
			MatchPolicy:      "semantic",
		})
	}
	return gradingAgentRequest{
		SchemaVersion:   gradingAgentSchemaVersion,
		RequestID:       requestID,
		Subject:         gradingAgentSubject(input.Subject),
		GradeLevel:      gradingAgentGradeLevel(input.GradeLevel),
		AgentRole:       gradingAgentContractRole(input.AgentRole),
		QuestionID:      input.Question.ID,
		AnswerSegmentID: input.SegmentID,
		QuestionType:    input.Question.QuestionType,
		QuestionText:    input.Question.Stem,
		MaxScore:        input.Question.Score,
		AnswerText:      input.AnswerText,
		OCRConfidence:   ocrConfidence,
		RubricVersion:   input.Rubric.Version,
		PromptVersion:   a.policy.PromptVersion,
		Rubric: gradingAgentRubric{
			RubricID:          input.Rubric.ID,
			RubricVersion:     input.Rubric.Version,
			QuestionType:      input.Question.QuestionType,
			MaxScore:          input.Rubric.MaxScore,
			AllowPartialTotal: false,
			Points:            points,
			Deductions:        nonNilAny(input.Rubric.Deductions),
			EquivalentAnswers: []any{},
			Examples:          nonNilAny(input.Rubric.Examples),
			ScoringNotes:      []string{},
		},
		ModelPolicy: gradingAgentModelPolicy{
			Mode:          "shadow",
			ModelVersion:  a.policy.ModelVersion,
			MinConfidence: a.policy.MinConfidence,
		},
		PromptGuard: gradingAgentPromptGuard{
			StudentAnswerIsUntrusted: true,
			SuspectedInjection:       input.PromptGuard.SuspectedInjection,
			Signals:                  nonNilStrings(input.PromptGuard.Signals),
		},
		OutputConstraint: gradingAgentOutputConstraint{
			CriteriaEvidenceOnly: input.OutputConstraint.CriteriaEvidenceOnly,
			AllowModelFinalScore: false,
			FinalScoreAuthority:  input.OutputConstraint.FinalScoreAuthority,
		},
	}, nil
}

// The grading-agent protocol uses the same canonical vocabulary as the
// assessment domain. Legacy aliases are accepted only at this adapter boundary
// so old callers cannot leak non-canonical values into the agent contract.
func gradingAgentSubject(subject string) string {
	switch strings.ToLower(strings.TrimSpace(subject)) {
	case "math":
		return "mathematics"
	case "politics", "civics":
		return "ethics_politics"
	default:
		return strings.ToLower(strings.TrimSpace(subject))
	}
}

func gradingAgentGradeLevel(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "junior_middle":
		return "junior"
	case "senior_middle", "high_school":
		return "senior"
	default:
		return strings.ToLower(strings.TrimSpace(stage))
	}
}

func gradingAgentContractRole(role string) string {
	switch strings.TrimSpace(role) {
	case AgentRolePrimaryA, AgentRolePrimaryB:
		return "primary"
	case AgentRoleArbiter:
		return "arbiter"
	default:
		return "single"
	}
}

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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var remote gradingAgentErrorResponse
		_ = json.Unmarshal(responseBody, &remote)
		code := strings.TrimSpace(remote.Error.Code)
		if code == "" {
			code = "agent_http_error"
		}
		return gradingAgentResponse{}, &GradingAgentError{
			Code:      code,
			Status:    resp.StatusCode,
			Retryable: remote.Error.Retryable || resp.StatusCode >= http.StatusInternalServerError,
		}
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

func (a *HTTPAdapter) mapResponse(input AdapterInput, response gradingAgentResponse) (AdapterOutput, error) {
	if response.SchemaVersion != gradingAgentSchemaVersion ||
		response.RequestID != input.RequestID ||
		response.Status != "suggestion" ||
		!response.NeedsHumanReview ||
		response.Mock ||
		response.ModelVersion != a.policy.ModelVersion ||
		response.PromptVersion != a.policy.PromptVersion ||
		response.RubricVersion != input.Rubric.Version ||
		response.CapabilityProfile != a.capabilityProfile {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_response_contract_mismatch"}
	}
	if response.Delivery != "teacher_suggestion" && response.Delivery != "shadow_only" {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_delivery_invalid"}
	}
	if (input.Question.QuestionType == "essay" || input.Question.QuestionType == "discussion") && response.Delivery != "shadow_only" {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_delivery_invalid"}
	}
	if len(response.Deductions) != 0 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_deductions_not_allowed"}
	}
	if response.SuggestedScore < 0 || response.SuggestedScore > input.Question.Score || !almostEqual(response.MaxScore, input.Question.Score) || response.Confidence != 0 {
		return AdapterOutput{}, &GradingAgentError{Code: "agent_score_invalid"}
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
		Adapter:         response.Telemetry.Adapter,
		Provider:        response.Telemetry.Provider,
		Deployment:      response.Telemetry.Deployment,
		Region:          response.Telemetry.Region,
		Attempts:        response.Telemetry.Attempts,
		RepairAttempted: response.Telemetry.RepairAttempted,
		PriorErrorCodes: nonNilStrings(response.Telemetry.PriorErrorCodes),
		ElapsedMS:       response.Telemetry.ElapsedMS,
		Usage:           response.Telemetry.Usage,
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

func newAdapterRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("sg-%d", time.Now().UTC().UnixNano())
	}
	return "sg-" + hex.EncodeToString(raw)
}

func (a *HTTPAdapter) failureOutput(requestID string, err error) AdapterOutput {
	code := "adapter_error"
	var agentErr *GradingAgentError
	if errors.As(err, &agentErr) && strings.TrimSpace(agentErr.Code) != "" {
		code = agentErr.Code
	}
	return AdapterOutput{
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
}

func agentErrorRetryable(err error) bool {
	var agentErr *GradingAgentError
	return errors.As(err, &agentErr) && agentErr.Retryable && agentErr.Code != "model_timeout"
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

type gradingAgentRequest struct {
	SchemaVersion    string                       `json:"schema_version"`
	RequestID        string                       `json:"request_id"`
	Subject          string                       `json:"subject"`
	GradeLevel       string                       `json:"grade_level"`
	AgentRole        string                       `json:"agent_role"`
	QuestionID       string                       `json:"question_id"`
	AnswerSegmentID  string                       `json:"answer_segment_id"`
	QuestionType     string                       `json:"question_type"`
	QuestionText     string                       `json:"question_text"`
	MaxScore         float64                      `json:"max_score"`
	AnswerText       string                       `json:"answer_text"`
	OCRConfidence    float64                      `json:"ocr_confidence"`
	RubricVersion    string                       `json:"rubric_version"`
	PromptVersion    string                       `json:"prompt_version"`
	Rubric           gradingAgentRubric           `json:"rubric"`
	ModelPolicy      gradingAgentModelPolicy      `json:"model_policy"`
	PromptGuard      gradingAgentPromptGuard      `json:"prompt_guard"`
	OutputConstraint gradingAgentOutputConstraint `json:"output_constraint"`
}

type gradingAgentRubric struct {
	RubricID          string                    `json:"rubric_id"`
	RubricVersion     string                    `json:"rubric_version"`
	QuestionType      string                    `json:"question_type"`
	MaxScore          float64                   `json:"max_score"`
	AllowPartialTotal bool                      `json:"allow_partial_total"`
	Points            []gradingAgentRubricPoint `json:"points"`
	Deductions        []any                     `json:"deductions"`
	EquivalentAnswers []any                     `json:"equivalent_answers"`
	Examples          []any                     `json:"examples"`
	ScoringNotes      []string                  `json:"scoring_notes"`
}

type gradingAgentRubricPoint struct {
	ID               string   `json:"id"`
	Description      string   `json:"description"`
	Score            float64  `json:"score"`
	Required         bool     `json:"required"`
	Aliases          []string `json:"aliases"`
	EvidenceRequired bool     `json:"evidence_required"`
	MatchPolicy      string   `json:"match_policy"`
}

type gradingAgentModelPolicy struct {
	Mode          string  `json:"mode"`
	ModelVersion  string  `json:"model_version"`
	MinConfidence float64 `json:"min_confidence"`
}

type gradingAgentPromptGuard struct {
	StudentAnswerIsUntrusted bool     `json:"student_answer_is_untrusted"`
	SuspectedInjection       bool     `json:"suspected_injection"`
	Signals                  []string `json:"signals"`
}

type gradingAgentOutputConstraint struct {
	CriteriaEvidenceOnly bool   `json:"criteria_evidence_only"`
	AllowModelFinalScore bool   `json:"allow_model_final_score"`
	FinalScoreAuthority  string `json:"final_score_authority"`
}

type gradingAgentResponse struct {
	SchemaVersion     string                     `json:"schema_version"`
	RequestID         string                     `json:"request_id"`
	Status            string                     `json:"status"`
	Delivery          string                     `json:"delivery"`
	SuggestedScore    float64                    `json:"suggested_score"`
	MaxScore          float64                    `json:"max_score"`
	Confidence        float64                    `json:"confidence"`
	MatchedPoints     []gradingAgentMatchedPoint `json:"matched_points"`
	MissingPoints     []gradingAgentMissingPoint `json:"missing_points"`
	Deductions        []any                      `json:"deductions"`
	Evidence          []gradingAgentEvidence     `json:"evidence"`
	RiskFlags         []string                   `json:"risk_flags"`
	NeedsHumanReview  bool                       `json:"needs_human_review"`
	StudentFeedback   string                     `json:"student_feedback"`
	TeacherNote       string                     `json:"teacher_note"`
	ModelVersion      string                     `json:"model_version"`
	PromptVersion     string                     `json:"prompt_version"`
	RubricVersion     string                     `json:"rubric_version"`
	CapabilityProfile string                     `json:"capability_profile"`
	Mock              bool                       `json:"mock"`
	Telemetry         gradingAgentTelemetry      `json:"telemetry"`
}

type gradingAgentMatchedPoint struct {
	RubricPointID string   `json:"rubric_point_id"`
	Label         string   `json:"label"`
	Score         float64  `json:"score"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type gradingAgentMissingPoint struct {
	RubricPointID string `json:"rubric_point_id"`
	Label         string `json:"label"`
	Reason        string `json:"reason"`
}

type gradingAgentEvidence struct {
	EvidenceID    string  `json:"evidence_id"`
	RubricPointID string  `json:"rubric_point_id"`
	TextExcerpt   string  `json:"text_excerpt"`
	Location      string  `json:"location"`
	Confidence    float64 `json:"confidence"`
}

type gradingAgentTelemetry struct {
	Adapter         string          `json:"adapter"`
	Provider        string          `json:"provider"`
	Deployment      string          `json:"deployment"`
	Region          string          `json:"region"`
	Attempts        int             `json:"attempts"`
	RepairAttempted bool            `json:"repair_attempted"`
	PriorErrorCodes []string        `json:"prior_error_codes"`
	ElapsedMS       int64           `json:"elapsed_ms"`
	Usage           ModelTokenUsage `json:"usage"`
}

func firstConfigured(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

type gradingAgentErrorResponse struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Error         struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}
