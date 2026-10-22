package reviewbookmark

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct {
	store    Store
	evidence *EvidenceService
}

func NewHandler(store Store) *Handler                             { return &Handler{store: store} }
func (h *Handler) WithEvidence(service *EvidenceService) *Handler { h.evidence = service; return h }

func handlerUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return user, false
	}
	// 个人说明与材料不允许浏览器、代理缓存，错误响应也沿用此策略。
	w.Header().Set("Cache-Control", "private, no-store")
	return user, true
}

func (h *Handler) GetState(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	item, err := h.store.State(r.Context(), user, r.PathValue("taskId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bookmark": item})
}
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	var raw struct {
		Reason           *string `json:"reason"`
		ExpectedRevision *int64  `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &raw) {
		return
	}
	if raw.Reason == nil || raw.ExpectedRevision == nil {
		writeError(w, r, ErrInvalid)
		return
	}
	input := PutInput{Reason: *raw.Reason, ExpectedRevision: *raw.ExpectedRevision}
	ack, err := h.store.Put(r.Context(), user, r.PathValue("taskId"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ack)
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	var raw struct {
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &raw) {
		return
	}
	if raw.ExpectedRevision == nil {
		writeError(w, r, ErrInvalid)
		return
	}
	ack, err := h.store.Delete(r.Context(), user, r.PathValue("bookmarkId"), DeleteInput{ExpectedRevision: *raw.ExpectedRevision})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ack)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	filter := ListFilter{ExamID: query.Get("exam_id"), QuestionID: query.Get("question_id"), Subject: query.Get("subject"), State: query.Get("state"), Q: query.Get("q"), Cursor: query.Get("cursor")}
	if query.Has("limit") {
		var err error
		filter.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || filter.Limit == 0 {
			writeError(w, r, ErrInvalid)
			return
		}
	}
	result, err := h.store.List(r.Context(), user, filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
func (h *Handler) Cleanup(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	items, err := h.store.Cleanup(r.Context(), user)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	// JSON 解码会用替代符修复非法 UTF-8，必须先检查原字节，不能让长度校验静默接受损坏文本。
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil || !utf8.Valid(raw) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid UTF-8 JSON within 8 KiB")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain known JSON fields")
		return false
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *RevisionConflict
	switch {
	case errors.As(err, &conflict):
		w.Header().Set(httpx.ErrorCodeHeader, "bookmark_revision_conflict")
		httpx.JSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "bookmark_revision_conflict", "message": "bookmark changed; reload current state", "conflict_revision": conflict.Revision}})
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_bookmark_not_found", "review bookmark was not found")
	case errors.Is(err, ErrForbidden), errors.Is(err, auth.ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "review bookmark access is forbidden")
	case errors.Is(err, auth.ErrUnauthenticated):
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	case errors.Is(err, ErrInvalid):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_bookmark_input", "review bookmark input is invalid")
	case errors.Is(err, ErrArchived):
		httpx.Error(w, r, http.StatusConflict, "exam_archived", "archived exam bookmarks cannot be added or edited")
	case errors.Is(err, ErrEvidenceChanged):
		httpx.Error(w, r, http.StatusConflict, "evidence_version_changed", "answer evidence changed; reload the context")
	case errors.Is(err, commandreceipt.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "idempotency_conflict", "command identity was used with another request")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_bookmark_operation_failed", "review bookmark operation failed")
	}
}
