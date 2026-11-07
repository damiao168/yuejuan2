package capture

import (
	"time"
)

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
	ID                    string               `json:"id,omitempty"`
	QuestionID            string               `json:"question_id,omitempty"`
	Label                 string               `json:"label,omitempty"`
	X                     float64              `json:"x"`
	Y                     float64              `json:"y"`
	Width                 float64              `json:"width"`
	Height                float64              `json:"height"`
	OptionRegions         []PrintPackageRegion `json:"option_regions,omitempty"`
	OptionCoordinateSpace string               `json:"option_coordinate_space,omitempty"`
}

type PrintPackagePage struct {
	PageNo            int                  `json:"page_no"`
	TemplateWidth     int                  `json:"template_width"`
	TemplateHeight    int                  `json:"template_height"`
	BarcodeValue      string               `json:"-"`
	QuestionRegions   []PrintPackageRegion `json:"question_regions"`
	RegistrationMarks []PrintPackageRegion `json:"registration_marks,omitempty"`
	IdentityRegions   []PrintPackageRegion `json:"identity_regions,omitempty"`
	GradingGroups     []PrintPackageRegion `json:"grading_groups,omitempty"`
	// 当前模板只声明像素宽高；未来接入可信物理尺寸时可显式传入，不能从像素冒充源文件DPI。
	PhysicalWidthMM  float64 `json:"-"`
	PhysicalHeightMM float64 `json:"-"`
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
