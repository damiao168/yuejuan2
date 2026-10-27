package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
)

type submittedHistoryBoundaryStore struct {
	*auth.MemoryStore
	reviews *MemoryStore
}

func (s submittedHistoryBoundaryStore) CanReadSubmittedReviewTask(ctx context.Context, tenant, task, reviewer string) (bool, error) {
	_, err := s.reviews.GetSubmittedGrade(ctx, tenant, task, reviewer)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

type submittedHistoryImageReader struct{}

func (submittedHistoryImageReader) ReadCropImage(context.Context, string, string) (binaryresource.Resource, error) {
	return binaryresource.Resource{ContentType: "image/png", Size: 13, Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBufferString("history-image")), nil
	}}, nil
}

func TestSubmittedReviewTaskRoutesKeepOwnHistoryReadOnly(t *testing.T) {
	reviews, task := submissionQualificationFixture(t, assessment.RiskR2)
	submitted, err := reviews.SubmitGrade(t.Context(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 0, ExpectedRevision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	identity := submittedHistoryBoundaryStore{MemoryStore: auth.NewMemoryStore(), reviews: reviews}
	identity.AddResourceBoundary("review_task", task.ID, auth.ResourceBoundary{TenantID: tenantID, AssignedTo: "reviewer-1"})
	handler := NewHandler(reviews, nil, submittedHistoryImageReader{}, nil)
	mux := http.NewServeMux()
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /api/v1/review-tasks/{id}/context", handler.GetTaskContext},
		{"GET /api/v1/review-tasks/{id}/workspace", handler.GetWorkspace},
		{"GET /api/v1/review-tasks/{id}/segment-image", handler.GetWorkspaceSegmentImage},
		{"POST /api/v1/review-tasks/{id}/renew", handler.RenewTaskClaim},
		{"PUT /api/v1/review-tasks/{id}/draft", handler.SaveDraft},
		{"POST /api/v1/review-tasks/{id}/submit", handler.SubmitGrade},
	} {
		mux.Handle(route.pattern, auth.RequireRequestResourceBoundary(identity)(auth.RequirePermission("review:work")(route.handler)))
	}
	// 模拟提交后活动分派集合已为空，真实对象边界中间件仍应允许本人回看而拒绝写入。
	for _, tc := range []struct {
		actor, method, action string
		want                  int
	}{
		{"reviewer-1", "GET", "context", 200},
		{"reviewer-1", "GET", "workspace", 200},
		{"reviewer-1", "GET", "segment-image", 200},
		{"reviewer-2", "GET", "context", 403},
		{"reviewer-2", "GET", "workspace", 403},
		{"reviewer-2", "GET", "segment-image", 403},
		{"reviewer-1", "POST", "renew", 403},
		{"reviewer-1", "PUT", "draft", 403},
		{"reviewer-1", "POST", "submit", 403},
	} {
		t.Run(tc.actor+" "+tc.method+" "+tc.action, func(t *testing.T) {
			user := auth.User{ID: tc.actor, TenantID: tenantID, Roles: []string{"grader"}, Permissions: []string{"review:work"}}
			scope := auth.AccessScope{TenantID: tenantID, ActorID: user.ID, AssignedOnly: true}
			request := httptest.NewRequest(tc.method, "/api/v1/review-tasks/"+task.ID+"/"+tc.action, strings.NewReader(`{}`))
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d: %s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == 200 && tc.action == "segment-image" && response.Body.String() != "history-image" {
				t.Fatal("submitted answer image was lost")
			}
			if tc.want == 200 && tc.action == "context" {
				var payload struct {
					Context TaskContext `json:"context"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Context.Claim.CanRenew || payload.Context.HumanGrade == nil || payload.Context.HumanGrade.ID != submitted.Grade.ID {
					t.Fatal("submitted context lost its formal score or became renewable")
				}
			}
		})
	}
	current, err := reviews.GetTask(t.Context(), tenantID, task.ID)
	if err != nil || current.Revision != submitted.Task.Revision || current.Status != submitted.Task.Status {
		t.Fatalf("read-only history mutated task: task=%#v err=%v", current, err)
	}
}
