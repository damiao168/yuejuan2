package capture

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
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
	Purpose         string     `json:"purpose"`
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
	UploadGroupID        string    `json:"upload_group_id,omitempty"`
	UploadGroupFileCount int       `json:"upload_group_file_count,omitempty"`
	UploadGroupFileIndex int       `json:"upload_group_file_index,omitempty"`
	PagesPerStudent      int       `json:"pages_per_student,omitempty"`
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	CaptureBatchID       string    `json:"capture_batch_id"`
	FileAssetID          string    `json:"file_asset_id"`
	OriginalName         string    `json:"original_name"`
	ContentType          string    `json:"content_type"`
	SHA256               string    `json:"sha256"`
	ByteSize             int64     `json:"byte_size"`
	PageCount            int       `json:"page_count"`
	Status               string    `json:"status"`
	ErrorCode            string    `json:"error_code,omitempty"`
	IdempotencyKey       string    `json:"idempotency_key"`
	UploadedBy           string    `json:"uploaded_by"`
	CreatedAt            time.Time `json:"created_at"`
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

type CreateBatchInput struct {
	Purpose        string `json:"purpose,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Name           string `json:"name"`
	SourceType     string `json:"source_type"`
	ScannerDevice  string `json:"scanner_device"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RegisterFileInput struct {
	// 同一次文件选择固定组号、文件总数和顺序；不同选择不能用余页拼成同一学生。
	UploadGroupID        string `json:"upload_group_id,omitempty"`
	UploadGroupFileCount int    `json:"upload_group_file_count,omitempty"`
	UploadGroupFileIndex int    `json:"upload_group_file_index,omitempty"`
	PagesPerStudent      int    `json:"pages_per_student,omitempty"`
	FileAssetID          string `json:"file_asset_id"`
	IdempotencyKey       string `json:"idempotency_key"`
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

// Store 组合本模块装配所需能力，保持现有构造入口兼容；新消费者应按业务职责依赖下方小接口。
// 接口拆分只说明依赖范围，不代替权限、版本校验，也不承诺跨能力调用自动进入同一事务。
type Store interface {
	StudentSheetStore
	BatchFileStore
	MatchingStore
	ProcessingStore
	RegistrationCorrectionStore
}

// StudentSheetStore 管理考试冻结名单对应的受控打印与纸张生命周期，不含扫描匹配或图像处理。
type StudentSheetStore interface {
	IssueTemplateBarcodes(ctx context.Context, tenantID, templateID string) (IssuedTemplateBarcodes, error)
	IssueStudentBarcodes(ctx context.Context, tenantID, templateID, actorID string, input IssueStudentBarcodesInput) (IssuedStudentBarcodes, error)
	GetStudentPrintContext(ctx context.Context, tenantID, templateID string) (StudentPrintContext, error)
	GetStudentPrintPackage(ctx context.Context, tenantID, printBatchID string) (StudentPrintPackage, error)
	RevokeStudentSheet(ctx context.Context, tenantID, sheetSerial, actorID string, input StudentSheetLifecycleInput) (StudentSheet, error)
	ReprintStudentSheet(ctx context.Context, tenantID, sheetSerial, actorID string, input ReprintStudentSheetInput) (IssuedStudentBarcodes, error)
}

// BatchFileStore 管理采集批次与源文件；命令恢复仍按考试、操作者和命令键检查。
type BatchFileStore interface {
	RecoverBatchCommand(context.Context, string, string, string, string) (BatchCommandRecovery, error)
	CreateBatch(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error)
	ListBatches(ctx context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error)
	GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error)
	RegisterFile(ctx context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error)
	GetFile(ctx context.Context, tenantID, fileID string) (File, error)
	ListFiles(ctx context.Context, tenantID, batchID string) ([]File, error)
	QueueBatch(ctx context.Context, tenantID, batchID, actorID string) (Batch, error)
	SetBatchStatus(ctx context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error)
}

// MatchingStore 管理页归属与答卷合并拆分；身份确认和页生命周期属于同一匹配领域。
type MatchingStore interface {
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
	UpdatePage(ctx context.Context, tenantID, pageID, actorID string, input UpdatePageInput) (Page, error)
}

// ProcessingStore 接收解码、质量、模板匹配及定位结果；租约终态事务仍由对应命令接口负责。
type ProcessingStore interface {
	ApplyFileResult(ctx context.Context, tenantID, fileID string, pages []DecodedPageInput) (File, error)
	ApplyFileFailure(ctx context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error)
	ApplyQualityOutcome(ctx context.Context, tenantID, submissionPageID, qualityStatus string) error
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
}

// RegistrationCorrectionStore 管理人工定位修正、预览及撤销，不能越过当前版本与审核约束。
type RegistrationCorrectionStore interface {
	CreateRegistrationCorrection(ctx context.Context, tenantID, runID, actorID string, input CreateRegistrationCorrectionInput) (RegistrationCorrection, error)
	GetRegistrationCorrection(ctx context.Context, tenantID, correctionID string) (RegistrationCorrection, error)
	QueueRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID, actorID string, revision int) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID string, input CorrectionPreviewResultInput) (RegistrationCorrection, error)
	ApplyRegistrationCorrectionFailure(ctx context.Context, tenantID, correctionID, errorCode string, detail map[string]any) (RegistrationCorrection, error)
	ApplyRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	UndoRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error)
	GetRegistrationCorrectionContext(ctx context.Context, tenantID, runID string) (RegistrationCorrectionContext, error)
}

// TransactionalTemplateMatchStore 由生产存储实现，使路由决策和工作器租约原子进入终态。
type TransactionalTemplateMatchStore interface {
	SubmitTemplateMatchResultCommand(context.Context, string, string, TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error)
	SubmitTemplateMatchFailureCommand(context.Context, string, string, TemplateMatchFailureInput) (TemplateMatchRun, string, error)
}

// FileResultCommandStore 将解码页面和当前租约回执放在同一提交边界；失败不能留下采集页面。
type FileResultCommandStore interface {
	SubmitFileResultCommand(context.Context, string, string, FileResultInput, workerruntime.Store, map[string]any) (File, error)
}

// FileFailureCommandStore 将失败租约和采集领域投影置于同一提交边界。
type FileFailureCommandStore interface {
	SubmitFileFailureCommand(context.Context, string, string, FileFailureInput, workerruntime.Store) (File, string, error)
}

// RegistrationResultCommandStore 把配准或修正预览与当前任务回执一起提交；成功回放只接受同一产物。
type RegistrationResultCommandStore interface {
	SubmitRegistrationResultCommand(context.Context, string, string, RegistrationResultInput, workerruntime.Store) (RegistrationRun, error)
	SubmitRegistrationCorrectionResultCommand(context.Context, string, string, CorrectionPreviewResultInput, workerruntime.Store) (RegistrationCorrection, error)
}

type FileAssetSnapshot struct {
	ID           string
	ExamID       string
	OriginalName string
	ContentType  string
	SizeBytes    int64
	SHA256       string
}
