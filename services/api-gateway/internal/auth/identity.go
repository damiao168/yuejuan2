package auth

import (
	"errors"
	"strings"
	"unicode"
)

// NormalizePhone 将支持的手机号格式归一为国际号码；未带区号的中国大陆手机号补 +86。
// 这里只验证格式，不证明号码存在或归调用者所有。
func NormalizePhone(value string) (string, error) {
	var builder strings.Builder
	for index, char := range strings.TrimSpace(value) {
		switch {
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '+' && index == 0:
			builder.WriteRune(char)
		case unicode.IsSpace(char) || strings.ContainsRune("-()", char):
			continue
		default:
			return "", errors.New("phone contains unsupported characters")
		}
	}
	normalized := builder.String()
	switch {
	case strings.HasPrefix(normalized, "0086"):
		normalized = "+86" + strings.TrimPrefix(normalized, "0086")
	case strings.HasPrefix(normalized, "86") && len(normalized) == 13:
		normalized = "+" + normalized
	case !strings.HasPrefix(normalized, "+") && len(normalized) == 11 && strings.HasPrefix(normalized, "1"):
		normalized = "+86" + normalized
	}
	digits := strings.TrimPrefix(normalized, "+")
	if !strings.HasPrefix(normalized, "+") || len(digits) < 8 || len(digits) > 15 || digits[0] == '0' {
		return "", errors.New("phone must be a valid E.164 number")
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return "", errors.New("phone must contain digits only")
		}
	}
	return normalized, nil
}

func MaskPhone(normalized string) string {
	digits := strings.TrimPrefix(normalized, "+86")
	if len(digits) >= 7 {
		return digits[:3] + "****" + digits[len(digits)-4:]
	}
	if len(digits) > 4 {
		return strings.Repeat("*", len(digits)-4) + digits[len(digits)-4:]
	}
	return normalized
}
