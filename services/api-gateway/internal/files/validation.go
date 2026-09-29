package files

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var allowedOwnerTypes = map[string]bool{
	"generic":                              true,
	"exam":                                 true,
	"exam_paper":                           true,
	"submission":                           true,
	"answer_page":                          true,
	"submission_page_original":             true,
	"submission_page_normalized":           true,
	"capture_batch":                        true,
	"capture_page_decoded":                 true,
	"page_registration_output":             true,
	"answer_segment_crop":                  true,
	"page_registration_correction_preview": true,
	"omr_evidence":                         true,
	"report":                               true,
	"import":                               true,
}

var allowedContentTypesByExt = map[string]map[string]bool{
	".pdf": {
		"application/pdf": true,
	},
	".png": {
		"image/png": true,
	},
	".jpg": {
		"image/jpeg": true,
	},
	".jpeg": {
		"image/jpeg": true,
	},
	".tif": {
		"image/tiff": true,
	},
	".tiff": {
		"image/tiff": true,
	},
	".csv": {
		"text/csv":                  true,
		"text/csv; charset=utf-8":   true,
		"text/plain; charset=utf-8": true,
		"text/plain":                true,
		"application/vnd.ms-excel":  true,
	},
	".docx": {
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		"application/zip": true,
	},
	".txt": {
		"text/plain":                true,
		"text/plain; charset=utf-8": true,
	},
	".md": {
		"text/plain":                   true,
		"text/plain; charset=utf-8":    true,
		"text/markdown":                true,
		"text/markdown; charset=utf-8": true,
	},
	".markdown": {
		"text/plain":                   true,
		"text/plain; charset=utf-8":    true,
		"text/markdown":                true,
		"text/markdown; charset=utf-8": true,
	},
}

func CleanFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "", ErrInvalidFile
	}
	if name == ".." || strings.ContainsAny(name, `:"<>|`) || reservedDeviceName(name) {
		return "", ErrInvalidFile
	}
	return name, nil
}

func ValidateOwnerType(ownerType string) bool {
	return allowedOwnerTypes[ownerType]
}

func ValidateOwnerReferences(ownerType string, ownerID string, examID string, submissionID string) bool {
	switch ownerType {
	case "exam":
		return ownerID != "" && examID != "" && ownerID == examID
	case "submission", "answer_page":
		return ownerID != "" && submissionID != "" && ownerID == submissionID
	case "submission_page_original", "submission_page_normalized":
		return ownerID != "" && submissionID != ""
	case "capture_batch":
		return ownerID != "" && examID != ""
	case "capture_page_decoded":
		return ownerID != "" && examID != ""
	case "omr_evidence":
		return ownerID != "" && examID != ""
	default:
		return true
	}
}

func ValidateFileType(filename string, declaredContentType string, sniffedContentType string, allowedExtensions []string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if !extensionAllowed(ext, allowedExtensions) {
		return "", ErrInvalidFile
	}
	allowedTypes, ok := allowedContentTypesByExt[ext]
	if !ok {
		return "", ErrInvalidFile
	}
	declaredContentType = strings.ToLower(strings.TrimSpace(declaredContentType))
	sniffedContentType = strings.ToLower(strings.TrimSpace(sniffedContentType))
	// Browsers and generic multipart clients commonly send this placeholder;
	// it carries no trustworthy type information, so defer to server sniffing.
	if declaredContentType == "application/octet-stream" {
		declaredContentType = ""
	}
	if !allowedTypes[sniffedContentType] {
		return "", ErrInvalidFile
	}
	if declaredContentType != "" && !allowedTypes[declaredContentType] {
		return "", ErrInvalidFile
	}
	return sniffedContentType, nil
}

// BuildStorageKey 只从原文件名取扩展名；随机后缀使相同内容也不必共用同一个对象。
func BuildStorageKey(tenantID string, hashSHA256 string, filename string) (string, error) {
	random, err := randomHex(8)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(filename))
	prefix := hashSHA256
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	return fmt.Sprintf("tenant/%s/files/%s-%s%s", tenantID, prefix, random, ext), nil
}

func SniffContentType(sample []byte) string {
	if len(sample) == 0 {
		return ""
	}
	if len(sample) >= 4 && ((sample[0] == 'I' && sample[1] == 'I' && sample[2] == 42 && sample[3] == 0) || (sample[0] == 'M' && sample[1] == 'M' && sample[2] == 0 && sample[3] == 42)) {
		return "image/tiff"
	}
	return http.DetectContentType(sample)
}

// SniffFileContentType keeps server-side inspection authoritative while
// treating UTF-8 Markdown and plain text as text even when the generic MIME
// sniffer encounters Markdown HTML or clipboard control whitespace.
func SniffFileContentType(filename string, sample []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if (ext == ".txt" || ext == ".md" || ext == ".markdown") && looksLikeUTF8Text(sample) {
		return "text/plain; charset=utf-8"
	}
	return SniffContentType(sample)
}

func looksLikeUTF8Text(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	for _, b := range sample {
		if b == 0 || b == 0x7f || (b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f') {
			return false
		}
	}
	if utf8.Valid(sample) {
		return true
	}
	// The 512-byte inspection window may end in the middle of a valid UTF-8
	// rune. Accept only that specific incomplete-tail case.
	for tailLength := 1; tailLength < utf8.UTFMax && tailLength <= len(sample); tailLength++ {
		cut := len(sample) - tailLength
		tail := sample[cut:]
		if utf8.Valid(sample[:cut]) && utf8.RuneStart(tail[0]) && !utf8.FullRune(tail) {
			return true
		}
	}
	return false
}

func extensionAllowed(ext string, allowed []string) bool {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), ext) {
			return true
		}
	}
	return false
}

func IsUUIDLike(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func reservedDeviceName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	base := strings.TrimSuffix(upper, strings.ToUpper(filepath.Ext(upper)))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
