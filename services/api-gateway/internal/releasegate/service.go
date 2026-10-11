package releasegate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type Service struct {
	store    Store
	base     BaseGateReader
	regrades RegradeBlockerReader
	now      func() time.Time
}

func NewService(store Store, base BaseGateReader) *Service {
	return &Service{store: store, base: base, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) WithRegradeBlockerReader(reader RegradeBlockerReader) *Service {
	s.regrades = reader
	return s
}

func (s *Service) CreatePolicy(ctx context.Context, tenantID, examID, actorID string, input CreatePolicyInput) (Policy, error) {
	if !validIDs(tenantID, examID, actorID) || !normalizePolicy(&input) {
		return Policy{}, ErrInvalidInput
	}
	return s.store.CreatePolicy(ctx, tenantID, examID, actorID, input)
}

// Preview 重新计算 A18 基础门禁并保存不可变证据。
// 即使门禁阻断，它仍向运维提供准确且不可变的原因，供修正或申请允许的警告豁免。
func (s *Service) Preview(ctx context.Context, tenantID, examID, releaseID, actorID string) (Evidence, error) {
	if !validIDs(tenantID, examID, actorID) {
		return Evidence{}, ErrInvalidInput
	}
	return s.evaluateAndAppend(ctx, tenantID, examID, strings.TrimSpace(releaseID), actorID, EvidencePreview)
}

// RecheckForPublish 是发布协调器唯一应调用的门禁 API。
// 它不复用 Preview 结果，始终追加新证据。
func (s *Service) RecheckForPublish(ctx context.Context, tenantID, examID, releaseID, actorID string) (Evidence, error) {
	if !validIDs(tenantID, examID, releaseID, actorID) {
		return Evidence{}, ErrInvalidInput
	}
	evidence, err := s.evaluateAndAppend(ctx, tenantID, examID, releaseID, actorID, EvidencePublish)
	if err != nil {
		return Evidence{}, err
	}
	if !evidence.Evaluation.Passed {
		return evidence, ErrGateBlocked
	}
	return evidence, nil
}

func (s *Service) RequestWaiver(ctx context.Context, tenantID, examID, actorID string, input RequestWaiverInput) (Waiver, error) {
	input.EvidenceID, input.IssueCode, input.Reason = strings.TrimSpace(input.EvidenceID), strings.TrimSpace(input.IssueCode), strings.TrimSpace(input.Reason)
	if !validIDs(tenantID, examID, actorID, input.EvidenceID, input.IssueCode) || utf8.RuneCountInString(input.Reason) == 0 || utf8.RuneCountInString(input.Reason) > 1000 {
		return Waiver{}, ErrInvalidInput
	}
	evidence, err := s.store.GetEvidence(ctx, tenantID, input.EvidenceID)
	if err != nil {
		return Waiver{}, err
	}
	if evidence.ExamID != examID || evidence.Phase != EvidencePreview || !warningCanBeWaived(evidence.Evaluation, input.IssueCode) {
		return Waiver{}, ErrForbiddenWaiver
	}
	return s.store.RequestWaiver(ctx, Waiver{TenantID: tenantID, ExamID: examID, PolicyID: evidence.Evaluation.Policy.ID,
		EvidenceID: evidence.ID, IssueCode: input.IssueCode, Reason: input.Reason, Status: WaiverRequested,
		RequestedBy: actorID, RequestedAt: s.now().UTC()})
}

func (s *Service) DecideWaiver(ctx context.Context, tenantID, waiverID, actorID string, input DecideWaiverInput) (Waiver, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if !validIDs(tenantID, waiverID, actorID) || utf8.RuneCountInString(input.Reason) == 0 || utf8.RuneCountInString(input.Reason) > 1000 {
		return Waiver{}, ErrInvalidInput
	}
	waiver, err := s.store.GetWaiver(ctx, tenantID, waiverID)
	if err != nil {
		return Waiver{}, err
	}
	if waiver.Status != WaiverRequested {
		return Waiver{}, ErrForbiddenWaiver
	}
	// 接受批准前立即按不可变证据重新校验，避免恶意请求或后续策略编辑将基础阻断项变成可豁免项。
	evidence, err := s.store.GetEvidence(ctx, tenantID, waiver.EvidenceID)
	if err != nil {
		return Waiver{}, err
	}
	if waiver.ExamID != evidence.ExamID || !warningCanBeWaived(evidence.Evaluation, waiver.IssueCode) {
		return Waiver{}, ErrForbiddenWaiver
	}
	return s.store.DecideWaiver(ctx, tenantID, waiverID, actorID, input)
}

func (s *Service) GetEvidence(ctx context.Context, tenantID, evidenceID string) (Evidence, error) {
	if !validIDs(tenantID, evidenceID) {
		return Evidence{}, ErrInvalidInput
	}
	return s.store.GetEvidence(ctx, tenantID, evidenceID)
}

// Inspect 读取完整发布条件，但不追加预检或发布证据。
// 结果只用于说明当前状态；正式发布仍必须调用 RecheckForPublish 重新求值。
func (s *Service) Inspect(ctx context.Context, tenantID, examID string) (Evaluation, error) {
	if !validIDs(tenantID, examID) {
		return Evaluation{}, ErrInvalidInput
	}
	return s.loadEvaluation(ctx, tenantID, examID)
}

func (s *Service) evaluateAndAppend(ctx context.Context, tenantID, examID, releaseID, actorID, phase string) (Evidence, error) {
	evaluation, err := s.loadEvaluation(ctx, tenantID, examID)
	if err != nil {
		return Evidence{}, err
	}
	evidence := Evidence{TenantID: tenantID, ExamID: examID, ReleaseID: releaseID, Phase: phase, Evaluation: evaluation, CreatedBy: actorID, CreatedAt: s.now().UTC()}
	evidence.Hash = evidenceHash(evidence)
	return s.store.AppendEvidence(ctx, evidence)
}

// 只读诊断与证据写入共用规则，避免助手遗漏重评阻塞、有效策略或警告确认。
func (s *Service) loadEvaluation(ctx context.Context, tenantID, examID string) (Evaluation, error) {
	base, err := s.base.Gate(ctx, tenantID, examID)
	if err != nil {
		return Evaluation{}, err
	}
	if s.regrades != nil {
		count, err := s.regrades.BlockingRegradeCount(ctx, tenantID, examID)
		if err != nil {
			return Evaluation{}, err
		}
		if count > 0 {
			base.Blocking = append(base.Blocking, scorerelease.GateIssue{Code: "regrade_release_pending", Message: "a regrade job has not been materialised into a successor release", Blocking: true, Count: count, ActionRoute: "regrade"})
			if base.Counts == nil {
				base.Counts = map[string]int{}
			}
			base.Counts["regrade_release_pending"] += count
			base.Passed = false
		}
	}
	policy, err := s.store.ActivePolicy(ctx, tenantID, examID)
	if err != nil && err != ErrNotFound {
		return Evaluation{}, err
	}
	if err == ErrNotFound {
		policy = defaultPolicy(tenantID, examID)
	}
	waivers, err := s.store.ApprovedWaivers(ctx, tenantID, examID, policy.ID)
	if err != nil {
		return Evaluation{}, err
	}
	return evaluate(policy, base, waivers, s.now().UTC()), nil
}

func evaluate(policy Policy, baseGate scorerelease.Gate, waivers []Waiver, now time.Time) Evaluation {
	result := Evaluation{Policy: clonePolicy(policy), BaseGate: cloneGate(baseGate), Passed: len(baseGate.Blocking) == 0,
		PendingWarningCodes: []string{}, AppliedWaiverIDs: []string{}, GeneratedAt: now.UTC()}
	approved := map[string]string{}
	for _, waiver := range waivers {
		if waiver.Status == WaiverApproved {
			approved[waiver.IssueCode] = waiver.ID
		}
	}
	if !policy.RequireWarningAcknowledgement {
		return result
	}
	seen := map[string]bool{}
	for _, issue := range baseGate.Warnings {
		if seen[issue.Code] || !policyAllowsWarning(policy, issue.Code) {
			continue
		}
		seen[issue.Code] = true
		if waiverID := approved[issue.Code]; waiverID != "" {
			result.AppliedWaiverIDs = append(result.AppliedWaiverIDs, waiverID)
			continue
		}
		result.PendingWarningCodes = append(result.PendingWarningCodes, issue.Code)
	}
	sort.Strings(result.PendingWarningCodes)
	sort.Strings(result.AppliedWaiverIDs)
	result.Passed = result.Passed && len(result.PendingWarningCodes) == 0
	return result
}

func warningCanBeWaived(evaluation Evaluation, code string) bool {
	if !policyAllowsWarning(evaluation.Policy, code) {
		return false
	}
	for _, issue := range evaluation.BaseGate.Blocking {
		if issue.Code == code {
			return false
		}
	}
	for _, issue := range evaluation.BaseGate.Warnings {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func policyAllowsWarning(policy Policy, code string) bool {
	for _, candidate := range policy.WaivableWarningCodes {
		if candidate == code {
			return true
		}
	}
	return false
}

func defaultPolicy(tenantID, examID string) Policy {
	return Policy{TenantID: tenantID, ExamID: examID, Version: DefaultPolicyVersion, Status: PolicyActive,
		WaivableWarningCodes: []string{}}
}

func normalizePolicy(input *CreatePolicyInput) bool {
	input.Version = strings.TrimSpace(input.Version)
	if utf8.RuneCountInString(input.Version) < 3 || utf8.RuneCountInString(input.Version) > 120 {
		return false
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(input.WaivableWarningCodes))
	for _, code := range input.WaivableWarningCodes {
		code = strings.TrimSpace(code)
		if code == "" || utf8.RuneCountInString(code) > 120 || seen[code] {
			return false
		}
		seen[code] = true
		clean = append(clean, code)
	}
	sort.Strings(clean)
	input.WaivableWarningCodes = clean
	// 未经确认，可豁免清单没有业务意义，会误导策略复核。
	return input.RequireWarningAcknowledgement || len(clean) == 0
}

func evidenceHash(evidence Evidence) string {
	// 排除身份和时间戳，让哈希证明不可变门禁载荷，而非某个存储实现的标识格式。
	payload := struct {
		ExamID     string     `json:"exam_id"`
		ReleaseID  string     `json:"release_id"`
		Phase      string     `json:"phase"`
		Evaluation Evaluation `json:"evaluation"`
	}{evidence.ExamID, evidence.ReleaseID, evidence.Phase, evidence.Evaluation}
	bytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func clonePolicy(value Policy) Policy {
	value.WaivableWarningCodes = append([]string(nil), value.WaivableWarningCodes...)
	return value
}

func cloneGate(value scorerelease.Gate) scorerelease.Gate {
	value.Blocking = append([]scorerelease.GateIssue(nil), value.Blocking...)
	value.Warnings = append([]scorerelease.GateIssue(nil), value.Warnings...)
	// 先复制原计数再替换 map，保留阻断数量且不共享可变的证据内容。
	counts := make(map[string]int, len(value.Counts))
	for key, count := range value.Counts {
		counts[key] = count
	}
	value.Counts = counts
	return value
}

func validIDs(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}
