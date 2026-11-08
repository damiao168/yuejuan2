package modelgovernance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	database "edugrade-enterprise/services/api-gateway/internal/db"
)

type policyTenantStore struct {
	Store
	calls int
}

func (s *policyTenantStore) GetPolicy(ctx context.Context, tenant string) (TenantPolicy, error) {
	actual, _ := database.TenantFromContext(ctx)
	if actual != tenant {
		return TenantPolicy{}, errors.New("wrong tenant context")
	}
	s.calls++
	return s.Store.GetPolicy(ctx, tenant)
}
func (s *policyTenantStore) UpdatePolicy(ctx context.Context, tenant, actor string, input PolicyUpdateInput) (TenantPolicy, error) {
	actual, _ := database.TenantFromContext(ctx)
	if actual != tenant {
		return TenantPolicy{}, errors.New("wrong tenant context")
	}
	s.calls++
	return s.Store.UpdatePolicy(ctx, tenant, actor, input)
}
func TestCrossTenantPolicyInstallsAuthorizedTenantContext(t *testing.T) {
	const target = "00000000-0000-0000-0000-000000000020"
	store := &policyTenantStore{Store: NewMemoryStore()}
	store.Store.EnsureLocalBaseline(t.Context(), target, testBaseline())
	h := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "platform", TenantID: auth.PlatformTenantID, Permissions: []string{"model:config:manage"}}
	response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/model-policy?tenant_id="+target, "", h.GetPolicy)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	body := `{"display_name":"Default","mode":"local_only","external_enabled":false,"text_export_enabled":false,"image_export_enabled":false,"allowed_deployments":[],"max_cost_micros_per_question":0,"max_cost_micros_per_exam":0,"fallback_mode":"manual_only","expected_version":1,"reason":"clear policy"}`
	response = performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy?tenant_id="+target, body, h.UpdatePolicy)
	if response.Code != 200 || store.calls != 2 {
		t.Fatal(response.Code, response.Body.String(), store.calls)
	}
}

type infiniteChatBody struct{ read int }

func (r *infiniteChatBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read += len(p)
	return len(p), nil
}
func TestManagedChatResponseIsBounded(t *testing.T) {
	reader := &infiniteChatBody{}
	_, err := readManagedChatResponse(reader)
	if !errors.Is(err, ErrManagedChatResponseLimit) || reader.read != managedAgentMaxResponseBytes+1 {
		t.Fatal(reader.read, err)
	}
	if raw, err := readManagedChatResponse(strings.NewReader("{}")); err != nil || string(raw) != "{}" {
		t.Fatal(string(raw), err)
	}
	if _, err = readManagedChatResponse(io.LimitReader(reader, managedAgentMaxResponseBytes)); err != nil {
		t.Fatal(err)
	}
}
