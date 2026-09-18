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

	"edugrade-enterprise/services/api-gateway/internal/files"
)

const maxDocumentTextBytes = 700_000

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
	// Encoded and scanned PDFs deliberately fail closed into OCR.
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
