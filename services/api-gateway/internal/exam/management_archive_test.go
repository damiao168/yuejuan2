package exam

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func archiveTestSession(t *testing.T) (*MemoryStore, auth.AccessScope, ManagementExam) {
	t.Helper()
	s := NewMemoryStore()
	scope := auth.AccessScope{TenantID: "tenant-archive", TenantWide: true}
	session, err := s.CreateExamSession(context.Background(), scope, "admin", CreateSessionInput{
		SchoolID: "school", GradeID: "grade", Name: "期中考试", ExamType: "midterm_exam",
		GradingMode: "ai_assisted", PublishPolicy: "manual", ClassIDs: []string{"class-1"},
		Subjects: []SessionSubjectInput{{Subject: "math", TotalScore: 100}, {Subject: "english", TotalScore: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.GetManagementExam(context.Background(), scope, "session", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, item
}

func archiveTestInput(item ManagementExam, commandID string) ManagementArchiveInput {
	revisions := map[string]int64{}
	for _, child := range item.Exams {
		revisions[child.ID] = child.Revision
	}
	return ManagementArchiveInput{ExpectedRevision: item.Revision, ExpectedSubjectRevisions: revisions, CommandID: commandID, Reason: "已结束"}
}

func TestManagementArchivePreservesSubjectStagesAndBlocksChildWrites(t *testing.T) {
	s, scope, item := archiveTestSession(t)
	ctx := context.Background()
	archived, err := s.ArchiveManagementExam(ctx, scope, "admin", item.Kind, item.ID, archiveTestInput(item, "archive-command-1"))
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || !archived.CanRestore || archived.CanEdit || archived.Revision != item.Revision+1 {
		t.Fatalf("unexpected whole exam archive: %+v", archived)
	}
	assertArchivedSubjectWritesBlocked(t, s, ctx, scope, archived, item)
	restored, err := s.RestoreManagementExam(ctx, scope, "admin", item.Kind, item.ID, archiveTestInput(archived, "restore-command-1"))
	if err != nil || restored.ArchivedAt != nil || !restored.CanEdit || !restored.CanArchive {
		t.Fatalf("restore: %+v, %v", restored, err)
	}
	for _, child := range restored.Exams {
		if child.Status != "draft" {
			t.Fatalf("restore changed original stage: %+v", child)
		}
	}
}

func TestManagementArchiveLateReplayAfterRestoreReturnsOriginalReceipt(t *testing.T) {
	s, scope, item := archiveTestSession(t)
	ctx := context.Background()
	input := archiveTestInput(item, "archive-command-replay")
	archived, err := s.ArchiveManagementExam(ctx, scope, "admin", item.Kind, item.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreManagementExam(ctx, scope, "admin", item.Kind, item.ID, archiveTestInput(archived, "restore-command-replay")); err != nil {
		t.Fatal(err)
	}
	// 恢复后迟到的归档重试只能返回旧回执，不能再次把当前考试归档。
	replayed, err := s.ArchiveManagementExam(ctx, scope, "admin", item.Kind, item.ID, input)
	if err != nil || replayed.ArchivedAt == nil || replayed.Revision != archived.Revision {
		t.Fatalf("unstable replay: %+v %v", replayed, err)
	}
	current, _ := s.GetManagementExam(ctx, scope, item.Kind, item.ID)
	if current.ArchivedAt != nil {
		t.Fatal("late replay rearchived restored exam")
	}
	recovered, err := s.RecoverManagementArchiveCommand(ctx, scope, "admin", input.CommandID)
	if err != nil || recovered.Status != "succeeded" || recovered.Exam == nil || recovered.Exam.Revision != archived.Revision {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	input.Reason = "不同请求"
	if _, err := s.ArchiveManagementExam(ctx, scope, "admin", item.Kind, item.ID, input); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed request reused command: %v", err)
	}
	unknown, err := s.RecoverManagementArchiveCommand(ctx, scope, "other-admin", input.CommandID)
	if err != nil || unknown.Status != "not_accepted" {
		t.Fatalf("receipt leaked across actors: %+v %v", unknown, err)
	}
}

func TestManagementArchiveRejectsPartialPermissionAndStaleChildRevision(t *testing.T) {
	s, scope, item := archiveTestSession(t)
	partial := auth.AccessScope{TenantID: scope.TenantID, ExamIDs: []string{item.Exams[0].ID}}
	check, err := s.CheckManagementArchive(context.Background(), partial, item.Kind, item.ID)
	if err != nil || check.Allowed || len(check.Blockers) != 1 || check.Blockers[0].Code != "whole_exam_permission_required" {
		t.Fatalf("partial check: %+v %v", check, err)
	}
	if _, err := s.ArchiveManagementExam(context.Background(), partial, "teacher", item.Kind, item.ID, archiveTestInput(item, "partial-command")); !errors.Is(err, ErrScopeForbidden) {
		t.Fatalf("partial permission accepted: %v", err)
	}
	input := archiveTestInput(item, "stale-child-command")
	input.ExpectedSubjectRevisions[item.Exams[0].ID]++
	if _, err := s.ArchiveManagementExam(context.Background(), scope, "admin", item.Kind, item.ID, input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale child accepted: %v", err)
	}
	input = archiveTestInput(item, "missing-child-command")
	input.ExpectedSubjectRevisions = nil
	if _, err := s.ArchiveManagementExam(context.Background(), scope, "admin", item.Kind, item.ID, input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("missing whole exam subject set accepted: %v", err)
	}
}

func TestManagementArchiveDoesNotTreatStatusOnlyPublicationOrLegacyArchiveAsRecoverable(t *testing.T) {
	s, scope, item := archiveTestSession(t)
	for _, child := range item.Exams {
		_ = s.SetStatusForTest(scope.TenantID, child.ID, "published")
	}
	check, err := s.CheckManagementArchive(context.Background(), scope, item.Kind, item.ID)
	if err != nil || check.Allowed || len(check.Blockers) != 2 || check.Blockers[0].Code != "archive_facts_unavailable" {
		t.Fatalf("unverified published stage accepted: %+v %v", check, err)
	}
	for _, child := range item.Exams {
		_ = s.SetStatusForTest(scope.TenantID, child.ID, "archived")
	}
	current, _ := s.GetManagementExam(context.Background(), scope, item.Kind, item.ID)
	if _, err := s.RestoreManagementExam(context.Background(), scope, "admin", item.Kind, item.ID, archiveTestInput(current, "legacy-restore-command")); !errors.Is(err, ErrLocked) {
		t.Fatalf("legacy lost-stage archive restored: %v", err)
	}
}

func TestManagementArchiveRequiresEveryPublishedAppealWindowToBeClosed(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, window string
		closed       bool
	}{
		{"disabled", `{"enabled":false}`, true},
		{"closed", `{"enabled":true,"closes_at":"2026-10-04T11:59:59Z"}`, true},
		{"exact deadline", `{"enabled":true,"closes_at":"2026-10-04T12:00:00Z"}`, true},
		{"future deadline", `{"enabled":true,"closes_at":"2026-10-04T12:00:01Z"}`, false},
		{"unlimited", `{"enabled":true}`, false},
		{"missing policy", `{}`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			closed, err := managementArchiveAppealWindowClosed([]byte(test.window), now)
			if err != nil || closed != test.closed {
				t.Fatalf("window closed=%v err=%v", closed, err)
			}
		})
	}
	if closed, err := managementArchiveAppealWindowClosed([]byte(`{"enabled":true,"closes_at":"invalid"}`), now); err == nil || closed {
		t.Fatal("malformed fact was treated as closed")
	}
}

// 整场归档保留学科原阶段和版本，但修改名称、阶段及名单都应被锁定。
func assertArchivedSubjectWritesBlocked(t *testing.T, s *MemoryStore, ctx context.Context, scope auth.AccessScope, archived ManagementExam, item ManagementExam) {
	t.Helper()
	for _, child := range archived.Exams {
		if child.Status != "draft" || child.Revision != item.Exams[0].Revision {
			t.Fatalf("archive rewrote subject stage: %+v", child)
		}
		name := "不应保存"
		if _, err := s.UpdateExam(ctx, scope, child.ID, UpdateInput{Name: &name, ExpectedRevision: child.Revision}); !errors.Is(err, ErrLocked) {
			t.Fatalf("archived child update accepted: %v", err)
		}
		if _, err := s.UpdateStatus(ctx, scope, child.ID, "configured", child.Revision); !errors.Is(err, ErrLocked) {
			t.Fatalf("archived child status accepted: %v", err)
		}
		if _, err := s.RefreshCandidateSnapshot(ctx, scope, child.ID); !errors.Is(err, ErrLocked) {
			t.Fatalf("archived child roster accepted: %v", err)
		}
	}
}
