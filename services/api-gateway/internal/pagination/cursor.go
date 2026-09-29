package pagination

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidCursor = errors.New("invalid pagination cursor")

// Cursor 使用时间与 ID 共同定位，避免相同创建时间的记录在翻页时丢失或重复。
// 它只承载位置；查询仍须独立执行权限过滤，并采用一致的排序字段。
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func Encode(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func Decode(value string) (Cursor, error) {
	if strings.TrimSpace(value) == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 512 {
		return Cursor{}, ErrInvalidCursor
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return Cursor{}, ErrInvalidCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{CreatedAt: createdAt, ID: parts[1]}, nil
}

// EncodeParts creates an opaque cursor for lists whose stable ordering needs
// more than created_at and id (for example priority queues).
func EncodeParts(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
}

func DecodeParts(value string, count int) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 512 {
		return nil, ErrInvalidCursor
	}
	parts := strings.Split(string(raw), "\x1f")
	if len(parts) != count {
		return nil, ErrInvalidCursor
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, ErrInvalidCursor
		}
	}
	return parts, nil
}

func Limit(value string, fallback int, maximum int) (int, error) {
	if fallback <= 0 {
		fallback = 50
	}
	if maximum < fallback {
		maximum = fallback
	}
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maximum {
		return 0, errors.New("invalid pagination limit")
	}
	return parsed, nil
}
