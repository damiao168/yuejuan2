package modelgovernance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func TestAnswerSheetSelectionSeparatesChatAndAcceptsAdministratorAssignment(t *testing.T) {
	store, school, chat, selected := documentSelectionFixture()
	ctx := context.Background()
	initial, err := store.GetAnswerSheetModelSelection(ctx, school)
	if err != nil || initial.Mode != "unconfigured" || initial.Revision != "absent" {
		t.Fatalf("initial %#v %v", initial, err)
	}
	if _, err = ResolveAnswerSheetManagedAPI(ctx, store, school); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatalf("chat fallback %v", err)
	}
	// 管理员直接指定；text 元数据和未做结构化检测不能额外阻止这个独立用途。
	config := store.managedConfigs[selected]
	config.Modalities = []string{"text"}
	config.LastCapabilityStatus = "untested"
	store.managedConfigs[selected] = config
	selection, err := store.SaveAnswerSheetModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: selected})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := ResolveAnswerSheetManagedAPI(ctx, store, school)
	if err != nil || connection.Config.ID != selected || !store.managedConfigs[chat].IsDefault {
		t.Fatalf("independent selection %v", err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", selected); !errors.Is(err, ErrManagedConfigInUse) {
		t.Fatalf("referenced model deletion: %v", err)
	}
	selection, err = store.SaveAnswerSheetModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: selection.Revision, Mode: "unconfigured"})
	if err != nil || selection.ModelConfigID != "" {
		t.Fatalf("clear %#v %v", selection, err)
	}
	if _, err = ResolveAnswerSheetManagedAPI(ctx, store, school); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatal("clear unexpectedly fell back to chat")
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", selected); err != nil {
		t.Fatalf("unreferenced model deletion: %v", err)
	}
}

func TestAnswerSheetSelectionRejectsUnavailableOrForeignConfiguration(t *testing.T) {
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
			if _, err := store.SaveAnswerSheetModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: id}); !errors.Is(err, ErrAnswerSheetModelRequired) {
				t.Fatalf("accepted invalid config: %v", err)
			}
			// 保存后的配置后来失效时也要重新校验，不能切换到聊天默认项。
			store.answerSheetSelections[school] = DocumentModelSelection{Mode: "model", ModelConfigID: id}
			if connection, err := ResolveAnswerSheetManagedAPI(context.Background(), store, school); err == nil || connection != nil {
				t.Fatal("runtime accepted invalid configuration")
			}
		})
	}
}

func TestAnswerSheetSelectionConcurrentFirstSaveHasOneWinner(t *testing.T) {
	store, school, chat, parser := documentSelectionFixture()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{chat, parser} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := store.SaveAnswerSheetModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: id})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAnswerSheetSelectionChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
}

func TestAnswerSheetSelectionHTTPIsSchoolScopedAndRevisionChecked(t *testing.T) {
	store, school, _, selected := documentSelectionFixture()
	h := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	body := fmt.Sprintf(`{"tenant_id":%q,"expected_revision":"absent","mode":"model","model_config_id":%q}`, school, selected)
	for _, want := range []int{200, 409} {
		response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/answer-sheet-model-selection", body, h.SavePlatformAnswerSheetModelSelection)
		if response.Code != want || strings.Contains(response.Body.String(), "synthetic-document-key") {
			t.Fatalf("HTTP %d expected %d", response.Code, want)
		}
	}
	response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/platform/model-applications?tenant_id="+uuid.NewString(), "", h.GetPlatformModelApplications)
	if response.Code != 200 || strings.Contains(response.Body.String(), selected) || !strings.Contains(response.Body.String(), `"answer_sheet_selection"`) {
		t.Fatalf("foreign scope %d %s", response.Code, response.Body.String())
	}
	forbidden := user
	forbidden.TenantID = uuid.NewString()
	response = performHandlerRequest(t, forbidden, http.MethodPut, "/api/v1/platform/answer-sheet-model-selection", body, h.SavePlatformAnswerSheetModelSelection)
	if response.Code != 403 {
		t.Fatalf("cross school write: %d", response.Code)
	}
}

func TestAnswerSheetSelectionRejectsMalformedModesAndIDs(t *testing.T) {
	for _, input := range []DocumentModelSelectionInput{{ExpectedRevision: "absent", Mode: "follow_chat"}, {ExpectedRevision: "absent", Mode: "model"}, {ExpectedRevision: "bad", Mode: "unconfigured"}, {ExpectedRevision: "absent", Mode: "unconfigured", ModelConfigID: uuid.NewString()}} {
		if _, err := normalizeAnswerSheetSelection(input); !errors.Is(err, ErrInvalidManagedConfig) {
			t.Fatalf("accepted input %#v", input)
		}
	}
}
