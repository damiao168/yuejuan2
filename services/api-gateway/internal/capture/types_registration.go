package capture

import (
	"time"
)

// RegistrationRun 保存一次页面配准的输入版本、算法结果和人工确认状态，旧运行不会覆盖新版本。
type RegistrationRun struct {
	ID                    string         `json:"id"`
	CapturePageID         string         `json:"capture_page_id"`
	SubmissionPageID      string         `json:"submission_page_id"`
	SourceFileAssetID     string         `json:"source_file_asset_id"`
	TemplateID            string         `json:"template_id"`
	TemplateContentHash   string         `json:"template_content_hash"`
	RoutingMode           string         `json:"routing_mode"`
	GuardReport           map[string]any `json:"guard_report"`
	PageNo                int            `json:"page_no"`
	ProcessingStatus      string         `json:"processing_status"`
	MatchStatus           string         `json:"match_status,omitempty"`
	Confidence            float64        `json:"confidence,omitempty"`
	Method                string         `json:"method,omitempty"`
	ProfileVersion        string         `json:"profile_version"`
	SourceToTemplate      []any          `json:"source_to_template_matrix"`
	TemplateToSource      []any          `json:"template_to_source_matrix"`
	FeatureCount          int            `json:"feature_count"`
	MatchCount            int            `json:"match_count"`
	InlierCount           int            `json:"inlier_count"`
	InlierRatio           float64        `json:"inlier_ratio,omitempty"`
	ReprojectionError     float64        `json:"reprojection_error,omitempty"`
	RegisteredFileAssetID string         `json:"registered_file_asset_id,omitempty"`
	RuntimeTaskID         string         `json:"runtime_task_id,omitempty"`
	ErrorCode             string         `json:"error_code,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
}

type TemplateMatchRun struct {
	ID                          string           `json:"id"`
	ExamID                      string           `json:"exam_id"`
	CapturePageID               string           `json:"capture_page_id"`
	SubmissionPageID            string           `json:"submission_page_id"`
	SourceFileAssetID           string           `json:"source_file_asset_id"`
	SourceSHA256                string           `json:"source_sha256"`
	SourcePageRevision          int              `json:"source_page_revision"`
	ProcessingStatus            string           `json:"processing_status"`
	Decision                    string           `json:"decision,omitempty"`
	SelectedTemplateID          string           `json:"selected_template_id,omitempty"`
	SelectedTemplateContentHash string           `json:"selected_template_content_hash,omitempty"`
	Score                       float64          `json:"score,omitempty"`
	Margin                      float64          `json:"margin,omitempty"`
	Candidates                  []map[string]any `json:"candidates"`
	ProfileVersion              string           `json:"profile_version"`
	RuntimeTaskID               string           `json:"runtime_task_id,omitempty"`
	ErrorCode                   string           `json:"error_code,omitempty"`
	CreatedBy                   string           `json:"created_by"`
	CreatedAt                   time.Time        `json:"created_at"`
}

type TemplateMatchResultInput struct {
	TaskID                      string           `json:"task_id"`
	LeaseToken                  string           `json:"lease_token"`
	ResultVersion               string           `json:"result_version"`
	DurationMS                  int              `json:"duration_ms"`
	Decision                    string           `json:"decision"`
	SelectedTemplateID          string           `json:"selected_template_id"`
	SelectedTemplateContentHash string           `json:"selected_template_content_hash"`
	Score                       float64          `json:"score"`
	Margin                      float64          `json:"margin"`
	Candidates                  []map[string]any `json:"candidates"`
}

type TemplateMatchFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

// 切片同时携带归一化框和像素框；两者都来自同一张已校验的配准结果，哈希用于核对产物。
type SegmentCropInput struct {
	QuestionID     string         `json:"question_id"`
	Label          string         `json:"label"`
	NormalizedBBox map[string]any `json:"normalized_bbox"`
	PixelBBox      map[string]any `json:"pixel_bbox"`
	FileAssetID    string         `json:"file_asset_id"`
	SHA256         string         `json:"sha256"`
}

type RegistrationResultInput struct {
	TaskID                string             `json:"task_id"`
	LeaseToken            string             `json:"lease_token"`
	ResultVersion         string             `json:"result_version"`
	DurationMS            int                `json:"duration_ms"`
	RegisteredFileAssetID string             `json:"registered_file_asset_id"`
	RegisteredSHA256      string             `json:"registered_sha256"`
	Method                string             `json:"method"`
	Confidence            float64            `json:"confidence"`
	SourceToTemplate      []any              `json:"source_to_template_matrix"`
	TemplateToSource      []any              `json:"template_to_source_matrix"`
	FeatureCount          int                `json:"feature_count"`
	MatchCount            int                `json:"match_count"`
	InlierCount           int                `json:"inlier_count"`
	InlierRatio           float64            `json:"inlier_ratio"`
	ReprojectionError     float64            `json:"reprojection_error"`
	Coverage              float64            `json:"coverage"`
	GuardReport           map[string]any     `json:"guard_report"`
	Segments              []SegmentCropInput `json:"segments"`
}

type RegistrationFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type RegistrationDecisionInput struct {
	Reason string `json:"reason"`
}
