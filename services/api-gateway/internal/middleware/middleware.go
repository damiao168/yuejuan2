package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Middleware func(http.Handler) http.Handler

const CSRFHeaderName = "X-EduGrade-CSRF"

var (
	requestIDPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{16,64}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9A-HJKMNP-TV-Z]{26})$`)
	traceIDPattern   = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers := w.Header()
			headers.Set("X-Content-Type-Options", "nosniff")
			headers.Set("X-Frame-Options", "DENY")
			headers.Set("Referrer-Policy", "no-referrer")
			headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			headers.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			next.ServeHTTP(w, r)
		})
	}
}

func BodyLimit(maxBytes int64, skip func(*http.Request) bool) Middleware {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && requestMayHaveBody(r.Method) && (skip == nil || !skip(r)) {
				if r.ContentLength > maxBytes {
					httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CORS(allowedOrigins []string, allowedMethods []string, allowedHeaders []string) Middleware {
	origins := normalizedSet(allowedOrigins)
	methods := strings.Join(nonEmptyOrDefault(allowedMethods, []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}), ", ")
	headerValues := nonEmptyOrDefault(allowedHeaders, []string{"Authorization", "Content-Type", "X-Request-ID", "X-Trace-ID", "Idempotency-Key", CSRFHeaderName})
	if !containsFold(headerValues, CSRFHeaderName) {
		headerValues = append(headerValues, CSRFHeaderName)
	}
	headers := strings.Join(headerValues, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !origins[origin] {
				if r.Method == http.MethodOptions {
					httpx.Error(w, r, http.StatusForbidden, "cors_origin_forbidden", "origin is not allowed")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", headers)
			h.Set("Access-Control-Expose-Headers", "Idempotency-Replayed, ETag, Location, X-Request-ID, X-Trace-ID")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Max-Age", "600")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BrowserCSRF requires a non-simple request header for browser-originated
// mutations. Cross-origin pages cannot attach this header unless their CORS
// preflight is explicitly allowed, while bearer-authenticated workers and
// non-browser API clients remain unaffected.
func BrowserCSRF(sessionCookieName string) Middleware {
	sessionCookieName = strings.TrimSpace(sessionCookieName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Match auth's bearer parser. An invalid Authorization header may
			// fall back to ambient cookies and must not exempt that request.
			parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
			hasBearer := len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && strings.TrimSpace(parts[1]) != ""
			if !requestMayHaveBody(r.Method) || r.URL.Path == "/api/v1/auth/token" || hasBearer {
				next.ServeHTTP(w, r)
				return
			}

			_, cookieErr := r.Cookie(sessionCookieName)
			browserRequest := cookieErr == nil || strings.TrimSpace(r.Header.Get("Origin")) != "" || strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) != ""
			if !browserRequest {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get(CSRFHeaderName) != "1" {
				httpx.Error(w, r, http.StatusForbidden, "csrf_validation_failed", "browser mutation requires CSRF protection")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if !requestIDPattern.MatchString(requestID) {
				requestID = newRequestID()
			}
			traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
			if !validTraceID(traceID) {
				traceID = traceIDFromTraceparent(r.Header.Get("Traceparent"))
			}
			if traceID == "" {
				traceID = newRequestID()
			}
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Trace-ID", traceID)
			ctx := logger.WithTraceID(logger.WithRequestID(r.Context(), requestID), traceID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requestMayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func normalizedSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func nonEmptyOrDefault(values []string, fallback []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}

func AccessLog(logg *logger.Logger, slowThresholds ...time.Duration) Middleware {
	var slowThreshold time.Duration
	if len(slowThresholds) > 0 {
		slowThreshold = slowThresholds[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			duration := time.Since(start)
			fields := map[string]any{
				"event":       "api_request",
				"log_stream":  "system",
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      rec.status,
				"duration_ms": duration.Milliseconds(),
				"remote_addr": r.RemoteAddr,
			}
			if errorCode := rec.Header().Get(httpx.ErrorCodeHeader); errorCode != "" {
				fields["error_code"] = errorCode
			}
			if outcome := rec.Header().Get(httpx.OperationOutcomeHeader); outcome != "" {
				fields["operation_outcome"] = outcome
			}
			if commandID := rec.Header().Get(httpx.CommandIDHeader); commandID != "" {
				fields["command_id"] = commandID
			}
			logg.Info(r.Context(), "http request", fields)
			if slowThreshold > 0 && duration >= slowThreshold {
				logg.Warn(r.Context(), "slow request observed", map[string]any{
					"event":        "slow_request",
					"log_stream":   "system",
					"method":       r.Method,
					"path":         r.URL.Path,
					"status":       rec.status,
					"duration_ms":  duration.Milliseconds(),
					"threshold_ms": slowThreshold.Milliseconds(),
				})
			}
		})
	}
}

func Recover(logg *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logg.Error(r.Context(), "panic recovered", map[string]any{
						"event":      "panic_recovered",
						"log_stream": "system",
						"panic_type": fmt.Sprintf("%T", recovered),
						"stack":      string(debug.Stack()),
					})
					httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func traceIDFromTraceparent(header string) string {
	parts := strings.Split(header, "-")
	if len(parts) < 4 {
		return ""
	}
	traceID := parts[1]
	if !validTraceID(traceID) {
		return ""
	}
	return strings.ToLower(traceID)
}

func validTraceID(value string) bool {
	return traceIDPattern.MatchString(value) && value != "00000000000000000000000000000000"
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		fallback := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		return hex.EncodeToString(fallback[:16])
	}
	return hex.EncodeToString(b[:])
}
