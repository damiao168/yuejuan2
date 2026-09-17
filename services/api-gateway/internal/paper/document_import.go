package paper

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
)

const maxDocumentTextBytes = 700_000
const maxDocumentParseResponseBytes = 4 << 20
const maxDocumentVisualPageBytes = 8 << 20
const maxDocumentVisualPayloadBytes = 32 << 20

const directVisualDocumentPlaceholder = "原始页面图片由多模态模型直接识别"

var errDocumentOCRRequired = errors.New("document OCR required")

type DocumentImportService struct {
	store         Store
	files         files.Store
	objects       files.ObjectStorage
	client        *http.Client
	baseURL       string
	token         string
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

type documentParseRequest struct {
	RequestID    string                     `json:"request_id"`
	Subject      string                     `json:"subject"`
	Documents    []normalizedImportDocument `json:"documents"`
	VisualPages  []documentVisualPage       `json:"visual_pages,omitempty"`
	ManagedModel *DocumentModelConfig       `json:"managed_model,omitempty"`
}

type documentVisualPage struct {
	SourceID      string `json:"source_id"`
	DocumentIndex int    `json:"document_index"`
	PageNo        int    `json:"page_no"`
	MediaType     string `json:"media_type"`
	DataBase64    string `json:"data_base64"`
	SHA256        string `json:"sha256"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
}

type documentParseResponse struct {
	Documents          []PaperImportDetectedDocument `json:"documents"`
	QuestionCandidates []QuestionCandidate           `json:"question_candidates"`
	AnswerCandidates   []AnswerCandidate             `json:"answer_candidates"`
	SolutionCandidates []SolutionCandidate           `json:"solution_candidates"`
	RubricCandidates   []RubricCandidate             `json:"rubric_candidates"`
	Issues             []PaperImportIssue            `json:"issues"`
	ModelUsage         PaperImportModelUsage         `json:"model_usage,omitempty"`
}

// PaperImportParseResult is the immutable parser output accepted by the
// transactional publisher. Keeping this type exported lets integration tests
// and alternate parser adapters exercise the exact command boundary.
type PaperImportParseResult = documentParseResponse

// The parser input is persisted before execution so it must have one stable
// wire representation shared by the request builder and the task executor.
type normalizedImportDocument = PaperImportParseDocument

func NewDocumentImportService(store Store, fileStore files.Store, objects files.ObjectStorage, baseURL, token string, timeout time.Duration) *DocumentImportService {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &DocumentImportService{store: store, files: fileStore, objects: objects, client: &http.Client{Timeout: timeout}, baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), token: token, activeParses: map[string]*paperImportParse{}}
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
		asset, getErr := s.files.Get(ctx, tenantID, source.FileAssetID)
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
	parsed, err := s.parseInputForTenantWithProgress(parseContext, tenantID, job.ID, job.Subject, binding.Input.Documents, binding.Input.Pages, onProgress)
	if err != nil {
		return documentParseResponse{}, err
	}
	parsed.Issues = append(parsed.Issues, binding.Input.ExtraIssues...)
	parsed.Issues = appendNoExamContentIssue(parsed, binding.Input.Documents)
	return parsed, nil
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
	asset, err := s.files.Get(ctx, tenantID, id)
	if err != nil {
		return "", ErrInvalidInput
	}
	body, err := s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		return "", err
	}
	var text string
	switch {
	case strings.Contains(asset.ContentType, "wordprocessingml") || strings.HasSuffix(strings.ToLower(asset.OriginalName), ".docx"):
		text, err = extractDOCXText(data)
	case asset.ContentType == "application/pdf" || strings.HasSuffix(strings.ToLower(asset.OriginalName), ".pdf"):
		text, err = extractPDFText(data)
	case strings.HasPrefix(asset.ContentType, "image/"):
		return "", errDocumentOCRRequired
	default:
		err = errors.New("仅支持 PDF 或 DOCX 文件")
	}
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if len([]rune(text)) < 20 {
		if asset.ContentType == "application/pdf" || strings.HasSuffix(strings.ToLower(asset.OriginalName), ".pdf") {
			return "", errDocumentOCRRequired
		}
		return "", errors.New("文件没有可提取文字")
	}
	if len(text) > maxDocumentTextBytes {
		text = text[:maxDocumentTextBytes]
	}
	return text, nil
}

func (s *DocumentImportService) parseForTenant(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument) (documentParseResponse, error) {
	return s.parseForTenantWithProgress(ctx, tenantID, requestID, subject, documents, nil)
}

func (s *DocumentImportService) parseForTenantWithProgress(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument, onProgress func(map[string]any) error) (documentParseResponse, error) {
	return s.parseInputForTenantWithProgress(ctx, tenantID, requestID, subject, documents, nil, onProgress)
}

func (s *DocumentImportService) parseInputForTenantWithProgress(ctx context.Context, tenantID, requestID, subject string, documents []normalizedImportDocument, pages []PaperImportDecodedPage, onProgress func(map[string]any) error) (documentParseResponse, error) {
	if s.baseURL == "" || len(s.token) < 32 {
		return documentParseResponse{}, errors.New("AI service not configured")
	}
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
	body, _ := json.Marshal(documentParseRequest{RequestID: requestID, Subject: subject, Documents: documents, VisualPages: visualPages, ManagedModel: model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/paper/parse", bytes.NewReader(body))
	if err != nil {
		return documentParseResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/x-ndjson, application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return documentParseResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return documentParseResponse{}, fmt.Errorf("paper parser returned %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/x-ndjson") {
		var out documentParseResponse
		dec := json.NewDecoder(io.LimitReader(resp.Body, maxDocumentParseResponseBytes))
		if err := dec.Decode(&out); err != nil {
			return documentParseResponse{}, err
		}
		return out, nil
	}

	type streamFrame struct {
		Type     string                 `json:"type"`
		Progress map[string]any         `json:"progress"`
		Result   *documentParseResponse `json:"result"`
		Error    json.RawMessage        `json:"error"`
	}
	type streamError struct {
		Status int `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxDocumentParseResponseBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxDocumentParseResponseBytes+1)
	readBytes := 0
	var result *documentParseResponse
	for scanner.Scan() {
		readBytes += len(scanner.Bytes()) + 1
		if readBytes > maxDocumentParseResponseBytes {
			return documentParseResponse{}, errors.New("paper parser response exceeded size limit")
		}
		var frame streamFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return documentParseResponse{}, errors.New("paper parser stream contained invalid JSON")
		}
		switch frame.Type {
		case "progress":
			if onProgress != nil {
				if err := onProgress(frame.Progress); err != nil {
					return documentParseResponse{}, err
				}
			}
		case "result":
			if frame.Result == nil || result != nil {
				return documentParseResponse{}, errors.New("paper parser stream contained an invalid result")
			}
			result = frame.Result
		case "error":
			var detail streamError
			if json.Unmarshal(frame.Error, &detail) != nil {
				return documentParseResponse{}, errors.New("paper parser stream reported an invalid error")
			}
			code := strings.TrimSpace(detail.Error.Code)
			if code == "" {
				code = "unknown_error"
			}
			return documentParseResponse{}, fmt.Errorf("paper parser stream failed: %s", code)
		default:
			return documentParseResponse{}, errors.New("paper parser stream contained an unknown event")
		}
	}
	if err := scanner.Err(); err != nil {
		return documentParseResponse{}, fmt.Errorf("read paper parser stream: %w", err)
	}
	if result == nil {
		return documentParseResponse{}, errors.New("paper parser stream ended without a result")
	}
	return *result, nil
}

func (s *DocumentImportService) loadDocumentVisualPages(ctx context.Context, tenantID string, documents []normalizedImportDocument, pages []PaperImportDecodedPage) ([]documentVisualPage, error) {
	if len(pages) == 0 {
		return nil, nil
	}
	if s.files == nil || s.objects == nil {
		return nil, errors.New("paper page image storage is unavailable")
	}
	documentIndexes := make(map[string]int, len(documents))
	for _, document := range documents {
		documentIndexes[document.SourceID] = document.DocumentIndex
	}
	ordered := append([]PaperImportDecodedPage(nil), pages...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DocumentIndex == ordered[j].DocumentIndex {
			return ordered[i].PageNo < ordered[j].PageNo
		}
		return ordered[i].DocumentIndex < ordered[j].DocumentIndex
	})
	seen := map[string]bool{}
	totalBytes := 0
	visualPages := make([]documentVisualPage, 0, len(ordered))
	for _, page := range ordered {
		documentIndex, ok := documentIndexes[page.SourceID]
		key := fmt.Sprintf("%s:%d", page.SourceID, page.PageNo)
		if !ok || documentIndex != page.DocumentIndex || page.PageNo <= 0 || strings.TrimSpace(page.FileAssetID) == "" || seen[key] {
			return nil, errors.New("paper page image reference is invalid")
		}
		seen[key] = true
		asset, err := s.files.Get(ctx, tenantID, page.FileAssetID)
		if err != nil {
			return nil, fmt.Errorf("load paper page image metadata: %w", err)
		}
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(asset.ContentType, ";")[0]))
		if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/webp" {
			return nil, errors.New("paper page image has an unsupported media type")
		}
		if asset.SizeBytes <= 0 || asset.SizeBytes > maxDocumentVisualPageBytes || totalBytes+int(asset.SizeBytes) > maxDocumentVisualPayloadBytes {
			return nil, errors.New("paper page images exceed the multimodal request limit")
		}
		reader, err := s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("load paper page image: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxDocumentVisualPageBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read paper page image: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close paper page image: %w", closeErr)
		}
		if len(data) == 0 || len(data) > maxDocumentVisualPageBytes || totalBytes+len(data) > maxDocumentVisualPayloadBytes {
			return nil, errors.New("paper page images exceed the multimodal request limit")
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		if expected := strings.TrimSpace(page.SHA256); expected != "" && !strings.EqualFold(expected, digest) {
			return nil, errors.New("paper page image checksum mismatch")
		}
		if expected := strings.TrimSpace(asset.HashSHA256); expected != "" && !strings.EqualFold(expected, digest) {
			return nil, errors.New("paper page image asset checksum mismatch")
		}
		totalBytes += len(data)
		visualPages = append(visualPages, documentVisualPage{
			SourceID: page.SourceID, DocumentIndex: page.DocumentIndex, PageNo: page.PageNo,
			MediaType: mediaType, DataBase64: base64.StdEncoding.EncodeToString(data), SHA256: digest,
			Width: page.Width, Height: page.Height,
		})
	}
	return visualPages, nil
}

func extractDOCXText(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("DOCX 文件损坏")
	}
	for _, file := range r.File {
		if file.Name != "word/document.xml" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxDocumentTextBytes*2))
		rc.Close()
		if err != nil {
			return "", err
		}
		s := string(raw)
		s = regexp.MustCompile(`</w:p>`).ReplaceAllString(s, "\n")
		s = regexp.MustCompile(`<w:(tab|br)[^>]*/>`).ReplaceAllString(s, "\t")
		s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		return html.UnescapeString(s), nil
	}
	return "", errors.New("DOCX 正文缺失")
}

func extractPDFText(data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", errors.New("PDF 文件损坏")
	}
	// Conservative fallback for text PDFs. Encoded/scanned PDFs deliberately fail
	// closed so that OCR is used instead of inventing question content.
	re := regexp.MustCompile(`\(([^()]*)\)\s*Tj`)
	matches := re.FindAllSubmatch(data, -1)
	var b strings.Builder
	for _, match := range matches {
		value := strings.ReplaceAll(string(match[1]), `\(`, "(")
		value = strings.ReplaceAll(value, `\)`, ")")
		value = strings.ReplaceAll(value, `\\`, `\`)
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
