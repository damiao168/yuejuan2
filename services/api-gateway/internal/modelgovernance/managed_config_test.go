package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestDefaultManagedAPIResolutionIsTenantScoped(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	verified := successfulManagedProbe()
	verified.ProbeMode = "capability"
	verified.GeneratedRequest = true
	input := ManagedAPIConfigInput{ProviderKey: "provider", DisplayName: "School API", AdapterType: "openai_compatible", BaseURL: "https://provider.test/v1", APIKey: "synthetic-secret-123456", ModelName: "school-model", ModelVersion: "v1", Region: "global", Status: "active", IsDefault: true, InitialProbe: &verified}
	a, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := ResolveDefaultManagedAPI(ctx, store, "school-a")
	if err != nil || connection == nil || connection.Config.ID != a.ID {
		t.Fatalf("default not resolved: %v", err)
	}
	connection, err = ResolveDefaultManagedAPI(ctx, store, "school-b")
	if err != nil || connection != nil {
		t.Fatal("school B must not inherit school A's model")
	}
	_, err = store.UpdateManagedAPIConfig(ctx, "school-a", a.ID, ManagedAPIConfigUpdateInput{DisplayName: input.DisplayName, AdapterType: input.AdapterType, BaseURL: input.BaseURL, ModelName: input.ModelName, ModelVersion: input.ModelVersion, Region: input.Region, Status: "disabled", IsDefault: true})
	if !errors.Is(err, ErrManagedDefaultMutation) {
		t.Fatalf("disabled current model must be rejected: %v", err)
	}
}

func TestCredentialCipherUsesTenantAndConfigBoundAuthenticatedEncryption(t *testing.T) {
	cipher, err := NewCredentialCipher("test-managed-model-credential-key-with-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt("sk-secret-value-at-least-16", "tenant-a", "config-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "sk-secret") || len(nonce) != 12 {
		t.Fatal("credential was not encrypted with an independent GCM nonce")
	}
	plaintext, err := cipher.Decrypt(ciphertext, nonce, "tenant-a", "config-a")
	if err != nil || plaintext != "sk-secret-value-at-least-16" {
		t.Fatalf("decrypt failed: %q %v", plaintext, err)
	}
	if _, err := cipher.Decrypt(ciphertext, nonce, "tenant-b", "config-a"); err == nil {
		t.Fatal("ciphertext must not decrypt in another tenant")
	}
}

func TestManagedAPIHandlersAssignPerTenantWithoutEchoingKey(t *testing.T) {
	store := NewMemoryStore()
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(staticManagedProber{result: ManagedAPIProbeResult{OK: true, StatusCode: 200, LatencyMS: 12, Message: "连接成功，密钥可用"}})
	user := auth.User{
		ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID,
		Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"},
	}
	tenantID := "00000000-0000-0000-0000-000000000020"
	body := `{
      "tenant_id":"` + tenantID + `","provider_key":"school-provider","display_name":"学校专用 API",
      "adapter_type":"openai_compatible","base_url":"https://models.example.test/v1",
      "model_name":"school-model","model_version":"school-model-v1","region":"cn",
      "api_key":"sk-secret-value-at-least-16","status":"active","is_default":true
    }`
	created := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs", body, handler.CreateManagedAPIConfig)
	if created.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "sk-secret") || !strings.Contains(created.Body.String(), `"credential_hint":"•••• t-16"`) {
		t.Fatalf("response exposed a credential or omitted its safe hint: %s", created.Body.String())
	}
	items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if err != nil || len(items) != 1 || items[0].TenantID != tenantID || items[0].IsDefault || items[0].LastProbeMode != "quick" {
		t.Fatalf("unexpected tenant assignment: %#v %v", items, err)
	}
	probeRequest := httptest.NewRequest(http.MethodPost,
		"/api/v1/platform/model-api-configs/"+items[0].ID+"/probe?tenant_id="+tenantID, nil)
	probeRequest.SetPathValue("id", items[0].ID)
	probeRequest = probeRequest.WithContext(auth.WithUser(probeRequest.Context(), user))
	probe := httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(probe, probeRequest)
	if probe.Code != http.StatusOK || strings.Contains(probe.Body.String(), "sk-secret") {
		t.Fatalf("probe returned %d or exposed credential: %s", probe.Code, probe.Body.String())
	}
	records, err := audits.ListAudits(context.Background(), user.TenantID, auth.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		raw, _ := json.Marshal(record.AfterValue)
		if strings.Contains(string(raw), "sk-secret") {
			t.Fatalf("audit exposed credential: %#v", record)
		}
	}
}

func TestManagedAPIHandlersRejectCrossTenantSchoolAdmin(t *testing.T) {
	handler := NewHandler(NewMemoryStore(), nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "school-admin", TenantID: "00000000-0000-0000-0000-000000000020", Permissions: []string{"model:provider:manage"}}
	response := performHandlerRequest(t, user, http.MethodGet,
		"/api/v1/platform/model-api-configs?tenant_id=00000000-0000-0000-0000-000000000030", "", handler.ListManagedAPIConfigs)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant list returned %d: %s", response.Code, response.Body.String())
	}
}

func TestManagedAPIProbeDefaultsToZeroGenerationQuickMode(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	initial := successfulManagedProbe()
	initial.ProbeMode = "capability"
	initial.GeneratedRequest = true
	initial.Usage = ManagedAPIProbeUsage{InputTokens: 9, OutputTokens: 5, TotalTokens: 14}
	item, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-v4-pro",
		ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active", IsDefault: true, InitialProbe: &initial,
	})
	if err != nil {
		t.Fatal(err)
	}
	quickCalls, capabilityCalls := 0, 0
	prober := splitManagedProber{quickCalls: &quickCalls, capabilityCalls: &capabilityCalls}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenantID, nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(response, request)
	if response.Code != http.StatusOK || quickCalls != 1 || capabilityCalls != 0 {
		t.Fatalf("quick probe returned %d; quick=%d capability=%d body=%s", response.Code, quickCalls, capabilityCalls, response.Body.String())
	}
	var payload struct {
		Result ManagedAPIProbeResult `json:"result"`
		Config ManagedAPIConfig      `json:"config"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Result.ProbeMode != "quick" || payload.Result.GeneratedRequest || payload.Result.Usage.TotalTokens != 0 {
		t.Fatalf("quick probe consumed generation: %#v", payload.Result)
	}
	if payload.Config.LastCapabilityStatus != "success" || payload.Config.LastCapabilityUsage.TotalTokens != 14 {
		t.Fatalf("quick probe overwrote capability evidence: %#v", payload.Config)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenantID+"&mode=capability", nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response = httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(response, request)
	if response.Code != http.StatusOK || capabilityCalls != 0 || !strings.Contains(response.Body.String(), `"reused":true`) {
		t.Fatalf("cached capability probe returned %d; capability=%d body=%s", response.Code, capabilityCalls, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenantID+"&mode=capability&force=true", nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response = httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(response, request)
	if response.Code != http.StatusOK || quickCalls != 1 || capabilityCalls != 1 {
		t.Fatalf("capability probe returned %d; quick=%d capability=%d body=%s", response.Code, quickCalls, capabilityCalls, response.Body.String())
	}
}

func TestFailedCapabilityProbeKeepsConfigAndPersistsOnlySafeDiagnostic(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	item, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-flash",
		ModelVersion: "deepseek-flash", Region: "global", Status: "active", IsDefault: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(diagnosticFailingProber{})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenantID+"&mode=capability&force=true", nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capability probe returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Result ManagedAPIProbeResult `json:"result"`
		Config ManagedAPIConfig      `json:"config"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Result.OK || payload.Result.ErrorCode != "invalid_json" || payload.Result.Diagnostic.ContentPreview == "" {
		t.Fatalf("request diagnostic missing: %#v", payload.Result)
	}
	if payload.Config.LastTestStatus != "success" || payload.Config.LastCapabilityStatus != "failed" || payload.Config.LastCapabilityDiagnostic.ContentPreview != "" {
		t.Fatalf("configuration or safe persisted diagnostic invalid: %#v", payload.Config)
	}
	items, _ := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if len(items) != 1 {
		t.Fatalf("failed capability probe removed configuration: %#v", items)
	}
}

func TestManagedProbeTransientFailureRetainsLastSuccessAndCapabilityEvidence(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	const tenantID = "school-a"
	verified := successfulManagedProbe()
	verified.ProbeMode = "capability"
	verified.GeneratedRequest = true
	verified.Usage = ManagedAPIProbeUsage{TotalTokens: 14}
	item, err := store.CreateManagedAPIConfig(ctx, tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "active", InitialProbe: &verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.LastSuccessfulTestedAt == nil || item.LastCapabilityStatus != "success" {
		t.Fatalf("initial success was not recorded: %#v", item)
	}
	lastSuccess := *item.LastSuccessfulTestedAt
	capabilityAt := *item.LastCapabilityTestedAt

	for _, code := range []string{"provider_timeout", "provider_unavailable"} {
		item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, ManagedAPIProbeResult{
			ProbeMode: "quick", ErrorCode: code, Message: "temporarily unreachable",
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.LastTestStatus != "temporary_unavailable" || item.LastTestedAt == nil ||
			item.LastSuccessfulTestedAt == nil || !item.LastSuccessfulTestedAt.Equal(lastSuccess) ||
			item.LastCapabilityStatus != "success" || item.LastCapabilityTestedAt == nil ||
			!item.LastCapabilityTestedAt.Equal(capabilityAt) || item.LastCapabilityUsage.TotalTokens != 14 {
			t.Fatalf("%s overwrote prior success or capability evidence: %#v", code, item)
		}
	}
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, ManagedAPIProbeResult{
		ProbeMode: "capability", GeneratedRequest: true, ErrorCode: "provider_timeout",
		Message:         "generation timed out",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "temporary_unavailable" || item.LastTestMessage != "generation timed out" ||
		item.LastSuccessfulTestedAt == nil || !item.LastSuccessfulTestedAt.Equal(lastSuccess) ||
		item.LastCapabilityStatus != "success" || item.LastCapabilityTestedAt == nil ||
		!item.LastCapabilityTestedAt.Equal(capabilityAt) || item.LastCapabilityUsage.TotalTokens != 14 {
		t.Fatalf("capability timeout overwrote last successful evidence: %#v", item)
	}

	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, ManagedAPIProbeResult{
		ProbeMode: "quick", ErrorCode: "credential_invalid", Message: "invalid credential",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "failed" || item.LastSuccessfulTestedAt == nil ||
		!item.LastSuccessfulTestedAt.Equal(lastSuccess) {
		t.Fatalf("deterministic failure was not distinguished from network interruption: %#v", item)
	}

	recovered := successfulManagedProbe()
	recovered.ProbeMode = "quick"
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, recovered)
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "success" || item.LastSuccessfulTestedAt == nil ||
		!item.LastSuccessfulTestedAt.Equal(*item.LastTestedAt) ||
		item.LastSuccessfulTestedAt.Before(lastSuccess) {
		t.Fatalf("recovery did not update last successful check: %#v", item)
	}
}

func TestManagedConfigConnectionChangeClearsOldProbeEvidence(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	verified := successfulManagedProbe()
	verified.ProbeMode = "capability"
	verified.GeneratedRequest = true
	item, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "active", InitialProbe: &verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	update := ManagedAPIConfigUpdateInput{
		DisplayName: item.DisplayName, AdapterType: item.AdapterType, BaseURL: item.BaseURL,
		ModelName: item.ModelName, ModelVersion: item.ModelVersion, Region: item.Region, Status: item.Status,
	}

	// Cosmetic edits keep observations attached to the same connection.
	update.DisplayName = "Updated DeepSeek"
	item, err = store.UpdateManagedAPIConfig(ctx, "school-a", item.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "success" || item.LastSuccessfulTestedAt == nil || item.LastCapabilityStatus != "success" {
		t.Fatalf("cosmetic edit erased valid evidence: %#v", item)
	}

	// A changed model with a failed initial quick check must not inherit old success.
	update.ModelName = "deepseek-reasoner"
	update.ModelVersion = "deepseek-reasoner"
	transient := ManagedAPIProbeResult{ProbeMode: "quick", ErrorCode: "provider_timeout", Message: "timed out"}
	update.InitialProbe = &transient
	item, err = store.UpdateManagedAPIConfig(ctx, "school-a", item.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "temporary_unavailable" || item.LastTestedAt == nil ||
		item.LastSuccessfulTestedAt != nil || item.LastCapabilityStatus != "untested" ||
		item.LastCapabilityTestedAt != nil || item.LastCapabilityVersion != "" {
		t.Fatalf("changed connection inherited old verification: %#v", item)
	}

	// Replacing the key without a new check leaves the connection unverified.
	update.APIKey = "rotated-secret-at-least-16"
	update.InitialProbe = nil
	item, err = store.UpdateManagedAPIConfig(ctx, "school-a", item.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	if item.LastTestStatus != "untested" || item.LastTestedAt != nil || item.LastSuccessfulTestedAt != nil {
		t.Fatalf("key rotation retained old probe status: %#v", item)
	}
}

func TestManagedProbePersistenceClassifiesTransientAndDeterministicErrors(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status string
	}{
		{"provider_timeout", "temporary_unavailable"},
		{"provider_unavailable", "temporary_unavailable"},
		{"credential_invalid", "failed"},
		{"model_not_found", "failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			values := managedProbePersistence(&ManagedAPIProbeResult{ProbeMode: "quick", ErrorCode: tc.code})
			if values.Status != tc.status || values.TestedAt == nil || values.UpdateCapability {
				t.Fatalf("unexpected persistence for %s: %#v", tc.code, values)
			}
		})
	}
	capabilityTimeout := managedProbePersistence(&ManagedAPIProbeResult{
		ProbeMode: "capability", GeneratedRequest: true, ErrorCode: "provider_timeout",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
	})
	if capabilityTimeout.Status != "temporary_unavailable" || capabilityTimeout.UpdateCapability {
		t.Fatalf("capability timeout must preserve previous verification: %#v", capabilityTimeout)
	}
}

func TestManagedProbeRejectsResultForConfigurationChangedDuringRequest(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	item, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	probeStartedAtRevision := item.UpdatedAt
	updated, err := store.UpdateManagedAPIConfig(ctx, "school-a", item.ID, ManagedAPIConfigUpdateInput{
		DisplayName: item.DisplayName, AdapterType: item.AdapterType, BaseURL: item.BaseURL,
		ModelName: "deepseek-reasoner", ModelVersion: "deepseek-reasoner", Region: item.Region, Status: item.Status,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.UpdatedAt.Equal(probeStartedAtRevision) {
		// Some platforms expose a coarse wall clock. The CAS contract still rejects
		// any revision other than the one currently stored.
		probeStartedAtRevision = probeStartedAtRevision.Add(-time.Second)
	}
	result := successfulManagedProbe()
	result.ProbeMode = "quick"
	if _, err = store.RecordManagedAPIProbe(ctx, "school-a", item.ID, probeStartedAtRevision, result); !errors.Is(err, ErrManagedProbeStale) {
		t.Fatalf("stale probe result was accepted: %v", err)
	}
	stored, err := store.ListManagedAPIConfigs(ctx, "school-a")
	if err != nil || len(stored) != 1 || stored[0].ModelName != "deepseek-reasoner" || stored[0].LastTestStatus != "untested" {
		t.Fatalf("stale result changed the new configuration: %#v %v", stored, err)
	}
	verified, err := store.RecordManagedAPIProbe(ctx, "school-a", item.ID, updated.UpdatedAt, result)
	if err != nil || verified.LastTestStatus != "success" || !verified.UpdatedAt.Equal(updated.UpdatedAt) {
		t.Fatalf("current probe result was not recorded without changing config revision: %#v %v", verified, err)
	}
}

type staticManagedProber struct {
	result ManagedAPIProbeResult
}

type splitManagedProber struct {
	quickCalls      *int
	capabilityCalls *int
}

type diagnosticFailingProber struct{}

func (diagnosticFailingProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return ManagedAPIProbeResult{
		OK: false, ProbeMode: "capability", GeneratedRequest: true, Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
		StatusCode: 200, Message: "模型返回内容不是合法 JSON", ErrorCode: "invalid_json",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
		CapabilityCheck: ManagedAPICheckResult{Code: "invalid_json", Message: "模型返回内容不是合法 JSON"},
		Usage:           ManagedAPIProbeUsage{InputTokens: 24, OutputTokens: 3, TotalTokens: 27},
		Diagnostic:      ManagedAPIProbeDiagnostic{FinishReason: "stop", ResponseFormat: "json_object", ContentLength: 8, ContentSHA256: "hash", ContentPreview: "not json"},
	}
}

func (p splitManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.capabilityCalls++
	result := successfulManagedProbe()
	result.ProbeMode = "capability"
	result.GeneratedRequest = true
	result.Usage = ManagedAPIProbeUsage{InputTokens: 8, OutputTokens: 5, TotalTokens: 13}
	return result
}

func (p splitManagedProber) ProbeQuick(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	*p.quickCalls++
	return ManagedAPIProbeResult{
		OK: true, ProbeMode: "quick", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
		Message:         "连接检查成功，本次未发送模型生成请求",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
		CapabilityCheck: ManagedAPICheckResult{OK: true, Code: "reused"},
	}
}

func (p staticManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	return p.result
}

func (p staticManagedProber) ProbeQuick(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	result := p.result
	result.ProbeMode = "quick"
	result.GeneratedRequest = false
	return result
}
