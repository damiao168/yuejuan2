package review

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type reviewGradingGroupReader struct{ groupCalls, childCalls, contextCalls int }

func (r *reviewGradingGroupReader) GetGradingGroup(context.Context, string, string) (*segment.GradingGroupCrop, error) {
	r.contextCalls++
	return &segment.GradingGroupCrop{ID: "fill-12-14", QuestionIDs: []string{"q12", "q13", "q14"}}, nil
}
func (r *reviewGradingGroupReader) ReadGradingGroupImage(context.Context, string, string) (binaryresource.Resource, error) {
	r.groupCalls++
	return binaryresource.Resource{ContentType: "image/png", Size: 6, Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBufferString("shared")), nil
	}}, nil
}
func (r *reviewGradingGroupReader) ReadCropImage(context.Context, string, string) (binaryresource.Resource, error) {
	r.childCalls++
	return binaryresource.Resource{}, segment.ErrEvidenceUnavailable
}

func TestReviewConfiguredGradingGroupHasAuthorizedImageAndIndependentQuestion(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	task, err := store.CreateTask(t.Context(), tenantID, "manager", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &reviewGradingGroupReader{}
	handler := NewHandler(store, nil, reader, nil)
	user := auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+task.ID+"/context", nil)
	request.SetPathValue("id", task.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.GetTaskContext(response, request)
	var body struct {
		Context TaskContext `json:"context"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("context unavailable: %d %s", response.Code, response.Body)
	}
	artifact := body.Context.AnswerArtifact
	if artifact.GradingGroupID != "fill-12-14" || !strings.Contains(artifact.GradingGroupImageURL, "scope=grading_group") || len(artifact.GradingGroupQuestionIDs) != 3 || body.Context.Task.QuestionID != task.QuestionID {
		t.Fatalf("shared context replaced question: %+v", body.Context)
	}
	image := httptest.NewRequest(http.MethodGet, artifact.GradingGroupImageURL, nil)
	image.SetPathValue("id", task.ID)
	image = image.WithContext(auth.WithUser(image.Context(), user))
	response = httptest.NewRecorder()
	handler.GetWorkspaceSegmentImage(response, image)
	if response.Code != http.StatusOK || response.Body.String() != "shared" || reader.groupCalls != 1 || reader.childCalls != 0 {
		t.Fatalf("wrong crop selected: status=%d group=%d child=%d", response.Code, reader.groupCalls, reader.childCalls)
	}
	denied := image.Clone(auth.WithUser(context.Background(), auth.User{ID: "another-teacher", TenantID: tenantID, Roles: []string{"grader"}}))
	response = httptest.NewRecorder()
	handler.GetWorkspaceSegmentImage(response, denied)
	if response.Code != http.StatusForbidden || reader.groupCalls != 1 {
		t.Fatal("unassigned teacher could read shared image")
	}
}
