package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"github.com/google/uuid"
)

func TestE2EPostgresUXHistoricalACLAndActualMemberResponsibilities(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	// 既有 helper 创建独立数据库并在结束时清理；历史组合事实直接落库，后续维护走真实 Store 事务。
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "teacher", "school_admin", "grader", "student"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	ownerID, reviewerID, groupMember, viewerID := e2eLookupUserID(t, db, "demo", "tenant_admin"), e2eLookupUserID(t, db, "demo", "teacher"), e2eLookupUserID(t, db, "demo", "grader"), e2eLookupUserID(t, db, "demo", "school_admin")
	var tenantID string
	if err := db.QueryRow(`SELECT tenant_id::text FROM app_user WHERE id=$1`, ownerID).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	schoolID := e2eString(t, e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, `{"name":"UX ACL school","code":"ux-acl"}`, 201)["school"].(map[string]any), "id")
	otherSchoolID := e2eString(t, e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, `{"name":"Other school","code":"ux-acl-other"}`, 201)["school"].(map[string]any), "id")
	if _, err := db.Exec(`UPDATE app_user SET school_id=$1 WHERE tenant_id=$2 AND id=ANY($3::uuid[])`, schoolID, tenantID, []string{ownerID, reviewerID, groupMember, viewerID}); err != nil {
		t.Fatal(err)
	}
	store, ctx := questionbank.NewPostgresStore(db), context.Background()
	owner := auth.AccessScope{TenantID: tenantID, ActorID: ownerID, TenantWide: true}
	bank, err := store.CreateBank(commandreceipt.WithID(ctx, "ux-bank"), owner, questionbank.CreateBankInput{SchoolID: schoolID, Name: "Historical authorization bank"})
	if err != nil {
		t.Fatal(err)
	}
	env := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: owner, router: router, token: token}
	actors := questionBankUXActors{reviewerID: reviewerID, groupMember: groupMember, viewerID: viewerID}
	groupID, before := seedQuestionBankHistoricalACL(t, env, bank, actors)
	update := assertQuestionBankHistoricalACLUpdate(t, env, bank, actors, groupID, before)
	roleID := installQuestionBankUXReviewerRole(t, env, actors)
	assertQuestionBankDirectoryIntersectsRBAC(t, env, bank, actors, roleID, schoolID)
	assertQuestionBankUXScopeBoundaries(t, env, bank, update, schoolID, otherSchoolID)
	assertQuestionBankInactiveMembershipAndOperatorRights(t, env, bank, actors, update, schoolID)

}

type questionBankUXActors struct{ reviewerID, groupMember, viewerID string }
type questionBankUXUpdate struct {
	groupID string
	input   questionbank.UpdateACLInput
	updated questionbank.ACLDocument
}

func seedQuestionBankHistoricalACL(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors) (string, questionbank.ACLDocument) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	owner := env.scope
	tenantID := env.scope.TenantID
	ownerID := env.scope.ActorID
	reviewerID := actors.reviewerID
	groupMember := actors.groupMember
	var err error
	groupID := uuid.NewString()
	for _, action := range []string{"read", "review", "statistics"} {
		if _, err = db.Exec(`INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,$4)`, tenantID, bank.ID, reviewerID, action); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO question_bank_group(id,tenant_id,bank_id,name,created_by) VALUES($1,$2,$3,'历史复核组',$4)`, groupID, tenantID, bank.ID, ownerID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "review", "publish"} {
		if _, err = db.Exec(`INSERT INTO question_bank_group_acl(tenant_id,bank_id,group_id,action) VALUES($1,$2,$3,$4)`, tenantID, bank.ID, groupID, action); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO question_bank_group_member(tenant_id,group_id,user_id) VALUES($1,$2,$3)`, tenantID, groupID, groupMember); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetACL(ctx, owner, bank.ID)
	if err != nil {
		t.Fatal(err)
	}

	return groupID, before

}

func assertQuestionBankHistoricalACLUpdate(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, groupID string, before questionbank.ACLDocument) questionBankUXUpdate {
	t.Helper()
	ctx := env.ctx
	store := env.store
	owner := env.scope
	reviewerID := actors.reviewerID
	groupMember := actors.groupMember
	viewerID := actors.viewerID
	input := questionbank.UpdateACLInput{ExpectedRevision: before.Revision, Bindings: []questionbank.ACLBinding{{UserID: viewerID, Preset: "Viewer"}}, Groups: []questionbank.ACLGroup{{Name: "普通出题组", Preset: "Author", MemberIDs: []string{viewerID}}}}
	commandCtx := commandreceipt.WithID(ctx, "ux-update-presets")
	updated, err := store.UpdateACL(commandCtx, owner, bank.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != before.Revision+1 || len(updated.Groups) != 2 {
		t.Fatalf("preset update blocked: %+v", updated)
	}
	assertQuestionBankHistoricalACLRetainedFacts(t, before, updated, groupID)
	replay, err := store.UpdateACL(commandCtx, owner, bank.ID, input)
	if err != nil || !reflect.DeepEqual(replay, updated) {
		t.Fatalf("receipt replay changed facts: %+v %v", replay, err)
	}
	for i, invalid := range []questionbank.UpdateACLInput{
		{ExpectedRevision: updated.Revision, Bindings: []questionbank.ACLBinding{{UserID: reviewerID, Preset: "Viewer"}}},
		{ExpectedRevision: updated.Revision, Groups: []questionbank.ACLGroup{{ID: groupID, Name: "覆盖旧组", Preset: "Author"}}},
		{ExpectedRevision: updated.Revision, Groups: []questionbank.ACLGroup{{Name: " 历史复核组 ", Preset: "Author"}}},
		{ExpectedRevision: updated.Revision, Bindings: []questionbank.ACLBinding{{UserID: groupMember, Preset: "Custom"}}},
	} {
		if _, err = store.UpdateACL(commandreceipt.WithID(ctx, uuid.NewString()), owner, bank.ID, invalid); !errors.Is(err, questionbank.ErrInvalidInput) {
			t.Fatalf("custom overwrite %d allowed: %v", i, err)
		}
	}
	after, err := store.GetACL(ctx, owner, bank.ID)
	if err != nil || !reflect.DeepEqual(after, updated) {
		t.Fatalf("rejected update modified ACL: %+v %v", after, err)
	}
	if _, err = store.UpdateACL(commandreceipt.WithID(ctx, "ux-stale"), owner, bank.ID, input); !errors.Is(err, questionbank.ErrConflict) {
		t.Fatalf("stale revision allowed: %v", err)
	}

	return questionBankUXUpdate{groupID: groupID, input: input, updated: updated}

}

func assertQuestionBankHistoricalACLRetainedFacts(t *testing.T, before, updated questionbank.ACLDocument, groupID string) {
	t.Helper()
	for _, old := range before.Bindings {
		found := false
		for _, current := range updated.Bindings {
			if current.UserID == old.UserID {
				found = reflect.DeepEqual(current, old)
			}
		}
		if !found {
			t.Fatalf("old direct facts changed: %+v", old)
		}
	}
	foundGroup := false
	for _, group := range updated.Groups {
		if group.ID == groupID {
			foundGroup = reflect.DeepEqual(group, before.Groups[0])
		}
	}
	if !foundGroup {
		t.Fatalf("custom group facts changed: %+v", updated.Groups)
	}

}

func installQuestionBankUXReviewerRole(t *testing.T, env questionBankE2EEnvironment, actors questionBankUXActors) string {
	t.Helper()
	db := env.db
	tenantID := env.scope.TenantID
	reviewerID := actors.reviewerID
	groupMember := actors.groupMember
	var err error
	// 构造独立职责，证明目录必须同时满足账号 RBAC 和题库 ACL，不能只看旧 ACL。
	var roleID string
	if err = db.QueryRow(`INSERT INTO role(tenant_id,code,name,scope_type) VALUES($1,'ux-review','UX reviewer','tenant') RETURNING id::text`, tenantID).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO role_permission(tenant_id,role_id,permission_id) SELECT $1,$2,id FROM permission WHERE tenant_id=$1 AND code IN ('question_bank:read','question_bank:review')`, tenantID, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE user_role SET deleted_at=now() WHERE tenant_id=$1 AND user_id=ANY($2::uuid[])`, tenantID, []string{reviewerID, groupMember}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenantID, reviewerID, roleID); err != nil {
		t.Fatal(err)
	}

	return roleID

}

func assertQuestionBankDirectoryIntersectsRBAC(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, roleID, schoolID string) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	tenantID := env.scope.TenantID
	reviewerID := actors.reviewerID
	groupMember := actors.groupMember
	viewerID := actors.viewerID
	reader := auth.AccessScope{TenantID: tenantID, ActorID: viewerID, SchoolIDs: []string{schoolID}}
	members, err := store.ListMembers(ctx, reader, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundReviewer := false
	for _, member := range members {
		if member.UserID == groupMember {
			t.Fatal("ACL-only contact bypassed revoked account responsibility")
		}
		if member.UserID == reviewerID {
			foundReviewer = true
			if !reflect.DeepEqual(member.Actions, []string{"read", "review"}) {
				t.Fatalf("directory actions did not intersect RBAC: %+v", member)
			}
		}
	}
	if !foundReviewer {
		t.Fatalf("authorized contact missing: %+v", members)
	}
	if _, err = db.Exec(`UPDATE role_permission SET deleted_at=now() WHERE tenant_id=$1 AND role_id=$2 AND permission_id IN (SELECT id FROM permission WHERE tenant_id=$1 AND code='question_bank:review')`, tenantID, roleID); err != nil {
		t.Fatal(err)
	}
	members, err = store.ListMembers(ctx, reader, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.UserID == reviewerID {
			t.Fatal("revoked RBAC reviewer remained visible")
		}
	}

}

func assertQuestionBankUXScopeBoundaries(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, update questionBankUXUpdate, schoolID, otherSchoolID string) {
	t.Helper()
	ctx := env.ctx
	store := env.store
	tenantID := env.scope.TenantID
	ownerID := env.scope.ActorID
	updated := update.updated
	var err error
	for _, bad := range []auth.AccessScope{{TenantID: uuid.NewString(), ActorID: ownerID, TenantWide: true}, {TenantID: tenantID, ActorID: ownerID, SchoolIDs: []string{otherSchoolID}}, {TenantID: tenantID, ActorID: uuid.NewString(), SchoolIDs: []string{schoolID}}} {
		if _, err = store.GetACL(ctx, bad, bank.ID); !errors.Is(err, questionbank.ErrNotFound) {
			t.Fatalf("ACL scope bypass: %v", err)
		}
		if _, err = store.UpdateACL(commandreceipt.WithID(ctx, uuid.NewString()), bad, bank.ID, questionbank.UpdateACLInput{ExpectedRevision: updated.Revision}); !errors.Is(err, questionbank.ErrNotFound) {
			t.Fatalf("ACL mutation scope bypass: %v", err)
		}
		if _, err = store.ListMembers(ctx, bad, bank.ID); !errors.Is(err, questionbank.ErrNotFound) {
			t.Fatalf("directory scope bypass: %v", err)
		}
	}

}

func assertQuestionBankInactiveMembershipAndOperatorRights(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, update questionBankUXUpdate, schoolID string) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	owner := env.scope
	tenantID := env.scope.TenantID
	groupMember := actors.groupMember
	viewerID := actors.viewerID
	groupID := update.groupID
	input := update.input
	updated := update.updated
	var err error
	// 历史成员停用不妨碍维护其他正常职责，也不能因此删除原组 membership。
	if _, err = db.Exec(`UPDATE app_user SET status='disabled' WHERE tenant_id=$1 AND id=$2`, tenantID, groupMember); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = updated.Revision
	updated, err = store.UpdateACL(commandreceipt.WithID(ctx, "ux-inactive-retained-member"), owner, bank.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	var retainedMemberCount int
	if err = db.QueryRow(`SELECT count(*) FROM question_bank_group_member WHERE tenant_id=$1 AND group_id=$2 AND user_id=$3`, tenantID, groupID, groupMember).Scan(&retainedMemberCount); err != nil || retainedMemberCount != 1 {
		t.Fatalf("inactive historic membership changed: %d %v", retainedMemberCount, err)
	}
	// 当前管理者与创建者都保留已有直接授权；移除其普通组不能把该组动作升级成永久直接授权。
	if _, err = db.Exec(`INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,'manage')`, tenantID, bank.ID, viewerID); err != nil {
		t.Fatal(err)
	}
	manager := auth.AccessScope{TenantID: tenantID, ActorID: viewerID, SchoolIDs: []string{schoolID}}
	_, err = store.UpdateACL(commandreceipt.WithID(ctx, "ux-protected-operator"), manager, bank.ID, questionbank.UpdateACLInput{ExpectedRevision: updated.Revision})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "statistics", "manage"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM question_bank_acl WHERE tenant_id=$1 AND bank_id=$2 AND user_id=$3 AND action=$4`, tenantID, bank.ID, viewerID, action).Scan(&count); err != nil || count != 1 {
			t.Fatalf("operator direct %s changed: %d %v", action, count, err)
		}
	}
	var actorEdit bool
	if err = db.QueryRow(`SELECT question_bank_actor_has_action($1,$2,$3,'edit')`, tenantID, bank.ID, viewerID).Scan(&actorEdit); err != nil || actorEdit {
		t.Fatalf("removed operator group retained edit: %t %v", actorEdit, err)
	}
}
