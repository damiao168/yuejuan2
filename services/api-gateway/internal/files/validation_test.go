package files

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFileTypeRequiresAllowedSniffedContentType(t *testing.T) {
	_, err := ValidateFileType("answer.pdf", "application/pdf", "text/plain; charset=utf-8", []string{".pdf"})
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("declared PDF must not override sniffed text content, got %v", err)
	}
}

func TestValidateFileTypeRejectsUnsupportedDeclaration(t *testing.T) {
	_, err := ValidateFileType("answer.pdf", "text/plain", "application/pdf", []string{".pdf"})
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("unsupported declared type must be rejected, got %v", err)
	}
}

func TestValidateFileTypeReturnsServerSniffedType(t *testing.T) {
	contentType, err := ValidateFileType("roster.csv", "application/vnd.ms-excel", "text/plain; charset=utf-8", []string{".csv"})
	if err != nil {
		t.Fatalf("valid CSV type pair was rejected: %v", err)
	}
	if contentType != "text/plain; charset=utf-8" {
		t.Fatalf("expected server-sniffed content type, got %q", contentType)
	}
}

func TestValidateFileTypeAllowsMarkdownReportedAsPlainText(t *testing.T) {
	contentType, err := ValidateFileType("questions.md", "text/plain", "text/plain; charset=utf-8", []string{".md"})
	if err != nil {
		t.Fatalf("markdown text should be accepted: %v", err)
	}
	if contentType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
}

func TestSniffContentTypeRecognizesTIFFByteOrders(t *testing.T) {
	for _, sample := range [][]byte{{'I', 'I', 42, 0, 8, 0, 0, 0}, {'M', 'M', 0, 42, 0, 0, 0, 8}} {
		if got := SniffContentType(sample); got != "image/tiff" {
			t.Fatalf("got %q", got)
		}
	}
}

func TestSniffFileContentTypeAcceptsUTF8MarkdownWithMathAndHTML(t *testing.T) {
	for _, sample := range [][]byte{
		[]byte("# 数学\n\n已知 $x^2=4$，求解。"),
		[]byte("<details>\n<summary>解析</summary>\n\\[x^2=4\\]\n</details>"),
		[]byte("第一题\f解答：\\(x=2\\)"),
	} {
		if got := SniffFileContentType("questions.md", sample); got != "text/plain; charset=utf-8" {
			t.Fatalf("got %q for %q", got, sample)
		}
	}
}

func TestSniffFileContentTypeAcceptsUTF8CutAtInspectionBoundary(t *testing.T) {
	sample := append([]byte(strings.Repeat("a", 510)), 0xe4, 0xb8)
	if got := SniffFileContentType("questions.md", sample); got != "text/plain; charset=utf-8" {
		t.Fatalf("truncated UTF-8 tail was rejected as %q", got)
	}
}

func TestSniffFileContentTypeRejectsBinaryDisguisedAsMarkdown(t *testing.T) {
	if got := SniffFileContentType("questions.md", []byte{'#', ' ', 'x', 0, 1, 2}); got != "application/octet-stream" {
		t.Fatalf("binary markdown was accepted as %q", got)
	}
}
