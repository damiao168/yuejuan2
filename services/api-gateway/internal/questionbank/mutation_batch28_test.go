package questionbank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func batch28MemoryFixture(t *testing.T) (*MemoryStore, auth.AccessScope, Bank, ItemResult) {
	t.Helper()
	store := NewMemoryStore()
	scope := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank, err := store.CreateBank(context.Background(), scope, CreateBankInput{SchoolID: uuid.NewString(), Name: "Bank"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateItem(context.Background(), scope, bank.ID, CreateItemInput{ItemCode: "M1", Content: fixtureContent()})
	if err != nil {
		t.Fatal(err)
	}
	return store, scope, bank, item
}

func assertBatch28JSON(t *testing.T, got, want any) {
	t.Helper()
	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	// 回执按 JSON 保存；空 metadata 的 nil/map 表示差异不影响对外字段。
	if !bytes.Equal(actual, expected) {
		t.Fatalf("result=%s, want=%s", actual, expected)
	}
}

func TestBatch28MemoryCommandKeepsRawInputAndIdentity(t *testing.T) {
	store, scope, bank, _ := batch28MemoryFixture(t)
	ctx := commandreceipt.WithID(context.Background(), "batch28-bank")
	input := CreateBankInput{SchoolID: bank.SchoolID, Name: "  Raw name  "}
	first, err := store.CreateBank(ctx, scope, input)
	if err != nil || first.Name != "Raw name" {
		t.Fatalf("create=%+v, error=%v", first, err)
	}
	replay, err := store.CreateBank(ctx, scope, input)
	if err != nil {
		t.Fatal(err)
	}
	assertBatch28JSON(t, replay, first)
	changed := input
	changed.Name = first.Name
	if _, err = store.CreateBank(ctx, scope, changed); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("normalized input bypassed raw fingerprint: %v", err)
	}
	itemInput := CreateItemInput{ItemCode: "M2", Content: fixtureContent()}
	if _, err = store.CreateItem(ctx, scope, bank.ID, itemInput); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("different operation reused command: %v", err)
	}
	itemCtx := commandreceipt.WithID(context.Background(), "batch28-item")
	if _, err = store.CreateItem(itemCtx, scope, bank.ID, itemInput); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateItem(itemCtx, scope, first.ID, itemInput); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("different target reused command: %v", err)
	}
	for _, identity := range []string{"actor", "tenant"} {
		t.Run(identity, func(t *testing.T) {
			other := scope
			if identity == "actor" {
				other.ActorID = uuid.NewString()
			} else {
				other.TenantID = uuid.NewString()
			}
			created, err := store.CreateBank(ctx, other, input)
			if err != nil || created.ID == first.ID || created.TenantID != other.TenantID || created.CreatedBy != other.ActorID {
				t.Fatalf("identity leaked receipt: %+v, error=%v", created, err)
			}
		})
	}
}

func TestBatch28MemoryCommandHonorsOriginalContextAndFailedRetry(t *testing.T) {
	store, scope, bank, _ := batch28MemoryFixture(t)
	ctx := commandreceipt.WithID(context.Background(), "batch28-original")
	ctx = commandreceipt.WithInput(ctx, map[string]any{"school_id": bank.SchoolID, "name": "original transport"})
	first, err := store.CreateBank(ctx, scope, CreateBankInput{SchoolID: bank.SchoolID, Name: "First completion"})
	if err != nil {
		t.Fatal(err)
	}
	// 调用方补全的字段可以变化；同一 context 中保存的原传输请求仍代表同一命令。
	replay, err := store.CreateBank(ctx, scope, CreateBankInput{SchoolID: bank.SchoolID, Name: "Later completion"})
	if err != nil {
		t.Fatal(err)
	}
	assertBatch28JSON(t, replay, first)
	retryCtx := commandreceipt.WithID(context.Background(), "batch28-retry")
	input := CreateItemInput{ItemCode: "M1", Content: fixtureContent()}
	failed, err := store.CreateItem(retryCtx, scope, bank.ID, input)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate item error=%v", err)
	}
	assertBatch28JSON(t, failed, ItemResult{})
	input.ItemCode = "M2"
	created, err := store.CreateItem(retryCtx, scope, bank.ID, input)
	if err != nil || created.Item.ItemCode != input.ItemCode {
		t.Fatalf("failed command left success receipt: %+v, error=%v", created, err)
	}
}

func TestBatch28MemoryReplayChecksCurrentPermissionForEveryResult(t *testing.T) {
	cases := []struct {
		name, action string
		zero         any
		call         func(context.Context, *MemoryStore, auth.AccessScope, Bank, ItemResult) (any, error)
	}{
		{"bank", "manage", Bank{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, bank Bank, _ ItemResult) (any, error) {
			return s.UpdateBank(ctx, scope, bank.ID, UpdateBankInput{ExpectedRevision: bank.Revision, Name: "Updated", Status: "active"})
		}},
		{"created_item", "create", ItemResult{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, bank Bank, _ ItemResult) (any, error) {
			return s.CreateItem(ctx, scope, bank.ID, CreateItemInput{ItemCode: "M2", Content: fixtureContent()})
		}},
		{"version", "edit", Version{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, _ Bank, item ItemResult) (any, error) {
			return s.CreateVersion(ctx, scope, item.Item.ID, CreateVersionInput{SourceVersionID: item.Version.ID})
		}},
		{"metadata_schema", "manage", MetadataSchema{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, bank Bank, _ ItemResult) (any, error) {
			return s.UpdateMetadataSchema(ctx, scope, bank.ID, UpdateMetadataSchemaInput{ExpectedRevision: bank.Revision})
		}},
		{"acl", "manage", ACLDocument{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, bank Bank, _ ItemResult) (any, error) {
			return s.UpdateACL(ctx, scope, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision})
		}},
		{"retired_item", "retire", Item{}, func(ctx context.Context, s *MemoryStore, scope auth.AccessScope, _ Bank, item ItemResult) (any, error) {
			return s.RetireItem(ctx, scope, item.Item.ID, RetireItemInput{ExpectedRevision: item.Item.Revision})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, scope, bank, item := batch28MemoryFixture(t)
			// 创建者默认没有 retire；本场景仅准备该公开操作所需权限。
			store.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "retire")] = true
			ctx := commandreceipt.WithID(context.Background(), "batch28-replay")
			first, err := tc.call(ctx, store, scope, bank, item)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := tc.call(ctx, store, scope, bank, item)
			if err != nil {
				t.Fatal(err)
			}
			assertBatch28JSON(t, replay, first)
			invalid := scope
			invalid.ActorID = ""
			blocked, err := tc.call(ctx, store, invalid, bank, item)
			if !errors.Is(err, auth.ErrForbidden) {
				t.Fatalf("invalid scope replay error=%v", err)
			}
			assertBatch28JSON(t, blocked, tc.zero)
			store.mu.Lock()
			delete(store.acl, aclKey(scope.TenantID, bank.ID, scope.ActorID, tc.action))
			store.mu.Unlock()
			blocked, err = tc.call(ctx, store, scope, bank, item)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("revoked %s replay error=%v", tc.action, err)
			}
			assertBatch28JSON(t, blocked, tc.zero)
		})
	}
}
