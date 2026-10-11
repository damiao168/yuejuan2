package releasegate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type remediationBaseGate struct {
	gate  scorerelease.Gate
	err   error
	calls int
}

func (r *remediationBaseGate) Gate(context.Context, string, string) (scorerelease.Gate, error) {
	r.calls++
	return r.gate, r.err
}

type remediationServiceStore struct {
	*MemoryStore
	policyErr      error
	waiversErr     error
	appendErr      error
	policyCalls    int
	waiversCalls   int
	appendCalls    int
	latestEvidence Evidence
}

func (s *remediationServiceStore) ActivePolicy(ctx context.Context, tenantID, examID string) (Policy, error) {
	s.policyCalls++
	if s.policyErr != nil {
		return Policy{}, s.policyErr
	}
	return s.MemoryStore.ActivePolicy(ctx, tenantID, examID)
}

func (s *remediationServiceStore) ApprovedWaivers(ctx context.Context, tenantID, examID, policyID string) ([]Waiver, error) {
	s.waiversCalls++
	if s.waiversErr != nil {
		return nil, s.waiversErr
	}
	return s.MemoryStore.ApprovedWaivers(ctx, tenantID, examID, policyID)
}

func (s *remediationServiceStore) AppendEvidence(ctx context.Context, evidence Evidence) (Evidence, error) {
	s.appendCalls++
	if s.appendErr != nil {
		return Evidence{}, s.appendErr
	}
	saved, err := s.MemoryStore.AppendEvidence(ctx, evidence)
	if err == nil {
		s.latestEvidence = saved
	}
	return saved, err
}

type remediationPublisher struct {
	result        scorerelease.Release
	err           error
	scopes        [][3]string
	beforePublish func()
}

func (p *remediationPublisher) Publish(_ context.Context, tenantID, releaseID, actorID string) (scorerelease.Release, error) {
	p.scopes = append(p.scopes, [3]string{tenantID, releaseID, actorID})
	if p.beforePublish != nil {
		p.beforePublish()
	}
	return p.result, p.err
}

func TestRemediationPublishUsesCurrentPolicyAndKeepsOldPreview(t *testing.T) {
	ctx := context.Background()
	store := &remediationServiceStore{MemoryStore: NewMemoryStore()}
	base := &remediationBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true,
		Warnings: []scorerelease.GateIssue{{Code: "quality_warning"}, {Code: "quality_warning"}, {Code: "routine_warning"}}}}
	service := NewService(store, base)
	oldPolicy, err := service.CreatePolicy(ctx, "tenant", "exam", "manager", CreatePolicyInput{
		Version: "school-v1", RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning"},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(ctx, "tenant", "exam", " release ", "manager")
	if err != nil || preview.ReleaseID != "release" || !reflect.DeepEqual(preview.Evaluation.PendingWarningCodes, []string{"quality_warning"}) {
		t.Fatalf("warning acknowledgement must be required once per allowed code: evidence=%#v err=%v", preview, err)
	}
	waiver, err := service.RequestWaiver(ctx, "tenant", "exam", "manager", RequestWaiverInput{
		EvidenceID: preview.ID, IssueCode: "quality_warning", Reason: "质量警告已核实",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideWaiver(ctx, "tenant", waiver.ID, "principal", DecideWaiverInput{Approve: true, Reason: "同意确认"}); err != nil {
		t.Fatal(err)
	}
	passingPreview, err := service.Preview(ctx, "tenant", "exam", "release", "manager")
	if err != nil || !passingPreview.Evaluation.Passed || !reflect.DeepEqual(passingPreview.Evaluation.AppliedWaiverIDs, []string{waiver.ID}) {
		t.Fatalf("expected old policy to accept its approved waiver: evidence=%#v err=%v", passingPreview, err)
	}

	// 通过预览后换版策略；旧版批准仍是历史事实，不能自动确认新版策略中的同名警告。
	currentPolicy, err := service.CreatePolicy(ctx, "tenant", "exam", "manager", CreatePolicyInput{
		Version: "school-v2", RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning"},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeInspect := store.appendCalls
	inspection, err := service.Inspect(ctx, "tenant", "exam")
	if err != nil || inspection.Passed || inspection.Policy.ID != currentPolicy.ID || store.appendCalls != beforeInspect {
		t.Fatalf("inspection must use current policy without creating evidence: result=%#v writes=%d err=%v", inspection, store.appendCalls, err)
	}
	publisher := &remediationPublisher{}
	_, publish, err := NewPublicationCoordinator(service, publisher).Publish(ctx, "tenant", "exam", "release", "principal")
	if !errors.Is(err, scorerelease.ErrGateBlocked) || len(publisher.scopes) != 0 {
		t.Fatalf("old approval bypassed new policy: calls=%#v err=%v", publisher.scopes, err)
	}
	if publish.ID == "" || publish.ID == passingPreview.ID || publish.Phase != EvidencePublish || publish.Evaluation.Policy.ID != currentPolicy.ID ||
		publish.Evaluation.Passed || len(publish.Evaluation.AppliedWaiverIDs) != 0 || !reflect.DeepEqual(publish.Evaluation.PendingWarningCodes, []string{"quality_warning"}) {
		t.Fatalf("publication did not persist the new policy decision: %#v", publish)
	}
	savedPreview, err := service.GetEvidence(ctx, "tenant", passingPreview.ID)
	if err != nil || !savedPreview.Evaluation.Passed || savedPreview.Evaluation.Policy.ID != oldPolicy.ID || savedPreview.Hash != passingPreview.Hash {
		t.Fatalf("policy replacement changed old preview: evidence=%#v err=%v", savedPreview, err)
	}
}

func TestRemediationPublicationResultPreservesPersistedGateEvidence(t *testing.T) {
	publisherFailure := errors.New("score publication transaction failed")
	for _, test := range []releaseGatePublicationResultCase{
		{name: "publisher_success"},
		{name: "publisher_failure", err: publisherFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertReleaseGatePersistedPublicationResult(t, test)
		})
	}
}

func TestRemediationPublicationStopsWhenGateDependencyFails(t *testing.T) {
	dependencyFailure := errors.New("gate dependency unavailable")
	for _, stage := range []string{"base_gate", "policy", "approved_waivers", "evidence_append"} {
		t.Run(stage, func(t *testing.T) {
			assertReleaseGateDependencyStopsPublication(t, stage, dependencyFailure)
		})
	}
}

func TestRemediationWaiverRequiresScopedPreviewWarningAndFinalDecision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	store.SetNow(func() time.Time { return now })
	service := NewService(store, fakeBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true, Warnings: []scorerelease.GateIssue{{Code: "quality_warning"}}}})
	service.now = func() time.Time { return now }
	if _, err := service.CreatePolicy(ctx, "tenant", "exam", "manager", CreatePolicyInput{
		Version: "school-v1", RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning", "absent_warning"},
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(ctx, "tenant", "exam", "release", "manager")
	if err != nil {
		t.Fatal(err)
	}
	publish, err := service.RecheckForPublish(ctx, "tenant", "exam", "release", "manager")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("unacknowledged warning must block publication: %v", err)
	}
	for _, test := range []struct {
		name, tenantID, examID, evidenceID, code, reason string
		wantErr                                          error
	}{
		{name: "other_tenant", tenantID: "other", examID: "exam", evidenceID: preview.ID, code: "quality_warning", reason: "已核实", wantErr: ErrNotFound},
		{name: "other_exam", tenantID: "tenant", examID: "other", evidenceID: preview.ID, code: "quality_warning", reason: "已核实", wantErr: ErrForbiddenWaiver},
		{name: "publish_evidence", tenantID: "tenant", examID: "exam", evidenceID: publish.ID, code: "quality_warning", reason: "已核实", wantErr: ErrForbiddenWaiver},
		{name: "absent_warning", tenantID: "tenant", examID: "exam", evidenceID: preview.ID, code: "absent_warning", reason: "已核实", wantErr: ErrForbiddenWaiver},
		{name: "empty_reason", tenantID: "tenant", examID: "exam", evidenceID: preview.ID, code: "quality_warning", reason: " \t ", wantErr: ErrInvalidInput},
		{name: "too_long_reason", tenantID: "tenant", examID: "exam", evidenceID: preview.ID, code: "quality_warning", reason: strings.Repeat("审", 1001), wantErr: ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.RequestWaiver(ctx, test.tenantID, test.examID, "manager", RequestWaiverInput{EvidenceID: test.evidenceID, IssueCode: test.code, Reason: test.reason})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("waiver boundary returned %v; want %v", err, test.wantErr)
			}
		})
	}
	waiver, err := service.RequestWaiver(ctx, "tenant", "exam", "manager", RequestWaiverInput{
		EvidenceID: " " + preview.ID + " ", IssueCode: " quality_warning ", Reason: " " + strings.Repeat("审", 1000) + " ",
	})
	if err != nil || waiver.EvidenceID != preview.ID || waiver.IssueCode != "quality_warning" || waiver.Reason != strings.Repeat("审", 1000) || !waiver.RequestedAt.Equal(now) || waiver.RequestedAt.Location() != time.UTC {
		t.Fatalf("valid Unicode reason or normalized evidence reference was rejected: waiver=%#v err=%v", waiver, err)
	}
	if _, err := service.DecideWaiver(ctx, "other", waiver.ID, "principal", DecideWaiverInput{Approve: true, Reason: "已核实"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant could decide waiver: %v", err)
	}
	if _, err := service.DecideWaiver(ctx, "tenant", waiver.ID, "principal", DecideWaiverInput{Approve: true, Reason: " \t "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty decision reason accepted: %v", err)
	}
	decision, err := service.DecideWaiver(ctx, "tenant", waiver.ID, "principal", DecideWaiverInput{Approve: false, Reason: " 需要整改 "})
	if err != nil || decision.Status != WaiverRejected || decision.DecisionReason != "需要整改" {
		t.Fatalf("rejection was not persisted: waiver=%#v err=%v", decision, err)
	}
	// 拒绝是只追加的终态；重试批准不能覆盖既有决定，也不能消除发布警告。
	if _, err := service.DecideWaiver(ctx, "tenant", waiver.ID, "principal", DecideWaiverInput{Approve: true, Reason: "重新批准"}); !errors.Is(err, ErrForbiddenWaiver) {
		t.Fatalf("final rejection could be overwritten: %v", err)
	}
	inspection, err := service.Inspect(ctx, "tenant", "exam")
	if err != nil || inspection.Passed || len(inspection.AppliedWaiverIDs) != 0 || !reflect.DeepEqual(inspection.PendingWarningCodes, []string{"quality_warning"}) {
		t.Fatalf("rejected waiver was applied to publication gate: result=%#v err=%v", inspection, err)
	}
}

type releaseGatePublicationResultCase struct {
	name string
	err  error
}

func assertReleaseGatePersistedPublicationResult(t *testing.T, test releaseGatePublicationResultCase) {
	t.Helper()
	ctx := context.Background()
	store := &remediationServiceStore{MemoryStore: NewMemoryStore()}
	service := NewService(store, &remediationBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true}})
	publisher := &remediationPublisher{result: scorerelease.Release{ID: "release", Status: "published"}, err: test.err}
	// 成绩事务可能在门禁求值通过后失败；已保存的门禁事实仍需能供失败排查读取。
	publisher.beforePublish = func() {
		if store.latestEvidence.ID == "" || store.latestEvidence.Phase != EvidencePublish || !store.latestEvidence.Evaluation.Passed {
			t.Fatalf("publisher ran before its passing evidence was persisted: %#v", store.latestEvidence)
		}
	}
	release, evidence, err := NewPublicationCoordinator(service, publisher).Publish(ctx, "tenant", "exam", "release", "principal")
	if !errors.Is(err, test.err) || !reflect.DeepEqual(publisher.scopes, [][3]string{{"tenant", "release", "principal"}}) {
		t.Fatalf("publication did not forward scope or original error: scopes=%#v err=%v", publisher.scopes, err)
	}
	if test.err == nil && !reflect.DeepEqual(release, publisher.result) {
		t.Fatalf("successful publication lost its release: %#v", release)
	}
	if test.err != nil && !reflect.DeepEqual(release, scorerelease.Release{}) {
		t.Fatalf("failed publication exposed an incomplete release: %#v", release)
	}
	saved, readErr := service.GetEvidence(ctx, "tenant", evidence.ID)
	if readErr != nil || !reflect.DeepEqual(saved, evidence) || !evidence.Evaluation.Passed || store.appendCalls != 1 {
		t.Fatalf("publication result lost its persisted evidence: evidence=%#v saved=%#v writes=%d err=%v", evidence, saved, store.appendCalls, readErr)
	}
}

func assertReleaseGateDependencyStopsPublication(t *testing.T, stage string, dependencyFailure error) {
	t.Helper()
	ctx := context.Background()
	store := &remediationServiceStore{MemoryStore: NewMemoryStore()}
	base := &remediationBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true}}
	service := NewService(store, base)
	preview, err := service.Preview(ctx, "tenant", "exam", "release", "manager")
	if err != nil {
		t.Fatal(err)
	}
	policyCalls, waiverCalls, appendCalls := store.policyCalls, store.waiversCalls, store.appendCalls
	switch stage {
	case "base_gate":
		base.err = dependencyFailure
	case "policy":
		store.policyErr = dependencyFailure
	case "approved_waivers":
		store.waiversErr = dependencyFailure
	case "evidence_append":
		store.appendErr = dependencyFailure
	}
	// 无法得到完整的新门禁或保存证据时，已有通过预览不能作为本次发布许可。
	publisher := &remediationPublisher{}
	release, evidence, err := NewPublicationCoordinator(service, publisher).Publish(ctx, "tenant", "exam", "release", "principal")
	if !errors.Is(err, dependencyFailure) || len(publisher.scopes) != 0 || !reflect.DeepEqual(release, scorerelease.Release{}) || !reflect.DeepEqual(evidence, Evidence{}) {
		t.Fatalf("dependency failure produced publication or evidence: release=%#v evidence=%#v calls=%#v err=%v", release, evidence, publisher.scopes, err)
	}
	assertReleaseGateStopsLaterDependencies(t, stage, store, policyCalls, waiverCalls, appendCalls)
	saved, readErr := service.GetEvidence(ctx, "tenant", preview.ID)
	if readErr != nil || !reflect.DeepEqual(saved, preview) {
		t.Fatalf("dependency failure changed historical preview: evidence=%#v err=%v", saved, readErr)
	}
}

// 原阶段读取计数是在成功预览后取得；失败发布不得绕过缺失依赖继续读取或保存通过证据。
func assertReleaseGateStopsLaterDependencies(t *testing.T, stage string, store *remediationServiceStore, policyCalls, waiverCalls, appendCalls int) {
	t.Helper()
	if stage == "base_gate" && (store.policyCalls != policyCalls || store.waiversCalls != waiverCalls) {
		t.Fatalf("base failure did not stop later reads: policy=%d waivers=%d", store.policyCalls, store.waiversCalls)
	}
	if stage == "policy" && store.waiversCalls != waiverCalls {
		t.Fatalf("policy failure queried waivers without a known policy: %d", store.waiversCalls)
	}
	wantAppendCalls := appendCalls
	if stage == "evidence_append" {
		wantAppendCalls++
	}
	if store.appendCalls != wantAppendCalls {
		t.Fatalf("unexpected evidence writes after %s failure: got=%d want=%d", stage, store.appendCalls, wantAppendCalls)
	}
}
