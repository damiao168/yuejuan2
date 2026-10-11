package processing

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"net/http"
)

type ExceptionContext struct {
	Exception      Exception `json:"exception"`
	SubmissionID   string    `json:"submission_id"`
	CandidateNo    string    `json:"candidate_no"`
	PageNo         int       `json:"page_no"`
	CaptureBatchID string    `json:"capture_batch_id,omitempty"`
	CapturePageID  string    `json:"capture_page_id,omitempty"`
	FileAssetID    string    `json:"-"`
	ImageURL       string    `json:"image_url"`
}
type ExceptionContextSource interface {
	GetExceptionContext(context.Context, string, string) (ExceptionContext, error)
}

func (s *PostgresStore) GetExceptionContext(ctx context.Context, tenantID, id string) (ExceptionContext, error) {
	item, err := s.GetException(ctx, tenantID, id)
	if err != nil {
		return ExceptionContext{}, err
	}
	out := ExceptionContext{Exception: item, ImageURL: "/api/v1/processing/exceptions/" + id + "/page-image"}
	err = s.db.QueryRowContext(ctx, `SELECT sub.id::text,COALESCE(sub.candidate_no,''),page.page_no,COALESCE(cp.capture_batch_id::text,''),COALESCE(cp.id::text,''),page.file_asset_id::text
FROM operational_exception ex JOIN submission_page page ON page.tenant_id=ex.tenant_id AND page.id=ex.page_id AND page.deleted_at IS NULL
JOIN submission sub ON sub.tenant_id=page.tenant_id AND sub.id=page.submission_id AND sub.exam_id=ex.exam_id AND sub.deleted_at IS NULL
LEFT JOIN capture_page cp ON cp.tenant_id=page.tenant_id AND cp.submission_page_id=page.id AND cp.status<>'deleted'
WHERE ex.tenant_id=$1::uuid AND ex.id=$2::uuid`, tenantID, id).Scan(&out.SubmissionID, &out.CandidateNo, &out.PageNo, &out.CaptureBatchID, &out.CapturePageID, &out.FileAssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return ExceptionContext{}, ErrNotFound
	}
	return out, err
}
func (h *Handler) WithFileDownload(reader files.DownloadReader) *Handler {
	h.download = reader
	return h
}
func (h *Handler) GetExceptionContext(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	source, ok := h.service.store.(ExceptionContextSource)
	if !ok {
		writeError(w, r, ErrNotFound)
		return
	}
	value, err := source.GetExceptionContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exception_context": value})
}
func (h *Handler) GetExceptionImage(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	source, ok := h.service.store.(ExceptionContextSource)
	if !ok || h.download == nil {
		writeError(w, r, ErrNotFound)
		return
	}
	value, err := source.GetExceptionContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	resource, err := h.download.ReadDownload(r.Context(), user, value.FileAssetID)
	if err != nil {
		files.WriteDownloadError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}
