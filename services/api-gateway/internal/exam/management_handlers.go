package exam

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

func (h *Handler) managementStore(w http.ResponseWriter, r *http.Request) (ManagementStore, bool) {
	store, ok := h.store.(ManagementStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "exam_management_unavailable", "考试管理服务未配置")
	}
	return store, ok
}

func (h *Handler) decorateManagementArchive(ctx context.Context, scope auth.AccessScope, item *ManagementExam) {
	if item.archiveCheckResolved {
		return
	}
	store, ok := h.store.(interface {
		CheckManagementArchive(context.Context, auth.AccessScope, string, string) (ManagementArchiveCheck, error)
	})
	if !ok {
		return
	}
	check, err := store.CheckManagementArchive(ctx, scope, item.Kind, item.ID)
	applyManagementArchiveCheck(item, check, err == nil)
}

func (h *Handler) decorateManagementArchives(ctx context.Context, scope auth.AccessScope, items []ManagementExam) {
	pending := []ManagementExam{}
	for _, item := range items {
		if !item.archiveCheckResolved {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return
	}
	if bulk, ok := h.store.(ManagementArchiveBulkChecker); ok {
		checks, err := bulk.CheckManagementArchives(ctx, scope, pending)
		for i := range items {
			if items[i].archiveCheckResolved {
				continue
			}
			check, found := checks[items[i].Kind+":"+items[i].ID]
			applyManagementArchiveCheck(&items[i], check, err == nil && found)
		}
		return
	}
	for i := range items {
		h.decorateManagementArchive(ctx, scope, &items[i])
	}
}

func (h *Handler) ListManagementExams(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementStore(w, r)
	if !ok {
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "分页数量无效")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "分页位置无效")
		return
	}
	archive := r.URL.Query().Get("archive")
	if archive == "" {
		archive = "active"
	}
	if archive != "active" && archive != "archived" && archive != "all" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam_filter", "归档筛选无效")
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !IsValidStatus(status) && status != "ongoing" && status != "partially_published" && status != "unavailable" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam_filter", "考试进度筛选无效")
		return
	}
	out, err := store.ListManagementExams(r.Context(), scope, ManagementListFilter{
		SchoolID: r.URL.Query().Get("school_id"), GradeID: r.URL.Query().Get("grade_id"), Search: strings.TrimSpace(r.URL.Query().Get("search")), ExamType: r.URL.Query().Get("exam_type"), Status: status, Archive: archive,
		Limit: limit + 1, CursorAt: cursor.CreatedAt, CursorID: cursor.ID,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	h.decorateManagementArchives(r.Context(), scope, out)
	httpx.JSON(w, http.StatusOK, map[string]any{"exams": out, "has_more": hasMore, "next_cursor": nextCursor})
}

func (h *Handler) GetManagementExam(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementStore(w, r)
	if !ok {
		return
	}
	out, err := store.GetManagementExam(r.Context(), scope, r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.decorateManagementArchive(r.Context(), scope, &out)
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}

func (h *Handler) UpdateManagementExam(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.managementStore(w, r)
	if !ok {
		return
	}
	var input ManagementUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateManagementUpdate(input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	out, err := store.UpdateManagementExam(r.Context(), scope, r.PathValue("kind"), r.PathValue("id"), input)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			httpx.Error(w, r, http.StatusConflict, "exam_locked", "考试已归档，或相关设置已进入不能修改的阶段")
			return
		}
		writeStoreError(w, r, err)
		return
	}
	// 审计 target_id 使用数据库实体的 UUID；整场与历史单科由 target_type 区分。
	targetType := "exam"
	if out.Kind == "session" {
		targetType = "exam_session"
	}
	h.auditAction(r, "exam_management.updated", targetType, out.ID, "更新整场考试公共设置")
	h.decorateManagementArchive(r.Context(), scope, &out)
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}
