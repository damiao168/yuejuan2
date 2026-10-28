package questionbank

import (
	"errors"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

// 使用随机schema与合成账号，真实执行迁移、冻结触发器和事务锁；没有隔离DSN时明确跳过。
func TestPostgresHandoffPersistsClaimsAndFrozenContent(t *testing.T) {
	f := newReferenceImportFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	reviewer, publisher := uuid.NewString(), uuid.NewString()
	for _, person := range []struct{ id, action string }{{reviewer, "review"}, {publisher, "publish"}} {
		exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1,$2,$3,$1::uuid::text,'synthetic handoff','not-a-real-password','active')`, person.id, f.scope.TenantID, f.scope.SchoolIDs[0])
		role := uuid.NewString()
		exec(`INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$1::uuid::text,'synthetic','school')`, role, f.scope.TenantID)
		exec(`INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, f.scope.TenantID, person.id, role)
		for _, action := range []string{"read", person.action} {
			permission := uuid.NewString()
			exec(`INSERT INTO permission(id,tenant_id,code,name,resource,action) VALUES($1,$2,$3,'synthetic','question_bank',$4) ON CONFLICT(tenant_id,code) DO NOTHING`, permission, f.scope.TenantID, "question_bank:"+action, action)
			exec(`INSERT INTO role_permission(tenant_id,role_id,permission_id) SELECT $1,$2,id FROM permission WHERE tenant_id=$1 AND code=$3`, f.scope.TenantID, role, "question_bank:"+action)
			exec(`INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,$4)`, f.scope.TenantID, f.bank, person.id, action)
		}
	}
	item, err := f.store.CreateItem(t.Context(), f.scope, f.bank, CreateItemInput{ItemCode: "HANDOFF", Content: publishableFixture().Content})
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.store.UpdateScoring(t.Context(), f.scope, item.Version.ID, UpdateScoringInput{ExpectedRevision: item.Version.Revision, Scoring: publishableFixture().Scoring})
	if err != nil {
		t.Fatal(err)
	}
	in := handoffReviewInput(v)
	in.ReviewerID, in.PublisherID = reviewer, publisher
	current, err := f.store.Transition(t.Context(), f.scope, v.ID, "submit-review", in)
	if err != nil {
		t.Fatal(err)
	}
	reviewScope := f.scope
	reviewScope.ActorID = reviewer
	claimInput := HandoffInput{ExpectedRevision: current.Revision, ExpectedHandoffRevision: handoffRevision(current)}
	// 两个请求都携带同一交接修订，等待父行锁者必须重读子表，不能再成功领取一次。
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := f.store.ClaimHandoff(commandreceipt.WithID(t.Context(), uuid.NewString()), reviewScope, v.ID, "review", claimInput)
			errorsOut <- err
		})
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	wins := 0
	for err := range errorsOut {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("claim winners=%d", wins)
	}
	claimed, err := f.store.GetVersion(t.Context(), reviewScope, v.ID)
	if err != nil || claimed.Handoff == nil || claimed.Handoff.ClaimedBy != reviewer || claimed.Revision != v.Revision || claimed.BundleHash != v.BundleHash {
		t.Fatalf("claimed=%+v error=%v", claimed, err)
	}
	approved, err := f.store.Transition(t.Context(), reviewScope, v.ID, "approve", handoffReviewInput(claimed))
	if err != nil || handoffRecipient(approved) != publisher {
		t.Fatalf("approved=%+v error=%v", approved, err)
	}
	events, err := f.store.ListHandoffEvents(t.Context(), f.scope, v.ID)
	if err != nil || len(events) != 3 || events[0].Kind != "approved" {
		t.Fatalf("events=%+v error=%v", events, err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE question_bank_handoff_event SET kind='published' WHERE tenant_id=$1 AND version_id=$2`, f.scope.TenantID, v.ID); err == nil {
		t.Fatal("immutable handoff event was changed")
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE question_bank_item_version SET content=jsonb_set(content,'{stem}','"tampered"') WHERE tenant_id=$1 AND id=$2`, f.scope.TenantID, v.ID); err == nil {
		t.Fatal("handoff bypassed frozen question contents")
	}
}
