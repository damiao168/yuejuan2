package questionbank

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"strconv"
	"time"
)

func (s *MemoryStore) UpdateScoring(ctx context.Context, scope auth.AccessScope, id string, in UpdateScoringInput) (Version, error) {
	scoring, err := normalizeScoring(in.Scoring)
	if err != nil || in.ExpectedRevision <= 0 {
		return Version{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, "question_bank.scoring.update", id, in, "edit", func() (Version, error) {
		v, err := s.version(scope, id, "edit")
		if err != nil {
			return v, err
		}
		i, _ := s.item(scope, v.ItemID, "edit")
		b, _ := s.bank(scope, i.BankID, "edit")
		if b.Status != "active" || i.Status != "active" || v.WorkflowStatus != "draft" {
			return v, ErrLocked
		}
		if v.Revision != in.ExpectedRevision {
			return v, ErrConflict
		}
		// File binding and materialization require the PostgreSQL transaction graph.
		if len(scoring.Assets) > 0 {
			return v, ErrUnavailable
		}
		if scoring.TemplateVersionID != nil {
			source, err := s.version(scope, *scoring.TemplateVersionID, "read")
			if err != nil {
				return v, err
			}
			si, _ := s.item(scope, source.ItemID, "read")
			sb, _ := s.bank(scope, si.BankID, "read")
			if i.Kind == "rubric_template" || si.Kind != "rubric_template" || source.WorkflowStatus != "published" || source.Scoring.Rubric == nil || si.Status != "active" || sb.Status != "active" {
				return v, ErrInvalidInput
			}
			scoring.Rubric = copyScoring(source.Scoring).Rubric
		}
		v.Scoring = scoring
		v.Revision++
		v.BundleHash = bundleHash(v.Content, scoring)
		v.UpdatedAt = time.Now().UTC()
		if scoring.Answer != nil {
			a := uuid.NewString()
			v.AnswerVersionID = &a
		}
		if scoring.Rubric != nil {
			r := uuid.NewString()
			v.RubricVersionID = &r
		}
		s.versions[id] = copyVersion(v)
		return copyVersion(v), nil
	})
}
func (s *MemoryStore) Transition(ctx context.Context, scope auth.AccessScope, id, decision string, in ReviewInput) (Version, error) {
	s.mu.Lock()
	v, err := s.version(scope, id, "read")
	s.mu.Unlock()
	if err != nil {
		return v, err
	}
	action := transitionAction(decision, v.AuthorID == scope.ActorID)
	return memoryMutation(ctx, s, scope, "question_bank."+decision, id, in, action, func() (Version, error) {
		v, err := s.version(scope, id, action)
		if err != nil {
			return v, err
		}
		i, _ := s.item(scope, v.ItemID, action)
		b, _ := s.bank(scope, i.BankID, action)
		if b.Status != "active" || i.Status != "active" {
			return v, ErrLocked
		}
		if decision == "submit-review" || decision == "publish" {
			if err := validationErr(validateContentMetadata(s.schemas[i.BankID+":"+strconv.Itoa(v.SchemaVersion)], v.Content, true)); err != nil {
				return v, err
			}
		}
		status, err := nextStatus(v, i.Kind, scope.ActorID, decision, in)
		if err != nil {
			return v, err
		}
		r := Review{ID: uuid.NewString(), VersionID: id, ReviewerID: scope.ActorID, Decision: decision, Comment: in.Comment, ContentRevision: v.Revision, BundleHash: v.BundleHash, CreatedAt: time.Now().UTC()}
		s.reviews[id] = append(s.reviews[id], r)
		v.WorkflowStatus = status
		if status == "draft" {
			v.Revision++
		}
		if status == "published" {
			published := id
			i.CurrentPublishedVersionID = &published
			s.items[i.ID] = i
		}
		v.UpdatedAt = r.CreatedAt
		// 审核记录保留当时的修订号与哈希；退回草稿会增加修订号，旧审核结果不能继续沿用。
		s.versions[id] = copyVersion(v)
		return copyVersion(v), nil
	})
}
func (s *MemoryStore) ListReviews(ctx context.Context, scope auth.AccessScope, id string) ([]Review, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.version(scope, id, "read"); err != nil {
		return nil, err
	}
	return append([]Review{}, s.reviews[id]...), nil
}
func (s *MemoryStore) BindReviewers(ctx context.Context, scope auth.AccessScope, id string, in ReviewerBinding) (Bank, error) {
	if !validID(in.UserID) || in.ExpectedRevision <= 0 || ((in.Review || in.Publish) && !in.Read) {
		return Bank{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, "question_bank.reviewers.bind", id, in, "manage", func() (Bank, error) {
		b, err := s.bank(scope, id, "manage")
		if err != nil {
			return b, err
		}
		if b.Revision != in.ExpectedRevision {
			return b, ErrConflict
		}
		if b.Status != "active" {
			return b, ErrLocked
		}
		for _, x := range []struct {
			a string
			e bool
		}{{"read", in.Read}, {"review", in.Review}, {"publish", in.Publish}} {
			s.acl[aclKey(scope.TenantID, id, in.UserID, x.a)] = x.e
		}
		b.Revision++
		b.UpdatedAt = time.Now().UTC()
		s.banks[id] = b
		return b, nil
	})
}
func (s *MemoryStore) Materialize(context.Context, auth.AccessScope, string, MaterializeInput) (MaterializeResult, error) {
	return MaterializeResult{}, ErrUnavailable
}
