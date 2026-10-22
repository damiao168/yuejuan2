package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
)

const maxLibraryMatchDocumentBytes = 8 << 20
const maxLibraryMatchTotalBytes = 24 << 20

type storedTemplateLibraryVisualMatcher struct {
	service *DocumentImportService
	objects files.ObjectStorage
}

// NewTemplateLibraryVisualMatcher 只从已授权的仓储快照读取对象，不接收浏览器任意 URL。
// 未配置内部视觉服务时返回 nil，调用方保留精确文件匹配及人工核对路径。
func NewTemplateLibraryVisualMatcher(service *DocumentImportService, objects files.ObjectStorage) TemplateLibraryVisualMatcher {
	if service == nil || objects == nil {
		return nil
	}
	client, ok := service.parser.(*HTTPDocumentParser)
	if !ok || client.baseURL == "" || len(client.token) < 32 {
		return nil
	}
	return storedTemplateLibraryVisualMatcher{service: service, objects: objects}
}

type libraryMatchWireDocument struct {
	ID                string `json:"id,omitempty"`
	MediaType         string `json:"media_type"`
	DataBase64        string `json:"data_base64"`
	SHA256            string `json:"sha256"`
	ExpectedPageCount int    `json:"expected_page_count,omitempty"`
}

func (matcher storedTemplateLibraryVisualMatcher) MatchTemplateLibrary(ctx context.Context, sources TemplateLibraryMatchSources) (TemplateLibraryVisualMatch, error) {
	if !sources.Complete || len(sources.Candidates) == 0 || len(sources.Candidates) > TemplateLibraryMatchCandidateLimit {
		return TemplateLibraryVisualMatch{}, errors.New("template library candidate search is incomplete")
	}
	total := 0
	source, err := matcher.loadDocument(ctx, sources.Source, &total)
	if err != nil {
		return TemplateLibraryVisualMatch{}, err
	}
	candidates := make([]libraryMatchWireDocument, 0, len(sources.Candidates))
	for _, candidate := range sources.Candidates {
		document, err := matcher.loadDocument(ctx, candidate, &total)
		if err != nil {
			return TemplateLibraryVisualMatch{}, err
		}
		candidates = append(candidates, document)
	}
	requestID := uuid.NewString()
	payload := map[string]any{"request_id": requestID, "schema_version": "answer-sheet-match-v1", "source": source, "candidates": candidates}
	raw, err := json.Marshal(payload)
	if err != nil {
		return TemplateLibraryVisualMatch{}, err
	}
	client := matcher.service.parser.(*HTTPDocumentParser)
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, client.baseURL+"/answer-sheet/match", bytes.NewReader(raw))
	if err != nil {
		return TemplateLibraryVisualMatch{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.client.Do(request)
	if err != nil {
		return TemplateLibraryVisualMatch{}, errors.New("template visual matcher is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return TemplateLibraryVisualMatch{}, fmt.Errorf("template visual matcher returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return TemplateLibraryVisualMatch{}, errors.New("template visual match response exceeds limit")
	}
	var result struct {
		RequestID     string  `json:"request_id"`
		SchemaVersion string  `json:"schema_version"`
		Status        string  `json:"status"`
		SelectedID    *string `json:"selected_id"`
		Candidates    []struct {
			ID          string   `json:"id"`
			Score       float64  `json:"score"`
			ReasonCodes []string `json:"reason_codes"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &result) != nil || result.RequestID != requestID || result.SchemaVersion != "answer-sheet-match-v1" {
		return TemplateLibraryVisualMatch{}, errors.New("template visual match response is invalid")
	}
	allowed := map[string]bool{}
	for _, candidate := range candidates {
		allowed[candidate.ID] = true
	}
	seen := map[string]bool{}
	for _, candidate := range result.Candidates {
		if !allowed[candidate.ID] || seen[candidate.ID] || candidate.Score < 0 || candidate.Score > 1 {
			return TemplateLibraryVisualMatch{}, errors.New("template visual match references an invalid candidate")
		}
		seen[candidate.ID] = true
	}
	if len(seen) != len(allowed) {
		return TemplateLibraryVisualMatch{}, errors.New("template visual matcher did not examine all candidates")
	}
	switch result.Status {
	case "matched":
		if result.SelectedID == nil || !allowed[*result.SelectedID] || !seen[*result.SelectedID] {
			return TemplateLibraryVisualMatch{}, errors.New("template visual match did not select an input candidate")
		}
		return TemplateLibraryVisualMatch{Status: "matched", LibraryIDs: []string{*result.SelectedID}}, nil
	case "unknown":
		if result.SelectedID != nil {
			return TemplateLibraryVisualMatch{}, errors.New("unknown template cannot contain a selection")
		}
		return TemplateLibraryVisualMatch{Status: "unknown", LibraryIDs: []string{}}, nil
	case "ambiguous":
		if result.SelectedID != nil || len(seen) == 0 {
			return TemplateLibraryVisualMatch{}, errors.New("template ambiguity did not preserve candidates")
		}
		ids := make([]string, 0, len(seen))
		for _, candidate := range result.Candidates {
			ids = append(ids, candidate.ID)
		}
		return TemplateLibraryVisualMatch{Status: "ambiguous", LibraryIDs: ids}, nil
	default:
		// 不支持的多页或格式与未知版面不同；上层返回失败提示，不能因此开始创建模板。
		return TemplateLibraryVisualMatch{}, errors.New("template visual matching is unsupported")
	}
}

func (matcher storedTemplateLibraryVisualMatcher) loadDocument(ctx context.Context, document TemplateLibraryMatchDocument, total *int) (libraryMatchWireDocument, error) {
	metadata := document.File
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(metadata.ContentType, ";")[0]))
	if (mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "application/pdf") || metadata.SizeBytes < 1 || metadata.SizeBytes > maxLibraryMatchDocumentBytes || int64(*total)+metadata.SizeBytes > maxLibraryMatchTotalBytes || metadata.StorageBucket == "" || metadata.StorageKey == "" {
		return libraryMatchWireDocument{}, errors.New("template reference cannot be analyzed within the resource budget")
	}
	reader, err := matcher.objects.Get(ctx, metadata.StorageBucket, metadata.StorageKey)
	if err != nil {
		return libraryMatchWireDocument{}, errors.New("template reference is unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, maxLibraryMatchDocumentBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || len(data) == 0 || len(data) > maxLibraryMatchDocumentBytes || len(data) != int(metadata.SizeBytes) || *total+len(data) > maxLibraryMatchTotalBytes {
		return libraryMatchWireDocument{}, errors.New("template reference read failed or exceeded the resource budget")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if !strings.EqualFold(digest, document.SourceSHA256) || !strings.EqualFold(digest, metadata.HashSHA256) {
		return libraryMatchWireDocument{}, errors.New("template reference checksum changed")
	}
	*total += len(data)
	return libraryMatchWireDocument{ID: document.LibraryID, MediaType: mediaType, DataBase64: base64.StdEncoding.EncodeToString(data), SHA256: digest, ExpectedPageCount: document.PageCount}, nil
}
