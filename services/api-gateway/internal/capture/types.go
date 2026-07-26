package capture

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("capture resource not found")
	ErrInvalidInput      = errors.New("invalid capture input")
	ErrInvalidTransition = errors.New("invalid capture transition")
	ErrConflict          = errors.New("capture revision conflict")
	ErrDuplicateFile     = errors.New("capture file already registered")
)

type Batch struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	ExamID          string     `json:"exam_id"`
	Name            string     `json:"name"`
	SourceType      string     `json:"source_type"`
	Status          string     `json:"status"`
	Revision        int        `json:"revision"`
	OperatorID      string     `json:"operator_id"`
	ScannerDevice   string     `json:"scanner_device,omitempty"`
	FileCount       int        `json:"file_count"`
	PageCount       int        `json:"page_count"`
	SubmissionCount int        `json:"submission_count"`
	NormalCount     int        `json:"normal_count"`
	ReviewCount     int        `json:"review_count"`
	FailedCount     int        `json:"failed_count"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type File struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	CaptureBatchID string    `json:"capture_batch_id"`
	FileAssetID    string    `json:"file_asset_id"`
	OriginalName   string    `json:"original_name"`
	ContentType    string    `json:"content_type"`
	SHA256         string    `json:"sha256"`
	ByteSize       int64     `json:"byte_size"`
	PageCount      int       `json:"page_count"`
	Status         string    `json:"status"`
	ErrorCode      string    `json:"error_code,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	UploadedBy     string    `json:"uploaded_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type Page struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	CaptureBatchID     string         `json:"capture_batch_id"`
	CaptureFileID      string         `json:"capture_file_id"`
	SourceIndex        int            `json:"source_index"`
	SubmissionID       string         `json:"submission_id,omitempty"`
	SubmissionPageID   string         `json:"submission_page_id,omitempty"`
	AssignedPageNo     int            `json:"assigned_page_no,omitempty"`
	SequenceNo         int            `json:"sequence_no"`
	RotationDegrees    int            `json:"rotation_degrees"`
	DecodedFileAssetID string         `json:"decoded_file_asset_id"`
	Status             string         `json:"status"`
	DuplicateOfPageID  string         `json:"duplicate_of_page_id,omitempty"`
	Revision           int            `json:"revision"`
	PageIdentity       map[string]any `json:"page_identity"`
	MatchCandidates    []any          `json:"match_candidates"`
	ManualOverride     map[string]any `json:"manual_override"`
	CreatedAt          time.Time      `json:"created_at"`
}

type StudentCandidate struct {
	ID        string `json:"id"`
	StudentNo string `json:"student_no"`
	Name      string `json:"name"`
	ClassID   string `json:"class_id"`
	ClassName string `json:"class_name"`
}

type MatchingSubmission struct {
	ID               string         `json:"id"`
	StudentID        string         `json:"student_id,omitempty"`
	CandidateNo      string         `json:"candidate_no,omitempty"`
	IdentityStatus   string         `json:"identity_status"`
	IdentityRevision int            `json:"identity_revision"`
	IdentityEvidence map[string]any `json:"identity_evidence"`
	Pages            []Page         `json:"pages"`
}

type MatchingQueue struct {
	BatchID     string               `json:"batch_id"`
	ExamID      string               `json:"exam_id"`
	Submissions []MatchingSubmission `json:"submissions"`
	Candidates  []StudentCandidate   `json:"candidates"`
}

type ConfirmStudentMatchInput struct {
	StudentID string `json:"student_id"`
	Revision  int    `json:"revision"`
	Reason    string `json:"reason"`
}

type MarkStudentUnknownInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

type ConfirmPageMatchInput struct {
	Revision int    `json:"revision"`
	PageNo   int    `json:"page_no"`
	Reason   string `json:"reason"`
}

type PageLifecycleInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}
type SplitSubmissionInput struct {
	SubmissionID string   `json:"submission_id"`
	PageIDs      []string `json:"page_ids"`
	Reason       string   `json:"reason"`
}
type MergeSubmissionsInput struct {
	TargetSubmissionID string `json:"target_submission_id"`
	SourceSubmissionID string `json:"source_submission_id"`
	Reason             string `json:"reason"`
}

type CreateBatchInput struct {
	Name           string `json:"name"`
	SourceType     string `json:"source_type"`
	ScannerDevice  string `json:"scanner_device"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RegisterFileInput struct {
	FileAssetID    string `json:"file_asset_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type UpdatePageInput struct {
	Revision        int  `json:"revision"`
	RotationDegrees *int `json:"rotation_degrees,omitempty"`
	SequenceNo      *int `json:"sequence_no,omitempty"`
}

type DecodedPageInput struct {
	SourceIndex int                  `json:"source_index"`
	FileAssetID string               `json:"file_asset_id"`
	SHA256      string               `json:"sha256"`
	Width       int                  `json:"width"`
	Height      int                  `json:"height"`
	Barcodes    []BarcodeObservation `json:"barcodes,omitempty"`
}

type BarcodeObservation struct {
	Format      string           `json:"format"`
	Text        string           `json:"text"`
	Polygon     []map[string]int `json:"polygon"`
	Orientation int              `json:"orientation"`
}

type IssuedPageBarcode struct {
	PageNo int    `json:"page_no"`
	Value  string `json:"value"`
}

type IssuedTemplateBarcodes struct {
	TemplateID          string              `json:"template_id"`
	TemplateContentHash string              `json:"template_content_hash"`
	KeyID               string              `json:"kid"`
	Pages               []IssuedPageBarcode `json:"pages"`
}

type IssueStudentBarcodesInput struct {
	StudentIDs []string `json:"student_ids"`
}

type IssuedStudentBarcodeSet struct {
	StudentID   string              `json:"student_id"`
	SheetSerial string              `json:"sheet_serial"`
	Pages       []IssuedPageBarcode `json:"pages"`
}

type IssuedStudentBarcodes struct {
	TemplateID          string                    `json:"template_id"`
	TemplateContentHash string                    `json:"template_content_hash"`
	KeyID               string                    `json:"kid"`
	Students            []IssuedStudentBarcodeSet `json:"students"`
}

type FileResultInput struct {
	TaskID              string             `json:"task_id"`
	LeaseToken          string             `json:"lease_token"`
	ResultVersion       string             `json:"result_version"`
	DurationMS          int                `json:"duration_ms"`
	DecoderProfile      string             `json:"decoder_profile"`
	Pages               []DecodedPageInput `json:"pages"`
	OriginalPageCount   int                `json:"original_page_count"`
	DetectedContentType string             `json:"detected_content_type"`
}

type FileFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type RegistrationRun struct {
	ID                    string    `json:"id"`
	CapturePageID         string    `json:"capture_page_id"`
	SubmissionPageID      string    `json:"submission_page_id"`
	SourceFileAssetID     string    `json:"source_file_asset_id"`
	TemplateID            string    `json:"template_id"`
	TemplateContentHash   string    `json:"template_content_hash"`
	PageNo                int       `json:"page_no"`
	ProcessingStatus      string    `json:"processing_status"`
	MatchStatus           string    `json:"match_status,omitempty"`
	Confidence            float64   `json:"confidence,omitempty"`
	Method                string    `json:"method,omitempty"`
	ProfileVersion        string    `json:"profile_version"`
	SourceToTemplate      []any     `json:"source_to_template_matrix"`
	TemplateToSource      []any     `json:"template_to_source_matrix"`
	FeatureCount          int       `json:"feature_count"`
	MatchCount            int       `json:"match_count"`
	InlierCount           int       `json:"inlier_count"`
	InlierRatio           float64   `json:"inlier_ratio,omitempty"`
	ReprojectionError     float64   `json:"reprojection_error,omitempty"`
	RegisteredFileAssetID string    `json:"registered_file_asset_id,omitempty"`
	RuntimeTaskID         string    `json:"runtime_task_id,omitempty"`
	ErrorCode             string    `json:"error_code,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
}

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
type ProcessingSummary struct {
	SubmissionID string              `json:"submission_id"`
	TotalPages   int                 `json:"total_pages"`
	ReadyPages   int                 `json:"ready_pages"`
	BlockedPages int                 `json:"blocked_pages"`
	PendingPages int                 `json:"pending_pages"`
	CanComplete  bool                `json:"can_complete"`
	Blockers     []ProcessingBlocker `json:"blockers"`
}
type ProcessingBlocker struct {
	PageID            string `json:"page_id"`
	PageNo            int    `json:"page_no"`
	RegistrationRunID string `json:"registration_run_id,omitempty"`
	Stage             string `json:"stage"`
	Code              string `json:"code"`
	Action            string `json:"action"`
}

type BatchDetail struct {
	Batch Batch  `json:"batch"`
	Files []File `json:"files"`
	Pages []Page `json:"pages"`
}

type Store interface {
	IssueTemplateBarcodes(ctx context.Context, tenantID, templateID string) (IssuedTemplateBarcodes, error)
	IssueStudentBarcodes(ctx context.Context, tenantID, templateID string, input IssueStudentBarcodesInput) (IssuedStudentBarcodes, error)
	CreateBatch(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error)
	ListBatches(ctx context.Context, tenantID, examID string) ([]Batch, error)
	GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error)
	RegisterFile(ctx context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error)
	GetFile(ctx context.Context, tenantID, fileID string) (File, error)
	ListFiles(ctx context.Context, tenantID, batchID string) ([]File, error)
	ListPages(ctx context.Context, tenantID, batchID string) ([]Page, error)
	GetMatchingQueue(ctx context.Context, tenantID, batchID string) (MatchingQueue, error)
	ConfirmStudentMatch(ctx context.Context, tenantID, submissionID, actorID string, input ConfirmStudentMatchInput) (MatchingSubmission, error)
	MarkStudentUnknown(ctx context.Context, tenantID, submissionID, actorID string, input MarkStudentUnknownInput) (MatchingSubmission, error)
	ConfirmPageMatch(ctx context.Context, tenantID, pageID, actorID string, input ConfirmPageMatchInput) (Page, error)
	DeletePage(ctx context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error)
	RestorePage(ctx context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error)
	SplitSubmission(ctx context.Context, tenantID, batchID, actorID string, input SplitSubmissionInput) (MatchingQueue, error)
	MergeSubmissions(ctx context.Context, tenantID, batchID, actorID string, input MergeSubmissionsInput) (MatchingQueue, error)
	QueueBatch(ctx context.Context, tenantID, batchID, actorID string) (Batch, error)
	ApplyFileResult(ctx context.Context, tenantID, fileID string, pages []DecodedPageInput) (File, error)
	ApplyFileFailure(ctx context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error)
	ApplyQualityOutcome(ctx context.Context, tenantID, submissionPageID, qualityStatus string) error
	UpdatePage(ctx context.Context, tenantID, pageID, actorID string, input UpdatePageInput) (Page, error)
	SetBatchStatus(ctx context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error)
	QueueSubmissionPages(ctx context.Context, tenantID, submissionID, actorID string) ([]RegistrationRun, error)
	GetRegistrationRun(ctx context.Context, tenantID, runID string) (RegistrationRun, error)
	ListRegistrationRuns(ctx context.Context, tenantID, submissionPageID string) ([]RegistrationRun, error)
	ApplyRegistrationResult(ctx context.Context, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error)
	ApplyRegistrationFailure(ctx context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (RegistrationRun, error)
	ConfirmRegistration(ctx context.Context, tenantID, runID, actorID, reason string) (RegistrationRun, error)
	PrepareRegistrationRetry(ctx context.Context, tenantID, runID string) (RegistrationRun, error)
	GetProcessingSummary(ctx context.Context, tenantID, submissionID string) (ProcessingSummary, error)
	CreateRegistrationCorrection(ctx context.Context, tenantID, runID, actorID string, input CreateRegistrationCorrectionInput) (RegistrationCorrection, error)
	GetRegistrationCorrection(ctx context.Context, tenantID, correctionID string) (RegistrationCorrection, error)
	QueueRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID, actorID string, revision int) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID string, input CorrectionPreviewResultInput) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionFailure(ctx context.Context, tenantID, correctionID, errorCode string, detail map[string]any) (RegistrationCorrection, error)
	ApplyRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	UndoRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	GetRegistrationCorrectionContext(ctx context.Context, tenantID, runID string) (RegistrationCorrectionContext, error)
}

type FileAssetSnapshot struct {
	ID           string
	ExamID       string
	OriginalName string
	ContentType  string
	SizeBytes    int64
	SHA256       string
}
