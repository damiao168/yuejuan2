package questionbank

import (
	"context"
	"errors"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func handoffFixture(t *testing.T) (*MemoryStore, auth.AccessScope, Bank, Version, string, string) {
	t.Helper()
	s, scope, bank, item := batch28MemoryFixture(t)
	v := item.Version
	v.Content = publishableFixture().Content
	var err error
	v.Scoring, err = normalizeScoring(publishableFixture().Scoring)
	if err != nil {
		t.Fatal(err)
	}
	v.ContentHash = contentHash(v.Content)
	v.BundleHash = bundleHash(v.Content, v.Scoring)
	s.versions[v.ID] = copyVersion(v)
	reviewer, publisher := uuid.NewString(), uuid.NewString()
	for _, p := range []struct{ id, action string }{{reviewer, "read"}, {reviewer, "review"}, {publisher, "read"}, {publisher, "publish"}} {
		s.acl[aclKey(scope.TenantID, bank.ID, p.id, p.action)] = true
	}
	return s, scope, bank, v, reviewer, publisher
}

func handoffReviewInput(v Version) ReviewInput {
	return ReviewInput{ExpectedRevision: v.Revision, ExpectedHandoffRevision: handoffRevision(v), BundleHash: v.BundleHash}
}

func TestHandoffAssignmentRoutesReviewAndPublishWithoutChangingContent(t *testing.T) {
	s, scope, _, v, reviewer, publisher := handoffFixture(t)
	in := handoffReviewInput(v)
	in.ReviewerID, in.PublisherID = reviewer, publisher
	current, err := s.Transition(context.Background(), scope, v.ID, "submit-review", in)
	if err != nil || current.Handoff.ReviewerID != reviewer || current.Revision != v.Revision || current.BundleHash != v.BundleHash {
		t.Fatalf("submit=%+v error=%v", current, err)
	}
	wrong := scope
	wrong.ActorID = publisher
	if _, err := s.ClaimHandoff(context.Background(), wrong, v.ID, "review", HandoffInput{ExpectedRevision: current.Revision, ExpectedHandoffRevision: handoffRevision(current)}); err == nil {
		t.Fatal("publisher acquired a review without the current duty")
	}
	reviewScope := scope
	reviewScope.ActorID = reviewer
	claimed, err := s.ClaimHandoff(context.Background(), reviewScope, v.ID, "review", HandoffInput{ExpectedRevision: current.Revision, ExpectedHandoffRevision: handoffRevision(current)})
	if err != nil || claimed.Handoff.ClaimedBy != reviewer || claimed.Revision != v.Revision {
		t.Fatalf("claim=%+v error=%v", claimed, err)
	}
	if _, err := s.Transition(context.Background(), reviewScope, v.ID, "approve", handoffReviewInput(current)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale claim should conflict: %v", err)
	}
	approved, err := s.Transition(context.Background(), reviewScope, v.ID, "approve", handoffReviewInput(claimed))
	if err != nil || handoffRecipient(approved) != publisher || approved.Handoff.ClaimedBy != "" {
		t.Fatalf("approve=%+v error=%v", approved, err)
	}
	wrong.ActorID = publisher
	published, err := s.Transition(context.Background(), wrong, v.ID, "publish", handoffReviewInput(approved))
	if err != nil || published.WorkflowStatus != "published" || published.BundleHash != v.BundleHash {
		t.Fatalf("publish=%+v error=%v", published, err)
	}
	events, err := s.ListHandoffEvents(context.Background(), scope, v.ID)
	if err != nil || len(events) != 4 || events[0].Kind != "published" || events[2].Kind != "claimed" {
		t.Fatalf("events=%+v error=%v", events, err)
	}
}

func TestHandoffRejectsInvalidRecipientWithoutPartialTransition(t *testing.T) {
	for _, mode := range []string{"self-review", "missing-publisher", "revoked-publisher"} {
		t.Run(mode, func(t *testing.T) {
			s, scope, bank, v, reviewer, publisher := handoffFixture(t)
			in := handoffReviewInput(v)
			in.ReviewerID, in.PublisherID = reviewer, publisher
			if mode == "self-review" {
				in.ReviewerID = scope.ActorID
			}
			if mode == "missing-publisher" {
				in.PublisherID = ""
			}
			if mode == "revoked-publisher" {
				delete(s.acl, aclKey(scope.TenantID, bank.ID, publisher, "publish"))
			}
			if _, err := s.Transition(context.Background(), scope, v.ID, "submit-review", in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid recipient accepted: %v", err)
			}
			if s.versions[v.ID].WorkflowStatus != "draft" || len(s.reviews[v.ID]) != 0 || len(s.handoffEvents[v.ID]) != 0 {
				t.Fatal("failed submit left review, assignment or notification behind")
			}
		})
	}
}

func TestHandoffConcurrentPoolClaimHasSingleOwner(t *testing.T) {
	s, scope, bank, v, reviewer, publisher := handoffFixture(t)
	s.acl[aclKey(scope.TenantID, bank.ID, publisher, "review")] = true
	v.WorkflowStatus = "reviewing"
	s.versions[v.ID] = v
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, actor := range []string{reviewer, publisher} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			<-start
			candidate := scope
			candidate.ActorID = actor
			_, err := s.ClaimHandoff(context.Background(), candidate, v.ID, "review", HandoffInput{ExpectedRevision: v.Revision})
			results <- err
		}(actor)
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 || len(s.handoffEvents[v.ID]) != 1 {
		t.Fatalf("winners=%d events=%d", wins, len(s.handoffEvents[v.ID]))
	}
}

func TestHandoffReassignmentRejectsFormerRecipientAndReplaysOnce(t *testing.T) {
	s, scope, bank, v, reviewer, publisher := handoffFixture(t)
	v.WorkflowStatus = "reviewing"
	s.versions[v.ID] = v
	in := HandoffInput{ExpectedRevision: v.Revision, ReviewerID: reviewer, PublisherID: publisher}
	ctx := commandreceipt.WithID(context.Background(), "handoff-assignment")
	assigned, err := s.AssignHandoff(ctx, scope, v.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.AssignHandoff(ctx, scope, v.ID, in)
	if err != nil || handoffRevision(replayed) != handoffRevision(assigned) || len(s.handoffEvents[v.ID]) != 1 {
		t.Fatalf("replay=%+v error=%v", replayed, err)
	}
	newReviewer := uuid.NewString()
	s.acl[aclKey(scope.TenantID, bank.ID, newReviewer, "read")] = true
	s.acl[aclKey(scope.TenantID, bank.ID, newReviewer, "review")] = true
	in.ExpectedHandoffRevision, in.ReviewerID = handoffRevision(assigned), newReviewer
	changed, err := s.AssignHandoff(context.Background(), scope, v.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	previous := scope
	previous.ActorID = reviewer
	if _, err := s.Transition(context.Background(), previous, v.ID, "approve", handoffReviewInput(changed)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("old recipient approval=%v", err)
	}
	otherTenant := scope
	otherTenant.TenantID = uuid.NewString()
	if _, err := s.ListHandoffEvents(context.Background(), otherTenant, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant history=%v", err)
	}
}

func TestHandoffRevokedPublisherBlocksApprovalAndAllowsAuthorRecovery(t *testing.T) {
	s, scope, bank, v, reviewer, publisher := handoffFixture(t)
	in := handoffReviewInput(v)
	in.ReviewerID, in.PublisherID = reviewer, publisher
	current, err := s.Transition(context.Background(), scope, v.ID, "submit-review", in)
	if err != nil {
		t.Fatal(err)
	}
	delete(s.acl, aclKey(scope.TenantID, bank.ID, publisher, "publish"))
	candidate := scope
	candidate.ActorID = reviewer
	if _, err := s.Transition(context.Background(), candidate, v.ID, "approve", handoffReviewInput(current)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("revoked publisher accepted: %v", err)
	}
	if len(s.handoffEvents[v.ID]) != 1 || len(s.reviews[v.ID]) != 1 {
		t.Fatal("failed approval persisted an event")
	}
	returned, err := s.Transition(context.Background(), scope, v.ID, "return-to-draft", handoffReviewInput(current))
	if err != nil || returned.WorkflowStatus != "draft" || returned.Revision != v.Revision+1 {
		t.Fatalf("author recovery=%+v error=%v", returned, err)
	}
}

func TestHandoffManagerRequiresContentReadAndAuthorCanUseEditFallback(t *testing.T) {
	s, scope, bank, v, reviewer, publisher := handoffFixture(t)
	v.WorkflowStatus = "reviewing"
	s.versions[v.ID] = v
	in := HandoffInput{ExpectedRevision: v.Revision, ReviewerID: reviewer, PublisherID: publisher, Manage: true, AuthorEdit: true}
	delete(s.acl, aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage"))
	assigned, err := s.AssignHandoff(context.Background(), scope, v.ID, in)
	if err != nil {
		t.Fatalf("author edit fallback: %v", err)
	}
	s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage")] = true
	in.ExpectedHandoffRevision = handoffRevision(assigned)
	ctx := commandreceipt.WithID(context.Background(), "handoff-manager-read")
	if _, err = s.AssignHandoff(ctx, scope, v.ID, in); err != nil {
		t.Fatal(err)
	}
	delete(s.acl, aclKey(scope.TenantID, bank.ID, scope.ActorID, "read"))
	if got, err := s.AssignHandoff(ctx, scope, v.ID, in); !errors.Is(err, ErrNotFound) || got.ID != "" {
		t.Fatalf("management receipt leaked content: %+v error=%v", got, err)
	}
}

func TestHandoffPendingListExcludesOtherAssignee(t *testing.T) {
	s, scope, bank, v, reviewer, publisher := handoffFixture(t)
	s.acl[aclKey(scope.TenantID, bank.ID, publisher, "review")] = true
	v.WorkflowStatus = "reviewing"
	v.Handoff = &HandoffState{Revision: 1, ReviewerID: reviewer, PublisherID: publisher}
	s.versions[v.ID] = v
	for _, actor := range []string{reviewer, publisher} {
		candidate := scope
		candidate.ActorID = actor
		page, err := s.SearchItems(context.Background(), candidate, SearchFilter{PendingAction: "review"})
		want := 0
		if actor == reviewer {
			want = 1
		}
		if err != nil || page.Total != want {
			t.Fatalf("actor=%s total=%d error=%v", actor, page.Total, err)
		}
	}
}
