package capture

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"time"
)

// 解码结果一次性物化为同一提交的页面，页面先进入质检等待，不直接视为可配准。
func (s *MemoryStore) ApplyFileResult(_ context.Context, tenantID, fileID string, inputs []DecodedPageInput) (File, error) {
	if len(inputs) == 0 {
		return File{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyFileResultLocked(tenantID, fileID, inputs)
}

func (s *MemoryStore) applyFileResultLocked(tenantID, fileID string, inputs []DecodedPageInput) (File, error) {
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
		if file.UploadGroupID != "" {
			page.Status = "decoded"
			page.PageIdentity["grouping_status"] = "waiting_files"
			page.PageIdentity["upload_group_id"] = file.UploadGroupID
			page.PageIdentity["expected_pages"] = file.PagesPerStudent
		}
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
	if file.UploadGroupID != "" {
		s.finalizeUploadGroupLocked(file)
	}
	return file, nil
}

// 内存运行模式沿用同一组边界；不同文件选择的余页不能拼到一起。
func (s *MemoryStore) finalizeUploadGroupLocked(file File) {
	files := map[string]File{}
	for _, f := range s.files {
		if f.TenantID == file.TenantID && f.CaptureBatchID == file.CaptureBatchID && f.UploadGroupID == file.UploadGroupID {
			if f.Status != "completed" {
				return
			}
			files[f.ID] = f
		}
	}
	if len(files) != file.UploadGroupFileCount {
		return
	}
	pages := []Page{}
	for _, p := range s.pages {
		if _, ok := files[p.CaptureFileID]; ok && p.Status != "deleted" {
			pages = append(pages, p)
		}
	}
	sort.Slice(pages, func(i, j int) bool {
		a, b := files[pages[i].CaptureFileID], files[pages[j].CaptureFileID]
		if a.UploadGroupFileIndex != b.UploadGroupFileIndex {
			return a.UploadGroupFileIndex < b.UploadGroupFileIndex
		}
		return pages[i].SourceIndex < pages[j].SourceIndex
	})
	complete := len(pages) > 0 && len(pages)%file.PagesPerStudent == 0
	target := ""
	for i, p := range pages {
		if complete {
			if i%file.PagesPerStudent == 0 {
				target = uuid.NewString()
			}
			p.SubmissionID = target
			p.AssignedPageNo = i%file.PagesPerStudent + 1
			p.Status = "quality_checking"
			p.PageIdentity["grouping_status"] = "complete"
			p.PageIdentity["received_pages"] = file.PagesPerStudent
		} else {
			p.Status = "needs_review"
			p.PageIdentity["grouping_status"] = "incomplete_pages"
			p.PageIdentity["received_pages"] = len(pages)
			p.PageIdentity["missing_pages"] = (file.PagesPerStudent - len(pages)%file.PagesPerStudent) % file.PagesPerStudent
		}
		s.pages[p.ID] = p
	}
	batch := s.batches[file.CaptureBatchID]
	submissions := map[string]bool{}
	for _, p := range s.pages {
		if p.CaptureBatchID == batch.ID && p.Status != "deleted" {
			submissions[p.SubmissionID] = true
		}
	}
	batch.SubmissionCount = len(submissions)
	if !complete {
		batch.Status = "needs_review"
	}
	s.batches[batch.ID] = batch
}
