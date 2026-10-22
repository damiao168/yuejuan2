package paper

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryExamTemplateBindingKeepsTemplateAndRoutingLifecyclesSeparate(t *testing.T) {
	store := NewMemoryStore()
	store.templates["template-1"] = AnswerSheetTemplate{
		ID: "template-1", TenantID: "tenant-1", ExamID: "exam-1", Status: "locked", ContentHash: "sha256:locked-v1",
	}

	binding, err := store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{
		TemplateID: "template-1", Mode: "locked_with_guard", ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("bind exam template: %v", err)
	}
	if binding.TemplateID != "template-1" || binding.TemplateContentHash != "sha256:locked-v1" || binding.Mode != "locked_with_guard" || binding.Revision != 1 {
		t.Fatalf("unexpected binding: %#v", binding)
	}
	if store.templates["template-1"].Status != "locked" {
		t.Fatal("binding an exam must not mutate the template lifecycle")
	}

	if _, err = store.UnbindExamTemplate(context.Background(), "tenant-1", "exam-1", UnbindExamTemplateInput{ExpectedRevision: binding.Revision - 1, Reason: "stale request"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale unbind should conflict, got %v", err)
	}
	if _, err = store.UnbindExamTemplate(context.Background(), "tenant-1", "exam-1", UnbindExamTemplateInput{ExpectedRevision: binding.Revision, Reason: "operator correction"}); err != nil {
		t.Fatalf("unbind exam template: %v", err)
	}
	if _, err = store.GetExamTemplateBinding(context.Background(), "tenant-1", "exam-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding should be absent after unbind, got %v", err)
	}
	if _, err = store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{TemplateID: "template-1", Mode: "locked_with_guard", ExpectedRevision: binding.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old revision must not rebind after unbind, got %v", err)
	}
	rebound, err := store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{TemplateID: "template-1", Mode: "locked_with_guard", ExpectedRevision: binding.Revision + 1})
	if err != nil || rebound.Revision != binding.Revision+2 {
		t.Fatalf("rebind must consume a fresh revision: binding=%#v err=%v", rebound, err)
	}
}

func TestMemoryExamTemplateBindingRequiresLockedTemplateAndRevision(t *testing.T) {
	store := NewMemoryStore()
	store.templates["draft-1"] = AnswerSheetTemplate{ID: "draft-1", TenantID: "tenant-1", ExamID: "exam-1", Status: "draft", ContentHash: "sha256:draft"}
	if _, err := store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{TemplateID: "draft-1"}); !errors.Is(err, ErrTemplateNotLocked) {
		t.Fatalf("draft template must not be bound, got %v", err)
	}

	store.templates["locked-1"] = AnswerSheetTemplate{ID: "locked-1", TenantID: "tenant-1", ExamID: "exam-1", Status: "locked", ContentHash: "sha256:locked"}
	if _, err := store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{TemplateID: "locked-1", Mode: "bound_auto"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("automatic binding mode must not be accepted from an administrator command, got %v", err)
	}
	if _, err := store.UnbindExamTemplate(context.Background(), "tenant-1", "exam-1", UnbindExamTemplateInput{ExpectedRevision: 1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbind requires an audit reason, got %v", err)
	}
	if _, err := store.BindExamTemplate(context.Background(), "tenant-1", "exam-1", "admin-1", BindExamTemplateInput{TemplateID: "locked-1", ExpectedRevision: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("first binding with a non-zero revision must conflict, got %v", err)
	}
}
