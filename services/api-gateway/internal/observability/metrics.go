package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 桶边界单位为秒；Prometheus 直方图是累计计数，一次观测会进入所有不小于它的桶。
var durationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type requestKey struct {
	method string
	route  string
	status int
}

type routeValueKey struct {
	method string
	route  string
	value  string
}

type durationSeries struct {
	count   uint64
	sum     float64
	buckets [len(durationBuckets)]uint64
}

type modelProbeKey struct {
	operation    string
	provider     string
	errorCode    string
	failureStage string
}

// Registry is a small Prometheus text exporter kept inside the gateway so the
// production image has no separate metrics sidecar or global mutable registry.
type Registry struct {
	mu                  sync.RWMutex
	requests            map[requestKey]uint64
	errors              map[routeValueKey]uint64
	outcomes            map[routeValueKey]uint64
	durations           map[[2]string]durationSeries
	dbQueries           map[[2]string]durationSeries
	dbSlow              map[string]uint64
	modelProbes         map[modelProbeKey]uint64
	aiGradingRequests   map[[2]string]uint64
	inFlight            atomic.Int64
	dbStats             func() DatabaseStats
	authLimiterDegraded func() bool
}

type DatabaseStats struct {
	OpenConnections int
	InUse           int
	Idle            int
	WaitCount       int64
	WaitDuration    time.Duration
}

func NewRegistry() *Registry {
	return &Registry{
		requests: map[requestKey]uint64{}, durations: map[[2]string]durationSeries{},
		errors: map[routeValueKey]uint64{}, outcomes: map[routeValueKey]uint64{},
		dbQueries: map[[2]string]durationSeries{}, dbSlow: map[string]uint64{},
		modelProbes: map[modelProbeKey]uint64{},
		aiGradingRequests: map[[2]string]uint64{
			{"v1", "success"}: 0, {"v1", "error"}: 0,
			{"v2", "success"}: 0, {"v2", "error"}: 0,
		},
	}
}

// ObserveAIGradingRequest counts a logical remote grading call after an HTTP
// attempt, including a final contract validation failure. The version is a
// fixed protocol label; request, tenant and model identifiers are never used.
func (r *Registry) ObserveAIGradingRequest(version string, success bool) {
	if r == nil || (version != "v1" && version != "v2") {
		return
	}
	outcome := "error"
	if success {
		outcome = "success"
	}
	r.mu.Lock()
	r.aiGradingRequests[[2]string{version, outcome}]++
	r.mu.Unlock()
}

// ObserveManagedModelProbe records bounded labels only. User-defined provider
// names, endpoints, credentials and upstream response bodies never become labels.
func (r *Registry) ObserveManagedModelProbe(operation, provider, errorCode, failureStage string, ok bool) {
	if r == nil {
		return
	}
	key := modelProbeKey{
		operation:    managedModelProbeOperation(operation),
		provider:     managedModelProbeProvider(provider),
		errorCode:    managedModelProbeErrorCode(errorCode, ok),
		failureStage: managedModelProbeFailureStage(failureStage),
	}
	r.mu.Lock()
	r.modelProbes[key]++
	r.mu.Unlock()
}

func managedModelProbeOperation(value string) string {
	switch value {
	case "create", "validate", "models", "auto_create", "update", "quick", "capability":
		return value
	default:
		return "other"
	}
}

func managedModelProbeProvider(value string) string {
	switch value {
	case "deepseek", "aliyun", "openai", "zhipu", "moonshot", "anthropic", "gemini", "custom":
		return value
	default:
		return "other"
	}
}

func managedModelProbeErrorCode(value string, ok bool) string {
	if ok {
		return "none"
	}
	switch value {
	case "provider_timeout", "provider_unavailable", "provider_rate_limited", "provider_error",
		"provider_invalid_response", "credential_invalid", "model_permission_denied", "model_not_found",
		"invalid_endpoint", "invalid_json", "no_choices", "response_format_unsupported",
		"quick_probe_unsupported":
		return value
	default:
		return "other"
	}
}

func managedModelProbeFailureStage(value string) string {
	switch value {
	case "dns", "connect", "tls", "ttfb", "response", "body", "request":
		return value
	default:
		return "none"
	}
}

func (r *Registry) ObserveDatabaseQuery(operation string, outcome string, duration time.Duration, slow bool) {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		operation = "unknown"
	}
	if outcome != "error" {
		outcome = "ok"
	}
	key := [2]string{operation, outcome}
	seconds := duration.Seconds()
	r.mu.Lock()
	series := r.dbQueries[key]
	series.count++
	series.sum += seconds
	for index, upper := range durationBuckets {
		if seconds <= upper {
			series.buckets[index]++
		}
	}
	r.dbQueries[key] = series
	if slow {
		r.dbSlow[operation]++
	}
	r.mu.Unlock()
}

func (r *Registry) SetDatabaseStats(provider func() DatabaseStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dbStats = provider
}

func (r *Registry) SetAuthRateLimiterDegraded(provider func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authLimiterDegraded = provider
}

func (r *Registry) ObserveRequest(method string, route string, status int, duration time.Duration) {
	r.observeRequest(method, route, status, duration, "", "")
}

func (r *Registry) observeRequest(method string, route string, status int, duration time.Duration, errorCode string, outcome string) {
	if route == "" {
		route = "unmatched"
	}
	key := requestKey{method: method, route: route, status: status}
	durationKey := [2]string{method, route}
	seconds := duration.Seconds()
	r.mu.Lock()
	r.requests[key]++
	if errorCode != "" {
		r.errors[routeValueKey{method: method, route: route, value: errorCode}]++
	}
	if outcome != "" {
		r.outcomes[routeValueKey{method: method, route: route, value: outcome}]++
	}
	series := r.durations[durationKey]
	series.count++
	series.sum += seconds
	for index, upper := range durationBuckets {
		if seconds <= upper {
			series.buckets[index]++
		}
	}
	r.durations[durationKey] = series
	r.mu.Unlock()
}

func (r *Registry) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			started := time.Now()
			r.inFlight.Add(1)
			defer r.inFlight.Add(-1)
			recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, req)
			route := req.Pattern
			if route == "" {
				route = req.URL.Path
			}
			r.observeRequest(req.Method, route, recorder.status, time.Since(started), recorder.Header().Get("X-EduGrade-Error-Code"), recorder.Header().Get("X-EduGrade-Operation-Outcome"))
		})
	}
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	r.mu.RLock()
	requestKeys := make([]requestKey, 0, len(r.requests))
	for key := range r.requests {
		requestKeys = append(requestKeys, key)
	}
	sort.Slice(requestKeys, func(i, j int) bool {
		if requestKeys[i].route != requestKeys[j].route {
			return requestKeys[i].route < requestKeys[j].route
		}
		if requestKeys[i].method != requestKeys[j].method {
			return requestKeys[i].method < requestKeys[j].method
		}
		return requestKeys[i].status < requestKeys[j].status
	})
	durationKeys := make([][2]string, 0, len(r.durations))
	for key := range r.durations {
		durationKeys = append(durationKeys, key)
	}
	sort.Slice(durationKeys, func(i, j int) bool {
		return durationKeys[i][1]+durationKeys[i][0] < durationKeys[j][1]+durationKeys[j][0]
	})
	requests := make(map[requestKey]uint64, len(r.requests))
	for key, value := range r.requests {
		requests[key] = value
	}
	durations := make(map[[2]string]durationSeries, len(r.durations))
	for key, value := range r.durations {
		durations[key] = value
	}
	errors := make(map[routeValueKey]uint64, len(r.errors))
	for key, value := range r.errors {
		errors[key] = value
	}
	outcomes := make(map[routeValueKey]uint64, len(r.outcomes))
	for key, value := range r.outcomes {
		outcomes[key] = value
	}
	dbQueries := make(map[[2]string]durationSeries, len(r.dbQueries))
	for key, value := range r.dbQueries {
		dbQueries[key] = value
	}
	dbSlow := make(map[string]uint64, len(r.dbSlow))
	for key, value := range r.dbSlow {
		dbSlow[key] = value
	}
	modelProbes := make(map[modelProbeKey]uint64, len(r.modelProbes))
	for key, value := range r.modelProbes {
		modelProbes[key] = value
	}
	aiGradingRequests := make(map[[2]string]uint64, len(r.aiGradingRequests))
	for key, value := range r.aiGradingRequests {
		aiGradingRequests[key] = value
	}
	dbStats := r.dbStats
	authLimiterDegraded := r.authLimiterDegraded
	r.mu.RUnlock()
	// 复制指标后再释放锁并生成响应，外部状态回调和网络写出都不占用统计写锁。

	var output strings.Builder
	output.WriteString("# HELP edugrade_http_requests_total Total HTTP requests.\n# TYPE edugrade_http_requests_total counter\n")
	for _, key := range requestKeys {
		fmt.Fprintf(&output, "edugrade_http_requests_total{method=%q,route=%q,status=%q} %d\n", escapeLabel(key.method), escapeLabel(key.route), strconv.Itoa(key.status), requests[key])
	}
	writeRouteValueCounters(&output, "edugrade_http_errors_total", "HTTP errors grouped by stable application error code.", "error_code", errors)
	writeRouteValueCounters(&output, "edugrade_operation_outcomes_total", "Operation recovery outcomes grouped by stable result status.", "outcome", outcomes)
	output.WriteString("# HELP edugrade_managed_model_probes_total Managed model checks by bounded provider and error category.\n# TYPE edugrade_managed_model_probes_total counter\n")
	probeKeys := make([]modelProbeKey, 0, len(modelProbes))
	for key := range modelProbes {
		probeKeys = append(probeKeys, key)
	}
	sort.Slice(probeKeys, func(i, j int) bool {
		a, b := probeKeys[i], probeKeys[j]
		return a.operation+a.provider+a.errorCode+a.failureStage < b.operation+b.provider+b.errorCode+b.failureStage
	})
	for _, key := range probeKeys {
		fmt.Fprintf(&output, "edugrade_managed_model_probes_total{operation=%q,provider=%q,error_code=%q,failure_stage=%q} %d\n",
			key.operation, key.provider, key.errorCode, key.failureStage, modelProbes[key])
	}
	output.WriteString("# HELP edugrade_ai_grading_requests_total Logical remote grading calls by contract version and final outcome.\n# TYPE edugrade_ai_grading_requests_total counter\n")
	for _, version := range []string{"v1", "v2"} {
		for _, outcome := range []string{"success", "error"} {
			fmt.Fprintf(&output, "edugrade_ai_grading_requests_total{ai_contract_version=%q,outcome=%q} %d\n", version, outcome, aiGradingRequests[[2]string{version, outcome}])
		}
	}
	output.WriteString("# HELP edugrade_http_requests_in_flight Current in-flight HTTP requests.\n# TYPE edugrade_http_requests_in_flight gauge\n")
	fmt.Fprintf(&output, "edugrade_http_requests_in_flight %d\n", r.inFlight.Load())
	output.WriteString("# HELP edugrade_http_request_duration_seconds HTTP request duration.\n# TYPE edugrade_http_request_duration_seconds histogram\n")
	for _, key := range durationKeys {
		series := durations[key]
		for index, upper := range durationBuckets {
			fmt.Fprintf(&output, "edugrade_http_request_duration_seconds_bucket{method=%q,route=%q,le=%q} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), strconv.FormatFloat(upper, 'f', -1, 64), series.buckets[index])
		}
		fmt.Fprintf(&output, "edugrade_http_request_duration_seconds_bucket{method=%q,route=%q,le=\"+Inf\"} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), series.count)
		fmt.Fprintf(&output, "edugrade_http_request_duration_seconds_sum{method=%q,route=%q} %g\n", escapeLabel(key[0]), escapeLabel(key[1]), series.sum)
		fmt.Fprintf(&output, "edugrade_http_request_duration_seconds_count{method=%q,route=%q} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), series.count)
	}
	if dbStats != nil {
		stats := dbStats()
		output.WriteString("# TYPE edugrade_postgres_connections gauge\n")
		fmt.Fprintf(&output, "edugrade_postgres_connections{state=\"open\"} %d\n", stats.OpenConnections)
		fmt.Fprintf(&output, "edugrade_postgres_connections{state=\"in_use\"} %d\n", stats.InUse)
		fmt.Fprintf(&output, "edugrade_postgres_connections{state=\"idle\"} %d\n", stats.Idle)
		output.WriteString("# TYPE edugrade_postgres_connection_wait_total counter\n")
		fmt.Fprintf(&output, "edugrade_postgres_connection_wait_total %d\n", stats.WaitCount)
		output.WriteString("# TYPE edugrade_postgres_connection_wait_seconds_total counter\n")
		fmt.Fprintf(&output, "edugrade_postgres_connection_wait_seconds_total %g\n", stats.WaitDuration.Seconds())
	}
	output.WriteString("# HELP edugrade_auth_rate_limiter_degraded Whether distributed authentication rate limiting is degraded.\n# TYPE edugrade_auth_rate_limiter_degraded gauge\n")
	degraded := 0
	if authLimiterDegraded != nil && authLimiterDegraded() {
		degraded = 1
	}
	fmt.Fprintf(&output, "edugrade_auth_rate_limiter_degraded %d\n", degraded)
	output.WriteString("# HELP edugrade_postgres_query_duration_seconds PostgreSQL query duration without SQL text or parameters.\n# TYPE edugrade_postgres_query_duration_seconds histogram\n")
	dbKeys := make([][2]string, 0, len(dbQueries))
	for key := range dbQueries {
		dbKeys = append(dbKeys, key)
	}
	sort.Slice(dbKeys, func(i, j int) bool { return dbKeys[i][0]+dbKeys[i][1] < dbKeys[j][0]+dbKeys[j][1] })
	for _, key := range dbKeys {
		series := dbQueries[key]
		for index, upper := range durationBuckets {
			fmt.Fprintf(&output, "edugrade_postgres_query_duration_seconds_bucket{operation=%q,outcome=%q,le=%q} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), strconv.FormatFloat(upper, 'f', -1, 64), series.buckets[index])
		}
		fmt.Fprintf(&output, "edugrade_postgres_query_duration_seconds_bucket{operation=%q,outcome=%q,le=\"+Inf\"} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), series.count)
		fmt.Fprintf(&output, "edugrade_postgres_query_duration_seconds_sum{operation=%q,outcome=%q} %g\n", escapeLabel(key[0]), escapeLabel(key[1]), series.sum)
		fmt.Fprintf(&output, "edugrade_postgres_query_duration_seconds_count{operation=%q,outcome=%q} %d\n", escapeLabel(key[0]), escapeLabel(key[1]), series.count)
	}
	output.WriteString("# HELP edugrade_postgres_slow_queries_total PostgreSQL queries exceeding the configured threshold.\n# TYPE edugrade_postgres_slow_queries_total counter\n")
	dbSlowKeys := make([]string, 0, len(dbSlow))
	for key := range dbSlow {
		dbSlowKeys = append(dbSlowKeys, key)
	}
	sort.Strings(dbSlowKeys)
	for _, operation := range dbSlowKeys {
		fmt.Fprintf(&output, "edugrade_postgres_slow_queries_total{operation=%q} %d\n", escapeLabel(operation), dbSlow[operation])
	}
	_, _ = w.Write([]byte(output.String()))
}

func writeRouteValueCounters(output *strings.Builder, name string, help string, valueLabel string, values map[routeValueKey]uint64) {
	keys := make([]routeValueKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].value < keys[j].value
	})
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	for _, key := range keys {
		fmt.Fprintf(output, "%s{method=%q,route=%q,%s=%q} %d\n", name, escapeLabel(key.method), escapeLabel(key.route), valueLabel, escapeLabel(key.value), values[key])
	}
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
