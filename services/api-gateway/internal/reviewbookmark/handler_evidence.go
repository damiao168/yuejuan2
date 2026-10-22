package reviewbookmark

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

func (h *Handler) GetContext(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	if h.evidence == nil || h.store == nil {
		writeEvidenceError(w, r, ErrEvidenceUnavailable)
		return
	}
	record, err := h.store.GetByID(r.Context(), user, r.PathValue("bookmarkId"))
	if err != nil {
		writeEvidenceError(w, r, err)
		return
	}
	result, err := h.evidence.Context(r.Context(), user, record)
	if err != nil {
		writeEvidenceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) GetImage(w http.ResponseWriter, r *http.Request) {
	user, ok := handlerUser(w, r)
	if !ok {
		return
	}
	if h.evidence == nil || h.store == nil {
		writeEvidenceError(w, r, ErrEvidenceUnavailable)
		return
	}
	record, err := h.store.GetByID(r.Context(), user, r.PathValue("bookmarkId"))
	if err != nil {
		writeEvidenceError(w, r, err)
		return
	}
	resource, err := h.evidence.Image(r.Context(), user, record, r.URL.Query().Get("version"))
	if err != nil {
		writeEvidenceError(w, r, err)
		return
	}
	// GET、HEAD 和条件请求都先完成同一套授权与版本检查。公开响应没有来源 ETag，
	// 因此不按 If-None-Match 快捷返回，也不把内部文件名或资产哈希带回浏览器。
	setHeaders := func() {
		w.Header().Set("Content-Type", resource.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(resource.Size, 10))
		w.Header().Set("Content-Disposition", `inline; filename="answer.png"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	if r.Method == http.MethodHead {
		setHeaders()
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := resource.Open(r.Context())
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "object_storage_failed", "failed to read answer image")
		return
	}
	defer body.Close()
	setHeaders()
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}
func writeEvidenceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrEvidenceUnavailable) || errors.Is(err, segment.ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, "review_bookmark_evidence_unavailable", "answer evidence is unavailable")
		return
	}
	writeError(w, r, err)
}
