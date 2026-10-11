package exam

import (
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) managementArchiveStore(w http.ResponseWriter, r *http.Request) (ManagementArchiveStore, bool) {
	store, ok := h.store.(ManagementArchiveStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "exam_archive_unavailable", "整场考试归档服务未配置")
	}
	return store, ok
}

func (h *Handler) CheckManagementArchive(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementArchiveStore(w, r)
	if !ok {
		return
	}
	check, err := store.CheckManagementArchive(r.Context(), scope, r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"check": check})
}

func (h *Handler) ArchiveManagementExam(w http.ResponseWriter, r *http.Request) {
	h.mutateManagementArchive(w, r, "archive")
}

func (h *Handler) RestoreManagementExam(w http.ResponseWriter, r *http.Request) {
	h.mutateManagementArchive(w, r, "restore")
}

func (h *Handler) mutateManagementArchive(w http.ResponseWriter, r *http.Request, action string) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementArchiveStore(w, r)
	if !ok {
		return
	}
	var input ManagementArchiveInput
	if !decodeJSON(w, r, &input) {
		return
	}
	commandID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if input.CommandID == "" {
		input.CommandID = commandID
	} else if commandID != "" && commandID != input.CommandID {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_archive_command", "command_id 必须与 Idempotency-Key 一致")
		return
	}
	if err := validateManagementArchiveInput(input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set(httpx.CommandIDHeader, input.CommandID)
	kind, id := r.PathValue("kind"), r.PathValue("id")
	var item ManagementExam
	var err error
	if action == "restore" {
		item, err = store.RestoreManagementExam(r.Context(), scope, mustUser(r).ID, kind, id, input)
	} else {
		item, err = store.ArchiveManagementExam(r.Context(), scope, mustUser(r).ID, kind, id, input)
	}
	if errors.Is(err, ErrArchiveBlocked) {
		// 拒绝后返回最新原因；检查故障仍保留冲突，不显示为已经归档或允许继续。
		check, checkErr := store.CheckManagementArchive(r.Context(), scope, kind, id)
		if checkErr != nil {
			check = ManagementArchiveCheck{Blockers: []ManagementArchiveBlocker{{Code: "archive_check_unavailable", Message: "暂无法核实归档条件，请刷新后重试"}}}
		}
		w.Header().Set(httpx.ErrorCodeHeader, "exam_archive_blocked")
		httpx.JSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "exam_archive_blocked", "message": "整场考试尚不满足归档条件"}, "check": check})
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// PostgreSQL 审计已与归档回执一起提交；这里不再写第二条可被重试重复的审计记录。
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": item})
}

func (h *Handler) RecoverManagementArchiveCommand(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementArchiveStore(w, r)
	if !ok {
		return
	}
	commandID := r.PathValue("commandID")
	if len(commandID) < 8 || len(commandID) > 128 {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	result, err := store.RecoverManagementArchiveCommand(r.Context(), scope, mustUser(r).ID, commandID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"command": result})
}
