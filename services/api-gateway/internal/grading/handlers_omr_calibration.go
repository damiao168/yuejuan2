package grading

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (h *Handler) CreateOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	var input CreateOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.CreateOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_created", "omr_calibration_session", detail.Session.ID, "create deterministic OMR calibration sample")
	httpx.JSON(w, http.StatusCreated, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) ListOMRCalibrations(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	items, err := store.ListOMRCalibrations(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibrations": items})
}

func (h *Handler) GetOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	detail, err := store.GetOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) LabelOMRCalibrationCase(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	var input LabelOMRCalibrationCaseInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.LabelOMRCalibrationCase(r.Context(), user.TenantID, r.PathValue("id"), r.PathValue("caseId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_case_labeled", "omr_calibration_case", r.PathValue("caseId"), "record immutable manual OMR calibration label")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) ApproveOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	var input ApproveOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.ApproveOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_approved", "omr_calibration_session", detail.Session.ID, "approve OMR automatic-confirmation calibration")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) RevokeOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	var input RevokeOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.RevokeOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_revoked", "omr_calibration_session", detail.Session.ID, "revoke OMR automatic-confirmation calibration")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func (h *Handler) DiscardOMRCalibration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	store, ok := h.omrCalibrationStore()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_calibration_unavailable", omrCalibrationStoreRequired)
		return
	}
	var input DiscardOMRCalibrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.DiscardOMRCalibration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_calibration_discarded", "omr_calibration_session", detail.Session.ID, "discard draft OMR calibration without altering its evidence")
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": decorateOMRCalibrationDetail(detail)})
}

func decorateOMRCalibrationDetail(detail OMRCalibrationDetail) OMRCalibrationDetail {
	for index := range detail.Cases {
		detail.Cases[index].SegmentImageURL = "/api/v1/answer-segments/" + detail.Cases[index].AnswerSegmentID + "/image"
		if detail.Cases[index].Matches == nil {
			// 浏览器开发者工具或直接 API 调用不得影响盲标注。本案例取得一次性人工标签后，才展示不可变工作器快照。
			detail.Cases[index].ObservedDecision = ""
			detail.Cases[index].ObservedOptions = nil
			detail.Cases[index].ObservedConfidence = 0
			detail.Cases[index].Measurements = nil
			detail.Cases[index].SampleStratum = ""
		}
	}
	return detail
}
