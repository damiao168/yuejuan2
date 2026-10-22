package modelgovernance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type coalescedManagedUsageStore struct {
	*recordingUsageStore
	secondRead chan struct{}
	readOnce   sync.Once
}

func (s *coalescedManagedUsageStore) GetManagedAPIConnection(ctx context.Context, tenantID, id string) (ManagedAPIConnection, error) {
	connection, err := s.MemoryStore.GetManagedAPIConnection(ctx, tenantID, id)
	if logger.RequestID(ctx) == "usage-http-b" {
		s.readOnce.Do(func() { close(s.secondRead) })
	}
	return connection, err
}

// 首次真实供应商 POST 保持在 barrier 内，两个不同 HTTP 请求共享生成；账本仍按生产唯一键去重。
// 先断言两个结果确实 Coalesced 且仅一次 POST，再检查用量，不能把没有合并的夹具误报为计量缺陷。
func TestManagedProbeCoalescingCountsEachProviderGenerationOnce(t *testing.T) {
	for _, mode := range []string{"text", "structured"} {
		for _, limited := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/limited=%t", mode, limited), func(t *testing.T) {
				store := &coalescedManagedUsageStore{recordingUsageStore: newRecordingUsageStore(), secondRead: make(chan struct{})}
				audits := auth.NewMemoryStore()
				started, release := make(chan struct{}, 2), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				var posts atomic.Int32
				provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
						_, _ = io.WriteString(w, `{"data":[{"id":"usage-model"}]}`)
						return
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
						http.NotFound(w, r)
						return
					}
					if posts.Add(1) == 1 {
						started <- struct{}{}
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					if limited {
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic limit"}}`)
						return
					}
					content := "ordinary reply"
					if mode == "structured" {
						content = `{"question_no":"1","max_score":2}`
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
						"usage":   map[string]any{"prompt_tokens": 8, "completion_tokens": 5, "total_tokens": 13},
					})
				}))
				defer func() {
					unblock()
					provider.Close()
				}()
				const tenant = "00000000-0000-0000-0000-000000000020"
				config, err := store.CreateManagedAPIConfig(t.Context(), tenant, "actor", ManagedAPIConfigInput{
					ProviderKey: "custom", DisplayName: "synthetic usage model", AdapterType: "openai_compatible", BaseURL: provider.URL + "/v1",
					APIKey: "synthetic-usage-key-at-least-16", ModelName: "usage-model", ModelVersion: "v1", Region: "global", Status: "active",
				})
				if err != nil {
					t.Fatal(err)
				}
				handler := NewHandler(store, audits, nil, testBaseline()).WithManagedAPIProber(newTestHTTPManagedAPIProber(provider.Client()))
				request := func(requestID string) *httptest.ResponseRecorder {
					r := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+config.ID+"/probe?tenant_id="+tenant+"&mode="+mode+"&force=true", nil)
					r.SetPathValue("id", config.ID)
					r = r.WithContext(auth.WithUser(logger.WithRequestID(r.Context(), requestID), auth.User{
						ID: "actor", TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"},
					}))
					response := httptest.NewRecorder()
					handler.ProbeManagedAPIConfig(response, r)
					return response
				}
				responses := make(chan *httptest.ResponseRecorder, 2)
				go func() { responses <- request("usage-http-a") }()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("first real provider generation did not start")
				}
				go func() { responses <- request("usage-http-b") }()
				select {
				case <-store.secondRead:
				case <-time.After(5 * time.Second):
					t.Fatal("second HTTP request did not reach the same configuration")
				}
				// 维持供应商 barrier，让第二条 Handler 走入 singleflight；实际合并仍由下面响应与 POST 数判定。
				timer := time.NewTimer(100 * time.Millisecond)
				<-timer.C
				unblock()
				for range 2 {
					select {
					case response := <-responses:
						var payload struct {
							Result ManagedAPIProbeResult `json:"result"`
						}
						if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || !payload.Result.Coalesced || payload.Result.OK == limited {
							t.Fatalf("fixture did not share a real generation: status=%d body=%s", response.Code, response.Body.String())
						}
					case <-time.After(5 * time.Second):
						t.Fatal("coalesced handlers did not finish")
					}
				}
				if posts.Load() != 1 {
					t.Fatalf("fixture generated %d provider requests instead of one", posts.Load())
				}
				events := store.recorded()
				if len(events) != 1 {
					t.Fatalf("one actual provider generation produced %d independently keyed usage events: %#v", len(events), events)
				}
				event := events[0]
				wantTokens, wantStatus := int64(13), "succeeded"
				if limited {
					wantTokens, wantStatus = 0, "failed"
				}
				if event.TenantID != tenant || event.TotalTokens != wantTokens || event.Status != wantStatus || event.RequestCount != 1 ||
					event.RequestID == "usage-http-a" || event.RequestID == "usage-http-b" || event.RequestID == "" ||
					(event.Metadata["http_request_id"] != "usage-http-a" && event.Metadata["http_request_id"] != "usage-http-b") {
					t.Fatalf("provider identity, HTTP source or usage changed: %#v", event)
				}
				originalUsageID := event.RequestID
				// HTTP request_id 重用不能吞掉下一次实际生成；两次真正调用应有两份账本记录。
				if response := request("usage-http-a"); response.Code != http.StatusOK {
					t.Fatalf("independent generation returned %d", response.Code)
				}
				events = store.recorded()
				if posts.Load() != 2 || len(events) != 2 || events[1].RequestID == originalUsageID || events[1].TotalTokens != wantTokens {
					t.Fatalf("independent generation was deduplicated: posts=%d events=%#v", posts.Load(), events)
				}
				records, err := audits.ListAudits(t.Context(), auth.PlatformTenantID, auth.AuditFilter{Action: "model.managed_api_probed"})
				if err != nil || len(records) != 3 {
					t.Fatalf("HTTP request audit trail changed: records=%#v err=%v", records, err)
				}
				seen := map[string]int{}
				for _, record := range records {
					seen[record.RequestID]++
				}
				if seen["usage-http-a"] != 2 || seen["usage-http-b"] != 1 {
					t.Fatalf("provider identity replaced original HTTP audit identity: %#v", seen)
				}
			})
		}
	}
}
