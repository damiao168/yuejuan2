package paper

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
)

const directVisualDocumentPlaceholder = "原始页面图片由多模态模型直接识别"

type DocumentImportService struct {
	store         PaperImportRepository
	sourceLoader  DocumentSourceLoader
	visualLoader  DocumentVisualPageLoader
	parser        DocumentParser
	parserTimeout time.Duration
	parseMu       sync.Mutex
	activeParses  map[string]*paperImportParse
	modelResolver func(context.Context, string) (*DocumentModelConfig, error)
}

// DocumentModelConfig travels only on the authenticated internal parser request.
// It must never be persisted in a paper import or returned to the browser.
type DocumentModelConfig struct {
	AdapterType  string `json:"adapter_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	ModelName    string `json:"model_name"`
	ModelVersion string `json:"model_version"`
}

func (s *DocumentImportService) WithModelResolver(resolve func(context.Context, string) (*DocumentModelConfig, error)) *DocumentImportService {
	s.modelResolver = resolve
	return s
}

type paperImportParse struct {
	cancel context.CancelFunc
}

// The parser input is persisted before execution so it must have one stable
// wire representation shared by the request builder and the task executor.
type normalizedImportDocument = PaperImportParseDocument

func NewDocumentImportService(store PaperImportRepository, fileStore files.Store, objects files.ObjectStorage, baseURL, token string, timeout time.Duration) *DocumentImportService {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &DocumentImportService{
		store: store, sourceLoader: storedDocumentSourceLoader{files: fileStore, objects: objects},
		visualLoader: storedDocumentVisualPageLoader{files: fileStore, objects: objects},
		parser:       NewHTTPDocumentParser(baseURL, token, timeout), parserTimeout: timeout,
		activeParses: map[string]*paperImportParse{},
	}
}

func (s *DocumentImportService) Start(ctx context.Context, tenantID, examID, userID string, input CreatePaperImportInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	job, err := s.store.CreatePaperImport(ctx, tenantID, examID, userID, input)
	if err != nil {
		return PaperImportJob{}, err
	}
	return s.processAcceptedSources(ctx, tenantID, userID, job)
}

func (s *DocumentImportService) AddSources(ctx context.Context, tenantID, userID, importID string, input AddPaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	job, err := s.store.AddPaperImportSources(ctx, tenantID, importID, userID, input)
	if err != nil {
		return PaperImportJob{}, err
	}
	return s.processAcceptedSources(ctx, tenantID, userID, job)
}

func (s *DocumentImportService) ReplaceSources(ctx context.Context, tenantID, userID, importID string, input ReplacePaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	job, err := s.store.ReplacePaperImportSources(ctx, tenantID, importID, userID, input)
	if err != nil {
		return PaperImportJob{}, err
	}
	return s.processAcceptedSources(ctx, tenantID, userID, job)
}

func (s *DocumentImportService) processAcceptedSources(ctx context.Context, tenantID, userID string, job PaperImportJob) (PaperImportJob, error) {
	if _, durable := s.store.(paperImportDispatchStore); durable {
		// Creation and its dispatch intent have already committed atomically.
		// Only the durable executor performs I/O; command replay never reschedules.
		return job, nil
	}
	return s.processSources(ctx, tenantID, userID, job)
}

func (s *DocumentImportService) processSources(ctx context.Context, tenantID, userID string, job PaperImportJob) (PaperImportJob, error) {
	documents := []normalizedImportDocument{}
	ocrAssets := []PaperImportOCRAsset{}
	directPages := []PaperImportDecodedPage{}
	allVisualAssetsDirect := true
	for _, source := range job.Sources {
		text, textErr := s.assetText(ctx, tenantID, source.FileAssetID)
		if textErr == nil {
			documents = append(documents, normalizedImportDocument{SourceID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint, Content: text, Blocks: []PaperImportOCRBlock{}})
			continue
		}
		if !errors.Is(textErr, errDocumentOCRRequired) {
			if _, durable := s.store.(paperImportDispatchStore); durable {
				return PaperImportJob{}, textErr
			}
			failed, failErr := s.store.FailPaperImport(ctx, tenantID, job.ID, "source_text_unavailable", []string{textErr.Error()})
			if failErr != nil {
				return PaperImportJob{}, failErr
			}
			return failed, nil
		}
		asset, getErr := s.sourceLoader.Metadata(ctx, tenantID, source.FileAssetID)
		if getErr != nil {
			return PaperImportJob{}, getErr
		}
		ocrAssets = append(ocrAssets, PaperImportOCRAsset{SourceID: source.ID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint, FileAssetID: source.FileAssetID, ContentType: asset.ContentType})
		if isDirectVisualMediaType(asset.ContentType) {
			directPages = append(directPages, PaperImportDecodedPage{
				SourceID: source.ID, DocumentIndex: source.DocumentIndex, PageNo: 1,
				FileAssetID: source.FileAssetID, SHA256: asset.HashSHA256,
			})
		} else {
			allVisualAssetsDirect = false
		}
	}
	if len(ocrAssets) > 0 {
		runtime, ok := s.store.(PaperImportRuntime)
		if !ok {
			failed, failErr := s.store.FailPaperImport(ctx, tenantID, job.ID, "paper_ocr_unavailable", []string{"扫描版 PDF 需要 OCR Worker，但当前存储未配置任务运行时"})
			if failErr != nil {
				return PaperImportJob{}, failErr
			}
			return failed, nil
		}
		if allVisualAssetsDirect {
			for _, asset := range ocrAssets {
				documents = append(documents, visualParseDocument(asset.SourceID, asset.FileAssetID, asset.DocumentIndex, asset.RoleHint))
			}
			if err := runtime.QueuePaperImportParse(ctx, tenantID, job, userID, PaperImportParseRequest{Documents: documents, Pages: directPages}); err != nil {
				return PaperImportJob{}, err
			}
			return job, nil
		}
		if decodeRuntime, supportsDirectDecode := s.store.(interface {
			QueuePaperImportDecode(context.Context, string, PaperImportJob, string, []PaperImportOCRAsset, []PaperImportParseDocument) error
		}); supportsDirectDecode {
			if err := decodeRuntime.QueuePaperImportDecode(ctx, tenantID, job, userID, ocrAssets, documents); err != nil {
				return PaperImportJob{}, err
			}
			return job, nil
		}
		if err := runtime.QueuePaperImportOCR(ctx, tenantID, job, userID, ocrAssets); err != nil {
			return PaperImportJob{}, err
		}
		return job, nil
	}
	if runtime, ok := s.store.(PaperImportRuntime); ok {
		if err := runtime.QueuePaperImportParse(ctx, tenantID, job, userID, PaperImportParseRequest{Documents: documents}); err != nil {
			return PaperImportJob{}, err
		}
		return job, nil
	}
	return s.completeParsedDocuments(ctx, tenantID, job, documents, nil)
}

func isDirectVisualMediaType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return mediaType == "image/png" || mediaType == "image/jpeg" || mediaType == "image/webp"
}

func visualParseDocument(sourceID, fileAssetID string, documentIndex int, roleHint string) PaperImportParseDocument {
	return PaperImportParseDocument{
		SourceID: sourceID, FileAssetID: fileAssetID, DocumentIndex: documentIndex,
		RoleHint: roleHint, Content: directVisualDocumentPlaceholder, Blocks: []PaperImportOCRBlock{},
	}
}

func (s *DocumentImportService) completeParsedDocuments(ctx context.Context, tenantID string, job PaperImportJob, documents []normalizedImportDocument, extraIssues []PaperImportIssue) (PaperImportJob, error) {
	parseContext, release := s.beginParse(ctx, tenantID, job.ID)
	defer release()
	parsed, err := s.parseForTenant(parseContext, tenantID, job.ID, job.Subject, documents)
	if err != nil {
		if errors.Is(parseContext.Err(), context.Canceled) {
			return PaperImportJob{}, ErrConflict
		}
		failed, failErr := s.store.FailPaperImport(ctx, tenantID, job.ID, "ai_parse_failed", []string{"AI 解析服务暂不可用，可稍后重试"})
		if failErr != nil {
			return PaperImportJob{}, failErr
		}
		return failed, nil
	}
	parsed.Issues = append(parsed.Issues, extraIssues...)
	parsed.Issues = appendNoExamContentIssue(parsed, documents)
	return s.store.CompletePaperImportCandidates(ctx, tenantID, job.ID, parsed.Documents, parsed.QuestionCandidates, parsed.AnswerCandidates, parsed.SolutionCandidates, parsed.RubricCandidates, parsed.Issues)
}

func (s *DocumentImportService) beginParse(ctx context.Context, tenantID, importID string) (context.Context, func()) {
	parseContext, cancel := context.WithCancel(ctx)
	entry := &paperImportParse{cancel: cancel}
	key := tenantID + "\x00" + importID
	s.parseMu.Lock()
	previous := s.activeParses[key]
	s.activeParses[key] = entry
	s.parseMu.Unlock()
	if previous != nil {
		previous.cancel()
	}
	return parseContext, func() {
		cancel()
		s.parseMu.Lock()
		if s.activeParses[key] == entry {
			delete(s.activeParses, key)
		}
		s.parseMu.Unlock()
	}
}

func (s *DocumentImportService) Cancel(tenantID, importID string) {
	key := tenantID + "\x00" + importID
	s.parseMu.Lock()
	entry := s.activeParses[key]
	s.parseMu.Unlock()
	if entry != nil {
		entry.cancel()
	}
}

func appendNoExamContentIssue(parsed documentParseResponse, documents []normalizedImportDocument) []PaperImportIssue {
	issues := append([]PaperImportIssue{}, parsed.Issues...)
	if len(parsed.QuestionCandidates) > 0 || len(parsed.AnswerCandidates) > 0 || len(parsed.SolutionCandidates) > 0 || len(parsed.RubricCandidates) > 0 {
		return issues
	}
	for _, issue := range issues {
		if issue.Code == "NO_EXAM_CONTENT_DETECTED" {
			return issues
		}
	}
	refs := make([]PaperImportSourceRef, 0, len(documents))
	for _, document := range documents {
		refs = append(refs, PaperImportSourceRef{SourceID: document.SourceID, FileAssetID: document.FileAssetID, DocumentIndex: document.DocumentIndex})
	}
	return append(issues, candidateIssue(
		"NO_EXAM_CONTENT_DETECTED",
		"error",
		"confirmed",
		"",
		"未识别到与考试有关的题目、答案、解析或评分标准，请检查是否上传了无关图片或错误文件",
		"请重新上传正确的考试资料；若图片确属考试资料，可重试识别或手动补充题目",
		refs,
	))
}

func (s *DocumentImportService) PrepareOCRParse(ctx context.Context, tenantID, importID string, blocks []PaperImportOCRBlock) (PaperImportJob, PaperImportParseRequest, error) {
	job, err := s.store.GetPaperImport(ctx, tenantID, importID)
	if err != nil {
		return PaperImportJob{}, PaperImportParseRequest{}, err
	}
	if job.Status != "processing" {
		return PaperImportJob{}, PaperImportParseRequest{}, ErrConflict
	}
	sortPaperImportOCRBlocks(blocks)
	bySource := map[string][]PaperImportOCRBlock{}
	sourceByID := map[string]PaperImportSource{}
	for _, source := range job.Sources {
		sourceByID[source.ID] = source
	}
	issues := []PaperImportIssue{}
	for _, block := range blocks {
		source, known := sourceByID[block.SourceID]
		if !known || source.DocumentIndex != block.DocumentIndex {
			return PaperImportJob{}, PaperImportParseRequest{}, ErrInvalidInput
		}
		if strings.TrimSpace(block.Text) != "" {
			bySource[block.SourceID] = append(bySource[block.SourceID], block)
		}
		if block.Confidence < 0.75 {
			c := block.Confidence
			issues = append(issues, PaperImportIssue{Code: "LOW_OCR_CONFIDENCE", Severity: "error", Certainty: "confirmed", Message: fmt.Sprintf("第 %d 页部分内容识别不确定（%.0f%%）", block.PageNo, block.Confidence*100), Confidence: &c, SourceRefs: []PaperImportSourceRef{{SourceID: block.SourceID, FileAssetID: source.FileAssetID, DocumentIndex: block.DocumentIndex, PageNo: block.PageNo, BlockID: block.BlockID, BBox: block.BBox, OCRConfidence: &c}}, ResolutionHint: "请对照原图核对"})
		}
	}
	issues = append(issues, possiblePageMissingIssues(job.Sources, blocks)...)
	documents := []normalizedImportDocument{}
	for _, source := range job.Sources {
		sourceBlocks := bySource[source.ID]
		contentParts := []string{}
		for _, block := range sourceBlocks {
			contentParts = append(contentParts, block.Text)
		}
		content := strings.Join(contentParts, "\n")
		if content == "" {
			text, textErr := s.assetText(ctx, tenantID, source.FileAssetID)
			if textErr != nil {
				return PaperImportJob{}, PaperImportParseRequest{}, fmt.Errorf("%w: OCR returned no usable text", ErrInvalidInput)
			}
			content = text
		}
		documents = append(documents, normalizedImportDocument{SourceID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint, Content: content, Blocks: sourceBlocks})
	}
	return job, PaperImportParseRequest{Documents: documents, ExtraIssues: issues}, nil
}

func (s *DocumentImportService) ExecuteParse(ctx context.Context, tenantID, importID string, input PaperImportParseRequest) (PaperImportJob, error) {
	job, err := s.store.GetPaperImport(ctx, tenantID, importID)
	if err != nil {
		return PaperImportJob{}, err
	}
	if job.Status == "review_required" {
		if job.ResultGeneration == job.Generation && job.Generation > 0 {
			return job, nil
		}
		return PaperImportJob{}, ErrConflict
	}
	if job.Status != "processing" {
		return PaperImportJob{}, ErrConflict
	}
	parseContext, release := s.beginParse(ctx, tenantID, job.ID)
	defer release()
	parsed, err := s.parseInputForTenantWithProgress(parseContext, tenantID, job.ID, job.Subject, input.Documents, input.Pages, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	parsed.Issues = append(parsed.Issues, input.ExtraIssues...)
	parsed.Issues = appendNoExamContentIssue(parsed, input.Documents)
	return s.store.CompletePaperImportCandidates(ctx, tenantID, job.ID, parsed.Documents, parsed.QuestionCandidates, parsed.AnswerCandidates, parsed.SolutionCandidates, parsed.RubricCandidates, parsed.Issues)
}

func (s *DocumentImportService) computeParseRun(ctx context.Context, tenantID string, binding PaperImportRunBinding, onProgress func(map[string]any) error) (documentParseResponse, error) {
	job, err := s.store.GetPaperImport(ctx, tenantID, binding.ImportID)
	if err != nil {
		return documentParseResponse{}, err
	}
	if job.Status != "processing" || job.Generation != binding.Generation || job.RunID != binding.RunID || job.SourceRevision != binding.SourceRevision {
		return documentParseResponse{}, ErrConflict
	}
	parseContext, release := s.beginParse(ctx, tenantID, job.ID)
	defer release()
	documents, pages, extraIssues := incrementalPaperImportParseInput(binding)
	parsed, err := s.parseInputForTenantWithProgress(parseContext, tenantID, job.ID, job.Subject, documents, pages, onProgress)
	if err != nil {
		return documentParseResponse{}, err
	}
	parsed.Issues = append(parsed.Issues, extraIssues...)
	parsed.Issues = appendNoExamContentIssue(parsed, documents)
	return parsed, nil
}

func incrementalPaperImportParseInput(binding PaperImportRunBinding) ([]normalizedImportDocument, []PaperImportDecodedPage, []PaperImportIssue) {
	if binding.CommandType != "add_sources" || len(binding.NewSourceIDs) == 0 {
		return binding.Input.Documents, binding.Input.Pages, binding.Input.ExtraIssues
	}
	selected := make(map[string]bool, len(binding.NewSourceIDs))
	for _, sourceID := range binding.NewSourceIDs {
		selected[sourceID] = true
	}
	documents := make([]normalizedImportDocument, 0, len(binding.NewSourceIDs))
	for _, document := range binding.Input.Documents {
		if selected[document.SourceID] {
			documents = append(documents, document)
		}
	}
	if len(documents) == 0 {
		return binding.Input.Documents, binding.Input.Pages, binding.Input.ExtraIssues
	}
	pages := make([]PaperImportDecodedPage, 0, len(binding.Input.Pages))
	for _, page := range binding.Input.Pages {
		if selected[page.SourceID] {
			pages = append(pages, page)
		}
	}
	issues := make([]PaperImportIssue, 0, len(binding.Input.ExtraIssues))
	for _, issue := range binding.Input.ExtraIssues {
		if len(issue.SourceRefs) == 0 {
			issues = append(issues, issue)
			continue
		}
		for _, ref := range issue.SourceRefs {
			if selected[ref.SourceID] {
				issues = append(issues, issue)
				break
			}
		}
	}
	return documents, pages, issues
}

func sortPaperImportOCRBlocks(blocks []PaperImportOCRBlock) {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].DocumentIndex == blocks[j].DocumentIndex {
			if blocks[i].PageNo == blocks[j].PageNo {
				return false
			}
			return blocks[i].PageNo < blocks[j].PageNo
		}
		return blocks[i].DocumentIndex < blocks[j].DocumentIndex
	})
}

func possiblePageMissingIssues(sources []PaperImportSource, blocks []PaperImportOCRBlock) []PaperImportIssue {
	pagesBySource := map[string]map[int]bool{}
	for _, block := range blocks {
		if block.PageNo > 0 {
			if pagesBySource[block.SourceID] == nil {
				pagesBySource[block.SourceID] = map[int]bool{}
			}
			pagesBySource[block.SourceID][block.PageNo] = true
		}
	}
	issues := []PaperImportIssue{}
	for _, source := range sources {
		pages := pagesBySource[source.ID]
		maxPage := 0
		for page := range pages {
			if page > maxPage {
				maxPage = page
			}
		}
		for page := 1; page < maxPage; page++ {
			if pages[page] {
				continue
			}
			issues = append(issues, candidateIssue("POSSIBLE_PAGE_MISSING", "warning", "suspected", "", fmt.Sprintf("资料第 %d 页没有可用 OCR 文本", page), "请核对该页是否空白、漏传或识别失败", []PaperImportSourceRef{{SourceID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, PageNo: page}}))
		}
	}
	return issues
}

func (s *DocumentImportService) assetText(ctx context.Context, tenantID, id string) (string, error) {
	return s.sourceLoader.Load(ctx, tenantID, id)
}

func (s *DocumentImportService) parseForTenant(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument) (documentParseResponse, error) {
	return s.parseForTenantWithProgress(ctx, tenantID, requestID, subject, documents, nil)
}

func (s *DocumentImportService) parseForTenantWithProgress(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument, onProgress func(map[string]any) error) (documentParseResponse, error) {
	return s.parseInputForTenantWithProgress(ctx, tenantID, requestID, subject, documents, nil, onProgress)
}

func (s *DocumentImportService) parseInputForTenantWithProgress(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument, pages []PaperImportDecodedPage, onProgress func(map[string]any) error) (documentParseResponse, error) {
	visualPages, err := s.loadDocumentVisualPages(ctx, tenantID, documents, pages)
	if err != nil {
		return documentParseResponse{}, err
	}
	var model *DocumentModelConfig
	if s.modelResolver != nil && tenantID != "" {
		var err error
		model, err = s.modelResolver(ctx, tenantID)
		if err != nil {
			return documentParseResponse{}, errors.New("school model configuration unavailable")
		}
	}
	return s.parser.Parse(ctx, DocumentParseRequest{
		RequestID: requestID, Subject: subject, Documents: documents, VisualPages: visualPages, ManagedModel: model,
	}, onProgress)
}

func (s *DocumentImportService) loadDocumentVisualPages(ctx context.Context, tenantID string, documents []normalizedImportDocument, pages []PaperImportDecodedPage) ([]documentVisualPage, error) {
	return s.visualLoader.Load(ctx, tenantID, documents, pages)
}
