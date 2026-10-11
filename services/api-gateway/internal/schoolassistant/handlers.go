package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct {
	service      *Service
	conversation *ConversationService
}

func NewHandler(deps Dependencies) *Handler {
	service := NewService(deps)
	return &Handler{service: service, conversation: NewConversationService(service, deps.Conversation)}
}

func RegisterRoutes(mux *http.ServeMux, h *Handler, requireSchoolAdmin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/school-assistant/capabilities", requireSchoolAdmin(h.Capabilities))
	mux.Handle("POST /api/v1/school-assistant/queries/stream", requireSchoolAdmin(h.QueryStream))
	mux.Handle("POST /api/v1/school-assistant/conversations/stream", requireSchoolAdmin(h.ConversationStream))
}

func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Capabilities(r.Context())
	if err != nil {
		writeQueryError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) QueryStream(w http.ResponseWriter, r *http.Request) {
	var input QueryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.Error(w, r, 400, "invalid_json", "请求必须为包含已知字段的 JSON 对象。")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, 400, "invalid_json", "请求只能包含一个 JSON 对象。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// 身份、业务权限及 body 中的考试 ID 均在 SSE 头之前验证；拒绝请求保持标准 HTTP 错误。
	prepared, err := h.service.prepare(ctx, input)
	if err != nil {
		writeQueryError(w, r, err)
		return
	}
	if ctx.Err() != nil {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// 统一使用 ResponseController；项目的审计和指标中间件通过 Unwrap 暴露底层流式能力。
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	seq := 0
	started := time.Now()
	emit := func(event Event) error {
		seq = event.Seq
		if event.Type == "done" {
			event.DurationMS = time.Since(started).Milliseconds()
		}
		return writeSSE(ctx, w, event)
	}
	_, err = h.service.queryPrepared(ctx, prepared, emit)
	if err != nil && r.Context().Err() == nil {
		code, message := "query_failed", "查询未完成，请重新查询。"
		if errors.Is(err, context.DeadlineExceeded) {
			code, message = "query_timeout", "查询超时，请稍后重试。"
		}
		_ = writeSSE(r.Context(), w, Event{Type: "error", RequestID: prepared.request.RequestID, Seq: seq + 1, Code: code, Message: message, Retryable: true})
	}
}

func writeSSE(ctx context.Context, w http.ResponseWriter, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, encoded); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func writeQueryError(w http.ResponseWriter, r *http.Request, err error) {
	var rejection *queryError
	if errors.As(err, &rejection) {
		httpx.Error(w, r, rejection.status, rejection.code, rejection.message)
		return
	}
	httpx.Error(w, r, 503, "assistant_unavailable", "学校业务助手暂不可用。")
}
