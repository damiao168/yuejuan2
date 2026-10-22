package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func reviewBoundaryRouter(t *testing.T, stores ApplicationStores, user auth.User) http.Handler {
	t.Helper()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	identities.AddUser(auth.UserWithPassword{User: user})
	_, err := identities.CreateSession(context.Background(), auth.CreateSessionInput{TenantID: user.TenantID, UserID: user.ID, TokenHash: auth.HashToken("review-boundary-session"), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Service: config.ServiceConfig{Name: "review-boundary-test", Environment: "test"}, Security: config.SecurityConfig{MaxRequestBodyBytes: 2 << 20}, Observability: config.ObservabilityConfig{SlowRequestThreshold: time.Hour}}
	return NewRouterWithApplicationStores(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
}

func reviewBoundaryRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer review-boundary-session")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// 使用真实认证、中间件和校准服务；缺少 R3 资格的直接提交不能绕过领取拒绝，R2 保留正向对照。
func TestReviewR3QualificationCannotBeBypassedAfterRejectedClaim(t *testing.T) {
	for _, risk := range []assessment.RiskTier{assessment.RiskR3, assessment.RiskR2} {
		t.Run(string(risk), func(t *testing.T) {
			stores := NewMemoryApplicationStores()
			reviews := stores.Grading.Review.(*review.MemoryStore)
			reviews.AddContext("tenant-1", "segment-1", review.Context{
				ExamID: "exam-a", SubmissionID: "submission-1", AnonymousCode: "ANON-1", RawAnswer: "student answer",
				Question:           paper.Question{ID: "question-1", TenantID: "tenant-1", ExamID: "exam-a", QuestionNo: "Q1", QuestionType: "short_answer", Score: 5},
				AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "snapshot-1", TenantID: "tenant-1", ExamID: "exam-a", QuestionID: "question-1", RiskTier: risk, ScoringPolicySnapshot: assessment.ScoringPolicy{Mode: assessment.ScoringHumanPrimary}},
			})
			task, err := reviews.CreateTask(context.Background(), "tenant-1", "manager-1", review.CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "grader-1"})
			if err != nil {
				t.Fatal(err)
			}
			stores.Identity.Auth.(*auth.MemoryStore).AddResourceBoundary("review_task", task.ID, auth.ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-a", ExamID: "exam-a", AssignedTo: "grader-1"})
			user := auth.User{ID: "grader-1", TenantID: "tenant-1", Username: "grader", Status: "active", Roles: []string{"grader"}, Permissions: []string{"review:work"}, DataScope: map[string]any{"grader": map[string]any{"scope": "exam_task", "review_task_ids": []string{task.ID}}}}
			router := reviewBoundaryRouter(t, stores, user)
			claim := reviewBoundaryRequest(router, http.MethodPost, "/api/v1/review-tasks/next", `{}`)
			current, err := reviews.GetTask(context.Background(), "tenant-1", task.ID)
			if err != nil {
				t.Fatal(err)
			}
			response := reviewBoundaryRequest(router, http.MethodPost, "/api/v1/review-tasks/"+task.ID+"/submit", fmt.Sprintf(`{"score":4,"expected_revision":%d}`, current.Revision))
			if risk == assessment.RiskR3 {
				unchanged, _ := reviews.GetTask(context.Background(), "tenant-1", task.ID)
				if claim.Code != http.StatusForbidden || response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "grader_qualification_required") || unchanged.Revision != current.Revision || unchanged.Status != current.Status {
					t.Fatalf("claim=%d submit=%d task=%#v body=%s", claim.Code, response.Code, unchanged, response.Body.String())
				}
			} else if claim.Code != http.StatusOK || response.Code != http.StatusCreated {
				t.Fatalf("R2 control: claim=%d submit=%d body=%s", claim.Code, response.Code, response.Body.String())
			}
		})
	}
}

func TestReviewDoubleMarkCollectionsMatchSchoolAndTenantBoundary(t *testing.T) {
	for _, tenantAdmin := range []bool{false, true} {
		t.Run(fmt.Sprintf("tenant_admin=%t", tenantAdmin), func(t *testing.T) {
			stores := NewMemoryApplicationStores()
			reviews := stores.Grading.Review.(*review.MemoryStore)
			identities := stores.Identity.Auth.(*auth.MemoryStore)
			var foreignSession string
			for _, suffix := range []string{"a", "b"} {
				exam, question, segment := "exam-"+suffix, "question-"+suffix, "segment-"+suffix
				reviews.AddContext("tenant-1", segment, review.Context{ExamID: exam, SubmissionID: "submission-" + suffix, AnonymousCode: "PRIVATE-" + suffix, Question: paper.Question{ID: question, TenantID: "tenant-1", ExamID: exam, QuestionNo: "Q1", QuestionType: "short_answer", Score: 5}})
				if _, err := reviews.SetExamDoubleMarkPolicy(context.Background(), "tenant-1", exam, "manager", review.SetDoubleMarkPolicyInput{Enabled: true, Threshold: 1, ResolutionStrategy: "average"}); err != nil {
					t.Fatal(err)
				}
				session, err := reviews.CreateDoubleMarkSession(context.Background(), "tenant-1", "manager", review.CreateDoubleMarkSessionInput{AnswerSegmentID: segment, FirstReviewerID: "first", SecondReviewerID: "second"})
				if err != nil {
					t.Fatal(err)
				}
				boundary := auth.ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-" + suffix, ExamID: exam}
				identities.AddResourceBoundary("exam", exam, boundary)
				identities.AddResourceBoundary("double_mark_session", session.ID, boundary)
				if suffix == "b" {
					foreignSession = session.ID
				}
			}
			user := auth.User{ID: "manager", TenantID: "tenant-1", Username: "manager", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
			if tenantAdmin {
				user.Roles = []string{"tenant_admin"}
				user.DataScope = map[string]any{"tenant_admin": map[string]any{"scope": "tenant"}}
			}
			router := reviewBoundaryRouter(t, stores, user)
			direct := reviewBoundaryRequest(router, http.MethodGet, "/api/v1/double-mark-sessions/"+foreignSession, "")
			wantDirect := http.StatusForbidden
			if tenantAdmin {
				wantDirect = http.StatusOK
			}
			if direct.Code != wantDirect {
				t.Fatalf("direct=%d %s", direct.Code, direct.Body.String())
			}
			for _, endpoint := range []struct{ path, key string }{{"/api/v1/double-mark-sessions", "double_mark_sessions"}, {"/api/v1/double-mark-policies", "policies"}} {
				for _, query := range []string{"", "?exam_id=exam-b"} {
					response := reviewBoundaryRequest(router, http.MethodGet, endpoint.path+query, "")
					var result map[string][]json.RawMessage
					if err := json.Unmarshal(response.Body.Bytes(), &result); response.Code != http.StatusOK || err != nil {
						t.Fatalf("collection=%d %s err=%v", response.Code, response.Body.String(), err)
					}
					want := 1
					if tenantAdmin && query == "" {
						want = 2
					} else if !tenantAdmin && query != "" {
						want = 0
					}
					if len(result[endpoint.key]) != want || !tenantAdmin && strings.Contains(response.Body.String(), `"exam_id":"exam-b"`) {
						t.Fatalf("%s%s count=%d want=%d body=%s", endpoint.path, query, len(result[endpoint.key]), want, response.Body.String())
					}
				}
			}
		})
	}
}
