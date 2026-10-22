package review

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type gradingGroupTaskStoreStub struct {
	*MemoryStore
	rows  []ReviewTask
	err   error
	calls int
}

func (s *gradingGroupTaskStoreStub) ListGradingGroupTasks(context.Context, string, string, string) ([]ReviewTask, error) {
	s.calls++
	return s.rows, s.err
}

type gradingGroupTaskImageStub struct {
	groups map[string]*segment.GradingGroupCrop
	errors map[string]error
}

func (r *gradingGroupTaskImageStub) GetGradingGroup(_ context.Context, _, id string) (*segment.GradingGroupCrop, error) {
	return r.groups[id], r.errors[id]
}

func (*gradingGroupTaskImageStub) ReadGradingGroupImage(context.Context, string, string) (binaryresource.Resource, error) {
	return binaryresource.Resource{}, segment.ErrEvidenceUnavailable
}

func (*gradingGroupTaskImageStub) ReadCropImage(context.Context, string, string) (binaryresource.Resource, error) {
	return binaryresource.Resource{}, segment.ErrEvidenceUnavailable
}

func gradingGroupTaskFixture() (ReviewTask, *segment.GradingGroupCrop, *gradingGroupTaskStoreStub, *gradingGroupTaskImageStub) {
	anchor := ReviewTask{ID: "task10", TenantID: "tenant", ExamID: "exam", QuestionID: "q10", QuestionNo: "10",
		AnswerSegmentID: "segment10", SubmissionID: "submission", AssignedTo: "reviewer", GradeRound: "first",
		Status: "in_progress", Revision: 4}
	group := &segment.GradingGroupCrop{ID: "fill-group", QuestionIDs: []string{"q2", "q10", "q12"},
		FileAssetID: "shared-asset", SHA256: strings.Repeat("a", 64)}
	second, twelfth := anchor, anchor
	second.ID, second.QuestionID, second.QuestionNo, second.AnswerSegmentID = "task2", "q2", "2", "segment2"
	second.Status, second.Revision = "completed", 8
	twelfth.ID, twelfth.QuestionID, twelfth.QuestionNo, twelfth.AnswerSegmentID = "task12", "q12", "12", "segment12"
	store := &gradingGroupTaskStoreStub{MemoryStore: NewMemoryStore(), rows: []ReviewTask{anchor, twelfth, second}}
	images := &gradingGroupTaskImageStub{groups: map[string]*segment.GradingGroupCrop{
		"segment2": group, "segment10": group, "segment12": group}, errors: map[string]error{}}
	return anchor, group, store, images
}

func TestGradingGroupTaskRefsKeepIndependentTasksInNaturalQuestionOrder(t *testing.T) {
	anchor, group, store, images := gradingGroupTaskFixture()
	refs := NewHandler(store, nil, images, nil).gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group)
	want := []GradingGroupTaskRef{
		{ID: "task2", QuestionID: "q2", QuestionNo: "2", Status: "completed", Revision: 8},
		{ID: "task10", QuestionID: "q10", QuestionNo: "10", Status: "in_progress", Revision: 4},
		{ID: "task12", QuestionID: "q12", QuestionNo: "12", Status: "in_progress", Revision: 4},
	}
	if !reflect.DeepEqual(refs, want) || store.calls != 1 {
		t.Fatalf("group task references = %#v, calls = %d", refs, store.calls)
	}
}

func TestGradingGroupTaskRefsSortSubquestionNumbersNaturally(t *testing.T) {
	anchor, group, store, images := gradingGroupTaskFixture()
	store.rows[0].QuestionNo, store.rows[1].QuestionNo, store.rows[2].QuestionNo = "2(10)", "10", "2(2)"
	refs := NewHandler(store, nil, images, nil).gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group)
	if len(refs) != 3 || refs[0].QuestionNo != "2(2)" || refs[1].QuestionNo != "2(10)" || refs[2].QuestionNo != "10" {
		t.Fatalf("subquestion order = %#v", refs)
	}
}

func TestGradingGroupTaskRefsRejectDifferentTaskAndAssetScopes(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ReviewTask, *segment.GradingGroupCrop)
	}{
		{"tenant", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.TenantID = "other-tenant" }},
		{"student", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.SubmissionID = "other-submission" }},
		{"exam", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.ExamID = "other-exam" }},
		{"teacher", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.AssignedTo = "other-reviewer" }},
		{"round", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.GradeRound = "second" }},
		{"outside group", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.QuestionID = "q99" }},
		{"cancelled", func(task *ReviewTask, _ *segment.GradingGroupCrop) { task.Status = "cancelled" }},
		{"group id", func(_ *ReviewTask, group *segment.GradingGroupCrop) { group.ID = "other-group" }},
		{"asset", func(_ *ReviewTask, group *segment.GradingGroupCrop) { group.FileAssetID = "other-asset" }},
		{"asset hash", func(_ *ReviewTask, group *segment.GradingGroupCrop) { group.SHA256 = strings.Repeat("b", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			anchor, group, store, images := gradingGroupTaskFixture()
			other := *group
			test.change(&store.rows[2], &other)
			images.groups["segment2"] = &other
			refs := NewHandler(store, nil, images, nil).gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group)
			if len(refs) != 2 || refs[0].QuestionID != "q10" || refs[1].QuestionID != "q12" {
				t.Fatalf("out-of-scope task entered shared region: %#v", refs)
			}
		})
	}
}

func TestGradingGroupTaskRefsRequireAuthorizedAnchorAndAvailableReaders(t *testing.T) {
	for _, scope := range []string{"tenant", "teacher", "group", "image reader"} {
		t.Run(scope, func(t *testing.T) {
			anchor, group, store, images := gradingGroupTaskFixture()
			handler := NewHandler(store, nil, images, nil)
			switch scope {
			case "tenant":
				anchor.TenantID = "other-tenant"
			case "teacher":
				anchor.AssignedTo = "other-reviewer"
			case "group":
				group = nil
			case "image reader":
				handler.segmentImage = nil
			}
			if refs := handler.gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group); len(refs) != 0 || store.calls != 0 {
				t.Fatalf("unavailable anchor read siblings: refs = %#v, calls = %d", refs, store.calls)
			}
		})
	}
}

func TestGradingGroupTaskRefsReadFailuresDoNotExposeUnverifiedSiblings(t *testing.T) {
	anchor, group, store, images := gradingGroupTaskFixture()
	handler := NewHandler(store, nil, images, nil)
	store.err = errors.New("task lookup failed")
	if refs := handler.gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group); len(refs) != 0 {
		t.Fatalf("failed task lookup returned siblings: %#v", refs)
	}
	store.err = nil
	images.errors["segment2"] = errors.New("asset lookup failed")
	images.groups["segment12"] = nil
	if refs := handler.gradingGroupTaskRefs(t.Context(), "tenant", "reviewer", anchor, group); len(refs) != 1 || refs[0].ID != anchor.ID {
		t.Fatalf("unverified asset returned siblings: %#v", refs)
	}
}

func TestMemoryGradingGroupTasksStayWithAnchorStudentTeacherAndRound(t *testing.T) {
	anchor, _, fixture, _ := gradingGroupTaskFixture()
	store := NewMemoryStore()
	for _, task := range fixture.rows {
		store.tasks[key(task.TenantID, task.ID)] = task
	}
	for index, change := range []func(*ReviewTask){
		func(task *ReviewTask) { task.TenantID = "other-tenant" },
		func(task *ReviewTask) { task.SubmissionID = "other-submission" },
		func(task *ReviewTask) { task.ExamID = "other-exam" },
		func(task *ReviewTask) { task.AssignedTo = "other-reviewer" },
		func(task *ReviewTask) { task.GradeRound = "second" },
		func(task *ReviewTask) { task.Status = "cancelled" },
	} {
		other := anchor
		other.ID = "outside-" + string(rune('a'+index))
		change(&other)
		store.tasks[key(other.TenantID, other.ID)] = other
	}
	tasks, err := store.ListGradingGroupTasks(t.Context(), "tenant", anchor.ID, "reviewer")
	if err != nil || len(tasks) != 3 {
		t.Fatalf("anchor task collection = %#v, err = %v", tasks, err)
	}
	for _, task := range tasks {
		if strings.HasPrefix(task.ID, "outside-") {
			t.Fatalf("out-of-scope task collected: %#v", task)
		}
	}
	for _, request := range [][2]string{{"other-tenant", "reviewer"}, {"tenant", "other-reviewer"}} {
		if _, err := store.ListGradingGroupTasks(t.Context(), request[0], anchor.ID, request[1]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unassigned anchor lookup = %v", err)
		}
	}
}
