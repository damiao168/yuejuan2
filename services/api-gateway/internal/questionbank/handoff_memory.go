package questionbank

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"time"
)

func (s *MemoryStore) memoryRecipient(scope auth.AccessScope, bankID, recipient, action string) error {
	candidate := scope
	candidate.ActorID = recipient
	if _, err := s.bank(candidate, bankID, action); err != nil {
		return ErrInvalidInput
	}
	return nil
}
func (s *MemoryStore) rememberHandoff(v Version, actor, kind, recipient string) {
	s.versions[v.ID] = copyVersion(v)
	s.handoffEvents[v.ID] = append(s.handoffEvents[v.ID], HandoffEvent{ID: uuid.NewString(), VersionID: v.ID, HandoffRevision: v.Handoff.Revision, ContentRevision: v.Revision, Kind: kind, ActorID: actor, RecipientID: recipient, CreatedAt: time.Now().UTC()})
}
func (s *MemoryStore) AssignHandoff(ctx context.Context, scope auth.AccessScope, id string, in HandoffInput) (Version, error) {
	action := "edit"
	if in.Manage {
		action = "manage"
		if in.AuthorEdit {
			s.mu.Lock()
			_, err := s.version(scope, id, "manage")
			s.mu.Unlock()
			if err != nil {
				action = "edit"
			}
		}
	}
	return memoryMutation(ctx, s, scope, memoryMutationCommand{op: "question_bank.handoff.assign", target: id, input: in, action: action}, func() (Version, error) {
		v, err := s.version(scope, id, action)
		if err != nil {
			return v, err
		}
		if action != "manage" && v.AuthorID != scope.ActorID {
			return v, auth.ErrForbidden
		}
		i, _ := s.item(scope, v.ItemID, action)
		if _, err := s.bank(scope, i.BankID, "read"); err != nil {
			return Version{}, err
		}
		b, _ := s.bank(scope, i.BankID, action)
		if b.Status != "active" || i.Status != "active" {
			return v, ErrLocked
		}
		h, err := handoffAssignment(v, in)
		if err != nil {
			return v, err
		}
		if err = s.memoryRecipient(scope, i.BankID, h.ReviewerID, "review"); err != nil {
			return v, err
		}
		if err = s.memoryRecipient(scope, i.BankID, h.PublisherID, "publish"); err != nil {
			return v, err
		}
		v.Handoff = &h
		s.rememberHandoff(v, scope.ActorID, "reassigned", handoffRecipient(v))
		return copyVersion(v), nil
	})
}
func (s *MemoryStore) ClaimHandoff(ctx context.Context, scope auth.AccessScope, id, action string, in HandoffInput) (Version, error) {
	return memoryMutation(ctx, s, scope, memoryMutationCommand{op: "question_bank.handoff.claim." + action, target: id, input: in, action: action}, func() (Version, error) {
		v, err := s.version(scope, id, action)
		if err != nil {
			return v, err
		}
		i, _ := s.item(scope, v.ItemID, action)
		b, _ := s.bank(scope, i.BankID, action)
		if b.Status != "active" || i.Status != "active" {
			return v, ErrLocked
		}
		h, err := handoffClaim(v, scope.ActorID, action, in)
		if err != nil {
			return v, err
		}
		v.Handoff = &h
		s.rememberHandoff(v, scope.ActorID, "claimed", scope.ActorID)
		return copyVersion(v), nil
	})
}
func (s *MemoryStore) ListHandoffEvents(_ context.Context, scope auth.AccessScope, id string) ([]HandoffEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.version(scope, id, "read"); err != nil {
		return nil, err
	}
	events := s.handoffEvents[id]
	start := 0
	if len(events) > 100 {
		start = len(events) - 100
	}
	out := make([]HandoffEvent, 0, len(events)-start)
	for index := len(events) - 1; index >= start; index-- {
		out = append(out, events[index])
	}
	return out, nil
}

// 调用方已持有整题锁；先校验指派与职责，再与正式审核一并写入，失败不得只通知不转换。
func (s *MemoryStore) transitionHandoff(scope auth.AccessScope, i Item, v Version, decision string, in ReviewInput) (*HandoffState, string, error) {
	return prepareHandoffTransition(v, scope.ActorID, decision, in, func(recipient, action string) error { return s.memoryRecipient(scope, i.BankID, recipient, action) })
}
