package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestLoggerAddsCorrelationIDsAndRedactsSensitiveFields(t *testing.T) {
	var out bytes.Buffer
	logg := New(&out, "info")
	ctx := WithTraceID(WithRequestID(context.Background(), "req-1"), "trace-1")

	logg.Info(ctx, "test event", map[string]any{
		"student_name": "张三",
		"score":        95,
		"status":       "ok",
		"nested": map[string]any{
			"answer_text": "学生答卷原文",
		},
	})

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &payload); err != nil {
		t.Fatalf("decode log: %v; raw=%s", err, out.String())
	}
	if payload["request_id"] != "req-1" || payload["trace_id"] != "trace-1" {
		t.Fatalf("missing correlation ids: %#v", payload)
	}
	if payload["student_name"] != "[REDACTED]" || payload["score"] != "[REDACTED]" {
		t.Fatalf("top-level sensitive fields not redacted: %#v", payload)
	}
	nested, ok := payload["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested log fields not preserved: %#v", payload)
	}
	if nested["answer_text"] != "[REDACTED]" {
		t.Fatalf("nested answer text not redacted: %#v", nested)
	}
	if payload["status"] != "ok" {
		t.Fatalf("non-sensitive status should be preserved: %#v", payload)
	}
}

func TestLoggerHonorsConfiguredMinimumLevel(t *testing.T) {
	var out bytes.Buffer
	logg := New(&out, "error")

	logg.Info(context.Background(), "ignored info", nil)
	logg.Warn(context.Background(), "ignored warning", nil)
	if out.Len() != 0 {
		t.Fatalf("configured error level must suppress lower levels: %s", out.String())
	}

	logg.Error(context.Background(), "kept error", nil)
	if !bytes.Contains(out.Bytes(), []byte(`"message":"kept error"`)) {
		t.Fatalf("configured error level must keep errors: %s", out.String())
	}
}
