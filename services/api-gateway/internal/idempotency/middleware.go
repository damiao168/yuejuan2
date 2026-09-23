package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const Header = "Idempotency-Key"

const maxPersistedCommandBodyBytes = 2 * 1024 * 1024

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var protectedRoutes = map[string]bool{
	"POST /api/v1/exams":                                        true,
	"POST /api/v1/exam-sessions":                                true,
	"POST /api/v1/exams/{examId}/paper-imports":                 true,
	"POST /api/v1/paper-imports/{id}/sources":                   true,
	"PUT /api/v1/paper-imports/{id}/sources":                    true,
	"POST /api/v1/paper-imports/{id}/cancel":                    true,
	"POST /api/v1/exams/{examId}/capture-batches":               true,
	"POST /api/v1/capture-batches/{id}/files":                   true,
	"POST /api/v1/capture-batches/{id}/process":                 true,
	"POST /api/v1/exams/{examId}/submissions":                   true,
	"POST /api/v1/submissions/{id}/ocr-tasks":                   true,
	"POST /api/v1/exams/{examId}/scoring-runs":                  true,
	"POST /api/v1/subjective-grading-batches":                   true,
	"POST /api/v1/subjective-grading-batches/{batchId}/enqueue": true,
	"POST /api/v1/review-tasks":                                 true,
	"POST /api/v1/review-tasks/next":                            true,
	"POST /api/v1/review-tasks/{id}/submit":                     true,
	"POST /api/v1/arbitration-tasks":                            true,
	"POST /api/v1/arbitration-tasks/{id}/submit":                true,
	"POST /api/v1/exams/{examId}/confirm-grades":                true,
	"POST /api/v1/exams/{examId}/publish":                       true,
	"POST /api/v1/appeals":                                      true,
	"POST /api/v1/appeals/{id}/recommendation":                  true,
	"POST /api/v1/answer-sheet-templates/{id}/student-barcodes": true,
	"POST /api/v1/answer-sheet-print-sheets/{id}/reprint":       true,
	"POST /api/v1/internal/worker/tasks/{taskId}/complete":      true,
	"POST /api/v1/internal/worker/tasks/{taskId}/fail":          true,
	"POST /api/v1/exams/{examId}/reports/export":                true,
	"POST /api/v1/files":                                        true,
}

var recoverableRoutes = map[string]bool{
	// Every route in this allowlist has a durable business identity, request
	// fingerprint, result/recovery relationship, and concurrent replay tests.
	// Do not broaden this to all protected routes.
	"POST /api/v1/exam-sessions":                                true,
	"POST /api/v1/exams/{examId}/capture-batches":               true,
	"POST /api/v1/exams/{examId}/scoring-runs":                  true,
	"POST /api/v1/subjective-grading-batches":                   true,
	"POST /api/v1/subjective-grading-batches/{batchId}/enqueue": true,
	"POST /api/v1/review-tasks/{id}/submit":                     true,
	"POST /api/v1/arbitration-tasks/{id}/submit":                true,
	"POST /api/v1/exams/{examId}/confirm-grades":                true,
	"POST /api/v1/exams/{examId}/publish":                       true,
	"POST /api/v1/exams/{examId}/reports/export":                true,
}

type Options struct {
	Enforce           bool
	TTL               time.Duration
	ProcessingTimeout time.Duration
	MaxResponseBytes  int
}

func Middleware(store Store, options Options) func(http.Handler) http.Handler {
	if options.TTL <= 0 {
		options.TTL = 24 * time.Hour
	}
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = 2 * 1024 * 1024
	}
	if options.ProcessingTimeout <= 0 {
		options.ProcessingTimeout = commandreceipt.TransportProcessingTimeout
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := r.Pattern
			key := strings.TrimSpace(r.Header.Get(Header))
			protected := protectedRoutes[route]
			if key == "" && (!options.Enforce || !protected) {
				next.ServeHTTP(w, r)
				return
			}
			if key == "" {
				httpx.Error(w, r, http.StatusBadRequest, "idempotency_key_required", "this operation requires an Idempotency-Key")
				return
			}
			if !keyPattern.MatchString(key) {
				httpx.Error(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key format is invalid")
				return
			}
			user, ok := auth.UserFromContext(r.Context())
			if !ok || user.TenantID == "" || user.ID == "" {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			hash, requestBody, cleanup, err := prepareRequestHash(r, route, recoverableRoutes[route])
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
					return
				}
				httpx.Error(w, r, http.StatusBadRequest, "request_body_invalid", "request body could not be read")
				return
			}
			defer cleanup()
			now := time.Now().UTC()
			input := BeginInput{
				TenantID: user.TenantID, ActorID: user.ID, Method: r.Method, Route: route, Key: key,
				RequestHash: hash, RequestBody: requestBody, ExpiresAt: now.Add(options.TTL), AllowTakeover: recoverableRoutes[route],
				StaleBefore: now.Add(-options.ProcessingTimeout),
			}
			record, execute, err := store.Begin(r.Context(), input)
			if err != nil {
				switch err {
				case ErrKeyConflict:
					httpx.Error(w, r, http.StatusConflict, "idempotency_key_reused_with_different_request", "Idempotency-Key was already used for another request")
				case ErrInProgress:
					w.Header().Set("Retry-After", "2")
					httpx.Error(w, r, http.StatusConflict, "operation_in_progress", "the operation is still processing")
				default:
					httpx.Error(w, r, http.StatusServiceUnavailable, "idempotency_unavailable", "safe request retry is temporarily unavailable")
				}
				return
			}
			if !execute {
				writeReplay(w, record)
				return
			}

			captured := httptest.NewRecorder()
			next.ServeHTTP(captured, r)
			body := captured.Body.Bytes()
			if retryableStatus(captured.Code) {
				_ = store.Abort(r.Context(), input)
				copyResponse(w, captured.Header(), captured.Code, body, "false")
				return
			}
			if len(body) > options.MaxResponseBytes {
				_ = store.Abort(r.Context(), input)
				httpx.Error(w, r, http.StatusInternalServerError, "idempotency_response_too_large", "operation response exceeded the safe replay limit")
				return
			}
			headers := replayHeaders(captured.Header())
			if err := store.Complete(r.Context(), input, captured.Code, headers, body); err != nil {
				httpx.Error(w, r, http.StatusServiceUnavailable, "idempotency_persist_failed", "operation completed but its safe retry record could not be confirmed")
				return
			}
			copyResponse(w, captured.Header(), captured.Code, body, "false")
		})
	}
}

func prepareRequestHash(r *http.Request, route string, persistBody bool) (string, []byte, func(), error) {
	temp, err := os.CreateTemp("", "edugrade-idempotency-*")
	if err != nil {
		return "", nil, func() {}, err
	}
	cleanup := func() { _ = temp.Close(); _ = os.Remove(temp.Name()) }
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, r.Method+"\n"+route+"\n"+r.URL.Path+"?"+r.URL.RawQuery+"\n")
	if r.Body != nil {
		if _, err = io.Copy(io.MultiWriter(temp, hasher), r.Body); err != nil {
			cleanup()
			return "", nil, func() {}, err
		}
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return "", nil, func() {}, err
	}
	var requestBody []byte
	if persistBody {
		requestBody, err = io.ReadAll(io.LimitReader(temp, maxPersistedCommandBodyBytes+1))
		if err != nil || len(requestBody) > maxPersistedCommandBodyBytes {
			cleanup()
			if err == nil {
				err = errors.New("recoverable command body exceeds persistence limit")
			}
			return "", nil, func() {}, err
		}
		// Bodyless commands still need an explicit immutable payload so recovery
		// never has to infer whether a payload was lost.
		if len(requestBody) == 0 {
			requestBody = []byte("null")
		}
		if !json.Valid(requestBody) {
			cleanup()
			return "", nil, func() {}, errors.New("recoverable command body is not valid JSON")
		}
		if _, err = temp.Seek(0, io.SeekStart); err != nil {
			cleanup()
			return "", nil, func() {}, err
		}
	}
	r.Body = temp
	return hex.EncodeToString(hasher.Sum(nil)), requestBody, cleanup, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooEarly || status == http.StatusPreconditionRequired || status == http.StatusTooManyRequests || status >= 500
}

func replayHeaders(header http.Header) map[string]string {
	out := map[string]string{}
	for _, name := range []string{"Content-Type", "ETag", "Location", "X-EduGrade-Watermark"} {
		if value := header.Get(name); value != "" {
			out[name] = value
		}
	}
	return out
}

func writeReplay(w http.ResponseWriter, record Record) {
	for key, value := range record.ResponseHeaders {
		w.Header().Set(key, value)
	}
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(record.ResponseStatus)
	_, _ = w.Write(record.ResponseBody)
}

func copyResponse(w http.ResponseWriter, header http.Header, status int, body []byte, replayed string) {
	for key, values := range header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Idempotency-Replayed", replayed)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
