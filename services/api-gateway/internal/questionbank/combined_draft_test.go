package questionbank

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

func TestCombinedDraftSavesOneRevisionAndDoesNotPartiallyApply(t *testing.T) {
	s := NewMemoryStore()
	scope := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	ctx := context.Background()
	bank, err := s.CreateBank(commandreceipt.WithID(ctx, "bank"), scope, CreateBankInput{SchoolID: uuid.NewString(), Name: "题库"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(commandreceipt.WithID(ctx, "item"), scope, bank.ID, CreateItemInput{ItemCode: "Q1", Content: fixtureContent()})
	if err != nil {
		t.Fatal(err)
	}
	content := item.Version.Content
	content.Stem = "同时修改题干与答案"
	scoring := emptyScoring()
	scoring.Answer = &paper.AnswerKeyInput{StandardAnswer: "B", EquivalentAnswers: []any{}, Tolerance: map[string]any{}}
	input := UpdateVersionInput{ExpectedRevision: item.Version.Revision, Content: content, Scoring: &scoring}
	command := commandreceipt.WithID(ctx, "combined")
	saved, err := s.UpdateVersion(command, scope, item.Version.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != item.Version.Revision+1 || saved.Stem != content.Stem || saved.Scoring.Answer.StandardAnswer != "B" {
		t.Fatalf("not one complete revision: %+v", saved)
	}
	replay, err := s.UpdateVersion(command, scope, item.Version.ID, input)
	if err != nil || replay.Revision != saved.Revision {
		t.Fatalf("retry changed revision: %+v %v", replay, err)
	}
	stale := input
	stale.Content.Stem = "陈旧修改"
	if _, err = s.UpdateVersion(commandreceipt.WithID(ctx, "stale"), scope, item.Version.ID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale accepted: %v", err)
	}
	// 题干合法而评分非法；失败不能只保存题干，原完整快照和修订必须保留。
	bad := input
	bad.ExpectedRevision = saved.Revision
	bad.Content.Stem = "不应落盘"
	badScoring := copyScoring(scoring)
	badScoring.UsePolicy = "invalid"
	bad.Scoring = &badScoring
	if _, err = s.UpdateVersion(commandreceipt.WithID(ctx, "bad"), scope, item.Version.ID, bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid scoring accepted: %v", err)
	}
	after, err := s.GetVersion(ctx, scope, item.Version.ID)
	if err != nil || after.Revision != saved.Revision || after.Stem != saved.Stem || after.Scoring.Answer.StandardAnswer != "B" {
		t.Fatalf("partial save: %+v %v", after, err)
	}
}

func TestQuestionBankHandoffsRespectRoleAuthorAndRevocation(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	owner := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	reviewer := auth.AccessScope{TenantID: owner.TenantID, ActorID: uuid.NewString(), TenantWide: true}
	reader := auth.AccessScope{TenantID: owner.TenantID, ActorID: uuid.NewString(), TenantWide: true}
	bank, err := s.CreateBank(commandreceipt.WithID(ctx, "bank-handoff"), owner, CreateBankInput{SchoolID: uuid.NewString(), Name: "题库"})
	if err != nil {
		t.Fatal(err)
	}
	acl, err := s.UpdateACL(commandreceipt.WithID(ctx, "acl-handoff"), owner, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: []ACLBinding{{UserID: reviewer.ActorID, Preset: "Reviewer"}, {UserID: reader.ActorID, Preset: "Viewer"}}})
	if err != nil {
		t.Fatal(err)
	}
	// 本测试只验证待办筛选：直接构造三个既有工作流状态，状态转换校验另有专门回归。
	for index, state := range []string{"reviewing", "reviewing", "approved"} {
		result, createErr := s.CreateItem(commandreceipt.WithID(ctx, "handoff-item-"+state+string(rune('a'+index))), owner, bank.ID, CreateItemInput{ItemCode: "HANDOFF-" + string(rune('a'+index)), Content: fixtureContent()})
		if createErr != nil {
			t.Fatal(createErr)
		}
		v := s.versions[result.Version.ID]
		v.WorkflowStatus = state
		if index == 1 {
			v.AuthorID = reviewer.ActorID
		}
		s.versions[v.ID] = v
	}
	page, err := s.SearchItems(ctx, reviewer, SearchFilter{PendingAction: "review", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Version.AuthorID == reviewer.ActorID {
		t.Fatalf("review handoffs: %+v %v", page, err)
	}
	for _, filter := range []SearchFilter{{PendingAction: "publish"}, {PendingAction: "review"}} {
		page, err = s.SearchItems(ctx, reader, filter)
		if err != nil || page.Total != 0 {
			t.Fatalf("read permission inherited work: %+v %v", page, err)
		}
	}
	_, err = s.UpdateACL(commandreceipt.WithID(ctx, "revoke-handoff"), owner, bank.ID, UpdateACLInput{ExpectedRevision: acl.Revision, Bindings: []ACLBinding{{UserID: reviewer.ActorID, Preset: "Viewer"}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err = s.SearchItems(ctx, reviewer, SearchFilter{PendingAction: "review"})
	if err != nil || page.Total != 0 {
		t.Fatalf("revoked handoff retained: %+v %v", page, err)
	}
	if _, err = s.SearchItems(ctx, owner, SearchFilter{PendingAction: "unknown"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid handoff accepted: %v", err)
	}
}
