package reviewannotation

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStudentAnnotationsChecksReleaseEvenWhenThereAreNoAnnotations(t *testing.T) {
	store := NewMemoryStore()
	store.SetStudentPublishedRelease("tenant-a", "exam-a", "release-new")
	ctx := context.Background()
	if _, err := store.ListStudentQuestionAnnotations(ctx, "tenant-a", "exam-a", "student-a", "question-a", "release-old"); !errors.Is(err, ErrStudentReleaseChanged) {
		t.Fatalf("old empty annotations: %v", err)
	}
	for _, expected := range [][]string{nil, {"release-new"}} {
		items, err := store.ListStudentQuestionAnnotations(ctx, "tenant-a", "exam-a", "student-a", "question-a", expected...)
		if err != nil || len(items) != 0 {
			t.Fatalf("matching/legacy: %+v %v", items, err)
		}
	}
	handler := NewHandler(store, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/student/exams/exam-a/questions/question-a/annotations?expected_release_id=release-old", nil)
	request.SetPathValue("examId", "exam-a")
	request.SetPathValue("questionId", "question-a")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{TenantID: "tenant-a", Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-a"}}))
	recorder := httptest.NewRecorder()
	handler.ListStudentQuestionAnnotations(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "student_release_changed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
