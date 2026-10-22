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
	// 答题卡图片使用平台单独分配的多模态模型，不借用聊天或试卷文本解析配置。
	templateVisionResolver func(context.Context, string) (*DocumentModelConfig, error)
}

// DocumentModelConfig 只随经过认证的内部解析请求传递。
// 它绝不能持久化到试卷导入记录中，也不能返回浏览器。
type DocumentModelConfig struct {
	AdapterType  string `json:"adapter_type"`
	ProviderKey  string `json:"provider_key,omitempty"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	ModelName    string `json:"model_name"`
	ModelVersion string `json:"model_version"`
}

func (s *DocumentImportService) WithModelResolver(resolve func(context.Context, string) (*DocumentModelConfig, error)) *DocumentImportService {
	s.modelResolver = resolve
	return s
}

func (s *DocumentImportService) WithTemplateVisionResolver(resolve func(context.Context, string) (*DocumentModelConfig, error)) *DocumentImportService {
	s.templateVisionResolver = resolve
	return s
}

type paperImportParse struct {
	cancel context.CancelFunc
}

// 解析输入在执行前持久化，因此请求构建器和任务执行器必须共享一种稳定的传输表示。
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
		// 创建操作及其分发意图已原子提交。
		// 只有持久化执行器执行 I/O；命令重放不会重新调度。
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
			return s.failUnavailableSource(ctx, tenantID, job.ID, textErr)
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
			return s.failUnavailableOCRRuntime(ctx, tenantID, job.ID)
		}
		if allVisualAssetsDirect {
			documents = appendVisualParseDocuments(documents, ocrAssets)
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

func (s *DocumentImportService) failUnavailableOCRRuntime(ctx context.Context, tenantID, importID string) (PaperImportJob, error) {
	// 缺少运行时先记录导入失败；若该记录也写入失败，调用方收到写入错误而非失败任务。
	failed, failErr := s.store.FailPaperImport(ctx, tenantID, importID, "paper_ocr_unavailable", []string{"扫描版 PDF 需要 OCR Worker，但当前存储未配置任务运行时"})
	if failErr != nil {
		return PaperImportJob{}, failErr
	}
	return failed, nil
}

func (s *DocumentImportService) failUnavailableSource(ctx context.Context, tenantID, importID string, textErr error) (PaperImportJob, error) {
	// 持久化存储把读取错误交回执行器；不在这里再写失败终态。
	if _, durable := s.store.(paperImportDispatchStore); durable {
		return PaperImportJob{}, textErr
	}
	failed, failErr := s.store.FailPaperImport(ctx, tenantID, importID, "source_text_unavailable", []string{textErr.Error()})
	if failErr != nil {
		return PaperImportJob{}, failErr
	}
	return failed, nil
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

// 已读取的文本保留在前，直接识别的图片按原来源顺序追加；解析器仍收到每份来源的原角色和索引。
func appendVisualParseDocuments(documents []normalizedImportDocument, assets []PaperImportOCRAsset) []normalizedImportDocument {
	for _, asset := range assets {
		documents = append(documents, visualParseDocument(asset.SourceID, asset.FileAssetID, asset.DocumentIndex, asset.RoleHint))
	}
	return documents
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
	return s.store.CompletePaperImportCandidates(ctx, tenantID, job.ID, parsed)
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
		// 旧请求结束时只能清理自己的登记，不能删除已经接替它的新请求。
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
	return s.store.CompletePaperImportCandidates(ctx, tenantID, job.ID, parsed)
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
	// 追加资料只解析本次新增来源；历史候选由提交阶段合并，避免重复解析改写人工核对结果。
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
		if hasSelectedPaperImportSource(issue, selected) {
			issues = append(issues, issue)
		}
	}
	return documents, pages, issues
}

func hasSelectedPaperImportSource(issue PaperImportIssue, selected map[string]bool) bool {
	// 无来源的整体提示仍保留；有来源时任一新增资料命中即可，但一条提示不能重复追加。
	if len(issue.SourceRefs) == 0 {
		return true
	}
	for _, ref := range issue.SourceRefs {
		if selected[ref.SourceID] {
			return true
		}
	}
	return false
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

func positiveOCRPagesBySource(blocks []PaperImportOCRBlock) map[string]map[int]bool {
	pagesBySource := map[string]map[int]bool{}
	for _, block := range blocks {
		if block.PageNo > 0 {
			if pagesBySource[block.SourceID] == nil {
				pagesBySource[block.SourceID] = map[int]bool{}
			}
			pagesBySource[block.SourceID][block.PageNo] = true
		}
	}
	return pagesBySource
}

func possiblePageMissingIssues(sources []PaperImportSource, blocks []PaperImportOCRBlock) []PaperImportIssue {
	pagesBySource := positiveOCRPagesBySource(blocks)
	issues := []PaperImportIssue{}
	for _, source := range sources {
		pages := pagesBySource[source.ID]
		maxPage := 0
		for page := range pages {
			if page > maxPage {
				maxPage = page
			}
		}
		// 这里只能发现已识别页码之间的空缺；没有总页数，不能据此判定末尾是否缺页。
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
