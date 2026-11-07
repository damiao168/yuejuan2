package capture

import (
	"time"
)

// NormalizedPoint 以图像左上角为原点，X、Y 分别按宽、高归一到 0 到 1。
type NormalizedPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type CreateRegistrationCorrectionInput struct {
	PageRevision       int               `json:"page_revision"`
	SourcePoints       []NormalizedPoint `json:"source_points"`
	TemplatePoints     []NormalizedPoint `json:"template_points"`
	AdvancedAnchorMode bool              `json:"advanced_anchor_mode"`
}

type RegistrationCorrection struct {
	ID                           string             `json:"id"`
	CapturePageID                string             `json:"capture_page_id"`
	BaseRegistrationRunID        string             `json:"base_registration_run_id"`
	AppliedRegistrationRunID     string             `json:"applied_registration_run_id,omitempty"`
	SourcePageRevision           int                `json:"source_page_revision"`
	TemplateID                   string             `json:"template_id"`
	TemplateContentHash          string             `json:"template_content_hash"`
	PageNo                       int                `json:"page_no"`
	SourcePoints                 []NormalizedPoint  `json:"source_points"`
	TemplatePoints               []NormalizedPoint  `json:"template_points"`
	AdvancedAnchorMode           bool               `json:"advanced_anchor_mode"`
	Status                       string             `json:"status"`
	Revision                     int                `json:"revision"`
	AttemptCount                 int                `json:"attempt_count"`
	PreviewRegisteredFileAssetID string             `json:"preview_registered_file_asset_id,omitempty"`
	PreviewSegments              []SegmentCropInput `json:"preview_segments"`
	SourceToTemplate             []any              `json:"source_to_template_matrix"`
	TemplateToSource             []any              `json:"template_to_source_matrix"`
	Coverage                     float64            `json:"coverage,omitempty"`
	ReprojectionError            float64            `json:"reprojection_error,omitempty"`
	ValidationReport             map[string]any     `json:"validation_report"`
	PreviousRegistrationSnapshot map[string]any     `json:"previous_registration_snapshot,omitempty"`
	RuntimeTaskID                string             `json:"runtime_task_id,omitempty"`
	ErrorCode                    string             `json:"error_code,omitempty"`
	ExpiresAt                    time.Time          `json:"expires_at"`
	AppliedAt                    *time.Time         `json:"applied_at,omitempty"`
	UndoneAt                     *time.Time         `json:"undone_at,omitempty"`
	CreatedAt                    time.Time          `json:"created_at"`
}

type RegistrationCorrectionDecisionInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

type RegistrationCorrectionContext struct {
	RegistrationRunID   string `json:"registration_run_id"`
	CapturePageID       string `json:"capture_page_id"`
	ExamID              string `json:"exam_id"`
	PageRevision        int    `json:"page_revision"`
	PageNo              int    `json:"page_no"`
	SourceFileAssetID   string `json:"source_file_asset_id"`
	TemplateFileAssetID string `json:"template_file_asset_id"`
	TemplateContentType string `json:"template_content_type"`
	TemplateWidth       int    `json:"template_width"`
	TemplateHeight      int    `json:"template_height"`
}

type CorrectionPreviewResultInput struct {
	TaskID                       string             `json:"task_id"`
	LeaseToken                   string             `json:"lease_token"`
	ResultVersion                string             `json:"result_version"`
	DurationMS                   int                `json:"duration_ms"`
	PreviewRegisteredFileAssetID string             `json:"preview_registered_file_asset_id"`
	PreviewRegisteredSHA256      string             `json:"preview_registered_sha256"`
	SourceToTemplate             []any              `json:"source_to_template_matrix"`
	TemplateToSource             []any              `json:"template_to_source_matrix"`
	Coverage                     float64            `json:"coverage"`
	ReprojectionError            float64            `json:"reprojection_error"`
	ValidationReport             map[string]any     `json:"validation_report"`
	Segments                     []SegmentCropInput `json:"segments"`
}

type CorrectionPreviewFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}
