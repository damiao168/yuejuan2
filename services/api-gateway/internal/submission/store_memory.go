package submission

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.RWMutex
	next        int
	submissions map[string]Submission
	pages       map[string]SubmissionPage
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:        1,
		submissions: map[string]Submission{},
		pages:       map[string]SubmissionPage{},
	}
}

func (s *MemoryStore) Create(_ context.Context, tenantID string, examID string, actorID string, input CreateSubmissionInput) (Submission, error) {
	if err := ValidateCreateInput(input); err != nil {
		return Submission{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := Submission{
		ID:                s.id("submission"),
		TenantID:          tenantID,
		ExamID:            examID,
		StudentID:         input.StudentID,
		CandidateNo:       input.CandidateNo,
		SourceType:        input.SourceType,
		Status:            "created",
		ExpectedPageCount: input.ExpectedPageCount,
		ActualPageCount:   0,
		QualityStatus:     "unchecked",
		QualityIssues:     []QualityIssue{},
		CollectedBy:       actorID,
		Revision:          1,
		CreatedAt:         time.Now().UTC(),
	}
	s.submissions[item.ID] = item
	return item, nil
}

func (s *MemoryStore) ListByExam(_ context.Context, tenantID string, examID string, filter ListFilter) ([]Submission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Submission{}
	for _, item := range s.submissions {
		if item.TenantID == tenantID && item.ExamID == examID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	out = submissionsFollowingCursor(out, filter)
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func submissionsFollowingCursor(items []Submission, filter ListFilter) []Submission {
	if filter.CursorID == "" {
		return items
	}
	// 列表已按时间和 ID 降序排列；只有 ID 非空才启用游标，严格排除游标项。
	start := 0
	for start < len(items) {
		item := items[start]
		if item.CreatedAt.Before(filter.CursorCreatedAt) ||
			(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
			break
		}
		start++
	}
	return items[start:]
}

func (s *MemoryStore) ListByExams(ctx context.Context, tenantID string, examIDs []string) (map[string][]Submission, error) {
	out := make(map[string][]Submission, len(examIDs))
	for _, examID := range examIDs {
		items, err := s.ListByExam(ctx, tenantID, examID, ListFilter{})
		if err != nil {
			return nil, err
		}
		out[examID] = items
	}
	return out, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID string, id string) (Submission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.submissions[id]
	if !ok || item.TenantID != tenantID {
		return Submission{}, ErrNotFound
	}
	item.Pages = s.pagesForLocked(tenantID, id)
	return item, nil
}

func (s *MemoryStore) AddPage(_ context.Context, tenantID string, submissionID string, _ string, input AddPageInput) (SubmissionPage, error) {
	if err := ValidatePageInput(input); err != nil {
		return SubmissionPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.submissions[submissionID]
	if !ok || item.TenantID != tenantID {
		return SubmissionPage{}, ErrNotFound
	}
	if item.Status == "ready_for_ocr" || item.Status == "rejected" {
		return SubmissionPage{}, ErrSubmissionLocked
	}
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionID == submissionID && page.PageNo == input.PageNo {
			return SubmissionPage{}, ErrDuplicatePage
		}
	}
	page := SubmissionPage{
		ID:              s.id("page"),
		TenantID:        tenantID,
		SubmissionID:    submissionID,
		FileAssetID:     input.FileAssetID,
		PageNo:          input.PageNo,
		Status:          "uploaded",
		QualityStatus:   "unchecked",
		QualityOverride: map[string]any{},
		QualityIssues:   []QualityIssue{},
		CreatedAt:       time.Now().UTC(),
	}
	s.pages[page.ID] = page
	item.ActualPageCount++
	item.Status = "pages_uploaded"
	item.QualityStatus = "unchecked"
	item.QualityIssues = []QualityIssue{}
	s.submissions[submissionID] = item
	return page, nil
}

func (s *MemoryStore) ReplacePage(_ context.Context, tenantID string, submissionID string, _ string, pageNo int, input AddPageInput) (SubmissionPage, error) {
	input.PageNo = pageNo
	if err := ValidatePageInput(input); err != nil {
		return SubmissionPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.submissions[submissionID]
	if !ok || item.TenantID != tenantID {
		return SubmissionPage{}, ErrNotFound
	}
	if item.Status == "ready_for_ocr" || item.Status == "rejected" {
		return SubmissionPage{}, ErrSubmissionLocked
	}
	var page SubmissionPage
	found := false
	for id, current := range s.pages {
		if current.TenantID == tenantID && current.SubmissionID == submissionID && current.PageNo == pageNo {
			current.FileAssetID = input.FileAssetID
			current.Status = "uploaded"
			current.LatestQualityRunID = ""
			current.NormalizedFileAssetID = ""
			current.QualityStatus = "unchecked"
			current.QualityOverride = map[string]any{}
			current.QualityIssues = []QualityIssue{}
			s.pages[id] = current
			page = current
			found = true
			break
		}
	}
	if !found {
		return SubmissionPage{}, ErrNotFound
	}
	item.ActualPageCount = len(s.pagesForLocked(tenantID, submissionID))
	item.Status = "pages_uploaded"
	item.QualityStatus = "unchecked"
	item.QualityIssues = []QualityIssue{}
	s.submissions[submissionID] = item
	return page, nil
}

func (s *MemoryStore) ListPages(_ context.Context, tenantID string, submissionID string) ([]SubmissionPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.submissions[submissionID]; !ok || item.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return s.pagesForLocked(tenantID, submissionID), nil
}

func (s *MemoryStore) ListAnswerRegions(_ context.Context, tenantID string, submissionID string) ([]AnswerRegion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.submissions[submissionID]; !ok || item.TenantID != tenantID {
		return nil, ErrNotFound
	}
	// 内存提交夹具不持久化 answer_segment 行；返回空集合会选择旧版整页路径。
	return []AnswerRegion{}, nil
}

func (s *MemoryStore) ApplyPageQualityResult(_ context.Context, tenantID string, input ApplyPageQualityInput) (SubmissionPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.submissions[input.SubmissionID]
	if !ok || item.TenantID != tenantID {
		return SubmissionPage{}, ErrNotFound
	}
	page, ok := s.pages[input.PageID]
	if !ok || page.TenantID != tenantID || page.SubmissionID != input.SubmissionID {
		return SubmissionPage{}, ErrNotFound
	}
	if input.QualityStatus != "passed" && input.QualityStatus != "review" && input.QualityStatus != "failed" {
		return SubmissionPage{}, ErrInvalidInput
	}
	page.LatestQualityRunID = input.LatestQualityRunID
	page.NormalizedFileAssetID = input.NormalizedFileAssetID
	page.QualityStatus = input.QualityStatus
	page.QualityIssues = append([]QualityIssue{}, input.QualityIssues...)
	s.pages[input.PageID] = page
	s.aggregateQualityLocked(tenantID, input.SubmissionID)
	return page, nil
}

func (s *MemoryStore) OverridePageQuality(_ context.Context, tenantID string, pageID string, actorID string, input OverridePageQualityInput) (SubmissionPage, error) {
	reason := strings.TrimSpace(input.Reason)
	if pageID == "" || actorID == "" || len([]rune(reason)) < 5 || len([]rune(reason)) > 500 {
		return SubmissionPage{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page, ok := s.pages[pageID]
	if !ok || page.TenantID != tenantID {
		return SubmissionPage{}, ErrNotFound
	}
	item, ok := s.submissions[page.SubmissionID]
	if !ok || item.TenantID != tenantID {
		return SubmissionPage{}, ErrNotFound
	}
	if item.Status == "ready_for_ocr" || item.Status == "rejected" {
		return SubmissionPage{}, ErrSubmissionLocked
	}
	if page.QualityStatus == "passed" && page.QualityOverride["decision"] == "accepted" {
		return page, nil
	}
	if page.QualityStatus != "review" && page.QualityStatus != "failed" {
		return SubmissionPage{}, ErrInvalidTransition
	}
	if page.NormalizedFileAssetID == "" {
		return SubmissionPage{}, ErrInvalidTransition
	}
	page.QualityOverride = map[string]any{
		"decision":                "accepted",
		"reason":                  reason,
		"actor_id":                actorID,
		"overridden_at":           time.Now().UTC(),
		"original_quality_status": page.QualityStatus,
		"original_quality_issues": append([]QualityIssue{}, page.QualityIssues...),
	}
	page.QualityStatus = "passed"
	s.pages[pageID] = page
	s.aggregateQualityLocked(tenantID, page.SubmissionID)
	return page, nil
}

func (s *MemoryStore) RunQualityCheck(_ context.Context, tenantID string, submissionID string, _ string) (QualityResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.submissions[submissionID]
	if !ok || item.TenantID != tenantID {
		return QualityResult{}, ErrNotFound
	}
	if item.Status == "ready_for_ocr" || item.Status == "rejected" {
		return QualityResult{}, ErrSubmissionLocked
	}
	pages := s.pagesForLocked(tenantID, submissionID)
	issues := qualityIssues(item, pages)
	item.QualityIssues = issues
	item.ActualPageCount = len(pages)
	s.submissions[submissionID] = item
	// 采集完整性只是第一道门禁。只有每页都通过图像质量检查或获得有效覆盖批准后，聚合结果才可通过。
	s.aggregateQualityLocked(tenantID, submissionID)
	return QualityResult{Valid: len(issues) == 0, Issues: issues}, nil
}

func (s *MemoryStore) UpdateStatus(_ context.Context, tenantID string, submissionID string, _ string, status string, expectedRevision int64) (Submission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.submissions[submissionID]
	if !ok || item.TenantID != tenantID {
		return Submission{}, ErrNotFound
	}
	if !CanTransition(item.Status, status, item.QualityStatus) {
		return Submission{}, ErrInvalidTransition
	}
	if expectedRevision <= 0 {
		return Submission{}, ErrInvalidInput
	}
	if item.Revision != expectedRevision {
		return Submission{}, ErrRevisionConflict
	}
	item.Status = status
	item.Revision++
	s.submissions[submissionID] = item
	return item, nil
}

func qualityIssues(item Submission, pages []SubmissionPage) []QualityIssue {
	issues := []QualityIssue{}
	if len(pages) == 0 {
		issues = append(issues, QualityIssue{Code: "no_pages", Message: "submission has no pages"})
	}
	if item.ExpectedPageCount > 0 && len(pages) != item.ExpectedPageCount {
		issues = append(issues, QualityIssue{Code: "page_count_mismatch", Message: fmt.Sprintf("expected %d pages, got %d", item.ExpectedPageCount, len(pages))})
	}
	if item.ExpectedPageCount > 0 {
		seen := map[int]bool{}
		for _, page := range pages {
			seen[page.PageNo] = true
		}
		for pageNo := 1; pageNo <= item.ExpectedPageCount; pageNo++ {
			if !seen[pageNo] {
				issues = append(issues, QualityIssue{Code: "missing_page", Message: fmt.Sprintf("page %d is missing", pageNo)})
			}
		}
	}
	return issues
}

func (s *MemoryStore) pagesForLocked(tenantID string, submissionID string) []SubmissionPage {
	out := []SubmissionPage{}
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionID == submissionID {
			out = append(out, page)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageNo < out[j].PageNo })
	return out
}

func (s *MemoryStore) aggregateQualityLocked(tenantID string, submissionID string) {
	item, ok := s.submissions[submissionID]
	if !ok || item.TenantID != tenantID {
		return
	}
	pages := s.pagesForLocked(tenantID, submissionID)
	item.ActualPageCount = len(pages)
	issues := qualityIssues(item, pages)
	status := "unchecked"
	hasUnchecked := false
	hasReview := false
	hasFailed := false
	for _, page := range pages {
		switch page.QualityStatus {
		case "failed":
			hasFailed = true
		case "review":
			hasReview = true
		case "passed":
		default:
			hasUnchecked = true
		}
	}
	if len(issues) > 0 {
		status = "failed"
	} else if hasFailed {
		status = "failed"
	} else if hasReview {
		status = "review"
	} else if hasUnchecked || len(pages) == 0 {
		status = "unchecked"
	} else {
		status = "passed"
		item.Status = "quality_checked"
	}
	item.QualityStatus = status
	item.QualityIssues = issues
	s.submissions[submissionID] = item
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func (s *MemoryStore) MarkPageQualityForTest(pageID string, runID string, normalizedFileID string, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, ok := s.pages[pageID]
	if !ok {
		return
	}
	page.LatestQualityRunID = runID
	page.NormalizedFileAssetID = normalizedFileID
	page.QualityStatus = status
	s.pages[pageID] = page
}
