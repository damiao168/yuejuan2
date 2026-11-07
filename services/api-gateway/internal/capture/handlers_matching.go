package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (h *Handler) GetMatchingQueue(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	queue, err := h.store.GetMatchingQueue(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, queue)
}

func (h *Handler) ConfirmStudentMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ConfirmStudentMatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmStudentMatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.student_match_confirmed", "submission", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"submission": out})
}

func (h *Handler) MarkStudentUnknown(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input MarkStudentUnknownInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.MarkStudentUnknown(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.student_marked_unknown", "submission", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"submission": out})
}

func (h *Handler) ConfirmPageMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ConfirmPageMatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmPageMatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_match_confirmed", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}

func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input PageLifecycleInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.DeletePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_deleted", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}

func (h *Handler) RestorePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input PageLifecycleInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.RestorePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_restored", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}

func (h *Handler) SplitSubmission(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input SplitSubmissionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.SplitSubmission(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.submission_split", "capture_batch", out.BatchID, input.Reason)
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) MergeSubmissions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input MergeSubmissionsInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.MergeSubmissions(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.submissions_merged", "capture_batch", out.BatchID, input.Reason)
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.ListPages(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pages": out})
}

func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdatePageInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.UpdatePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_page.updated", "capture_page", out.ID, "update capture page order or rotation")
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}
