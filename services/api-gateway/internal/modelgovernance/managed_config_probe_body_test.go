package modelgovernance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type managedProbeBodyWithClose struct {
	io.Reader
	close func()
}

func (body managedProbeBodyWithClose) Close() error {
	body.close()
	return nil
}

func TestReadManagedProbeBodyKeepsRequestFailureAfterEOF(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty body"},
		{name: "partial body", body: `{"choices":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 传输层在预算耗尽后也可能返回正常 EOF；不能把已超时的空/部分正文当成格式错误。
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com", nil)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			response := &http.Response{Request: request, Body: managedProbeBodyWithClose{
				Reader: strings.NewReader(test.body), close: func() { closed = true },
			}}
			body, err := readManagedProbeBody(response)
			if string(body) != test.body || !errors.Is(err, context.DeadlineExceeded) || !closed {
				t.Fatalf("expired request lost after EOF: body=%q err=%v closed=%t", body, err, closed)
			}
			result := bodyManagedProbeFailure(ManagedAPIProbeResult{}, err, "capability")
			if result.ErrorCode != "provider_timeout" || result.CapabilityCheck.Code != "provider_timeout" || result.ConnectionDiagnostic.FailureStage != "body" {
				t.Fatalf("expired body was misclassified: %#v", result)
			}
		})
	}
}

func TestReadManagedProbeBodyChecksContextBeforeClosing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 正常关闭正文会释放请求预算；必须先检查读取期间的状态，不能把清理造成的取消误报成故障。
	response := &http.Response{Request: request, Body: managedProbeBodyWithClose{
		Reader: strings.NewReader(`{"data":[]}`), close: cancel,
	}}
	body, err := readManagedProbeBody(response)
	if string(body) != `{"data":[]}` || err != nil || ctx.Err() != context.Canceled {
		t.Fatalf("successful body was affected by cleanup: body=%q err=%v context=%v", body, err, ctx.Err())
	}
}
