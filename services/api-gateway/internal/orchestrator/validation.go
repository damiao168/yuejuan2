package orchestrator

import "strings"

var validWorkflowTypes = map[string]bool{
	"ocr_pipeline":          true,
	"segmentation_pipeline": true,
	"grading_pipeline":      true,
	"review_pipeline":       true,
	"custom":                true,
}

var validTargetTypes = map[string]bool{
	"exam":           true,
	"submission":     true,
	"answer_segment": true,
	"ocr_task":       true,
}

var validAgentTypes = map[string]bool{
	"ocr_agent":                true,
	"layout_agent":             true,
	"segmentation_agent":       true,
	"objective_grading_agent":  true,
	"fill_blank_agent":         true,
	"subjective_grading_agent": true,
	"essay_grading_agent":      true,
	"evidence_check_agent":     true,
	"consistency_check_agent":  true,
	"anomaly_detection_agent":  true,
	"fairness_check_agent":     true,
	"analytics_agent":          true,
	"audit_agent":              true,
}

func NormalizeRunInput(input CreateRunInput) CreateRunInput {
	input.WorkflowType = strings.TrimSpace(input.WorkflowType)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.TargetID = strings.TrimSpace(input.TargetID)
	return input
}

func ValidateRunInput(input CreateRunInput) error {
	if !validWorkflowTypes[input.WorkflowType] || !validTargetTypes[input.TargetType] || input.TargetID == "" {
		return ErrInvalidInput
	}
	return nil
}

func NormalizeTaskInput(input CreateTaskInput) CreateTaskInput {
	input.AgentType = strings.TrimSpace(input.AgentType)
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 3
	}
	if input.InputRef == nil {
		input.InputRef = map[string]any{}
	}
	return input
}

func ValidateTaskInput(input CreateTaskInput) error {
	if !validAgentTypes[input.AgentType] || input.MaxAttempts <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func CanStart(status string) bool {
	return status == "queued"
}

func CanComplete(status string) bool {
	return status == "running"
}

func CanFail(status string) bool {
	return status == "queued" || status == "running"
}

// 只有失败任务且尚未达到最大尝试次数才能重试，避免无限重跑同一输入。
func CanRetry(status string, attemptNo int, maxAttempts int) bool {
	return status == "failed" && attemptNo < maxAttempts
}
