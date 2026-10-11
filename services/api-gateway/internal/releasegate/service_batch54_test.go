package releasegate

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type batch54RegradeReader struct {
	count  int
	err    error
	scopes [][2]string
}

func (r *batch54RegradeReader) BlockingRegradeCount(_ context.Context, tenantID, examID string) (int, error) {
	r.scopes = append(r.scopes, [2]string{tenantID, examID})
	return r.count, r.err
}

type batch54Publisher struct{ calls int }

func (p *batch54Publisher) Publish(context.Context, string, string, string) (scorerelease.Release, error) {
	p.calls++
	return scorerelease.Release{}, nil
}

type batch54EvidenceStore struct {
	*MemoryStore
	appendCalls int
}

func (s *batch54EvidenceStore) AppendEvidence(ctx context.Context, evidence Evidence) (Evidence, error) {
	s.appendCalls++
	return s.MemoryStore.AppendEvidence(ctx, evidence)
}

func TestPublishRechecksNewRegradeBlockersAndPreservesPreviewEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	reader := &batch54RegradeReader{}
	base := fakeBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true}}
	service := NewService(store, base).WithRegradeBlockerReader(reader)
	publisher := &batch54Publisher{}
	coordinator := NewPublicationCoordinator(service, publisher)
	preview, err := service.Preview(ctx, "tenant", "exam", "release", "manager")
	if err != nil || !preview.Evaluation.Passed {
		t.Fatalf("expected passing preview before regrade starts: evidence=%#v err=%v", preview, err)
	}

	// 预览后出现待落库重评，发布必须重读当前阻断项；旧预览仍保留当时的不可变事实。
	reader.count = 2
	_, publish, err := coordinator.Publish(ctx, "tenant", "exam", "release", "principal")
	if !errors.Is(err, scorerelease.ErrGateBlocked) {
		t.Fatalf("expected publication gate conflict for pending regrade, got %v", err)
	}
	if publisher.calls != 0 {
		t.Fatalf("publisher was called despite pending regrades: %d", publisher.calls)
	}
	if publish.ID == "" || publish.ID == preview.ID || publish.Phase != EvidencePublish || publish.Evaluation.Passed {
		t.Fatalf("expected a new blocked publication evidence, got %#v", publish)
	}
	if len(publish.Evaluation.BaseGate.Blocking) != 1 {
		t.Fatalf("expected one regrade blocker, got %#v", publish.Evaluation.BaseGate.Blocking)
	}
	blocker := publish.Evaluation.BaseGate.Blocking[0]
	if blocker.Code != "regrade_release_pending" || !blocker.Blocking || blocker.Count != 2 || blocker.ActionRoute != "regrade" {
		t.Fatalf("unexpected regrade blocker: %#v", blocker)
	}
	if publish.Evaluation.BaseGate.Passed || publish.Evaluation.BaseGate.Counts["regrade_release_pending"] != 2 {
		t.Fatalf("regrade blocker was not reflected in source gate: %#v", publish.Evaluation.BaseGate)
	}
	if len(reader.scopes) != 2 || reader.scopes[0] != [2]string{"tenant", "exam"} || reader.scopes[1] != [2]string{"tenant", "exam"} {
		t.Fatalf("preview and publish must each query scoped regrades: %#v", reader.scopes)
	}
	assertRegradePublicationEvidenceIsImmutable(t, ctx, service, preview, publish)
}

func TestPublishRejectsUnknownRegradeStateWithoutAppendingPassingEvidence(t *testing.T) {
	ctx := context.Background()
	store := &batch54EvidenceStore{MemoryStore: NewMemoryStore()}
	reader := &batch54RegradeReader{}
	base := fakeBaseGate{gate: scorerelease.Gate{Version: "A18", Passed: true}}
	service := NewService(store, base).WithRegradeBlockerReader(reader)
	publisher := &batch54Publisher{}
	coordinator := NewPublicationCoordinator(service, publisher)
	preview, err := service.Preview(ctx, "tenant", "exam", "release", "manager")
	if err != nil || !preview.Evaluation.Passed || store.appendCalls != 1 {
		t.Fatalf("expected one passing preview: evidence=%#v writes=%d err=%v", preview, store.appendCalls, err)
	}

	// 重评状态读取失败不是零个阻断项，不能复用旧预览发布或写入伪造的通过证据。
	reader.err = errors.New("regrade state unavailable")
	_, evidence, err := coordinator.Publish(ctx, "tenant", "exam", "release", "principal")
	if !errors.Is(err, reader.err) {
		t.Fatalf("expected original regrade reader error, got %v", err)
	}
	if publisher.calls != 0 || store.appendCalls != 1 {
		t.Fatalf("unknown regrade state triggered publication or evidence write: publishes=%d writes=%d", publisher.calls, store.appendCalls)
	}
	if evidence.ID != "" || evidence.Hash != "" || evidence.Evaluation.Passed {
		t.Fatalf("failed recheck returned publication evidence: %#v", evidence)
	}
	if len(reader.scopes) != 2 || reader.scopes[1] != [2]string{"tenant", "exam"} {
		t.Fatalf("publish did not recheck tenant-scoped regrade state: %#v", reader.scopes)
	}
	savedPreview, err := service.GetEvidence(ctx, "tenant", preview.ID)
	if err != nil || !savedPreview.Evaluation.Passed || savedPreview.Hash != preview.Hash {
		t.Fatalf("failed recheck changed existing preview: evidence=%#v err=%v", savedPreview, err)
	}
}

func assertRegradePublicationEvidenceIsImmutable(t *testing.T, ctx context.Context, service *Service, preview, publish Evidence) {
	t.Helper()
	savedPreview, err := service.GetEvidence(ctx, "tenant", preview.ID)
	if err != nil || !savedPreview.Evaluation.Passed || len(savedPreview.Evaluation.BaseGate.Blocking) != 0 || savedPreview.Hash != preview.Hash {
		t.Fatalf("publication changed immutable preview evidence: evidence=%#v err=%v", savedPreview, err)
	}
	savedPublish, err := service.GetEvidence(ctx, "tenant", publish.ID)
	if err != nil || savedPublish.Evaluation.Passed || savedPublish.Hash != publish.Hash || savedPublish.Evaluation.BaseGate.Counts["regrade_release_pending"] != 2 {
		t.Fatalf("blocked publication evidence was not persisted: evidence=%#v err=%v", savedPublish, err)
	}
	// 调用方修改返回副本不能反向改变已落库证据的计数。
	publish.Evaluation.BaseGate.Counts["regrade_release_pending"] = 99
	unchanged, err := service.GetEvidence(ctx, "tenant", publish.ID)
	if err != nil || unchanged.Evaluation.BaseGate.Counts["regrade_release_pending"] != 2 {
		t.Fatalf("returned count map shares stored evidence: evidence=%#v err=%v", unchanged, err)
	}
}
