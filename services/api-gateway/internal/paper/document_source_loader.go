package paper

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

const maxDocumentTextBytes = 700_000
const maxDocumentSourceBytes = 32 << 20

var errDocumentOCRRequired = errors.New("document OCR required")

type DocumentSourceLoader interface {
	Load(context.Context, string, string) (string, error)
	Metadata(context.Context, string, string) (files.FileAsset, error)
}

type storedDocumentSourceLoader struct {
	files   files.Store
	objects files.ObjectStorage
}

func (l storedDocumentSourceLoader) Load(ctx context.Context, tenantID, id string) (string, error) {
	asset, err := l.Metadata(ctx, tenantID, id)
	if err != nil {
		return "", ErrInvalidInput
	}
	body, err := l.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxDocumentSourceBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxDocumentSourceBytes {
		return "", errors.New("资料文件超过解析大小上限，请拆分后重新导入")
	}
	var text string
	switch {
	case strings.Contains(asset.ContentType, "wordprocessingml") || strings.HasSuffix(strings.ToLower(asset.OriginalName), ".docx"):
		text, err = extractDOCXText(data)
	case asset.ContentType == "application/pdf" || strings.HasSuffix(strings.ToLower(asset.OriginalName), ".pdf"):
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", errors.New("PDF 文件损坏")
		}
		// 少量 Tj 文本不能证明整页已提取；PDF 统一交给现有页面解码/视觉路径处理。
		return "", errDocumentOCRRequired
	case isPlainTextDocument(asset):
		if !utf8.Valid(data) {
			err = errors.New("文本资料必须使用 UTF-8 编码")
		} else {
			text = strings.TrimPrefix(string(data), "\uFEFF")
		}
	// 图片和缺少可提取文字的 PDF 留给 OCR 流程，本加载器不尝试把二进制当正文。
	case strings.HasPrefix(asset.ContentType, "image/"):
		return "", errDocumentOCRRequired
	default:
		err = errors.New("仅支持 PDF、DOCX、Markdown 或纯文本文件")
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
		return "", errors.New("资料正文超过解析大小上限，请拆分后重新导入")
	}
	return text, nil
}

func isPlainTextDocument(asset files.FileAsset) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(asset.ContentType, ";")[0]))
	name := strings.ToLower(asset.OriginalName)
	return mediaType == "text/plain" || mediaType == "text/markdown" ||
		strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".markdown")
}

func (l storedDocumentSourceLoader) Metadata(ctx context.Context, tenantID, id string) (files.FileAsset, error) {
	asset, err := l.files.Get(ctx, tenantID, id)
	if err != nil {
		return files.FileAsset{}, ErrInvalidInput
	}
	return asset, nil
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
		raw, err := io.ReadAll(io.LimitReader(rc, maxDocumentTextBytes*2+1))
		rc.Close()
		if err != nil {
			return "", err
		}
		if len(raw) > maxDocumentTextBytes*2 {
			return "", errors.New("DOCX 正文超过解析大小上限，请拆分后重新导入")
		}
		s := string(raw)
		s = regexp.MustCompile(`</w:p>`).ReplaceAllString(s, "\n")
		s = regexp.MustCompile(`<w:(tab|br)[^>]*/>`).ReplaceAllString(s, "\t")
		s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
		return html.UnescapeString(s), nil
	}
	return "", errors.New("DOCX 正文缺失")
}
