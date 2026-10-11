package ocr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const maxIdempotencyKeyLength = 200

// PrepareCreateInput 规范化 OCR 配置，并在调用方未提供键时补充稳定的键。
// 主动重新运行可使用新键；同一请求的传输重试复用原来源任务。
func PrepareCreateInput(submissionID string, input CreateTaskInput) (CreateTaskInput, error) {
	input = NormalizeCreateInput(input)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if err := ValidateCreateInput(input); err != nil {
		return CreateTaskInput{}, err
	}
	if input.IdempotencyKey == "" {
		raw, err := json.Marshal(struct {
			SubmissionID  string  `json:"submission_id"`
			Engine        string  `json:"engine"`
			EngineVersion string  `json:"engine_version"`
			MinConfidence float64 `json:"min_confidence"`
		}{
			SubmissionID: submissionID, Engine: input.Engine,
			EngineVersion: input.EngineVersion, MinConfidence: input.MinConfidence,
		})
		if err != nil {
			return CreateTaskInput{}, err
		}
		sum := sha256.Sum256(raw)
		input.IdempotencyKey = "ocr:" + hex.EncodeToString(sum[:])
	}
	if len(input.IdempotencyKey) > maxIdempotencyKeyLength {
		return CreateTaskInput{}, ErrInvalidInput
	}
	return input, nil
}

func sameCreateRequest(task Task, submissionID string, input CreateTaskInput) bool {
	return task.SubmissionID == submissionID && task.Engine == input.Engine &&
		task.EngineVersion == input.EngineVersion && task.MinConfidence == input.MinConfidence
}
