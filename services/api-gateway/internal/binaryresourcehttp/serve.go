package binaryresourcehttp

import (
	"io"
	"net/http"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// Serve writes the same private-object response for direct and delegated
// routes, after the caller has completed its own authorization checks.
func Serve(w http.ResponseWriter, r *http.Request, resource binaryresource.Resource, audit auth.AuditRecorder) {
	if resource.ETag != "" {
		w.Header().Set("ETag", resource.ETag)
	}
	if resource.CacheControl != "" {
		w.Header().Set("Cache-Control", resource.CacheControl)
	}
	if resource.ETag != "" {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	if resource.ETag != "" && r.Header.Get("If-None-Match") == resource.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if resource.ETag != "" {
		setContentHeaders(w, resource)
		if resource.AllowHead && r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	body, err := resource.Open(r.Context())
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "object_storage_failed", resource.OpenErrorMessage)
		return
	}
	defer body.Close()
	user, _ := auth.UserFromContext(r.Context())
	auth.RecordAudit(r.Context(), audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID,
		Action: resource.AuditAction, TargetType: resource.AuditTargetType,
		TargetID: resource.AuditTargetID, Reason: resource.AuditReason,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	if resource.ETag == "" {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		setContentHeaders(w, resource)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

func setContentHeaders(w http.ResponseWriter, resource binaryresource.Resource) {
	w.Header().Set("Content-Type", resource.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(resource.Size, 10))
	w.Header().Set("Content-Disposition", resource.Disposition)
}
