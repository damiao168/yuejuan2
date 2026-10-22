package paper

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func assertTemplateBindingCursorLifecycle(t *testing.T, store TemplateBindingRepository, tenant, exam, actor, templateID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	initial, err := store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || initial.Binding != nil || initial.Revision != 0 {
		t.Fatalf("initial state: %#v err=%v", initial, err)
	}
	first, err := store.BindExamTemplate(ctx, tenant, exam, actor, BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: initial.Revision})
	if err != nil || first.Revision != 1 {
		t.Fatalf("first bind: %#v err=%v", first, err)
	}
	if _, err = store.UnbindExamTemplate(ctx, tenant, exam, UnbindExamTemplateInput{ExpectedRevision: first.Revision, Reason: "operator correction"}); err != nil {
		t.Fatal(err)
	}
	unbound, err := store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || unbound.Binding != nil || unbound.Revision != 2 {
		t.Fatalf("read after unbind: %#v err=%v", unbound, err)
	}
	for _, revision := range []int{0, 1} {
		if _, err = store.BindExamTemplate(ctx, tenant, exam, actor, BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: revision}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale bind %d before recreation: %v", revision, err)
		}
	}

	// 两名编辑者都读取到空绑定 revision=2；仓储必须在同一临界区内比较游标和写入绑定。
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, bindErr := store.BindExamTemplate(ctx, tenant, exam, actor, BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: unbound.Revision})
			results <- bindErr
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var successful, conflicted int
	for result := range results {
		switch {
		case result == nil:
			successful++
		case errors.Is(result, ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent bind: %v", result)
		}
	}
	if successful != 1 || conflicted != 1 {
		t.Fatalf("concurrent recreation: successful=%d conflicted=%d", successful, conflicted)
	}
	for _, revision := range []int{0, 1} {
		if _, err = store.BindExamTemplate(ctx, tenant, exam, actor, BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: revision}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale bind %d after recreation: %v", revision, err)
		}
		if _, err = store.UnbindExamTemplate(ctx, tenant, exam, UnbindExamTemplateInput{ExpectedRevision: revision, Reason: "late old removal"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale unbind %d after recreation: %v", revision, err)
		}
	}
	final, err := store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || final.Binding == nil || final.Binding.Revision != 3 || final.Revision != 3 || final.Binding.TemplateID != templateID {
		t.Fatalf("stale commands changed final state: %#v err=%v", final, err)
	}
}

func TestMemoryTemplateBindingCursorSurvivesConcurrentRecreation(t *testing.T) {
	store := NewMemoryStore()
	store.templates["template"] = AnswerSheetTemplate{ID: "template", TenantID: "tenant", ExamID: "exam", Status: "locked", ContentHash: "sha256:locked"}
	assertTemplateBindingCursorLifecycle(t, store, "tenant", "exam", "actor", "template")
	other, err := store.GetExamTemplateBindingState(context.Background(), "other-tenant", "exam")
	if err != nil || other.Binding != nil || other.Revision != 0 {
		t.Fatalf("another tenant must not see the cursor: %#v err=%v", other, err)
	}
}
