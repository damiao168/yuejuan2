package paper

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

const maxDocumentVisualPageBytes = 8 << 20
// 此限额统计编码前的图像字节，Base64 和 JSON 封装还会增加请求大小。
const maxDocumentVisualPayloadBytes = 32 << 20

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

type DocumentVisualPageLoader interface {
	Load(context.Context, string, []normalizedImportDocument, []PaperImportDecodedPage) ([]documentVisualPage, error)
}

type storedDocumentVisualPageLoader struct {
	files   files.Store
	objects files.ObjectStorage
}

func (l storedDocumentVisualPageLoader) Load(ctx context.Context, tenantID string, documents []normalizedImportDocument, pages []PaperImportDecodedPage) ([]documentVisualPage, error) {
	if len(pages) == 0 {
		return nil, nil
	}
	if l.files == nil || l.objects == nil {
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
		asset, err := l.files.Get(ctx, tenantID, page.FileAssetID)
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
		reader, err := l.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
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
		// 同时核对解码页引用和文件登记摘要，防止同一对象键内容变化后混入旧页面结果。
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
