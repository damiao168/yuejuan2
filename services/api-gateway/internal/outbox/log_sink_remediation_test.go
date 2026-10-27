package outbox

import (
	"bytes"
	"context"
	"io"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type adversarialFailingLogWriter struct{ calls int }

type shortEventWriter struct{}

func (shortEventWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func (w *adversarialFailingLogWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, io.ErrClosedPipe
}

// 使用真实日志器、发布器和分发器，仅以现有内存 Store 捕获确认。
// 只有writer实际接受完整事件才确认；诊断级别不能使业务事件消失。
func TestOutboxConfirmsOnlySuccessfulEventWrite(t *testing.T) {
	t.Run("short writer", func(t *testing.T) {
		store := &testStore{events: []Event{{ID: "short", AttemptCount: 1}}}
		count, err := NewDispatcher(store, NewLogPublisher(logger.New(shortEventWriter{}, "error")), Options{}).RunOnce(context.Background())
		if err != nil || count != 0 || len(store.published) != 0 || len(store.failed) != 1 {
			t.Fatalf("short write confirmed: count=%d err=%v", count, err)
		}
	})
	t.Run("configured warning level", func(t *testing.T) {
		var output bytes.Buffer
		store := &testStore{events: []Event{{ID: "synthetic-event", AttemptCount: 1}}}
		dispatcher := NewDispatcher(store, NewLogPublisher(logger.New(&output, "warn")), Options{Owner: "synthetic-owner"})
		count, err := dispatcher.RunOnce(context.Background())
		if err != nil || count != 1 || len(store.published) != 1 || output.Len() == 0 || len(store.failed) != 0 {
			t.Fatalf("business event not emitted: count=%d error=%v published=%v bytes=%d failed=%v", count, err, store.published, output.Len(), store.failed)
		}
		t.Log("warning log level: business event emitted and confirmed")
	})
	t.Run("failed writer", func(t *testing.T) {
		output := &adversarialFailingLogWriter{}
		store := &testStore{events: []Event{{ID: "synthetic-event", AttemptCount: 1}}}
		dispatcher := NewDispatcher(store, NewLogPublisher(logger.New(output, "info")), Options{Owner: "synthetic-owner"})
		count, err := dispatcher.RunOnce(context.Background())
		if err != nil || count != 0 || len(store.published) != 0 || output.calls != 1 || len(store.failed) != 1 {
			t.Fatalf("failed event write incorrectly confirmed: count=%d error=%v published=%v writes=%d failed=%v", count, err, store.published, output.calls, store.failed)
		}
		t.Log("writer returned io.ErrClosedPipe: event retained for retry")
	})
}
