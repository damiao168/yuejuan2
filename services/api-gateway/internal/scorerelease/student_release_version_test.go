package scorerelease

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 先公开旧版再切换当前指针；版本条件须保护题目、裁切图和整页图，省略条件仍兼容既有客户端。
func TestStudentEvidenceRejectsChangedReleaseAndAcceptsMatchingOrLegacyRead(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store)
	publish := func(key string, score float64) Release {
		store.SeedFacts(exam, []SubmissionFact{fixtureFact(score)})
		release, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "release consistency", IdempotencyKey: key, VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Publish(ctx, tenant, release.ID, actor); err != nil {
			t.Fatal(err)
		}
		return release
	}
	first := publish("student-release-version-1", 4)
	store.SeedStudentQuestionImage(tenant, exam, "student-1", "question-1", "segment-1")
	second := publish("student-release-version-2", 5)
	if _, err := service.StudentQuestion(ctx, tenant, exam, "student-1", "question-1", first.ID); !errors.Is(err, ErrStudentReleaseChanged) {
		t.Fatalf("old question: %v", err)
	}
	if _, err := service.StudentQuestionImage(ctx, tenant, exam, "student-1", "question-1", first.ID); !errors.Is(err, ErrStudentReleaseChanged) {
		t.Fatalf("old crop: %v", err)
	}
	if _, err := service.StudentPaperPageImage(ctx, tenant, exam, "student-1", "question-1", false, first.ID); !errors.Is(err, ErrStudentReleaseChanged) {
		t.Fatalf("old page: %v", err)
	}
	for _, expected := range [][]string{nil, {second.ID}} {
		question, err := service.StudentQuestion(ctx, tenant, exam, "student-1", "question-1", expected...)
		if err != nil || question.Score != 5 {
			t.Fatalf("matching/legacy: %+v %v", question, err)
		}
		image, err := service.StudentQuestionImage(ctx, tenant, exam, "student-1", "question-1", expected...)
		if err != nil || image.ReleaseID != second.ID {
			t.Fatalf("image: %+v %v", image, err)
		}
	}
	handler := NewHandler(service, nil)
	for _, route := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"questions/question-1", handler.StudentQuestion},
		{"questions/question-1/answer-image", handler.StudentQuestionImage},
		{"questions/question-1/page-image", handler.StudentPaperPageImage},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/student/exams/exam-1/"+route.path+"?expected_release_id="+first.ID, nil)
		request.SetPathValue("examId", exam)
		request.SetPathValue("questionId", "question-1")
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{TenantID: tenant, Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-1"}}))
		recorder := httptest.NewRecorder()
		route.handle(recorder, request)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "student_release_changed") {
			t.Fatalf("%s: %d %s", route.path, recorder.Code, recorder.Body.String())
		}
	}
}
