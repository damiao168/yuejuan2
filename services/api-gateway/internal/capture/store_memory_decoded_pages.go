package capture

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"time"
)

// ApplyFileResult 把同一文件的解码页面归入同一提交。成组上传等待组内文件到齐，其他页面直接等待质检。
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
	base := s.decodedPageSequenceBaseLocked(file.CaptureBatchID)
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

// 在写入页面的同一写锁下取批次最大序号。仍保留的已删除页也计入，避免重用其序号。
func (s *MemoryStore) decodedPageSequenceBaseLocked(batchID string) int {
	base := 0
	for _, page := range s.pages {
		if page.CaptureBatchID == batchID && page.SequenceNo > base {
			base = page.SequenceNo
		}
	}
	return base
}

// 内存运行模式沿用同一组边界；不同文件选择的余页不能拼到一起。
func (s *MemoryStore) finalizeUploadGroupLocked(file File) {
	files, completeFiles := completedUploadGroupFiles(s.files, file)
	if !completeFiles || len(files) != file.UploadGroupFileCount {
		return
	}
	pages := uploadGroupPages(s.pages, files)
	complete := len(pages) > 0 && len(pages)%file.PagesPerStudent == 0
	applyUploadGroupPageStatus(s.pages, pages, file.PagesPerStudent, complete)
	batch := s.batches[file.CaptureBatchID]
	batch.SubmissionCount = uploadGroupSubmissionCount(s.pages, batch.ID)
	if !complete {
		batch.Status = "needs_review"
	}
	s.batches[batch.ID] = batch
}

func completedUploadGroupFiles(all map[string]File, file File) (map[string]File, bool) {
	files := map[string]File{}
	for _, candidate := range all {
		if candidate.TenantID != file.TenantID || candidate.CaptureBatchID != file.CaptureBatchID || candidate.UploadGroupID != file.UploadGroupID {
			continue
		}
		if candidate.Status != "completed" {
			return nil, false
		}
		files[candidate.ID] = candidate
	}
	return files, true
}

func uploadGroupPages(all map[string]Page, files map[string]File) []Page {
	pages := []Page{}
	for _, page := range all {
		if _, ok := files[page.CaptureFileID]; ok && page.Status != "deleted" {
			pages = append(pages, page)
		}
	}
	sort.Slice(pages, func(i, j int) bool {
		a, b := files[pages[i].CaptureFileID], files[pages[j].CaptureFileID]
		if a.UploadGroupFileIndex != b.UploadGroupFileIndex {
			return a.UploadGroupFileIndex < b.UploadGroupFileIndex
		}
		return pages[i].SourceIndex < pages[j].SourceIndex
	})
	return pages
}

func applyUploadGroupPageStatus(all map[string]Page, pages []Page, pagesPerStudent int, complete bool) {
	target := ""
	for index, page := range pages {
		if complete {
			if index%pagesPerStudent == 0 {
				target = uuid.NewString()
			}
			page.SubmissionID = target
			page.AssignedPageNo = index%pagesPerStudent + 1
			page.Status = "quality_checking"
			page.PageIdentity["grouping_status"] = "complete"
			page.PageIdentity["received_pages"] = pagesPerStudent
		} else {
			page.Status = "needs_review"
			page.PageIdentity["grouping_status"] = "incomplete_pages"
			page.PageIdentity["received_pages"] = len(pages)
			page.PageIdentity["missing_pages"] = (pagesPerStudent - len(pages)%pagesPerStudent) % pagesPerStudent
		}
		all[page.ID] = page
	}
}

func uploadGroupSubmissionCount(pages map[string]Page, batchID string) int {
	submissions := map[string]bool{}
	for _, page := range pages {
		if page.CaptureBatchID == batchID && page.Status != "deleted" {
			submissions[page.SubmissionID] = true
		}
	}
	return len(submissions)
}
