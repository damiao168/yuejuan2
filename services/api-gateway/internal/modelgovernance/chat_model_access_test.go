package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func TestChatAccessMultiProviderAndIsolation(t *testing.T) {
	ctx := context.Background()
	store, school, first, second := documentSelectionFixture()
	other := store.managedConfigs[second]
	other.ProviderKey = "second-provider"
	store.managedConfigs[second] = other
	original, err := store.GetChatModelAccess(ctx, school)
	if err != nil || len(original.ModelIDs) != 1 || original.ModelIDs[0] != first {
		t.Fatalf("legacy: %+v %v", original, err)
	}
	// 保存普通回复资格即可；结构化未通过不能阻止聊天开放。
	other.LastCapabilityStatus = "failed"
	store.managedConfigs[second] = other
	access, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: original.Revision, ModelIDs: []string{first, second}})
	if err != nil || len(access.ModelIDs) != 2 {
		t.Fatalf("allow two: %+v %v", access, err)
	}
	for _, id := range []string{first, second} {
		connection, err := ResolveChatManagedAPI(ctx, store, school, id)
		if err != nil || connection == nil || connection.Config.ID != id {
			t.Fatalf("resolve %s: %+v %v", id, connection, err)
		}
	}
	if !store.managedConfigs[first].IsDefault || store.managedConfigs[second].IsDefault {
		t.Fatal("chat access must not alter parsing default")
	}
	for _, id := range []string{first, second} {
		if _, err := ResolveChatManagedAPI(ctx, store, uuid.NewString(), id); !errors.Is(err, ErrManagedConfigUnavailable) {
			t.Fatalf("cross school: %v", err)
		}
	}
	access.ModelIDs[0] = "tampered"
	again, _ := store.GetChatModelAccess(ctx, school)
	if contains(again.ModelIDs, "tampered") {
		t.Fatal("caller mutated saved access")
	}
	if _, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: original.Revision, ModelIDs: []string{first}}); !errors.Is(err, ErrChatAccessChanged) {
		t.Fatalf("stale revision: %v", err)
	}
	other.Status = "disabled"
	store.managedConfigs[second] = other
	if _, err = ResolveChatManagedAPI(ctx, store, school, second); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: again.Revision, ModelIDs: []string{first}}); err != nil {
		t.Fatal(err)
	}
	other.Status = "active"
	store.managedConfigs[second] = other
	if _, err = ResolveChatManagedAPI(ctx, store, school, second); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("revoked cannot be reused: %v", err)
	}
}

func TestChatAccessRejectsUnverifiedAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	store, school, first, second := documentSelectionFixture()
	config := store.managedConfigs[second]
	config.LastTextStatus = "untested"
	store.managedConfigs[second] = config
	if _, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first, second}}); !errors.Is(err, ErrManagedTextRequired) {
		t.Fatalf("text gate: %v", err)
	}
	foreign := uuid.NewString()
	if _, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{foreign}}); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("unknown id: %v", err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first}})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrChatAccessChanged) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent saves %d %d", successes, conflicts)
	}
}

func TestSchoolChatListsApprovedMetadataOnly(t *testing.T) {
	store, school, first, second := documentSelectionFixture()
	ctx := context.Background()
	if _, err := store.SaveChatModelAccess(ctx, school, uuid.NewString(), ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first, second}}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, auth.NewMemoryStore(), nil, testBaseline())
	schoolAdmin := auth.User{ID: uuid.NewString(), TenantID: school, Roles: []string{"school_admin"}}
	response := performHandlerRequest(t, schoolAdmin, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
	if response.Code != 200 || !strings.Contains(response.Body.String(), second) || strings.Contains(response.Body.String(), "synthetic-document-key") || strings.Contains(response.Body.String(), "base_url") {
		t.Fatalf("metadata response %d %s", response.Code, response.Body.String())
	}
	response = performHandlerRequest(t, schoolAdmin, http.MethodGet, "/api/v1/platform/chat-model-access?tenant_id="+school, "", handler.GetPlatformChatModelAccess)
	if response.Code != 403 {
		t.Fatalf("school admin cannot grant models: %d %s", response.Code, response.Body.String())
	}
}

func TestSchoolChatListsOpenedConfigurationsAndAvailability(t *testing.T) {
	store, school, first, second := documentSelectionFixture()
	third := uuid.NewString()
	for index, id := range []string{first, second, third} {
		config := store.managedConfigs[first]
		config.ID = id
		config.IsDefault = id == first
		config.ModelName = "deepseek-flash"
		config.ProviderKey = []string{"custom", "custom", "deepseek"}[index]
		config.LastTestStatus = "success"
		store.managedConfigs[id] = config
		store.managedSecrets[id] = "synthetic-document-key"
	}
	access, err := store.SaveChatModelAccess(t.Context(), school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first, second, third}})
	if err != nil {
		t.Fatal(err)
	}
	// 已开放项的普通回复后来失败，连接和结构化检测通过也不能替代；同名配置仍按编号保留。
	config := store.managedConfigs[third]
	config.LastTextStatus = "failed"
	config.LastTextMessage = "synthetic-private-provider-error https://private.example sk-synthetic-secret"
	config.BaseURL = "https://private.example"
	store.managedConfigs[third] = config
	handler := NewHandler(store, nil, nil, testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: school, Roles: []string{"school_admin"}}
	readModels := func() map[string]ManagedChatModelStatus {
		t.Helper()
		response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
		if response.Code != http.StatusOK {
			t.Fatalf("metadata response %d", response.Code)
		}
		for _, private := range []string{"synthetic-document-key", "synthetic-private-provider-error", "private.example", "sk-synthetic-secret", "base_url", "last_text_message"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatalf("model metadata exposed private field %q", private)
			}
		}
		var payload struct {
			Model  ManagedChatModelStatus   `json:"model"`
			Models []ManagedChatModelStatus `json:"models"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Models) != 3 || payload.Model.ID != first {
			t.Fatalf("opened configurations or default changed: %+v", payload)
		}
		models := map[string]ManagedChatModelStatus{}
		for _, model := range payload.Models {
			if _, duplicate := models[model.ID]; duplicate {
				t.Fatalf("duplicate configuration ID %q", model.ID)
			}
			models[model.ID] = model
		}
		return models
	}
	models := readModels()
	if !models[first].Available || !models[second].Available || models[third].Available || models[third].Message != "普通回复检测未通过，请联系平台管理员重新检测" {
		t.Fatalf("expected three opened and two available configurations: %+v", models)
	}
	if models[first].ProviderKey != "custom" || models[third].ProviderKey != "deepseek" || models[first].ModelName != models[third].ModelName {
		t.Fatalf("same-name provider configurations were merged: %+v", models)
	}
	if _, err := ResolveChatManagedAPI(t.Context(), store, school, third); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("unavailable opened configuration remained callable: %v", err)
	}
	probe := successfulManagedProbe()
	probe.ProbeMode = "text"
	if _, err := store.RecordManagedAPIProbe(t.Context(), school, third, config.UpdatedAt, probe); err != nil {
		t.Fatal(err)
	}
	models = readModels()
	if !models[third].Available || models[third].Message != "当前模型可用" {
		t.Fatalf("successful ordinary-reply retest did not restore availability: %+v", models[third])
	}
	after, err := store.GetChatModelAccess(t.Context(), school)
	if err != nil || after.Revision != access.Revision {
		t.Fatalf("availability change rewrote model authorization: %+v %v", after, err)
	}
	if connection, err := ResolveChatManagedAPI(t.Context(), store, school, third); err != nil || connection == nil || connection.Config.ID != third {
		t.Fatalf("recovered configuration could not be selected: %v", err)
	}
}

func TestSchoolChatUnavailableMetadataExplainsQualification(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ManagedAPIConfig)
		want   string
	}{
		{name: "disabled", change: func(config *ManagedAPIConfig) { config.Status = "disabled" }, want: "模型已停用，请联系平台管理员"},
		{name: "missing credential", change: func(config *ManagedAPIConfig) { config.CredentialConfigured = false }, want: "模型未配置密钥，请联系平台管理员"},
		{name: "ordinary reply failed", change: func(config *ManagedAPIConfig) { config.LastTextStatus = "failed" }, want: "普通回复检测未通过，请联系平台管理员重新检测"},
		{name: "ordinary reply interrupted", change: func(config *ManagedAPIConfig) { config.LastTextStatus = "temporary_unavailable" }, want: "普通回复检测暂未完成，请联系平台管理员重新检测"},
		{name: "ordinary reply untested", change: func(config *ManagedAPIConfig) { config.LastTextStatus = "untested" }, want: "普通回复尚未验证，请联系平台管理员检测"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, school, first, _ := documentSelectionFixture()
			config := store.managedConfigs[first]
			test.change(&config)
			store.managedConfigs[first] = config
			handler := NewHandler(store, nil, nil, testBaseline())
			user := auth.User{ID: uuid.NewString(), TenantID: school, Roles: []string{"school_admin"}}
			response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
			var payload struct {
				Models []ManagedChatModelStatus `json:"models"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(payload.Models) != 1 || payload.Models[0].Available || payload.Models[0].Message != test.want {
				t.Fatalf("unavailable reason: status=%d models=%+v", response.Code, payload.Models)
			}
		})
	}
}

func TestChatAccessRevalidatesEachRequestAndEveryNewGrant(t *testing.T) {
	for _, state := range []string{"disabled", "credential removed", "text evidence invalidated", "deleted"} {
		t.Run(state, func(t *testing.T) {
			store, school, first, second := documentSelectionFixture()
			ctx := t.Context()
			access, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first, second}})
			if err != nil {
				t.Fatal(err)
			}
			config := store.managedConfigs[second]
			switch state {
			case "disabled":
				config.Status = "disabled"
			case "credential removed":
				config.CredentialConfigured = false
			case "text evidence invalidated":
				config.LastTextStatus = "untested"
			case "deleted":
				now := time.Now().UTC()
				config.DeletedAt = &now
			}
			store.managedConfigs[second] = config
			if connection, err := ResolveChatManagedAPI(ctx, store, school, second); err == nil || connection != nil {
				t.Fatalf("invalid connection reused: %+v %v", connection, err)
			}
			// 其他已开放模型仍可使用；界面可一次保存撤销失效模型，之后不能绕过资格重新开放。
			if connection, err := ResolveChatManagedAPI(ctx, store, school, first); err != nil || connection == nil || connection.Config.ID != first {
				t.Fatalf("healthy model blocked: %+v %v", connection, err)
			}
			if state != "deleted" {
				access, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{first, second}})
				if err != nil {
					t.Fatalf("unavailable retained grant blocked other edits: %v", err)
				}
			}
			access, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{first}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{first, second}}); err == nil {
				t.Fatal("invalid model granted after revocation")
			}
		})
	}
}

func TestChatAccessExplicitEmptyPolicyDoesNotRestoreLegacyDefault(t *testing.T) {
	store, school, first, second := documentSelectionFixture()
	ctx := t.Context()
	access, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{second}})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := ResolveChatManagedAPI(ctx, store, school, "")
	if err != nil || connection == nil || connection.Config.ID != second {
		t.Fatalf("implicit choice escaped approved models: %+v %v", connection, err)
	}
	if _, err = store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	connection, err = ResolveChatManagedAPI(ctx, store, school, "")
	if err != nil || connection != nil {
		t.Fatalf("empty saved policy restored default: %+v %v", connection, err)
	}
	if _, err = ResolveChatManagedAPI(ctx, store, school, first); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("legacy default remained callable after revoke all: %v", err)
	}
	if !store.managedConfigs[first].IsDefault {
		t.Fatal("chat revocation changed parser default")
	}
}

func TestChatAccessImplicitSelectionSkipsUnavailableDefault(t *testing.T) {
	store, school, first, second := documentSelectionFixture()
	ctx := t.Context()
	if _, err := store.SaveChatModelAccess(ctx, school, "actor", ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first, second}}); err != nil {
		t.Fatal(err)
	}
	config := store.managedConfigs[first]
	config.LastTextStatus = "untested"
	store.managedConfigs[first] = config
	connection, err := ResolveChatManagedAPI(ctx, store, school, "")
	if err != nil || connection == nil || connection.Config.ID != second {
		t.Fatalf("implicit selection did not find available approved model: %+v %v", connection, err)
	}
	if _, err = ResolveChatManagedAPI(ctx, store, school, first); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("explicit unavailable model silently changed: %v", err)
	}
	handler := NewHandler(store, nil, nil, testBaseline())
	user := auth.User{ID: uuid.NewString(), TenantID: school, Roles: []string{"school_admin"}}
	response := performHandlerRequest(t, user, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":{"id":"`+second+`"`) {
		t.Fatalf("metadata default differs from runtime selection: %d %s", response.Code, response.Body.String())
	}
}
