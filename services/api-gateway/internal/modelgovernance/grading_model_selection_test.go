package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
)

func TestGradingSelectionSeparatesChatAndAcceptsAdministratorAssignment(t *testing.T) {
	store, school, chat, selected := documentSelectionFixture()
	ctx := context.Background()
	initial, err := store.GetGradingModelSelection(ctx, school)
	if err != nil || initial.Mode != "unconfigured" || initial.Revision != "absent" {
		t.Fatalf("initial %#v %v", initial, err)
	}
	if _, err = ResolveGradingManagedAPI(ctx, store, school); !errors.Is(err, ErrGradingModelRequired) {
		t.Fatalf("chat fallback %v", err)
	}
	// 管理员直接指定；text 元数据和未做结构化检测不能额外阻止这个独立用途。
	config := store.managedConfigs[selected]
	config.Modalities = []string{"text"}
	config.LastCapabilityStatus = "untested"
	store.managedConfigs[selected] = config
	selection, err := store.SaveGradingModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: selected})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := ResolveGradingManagedAPI(ctx, store, school)
	if err != nil || connection.Config.ID != selected || !store.managedConfigs[chat].IsDefault {
		t.Fatalf("independent selection %v", err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", selected); !errors.Is(err, ErrManagedConfigInUse) {
		t.Fatalf("referenced model deletion: %v", err)
	}
	selection, err = store.SaveGradingModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: selection.Revision, Mode: "unconfigured"})
	if err != nil || selection.ModelConfigID != "" {
		t.Fatalf("clear %#v %v", selection, err)
	}
	if _, err = ResolveGradingManagedAPI(ctx, store, school); !errors.Is(err, ErrGradingModelRequired) {
		t.Fatal("clear unexpectedly fell back to chat")
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", selected); err != nil {
		t.Fatalf("unreferenced model deletion: %v", err)
	}
}

func TestGradingSelectionRejectsUnavailableOrForeignConfiguration(t *testing.T) {
	for _, test := range []string{"foreign", "disabled", "no_credential", "no_secret", "deleted"} {
		t.Run(test, func(t *testing.T) {
			store, school, _, id := documentSelectionFixture()
			config := store.managedConfigs[id]
			switch test {
			case "foreign":
				config.TenantID = uuid.NewString()
			case "disabled":
				config.Status = "disabled"
			case "no_credential":
				config.CredentialConfigured = false
			case "no_secret":
				delete(store.managedSecrets, id)
			case "deleted":
				config.DeletedAt = &config.CreatedAt
			}
			store.managedConfigs[id] = config
			if _, err := store.SaveGradingModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: id}); !errors.Is(err, ErrGradingModelRequired) {
				t.Fatalf("accepted invalid config: %v", err)
			}
			// 保存后的配置后来失效时也要重新校验，不能切换到聊天默认项。
			store.gradingSelections[school] = DocumentModelSelection{Mode: "model", ModelConfigID: id}
			if connection, err := ResolveGradingManagedAPI(context.Background(), store, school); err == nil || connection != nil {
				t.Fatal("runtime accepted invalid configuration")
			}
		})
	}
}

func TestGradingSelectionConcurrentFirstSaveHasOneWinner(t *testing.T) {
	store, school, chat, parser := documentSelectionFixture()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{chat, parser} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := store.SaveGradingModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: id})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrGradingSelectionChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
}

func TestGradingSelectionHTTPIsSchoolScopedAndRevisionChecked(t *testing.T) {
	store, school, _, selected := documentSelectionFixture()
	h := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	body := fmt.Sprintf(`{"tenant_id":%q,"expected_revision":"absent","mode":"model","model_config_id":%q}`, school, selected)
	for _, want := range []int{200, 409} {
		response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/grading-model-selection", body, h.SavePlatformGradingModelSelection)
		if response.Code != want || strings.Contains(response.Body.String(), "synthetic-document-key") {
			t.Fatalf("HTTP %d expected %d", response.Code, want)
		}
	}
	response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/platform/model-applications?tenant_id="+uuid.NewString(), "", h.GetPlatformModelApplications)
	if response.Code != 200 || strings.Contains(response.Body.String(), selected) || !strings.Contains(response.Body.String(), `"grading_selection"`) {
		t.Fatalf("foreign scope %d %s", response.Code, response.Body.String())
	}
	forbidden := user
	forbidden.TenantID = uuid.NewString()
	response = performHandlerRequest(t, forbidden, http.MethodPut, "/api/v1/platform/grading-model-selection", body, h.SavePlatformGradingModelSelection)
	if response.Code != 403 {
		t.Fatalf("cross school write: %d", response.Code)
	}
}

func TestGradingSelectionRejectsMalformedModesAndIDs(t *testing.T) {
	for _, input := range []DocumentModelSelectionInput{{ExpectedRevision: "absent", Mode: "follow_chat"}, {ExpectedRevision: "absent", Mode: "model"}, {ExpectedRevision: "bad", Mode: "unconfigured"}, {ExpectedRevision: "absent", Mode: "unconfigured", ModelConfigID: uuid.NewString()}} {
		if _, err := normalizeGradingSelection(input); !errors.Is(err, ErrInvalidManagedConfig) {
			t.Fatalf("accepted input %#v", input)
		}
	}
}

func TestGradingSelectionProbeUsesAuthorizedSchoolAndRejectsConnectionInput(t *testing.T) {
	store, school, _, _ := documentSelectionFixture()
	h := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	calls := 0
	h.WithGradingModelProbe(func(ctx context.Context, tenant, requestID string) (json.RawMessage, error) {
		calls++
		contextTenant, ok := database.TenantFromContext(ctx)
		if !ok || contextTenant != school || tenant != school || requestID != "synthetic-probe-123" {
			t.Fatalf("probe tenant mismatch: %q %q", tenant, contextTenant)
		}
		return json.RawMessage(`{"capability":{"visual_supported":true}}`), nil
	})
	body := fmt.Sprintf(`{"tenant_id":%q,"request_id":"synthetic-probe-123"}`, school)
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/grading-model-selection/probe", body, h.ProbePlatformGradingModelSelection)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("authorized probe status=%d calls=%d", response.Code, calls)
	}
	forbidden := user
	forbidden.TenantID = uuid.NewString()
	response = performHandlerRequest(t, forbidden, http.MethodPost, "/api/v1/platform/grading-model-selection/probe", body, h.ProbePlatformGradingModelSelection)
	if response.Code != http.StatusForbidden || calls != 1 {
		t.Fatalf("foreign probe status=%d calls=%d", response.Code, calls)
	}
	// 接口只接受已保存的用途，不能借探测请求临时注入连接或密钥。
	response = performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/grading-model-selection/probe", strings.TrimSuffix(body, "}")+`,"api_key":"injected"}`, h.ProbePlatformGradingModelSelection)
	if response.Code != http.StatusBadRequest || calls != 1 {
		t.Fatalf("connection injection status=%d calls=%d", response.Code, calls)
	}
}
