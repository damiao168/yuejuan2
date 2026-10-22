package reviewbookmark

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type bookmarkMemoryIdentity struct {
	user auth.UserWithPassword
	exam exam.Exam
}

func TestMemoryBookmarkEvidenceRequiresMatchingFrozenSnapshot(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*review.Context)
	}{
		{"missing", func(c *review.Context) { c.AssessmentSnapshot = assessment.ExamQuestionSnapshot{} }},
		{"missing_id", func(c *review.Context) { c.AssessmentSnapshot.ID = "" }},
		{"other_tenant", func(c *review.Context) { c.AssessmentSnapshot.TenantID = "other-tenant" }},
		{"other_exam", func(c *review.Context) { c.AssessmentSnapshot.ExamID = "other-exam" }},
		{"other_question_same_number", func(c *review.Context) { c.AssessmentSnapshot.QuestionID = "other-question" }},
		{"question_other_exam", func(c *review.Context) { c.Question.ExamID = "other-exam" }},
		{"question_other_tenant", func(c *review.Context) { c.Question.TenantID = "other-tenant" }},
	}
	for _, seed := range []bool{false, true} {
		kind := "ordinary"
		if seed {
			kind = "seed"
		}
		t.Run(kind, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()
					user := testUser()
					tasks, provider, taskID, segmentID, material := memoryBookmarkEvidenceFixture(t, user, seed)
					store := NewMemoryStore(provider)
					ack, err := store.Put(ctx, user, taskID, PutInput{Reason: "待复查"})
					if err != nil {
						t.Fatal(err)
					}
					record, err := store.GetByID(ctx, user, ack.BookmarkID)
					if err != nil {
						t.Fatal(err)
					}
					evidence := NewEvidenceService(provider, nil, []byte("synthetic-memory-secret"))
					if _, err = evidence.Context(ctx, user, record); err != nil {
						t.Fatalf("valid frozen context: %v", err)
					}
					// 保留已标记的任务和相同题号，只替换冻结依据；回看必须失败，个人标记仍可维护。
					tc.mutate(&material)
					tasks.AddContext(user.TenantID, segmentID, material)
					if _, err = evidence.Context(ctx, user, record); !errors.Is(err, ErrEvidenceUnavailable) {
						t.Fatalf("unbound frozen context accepted: %v", err)
					}
					if _, err = evidence.Image(ctx, user, record, "old-version"); !errors.Is(err, ErrEvidenceUnavailable) {
						t.Fatalf("unbound frozen image accepted: %v", err)
					}
					if state, err := store.State(ctx, user, taskID); err != nil || !state.Marked || state.Revision != ack.Revision {
						t.Fatalf("missing evidence must not hide bookmark state: %+v %v", state, err)
					}
					if _, err = store.Put(ctx, user, taskID, PutInput{Reason: "仅更新个人原因", ExpectedRevision: ack.Revision}); err != nil {
						t.Fatalf("missing evidence must not prevent reason edit: %v", err)
					}
					fresh := NewMemoryStore(provider)
					if _, err = fresh.Put(ctx, user, taskID, PutInput{}); err != nil {
						t.Fatalf("missing evidence must not prevent initial bookmark: %v", err)
					}
				})
			}
		})
	}
}

func TestMemorySeedBookmarkEvidenceDoesNotSubstituteAnotherSnapshot(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	tasks, provider, taskID, segmentID, material := memoryBookmarkEvidenceFixture(t, user, true)
	material.AssessmentSnapshot.ID = "another-snapshot"
	tasks.AddContext(user.TenantID, segmentID, material)
	if _, _, err := provider.ResolveEvidence(ctx, user, taskID); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatalf("seed must keep its exact frozen snapshot: %v", err)
	}
	if _, err := provider.Resolve(ctx, user, taskID); err != nil {
		t.Fatalf("snapshot mismatch only restricts evidence: %v", err)
	}
}

func TestMemoryBookmarkRejectsReplacedTaskContext(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*review.Context)
	}{
		{"exam", func(c *review.Context) { c.ExamID = "other-exam" }},
		{"submission", func(c *review.Context) { c.SubmissionID = "other-submission" }},
		{"question_same_number", func(c *review.Context) { c.Question.ID = "other-question" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := testUser()
			tasks, provider, taskID, segmentID, material := memoryBookmarkEvidenceFixture(t, user, false)
			tc.mutate(&material)
			tasks.AddContext(user.TenantID, segmentID, material)
			if _, err := provider.Resolve(context.Background(), user, taskID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("replaced task source accepted: %v", err)
			}
		})
	}
}

func memoryBookmarkEvidenceFixture(t *testing.T, user auth.User, seed bool) (*review.MemoryStore, *MemorySourceProvider, string, string, review.Context) {
	t.Helper()
	ctx := context.Background()
	segments := segment.NewMemoryStore()
	items, err := segments.CreateSegments(ctx, []segment.CreateSegmentInput{{TenantID: user.TenantID, SubmissionID: "submission", SubmissionPageID: "page", QuestionID: "question", QuestionNo: "Q1", BBox: []float64{0, 0, .5, .5}, Source: "manual", Status: "accepted"}})
	if err != nil {
		t.Fatal(err)
	}
	material := review.Context{
		ExamID: "exam", SubmissionID: "submission",
		Question:           paper.Question{ID: "question", TenantID: user.TenantID, ExamID: "exam", QuestionNo: "Q1", Stem: "同一题号不能代替冻结绑定", Score: 5},
		AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "snapshot", TenantID: user.TenantID, ExamID: "exam", QuestionID: "question"},
	}
	tasks := review.NewMemoryStore()
	tasks.AddContext(user.TenantID, items[0].ID, material)
	task, err := tasks.CreateTask(ctx, user.TenantID, user.ID, review.CreateTaskInput{AnswerSegmentID: items[0].ID, Source: "manual_sample", AssignedTo: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	seeds := seedquality.NewMemoryStore()
	if seed {
		seedTask, issued, err := seeds.AdvanceAndMaybeCreate(ctx, user.TenantID, seedquality.IssueDecision{
			Policy:   seedquality.Policy{ExamID: "exam", QuestionID: "question", Status: seedquality.PolicyActive, Rate: 1, MinInterval: 1},
			GraderID: user.ID, QuestionNo: "Q1", NextInterval: 1,
			Gold: seedquality.GoldSample{GoldPaperID: "gold", GoldVersion: 1, SnapshotID: "snapshot", MaxScore: 5, AnswerImageURL: "/api/v1/answer-segments/" + items[0].ID + "/image"},
		})
		if err != nil || !issued {
			t.Fatalf("create seed: %+v %v", seedTask, err)
		}
		taskID = seedTask.ID
	}
	identity := bookmarkMemoryIdentity{user: auth.UserWithPassword{User: user, SchoolID: "school"}, exam: exam.Exam{ID: "exam", TenantID: user.TenantID, SchoolID: "school", Status: "ready"}}
	return tasks, NewMemorySourceProvider(tasks, seeds, identity, identity, identity, segments), taskID, items[0].ID, material
}

func (f bookmarkMemoryIdentity) FindUserByID(_ context.Context, tenantID, userID string) (auth.UserWithPassword, error) {
	if f.user.TenantID != tenantID || f.user.ID != userID {
		return auth.UserWithPassword{}, auth.ErrManagedUserNotFound
	}
	return f.user, nil
}
func (f bookmarkMemoryIdentity) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	if f.exam.TenantID != scope.TenantID || f.exam.ID != id {
		return exam.Exam{}, exam.ErrNotFound
	}
	return f.exam, nil
}
func (f bookmarkMemoryIdentity) ListSchools(_ context.Context, tenantID string) ([]org.School, error) {
	return []org.School{{ID: f.user.SchoolID, TenantID: tenantID, Status: "active"}}, nil
}

func TestMemoryBookmarkSourceKeepsActualSegmentWithoutInventingImage(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	segments := segment.NewMemoryStore()
	items, err := segments.CreateSegments(ctx, []segment.CreateSegmentInput{{TenantID: user.TenantID, SubmissionID: "submission", SubmissionPageID: "page", QuestionID: "question", QuestionNo: "Q1", BBox: []float64{0, 0, .5, .5}, Source: "manual", Status: "accepted"}})
	if err != nil {
		t.Fatal(err)
	}
	tasks := review.NewMemoryStore()
	tasks.AddContext(user.TenantID, items[0].ID, review.Context{ExamID: "exam", SubmissionID: "submission", Question: paper.Question{ID: "question", ExamID: "exam", Score: 5}, AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "snapshot", TenantID: user.TenantID, ExamID: "exam", QuestionID: "question"}})
	task, err := tasks.CreateTask(ctx, user.TenantID, user.ID, review.CreateTaskInput{AnswerSegmentID: items[0].ID, Source: "manual_sample", AssignedTo: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	identity := bookmarkMemoryIdentity{user: auth.UserWithPassword{User: user, SchoolID: "school"}, exam: exam.Exam{ID: "exam", TenantID: user.TenantID, SchoolID: "school", Status: "ready"}}
	provider := NewMemorySourceProvider(tasks, nil, identity, identity, identity, segments)
	store := NewMemoryStore(provider)
	ack, err := store.Put(ctx, user, task.ID, PutInput{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.GetByID(ctx, user, ack.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewEvidenceService(provider, nil, []byte("synthetic-memory-secret")).Context(ctx, user, record)
	if err != nil || view.Answer.ImageAvailable || view.Answer.ImageURL != "" || view.Answer.MaterialChange != "unknown" {
		t.Fatalf("unmaterialized crop must remain unavailable: %+v %v", view.Answer, err)
	}
	for _, source := range []Source{
		{TaskID: task.ID, ReviewTaskID: task.ID, AnswerSegmentID: items[0].ID, ExamID: "exam", QuestionID: "other-question"},
		{TaskID: task.ID, ReviewTaskID: task.ID, AnswerSegmentID: "missing", ExamID: "exam", QuestionID: "question"},
	} {
		if _, err := provider.sourceSegment(ctx, user.TenantID, source, "submission"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid source relation accepted: %+v %v", source, err)
		}
	}
	source := Source{AnswerSegmentID: items[0].ID, ExamID: "exam", QuestionID: "question"}
	for _, args := range [][2]string{{user.TenantID, "other-submission"}, {"other-tenant", "submission"}} {
		if _, err := provider.sourceSegment(ctx, args[0], source, args[1]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross relation accepted: %v %v", args, err)
		}
	}
	// 暗样本只用原任务定位实际题块；没有原任务可核对时失败关闭。
	source.SeedTaskID = "seed"
	if _, err := provider.sourceSegment(ctx, user.TenantID, source, ""); err != nil {
		t.Fatalf("seed source with actual relation: %v", err)
	}
	source.ExamID = "other-exam"
	if _, err := provider.sourceSegment(ctx, user.TenantID, source, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seed with unverifiable exam relation accepted: %v", err)
	}
}
