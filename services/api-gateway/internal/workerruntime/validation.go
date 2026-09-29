package workerruntime

import "strings"

var validTaskTypes = map[string]bool{
	"ocr": true, "layout": true, "preprocess": true, "image_quality": true,
	"ai_grade": true, "evidence_verify": true, "report_generate": true,
	"export": true, "desktop_sync": true, "omr_extract": true,
	"paper_parse": true, "paper_formula": true,
}

func normalizeCreateInput(input CreateTaskInput) CreateTaskInput {
	input.TaskType = strings.TrimSpace(input.TaskType)
	input.QueueName = strings.TrimSpace(input.QueueName)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.PayloadSchemaVersion = strings.TrimSpace(input.PayloadSchemaVersion)
	if input.Priority == 0 {
		input.Priority = 100
	}
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 3
	}
	if input.RetryBackoffSeconds == 0 {
		input.RetryBackoffSeconds = 60
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	return input
}

func validateCreateInput(input CreateTaskInput) error {
	if !validTaskTypes[input.TaskType] || input.QueueName == "" || input.SourceType == "" || input.SourceID == "" || input.IdempotencyKey == "" || input.PayloadSchemaVersion == "" {
		return ErrInvalidInput
	}
	if input.MaxAttempts < 1 || input.MaxAttempts > 100 || input.RetryBackoffSeconds < 1 || input.RetryBackoffSeconds > 86400 {
		return ErrInvalidInput
	}
	if containsSensitivePayloadKey(input.Payload) {
		return ErrInvalidInput
	}
	return nil
}

func normalizeClaimInput(input ClaimInput) ClaimInput {
	input.QueueName = strings.TrimSpace(input.QueueName)
	input.WorkerService = strings.TrimSpace(input.WorkerService)
	input.WorkerInstanceID = strings.TrimSpace(input.WorkerInstanceID)
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 1
	}
	if input.LeaseSeconds < 30 || input.LeaseSeconds > 3600 {
		input.LeaseSeconds = 300
	}
	return input
}

func validateClaimInput(input ClaimInput) error {
	if input.QueueName == "" || input.WorkerService == "" || input.WorkerInstanceID == "" {
		return ErrInvalidInput
	}
	return nil
}

func normalizeHeartbeatInput(input HeartbeatInput) HeartbeatInput {
	input.WorkerService = strings.TrimSpace(input.WorkerService)
	input.WorkerInstanceID = strings.TrimSpace(input.WorkerInstanceID)
	if input.LeaseSeconds < 30 || input.LeaseSeconds > 3600 {
		input.LeaseSeconds = 300
	}
	return input
}

// 队列只传资源引用，正文和凭据应通过受控接口读取；嵌套对象也要检查，不能只过滤顶层。
// 这里只识别明确列出的字段名，并不替代调用方对其他敏感内容的审核。
func containsSensitivePayloadKey(value any) bool {
	sensitive := map[string]bool{
		"student_id": true, "candidate_no": true, "student_answer": true,
		"ocr_text": true, "password": true, "access_token": true, "secret": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if sensitive[strings.ToLower(strings.TrimSpace(key))] || containsSensitivePayloadKey(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsSensitivePayloadKey(item) {
				return true
			}
		}
	}
	return false
}
