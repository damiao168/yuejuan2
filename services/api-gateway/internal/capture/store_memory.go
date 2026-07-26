package capture

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryStore struct {
	mu            sync.RWMutex
	batches       map[string]Batch
	batchKeys     map[string]string
	files         map[string]File
	pages         map[string]Page
	registrations map[string]RegistrationRun
	identities    map[string]MatchingSubmission
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{batches: map[string]Batch{}, batchKeys: map[string]string{}, files: map[string]File{}, pages: map[string]Page{}, registrations: map[string]RegistrationRun{}, identities: map[string]MatchingSubmission{}}
}

func (s *MemoryStore) IssueTemplateBarcodes(context.Context, string, string) (IssuedTemplateBarcodes, error) {
	return IssuedTemplateBarcodes{}, ErrInvalidTransition
}

func (s *MemoryStore) IssueStudentBarcodes(context.Context, string, string, IssueStudentBarcodesInput) (IssuedStudentBarcodes, error) {
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

func (s *MemoryStore) GetMatchingQueue(_ context.Context, tenantID, batchID string) (MatchingQueue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return MatchingQueue{}, ErrNotFound
	}
	queue := MatchingQueue{BatchID: batchID, ExamID: batch.ExamID, Candidates: []StudentCandidate{}, Submissions: []MatchingSubmission{}}
	for _, identity := range s.identities {
		pages := []Page{}
		for _, page := range s.pages {
			if page.TenantID == tenantID && page.CaptureBatchID == batchID && page.SubmissionID == identity.ID {
				pages = append(pages, page)
			}
		}
		if len(pages) > 0 {
			identity.Pages = pages
			queue.Submissions = append(queue.Submissions, identity)
		}
	}
	return queue, nil
}

func (s *MemoryStore) ConfirmStudentMatch(_ context.Context, tenantID, submissionID, actorID string, input ConfirmStudentMatchInput) (MatchingSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Revision <= 0 || input.StudentID == "" {
		return MatchingSubmission{}, ErrInvalidInput
	}
	x, ok := s.identities[submissionID]
	if !ok {
		x = MatchingSubmission{ID: submissionID, IdentityStatus: "unassigned", IdentityRevision: 1, IdentityEvidence: map[string]any{}}
	}
	if x.IdentityRevision != input.Revision {
		return MatchingSubmission{}, ErrConflict
	}
	x.StudentID = input.StudentID
	x.IdentityStatus = "matched"
	x.IdentityRevision++
	x.IdentityEvidence = map[string]any{"method": "manual_confirmation", "actor_id": actorID, "reason": input.Reason}
	s.identities[submissionID] = x
	return x, nil
}

func (s *MemoryStore) MarkStudentUnknown(_ context.Context, tenantID, submissionID, actorID string, input MarkStudentUnknownInput) (MatchingSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return MatchingSubmission{}, ErrInvalidInput
	}
	x, ok := s.identities[submissionID]
	if !ok {
		x = MatchingSubmission{ID: submissionID, IdentityStatus: "unassigned", IdentityRevision: 1}
	}
	if x.IdentityRevision != input.Revision {
		return MatchingSubmission{}, ErrConflict
	}
	x.StudentID = ""
	x.CandidateNo = ""
	x.IdentityStatus = "unknown"
	x.IdentityRevision++
	x.IdentityEvidence = map[string]any{"method": "manual_unknown", "actor_id": actorID, "reason": input.Reason}
	s.identities[submissionID] = x
	return x, nil
}

func (s *MemoryStore) ConfirmPageMatch(_ context.Context, tenantID, pageID, actorID string, input ConfirmPageMatchInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision <= 0 || input.PageNo <= 0 {
		return Page{}, ErrInvalidInput
	}
	if x.Revision != input.Revision {
		return Page{}, ErrConflict
	}
	x.AssignedPageNo = input.PageNo
	x.Revision++
	x.ManualOverride = map[string]any{"page_no": input.PageNo, "actor_id": actorID, "reason": input.Reason}
	s.pages[pageID] = x
	return x, nil
}

func (s *MemoryStore) DeletePage(_ context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision != x.Revision || strings.TrimSpace(input.Reason) == "" {
		return Page{}, ErrConflict
	}
	x.Status = "deleted"
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}
func (s *MemoryStore) RestorePage(_ context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision != x.Revision || x.Status != "deleted" {
		return Page{}, ErrConflict
	}
	x.Status = "needs_review"
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}
func (s *MemoryStore) SplitSubmission(context.Context, string, string, string, SplitSubmissionInput) (MatchingQueue, error) {
	return MatchingQueue{}, ErrInvalidTransition
}
func (s *MemoryStore) MergeSubmissions(context.Context, string, string, string, MergeSubmissionsInput) (MatchingQueue, error) {
	return MatchingQueue{}, ErrInvalidTransition
}
func (s *MemoryStore) ConfirmRegistration(_ context.Context, tenantID, runID, actorID, reason string) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	if x.MatchStatus != "needs_review" || strings.TrimSpace(reason) == "" {
		return RegistrationRun{}, ErrInvalidTransition
	}
	x.MatchStatus = "matched"
	s.registrations[runID] = x
	return x, nil
}
func (s *MemoryStore) PrepareRegistrationRetry(_ context.Context, tenantID, runID string) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	x.ProcessingStatus = "processing"
	x.ErrorCode = ""
	s.registrations[runID] = x
	return x, nil
}
func (s *MemoryStore) GetProcessingSummary(_ context.Context, tenantID, submissionID string) (ProcessingSummary, error) {
	return ProcessingSummary{SubmissionID: submissionID, Blockers: []ProcessingBlocker{}}, nil
}

func (s *MemoryStore) QueueSubmissionPages(_ context.Context, tenantID, submissionID, actorID string) ([]RegistrationRun, error) {
	return nil, ErrInvalidTransition
}

func (s *MemoryStore) GetRegistrationRun(_ context.Context, tenantID, runID string) (RegistrationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) ListRegistrationRuns(_ context.Context, tenantID, submissionPageID string) ([]RegistrationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []RegistrationRun{}
	for _, item := range s.registrations {
		if item.SubmissionPageID == submissionPageID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) ApplyRegistrationResult(_ context.Context, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	item.ProcessingStatus = "completed"
	item.MatchStatus = "matched"
	item.Confidence = input.Confidence
	item.Method = input.Method
	item.RegisteredFileAssetID = input.RegisteredFileAssetID
	s.registrations[runID] = item
	return item, nil
}

func (s *MemoryStore) ApplyRegistrationFailure(_ context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	item.ProcessingStatus = "terminal_error"
	if retryable {
		item.ProcessingStatus = "retryable_error"
	}
	item.ErrorCode = errorCode
	s.registrations[runID] = item
	return item, nil
}

func (s *MemoryStore) CreateBatch(_ context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error) {
	if validateCreateBatch(&input) != nil || tenantID == "" || examID == "" || actorID == "" {
		return Batch{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + examID + ":" + input.IdempotencyKey
	if input.IdempotencyKey != "" {
		if id, ok := s.batchKeys[key]; ok {
			return s.batches[id], nil
		}
	}
	now := time.Now().UTC()
	item := Batch{ID: uuid.NewString(), TenantID: tenantID, ExamID: examID, Name: input.Name, SourceType: input.SourceType, Status: "draft", Revision: 1, OperatorID: actorID, ScannerDevice: input.ScannerDevice, CreatedAt: now}
	s.batches[item.ID] = item
	if input.IdempotencyKey != "" {
		s.batchKeys[key] = item.ID
	}
	return item, nil
}

func (s *MemoryStore) ListBatches(_ context.Context, tenantID, examID string) ([]Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Batch{}
	for _, x := range s.batches {
		if x.TenantID == tenantID && x.ExamID == examID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) GetBatch(_ context.Context, tenantID, batchID string) (Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x, ok := s.batches[batchID]
	if !ok || x.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	return x, nil
}

func (s *MemoryStore) RegisterFile(_ context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error) {
	if validateRegisterFile(&input, asset) != nil {
		return File{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return File{}, ErrNotFound
	}
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return File{}, ErrInvalidTransition
	}
	for _, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.IdempotencyKey == input.IdempotencyKey {
			return x, nil
		}
	}
	status := "uploaded"
	for _, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.SHA256 == asset.SHA256 {
			status = "duplicate"
		}
	}
	x := File{ID: uuid.NewString(), TenantID: tenantID, CaptureBatchID: batchID, FileAssetID: asset.ID, OriginalName: asset.OriginalName, ContentType: asset.ContentType, SHA256: asset.SHA256, ByteSize: asset.SizeBytes, Status: status, IdempotencyKey: input.IdempotencyKey, UploadedBy: actorID, CreatedAt: time.Now().UTC()}
	s.files[x.ID] = x
	batch.FileCount++
	batch.Status = "uploading"
	batch.Revision++
	s.batches[batchID] = batch
	return x, nil
}

func (s *MemoryStore) ListFiles(_ context.Context, tenantID, batchID string) ([]File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []File{}
	for _, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) GetFile(_ context.Context, tenantID, fileID string) (File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x, ok := s.files[fileID]
	if !ok || x.TenantID != tenantID {
		return File{}, ErrNotFound
	}
	return x, nil
}
func (s *MemoryStore) ListPages(_ context.Context, tenantID, batchID string) ([]Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Page{}
	for _, x := range s.pages {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SequenceNo < out[j].SequenceNo })
	return out, nil
}

func (s *MemoryStore) QueueBatch(_ context.Context, tenantID, batchID, actorID string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return Batch{}, ErrInvalidTransition
	}
	count := 0
	for id, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.Status == "uploaded" {
			x.Status = "queued"
			s.files[id] = x
			count++
		}
	}
	if count == 0 {
		return Batch{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	batch.Status = "processing"
	batch.StartedAt = &now
	batch.Revision++
	s.batches[batchID] = batch
	return batch, nil
}

func (s *MemoryStore) ApplyFileResult(_ context.Context, tenantID, fileID string, inputs []DecodedPageInput) (File, error) {
	if len(inputs) == 0 {
		return File{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[fileID]
	if !ok || file.TenantID != tenantID {
		return File{}, ErrNotFound
	}
	if file.Status == "completed" && file.PageCount == len(inputs) {
		return file, nil
	}
	if file.Status != "queued" && file.Status != "processing" && file.Status != "failed" {
		return File{}, ErrInvalidTransition
	}
	base := 0
	for _, p := range s.pages {
		if p.CaptureBatchID == file.CaptureBatchID && p.SequenceNo > base {
			base = p.SequenceNo
		}
	}
	submissionID := uuid.NewString()
	for i, input := range inputs {
		if input.SourceIndex != i+1 || input.FileAssetID == "" {
			return File{}, ErrInvalidInput
		}
		page := Page{ID: uuid.NewString(), TenantID: tenantID, CaptureBatchID: file.CaptureBatchID, CaptureFileID: file.ID, SourceIndex: input.SourceIndex, SubmissionID: submissionID, SubmissionPageID: uuid.NewString(), AssignedPageNo: input.SourceIndex, SequenceNo: base + i + 1, DecodedFileAssetID: input.FileAssetID, Status: "quality_checking", Revision: 1, PageIdentity: map[string]any{"width": input.Width, "height": input.Height, "sha256": input.SHA256}, MatchCandidates: []any{}, ManualOverride: map[string]any{}, CreatedAt: time.Now().UTC()}
		s.pages[page.ID] = page
	}
	file.Status = "completed"
	file.PageCount = len(inputs)
	file.ErrorCode = ""
	s.files[fileID] = file
	batch := s.batches[file.CaptureBatchID]
	batch.PageCount += len(inputs)
	batch.SubmissionCount++
	batch.Status = "matching"
	batch.Revision++
	s.batches[batch.ID] = batch
	return file, nil
}

func (s *MemoryStore) ApplyFileFailure(_ context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.files[fileID]
	if !ok || x.TenantID != tenantID {
		return File{}, ErrNotFound
	}
	x.Status = "failed"
	if retryable {
		x.Status = "queued"
	}
	x.ErrorCode = errorCode
	s.files[fileID] = x
	if batch, exists := s.batches[x.CaptureBatchID]; exists {
		batch.Revision++
		if retryable {
			batch.Status = "processing"
		} else {
			batch.Status = "needs_review"
			batch.FailedCount = 0
			for _, file := range s.files {
				if file.CaptureBatchID == batch.ID && file.Status == "failed" {
					batch.FailedCount++
				}
			}
		}
		s.batches[batch.ID] = batch
	}
	return x, nil
}

func (s *MemoryStore) ApplyQualityOutcome(_ context.Context, tenantID, submissionPageID, qualityStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "quality_rejected"
	if qualityStatus == "passed" {
		status = "normalized"
	} else if qualityStatus == "review" {
		status = "needs_review"
	} else if qualityStatus != "failed" {
		return ErrInvalidInput
	}
	for id, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionPageID == submissionPageID {
			page.Status = status
			page.Revision++
			s.pages[id] = page
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) UpdatePage(_ context.Context, tenantID, pageID, actorID string, input UpdatePageInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	batch := s.batches[x.CaptureBatchID]
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return Page{}, ErrInvalidTransition
	}
	if input.Revision != x.Revision {
		return Page{}, ErrConflict
	}
	if input.RotationDegrees != nil {
		if !validRotation(*input.RotationDegrees) {
			return Page{}, ErrInvalidInput
		}
		x.RotationDegrees = *input.RotationDegrees
		x.Status = "needs_review"
	}
	if input.SequenceNo != nil {
		if *input.SequenceNo <= 0 {
			return Page{}, ErrInvalidInput
		}
		x.SequenceNo = *input.SequenceNo
	}
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}
func (s *MemoryStore) SetBatchStatus(_ context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.batches[batchID]
	if !ok || x.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	if !canSetBatchStatus(x.Status, status) {
		return Batch{}, ErrInvalidTransition
	}
	x.Status = status
	x.Revision++
	if status == "completed" {
		now := time.Now().UTC()
		x.CompletedAt = &now
	} else {
		x.CompletedAt = nil
	}
	s.batches[batchID] = x
	return x, nil
}
