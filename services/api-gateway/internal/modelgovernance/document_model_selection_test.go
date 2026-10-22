package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func documentSelectionFixture() (*MemoryStore, string, string, string) {
	store := NewMemoryStore()
	school, chat, parser := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{chat, parser} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: school, ModelName: id, Status: "active", CredentialConfigured: true, IsDefault: id == chat, LastTextStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
		store.managedSecrets[id] = "synthetic-document-key"
	}
	return store, school, chat, parser
}

func TestDocumentSelectionSeparatesChatAndRevalidatesParser(t *testing.T) {
	ctx := context.Background()
	store, school, chat, parser := documentSelectionFixture()
	selection, err := store.GetDocumentModelSelection(ctx, school)
	if err != nil || selection.Mode != "follow_chat" || selection.Revision != "absent" {
		t.Fatalf("initial: %#v %v", selection, err)
	}
	connection, err := ResolveDocumentManagedAPI(ctx, store, school)
	if err != nil || connection.Config.ID != chat {
		t.Fatalf("legacy follow: %#v %v", connection, err)
	}
	item := store.managedConfigs[parser]
	item.LastTextStatus = "failed"
	store.managedConfigs[parser] = item
	selection, err = store.SaveDocumentModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: parser})
	if err != nil {
		t.Fatal(err)
	}
	connection, err = ResolveDocumentManagedAPI(ctx, store, school)
	if err != nil || connection.Config.ID != parser || !store.managedConfigs[chat].IsDefault {
		t.Fatalf("independent parser: %#v %v", connection, err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", parser); !errors.Is(err, ErrManagedConfigInUse) {
		t.Fatalf("parser deletion: %v", err)
	}
	// 已选配置后来停用、失去证据或换成旧协议，都必须显式失败，不能悄悄换回聊天模型。
	for _, change := range []func(*ManagedAPIConfig){func(c *ManagedAPIConfig) { c.Status = "disabled" }, func(c *ManagedAPIConfig) { c.LastCapabilityStatus = "failed" }, func(c *ManagedAPIConfig) { c.LastCapabilityVersion = "structured-json-v3" }} {
		changed := item
		change(&changed)
		store.managedConfigs[parser] = changed
		if connection, err = ResolveDocumentManagedAPI(ctx, store, school); err == nil || connection != nil {
			t.Fatalf("invalid selected parser accepted: %#v %v", connection, err)
		}
	}
	store.managedConfigs[parser] = item
	_, err = store.SaveDocumentModelSelection(ctx, school, "actor", DocumentModelSelectionInput{ExpectedRevision: selection.Revision, Mode: "follow_chat"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, "actor", parser); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentSelectionRejectsForeignAndUnverifiedModels(t *testing.T) {
	for _, test := range []string{"foreign", "disabled", "no_credential", "no_evidence", "old_evidence"} {
		t.Run(test, func(t *testing.T) {
			store, school, _, parser := documentSelectionFixture()
			item := store.managedConfigs[parser]
			switch test {
			case "foreign":
				item.TenantID = uuid.NewString()
			case "disabled":
				item.Status = "disabled"
			case "no_credential":
				item.CredentialConfigured = false
			case "no_evidence":
				item.LastCapabilityStatus = "untested"
			case "old_evidence":
				item.LastCapabilityVersion = "structured-json-v3"
			}
			store.managedConfigs[parser] = item
			if _, err := store.SaveDocumentModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: parser}); !errors.Is(err, ErrManagedCapabilityRequired) {
				t.Fatalf("accepted %s: %v", test, err)
			}
		})
	}
}

func TestDocumentSelectionConcurrentFirstSaveHasSingleWinner(t *testing.T) {
	store, school, chat, parser := documentSelectionFixture()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{chat, parser} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := store.SaveDocumentModelSelection(context.Background(), school, "actor", DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: id})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrDocumentSelectionChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestDocumentSelectionHTTPIsSchoolScopedAndRevisionChecked(t *testing.T) {
	store, school, _, parser := documentSelectionFixture()
	handler := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	payload, _ := json.Marshal(map[string]string{"tenant_id": school, "expected_revision": "absent", "mode": "model", "model_config_id": parser})
	response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/document-model-selection", string(payload), handler.SavePlatformDocumentModelSelection)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "synthetic-document-key") {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	response = performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/document-model-selection", string(payload), handler.SavePlatformDocumentModelSelection)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale write: %d", response.Code)
	}
	response = performHandlerRequest(t, user, http.MethodGet, "/api/v1/platform/model-applications?tenant_id="+uuid.NewString(), "", handler.GetPlatformModelApplications)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), parser) {
		t.Fatalf("school isolation: %d %s", response.Code, response.Body.String())
	}
}
