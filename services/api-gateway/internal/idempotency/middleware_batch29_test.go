package idempotency

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type batch29FaultStore struct {
	Store
	events      []string
	beginErr    error
	completeErr error
	input       BeginInput
	headers     map[string]string
}

func (s *batch29FaultStore) Begin(ctx context.Context, input BeginInput) (Record, bool, error) {
	s.events = append(s.events, "begin")
	s.input = input
	if s.beginErr != nil {
		return Record{}, false, s.beginErr
	}
	return s.Store.Begin(ctx, input)
}

func (s *batch29FaultStore) Complete(ctx context.Context, input BeginInput, status int, headers map[string]string, body []byte) error {
	s.events = append(s.events, "complete")
	s.headers = cloneHeaders(headers)
	if s.completeErr != nil {
		return s.completeErr
	}
	return s.Store.Complete(ctx, input, status, headers, body)
}

func (s *batch29FaultStore) Abort(ctx context.Context, input BeginInput) error {
	s.events = append(s.events, "abort")
	return s.Store.Abort(ctx, input)
}

func batch29Request(handler http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exams", strings.NewReader(`{"name":"A"}`))
	req.Header.Set(Header, "batch29-command")
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{TenantID: "tenant-a", ID: "actor-a"}))
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, req)
	return result
}

func TestBatch29MiddlewareResponsePersistenceOrder(t *testing.T) {
	cases := []idempotencyPersistenceCase{
		{"success-at-limit", 201, "abc", nil, 201, "complete", ""},
		{"stable-client-error", 400, "abc", nil, 400, "complete", ""},
		{"oversize-success", 201, "abcd", nil, 500, "abort", "idempotency_response_too_large"},
		{"persist-failed", 201, "abc", errors.New("save unavailable"), 503, "complete", "idempotency_persist_failed"},
		{"timeout-before-size", 408, "abcd", nil, 408, "abort", ""},
		{"conflict-before-size", 409, "abcd", nil, 409, "abort", ""},
		{"too-early-before-size", 425, "abcd", nil, 425, "abort", ""},
		{"authentication-before-size", 428, "abcd", nil, 428, "abort", ""},
		{"rate-limit-before-size", 429, "abcd", nil, 429, "abort", ""},
		{"server-failure-before-size", 503, "abcd", nil, 503, "abort", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertIdempotencyResponsePersistenceOrder(t, tc)
		})
	}
}

func TestBatch29MiddlewareBeginErrorsNeverExecuteBusiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
		retry  string
	}{
		{"conflict", ErrKeyConflict, 409, "idempotency_key_reused_with_different_request", ""},
		{"in-progress", ErrInProgress, 409, "operation_in_progress", "2"},
		{"unavailable", errors.New("begin unavailable"), 503, "idempotency_unavailable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &batch29FaultStore{Store: NewMemoryStore(), beginErr: tc.err}
			mux := http.NewServeMux()
			mux.Handle("POST /api/v1/exams", Middleware(store, Options{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("failed Begin must not execute business")
			})))
			got := batch29Request(mux)
			if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) || got.Header().Get("Retry-After") != tc.retry || !reflect.DeepEqual(store.events, []string{"begin"}) {
				t.Fatalf("begin error: %d %#v %s events=%v", got.Code, got.Header(), got.Body.String(), store.events)
			}
		})
	}
}

func TestBatch29MiddlewareReplayKeepsOnlyStoredHeaders(t *testing.T) {
	store := &batch29FaultStore{Store: NewMemoryStore()}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams", Middleware(store, Options{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		store.events = append(store.events, "handler")
		for name, value := range map[string]string{"Content-Type": "application/json", "ETag": "version-a", "Location": "/exams/a", "X-EduGrade-Watermark": "teacher-a", "X-Transient": "first-only"} {
			w.Header().Set(name, value)
		}
		w.Header().Add("X-Multi", "one")
		w.Header().Add("X-Multi", "two")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"a"}`))
	})))
	first, replay := batch29Request(mux), batch29Request(mux)
	wantHeaders := map[string]string{"Content-Type": "application/json", "ETag": "version-a", "Location": "/exams/a", "X-EduGrade-Watermark": "teacher-a"}
	if first.Code != 201 || replay.Code != 201 || first.Body.String() != replay.Body.String() || !reflect.DeepEqual(store.headers, wantHeaders) || !reflect.DeepEqual(store.events, []string{"begin", "handler", "complete", "begin"}) {
		t.Fatalf("replay: first=%d replay=%d events=%v headers=%v", first.Code, replay.Code, store.events, store.headers)
	}
	if first.Header().Get("Idempotency-Replayed") != "false" || replay.Header().Get("Idempotency-Replayed") != "true" || !reflect.DeepEqual(first.Header().Values("X-Multi"), []string{"one", "two"}) || replay.Header().Get("X-Multi") != "" || replay.Header().Get("X-Transient") != "" {
		t.Fatalf("replay headers: first=%#v replay=%#v", first.Header(), replay.Header())
	}
	for name, value := range wantHeaders {
		if replay.Header().Get(name) != value {
			t.Fatalf("replay %s = %q, want %q", name, replay.Header().Get(name), value)
		}
	}
}

func TestBatch29MemoryTakeoverRequiresStrictStalenessAndKeepsBody(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []idempotencyTakeoverCase{
		{"disabled", false, now.Add(time.Minute), "same", ErrInProgress},
		{"zero-boundary", true, time.Time{}, "same", ErrInProgress},
		{"equal-boundary", true, now, "same", ErrInProgress},
		{"newer-record", true, now.Add(-time.Minute), "same", ErrInProgress},
		{"stale-record", true, now.Add(time.Minute), "same", nil},
		{"fingerprint-before-takeover", true, now.Add(time.Minute), "changed", ErrKeyConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertIdempotencyTakeoverStalenessAndBody(t, tc, now)
		})
	}
}

type idempotencyPersistenceCase struct {
	name        string
	status      int
	body        string
	completeErr error
	wantStatus  int
	wantEvent   string
	wantCode    string
}

func assertIdempotencyResponsePersistenceOrder(t *testing.T, tc idempotencyPersistenceCase) {
	t.Helper()
	// Store 只注入持久化故障，业务入口和响应顺序仍走真实 Middleware；不是数据库故障恢复证明。
	store := &batch29FaultStore{Store: NewMemoryStore(), completeErr: tc.completeErr}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams", Middleware(store, Options{Enforce: true, MaxResponseBytes: 3})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		store.events = append(store.events, "handler")
		w.Header().Set("ETag", "version-a")
		w.WriteHeader(tc.status)
		_, _ = w.Write([]byte(tc.body))
	})))
	got := batch29Request(mux)
	if got.Code != tc.wantStatus || !reflect.DeepEqual(store.events, []string{"begin", "handler", tc.wantEvent}) {
		t.Fatalf("response=%d events=%v", got.Code, store.events)
	}
	if tc.wantCode != "" {
		if !strings.Contains(got.Body.String(), tc.wantCode) || got.Header().Get("Idempotency-Replayed") != "" {
			t.Fatalf("failure response: %#v %s", got.Header(), got.Body.String())
		}
	} else if got.Body.String() != tc.body || got.Header().Get("ETag") != "version-a" || got.Header().Get("Idempotency-Replayed") != "false" {
		t.Fatalf("original response: %#v %s", got.Header(), got.Body.String())
	}
	if store.input.TenantID != "tenant-a" || store.input.ActorID != "actor-a" || store.input.Route != "POST /api/v1/exams" || store.input.AllowTakeover {
		t.Fatalf("input identity: %#v", store.input)
	}
}

type idempotencyTakeoverCase struct {
	name        string
	allow       bool
	staleBefore time.Time
	hash        string
	wantErr     error
}

func assertIdempotencyTakeoverStalenessAndBody(t *testing.T, tc idempotencyTakeoverCase, now time.Time) {
	t.Helper()
	store := NewMemoryStore()
	input := BeginInput{TenantID: "tenant-a", ActorID: "actor-a", Method: "POST", Route: "route", Key: "command", RequestHash: tc.hash, RequestBody: []byte("new-body"), ExpiresAt: now.Add(time.Hour), AllowTakeover: tc.allow, StaleBefore: tc.staleBefore}
	key := memoryKey(input)
	original := memoryEntry{record: Record{State: "processing", RequestHash: "same", RequestBody: []byte("original-body"), UpdatedAt: now}, updatedAt: now, expiresAt: now.Add(time.Hour)}
	// 固定存储时间覆盖严格 Before 的等值边界，不依赖 sleep 或运行速度。
	store.records[key] = original
	record, execute, err := store.Begin(context.Background(), input)
	if !errors.Is(err, tc.wantErr) || execute != (tc.wantErr == nil) {
		t.Fatalf("begin: %#v execute=%v err=%v", record, execute, err)
	}
	if tc.wantErr != nil {
		if !reflect.DeepEqual(store.records[key], original) || !reflect.DeepEqual(record, Record{}) {
			t.Fatal("failed takeover changed the record or exposed its body")
		}
	} else {
		if string(record.RequestBody) != "original-body" || record.UpdatedAt.Before(now) || !store.records[key].expiresAt.Equal(input.ExpiresAt) {
			t.Fatalf("takeover record: %#v", record)
		}
		record.RequestBody[0] = 'X'
		if string(store.records[key].record.RequestBody) != "original-body" {
			t.Fatal("takeover result body aliases stored request")
		}
	}
}
