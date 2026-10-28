package calibration

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

var ErrTemplateUnavailable = errors.New("school calibration template unavailable")
var ErrTemplateContextMissing = errors.New("school calibration template target context missing")
var ErrTemplateForbidden = errors.New("school calibration template approval forbidden")

// SchoolTemplate 只冻结通过标准，不携带标准卷内容、答案或其他教师的试评资格。
// 同一来源修订和名称只批准一次；撤回后保留原事实，重新批准必须使用新名称或新来源修订。
type SchoolTemplate struct {
	ID                   string     `json:"id"`
	SchoolID             string     `json:"school_id"`
	Subject              string     `json:"subject"`
	Name                 string     `json:"name"`
	SourceExamID         string     `json:"source_exam_id"`
	SourceQuestionID     string     `json:"source_question_id"`
	SourcePolicyRevision int64      `json:"source_policy_revision"`
	Policy               Policy     `json:"policy"`
	Status               string     `json:"status"`
	Revision             int64      `json:"revision"`
	ApprovedBy           string     `json:"approved_by"`
	ApprovedAt           time.Time  `json:"approved_at"`
	WithdrawnBy          string     `json:"withdrawn_by,omitempty"`
	WithdrawnAt          *time.Time `json:"withdrawn_at,omitempty"`
	WithdrawReason       string     `json:"withdraw_reason,omitempty"`
}

type SchoolTemplateTarget struct {
	SchoolID      string  `json:"school_id"`
	Subject       string  `json:"subject"`
	ArchetypeCode string  `json:"archetype_code"`
	MaxScore      float64 `json:"max_score"`
}

type ApproveSchoolTemplateInput struct {
	Name                 string `json:"name"`
	SourcePolicyRevision int64  `json:"source_policy_revision"`
}
type WithdrawSchoolTemplateInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type SchoolTemplateRepository interface {
	SchoolTemplateTarget(context.Context, string, string, string) (SchoolTemplateTarget, error)
	ListSchoolTemplates(context.Context, string, SchoolTemplateTarget) ([]SchoolTemplate, error)
	ApproveSchoolTemplate(context.Context, string, string, string, string, ApproveSchoolTemplateInput) (SchoolTemplate, error)
	WithdrawSchoolTemplate(context.Context, string, string, SchoolTemplateTarget, string, WithdrawSchoolTemplateInput) (SchoolTemplate, error)
	ApplySchoolTemplate(context.Context, string, string, string, PutPolicyInput) (Policy, error)
}

type templateTargetContextKey struct{}
type templateTargetContext struct {
	exam, question string
	target         SchoolTemplateTarget
}

func withExpectedTemplateTarget(ctx context.Context, exam, question string, target SchoolTemplateTarget) context.Context {
	return context.WithValue(ctx, templateTargetContextKey{}, templateTargetContext{exam: exam, question: question, target: target})
}
func checkExpectedTemplateTarget(ctx context.Context, exam, question string, target SchoolTemplateTarget, approval bool) error {
	expected, hasExpected := ctx.Value(templateTargetContextKey{}).(templateTargetContext)
	if hasExpected && (expected.exam != exam || expected.question != question || expected.target != target) {
		return ErrConflict
	}
	user, authenticated := auth.UserFromContext(ctx)
	if !authenticated {
		return nil
	}
	// HTTP写入必须带上已授权的服务端目标。锁内再次核对，防止学校或题目事实在普通读取后改变。
	if !hasExpected {
		return ErrConflict
	}
	scope, ok := auth.AccessScopeFromContext(ctx)
	if !ok || scope.ActorID != user.ID || scope.TenantID != user.TenantID || !auth.HasPermission(user, reviewManagePermission) {
		return ErrTemplateForbidden
	}
	if approval {
		if !schoolTemplateCapability(user, scope, target.SchoolID) {
			return ErrTemplateForbidden
		}
	} else if !scope.AllowsExam(exam) && !schoolTemplateCapability(user, scope, target.SchoolID) {
		return ErrTemplateForbidden
	}
	return nil
}

func validTarget(target SchoolTemplateTarget) bool {
	return target.SchoolID != "" && target.Subject != "" && target.ArchetypeCode != "" && target.MaxScore > 0
}
func matchesTarget(item SchoolTemplate, target SchoolTemplateTarget) bool {
	return item.SchoolID == target.SchoolID && item.Subject == target.Subject && item.Policy.ArchetypeCode == target.ArchetypeCode && item.Policy.MaxScore == target.MaxScore
}
func templatePolicyInput(item SchoolTemplate, expected int64) PutPolicyInput {
	p := item.Policy
	return PutPolicyInput{ArchetypeCode: p.ArchetypeCode, MaxScore: p.MaxScore, MinimumSamples: p.MinimumSamples, MaximumMAE: p.MaximumMAE,
		MinimumExactAgreement: p.MinimumExactAgreement, MinimumWithinOneAgreement: p.MinimumWithinOneAgreement, MinimumCriterionAgreement: cloneFloat(p.MinimumCriterionAgreement),
		MaximumSevereRate: p.MaximumSevereRate, SevereErrorThreshold: p.SevereErrorThreshold, QualificationValidityDays: p.QualificationValidityDays, ExpectedRevision: expected,
		SchoolTemplateID: item.ID, SchoolTemplateRevision: item.Revision}
}
func validApproval(input ApproveSchoolTemplateInput) bool {
	return strings.TrimSpace(input.Name) != "" && !strings.ContainsRune(input.Name, '\x00') && utf8.RuneCountInString(strings.TrimSpace(input.Name)) <= 80 && input.SourcePolicyRevision > 0
}
func validWithdrawal(input WithdrawSchoolTemplateInput) bool {
	return input.ExpectedRevision > 0 && strings.TrimSpace(input.Reason) != "" && !strings.ContainsRune(input.Reason, '\x00') && utf8.RuneCountInString(input.Reason) <= 500
}

// SetSchoolTemplateTarget 仅用于内存演示和合成测试；生产目标事实由数据库关联当前考试和题目取得。
func (s *MemoryStore) SetSchoolTemplateTarget(tenant, exam, question string, target SchoolTemplateTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.templateTargets == nil {
		s.templateTargets = map[string]SchoolTemplateTarget{}
	}
	s.templateTargets[policyKey(tenant, exam, question)] = target
}
func (s *MemoryStore) SchoolTemplateTarget(_ context.Context, tenant, exam, question string) (SchoolTemplateTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.templateTargets[policyKey(tenant, exam, question)]
	if !ok {
		return target, ErrNotFound
	}
	if !validTarget(target) {
		return target, ErrTemplateContextMissing
	}
	return target, nil
}
func cloneTemplate(item SchoolTemplate) SchoolTemplate {
	item.Policy = clonePolicy(item.Policy)
	if item.WithdrawnAt != nil {
		v := *item.WithdrawnAt
		item.WithdrawnAt = &v
	}
	return item
}
func (s *MemoryStore) ListSchoolTemplates(_ context.Context, tenant string, target SchoolTemplateTarget) ([]SchoolTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SchoolTemplate{}
	for key, item := range s.templates {
		if strings.HasPrefix(key, tenant+"|") && item.Status == "approved" && matchesTarget(item, target) {
			out = append(out, cloneTemplate(item))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ApprovedAt.Equal(out[j].ApprovedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].ApprovedAt.After(out[j].ApprovedAt)
	})
	return out, nil
}
func (s *MemoryStore) ApproveSchoolTemplate(ctx context.Context, tenant, actor, exam, question string, input ApproveSchoolTemplateInput) (SchoolTemplate, error) {
	if !validApproval(input) {
		return SchoolTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, ok := s.templateTargets[policyKey(tenant, exam, question)]
	if !ok {
		return SchoolTemplate{}, ErrNotFound
	}
	if !validTarget(target) {
		return SchoolTemplate{}, ErrTemplateContextMissing
	}
	if err := checkExpectedTemplateTarget(ctx, exam, question, target, true); err != nil {
		return SchoolTemplate{}, err
	}
	p, ok := s.policies[policyKey(tenant, exam, question)]
	if !ok {
		return SchoolTemplate{}, ErrPolicyMissing
	}
	if p.Revision != input.SourcePolicyRevision {
		return SchoolTemplate{}, ErrConflict
	}
	if p.ArchetypeCode != target.ArchetypeCode || p.MaxScore != target.MaxScore {
		return SchoolTemplate{}, ErrTemplateUnavailable
	}
	if s.templates == nil {
		s.templates = map[string]SchoolTemplate{}
	}
	name := strings.TrimSpace(input.Name)
	for key, item := range s.templates {
		if strings.HasPrefix(key, tenant+"|") && item.Policy.ID == p.ID && item.SourcePolicyRevision == p.Revision && item.Name == name {
			return cloneTemplate(item), nil
		}
	}
	item := SchoolTemplate{ID: uuid.NewString(), SchoolID: target.SchoolID, Subject: target.Subject, Name: name, SourceExamID: exam, SourceQuestionID: question, SourcePolicyRevision: p.Revision, Policy: clonePolicy(p), Status: "approved", Revision: 1, ApprovedBy: actor, ApprovedAt: time.Now().UTC()}
	s.templates[tenant+"|"+item.ID] = item
	return cloneTemplate(item), nil
}
func (s *MemoryStore) WithdrawSchoolTemplate(ctx context.Context, tenant, actor string, target SchoolTemplateTarget, id string, input WithdrawSchoolTemplateInput) (SchoolTemplate, error) {
	if !validWithdrawal(input) {
		return SchoolTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected, ok := ctx.Value(templateTargetContextKey{}).(templateTargetContext); ok {
		actual, exists := s.templateTargets[policyKey(tenant, expected.exam, expected.question)]
		if !exists {
			return SchoolTemplate{}, ErrNotFound
		}
		if err := checkExpectedTemplateTarget(ctx, expected.exam, expected.question, actual, true); err != nil {
			return SchoolTemplate{}, err
		}
	} else if _, authenticated := auth.UserFromContext(ctx); authenticated {
		return SchoolTemplate{}, ErrConflict
	}
	key := tenant + "|" + id
	item, ok := s.templates[key]
	if !ok || !matchesTarget(item, target) {
		return SchoolTemplate{}, ErrNotFound
	}
	if item.Status != "approved" || item.Revision != input.ExpectedRevision {
		return SchoolTemplate{}, ErrConflict
	}
	now := time.Now().UTC()
	item.Status = "withdrawn"
	item.Revision++
	item.WithdrawnBy = actor
	item.WithdrawnAt = &now
	item.WithdrawReason = strings.TrimSpace(input.Reason)
	s.templates[key] = item
	return cloneTemplate(item), nil
}
func (s *MemoryStore) ApplySchoolTemplate(ctx context.Context, tenant, exam, question string, input PutPolicyInput) (Policy, error) {
	// 与撤回共用同一把锁，批准状态核对和目标策略CAS不能分开完成。
	s.mu.Lock()
	defer s.mu.Unlock()
	target, ok := s.templateTargets[policyKey(tenant, exam, question)]
	if !ok {
		return Policy{}, ErrNotFound
	}
	if err := checkExpectedTemplateTarget(ctx, exam, question, target, false); err != nil {
		return Policy{}, err
	}
	item, ok := s.templates[tenant+"|"+input.SchoolTemplateID]
	if !ok || item.Status != "approved" || item.Revision != input.SchoolTemplateRevision || !matchesTarget(item, target) {
		return Policy{}, ErrTemplateUnavailable
	}
	return s.putPolicyLocked(tenant, exam, question, templatePolicyInput(item, input.ExpectedRevision))
}
