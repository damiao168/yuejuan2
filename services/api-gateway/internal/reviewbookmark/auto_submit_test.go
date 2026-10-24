package reviewbookmark

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func autoSubmitMemoryFixture(t *testing.T) (*review.MemoryStore, review.ReviewTask, auth.User) {
	t.Helper()
	user := testUser()
	store := review.NewMemoryStore()
	store.AddContext(user.TenantID, "segment", review.Context{ExamID: "exam", SubmissionID: "submission", Question: paper.Question{ID: "question", QuestionNo: "Q1", Score: 5}, Rubric: paper.Rubric{ID: "rubric", MaxScore: 5, Points: []paper.RubricPoint{{ID: "p1", Score: 5}}}})
	task, err := store.CreateTask(context.Background(), user.TenantID, user.ID, review.CreateTaskInput{AnswerSegmentID: "segment", Source: "manual_sample", AssignedTo: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	return store, task, user
}

func TestMemoryAutomaticGradeUsesActualPersonalBookmarkAndTombstone(t *testing.T) {
	reviews, task, user := autoSubmitMemoryFixture(t)
	bookmarks := NewMemoryStore(&testSourceProvider{source: Source{TaskID: task.ID, ReviewTaskID: task.ID}})
	reviews.WithAutoSubmitBookmarkGuard(bookmarks)
	ack, err := bookmarks.Put(context.Background(), user, task.ID, PutInput{})
	if err != nil {
		t.Fatal(err)
	}
	input := review.SubmitGradeInput{AutoSubmit: true, Score: 0, ExpectedRevision: task.Revision}
	if _, err := reviews.SubmitGrade(context.Background(), user.TenantID, task.ID, user.ID, input); !errors.Is(err, review.ErrTaskMarked) {
		t.Fatalf("actual mark did not block grade: %v", err)
	}
	current, err := reviews.GetTask(context.Background(), user.TenantID, task.ID)
	if err != nil || current.Status != "assigned" || current.Revision != task.Revision {
		t.Fatalf("blocked submission changed task: %#v %v", current, err)
	}
	if _, err = bookmarks.Delete(context.Background(), user, ack.BookmarkID, DeleteInput{ExpectedRevision: ack.Revision}); err != nil {
		t.Fatal(err)
	}
	ctx := commandreceipt.WithID(context.Background(), "submit-after-unmark")
	result, err := reviews.SubmitGrade(ctx, user.TenantID, task.ID, user.ID, input)
	if err != nil || result.Grade.Score != 0 || result.Task.Status != "submitted" {
		t.Fatalf("bookmark tombstone still blocked grade: %#v %v", result, err)
	}
	if _, err = bookmarks.Put(context.Background(), user, task.ID, PutInput{ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	replay, err := reviews.SubmitGrade(ctx, user.TenantID, task.ID, user.ID, input)
	if err != nil || replay.Grade.ID != result.Grade.ID {
		t.Fatalf("later mark invalidated successful grade receipt: %#v %v", replay, err)
	}
}

type autoSubmitBlockingSource struct {
	reviews *review.MemoryStore
	entered chan struct{}
	resume  chan struct{}
}

func (p *autoSubmitBlockingSource) Resolve(ctx context.Context, user auth.User, taskID string) (Source, error) {
	if _, err := p.reviews.GetTask(ctx, user.TenantID, taskID); err != nil {
		return Source{}, err
	}
	close(p.entered)
	<-p.resume
	return Source{TaskID: taskID, ReviewTaskID: taskID}, nil
}

func TestMemoryBookmarkFirstCreationSerializesAutomaticGrade(t *testing.T) {
	reviews, task, user := autoSubmitMemoryFixture(t)
	provider := &autoSubmitBlockingSource{reviews: reviews, entered: make(chan struct{}), resume: make(chan struct{})}
	bookmarks := NewMemoryStore(provider)
	reviews.WithAutoSubmitBookmarkGuard(bookmarks)
	putDone := make(chan error, 1)
	go func() {
		_, err := bookmarks.Put(context.Background(), user, task.ID, PutInput{})
		putDone <- err
	}()
	// 首次标记还没有记录行；在来源检查已持标记锁时并发自动提交，保护检查与写分之间的空隙。
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("bookmark source lookup did not start")
	}
	submitDone := make(chan error, 1)
	go func() {
		_, err := reviews.SubmitGrade(context.Background(), user.TenantID, task.ID, user.ID, review.SubmitGradeInput{AutoSubmit: true, Score: 4, ExpectedRevision: task.Revision})
		submitDone <- err
	}()
	close(provider.resume)
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-submitDone:
		if !errors.Is(err, review.ErrTaskMarked) {
			t.Fatalf("first bookmark was bypassed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bookmark/task lock order deadlocked")
	}
}

var _ review.AutoSubmitBookmarkGuard = (*MemoryStore)(nil)
