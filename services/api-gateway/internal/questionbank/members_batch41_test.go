package questionbank

import (
	"context"
	"sort"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func TestBatch41MemberDirectoryManagerKeepsAuthorizedMembersAndSortedActions(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	owner := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank, err := store.CreateBank(commandreceipt.WithID(ctx, "batch41-bank"), owner, CreateBankInput{SchoolID: uuid.NewString(), Name: "成员视图"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, author, reviewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	bindings := []ACLBinding{{UserID: viewer, Preset: "Viewer"}, {UserID: author, Preset: "Author"}, {UserID: reviewer, Preset: "Reviewer"}}
	if _, err = store.UpdateACL(commandreceipt.WithID(ctx, "batch41-acl"), owner, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: bindings}); err != nil {
		t.Fatal(err)
	}
	// 内存 ACL 允许单动作条目；只有 review 而没有 read 的成员不能成为读者的联系人。
	reviewOnly := uuid.NewString()
	store.acl[aclKey(owner.TenantID, bank.ID, reviewOnly, "review")] = true
	members, err := store.ListMembers(ctx, owner, bank.ID)
	if err != nil || len(members) != 5 {
		t.Fatalf("manager directory: %#v %v", members, err)
	}
	found := map[string]bool{}
	for i, member := range members {
		found[member.UserID] = true
		if !sort.StringsAreSorted(member.Actions) || (i > 0 && members[i-1].UserID >= member.UserID) {
			t.Fatalf("unstable member or action order: %#v", members)
		}
	}
	for _, id := range []string{owner.ActorID, viewer, author, reviewer, reviewOnly} {
		if !found[id] {
			t.Fatalf("manager missing authorized member %s", id)
		}
	}
	reader := auth.AccessScope{TenantID: owner.TenantID, ActorID: viewer, TenantWide: true}
	contacts, err := store.ListMembers(ctx, reader, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range contacts {
		if member.UserID == reviewOnly || member.UserID == viewer || member.UserID == author {
			t.Fatalf("reader saw non-contact: %#v", member)
		}
	}
}
