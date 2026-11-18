package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (h *Handler) IssueTemplateBarcodes(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.IssueTemplateBarcodes(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_template.barcodes_issued", "answer_sheet_template", out.TemplateID, "issue controlled page barcodes")
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}

func (h *Handler) IssueStudentBarcodes(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input IssueStudentBarcodesInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.IssueStudentBarcodes(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_template.student_barcodes_issued", "answer_sheet_template", out.TemplateID, "issue student-bound sheet barcodes")
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}

func (h *Handler) GetStudentPrintContext(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetStudentPrintContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"print_context": out})
}

func (h *Handler) DownloadStudentPrintSource(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetStudentPrintSource(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	extension := map[string]string{"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg"}[out.ContentType]
	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Content-Disposition", `inline; filename="edugrade-answer-sheet-source`+extension+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(out.Payload)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", `"`+out.SHA256+`"`)
	w.Header().Set("X-EduGrade-SHA256", "sha256:"+out.SHA256)
	h.auditAction(r, "answer_sheet_template.print_source_downloaded", "answer_sheet_template", r.PathValue("id"), "view verified print source")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Payload)
}

func (h *Handler) ConfirmStudentPrintLayout(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input StudentPrintLayout
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmStudentPrintLayout(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_print_batch.layout_confirmed", "answer_sheet_print_batch", r.PathValue("id"), "confirm print QR positions")
	httpx.JSON(w, http.StatusOK, map[string]any{"qr_layout": out})
}

func (h *Handler) DownloadStudentPrintPackage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetStudentPrintPackage(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, digest, err := RenderStudentPrintPackage(out)
	if err != nil {
		var inputErr printPackageInputError
		if errors.As(err, &inputErr) {
			writeError(w, r, err)
			return
		}
		httpx.Error(w, r, http.StatusInternalServerError, "print_package_failed", "print package could not be rendered")
		return
	}
	fileName := fmt.Sprintf("edugrade-answer-sheets-%s.pdf", out.PrintBatchID)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
	w.Header().Set("ETag", `"`+strings.TrimPrefix(digest, "sha256:")+`"`)
	w.Header().Set("X-EduGrade-SHA256", digest)
	w.Header().Set("Cache-Control", "private, no-store")
	h.auditAction(r, "answer_sheet_print_batch.package_downloaded", "answer_sheet_print_batch", out.PrintBatchID, "download controlled answer sheet package")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (h *Handler) RevokeStudentSheet(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input StudentSheetLifecycleInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.RevokeStudentSheet(
		r.Context(), user.TenantID, r.PathValue("id"), user.ID, input,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_print_sheet.revoked", "answer_sheet_print_sheet", out.SheetSerial, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"sheet": out})
}

func (h *Handler) ReprintStudentSheet(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ReprintStudentSheetInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ReprintStudentSheet(
		r.Context(), user.TenantID, r.PathValue("id"), user.ID, input,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_print_sheet.reprinted", "answer_sheet_print_sheet", r.PathValue("id"), input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}
