package reviewbookmark

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

type testSourceProvider struct {
	source Source
	err    error
}

func (p *testSourceProvider) Resolve(context.Context, auth.User, string) (Source, error) {
	return p.source, p.err
}
func testUser() auth.User {
	return auth.User{ID: "grader", TenantID: "tenant", Status: "active", Permissions: []string{"review:work"}}
}
func testStore() (*MemoryStore, *testSourceProvider) {
	p := &testSourceProvider{source: Source{TaskID: "task", ReviewTaskID: "task", ExamID: "exam", ExamName: "合成考试", QuestionID: "question", QuestionNo: "Q1", AnonymousCode: "ANON", State: "processable"}}
	return NewMemoryStore(p), p
}

func TestMemoryBookmarkTombstoneAndReceipt(t *testing.T) {
	store, provider := testStore()
	user := testUser()
	ctx := context.Background()
	create := commandreceipt.WithID(ctx, "create")
	first, err := store.Put(create, user, "task", PutInput{Reason: "核对第二步"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || !first.Marked {
		t.Fatalf("create: %+v", first)
	}
	removed, err := store.Delete(commandreceipt.WithID(ctx, "remove"), user, first.BookmarkID, DeleteInput{ExpectedRevision: 1})
	if err != nil || removed.Revision != 2 || removed.Marked {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	// 延迟的创建确认可以重放旧回执，但不能复活墓碑；下次读取必须看到取消后的版本。
	replayed, err := store.Put(create, user, "task", PutInput{Reason: "核对第二步"})
	if err != nil || replayed != first {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	state, err := store.State(ctx, user, "task")
	if err != nil || state.Marked || state.Revision != 2 || state.Reason != "" {
		t.Fatalf("state: %+v %v", state, err)
	}
	_, err = store.Put(ctx, user, "task", PutInput{ExpectedRevision: 0})
	var conflict *RevisionConflict
	if !errors.As(err, &conflict) || conflict.Revision != 2 {
		t.Fatalf("stale create: %v", err)
	}
	added, err := store.Put(ctx, user, "task", PutInput{ExpectedRevision: 2})
	if err != nil || added.BookmarkID != first.BookmarkID || added.Revision != 3 {
		t.Fatalf("readd: %+v %v", added, err)
	}
	provider.err = ErrNotFound
	if _, err = store.Put(create, user, "task", PutInput{Reason: "核对第二步"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked replay: %v", err)
	}
	list, err := store.List(ctx, user, ListFilter{})
	if err != nil || list.TotalCount != 0 || len(list.Exams) != 0 {
		t.Fatalf("inaccessible list: %+v %v", list, err)
	}
	items, err := store.Cleanup(ctx, user)
	if err != nil || len(items) != 1 {
		t.Fatalf("cleanup: %+v %v", items, err)
	}
	user.Permissions = nil
	if _, err = store.Delete(ctx, user, first.BookmarkID, DeleteInput{ExpectedRevision: 3}); err != nil {
		t.Fatalf("lost role cleanup: %v", err)
	}
}

func TestMemoryBookmarkConcurrentCAS(t *testing.T) {
	store, _ := testStore()
	user := testUser()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Put(context.Background(), user, "task", PutInput{})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		var conflict *RevisionConflict
		if err == nil {
			successes++
		} else if errors.As(err, &conflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
}

func TestMemoryBookmarkUnicodeArchiveAndOwner(t *testing.T) {
	store, provider := testStore()
	user := testUser()
	ctx := context.Background()
	first, err := store.Put(ctx, user, "task", PutInput{Reason: strings.Repeat("😀", 200)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Put(ctx, user, "task", PutInput{Reason: strings.Repeat("😀", 201), ExpectedRevision: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	other := user
	other.ID = "other"
	if _, err = store.Delete(ctx, other, first.BookmarkID, DeleteInput{ExpectedRevision: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner isolation: %v", err)
	}
	provider.source.Archived = true
	if _, err = store.Put(ctx, user, "task", PutInput{Reason: "edit", ExpectedRevision: 1}); !errors.Is(err, ErrArchived) {
		t.Fatalf("archive edit: %v", err)
	}
	state, err := store.State(ctx, user, "task")
	if err != nil || state.CanEditReason {
		t.Fatalf("archive read: %+v %v", state, err)
	}
	if _, err = store.Delete(ctx, user, first.BookmarkID, DeleteInput{ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Put(ctx, user, "task", PutInput{ExpectedRevision: 2}); !errors.Is(err, ErrArchived) {
		t.Fatalf("archive readd: %v", err)
	}
}

func TestMemoryReceiptPreservesExactInput(t *testing.T) {
	store, _ := testStore()
	ctx := commandreceipt.WithID(context.Background(), "exact")
	user := testUser()
	if _, err := store.Put(ctx, user, "task", PutInput{Reason: " reason "}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, user, "task", PutInput{Reason: "reason"}); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("normalized input hid request change: %v", err)
	}
}
