package subjective

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

const ImageProtocolVersion = "grading-image-v3"
const ImageObservationPromptVersion = "student-observation-v3-20261022-r4"
const ImageComparisonPromptVersion = "frozen-rubric-comparison-v3-20261021"

type ObservationMember struct {
	QuestionID   string `json:"question_id"`
	QuestionNo   string `json:"question_no"`
	QuestionText string `json:"question_text"`
	QuestionType string `json:"question_type"`
}

type ObservationInput struct {
	RequestID      string
	InputVersionID string
	SegmentID      string
	CropSHA256     string
	Members        []ObservationMember
}

type ObservedAnswer struct {
	Type  string   `json:"type"`
	Value string   `json:"value"`
	Parts []string `json:"parts"`
}

type ObservationEvidence struct {
	EvidenceID string    `json:"evidence_id"`
	Text       string    `json:"text"`
	BBox       []float64 `json:"bbox"`
}

type ObservedMember struct {
	Confidence  float64               `json:"confidence"`
	QuestionID  string                `json:"question_id"`
	Presence    string                `json:"presence"`
	Association string                `json:"association"`
	Answer      *ObservedAnswer       `json:"answer"`
	Evidence    []ObservationEvidence `json:"evidence"`
}

type StudentObservation struct {
	ModelBinding   string           `json:"model_binding"`
	SchemaVersion  string           `json:"schema_version"`
	InputVersionID string           `json:"input_version_id"`
	CropSHA256     string           `json:"crop_sha256"`
	PromptVersion  string           `json:"prompt_version"`
	Members        []ObservedMember `json:"members"`
}

type ImageCriterionJudgment struct {
	CriterionID string   `json:"criterion_id"`
	Status      string   `json:"status"`
	EvidenceIDs []string `json:"evidence_ids"`
	Reason      string   `json:"reason"`
}

type imageStudentMedia struct {
	Role      string `json:"role"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	ByteSize  int64  `json:"byte_size"`
	Data      string `json:"data"`
}

type imageAgentRequest struct {
	AgentRole                string                   `json:"agent_role,omitempty"`
	ObservationSourceBinding string                   `json:"observation_source_binding,omitempty"`
	ManagedModel             *ImageModelConfig        `json:"managed_model,omitempty"`
	SchemaVersion            string                   `json:"schema_version"`
	RequestID                string                   `json:"request_id"`
	InputVersionID           string                   `json:"input_version_id"`
	InputMode                string                   `json:"input_mode"`
	StudentImage             *imageStudentMedia       `json:"student_image,omitempty"`
	AnswerText               string                   `json:"answer_text"`
	Members                  []ObservationMember      `json:"members"`
	ModelPolicy              ModelPolicy              `json:"model_policy"`
	Rubric                   any                      `json:"rubric,omitempty"`
	FrozenReference          *GradingReferenceContext `json:"frozen_reference,omitempty"`
	Observation              *StudentObservation      `json:"observation,omitempty"`
}

type imageAgentResponse struct {
	SchemaVersion    string                   `json:"schema_version"`
	RequestID        string                   `json:"request_id"`
	Observation      StudentObservation       `json:"observation"`
	Judgments        []ImageCriterionJudgment `json:"judgments,omitempty"`
	StudentFeedback  string                   `json:"student_feedback,omitempty"`
	TeacherNote      string                   `json:"teacher_note,omitempty"`
	ModelVersion     string                   `json:"model_version,omitempty"`
	PromptVersion    string                   `json:"prompt_version,omitempty"`
	RubricVersion    string                   `json:"rubric_version,omitempty"`
	NeedsHumanReview bool                     `json:"needs_human_review,omitempty"`
	Telemetry        gradingAgentTelemetry    `json:"telemetry"`
}

func imageRequest(input AdapterInput) (imageAgentRequest, error) {
	if input.InputVersionID == "" || input.ActiveCrop == nil || input.ActiveCrop.SHA256 != input.CropSHA256 || len(input.ActiveCrop.Data) == 0 {
		return imageAgentRequest{}, ErrActiveCropUnavailable
	}
	crop := input.ActiveCrop
	if crop.MediaType != "image/png" || crop.ByteSize != int64(len(crop.Data)) || crop.ByteSize > maxActiveCropBytes || fmt.Sprintf("%x", sha256.Sum256(crop.Data)) != crop.SHA256 {
		return imageAgentRequest{}, ErrActiveCropUnavailable
	}
	if _, _, err := validateActiveCropPNG(crop.Data); err != nil {
		return imageAgentRequest{}, ErrActiveCropUnavailable
	}
	mode := "image"
	if strings.TrimSpace(input.AnswerText) != "" {
		mode = "image_text"
	}
	request := imageAgentRequest{AgentRole: input.AgentRole, ManagedModel: input.ImageModel, SchemaVersion: ImageProtocolVersion, RequestID: input.RequestID, InputVersionID: input.InputVersionID, InputMode: mode,
		StudentImage: &imageStudentMedia{Role: "student_answer", MediaType: crop.MediaType, SHA256: crop.SHA256, ByteSize: crop.ByteSize, Data: base64.StdEncoding.EncodeToString(crop.Data)},
		AnswerText:   input.AnswerText, Members: []ObservationMember{{QuestionID: input.Question.ID, QuestionNo: input.Question.QuestionNo, QuestionType: input.Question.QuestionType, QuestionText: input.Question.Stem}},
		ModelPolicy: input.ModelPolicy, Rubric: input.Rubric, FrozenReference: input.ReferenceContext, Observation: input.Observation}
	if input.Observation != nil {
		request.ObservationSourceBinding = input.Observation.ModelBinding
	}
	return request, nil
}

func (a *HTTPAdapter) imageRequest(ctx context.Context, path string, request imageAgentRequest) (imageAgentResponse, error) {
	if a.baseURL == "" || len(a.token) < 32 || a.policy.ModelVersion == "" {
		return imageAgentResponse{}, &GradingAgentError{Code: "visual_model_not_configured"}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return imageAgentResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return imageAgentResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Idempotency-Key", request.RequestID)
	response, err := a.client.Do(req)
	if err != nil {
		return imageAgentResponse{}, &GradingAgentError{Code: "adapter_transport_error", Retryable: false, cause: err}
	}
	defer response.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(http.MaxBytesReader(nil, response.Body, maxGradingAgentResponseBytes)); err != nil {
		return imageAgentResponse{}, &GradingAgentError{Code: "agent_response_too_large", cause: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return imageAgentResponse{}, parseAgentError(raw.Bytes(), response.StatusCode, ImageProtocolVersion, request.RequestID)
	}
	var result imageAgentResponse
	decoder := json.NewDecoder(&raw)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, &GradingAgentError{Code: "agent_response_invalid", cause: err}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return result, ErrInvalidModelOutput
	}
	if result.SchemaVersion != ImageProtocolVersion || result.RequestID != request.RequestID {
		return result, attachResponseDiagnostics(ErrInvalidModelOutput, result.Telemetry)
	}
	if result.Telemetry.Attempts < 1 || result.Telemetry.Attempts > 2 || result.Telemetry.Adapter != a.adapterType || result.Telemetry.Provider != a.providerKey || result.Telemetry.Deployment != a.deploymentKey || result.Telemetry.Region != a.deploymentRegion || validateSuccessDiagnostics(result.Telemetry) != nil {
		return result, attachResponseDiagnostics(ErrInvalidModelOutput, result.Telemetry)
	}
	if err := ValidateStudentObservation(result.Observation, request.InputVersionID, request.StudentImage.SHA256, request.Members); err != nil {
		return result, attachResponseDiagnostics(err, result.Telemetry)
	}
	expectedBinding := imageModelBinding(request.ManagedModel)
	if request.Observation != nil {
		expectedBinding = request.Observation.ModelBinding
	}
	if request.ManagedModel == nil || result.Observation.ModelBinding != expectedBinding {
		return result, attachResponseDiagnostics(ErrInvalidModelOutput, result.Telemetry)
	}
	return result, nil
}

// ValidateStudentObservation 按成员校验证据，不允许漏题、重复证据身份或用空字符串把不清晰当成空白。
// unresolved 是该成员的事实；调用方应继续保存同一整区其它 matched 成员。
func ValidateStudentObservation(observation StudentObservation, inputVersionID, cropHash string, members []ObservationMember) error {
	// r4 新观察增加区间端点辨认；历史观察仍按原版本读取，不能在评分时静默改写。
	validPrompt := observation.PromptVersion == ImageObservationPromptVersion || observation.PromptVersion == "student-observation-v3-20261021-r3" || observation.PromptVersion == "student-observation-v3-20261021-r2" || observation.PromptVersion == "student-observation-v3-20261021"
	if observation.SchemaVersion != ImageProtocolVersion || !validPrompt || observation.InputVersionID != inputVersionID || observation.CropSHA256 != cropHash || len(observation.Members) != len(members) {
		return ErrInvalidModelOutput
	}
	expected := map[string]bool{}
	seenEvidence := map[string]bool{}
	for _, member := range members {
		if member.QuestionID == "" || expected[member.QuestionID] {
			return ErrInvalidInput
		}
		expected[member.QuestionID] = true
	}
	for _, member := range observation.Members {
		if !expected[member.QuestionID] {
			return ErrInvalidModelOutput
		}
		delete(expected, member.QuestionID)
		if member.Association != "matched" && member.Association != "unresolved" {
			return ErrInvalidModelOutput
		}
		if math.IsNaN(member.Confidence) || math.IsInf(member.Confidence, 0) || member.Confidence < 0 || member.Confidence > 1 {
			return ErrInvalidModelOutput
		}
		switch member.Presence {
		case "present":
			hasAnswer := member.Answer != nil && strings.TrimSpace(member.Answer.Value) != ""
			if member.Answer != nil {
				for _, part := range member.Answer.Parts {
					hasAnswer = hasAnswer || strings.TrimSpace(part) != ""
				}
			}
			if member.Association != "matched" || member.Answer == nil || !hasAnswer || len(member.Evidence) == 0 {
				return ErrInvalidModelOutput
			}
			switch member.Answer.Type {
			case "text", "latex", "number", "interval", "parts":
			default:
				return ErrInvalidModelOutput
			}
		case "blank", "unreadable", "uncertain":
			if member.Answer != nil {
				return ErrInvalidModelOutput
			}
		default:
			return ErrInvalidModelOutput
		}
		if member.Association == "unresolved" && member.Presence != "uncertain" {
			return ErrInvalidModelOutput
		}
		if member.Presence == "blank" && len(member.Evidence) == 0 {
			return ErrInvalidModelOutput
		}
		for _, evidence := range member.Evidence {
			if evidence.EvidenceID == "" || seenEvidence[evidence.EvidenceID] || !validObservationBBox(evidence.BBox) {
				return ErrInvalidModelOutput
			}
			seenEvidence[evidence.EvidenceID] = true
		}
	}
	return nil
}

func validObservationBBox(bbox []float64) bool {
	if len(bbox) != 4 {
		return false
	}
	for _, value := range bbox {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	return bbox[2] > 0 && bbox[3] > 0 && bbox[0]+bbox[2] <= 1.000001 && bbox[1]+bbox[3] <= 1.000001
}

func (a *HTTPAdapter) gradeImage(ctx context.Context, input AdapterInput) (AdapterOutput, error) {
	request, err := imageRequest(input)
	if err != nil {
		return AdapterOutput{}, err
	}
	request.ModelPolicy = a.policy
	response, err := a.imageRequest(ctx, "/grading/grade-image-v3", request)
	if err != nil {
		return a.failureOutput(input.RequestID, err), err
	}
	if response.ModelVersion != a.policy.ModelVersion || response.PromptVersion != ImageComparisonPromptVersion || response.RubricVersion != input.Rubric.Version || !response.NeedsHumanReview {
		err := attachResponseDiagnostics(ErrInvalidModelOutput, response.Telemetry)
		return a.failureOutput(input.RequestID, err), err
	}
	output := AdapterOutput{SchemaVersion: ImageProtocolVersion, RequestID: input.RequestID, ModelVersion: response.ModelVersion, PromptVersion: input.ModelPolicy.PromptVersion,
		RubricVersion: response.RubricVersion, NeedsHumanReview: true, DeliveryMode: "teacher_suggestion", CapabilityProfile: a.capabilityProfile,
		StudentFeedback: response.StudentFeedback, TeacherNote: response.TeacherNote, Observation: &response.Observation, ImageJudgments: response.Judgments,
		Telemetry: AdapterTelemetry{Adapter: response.Telemetry.Adapter, Provider: response.Telemetry.Provider, Deployment: response.Telemetry.Deployment, Region: response.Telemetry.Region,
			Attempts: response.Telemetry.Attempts, ElapsedMS: response.Telemetry.ElapsedMS, Usage: response.Telemetry.Usage, ExecutionDiagnostics: response.Telemetry.ExecutionDiagnostics},
		RawOutput: map[string]any{"schema_version": ImageProtocolVersion, "input_version_id": input.InputVersionID, "observation_prompt_version": response.Observation.PromptVersion, "comparison_prompt_version": ImageComparisonPromptVersion, "observation": response.Observation, "judgments": response.Judgments}}
	if err := projectImageJudgments(&output, input); err != nil {
		err = attachResponseDiagnostics(err, response.Telemetry)
		return a.failureOutput(input.RequestID, err), err
	}
	return output, nil
}

// 分数只由冻结量规重建；比较模型没有评分字段，也不能把参考答案写成学生证据。
func projectImageJudgments(output *AdapterOutput, input AdapterInput) error {
	if len(input.Rubric.Deductions) > 0 {
		return &GradingAgentError{Code: "image_rubric_deductions_unsupported"}
	}
	points := map[string]float64{}
	labels := map[string]string{}
	var maximum float64
	for _, point := range input.Rubric.Points {
		if point.ID == "" || points[point.ID] != 0 || point.Score <= 0 || math.IsNaN(point.Score) || math.IsInf(point.Score, 0) {
			return ErrInvalidModelOutput
		}
		points[point.ID] = point.Score
		labels[point.ID] = point.Description
		maximum += point.Score
	}
	if len(points) == 0 || maximum > input.Question.Score+0.000001 {
		return ErrInvalidModelOutput
	}
	if len(output.ImageJudgments) != len(points) || output.Observation == nil {
		return ErrInvalidModelOutput
	}
	var member *ObservedMember
	for index := range output.Observation.Members {
		if output.Observation.Members[index].QuestionID == input.Question.ID {
			member = &output.Observation.Members[index]
		}
	}
	if member == nil {
		return ErrInvalidModelOutput
	}
	evidence := map[string]ObservationEvidence{}
	for _, item := range member.Evidence {
		evidence[item.EvidenceID] = item
	}
	output.MatchedPoints = nil
	output.MissingPoints = nil
	output.Evidence = nil
	output.Confidence = member.Confidence
	for _, judgment := range output.ImageJudgments {
		weight, ok := points[judgment.CriterionID]
		if !ok || strings.TrimSpace(judgment.Reason) == "" {
			return ErrInvalidModelOutput
		}
		delete(points, judgment.CriterionID)
		seen := map[string]bool{}
		for _, id := range judgment.EvidenceIDs {
			if _, ok := evidence[id]; !ok || seen[id] {
				return ErrInvalidModelOutput
			}
			seen[id] = true
		}
		switch judgment.Status {
		case "supported":
			if member.Presence != "present" || member.Association != "matched" || len(judgment.EvidenceIDs) == 0 {
				return ErrInvalidModelOutput
			}
			ids := []string{}
			for _, id := range judgment.EvidenceIDs {
				item, ok := evidence[id]
				if !ok {
					return ErrInvalidModelOutput
				}
				unique := judgment.CriterionID + ":" + id
				ids = append(ids, unique)
				output.Evidence = append(output.Evidence, grading.Evidence{Type: "observation", EvidenceID: unique, RubricPointID: judgment.CriterionID, AnswerSegment: input.SegmentID, AnswerText: item.Text, Location: "student_image:" + input.CropSHA256, BBox: item.BBox, Confidence: 0})
			}
			output.MatchedPoints = append(output.MatchedPoints, grading.PointResult{Code: judgment.CriterionID, Label: labels[judgment.CriterionID], Score: weight, Reason: judgment.Reason, EvidenceIDs: ids})
		case "contradicted", "uncertain":
			// 未得分项的 Score 表示该项可得分值；总分只累加 MatchedPoints，不能把它当已得分。
			output.MissingPoints = append(output.MissingPoints, grading.PointResult{Code: judgment.CriterionID, Label: labels[judgment.CriterionID], Score: weight, Reason: judgment.Status + ": " + judgment.Reason})
		default:
			return ErrInvalidModelOutput
		}
	}
	DeriveSuggestedScore(output)
	output.NeedsHumanReview = true
	output.RiskFlags = appendFlag(output.RiskFlags, "human_review_required")
	if member.Presence != "present" {
		output.RiskFlags = appendFlag(output.RiskFlags, "student_answer_"+member.Presence)
	}
	return nil
}

func imageOutputEvidenceIndex(output AdapterOutput, ctx Context) (map[string]gradingEvidenceLink, error) {
	if ctx.Observation == nil || output.Observation == nil {
		return nil, ErrInvalidModelOutput
	}
	if err := ValidateStudentObservation(*ctx.Observation, ctx.InputVersionID, ctx.CropSHA256, []ObservationMember{{QuestionID: ctx.Question.ID}}); err != nil {
		return nil, err
	}
	allowed := map[string]ObservationEvidence{}
	for _, member := range ctx.Observation.Members {
		for _, item := range member.Evidence {
			allowed[item.EvidenceID] = item
		}
	}
	result := map[string]gradingEvidenceLink{}
	for _, item := range output.Evidence {
		if item.Type != "observation" || item.Location != "student_image:"+ctx.CropSHA256 || item.AnswerSegment != ctx.SegmentID || result[item.EvidenceID].rubricPointID != "" {
			return nil, ErrInvalidModelOutput
		}
		prefix := item.RubricPointID + ":"
		if !strings.HasPrefix(item.EvidenceID, prefix) {
			return nil, ErrInvalidModelOutput
		}
		source, ok := allowed[strings.TrimPrefix(item.EvidenceID, prefix)]
		if !ok || source.Text != item.AnswerText || !sameImageBBox(source.BBox, item.BBox) {
			return nil, ErrInvalidModelOutput
		}
		result[item.EvidenceID] = gradingEvidenceLink{rubricPointID: item.RubricPointID}
	}
	return result, nil
}

func sameImageBBox(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
