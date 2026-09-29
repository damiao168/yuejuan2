package ocr

import (
	"math"
	"strings"
)

func NormalizeCreateInput(input CreateTaskInput) CreateTaskInput {
	input.Engine = strings.TrimSpace(input.Engine)
	input.EngineVersion = strings.TrimSpace(input.EngineVersion)
	if input.Engine == "" {
		input.Engine = "external_ocr"
	}
	if input.EngineVersion == "" {
		input.EngineVersion = "unversioned"
	}
	if input.MinConfidence == 0 {
		input.MinConfidence = 0.8
	}
	return input
}

func ValidateCreateInput(input CreateTaskInput) error {
	if input.Engine == "" || input.EngineVersion == "" {
		return ErrInvalidInput
	}
	if math.IsNaN(input.MinConfidence) || math.IsInf(input.MinConfidence, 0) || input.MinConfidence < 0 || input.MinConfidence > 1 {
		return ErrInvalidInput
	}
	return nil
}

// BBox 使用 x、y、宽、高表示；坐标不能为负、宽高必须大于零，这里不判断是否越出原图。
func ValidateResultInput(input ResultInput) error {
	if strings.TrimSpace(input.SubmissionPageID) == "" || strings.TrimSpace(input.Text) == "" {
		return ErrInvalidInput
	}
	if len(input.BBox) != 4 {
		return ErrInvalidInput
	}
	for _, value := range input.BBox {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrInvalidInput
		}
	}
	if input.BBox[0] < 0 || input.BBox[1] < 0 || input.BBox[2] <= 0 || input.BBox[3] <= 0 {
		return ErrInvalidInput
	}
	if math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) || input.Confidence < 0 || input.Confidence > 1 {
		return ErrInvalidInput
	}
	return nil
}

func CanStart(status string) bool {
	return status == "queued" || status == "failed"
}

// IsStarted reports the sole state in which a repeated start request is an
// idempotent acknowledgement rather than a state transition.
func IsStarted(status string) bool {
	return status == "processing"
}

func CanComplete(status string) bool {
	return status == "processing"
}

func CanFail(status string) bool {
	return status == "queued" || status == "processing"
}
