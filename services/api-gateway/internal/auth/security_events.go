package auth

import (
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const personalSecurityEventLimit = 20

type SecurityEvent struct {
	ID            string    `json:"id"`
	EventType     string    `json:"event_type"`
	RiskLevel     string    `json:"risk_level"`
	DeviceSummary string    `json:"device_summary"`
	OccurredAt    time.Time `json:"occurred_at"`
}

var personalSecurityActions = map[string]struct{}{
	"auth.login_succeeded":               {},
	"auth.login_failed":                  {},
	"auth.login_rate_limited":            {},
	"auth.logout":                        {},
	"auth.password_changed":              {},
	"auth.session_revoked":               {},
	"auth.sessions_revoked_all":          {},
	"auth.account_activated":             {},
	"auth.credential_recovery_completed": {},
	"auth.reauthentication_failed":       {},
	"auth.session_reauthenticated":       {},
	"auth.session_locked":                {},
	"auth.mfa_enabled":                   {},
	"auth.mfa_disabled":                  {},
	"auth.mfa_recovery_used":             {},
	"auth.mfa_recovery_rotated":          {},
}

// ListSecurityEvents 将已有审计轨迹投影为仅供本人查看的有限视图；原始 IP、用户代理、请求 ID 和审计前后载荷不会通过此端点返回。
func (h *Handler) ListSecurityEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	records, err := h.store.ListAudits(r.Context(), user.TenantID, AuditFilter{
		ActorID:   user.ID,
		Limit:     100,
		ScopeMode: "tenant",
	})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "security_event_lookup_failed", "failed to list security events")
		return
	}
	events := make([]SecurityEvent, 0, personalSecurityEventLimit)
	for _, record := range records {
		if _, allowed := personalSecurityActions[record.Action]; !allowed {
			continue
		}
		events = append(events, SecurityEvent{
			ID:            record.ID,
			EventType:     record.Action,
			RiskLevel:     securityEventRisk(record.AfterValue),
			DeviceSummary: coarseDeviceSummary(record.UserAgent),
			OccurredAt:    record.CreatedAt,
		})
		if len(events) == personalSecurityEventLimit {
			break
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": events})
}

func securityEventRisk(afterValue map[string]any) string {
	value, _ := afterValue["risk_level"].(string)
	switch value {
	case "medium", "high":
		return value
	default:
		return "low"
	}
}

func coarseDeviceSummary(userAgent string) string {
	value := strings.ToLower(userAgent)
	switch {
	case strings.Contains(value, "iphone"), strings.Contains(value, "ipad"), strings.Contains(value, "android"), strings.Contains(value, "mobile"):
		return "移动设备"
	case strings.Contains(value, "windows"):
		return "Windows 设备"
	case strings.Contains(value, "macintosh"), strings.Contains(value, "mac os"):
		return "Mac 设备"
	case strings.Contains(value, "linux"):
		return "Linux 设备"
	default:
		return "未知设备"
	}
}
