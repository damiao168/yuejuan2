package reviewannotation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 学生接口同时验证身份范围和 DTO 脱敏；测试数据包含私有批注以防止过滤回归。
func TestListStudentQuestionAnnotationsRequiresStudentScopeAndReturnsSafeDTO(t *testing.T) {
	store := NewMemoryStore()
	store.SetStudentQuestionAnnotations("tenant-a", "exam-a", "student-a", "question-a", []Annotation{
		{ID: "private", Visibility: VisibilityPrivate, Content: "teacher-only"},
		{ID: "visible", Visibility: VisibilityStudentAfterPublish, Content: "public", Type: AnnotationNote},
	})
	handler := NewHandler(store, nil)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/student/exams/exam-a/questions/question-a/annotations", nil)
	request.SetPathValue("examId", "exam-a")
	request.SetPathValue("questionId", "question-a")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{
		TenantID: "tenant-a", Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-a"},
	}))
	recorder := httptest.NewRecorder()
	handler.ListStudentQuestionAnnotations(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Annotations []StudentAnnotation `json:"annotations"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Annotations) != 1 || payload.Annotations[0].ID != "visible" || payload.Annotations[0].Content != "public" {
		t.Fatalf("annotations = %#v", payload.Annotations)
	}

	forbidden := httptest.NewRequest(http.MethodGet, request.URL.String(), nil)
	forbidden.SetPathValue("examId", "exam-a")
	forbidden.SetPathValue("questionId", "question-a")
	forbidden = forbidden.WithContext(auth.WithUser(context.Background(), auth.User{TenantID: "tenant-a", Permissions: []string{"student:grade:read"}}))
	forbiddenRecorder := httptest.NewRecorder()
	handler.ListStudentQuestionAnnotations(forbiddenRecorder, forbidden)
	if forbiddenRecorder.Code != http.StatusForbidden {
		t.Fatalf("unscoped status = %d body=%s", forbiddenRecorder.Code, forbiddenRecorder.Body.String())
	}
}
