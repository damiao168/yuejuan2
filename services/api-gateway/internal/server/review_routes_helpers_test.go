package server

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type reviewRouteScopeSegments struct{ segment.Store }

func (s reviewRouteScopeSegments) GetEvidence(_ context.Context, tenant, id string) (segment.SegmentEvidence, error) {
	if tenant != reviewTenantID || (id != "segment-1" && id != "segment-2") {
		return segment.SegmentEvidence{}, segment.ErrNotFound
	}
	return segment.SegmentEvidence{SegmentID: id, ExamID: "exam-1", QuestionID: "question-1", NormalizedBBox: map[string]any{"answer_scope": "question"}}, nil
}

func reviewRouter(authStore *auth.MemoryStore, reviewStore review.Store, segments ...segment.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return NewMemoryRouter(cfg, loggerForReviewTest(), nil, files.NewMemoryObjectStorage(), func(stores *ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Grading.Review = reviewStore
		if len(segments) > 0 {
			stores.Exam.Segments = segments[0]
		}
	})
}

func createReviewTaskForRoute(t *testing.T, router http.Handler, token string, segmentID string, source string) string {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks", `{"answer_segment_id":"`+segmentID+`","source":"`+source+`"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review task expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	return decodeReviewTaskID(t, rec.Body.Bytes())
}

func loggerForReviewTest() *logger.Logger {
	return logger.New(io.Discard, "error")
}

func reviewAuthStore(t *testing.T, permissions []string) *auth.MemoryStore {
	return reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":          permissions,
		"review_grader":           permissions,
		"review_second_grader":    permissions,
		"review_arbitrator":       permissions,
		"review_other_arbitrator": permissions,
	})
}

func reviewAuthStoreByUser(t *testing.T, permissionsByUsername map[string][]string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	for _, user := range []auth.User{
		{
			ID:          reviewManagerID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_manager",
			DisplayName: "Review Manager",
			Status:      "active",
			Roles:       []string{"school_admin"},
			DataScope:   map[string]any{"scope": "school", "school_id": "school-1", "synthetic": true},
		},
		{
			ID:          reviewGraderID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_grader",
			DisplayName: "Review Grader",
			Status:      "active",
			Roles:       []string{"grader"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewSecondGraderID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_second_grader",
			DisplayName: "Review Second Grader",
			Status:      "active",
			Roles:       []string{"grader"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewArbitratorID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_arbitrator",
			DisplayName: "Review Arbitrator",
			Status:      "active",
			Roles:       []string{"arbitrator"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewOtherArbitratorID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_other_arbitrator",
			DisplayName: "Review Other Arbitrator",
			Status:      "active",
			Roles:       []string{"arbitrator"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
	} {
		if scopedPermissions, ok := permissionsByUsername[user.Username]; ok {
			user.Permissions = scopedPermissions
		} else {
			user.Permissions = nil
		}
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}

// 列表用本校考试边界筛选任务，内存夹具也需要登记考试所属学校。
func addReviewManagedExam(store *auth.MemoryStore, examID string) {
	store.AddResourceBoundary("exam", examID, auth.ResourceBoundary{TenantID: reviewTenantID, SchoolID: "school-1", ExamID: examID})
}

func addReviewManagedUser(t *testing.T, store *auth.MemoryStore, user auth.User) {
	t.Helper()
	if len(user.DataScope) == 0 {
		switch {
		case slices.Contains(user.Roles, "tenant_admin"), slices.Contains(user.Roles, "platform_admin"):
			user.DataScope = map[string]any{"scope": "tenant"}
		case slices.Contains(user.Roles, "school_admin"), slices.Contains(user.Roles, "teacher"):
			user.DataScope = map[string]any{"scope": "school", "school_id": "school-1"}
		case slices.Contains(user.Roles, "grader"), slices.Contains(user.Roles, "arbitrator"):
			user.DataScope = map[string]any{"scope": "assigned"}
		default:
			user.DataScope = map[string]any{"scope": "none"}
		}
	}
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
}

func reviewLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": username, "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func reviewAuthedRequest(method string, path string, body string, token string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func decodeReviewTaskID(t *testing.T, raw []byte) string {
	t.Helper()
	var response struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if response.Task.ID == "" {
		t.Fatalf("missing task id in %s", string(raw))
	}
	return response.Task.ID
}

func assignReviewTaskForRoute(t *testing.T, router http.Handler, token string, taskID string, assignedTo string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", `{"assigned_to":"`+assignedTo+`","expected_revision":1}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign review task expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func decodeDoubleMarkSession(t *testing.T, raw []byte) struct {
	ID                 string `json:"id"`
	FirstReviewTaskID  string `json:"first_review_task_id"`
	SecondReviewTaskID string `json:"second_review_task_id"`
} {
	t.Helper()
	var response struct {
		DoubleMarkSession struct {
			ID                 string `json:"id"`
			FirstReviewTaskID  string `json:"first_review_task_id"`
			SecondReviewTaskID string `json:"second_review_task_id"`
		} `json:"double_mark_session"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode double mark session: %v", err)
	}
	if response.DoubleMarkSession.ID == "" || response.DoubleMarkSession.FirstReviewTaskID == "" || response.DoubleMarkSession.SecondReviewTaskID == "" {
		t.Fatalf("missing double mark session ids in %s", string(raw))
	}
	return response.DoubleMarkSession
}

func decodeArbitrationIDFromSubmit(t *testing.T, raw []byte) string {
	t.Helper()
	var response struct {
		ArbitrationTask struct {
			ID string `json:"id"`
		} `json:"arbitration_task"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode arbitration task: %v", err)
	}
	if response.ArbitrationTask.ID == "" {
		t.Fatalf("missing arbitration id in %s", string(raw))
	}
	return response.ArbitrationTask.ID
}

func reviewRouteContext() review.Context {
	return review.Context{
		ExamID:          "exam-1",
		SubmissionID:    "submission-1",
		AnswerSegmentID: "segment-1",
		AnonymousCode:   "ANON-001",
		Question: paper.Question{
			ID:           "question-1",
			TenantID:     reviewTenantID,
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: "short_answer",
			Score:        5,
		},
		Rubric: paper.Rubric{
			ID:         "rubric-1",
			QuestionID: "question-1",
			Version:    "v1",
			Status:     "approved",
			MaxScore:   5,
			Points: []paper.RubricPoint{
				{ID: "p1", Description: "main idea", Score: 5, Required: true},
			},
		},
		RawAnswer: "student wrote the main idea",
		OCRText:   "student wrote the main idea",
		AISuggestion: map[string]any{
			"suggested_score": 4,
			"mock":            true,
		},
	}
}

func reviewAssertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}
