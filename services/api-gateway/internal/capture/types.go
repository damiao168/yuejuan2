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

// Batch 汇总采集文件、页面和身份处理状态；计数由存储层聚合，不应在接口层自行推算。
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

type BatchListFilter struct {
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
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

// Page 的 Revision 用于人工改页、删除和恢复的乐观并发校验；状态变化必须随版本一起保存。
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
	BarcodeStudentID   string         `json:"barcode_student_id,omitempty"`
	BarcodeTemplateID  string         `json:"barcode_template_id,omitempty"`
	SheetSerial        string         `json:"sheet_serial,omitempty"`
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

// BarcodeObservation 是解码器看到的原始条码，尚未通过签名、模板归属和打印台账校验。
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
	StudentIDs     []string `json:"student_ids"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type IssuedStudentBarcodeSet struct {
	StudentID   string              `json:"student_id"`
	SheetSerial string              `json:"sheet_serial"`
	Pages       []IssuedPageBarcode `json:"pages"`
}

type IssuedStudentBarcodes struct {
	PrintBatchID        string                    `json:"print_batch_id"`
	TemplateID          string                    `json:"template_id"`
	TemplateContentHash string                    `json:"template_content_hash"`
	KeyID               string                    `json:"kid"`
	Students            []IssuedStudentBarcodeSet `json:"students"`
	IssuedAt            time.Time                 `json:"issued_at"`
}

type PrintPackageRegion struct {
	QuestionID string  `json:"question_id,omitempty"`
	Label      string  `json:"label,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
}

type PrintPackagePage struct {
	PageNo          int                  `json:"page_no"`
	TemplateWidth   int                  `json:"template_width"`
	TemplateHeight  int                  `json:"template_height"`
	BarcodeValue    string               `json:"-"`
	QuestionRegions []PrintPackageRegion `json:"question_regions"`
}

type PrintPackageSheet struct {
	StudentID   string             `json:"student_id"`
	SheetSerial string             `json:"sheet_serial"`
	Ordinal     int                `json:"ordinal"`
	Pages       []PrintPackagePage `json:"pages"`
}

type StudentPrintPackage struct {
	PrintBatchID        string              `json:"print_batch_id"`
	ExamID              string              `json:"exam_id"`
	ExamName            string              `json:"exam_name"`
	TemplateID          string              `json:"template_id"`
	TemplateContentHash string              `json:"template_content_hash"`
	KeyID               string              `json:"kid"`
	IssuedAt            time.Time           `json:"issued_at"`
	Sheets              []PrintPackageSheet `json:"sheets"`
}

type StudentPrintCandidate struct {
	StudentID          string `json:"student_id"`
	StudentNo          string `json:"student_no"`
	StudentName        string `json:"student_name"`
	ClassID            string `json:"class_id"`
	ClassName          string `json:"class_name"`
	AttendanceStatus   string `json:"attendance_status"`
	HasActiveSheet     bool   `json:"has_active_sheet"`
	ActiveSheetSerial  string `json:"active_sheet_serial,omitempty"`
	ActiveSheetStatus  string `json:"active_sheet_status,omitempty"`
	ActivePrintBatchID string `json:"active_print_batch_id,omitempty"`
}

type StudentPrintBatchSummary struct {
	PrintBatchID  string    `json:"print_batch_id"`
	Operation     string    `json:"operation"`
	Reason        string    `json:"reason,omitempty"`
	SheetCount    int       `json:"sheet_count"`
	PageCount     int       `json:"page_count"`
	IssuedCount   int       `json:"issued_count"`
	ObservedCount int       `json:"observed_count"`
	ConflictCount int       `json:"conflict_count"`
	RevokedCount  int       `json:"revoked_count"`
	Downloadable  bool      `json:"downloadable"`
	IssuedAt      time.Time `json:"issued_at"`
}

type StudentPrintContext struct {
	ExamID              string                     `json:"exam_id"`
	TemplateID          string                     `json:"template_id"`
	TemplateContentHash string                     `json:"template_content_hash"`
	Candidates          []StudentPrintCandidate    `json:"candidates"`
	Batches             []StudentPrintBatchSummary `json:"batches"`
}

type StudentSheetLifecycleInput struct {
	Reason string `json:"reason"`
}

type ReprintStudentSheetInput struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type StudentSheet struct {
	SheetSerial         string     `json:"sheet_serial"`
	PrintBatchID        string     `json:"print_batch_id"`
	ExamID              string     `json:"exam_id"`
	TemplateID          string     `json:"template_id"`
	TemplateContentHash string     `json:"template_content_hash"`
	StudentID           string     `json:"student_id"`
	Status              string     `json:"status"`
	SupersedesSheetID   string     `json:"supersedes_sheet_id,omitempty"`
	RevokedBy           string     `json:"revoked_by,omitempty"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	RevokeReason        string     `json:"revoke_reason,omitempty"`
	FirstObservedAt     *time.Time `json:"first_observed_at,omitempty"`
	LastObservedAt      *time.Time `json:"last_observed_at,omitempty"`
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
	Batch               Batch               `json:"batch"`
	Files               []File              `json:"files"`
	Pages               []Page              `json:"pages"`
	ProcessingSummaries []ProcessingSummary `json:"processing_summaries"`
}

type Store interface {
	RecoverBatchCommand(context.Context, string, string, string, string) (BatchCommandRecovery, error)
	IssueTemplateBarcodes(ctx context.Context, tenantID, templateID string) (IssuedTemplateBarcodes, error)
	IssueStudentBarcodes(ctx context.Context, tenantID, templateID, actorID string, input IssueStudentBarcodesInput) (IssuedStudentBarcodes, error)
	GetStudentPrintContext(ctx context.Context, tenantID, templateID string) (StudentPrintContext, error)
	GetStudentPrintPackage(ctx context.Context, tenantID, printBatchID string) (StudentPrintPackage, error)
	RevokeStudentSheet(ctx context.Context, tenantID, sheetSerial, actorID string, input StudentSheetLifecycleInput) (StudentSheet, error)
	ReprintStudentSheet(ctx context.Context, tenantID, sheetSerial, actorID string, input ReprintStudentSheetInput) (IssuedStudentBarcodes, error)
	CreateBatch(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error)
	ListBatches(ctx context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error)
	GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error)
	RegisterFile(ctx context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error)
	GetFile(ctx context.Context, tenantID, fileID string) (File, error)
	ListFiles(ctx context.Context, tenantID, batchID string) ([]File, error)
	ListPages(ctx context.Context, tenantID, batchID string) ([]Page, error)
	GetPageBySubmissionPageID(ctx context.Context, tenantID, submissionPageID string) (Page, error)
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
	GetTemplateMatchRun(ctx context.Context, tenantID, runID string) (TemplateMatchRun, error)
	ApplyTemplateMatchResult(ctx context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error)
	ApplyTemplateMatchFailure(ctx context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (TemplateMatchRun, error)
	ApplyRegistrationResult(ctx context.Context, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error)
	ApplyRegistrationFailure(ctx context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (RegistrationRun, error)
	ConfirmRegistration(ctx context.Context, tenantID, runID, actorID, reason string) (RegistrationRun, error)
	PrepareRegistrationRetry(ctx context.Context, tenantID, runID string) (RegistrationRun, error)
	GetProcessingSummary(ctx context.Context, tenantID, submissionID string) (ProcessingSummary, error)
	ListProcessingSummaries(ctx context.Context, tenantID, batchID string) ([]ProcessingSummary, error)
	CreateRegistrationCorrection(ctx context.Context, tenantID, runID, actorID string, input CreateRegistrationCorrectionInput) (RegistrationCorrection, error)
	GetRegistrationCorrection(ctx context.Context, tenantID, correctionID string) (RegistrationCorrection, error)
	QueueRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID, actorID string, revision int) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID string, input CorrectionPreviewResultInput) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionFailure(ctx context.Context, tenantID, correctionID, errorCode string, detail map[string]any) (RegistrationCorrection, error)
	ApplyRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	UndoRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	GetRegistrationCorrectionContext(ctx context.Context, tenantID, runID string) (RegistrationCorrectionContext, error)
}

// TransactionalTemplateMatchStore is implemented by the production store so
// the routing decision and worker lease reach a terminal state atomically.
type TransactionalTemplateMatchStore interface {
	SubmitTemplateMatchResultCommand(context.Context, string, string, TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error)
	SubmitTemplateMatchFailureCommand(context.Context, string, string, TemplateMatchFailureInput) (TemplateMatchRun, string, error)
}

type FileAssetSnapshot struct {
	ID           string
	ExamID       string
	OriginalName string
	ContentType  string
	SizeBytes    int64
	SHA256       string
}
