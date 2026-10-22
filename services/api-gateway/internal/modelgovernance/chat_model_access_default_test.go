package modelgovernance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type chatDefaultTestStore interface {
	ManagedAPIConfigStore
	ChatModelAccessStore
}

func TestChatAccessPreservesInheritedScopeAcrossDefaultChanges(t *testing.T) {
	for _, action := range []string{"rename", "clear", "disable and reenable", "switch", "create new default", "explicit empty", "stale rollback"} {
		t.Run(action, func(t *testing.T) {
			verifyInheritedChatAccessDefaultChange(t, NewMemoryStore(), uuid.NewString(), uuid.NewString(), action)
		})
	}
}

// 同一组行为同时保护内存仓储与 PostgreSQL：默认项只影响原默认用途，不能顺带增加或撤销聊天开放范围。
func verifyInheritedChatAccessDefaultChange(t *testing.T, store chatDefaultTestStore, school, actor, action string) {
	t.Helper()
	ctx := context.Background()
	probe := successfulManagedProbe()
	probe.ProbeMode, probe.GeneratedRequest, probe.TextCheck.OK = "text", true, true
	input := ManagedAPIConfigInput{ProviderKey: "custom", DisplayName: "合成模型", AdapterType: "openai_compatible", BaseURL: "https://example.invalid/v1", ModelName: "synthetic-model", ModelVersion: "v1", Region: "global", Status: "active", APIKey: "synthetic-chat-default-key-2026", IsDefault: true, InitialProbe: &probe}
	first, err := store.CreateManagedAPIConfig(ctx, school, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	input.IsDefault = false
	second, err := store.CreateManagedAPIConfig(ctx, school, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetChatModelAccess(ctx, school)
	if err != nil || before.Revision != "absent" || len(before.ModelIDs) != 1 || before.ModelIDs[0] != first.ID {
		t.Fatalf("initial scope: %+v %v", before, err)
	}
	update := chatDefaultUpdate(first)
	update.IsDefault = false
	switch action {
	case "rename":
		update.IsDefault, update.DisplayName = true, "改名"
	case "disable and reenable":
		update.Status = "disabled"
	case "switch":
		update = chatDefaultUpdate(second)
		update.IsDefault = true
	case "create new default":
		input.IsDefault = true
		second, err = store.CreateManagedAPIConfig(ctx, school, actor, input)
	case "explicit empty":
		before, err = store.SaveChatModelAccess(ctx, school, actor, ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{}})
	case "stale rollback":
		update.ExpectedUpdatedAt = first.UpdatedAt.Add(-time.Microsecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if action != "create new default" {
		id := first.ID
		if action == "switch" {
			id = second.ID
		}
		_, err = store.UpdateManagedAPIConfig(ctx, school, id, update)
		if action == "stale rollback" {
			if !errors.Is(err, ErrManagedProbeStale) {
				t.Fatalf("stale update: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.GetChatModelAccess(ctx, school)
	if err != nil {
		t.Fatal(err)
	}
	if action == "explicit empty" {
		if after.Revision != before.Revision || len(after.ModelIDs) != 0 {
			t.Fatalf("explicit revoke overwritten: %+v", after)
		}
		return
	}
	if len(after.ModelIDs) != 1 || after.ModelIDs[0] != first.ID {
		t.Fatalf("default change altered inherited chat scope: %+v", after)
	}
	if action == "rename" || action == "stale rollback" {
		if after.Revision != "absent" {
			t.Fatalf("unrelated or rejected update materialized access: %+v", after)
		}
	} else if after.Revision == "absent" {
		t.Fatal("inherited access was not frozen before default changed")
	}
	if action == "disable and reenable" {
		if _, err = ResolveChatManagedAPI(ctx, store, school, first.ID); !errors.Is(err, ErrManagedConfigUnavailable) {
			t.Fatalf("disabled model still callable: %v", err)
		}
		update.Status = "active"
		if _, err = store.UpdateManagedAPIConfig(ctx, school, first.ID, update); err != nil {
			t.Fatal(err)
		}
	}
	if connection, err := ResolveChatManagedAPI(ctx, store, school, first.ID); err != nil || connection == nil || connection.Config.ID != first.ID {
		t.Fatalf("inherited model lost after default change: %+v %v", connection, err)
	}
	if _, err = ResolveChatManagedAPI(ctx, store, school, second.ID); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("new parser default unexpectedly granted for chat: %v", err)
	}
}

func chatDefaultUpdate(config ManagedAPIConfig) ManagedAPIConfigUpdateInput {
	return ManagedAPIConfigUpdateInput{DisplayName: config.DisplayName, AdapterType: config.AdapterType, BaseURL: config.BaseURL, ModelName: config.ModelName, ModelVersion: config.ModelVersion, Region: config.Region, Status: config.Status, IsDefault: config.IsDefault}
}
