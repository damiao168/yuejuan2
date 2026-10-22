package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type batch6SecretResolver struct {
	calls []string
	probe SecretProbe
	err   error
}

func (s *batch6SecretResolver) Probe(reference string) (SecretProbe, error) {
	s.calls = append(s.calls, reference)
	return s.probe, s.err
}

func TestBatch6ProductionValidationOrder(t *testing.T) {
	t.Run("metadata before secret and deployments", func(t *testing.T) {
		provider := productionExternalProvider("env://ORDER")
		provider.Region = ""
		deployment := productionExternalDeployment()
		deployment.PricingPolicy = nil
		resolver := &batch6SecretResolver{err: ErrInvalidSecretReference}
		err := ValidateProductionInventory([]Provider{provider}, []Deployment{deployment}, resolver)
		if err == nil || !strings.Contains(err.Error(), "provider vendor-a metadata") || len(resolver.calls) != 0 {
			t.Fatalf("order changed: err=%v calls=%v", err, resolver.calls)
		}
	})
	t.Run("all providers before deployments and disabled skipped", func(t *testing.T) {
		first := productionExternalProvider("env://FIRST")
		disabled := first
		disabled.Key, disabled.CredentialRef, disabled.Status = "disabled", "env://SKIP", "disabled"
		second := first
		second.Key, second.CredentialRef = "second", "env://SECOND"
		deployment := productionExternalDeployment()
		deployment.PricingPolicy = map[string]any{"meter": "not_configured"}
		resolver := &batch6SecretResolver{probe: SecretProbe{ResolverSupported: false}}
		err := ValidateProductionInventory([]Provider{first, disabled, second}, []Deployment{deployment}, resolver)
		if err == nil || !strings.Contains(err.Error(), "has no pricing meter") || !reflect.DeepEqual(resolver.calls, []string{"env://FIRST", "env://SECOND"}) {
			t.Fatalf("order changed: err=%v calls=%v", err, resolver.calls)
		}
	})
	t.Run("active unsupported resolver before bad deployment", func(t *testing.T) {
		provider := productionExternalProvider("vault://FIRST")
		provider.Status = "active"
		err := ValidateProductionInventory([]Provider{provider}, []Deployment{{TenantID: provider.TenantID, ProviderKey: provider.Key}}, &batch6SecretResolver{})
		if err == nil || !strings.Contains(err.Error(), "has no installed secret resolver") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestBatch6ProviderExplicitChoiceDoesNotFallBack(t *testing.T) {
	registry := NewProviderRegistry()
	if _, err := registry.Resolve("deepseek-chat", "unknown", ""); !errors.Is(err, ErrManagedProviderUnknown) {
		t.Fatalf("explicit provider unexpectedly fell back: %v", err)
	}
	resolved, err := registry.Resolve(" qwen-plus ", " DEEPSEEK ", "ignored")
	if err != nil || resolved.Key != "deepseek" {
		t.Fatalf("explicit known provider changed: %#v %v", resolved, err)
	}
	custom, err := registry.Resolve(" school-model ", "custom", " http://example.test/ ")
	if err != nil || custom.BaseURL != "http://example.test" {
		t.Fatalf("registry custom resolution changed: %#v %v", custom, err)
	}
}

func TestBatch6RoleResolutionKeepsFirstEligibleBinding(t *testing.T) {
	store := &panelRoleBindingStoreStub{items: []ModelRoleBinding{
		{ID: "disabled", AgentRole: ModelRolePrimaryA, Status: "disabled", ManagedModelAPIConfigID: "wrong"},
		{ID: "a-first", AgentRole: ModelRolePrimaryA, Status: "active", ManagedModelAPIConfigID: "a", StrengthRank: 1},
		{ID: "a-later", AgentRole: ModelRolePrimaryA, Status: "active", ManagedModelAPIConfigID: "wrong", StrengthRank: 9},
		{ID: "b", AgentRole: ModelRolePrimaryB, Status: "active", ManagedModelAPIConfigID: "b", StrengthRank: 2},
		{ID: "arbiter", AgentRole: ModelRoleArbiter, Status: "active", ManagedModelAPIConfigID: "c", StrengthRank: 3},
		{ID: "irrelevant", AgentRole: "other", Status: "active"},
	}}
	bindings, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "physics", "structured_steps")
	if err != nil || bindings.PrimaryA.ID != "a-first" || bindings.PrimaryB.ID != "b" || bindings.Arbiter.ID != "arbiter" {
		t.Fatalf("unexpected selection: %#v %v", bindings, err)
	}
}

func batch6RuntimePrompt() RuntimePrompt {
	prompt := RuntimePrompt{PromptVersion: "fixture", BundleSHA256: strings.Repeat("z", 64), ActivationMode: "deployment_manifest"}
	for _, key := range []string{"base", "structured", "role.primary", "role.arbiter", "stage.junior", "stage.senior", "subject.physics.calculation"} {
		prompt.Components = append(prompt.Components, RuntimePromptComponent{Key: key, Filename: key + ".md", SHA256: strings.Repeat("z", 64), Content: "fixture"})
	}
	return prompt
}

func TestBatch6RuntimePromptValidationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*RuntimePrompt)
		suffix string
		valid  bool
	}{
		{name: "length only not cryptographic validation", valid: true},
		{name: "duplicate component", mutate: func(p *RuntimePrompt) { p.Components = append(p.Components, p.Components[0]) }},
		{name: "required role absent", mutate: func(p *RuntimePrompt) { p.Components = append(p.Components[:3], p.Components[4:]...) }},
		{name: "unknown subject", mutate: func(p *RuntimePrompt) { p.Components[6].Key = "subject.unknown.calculation" }},
		{name: "short component hash", mutate: func(p *RuntimePrompt) { p.Components[0].SHA256 = "short" }},
		{name: "empty component", mutate: func(p *RuntimePrompt) { p.Components[0].Content = " \t " }},
		{name: "mutable", mutate: func(p *RuntimePrompt) { p.MutableAtRuntime = true }},
		{name: "wrong activation", mutate: func(p *RuntimePrompt) { p.ActivationMode = "live" }},
		{name: "trailing object", suffix: `{}`},
		{name: "trailing whitespace", suffix: " \n\t", valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompt := batch6RuntimePrompt()
			if test.mutate != nil {
				test.mutate(&prompt)
			}
			body, err := json.Marshal(map[string]any{"prompt": prompt})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/grading/prompts/current" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
					t.Errorf("request contract changed: %s %s", r.URL.Path, r.Header.Get("Authorization"))
				}
				_, _ = w.Write(append(body, []byte(test.suffix)...))
			}))
			defer server.Close()
			_, err = NewHTTPRuntimePromptSource(server.URL, strings.Repeat("t", 32), time.Second).Current(context.Background())
			if (err == nil) != test.valid || (err != nil && !errors.Is(err, ErrRuntimePromptUnavailable)) {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}

func TestBatch6SandboxAggregatesAndBoundsLifetime(t *testing.T) {
	provider, deployment, policy, secret, evidence, now := validSandboxAdmission()
	evidence.ExpiresAt = now.Add(90 * 24 * time.Hour)
	if decision := AssessSandboxAdmission(provider, deployment, policy, secret, evidence, "text", now); !decision.Allowed {
		t.Fatalf("exact lifetime rejected: %#v", decision)
	}
	evidence.ExpiresAt = evidence.ExpiresAt.Add(time.Nanosecond)
	assertSandboxBlocker(t, AssessSandboxAdmission(provider, deployment, policy, secret, evidence, "text", now), "approval_expired_or_unbounded")
	evidence.ExpiresAt = now
	assertSandboxBlocker(t, AssessSandboxAdmission(provider, deployment, policy, secret, evidence, "text", now), "approval_expired_or_unbounded")
	evidence.ExpiresAt = now.Add(time.Hour)
	provider.Status = "unverified"
	policy.MaxCostMicrosPerQuestion = 0
	secret.Configured = false
	evidence.ContractReviewed = false
	evidence.PricingReviewed = false
	decision := AssessSandboxAdmission(provider, deployment, policy, secret, evidence, "text", now)
	want := []string{"budget_not_configured", "contract_not_reviewed", "credential_not_ready", "pricing_not_reviewed", "sandbox_not_active"}
	if decision.Allowed || !reflect.DeepEqual(decision.Blockers, want) {
		t.Fatalf("aggregation changed: %#v", decision)
	}
}

func batch6ManagedInput() ManagedAPIConfigInput {
	return ManagedAPIConfigInput{ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKey: "fixture-secret-at-least-16", ModelName: "model", ModelVersion: "v1"}
}

func TestBatch6ManagedInputNormalizationAndErrorOrder(t *testing.T) {
	input := batch6ManagedInput()
	input.ProviderKey, input.DisplayName, input.BaseURL = " DEEPSEEK ", " DeepSeek ", " https://api.deepseek.com/// "
	probe := successfulManagedProbe()
	input.InitialProbe = &probe
	normalized, err := normalizeManagedAPIInput(input, true)
	if err != nil || normalized.ProviderKey != "deepseek" || normalized.BaseURL != "https://api.deepseek.com" || normalized.Region != "global" || normalized.ConfigSource != "manual" || normalized.Status != "active" || normalized.InitialProbe != &probe {
		t.Fatalf("normalization changed: %#v %v", normalized, err)
	}
	input = batch6ManagedInput()
	input.IsDefault, input.Status, input.BaseURL, input.ConfigSource = true, "disabled", "http://invalid", "invalid"
	if _, err := normalizeManagedAPIInput(input, true); !errors.Is(err, ErrManagedDefaultMutation) {
		t.Fatalf("default guard must precede endpoint/source guard: %v", err)
	}
	input.DisplayName = ""
	if _, err := normalizeManagedAPIInput(input, true); !errors.Is(err, ErrInvalidManagedConfig) {
		t.Fatalf("metadata guard must precede default guard: %v", err)
	}
	for _, endpoint := range []string{"http://example.test", "https://user@example.test", "https://example.test?a=1", "https://example.test#fragment"} {
		t.Run(endpoint, func(t *testing.T) {
			input := batch6ManagedInput()
			input.BaseURL = endpoint
			if _, err := normalizeManagedAPIInput(input, true); !errors.Is(err, ErrInvalidManagedConfig) {
				t.Fatalf("invalid endpoint accepted: %v", err)
			}
		})
	}
}

type batch6ManagedDeleteCase struct {
	name string
	want error
	edit func(*MemoryStore, string)
}

// 删除按角色、审批和策略检查依赖；失败保留配置与密钥，成功才做软删除并移除秘密。
func exerciseBatch6ManagedDelete(t *testing.T, test batch6ManagedDeleteCase) {
	t.Helper()
	store := NewMemoryStore()
	item, err := store.CreateManagedAPIConfig(context.Background(), "school-a", "actor", batch6ManagedInput())
	if err != nil {
		t.Fatal(err)
	}
	test.edit(store, item.ID)
	err = store.DeleteManagedAPIConfig(context.Background(), "school-a", "delete-actor", item.ID)
	if !errors.Is(err, test.want) {
		t.Fatalf("delete error=%v want=%v", err, test.want)
	}
	stored := store.managedConfigs[item.ID]
	if test.want != nil {
		if !reflect.DeepEqual(stored, item) || store.managedSecrets[item.ID] == "" {
			t.Fatalf("failed delete changed state: %#v", stored)
		}
		return
	}
	if stored.DeletedAt == nil || stored.DeletedBy != "delete-actor" || stored.Status != "disabled" || store.managedSecrets[item.ID] != "" {
		t.Fatalf("soft delete incomplete: %#v", stored)
	}
	if _, err := store.GetManagedAPIConnection(context.Background(), "school-a", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted connection exposed: %v", err)
	}
}

func TestBatch6ManagedDeleteDependencyOrderAndAtomicity(t *testing.T) {
	for _, test := range []batch6ManagedDeleteCase{
		{name: "role wins over approval and policy", want: ErrManagedConfigInUse, edit: func(s *MemoryStore, id string) {
			s.roleBindings["role"] = ModelRoleBinding{TenantID: "school-a", ManagedModelAPIConfigID: id, Status: "active"}
			s.modelApprovals["model"] = ModelApproval{TenantID: "school-a", ModelConfigID: id, ExpiresAt: time.Now().Add(time.Hour)}
			s.policies["school-a"] = TenantPolicy{AllowedModelConfigIDs: []string{id}}
		}},
		{name: "model approval", want: ErrConflict, edit: func(s *MemoryStore, id string) {
			s.modelApprovals["model"] = ModelApproval{TenantID: "school-a", ModelConfigID: id, ExpiresAt: time.Now().Add(time.Hour)}
		}},
		{name: "sandbox approval", want: ErrConflict, edit: func(s *MemoryStore, id string) {
			s.approvals["sandbox"] = SandboxApproval{TenantID: "school-a", ModelConfigID: id, ExpiresAt: time.Now().Add(time.Hour)}
		}},
		{name: "policy", want: ErrConflict, edit: func(s *MemoryStore, id string) {
			s.policies["school-a"] = TenantPolicy{AllowedModelConfigIDs: []string{"other", id}}
		}},
		{name: "foreign inactive expired revoked do not block", edit: func(s *MemoryStore, id string) {
			now := time.Now()
			s.roleBindings["foreign"] = ModelRoleBinding{TenantID: "school-b", ManagedModelAPIConfigID: id, Status: "active"}
			s.roleBindings["inactive"] = ModelRoleBinding{TenantID: "school-a", ManagedModelAPIConfigID: id, Status: "disabled"}
			s.modelApprovals["expired"] = ModelApproval{TenantID: "school-a", ModelConfigID: id, ExpiresAt: now.Add(-time.Hour)}
			s.approvals["revoked"] = SandboxApproval{TenantID: "school-a", ModelConfigID: id, ExpiresAt: now.Add(time.Hour), RevokedAt: &now}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			exerciseBatch6ManagedDelete(t, test)
		})
	}
}

func TestBatch6ManagedProbeCapabilityPersistence(t *testing.T) {
	now := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	result := successfulManagedProbe()
	result.ProbeMode, result.GeneratedRequest = "capability", true
	result.Message = "  success  "
	result.Usage = ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
	result.Diagnostic.ContentPreview = "must not persist"
	var item ManagedAPIConfig
	applyManagedProbe(&item, result, now)
	if item.LastTestedAt == nil || item.LastTestedAt != item.LastSuccessfulTestedAt || item.LastTestedAt != item.LastCapabilityTestedAt || item.LastCapabilityDiagnostic.ContentPreview != "" || result.Diagnostic.ContentPreview == "" {
		t.Fatalf("timestamp alias or sanitization changed: %#v", item)
	}
	values := managedProbePersistence(&result)
	var usage ManagedAPIProbeUsage
	var diagnostic ManagedAPIProbeDiagnostic
	if err := json.Unmarshal([]byte(values.CapabilityUsage), &usage); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(values.CapabilityDiagnostic), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if !values.HasProbe || !values.UpdateCapability || values.Status != "success" || values.CapabilityStatus != "success" || values.TestedAt != values.CapabilityTestedAt || usage != result.Usage || diagnostic.ContentPreview != "" {
		t.Fatalf("unexpected persistence: %#v %#v %#v", values, usage, diagnostic)
	}
	for _, test := range []struct {
		name string
		edit func(*ManagedAPIProbeResult)
	}{
		{name: "quick", edit: func(r *ManagedAPIProbeResult) { r.ProbeMode = "quick" }},
		{name: "reused", edit: func(r *ManagedAPIProbeResult) { r.Reused = true }},
		{name: "transient", edit: func(r *ManagedAPIProbeResult) { r.OK = false; r.ErrorCode = "provider_timeout" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := result
			test.edit(&candidate)
			before := item
			applyManagedProbe(&item, candidate, now.Add(time.Hour))
			if item.LastCapabilityTestedAt != before.LastCapabilityTestedAt || item.LastCapabilityUsage != before.LastCapabilityUsage || item.LastCapabilityStatus != before.LastCapabilityStatus || managedProbePersistence(&candidate).UpdateCapability {
				t.Fatalf("previous capability evidence overwritten: %#v", item)
			}
		})
	}
	result.OK, result.CapabilityCheck.OK, result.ErrorCode = false, false, "capability_invalid"
	result.CapabilityCheck.Code, result.CapabilityCheck.Message = "invalid", ""
	result.Message = strings.Repeat("x", 300)
	values = managedProbePersistence(&result)
	if !values.UpdateCapability || values.Status != "success" || values.CapabilityStatus != "failed" || values.CapabilityMessage != strings.Repeat("x", 256) || len(values.Message) > 256 {
		t.Fatalf("deterministic failure not persisted: %#v", values)
	}
}

type batch6UpdateProber struct {
	full, quick, text int
	seen              ManagedAPIConnection
	result            ManagedAPIProbeResult
}

func (p *batch6UpdateProber) ProbeText(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	p.text++
	p.seen = connection
	return managedTextProbeFixture(p.result)
}

func (p *batch6UpdateProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	p.full++
	p.seen = connection
	return p.result
}

func (p *batch6UpdateProber) ProbeQuick(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	p.quick++
	p.seen = connection
	return p.result
}

type batch6ManagedUpdateCase struct {
	name       string
	isDefault  bool
	ok         bool
	wantStatus int
}

// 默认聊天模型更换必须先验证文本生成，备用模型仅快速探测；失败后旧配置和密钥必须保留。
// 检查探测模式、调用次数和被探测的新连接，随后才读取最终存储状态。
func verifyBatch6ManagedUpdateProbe(t *testing.T, response *httptest.ResponseRecorder, prober *batch6UpdateProber, test batch6ManagedUpdateCase, update ManagedAPIConfigUpdateInput) {
	t.Helper()
	if response.Code != test.wantStatus || prober.full != 0 || (test.isDefault && (prober.text != 1 || prober.quick != 0)) || (!test.isDefault && (prober.text != 0 || prober.quick != 1)) || prober.seen.APIKey != update.APIKey || prober.seen.Config.ModelName != update.ModelName {
		t.Fatalf("status=%d full=%d text=%d quick=%d seen=%#v body=%s", response.Code, prober.full, prober.text, prober.quick, prober.seen.Config, response.Body.String())
	}
}

func exerciseBatch6ManagedUpdate(t *testing.T, test batch6ManagedUpdateCase) {
	t.Helper()
	store := NewMemoryStore()
	input := batch6ManagedInput()
	input.IsDefault = test.isDefault
	const tenantID = "00000000-0000-0000-0000-000000000020"
	item, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	prober := &batch6UpdateProber{result: successfulManagedProbe()}
	prober.result.OK = test.ok
	prober.result.GeneratedRequest = test.isDefault
	if !test.ok {
		prober.result.ErrorCode = "provider_timeout"
	}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
	update := ManagedAPIConfigUpdateInput{DisplayName: "Changed", AdapterType: input.AdapterType, BaseURL: input.BaseURL, APIKey: "rotated-fixture-secret-123", ModelName: "new-model", ModelVersion: "v2", Region: "cn", Status: "active", IsDefault: test.isDefault}
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+item.ID+"?tenant_id="+tenantID, strings.NewReader(string(body)))
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)
	verifyBatch6ManagedUpdateProbe(t, response, prober, test, update)
	connection, err := store.GetManagedAPIConnection(context.Background(), tenantID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !test.ok {
		if !reflect.DeepEqual(connection.Config, item) || connection.APIKey != input.APIKey {
			t.Fatalf("failed validation changed stored connection: %#v", connection.Config)
		}
		return
	}
	wantMode := "quick"
	if test.isDefault {
		wantMode = "text"
	}
	if connection.APIKey != update.APIKey || connection.Config.ModelName != update.ModelName || connection.Config.LastProbeMode != wantMode {
		t.Fatalf("successful update incomplete: %#v", connection.Config)
	}
}

func TestBatch6ManagedUpdateProbesBeforeSaving(t *testing.T) {
	for _, test := range []batch6ManagedUpdateCase{
		{name: "default failure retains prior secret", isDefault: true, wantStatus: http.StatusUnprocessableEntity},
		{name: "default success text generation", isDefault: true, ok: true, wantStatus: http.StatusOK},
		{name: "backup success quick only", ok: true, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			exerciseBatch6ManagedUpdate(t, test)
		})
	}
}

type batch6ErrorReader struct {
	data string
	err  error
}

func (r *batch6ErrorReader) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type batch6ManagedStreamCase struct {
	name        string
	stream      string
	dashscope   bool
	readerErr   error
	failType    string
	callbackErr error
	wantErr     error
	wantTypes   []string
	content     string
	reasoning   string
	finish      string
	usage       ManagedAPIProbeUsage
}

// 事件按reasoning、content、done顺序消费；回调或读取失败按原位置停止，不能补发完成事件。
func exerciseBatch6ManagedStream(t *testing.T, test batch6ManagedStreamCase, connection ManagedAPIConnection) {
	t.Helper()
	var reader io.Reader = strings.NewReader(test.stream)
	if test.readerErr != nil {
		reader = &batch6ErrorReader{data: test.stream, err: test.readerErr}
	}
	var types []string
	var events []ManagedChatEvent
	var completion *ManagedChatCompletion
	err := consumeManagedChatStream(reader, connection, test.dashscope, func(event ManagedChatEvent) error {
		types = append(types, event.Type)
		events = append(events, event)
		if event.Completion != nil {
			completion = event.Completion
		}
		if event.Type == test.failType {
			return test.callbackErr
		}
		return nil
	})
	trace, marshalErr := json.Marshal(events)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("TRACE %s", trace)
	if !errors.Is(err, test.wantErr) || !reflect.DeepEqual(types, test.wantTypes) {
		t.Fatalf("error=%v types=%v wantError=%v wantTypes=%v", err, types, test.wantErr, test.wantTypes)
	}
	if test.wantErr == nil && (completion == nil || completion.Message.Content != test.content || completion.Message.ReasoningContent != test.reasoning || completion.FinishReason != test.finish || completion.Usage != test.usage) {
		t.Fatalf("completion=%#v", completion)
	}
}

func TestBatch6ManagedStreamReaderOrdering(t *testing.T) {
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{ModelName: "model", DisplayName: "fixture", ProviderKey: "fixture"}}
	compat := `data: {"choices":[{"delta":{"content":" answer ","reasoning_content":"think"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}` + "\n"
	dash := `data: {"output":{"choices":[{"message":{"content":"native","reasoning_content":"reason"},"finish_reason":"stop"}]},"choices":[{"delta":{"content":"ignored"}}],"usage":{"input_tokens":2,"output_tokens":3}}` + "\n"
	readerFailure := errors.New("fixture reader failed")
	callbackFailure := errors.New("fixture callback failed")
	for _, test := range []batch6ManagedStreamCase{
		{name: "compat malformed empty done and trailing ignored", stream: ":comment\ndata:\ndata: broken\nevent: other\n" + compat + "data: [DONE]\n" + compat, wantTypes: []string{"reasoning", "content", "done"}, content: "answer", reasoning: "think", finish: "stop", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{name: "native prefers output and retains zero total", stream: dash + "data: [DONE]\n", dashscope: true, wantTypes: []string{"reasoning", "content", "done"}, content: "native", reasoning: "reason", finish: "stop", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3}},
		{name: "compat keeps finish across empty choice and malformed usage", stream: compat + `data: {"choices":[{"delta":{"content":"next"}}],"usage":"invalid"}` + "\n", wantTypes: []string{"reasoning", "content", "content", "done"}, content: "answer next", reasoning: "think", finish: "stop", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{name: "native empty finish replaces prior value", stream: dash + `data: {"output":{"choices":[{"message":{"content":"next"}}]},"usage":null}` + "\n", dashscope: true, wantTypes: []string{"reasoning", "content", "content", "done"}, content: "nativenext", reasoning: "reason", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3}},
		{name: "reasoning callback error stops content", stream: compat, failType: "reasoning", callbackErr: callbackFailure, wantErr: callbackFailure, wantTypes: []string{"reasoning"}},
		{name: "content callback error stops done", stream: compat, failType: "content", callbackErr: callbackFailure, wantErr: callbackFailure, wantTypes: []string{"reasoning", "content"}},
		{name: "done callback error propagated", stream: compat, failType: "done", callbackErr: callbackFailure, wantErr: callbackFailure, wantTypes: []string{"reasoning", "content", "done"}},
		{name: "cancel delivered by callback", stream: compat, failType: "reasoning", callbackErr: context.Canceled, wantErr: context.Canceled, wantTypes: []string{"reasoning"}},
		{name: "reader error after frame prevents done", stream: compat, readerErr: readerFailure, wantErr: readerFailure, wantTypes: []string{"reasoning", "content"}},
		{name: "done ignores later reader error", stream: compat + "data: [DONE]\n", readerErr: readerFailure, wantTypes: []string{"reasoning", "content", "done"}, content: "answer", reasoning: "think", finish: "stop", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
		{name: "empty response", stream: "data: [DONE]\n", wantErr: ErrManagedChatEmptyResponse},
		{name: "whitespace content empty", stream: `data: {"choices":[{"delta":{"content":" "}}]}` + "\n", wantErr: ErrManagedChatEmptyResponse, wantTypes: []string{"content"}},
		{name: "final line without newline", stream: strings.TrimSpace(compat), wantTypes: []string{"reasoning", "content", "done"}, content: "answer", reasoning: "think", finish: "stop", usage: ManagedAPIProbeUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
	} {
		t.Run(test.name, func(t *testing.T) {
			exerciseBatch6ManagedStream(t, test, connection)
		})
	}
}

func TestBatch6ManagedChatRuneBudgetAndPartialMutation(t *testing.T) {
	for _, count := range []int{managedChatMaxMessageRunes, managedChatMaxMessageRunes + 1} {
		messages := []ManagedChatMessage{{Role: "user", Content: strings.Repeat("界", count)}}
		_, err := normalizeManagedChatMessages(messages)
		if (err == nil) != (count == managedChatMaxMessageRunes) {
			t.Fatalf("rune count=%d err=%v", count, err)
		}
	}
	messages := make([]ManagedChatMessage, 10)
	for index := range messages {
		messages[index] = ManagedChatMessage{Role: "user", Content: strings.Repeat("x", managedChatMaxMessageRunes)}
	}
	if _, err := normalizeManagedChatMessages(messages); err != nil {
		t.Fatalf("exact total budget rejected: %v", err)
	}
	messages = append(messages, ManagedChatMessage{Role: "user", Content: "x"})
	if _, err := normalizeManagedChatMessages(messages); !errors.Is(err, ErrManagedChatInvalidRequest) {
		t.Fatalf("total budget overflow accepted: %v", err)
	}
	attachments := []ManagedChatAttachment{
		{Name: " first.txt ", MediaType: " TEXT/PLAIN ", Content: " text "},
		{Name: " invalid.txt ", MediaType: " TEXT/PLAIN ", Content: " "},
		{Name: " untouched.txt ", MediaType: " TEXT/PLAIN ", Content: " text "},
	}
	if _, err := normalizeManagedChatMessages([]ManagedChatMessage{{Role: "user", Attachments: attachments}}); !errors.Is(err, ErrManagedChatInvalidRequest) {
		t.Fatalf("invalid second attachment accepted: %v", err)
	}
	if attachments[0].Name != "first.txt" || attachments[0].MediaType != "text/plain" || attachments[1].Content != "" || attachments[2].Name != " untouched.txt " {
		t.Fatalf("partial mutation changed: %#v", attachments)
	}
}
