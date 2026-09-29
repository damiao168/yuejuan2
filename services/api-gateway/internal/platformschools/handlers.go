package platformschools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct{ service *Service }

func NewHandler(store Store) *Handler { return &Handler{service: NewService(store)} }

// 查询参数先在 HTTP 层解析，非法筛选直接返回 400，避免把模糊条件传到统计层。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	limit, err := parseOptionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.service.List(r.Context(), ListFilter{Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Activity: r.URL.Query().Get("activity"), ModelHealth: r.URL.Query().Get("model_health"), UsageDays: parseWindowDays(r.URL.Query().Get("usage_window")), Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, result)
	}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), r.PathValue("tenant_id"))
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, map[string]any{"school": item})
	}
}

func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Members(r.Context(), r.PathValue("tenant_id"), strings.TrimSpace(r.URL.Query().Get("q")), strings.TrimSpace(r.URL.Query().Get("role")), strings.TrimSpace(r.URL.Query().Get("status")))
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, result)
	}
}

func (h *Handler) Usage(w http.ResponseWriter, r *http.Request) {
	rangeValue, err := parseUsageRange(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.service.Usage(r.Context(), r.PathValue("tenant_id"), rangeValue)
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, result)
	}
}

func (h *Handler) ModelHealth(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ModelHealth(r.Context(), r.PathValue("tenant_id"))
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, result)
	}
}
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	limit, err := parseOptionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.service.Activity(r.Context(), r.PathValue("tenant_id"), limit)
	if !writeError(w, r, err) {
		httpx.JSON(w, http.StatusOK, result)
	}
}

func parseOptionalPositiveInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, ErrInvalidFilter
	}
	return parsed, nil
}

func parseWindowDays(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "today", "1d":
		return 1
	case "7d":
		return 7
	case "90d":
		return 90
	case "", "30d":
		return 30
	default:
		return -1
	}
}

func parseUsageRange(r *http.Request) (UsageRange, error) {
	startRaw, endRaw := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if startRaw != "" || endRaw != "" {
		start, err := time.Parse("2006-01-02", startRaw)
		if err != nil {
			return UsageRange{}, ErrInvalidFilter
		}
		end, err := time.Parse("2006-01-02", endRaw)
		if err != nil {
			return UsageRange{}, ErrInvalidFilter
		}
		return UsageRange{Start: start, End: end, Days: int(end.Sub(start).Hours()/24) + 1}, nil
	}
	days := parseWindowDays(r.URL.Query().Get("window"))
	if days < 1 {
		return UsageRange{}, ErrInvalidFilter
	}
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return UsageRange{Start: end.AddDate(0, 0, -days+1), End: end, Days: days}, nil
}

func writeError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrInvalidFilter):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_platform_school_filter", "学校查询条件无效")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "platform_school_not_found", "学校不存在")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "platform_school_query_failed", "学校运营数据加载失败")
	}
	return true
}
