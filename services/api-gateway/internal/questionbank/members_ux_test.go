package questionbank

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestMemberDirectoryLimitsReadersToCurrentReviewContacts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	owner := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank, err := store.CreateBank(commandreceipt.WithID(ctx, "bank"), owner, CreateBankInput{SchoolID: uuid.NewString(), Name: "学校题库"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, author, reviewer, publisher := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	bindings := []ACLBinding{{UserID: viewer, Preset: "Viewer"}, {UserID: author, Preset: "Author"}, {UserID: reviewer, Preset: "Reviewer"}, {UserID: publisher, Preset: "Publisher"}}
	doc, err := store.UpdateACL(commandreceipt.WithID(ctx, "acl"), owner, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	reader := auth.AccessScope{TenantID: owner.TenantID, ActorID: viewer, TenantWide: true}
	members, err := store.ListMembers(ctx, reader, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, member := range members {
		found[member.UserID] = true
	}
	if found[viewer] || found[author] || !found[reviewer] || !found[publisher] {
		t.Fatalf("non-contact exposed or authorized contact missing: %+v", members)
	}
	_, err = store.UpdateACL(commandreceipt.WithID(ctx, "revoke"), owner, bank.ID, UpdateACLInput{ExpectedRevision: doc.Revision, Bindings: bindings[:2]})
	if err != nil {
		t.Fatal(err)
	}
	members, err = store.ListMembers(ctx, reader, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.UserID == reviewer || member.UserID == publisher {
			t.Fatal("revoked contacts remained visible")
		}
	}
	for _, scope := range []auth.AccessScope{
		{TenantID: uuid.NewString(), ActorID: viewer, TenantWide: true},
		{TenantID: owner.TenantID, ActorID: viewer, SchoolIDs: []string{uuid.NewString()}},
		{TenantID: owner.TenantID, ActorID: uuid.NewString(), TenantWide: true},
	} {
		if _, err = store.ListMembers(ctx, scope, bank.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("directory bypassed bank scope: %v", err)
		}
	}
}
