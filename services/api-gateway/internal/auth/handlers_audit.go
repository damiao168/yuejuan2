package auth

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

func (h *Handler) ListAudits(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	filter, ok := h.auditFilterFromRequest(w, r, limit)
	if !ok {
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	filter.Limit = limit + 1
	filter.CursorCreatedAt = cursor.CreatedAt
	filter.CursorID = cursor.ID
	if scope, ok := AccessScopeFromContext(r.Context()); ok {
		filter.ScopeMode = scope.QueryMode()
	}
	if scope, ok := AccessScopeFromContext(r.Context()); ok {
		filter.ScopeMode = scope.QueryMode()
	}
	records, err := h.store.ListAudits(r.Context(), user.TenantID, filter)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "audit_lookup_failed", "failed to list audit logs")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	nextCursor := ""
	if hasMore && len(records) > 0 {
		last := records[len(records)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	records = RedactAuditRecords(records)
	httpx.JSON(w, http.StatusOK, map[string]any{"audit_logs": records, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) ExportAudits(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	filter, ok := h.auditFilterFromRequest(w, r, 200)
	if !ok {
		return
	}
	if scope, ok := AccessScopeFromContext(r.Context()); ok {
		filter.ScopeMode = scope.QueryMode()
	}
	if scope, ok := AccessScopeFromContext(r.Context()); ok {
		filter.ScopeMode = scope.QueryMode()
	}
	records, err := h.store.ListAudits(r.Context(), user.TenantID, filter)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "audit_export_failed", "failed to export audit logs")
		return
	}
	records = RedactAuditRecords(records)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade audit export tenant=%s actor=%s at=%s", user.TenantID, user.ID, exportedAt)
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"id", "tenant_id", "actor_id", "action", "target_type", "target_id", "before_value", "after_value", "reason", "ip_address", "user_agent", "request_id", "created_at", "watermark"})
	for _, record := range records {
		// 审计内容含用户输入；导出前逐单元格处理，避免被表格软件解释成公式。
		_ = writer.Write(csvsafe.Row([]string{
			record.ID,
			record.TenantID,
			record.ActorID,
			record.Action,
			record.TargetType,
			record.TargetID,
			jsonString(record.BeforeValue),
			jsonString(record.AfterValue),
			record.Reason,
			record.IPAddress,
			record.UserAgent,
			record.RequestID,
			record.CreatedAt.Format(time.RFC3339),
			watermark,
		}))
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "audit_export_failed", "failed to build audit export")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     "audit.exported",
		TargetType: "audit_log",
		Reason:     "export audit logs",
		IPAddress:  h.remoteIP(r),
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-logs.csv"`)
	w.Header().Set("X-EduGrade-Watermark", watermark)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

func (h *Handler) auditFilterFromRequest(w http.ResponseWriter, r *http.Request, defaultLimit int) (AuditFilter, bool) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_audit_limit", "audit limit is invalid")
			return AuditFilter{}, false
		}
		limit = parsed
	}
	createdFrom, ok := parseAuditTime(w, r, "created_from")
	if !ok {
		return AuditFilter{}, false
	}
	createdTo, ok := parseAuditTime(w, r, "created_to")
	if !ok {
		return AuditFilter{}, false
	}
	return AuditFilter{
		Action:      r.URL.Query().Get("action"),
		ActorID:     r.URL.Query().Get("actor_id"),
		TargetType:  r.URL.Query().Get("target_type"),
		TargetID:    r.URL.Query().Get("target_id"),
		ExamID:      r.URL.Query().Get("exam_id"),
		IPAddress:   r.URL.Query().Get("ip_address"),
		CreatedFrom: createdFrom,
		CreatedTo:   createdTo,
		Limit:       limit,
	}, true
}

func parseAuditTime(w http.ResponseWriter, r *http.Request, key string) (time.Time, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" && key == "created_from" {
		raw = r.URL.Query().Get("from")
	}
	if raw == "" && key == "created_to" {
		raw = r.URL.Query().Get("to")
	}
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		if dateOnly, dateErr := time.Parse("2006-01-02", raw); dateErr == nil {
			// 无时区的日期按 UTC 解析；截止日期包含该日最后一个纳秒。
			if key == "created_to" {
				return dateOnly.Add(24*time.Hour - time.Nanosecond), true
			}
			return dateOnly, true
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_audit_time", "audit time filter is invalid")
		return time.Time{}, false
	}
	return parsed, true
}

func jsonString(value map[string]any) string {
	if len(value) == 0 {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}
