package questionbank

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func TestUXACLRetainsHistoricalCustomFactsWhileUpdatingPresetRoles(t *testing.T) {
	s, owner, bank := batch23MemoryBank()
	customUser, oldViewer, newAuthor, member := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	historical := ACLDocument{Bindings: []ACLBinding{{UserID: customUser, Preset: "Custom", Actions: []string{"read", "review", "statistics"}}, {UserID: oldViewer, Preset: "Viewer", Actions: []string{"read", "statistics"}}}, Groups: []ACLGroup{{ID: uuid.NewString(), Name: "历史复核组", Preset: "Custom", Actions: []string{"read", "review", "publish"}, MemberIDs: []string{member}}}}
	s.aclDocs[bank.ID] = copyACL(historical)
	seedHistoricalQuestionBankCustomGrants(s, owner, bank, historical, member)
	input := UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: []ACLBinding{{UserID: newAuthor, Preset: "Author", Actions: []string{"manage"}}}}
	ctx := commandreceipt.WithID(context.Background(), "ux-acl-preserve")
	got, err := s.UpdateACL(ctx, owner, bank.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Groups, historical.Groups) {
		t.Fatalf("historical group changed: %+v", got.Groups)
	}
	if !reflect.DeepEqual(got.Bindings[0], historical.Bindings[0]) {
		t.Fatalf("historical direct grant changed: %+v", got.Bindings)
	}
	if s.acl[aclKey(owner.TenantID, bank.ID, oldViewer, "read")] || !s.acl[aclKey(owner.TenantID, bank.ID, newAuthor, "edit")] || s.acl[aclKey(owner.TenantID, bank.ID, newAuthor, "manage")] {
		t.Fatal("preset replacement or untrusted actions changed authorization")
	}
	for _, action := range historical.Groups[0].Actions {
		if !s.acl[aclKey(owner.TenantID, bank.ID, member, action)] {
			t.Fatalf("group action removed: %s", action)
		}
	}
	replay, err := s.UpdateACL(ctx, owner, bank.ID, input)
	if err != nil || !reflect.DeepEqual(got, replay) {
		t.Fatalf("replay changed ACL: %+v %v", replay, err)
	}
	got.Groups[0].MemberIDs[0] = "client mutation"
	stored, err := s.GetACL(context.Background(), owner, bank.ID)
	if err != nil || stored.Groups[0].MemberIDs[0] != member {
		t.Fatalf("returned facts alias store: %+v %v", stored, err)
	}
	for _, bad := range []auth.AccessScope{{TenantID: uuid.NewString(), ActorID: owner.ActorID, TenantWide: true}, {TenantID: owner.TenantID, ActorID: owner.ActorID, SchoolIDs: []string{uuid.NewString()}}, {TenantID: owner.TenantID, ActorID: uuid.NewString(), TenantWide: true}} {
		if _, err := s.UpdateACL(commandreceipt.WithID(context.Background(), uuid.NewString()), bad, bank.ID, UpdateACLInput{ExpectedRevision: stored.Revision}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope bypassed ACL: %v", err)
		}
	}
}

func TestUXACLRejectsCustomReplacementBeforeChangingAnyGrant(t *testing.T) {
	user, groupID := uuid.NewString(), uuid.NewString()
	existing := ACLDocument{Bindings: []ACLBinding{{UserID: user, Preset: "Custom", Actions: []string{"read", "manage"}}}, Groups: []ACLGroup{{ID: groupID, Name: "旧组", Preset: "Custom", Actions: []string{"read", "manage"}}}}
	for _, input := range []UpdateACLInput{
		{ExpectedRevision: 3, Bindings: []ACLBinding{{UserID: strings.ToUpper(user), Preset: "Viewer"}}},
		{ExpectedRevision: 3, Groups: []ACLGroup{{ID: strings.ToUpper(groupID), Name: "换名", Preset: "Viewer"}}},
		{ExpectedRevision: 3, Groups: []ACLGroup{{Name: " 旧组 ", Preset: "Reviewer"}}},
		{ExpectedRevision: 3, Bindings: []ACLBinding{{UserID: uuid.NewString(), Preset: "Custom"}}},
	} {
		s, scope, bank := batch23MemoryBank()
		before := copyACL(existing)
		s.aclDocs[bank.ID] = copyACL(existing)
		_, err := s.UpdateACL(commandreceipt.WithID(context.Background(), uuid.NewString()), scope, bank.ID, input)
		if !errors.Is(err, ErrInvalidInput) || s.banks[bank.ID].Revision != 3 || !reflect.DeepEqual(s.aclDocs[bank.ID], before) {
			t.Fatalf("invalid replacement changed stored facts: %v", err)
		}
	}
}

func TestUXACLDoesNotConvertRevokedOperatorGroupRightsIntoDirectGrants(t *testing.T) {
	s, scope, bank := batch23MemoryBank()
	s.aclDocs[bank.ID] = ACLDocument{Groups: []ACLGroup{{ID: uuid.NewString(), Name: "可撤销组", Preset: "Author", Actions: []string{"read", "create", "edit"}, MemberIDs: []string{scope.ActorID}}}}
	for _, action := range []string{"create", "edit"} {
		s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, action)] = true
	}
	got, err := s.UpdateACL(commandreceipt.WithID(context.Background(), "ux-group-revoke"), scope, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "create", "edit"} {
		if s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, action)] {
			t.Fatalf("removed group became direct %s: %+v", action, got)
		}
	}
	if !s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage")] {
		t.Fatal("operator lost manage")
	}
}

func TestUXPostgresACLReplacementExcludesCustomObjectsFromDeletion(t *testing.T) {
	ctx, scope := context.Background(), batch22Scope()
	retained := ACLDocument{Bindings: []ACLBinding{{UserID: "historic-user"}}, Groups: []ACLGroup{{ID: "historic-group"}}}
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "exec", fragment: "VALUES($1,$2,$3,'manage')"},
		{kind: "exec", fragment: "AND NOT(user_id::text=ANY($5::text[]))", args: map[int]any{0: scope.TenantID, 1: batch22BankID, 2: "creator", 3: scope.ActorID, 4: []string{"historic-user"}}},
		{kind: "exec", fragment: "AND NOT(id::text=ANY($3::text[]))", args: map[int]any{0: scope.TenantID, 1: batch22BankID, 2: []string{"historic-group"}}},
	})
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = replacePostgresACL(ctx, tx, scope, batch22BankID, "creator", UpdateACLInput{}, retained); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertBatch22Script(t, script, "commit")
}

// 历史自定义授权先按其已有动作落入夹具，预设角色替换不能顺带抹除这些事实。
func seedHistoricalQuestionBankCustomGrants(s *MemoryStore, owner auth.AccessScope, bank Bank, historical ACLDocument, member string) {
	for _, binding := range historical.Bindings {
		for _, action := range binding.Actions {
			s.acl[aclKey(owner.TenantID, bank.ID, binding.UserID, action)] = true
		}
	}
	for _, action := range historical.Groups[0].Actions {
		s.acl[aclKey(owner.TenantID, bank.ID, member, action)] = true
	}
}
