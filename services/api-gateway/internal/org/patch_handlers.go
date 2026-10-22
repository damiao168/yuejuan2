package org

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) UpdateGrade(w http.ResponseWriter, r *http.Request) {
	var input GradePatch
	if !decodeOrganizationPatch(w, r, &input) {
		return
	}
	if input.Name == nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if !normalizePatchText(w, r, input.Name, "name", 120, false) {
		return
	}
	out, err := h.store.UpdateGrade(r.Context(), mustUser(r).TenantID, r.PathValue("id"), input)
	if err != nil {
		writeOrganizationPatchError(w, r, err, "grade")
		return
	}
	h.auditAction(r, "org.grade_updated", "grade", out.ID, "update grade information")
	httpx.JSON(w, http.StatusOK, map[string]any{"grade": out})
}

func (h *Handler) UpdateClass(w http.ResponseWriter, r *http.Request) {
	var input ClassPatch
	if !decodeOrganizationPatch(w, r, &input) {
		return
	}
	if input.Name == nil && input.Code == nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "name or code is required")
		return
	}
	if !normalizePatchText(w, r, input.Name, "name", 120, false) || !normalizePatchText(w, r, input.Code, "code", 64, false) {
		return
	}
	out, err := h.store.UpdateClass(r.Context(), mustUser(r).TenantID, r.PathValue("id"), input)
	if err != nil {
		writeOrganizationPatchError(w, r, err, "class")
		return
	}
	h.auditAction(r, "org.class_updated", "class", out.ID, "update class information")
	httpx.JSON(w, http.StatusOK, map[string]any{"class": out})
}

func decodeOrganizationPatch(w http.ResponseWriter, r *http.Request, target any) bool {
	// 归属字段不在补丁类型中，拒绝而不是静默忽略，避免教师误以为已完成转班或迁移。
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body or unsupported editable field")
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "body must contain one json object")
		return false
	}
	return true
}

func normalizePatchText(w http.ResponseWriter, r *http.Request, value *string, field string, limit int, allowEmpty bool) bool {
	if value == nil {
		return true
	}
	*value = strings.TrimSpace(*value)
	if (!allowEmpty && *value == "") || utf8.RuneCountInString(*value) > limit {
		minimum := 1
		if allowEmpty {
			minimum = 0
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s must contain %d to %d characters", field, minimum, limit))
		return false
	}
	return true
}

func writeOrganizationPatchError(w http.ResponseWriter, r *http.Request, err error, resource string) {
	if errors.Is(err, ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, resource+"_not_found", resource+" not found")
		return
	}
	writeStoreError(w, r, err, resource+"_update_failed", "failed to update "+resource)
}
