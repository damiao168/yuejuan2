package reviewannotation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalGeometryJSONRoundTrip(t *testing.T) {
	want := ImageGeometry{
		CoordinateSpace: CanonicalImageNormalized,
		X:               0.123456789, Y: 0.234567891, Width: 0.345678912, Height: 0.456789123,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got ImageGeometry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !got.Valid() {
		t.Fatalf("geometry did not round trip: got %#v want %#v", got, want)
	}
}

// 覆盖租户隔离和乐观锁：旧 revision 的保存必须失败，不能静默覆盖最新批注。
func TestMemoryAnnotationTenantIsolationAndOptimisticLock(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateAnnotation(ctx, "tenant-a", "task-1", "reviewer-1", CreateAnnotationInput{
		Type:     AnnotationHighlight,
		Geometry: ImageGeometry{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4},
		Content:  "show the working", Visibility: VisibilityStudentAfterPublish,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Geometry.CoordinateSpace != CanonicalImageNormalized || created.Revision != 1 {
		t.Fatalf("unexpected created annotation: %#v", created)
	}
	if _, err := store.GetAnnotation(ctx, "tenant-b", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read error = %v, want not found", err)
	}
	otherItems, err := store.ListAnnotations(ctx, "tenant-b", "task-1")
	if err != nil || len(otherItems) != 0 {
		t.Fatalf("cross-tenant list leaked data: %#v, %v", otherItems, err)
	}

	updated, err := store.UpdateAnnotation(ctx, "tenant-a", created.ID, "reviewer-1", UpdateAnnotationInput{
		Type: created.Type, Geometry: created.Geometry, Content: "revised",
		Visibility: created.Visibility, ExpectedRevision: created.Revision,
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	_, err = store.UpdateAnnotation(ctx, "tenant-a", created.ID, "reviewer-1", UpdateAnnotationInput{
		Type: created.Type, Geometry: created.Geometry, Content: "stale",
		Visibility: created.Visibility, ExpectedRevision: created.Revision,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v, want revision conflict", err)
	}
}

func TestStudentDTOCannotRepresentPrivateAnnotation(t *testing.T) {
	privateItem := Annotation{
		ID: "private-id", Visibility: VisibilityPrivate, Content: "teacher-only",
	}
	visibleItem := Annotation{
		ID: "visible-id", AnswerSegmentID: "segment-1", SubmissionPageID: "page-1",
		Type: AnnotationNote, Geometry: ImageGeometry{CoordinateSpace: CanonicalImageNormalized, X: .2, Y: .3},
		Content: "student feedback", Visibility: VisibilityStudentAfterPublish,
	}
	studentItems := StudentAnnotations([]Annotation{privateItem, visibleItem})
	if len(studentItems) != 1 || studentItems[0].ID != visibleItem.ID {
		t.Fatalf("student filtering returned %#v", studentItems)
	}
	raw, err := json.Marshal(studentItems)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, forbidden := range []string{"private-id", "teacher-only", `"visibility"`, `"payload"`, `"created_by"`, `"revision"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("student DTO leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestMemoryStudentQuestionAnnotationsAreScopedAndPrivateAnnotationsNeverPersist(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	store.SetStudentQuestionAnnotations("tenant-a", "exam-a", "student-a", "question-a", []Annotation{
		{ID: "private", Visibility: VisibilityPrivate, Content: "teacher-only"},
		{ID: "visible", Visibility: VisibilityStudentAfterPublish, Content: "public", Type: AnnotationNote},
	})
	items, err := store.ListStudentQuestionAnnotations(ctx, "tenant-a", "exam-a", "student-a", "question-a")
	if err != nil || len(items) != 1 || items[0].ID != "visible" || items[0].Content != "public" {
		t.Fatalf("student annotations = %#v, %v", items, err)
	}
	other, err := store.ListStudentQuestionAnnotations(ctx, "tenant-a", "exam-a", "student-b", "question-a")
	if err != nil || len(other) != 0 {
		t.Fatalf("other student annotations = %#v, %v", other, err)
	}
}

func TestCommentTemplateShortcutUsageAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateCommentTemplate(ctx, "tenant-a", "reviewer-1", CreateCommentTemplateInput{
		Title: "Clear method", Content: "Method is clear and complete.", Shortcut: " GOOD.METHOD ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Shortcut != "good.method" {
		t.Fatalf("shortcut = %q", created.Shortcut)
	}
	if _, err := store.CreateCommentTemplate(ctx, "tenant-a", "reviewer-1", CreateCommentTemplateInput{
		Title: "Duplicate", Content: "Duplicate shortcut", Shortcut: "good.method",
	}); !errors.Is(err, ErrShortcutConflict) {
		t.Fatalf("duplicate shortcut error = %v", err)
	}
	if _, err := store.CreateCommentTemplate(ctx, "tenant-b", "reviewer-1", CreateCommentTemplateInput{
		Title: "Other tenant", Content: "Same shortcut is isolated", Shortcut: "good.method",
	}); err != nil {
		t.Fatalf("cross-tenant shortcut should be allowed: %v", err)
	}
	used, err := store.UseCommentTemplate(ctx, "tenant-a", "reviewer-1", "GOOD.METHOD")
	if err != nil || used.UsageCount != 1 || used.Revision != created.Revision+1 {
		t.Fatalf("used template = %#v, %v", used, err)
	}
	if _, err := store.GetCommentTemplate(ctx, "tenant-a", "reviewer-2", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read error = %v", err)
	}
}

var _ Store = (*MemoryStore)(nil)
var _ Store = (*PostgresStore)(nil)
