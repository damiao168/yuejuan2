package platformschools

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// 登录前只允许用已知学校代码核对展示名称；公开响应不含内部ID、账号、运营汇总或学校目录。
type PublicSchool struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func (h *Handler) PublicSchool(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if code == "" || utf8.RuneCountInString(code) > 128 || strings.ContainsRune(code, '\x00') {
		httpx.Error(w, r, http.StatusNotFound, "school_not_found", "school was not found")
		return
	}
	item, err := h.service.store.GetPublicSchool(r.Context(), code)
	if errors.Is(err, ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, "school_not_found", "school was not found")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "school_lookup_failed", "school lookup failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"school": item})
}

func (s *MemoryStore) GetPublicSchool(_ context.Context, code string) (PublicSchool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.schools {
		if item.Code == code && item.Code != "platform" && item.Status == "active" {
			return PublicSchool{Code: item.Code, Name: item.Name}, nil
		}
	}
	return PublicSchool{}, ErrNotFound
}

func (s *PostgresStore) GetPublicSchool(ctx context.Context, code string) (PublicSchool, error) {
	var item PublicSchool
	// 不依赖运营投影，刚创建但尚未产生指标的学校也可核对名称；停用/删除和平台租户均不公开。
	err := s.db.QueryRowContext(ctx, `SELECT code,name FROM tenant WHERE code=$1 AND code<>'platform' AND status='active' AND deleted_at IS NULL
AND EXISTS (SELECT 1 FROM school WHERE school.tenant_id=tenant.id AND school.deleted_at IS NULL)`, code).Scan(&item.Code, &item.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicSchool{}, ErrNotFound
	}
	return item, err
}
