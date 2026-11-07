package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	modelProviderInspectionFailed = "failed to inspect model provider"
)

type Handler struct {
	gradingModelProbe       func(context.Context, string, string) (json.RawMessage, error)
	store                   Store
	audit                   auth.AuditRecorder
	secrets                 SecretReferenceResolver
	baseline                LocalBaseline
	prompts                 RuntimePromptSource
	managedAPIProber        ManagedAPIProber
	managedAPIProbeObserver ManagedAPIProbeObserver
	managedAPIChatter       ManagedAPIChatter
	chatMu                  sync.Mutex
	activeChats             map[string]int
}

func NewHandler(store Store, audit auth.AuditRecorder, secrets SecretReferenceResolver, baseline LocalBaseline) *Handler {
	return &Handler{store: store, audit: audit, secrets: secrets, baseline: baseline, managedAPIProber: NewHTTPManagedAPIProber(0)}
}

func (h *Handler) WithRuntimePromptSource(source RuntimePromptSource) *Handler {
	h.prompts = source
	return h
}

func (h *Handler) WithManagedAPIChatter(chatter ManagedAPIChatter) *Handler {
	h.managedAPIChatter = chatter
	return h
}

// 普通用户只能访问自己的租户；平台用户也必须具备对应治理权限，不能仅靠 tenant_id 越权切换。
func (h *Handler) targetTenant(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	user := mustUser(r)
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == user.TenantID {
		return user.TenantID, true
	}
	if user.TenantID == auth.PlatformTenantID &&
		(hasPermission(user, "model:config:manage") ||
			hasPermission(user, "model:provider:manage") ||
			hasPermission(user, "model:evaluation:manage")) {
		return requested, true
	}
	httpx.Error(w, r, http.StatusForbidden, "tenant_scope_forbidden", "target tenant is outside the caller scope")
	return "", false
}

// 学校模型接口按本次操作的细分权限选择学校；不能把读取或面板权限扩展到其他治理接口。
// 旧治理权限沿用原范围检查，外层路由仍负责角色、操作权限和资源边界。
func (h *Handler) targetManagedTenant(w http.ResponseWriter, r *http.Request, requested, permission string) (string, bool) {
	user := mustUser(r)
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != user.TenantID && user.TenantID == auth.PlatformTenantID &&
		auth.HasRole(user, "platform_admin") && hasPermission(user, permission) {
		return requested, true
	}
	return h.targetTenant(w, r, requested)
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string, after map[string]any) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		AfterValue: after,
		Reason:     reason,
		IPAddress:  httpx.ClientIP(r),
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain exactly one json object")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error, code string, message string) {
	switch {
	case errors.Is(err, ErrInvalidProvider), errors.Is(err, ErrInvalidDeployment),
		errors.Is(err, ErrInvalidPolicy), errors.Is(err, ErrInvalidApproval),
		errors.Is(err, ErrInvalidEvaluation), errors.Is(err, ErrInvalidPromotion):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_governance_request", "invalid model governance request")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "model_governance_not_found", "model governance resource not found")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "resource was updated in another session; refresh and retry")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "model_governance_conflict", "model governance resource conflict")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, code, message)
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func hasPermission(user auth.User, expected string) bool {
	for _, permission := range user.Permissions {
		if permission == expected {
			return true
		}
	}
	return false
}

func providerByID(items []Provider, id string) (Provider, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Provider{}, false
}

func deploymentByID(items []Deployment, id string) (Deployment, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Deployment{}, false
}

func deploymentKeysExist(expected []string, items []Deployment) bool {
	available := make(map[string]bool, len(items))
	for _, item := range items {
		available[item.Key] = true
	}
	for _, key := range expected {
		if !available[key] {
			return false
		}
	}
	return true
}

func validProviderStatus(value string) bool {
	return value == "unverified" || value == "active" || value == "degraded" || value == "rate_limited" || value == "disabled"
}

func validDeploymentStatus(value string) bool {
	return value == "unverified" || value == "shadow_only" || value == "disabled"
}

func validHealthState(value string) bool {
	return value == "unverified" || value == "available" || value == "degraded" || value == "rate_limited" || value == "unavailable" || value == "disabled"
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (input ProviderInput) Reason() string {
	if input.Kind == ProviderExternal {
		return "register external provider metadata and secret reference"
	}
	return "register local provider metadata"
}
