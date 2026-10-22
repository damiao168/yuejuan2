package subjective

import (
	"context"
	"sync"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type referencePreflightContextKey struct{}

type referencePreflightIdentity struct {
	Tenant, Exam, Question               string
	Snapshot, SnapshotHash               string
	Subject, Grade, QuestionType, Rubric string
	Binding                              paper.ReferenceMaterialBinding
	Policy                               ModelPolicy
}

type referencePreflightCache struct {
	mu     sync.Mutex
	passed map[referencePreflightIdentity]bool
}

// 缓存仅附着本次入队请求；同题扩批复用同一冻结材料检查，不跨请求延长审批有效期。
func withReferencePreflightCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(referencePreflightContextKey{}).(*referencePreflightCache); ok {
		return ctx
	}
	return context.WithValue(ctx, referencePreflightContextKey{}, &referencePreflightCache{passed: map[referencePreflightIdentity]bool{}})
}

func (c *referencePreflightCache) contains(identity referencePreflightIdentity) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.passed[identity]
}

func (c *referencePreflightCache) add(identity referencePreflightIdentity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passed[identity] = true
}

func (h *Handler) referencePreflightIdentity(tenant string, value Context) referencePreflightIdentity {
	policy := ModelPolicy{}
	if governed, ok := h.mathAdapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	return referencePreflightIdentity{
		Tenant: tenant, Exam: value.AssessmentSnapshot.ExamID, Question: value.Question.ID,
		Snapshot: value.AssessmentSnapshot.ID, SnapshotHash: value.AssessmentSnapshot.ContentHash,
		Subject: value.Subject, Grade: value.GradeLevel, QuestionType: value.Question.QuestionType,
		Rubric: value.Rubric.Version, Binding: *value.ReferenceMaterials, Policy: policy,
	}
}
