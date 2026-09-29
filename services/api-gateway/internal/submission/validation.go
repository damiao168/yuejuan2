package submission

var validSourceTypes = map[string]bool{
	"scanner_upload": true,
	"image_upload":   true,
	"pdf_upload":     true,
	"mobile_capture": true,
	"lms_import":     true,
	"manual_import":  true,
}

func IsValidSourceType(sourceType string) bool {
	return validSourceTypes[sourceType]
}

// ready_for_ocr 只允许质量通过的答卷进入；已 ready 的答卷禁止再被普通流程改写。
func CanTransition(current string, next string, qualityStatus string) bool {
	if next == "rejected" {
		return current != "ready_for_ocr"
	}
	switch current {
	case "created":
		return next == "pages_uploaded"
	case "quality_checked":
		return next == "ready_for_ocr" && qualityStatus == "passed"
	default:
		return false
	}
}

func ValidateCreateInput(input CreateSubmissionInput) error {
	if !IsValidSourceType(input.SourceType) {
		return ErrInvalidInput
	}
	if input.ExpectedPageCount < 0 {
		return ErrInvalidInput
	}
	return nil
}

func ValidatePageInput(input AddPageInput) error {
	if input.FileAssetID == "" || input.PageNo <= 0 {
		return ErrInvalidInput
	}
	return nil
}
