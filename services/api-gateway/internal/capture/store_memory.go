package capture

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

func (s *MemoryStore) GetStudentPrintPackage(context.Context, string, string) (StudentPrintPackage, error) {
	return StudentPrintPackage{}, ErrNotFound
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processingSummaryLocked(tenantID, submissionID)
}

func (s *MemoryStore) ListProcessingSummaries(_ context.Context, tenantID, batchID string) ([]ProcessingSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return nil, ErrNotFound
	}
	submissionIDs := map[string]struct{}{}
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.CaptureBatchID == batchID && page.SubmissionID != "" && page.Status != "deleted" {
			submissionIDs[page.SubmissionID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(submissionIDs))
	for submissionID := range submissionIDs {
		ids = append(ids, submissionID)
	}
	sort.Strings(ids)
	out := make([]ProcessingSummary, 0, len(ids))
	for _, submissionID := range ids {
		summary, err := s.processingSummaryLocked(tenantID, submissionID)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

// 内存实现按页面和最近配准运行生成展示用阻塞摘要；它只模拟状态，不替代数据库聚合。
func (s *MemoryStore) processingSummaryLocked(tenantID, submissionID string) (ProcessingSummary, error) {
	out := ProcessingSummary{SubmissionID: submissionID, Blockers: []ProcessingBlocker{}}
	pages := make([]Page, 0)
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionID == submissionID && page.Status != "deleted" {
			pages = append(pages, page)
		}
	}
	if len(pages) == 0 {
		return out, ErrNotFound
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].SequenceNo < pages[j].SequenceNo })
	for _, page := range pages {
		out.TotalPages++
		if page.Status == "ready" {
			out.ReadyPages++
			continue
		}
		blocker := ProcessingBlocker{PageID: page.ID, PageNo: page.AssignedPageNo, Stage: "processing", Code: "pending", Action: "wait"}
		var latest RegistrationRun
		for _, run := range s.registrations {
			if run.CapturePageID == page.ID && (latest.ID == "" || run.CreatedAt.After(latest.CreatedAt)) {
				latest = run
			}
		}
		blocker.RegistrationRunID = latest.ID
		switch {
		case page.Status == "quality_rejected" || page.Status == "needs_review":
			blocker.Stage = "quality"
			blocker.Code = "quality_review"
			blocker.Action = "review_quality"
			out.BlockedPages++
		case latest.ProcessingStatus == "terminal_error":
			blocker.Stage = "registration"
			blocker.Code = "registration_failed"
			blocker.Action = "retry_registration"
			out.BlockedPages++
		case latest.MatchStatus == "needs_review":
			blocker.Stage = "registration"
			blocker.Code = "low_confidence"
			blocker.Action = "confirm_registration"
			out.BlockedPages++
		default:
			out.PendingPages++
		}
		out.Blockers = append(out.Blockers, blocker)
	}
	out.CanComplete = out.ReadyPages == out.TotalPages
	return out, nil
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

func (s *MemoryStore) GetTemplateMatchRun(_ context.Context, tenantID, runID string) (TemplateMatchRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) ApplyTemplateMatchResult(_ context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, nil, ErrNotFound
	}
	item.ProcessingStatus = "completed"
	item.Decision = input.Decision
	item.SelectedTemplateID = input.SelectedTemplateID
	item.SelectedTemplateContentHash = input.SelectedTemplateContentHash
	item.Score = input.Score
	item.Margin = input.Margin
	item.Candidates = input.Candidates
	s.templateMatches[runID] = item
	return item, []RegistrationRun{}, nil
}

func (s *MemoryStore) ApplyTemplateMatchFailure(_ context.Context, tenantID, runID, errorCode string, _ map[string]any, retryable bool) (TemplateMatchRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, ErrNotFound
	}
	item.ProcessingStatus = "terminal_error"
	if retryable {
		item.ProcessingStatus = "retryable_error"
	}
	item.ErrorCode = errorCode
	s.templateMatches[runID] = item
	return item, nil
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
	item.GuardReport = input.GuardReport
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
	key := tenantID + ":" + actorID + ":" + input.IdempotencyKey
	if input.IdempotencyKey != "" {
		if id, ok := s.batchKeys[key]; ok {
			batch := s.batches[id]
			if batchCommandHash(examID, input) != batchCommandHash(batch.ExamID, CreateBatchInput{Name: batch.Name, SourceType: batch.SourceType, ScannerDevice: batch.ScannerDevice}) {
				return Batch{}, ErrConflict
			}
			return batch, nil
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

func (s *MemoryStore) RecoverBatchCommand(_ context.Context, tenantID, examID, actorID, commandID string) (BatchCommandRecovery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := BatchCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	if id, ok := s.batchKeys[tenantID+":"+actorID+":"+commandID]; ok {
		batch := s.batches[id]
		if batch.ExamID != examID {
			return result, ErrConflict
		}
		result.Batch = &batch
		result.Status = "succeeded"
	}
	return result, nil
}

func (s *MemoryStore) ListBatches(_ context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Batch{}
	for _, x := range s.batches {
		if x.TenantID == tenantID && x.ExamID == examID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) ||
				(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
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

// 同一批次内按幂等键复用注册结果，活动文件的哈希重复则标为 duplicate；失败文件可再次上传。
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
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.SHA256 == asset.SHA256 &&
			(x.Status == "uploaded" || x.Status == "queued" || x.Status == "processing" || x.Status == "completed") {
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

func (s *MemoryStore) GetPageBySubmissionPageID(_ context.Context, tenantID, submissionPageID string) (Page, error) {
	if submissionPageID == "" {
		return Page{}, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionPageID == submissionPageID {
			return page, nil
		}
	}
	return Page{}, ErrNotFound
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
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && (x.Status == "uploaded" || x.Status == "failed") {
			x.Status = "queued"
			x.ErrorCode = ""
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

// 解码结果一次性物化为同一提交的页面，页面先进入质检等待，不直接视为可配准。
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
			if page.PageIdentity == nil {
				page.PageIdentity = map[string]any{}
			}
			page.PageIdentity["quality_status"] = qualityStatus
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
