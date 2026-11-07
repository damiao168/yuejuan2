package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
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

func (h *Handler) DownloadStudentPrintPackage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetStudentPrintPackage(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload, digest, err := RenderStudentPrintPackage(out)
	if err != nil {
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
