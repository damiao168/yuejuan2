package logger

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	traceIDKey   contextKey = "trace_id"
)

type Logger struct {
	out   io.Writer
	level string
}

func New(out io.Writer, level string) *Logger {
	return &Logger{out: out, level: level}
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func TraceID(ctx context.Context) string {
	value, _ := ctx.Value(traceIDKey).(string)
	return value
}

func (l *Logger) Info(ctx context.Context, message string, fields map[string]any) {
	l.write(ctx, "info", message, fields)
}

func (l *Logger) Warn(ctx context.Context, message string, fields map[string]any) {
	l.write(ctx, "warn", message, fields)
}

func (l *Logger) Error(ctx context.Context, message string, fields map[string]any) {
	l.write(ctx, "error", message, fields)
}

func (l *Logger) write(ctx context.Context, level string, message string, fields map[string]any) {
	payload := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"level":   level,
		"message": message,
	}
	if rid := RequestID(ctx); rid != "" {
		payload["request_id"] = rid
	}
	if tid := TraceID(ctx); tid != "" {
		payload["trace_id"] = tid
	}
	for key, value := range fields {
		payload[key] = sanitizeField(key, value)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		slog.Error("encode log failed", "error", err)
		return
	}
	_, _ = l.out.Write(append(encoded, '\n'))
}

// 脱敏依赖字段名，仅递归处理 map[string]any 和 []any；调用方不能把秘密藏在自由文本或自定义结构中。
func sanitizeField(key string, value any) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizeField(nestedKey, nestedValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeField("", item))
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	sensitiveTokens := []string{
		"answer",
		"score",
		"student",
		"student_name",
		"student_no",
		"student_number",
		"identity",
		"id_card",
		"phone",
		"email",
		"password",
		"token",
		"secret",
		"credential",
	}
	for _, token := range sensitiveTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
