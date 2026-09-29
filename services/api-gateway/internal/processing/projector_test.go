package processing

import (
	"context"
	"errors"
	"testing"
	"time"
)

type projectorStoreStub struct {
	refresh        ProjectionRefresh
	claim          bool
	refreshErr     error
	completed      bool
	failed         bool
	failureDelay   time.Duration
	claimedOwner   string
	completedOwner string
}

func (s *projectorStoreStub) ClaimProjection(_ context.Context, owner string, _ time.Duration) (ProjectionRefresh, bool, error) {
	s.claimedOwner = owner
	return s.refresh, s.claim, nil
}

func (s *projectorStoreStub) ApplyProjection(_ context.Context, owner string, refresh ProjectionRefresh) error {
	if refresh.TenantID != s.refresh.TenantID || refresh.ExamID != s.refresh.ExamID {
		return errors.New("projector refreshed a different exam")
	}
	if s.refreshErr == nil {
		s.completed = refresh == s.refresh
		s.completedOwner = owner
	}
	return s.refreshErr
}

func (s *projectorStoreStub) FailProjection(_ context.Context, owner string, refresh ProjectionRefresh, _ string, delay time.Duration) error {
	s.failed = owner == s.claimedOwner && refresh == s.refresh
	s.failureDelay = delay
	return nil
}

func TestProjectorCompletesClaimedSourceVersion(t *testing.T) {
	// 成功路径必须确认同一租约被应用并完成，避免误确认别的考试版本。
	store := &projectorStoreStub{
		claim:   true,
		refresh: ProjectionRefresh{TenantID: "tenant-a", ExamID: "exam-a", RequestedVersion: 7, AttemptCount: 1},
	}
	projector := NewProjector(store, ProjectorOptions{Owner: "projector-a"})

	worked, err := projector.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("run once = %t, %v", worked, err)
	}
	if !store.completed || store.failed || store.completedOwner != "projector-a" {
		t.Fatalf("claimed projection was not completed: %#v", store)
	}
}

func TestProjectorRetainsFailedVersionForRetryWithBoundedBackoff(t *testing.T) {
	// 连续失败按指数退避并封顶，版本不能被标记为已投影。
	store := &projectorStoreStub{
		claim:      true,
		refresh:    ProjectionRefresh{TenantID: "tenant-a", ExamID: "exam-a", RequestedVersion: 9, AttemptCount: 4},
		refreshErr: errors.New("temporary database error"),
	}
	projector := NewProjector(store, ProjectorOptions{
		Owner: "projector-a", BaseBackoff: time.Second, MaxBackoff: 5 * time.Second,
	})

	worked, err := projector.RunOnce(context.Background())
	if err == nil || !worked {
		t.Fatalf("run once = %t, %v", worked, err)
	}
	if !store.failed || store.completed || store.failureDelay != 5*time.Second {
		t.Fatalf("failed projection was not retained with bounded retry: %#v", store)
	}
}
