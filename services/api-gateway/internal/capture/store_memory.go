package capture

import (
	"context"
	"sync"
)

// MemoryStore 只模拟采集状态流转；条码打印、配准校正等需要真实台账的能力明确返回不支持。
type MemoryStore struct {
	mu              sync.RWMutex
	batches         map[string]Batch
	batchKeys       map[string]string
	files           map[string]File
	pages           map[string]Page
	registrations   map[string]RegistrationRun
	templateMatches map[string]TemplateMatchRun
	identities      map[string]MatchingSubmission
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{batches: map[string]Batch{}, batchKeys: map[string]string{}, files: map[string]File{}, pages: map[string]Page{}, registrations: map[string]RegistrationRun{}, templateMatches: map[string]TemplateMatchRun{}, identities: map[string]MatchingSubmission{}}
}

func (s *MemoryStore) IssueTemplateBarcodes(context.Context, string, string) (IssuedTemplateBarcodes, error) {
	return IssuedTemplateBarcodes{}, ErrInvalidTransition
}

func (s *MemoryStore) IssueStudentBarcodes(context.Context, string, string, string, IssueStudentBarcodesInput) (IssuedStudentBarcodes, error) {
	return IssuedStudentBarcodes{}, ErrInvalidTransition
}

func (s *MemoryStore) GetStudentPrintContext(context.Context, string, string) (StudentPrintContext, error) {
	return StudentPrintContext{}, ErrNotFound
}

func (s *MemoryStore) GetStudentPrintSource(context.Context, string, string) (StudentPrintSourceContent, error) {
	return StudentPrintSourceContent{}, ErrNotFound
}

func (s *MemoryStore) GetStudentPrintPackage(context.Context, string, string) (StudentPrintPackage, error) {
	return StudentPrintPackage{}, ErrNotFound
}

func (s *MemoryStore) ConfirmStudentPrintLayout(context.Context, string, string, StudentPrintLayout) (StudentPrintLayout, error) {
	return StudentPrintLayout{}, ErrInvalidTransition
}

func (s *MemoryStore) RevokeStudentSheet(context.Context, string, string, string, StudentSheetLifecycleInput) (StudentSheet, error) {
	return StudentSheet{}, ErrInvalidTransition
}

func (s *MemoryStore) ReprintStudentSheet(context.Context, string, string, string, ReprintStudentSheetInput) (IssuedStudentBarcodes, error) {
	return IssuedStudentBarcodes{}, ErrInvalidTransition
}

func (s *MemoryStore) CreateRegistrationCorrection(context.Context, string, string, string, CreateRegistrationCorrectionInput) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) GetRegistrationCorrection(context.Context, string, string) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrNotFound
}

func (s *MemoryStore) QueueRegistrationCorrectionPreview(context.Context, string, string, string, int) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) ApplyRegistrationCorrectionPreview(context.Context, string, string, CorrectionPreviewResultInput) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) ApplyRegistrationCorrectionFailure(context.Context, string, string, string, map[string]any) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) ApplyRegistrationCorrection(context.Context, string, string, string, RegistrationCorrectionDecisionInput) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) UndoRegistrationCorrection(context.Context, string, string, string, RegistrationCorrectionDecisionInput) (RegistrationCorrection, error) {
	return RegistrationCorrection{}, ErrInvalidTransition
}

func (s *MemoryStore) GetRegistrationCorrectionContext(context.Context, string, string) (RegistrationCorrectionContext, error) {
	return RegistrationCorrectionContext{}, ErrNotFound
}
