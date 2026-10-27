package paper

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
)

func TestRubricDraftArchiveBlocksActiveWorkForStandaloneAndParentSession(t *testing.T) {
	for _, parent := range []bool{false, true} {
		for _, status := range []string{"queued", "running", "cancelling"} {
			t.Run(status+map[bool]string{false: "_standalone", true: "_session"}[parent], func(t *testing.T) {
				server := questionDraftServer(t, nil)
				defer server.Close()
				_, question, jobs, scope, exams, item := newRubricDraftArchiveFixture(t, server, parent)
				job := createRubricDraftJobForQuestion(t, jobs, scope, question)
				if status != "queued" {
					if record, err := jobs.store.claimRubricDraftJob(t.Context(), jobs.timeout, job.ID); err != nil || record == nil {
						t.Fatalf("claim: %#v %v", record, err)
					}
				}
				if status == "cancelling" {
					if _, err := jobs.store.cancelRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID); err != nil {
						t.Fatal(err)
					}
				}
				check, err := exams.CheckManagementArchive(t.Context(), scope, item.Kind, item.ID)
				if err != nil || check.Allowed || len(check.Blockers) != 1 || check.Blockers[0].Code != "unfinished_work" {
					t.Fatalf("active suggestion must be visible in archive preview: %#v %v", check, err)
				}
				if _, err := exams.ArchiveManagementExam(t.Context(), scope, scope.ActorID, item.Kind, item.ID, rubricArchiveInput(item)); !errors.Is(err, exam.ErrArchiveBlocked) {
					t.Fatalf("archive crossed active %s suggestion: %v", status, err)
				}
			})
		}
	}
}

func TestRubricDraftArchiveRejectsCreationAfterPrevalidationAndHidesCompletedResult(t *testing.T) {
	for _, parent := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "parent_session"}[parent], func(t *testing.T) {
			var calls atomic.Int32
			server := questionDraftServer(t, func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig) { calls.Add(1) })
			defer server.Close()
			_, question, jobs, scope, exams, item := newRubricDraftArchiveFixture(t, server, parent)
			job := createRubricDraftJobForQuestion(t, jobs, scope, question)
			executeRubricDraftJob(t, jobs, job.ID)
			if _, err := exams.ArchiveManagementExam(t.Context(), scope, scope.ActorID, item.Kind, item.ID, rubricArchiveInput(item)); err != nil {
				t.Fatal(err)
			}
			current, err := jobs.Result(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
			if err != nil || current.Status != "stale" || current.Result != nil || calls.Load() != 1 {
				t.Fatalf("archived completed suggestion remained usable: %#v calls=%d err=%v", current, calls.Load(), err)
			}
			if _, err := jobs.Create(t.Context(), scope, question.ExamID, question.ID, CreateRubricDraftJobInput{
				RequestID: uuid.NewString(), QuestionMathRubricDraftInput: QuestionMathRubricDraftInput{ExpectedQuestion: &question},
			}); !errors.Is(err, ErrExamFrozen) {
				t.Fatalf("business stage draft cannot conceal management archive: %v", err)
			}
		})
	}

	server := questionDraftServer(t, nil)
	defer server.Close()
	store, question, jobs, scope, exams, item := newRubricDraftArchiveFixture(t, server, false)
	entered, release := make(chan struct{}), make(chan struct{})
	resolver := jobs.service.modelResolver
	jobs.service.modelResolver = func(ctx context.Context, tenantID string) (*DocumentModelConfig, error) {
		close(entered)
		<-release
		return resolver(ctx, tenantID)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := jobs.Create(t.Context(), scope, question.ExamID, question.ID, CreateRubricDraftJobInput{RequestID: uuid.NewString(),
			QuestionMathRubricDraftInput: QuestionMathRubricDraftInput{ExpectedQuestion: &question}})
		finished <- err
	}()
	<-entered
	_, archiveErr := exams.ArchiveManagementExam(t.Context(), scope, scope.ActorID, item.Kind, item.ID, rubricArchiveInput(item))
	close(release)
	if archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if err := <-finished; !errors.Is(err, ErrExamFrozen) {
		t.Fatalf("create used a mutable prevalidation snapshot after archive committed: %v", err)
	}
	if len(store.rubricDraftJobs) != 0 {
		t.Fatal("archive/create race left a queued paid task")
	}
}

func TestRubricDraftArchiveDiscardsLegacyQueuedWorkWithoutCallingModel(t *testing.T) {
	var calls atomic.Int32
	server := questionDraftServer(t, func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig) { calls.Add(1) })
	defer server.Close()
	store, question, jobs, scope, exams, item := newRubricDraftArchiveFixture(t, server, true)
	job := createRubricDraftJobForQuestion(t, jobs, scope, question)
	if _, err := store.cancelRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := exams.ArchiveManagementExam(t.Context(), scope, scope.ActorID, item.Kind, item.ID, rubricArchiveInput(item)); err != nil {
		t.Fatal(err)
	}
	// 仅恢复合成内存夹具中的旧排队状态，模拟升级前已归档却遗漏的任务；执行必须在调用模型前拒绝。
	store.mu.Lock()
	legacy := store.rubricDraftJobs[job.ID]
	legacy.Job.Status = "queued"
	store.rubricDraftJobs[job.ID] = legacy
	store.mu.Unlock()
	executeRubricDraftJob(t, jobs, job.ID)
	current, err := jobs.Result(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
	if err != nil || current.Status != "stale" || current.Result != nil || calls.Load() != 0 {
		t.Fatalf("legacy archived queue invoked a model or published a result: %#v calls=%d err=%v", current, calls.Load(), err)
	}
}

func newRubricDraftArchiveFixture(t *testing.T, server *httptest.Server, parent bool) (*MemoryStore, Question, *RubricDraftJobs, auth.AccessScope, *exam.MemoryStore, exam.ManagementExam) {
	t.Helper()
	store, question, original, scope := newRubricDraftJobsFixture(t, server)
	exams := exam.NewMemoryStore()
	kind, id := "standalone", ""
	if parent {
		session, err := exams.CreateExamSession(t.Context(), scope, scope.ActorID, exam.CreateSessionInput{SchoolID: "school", GradeID: "grade", Name: "synthetic", ExamType: "midterm_exam",
			GradingMode: "ai_assisted", PublishPolicy: "manual", ClassIDs: []string{"class"}, Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 100}}})
		if err != nil {
			t.Fatal(err)
		}
		question.ExamID, id, kind = session.Exams[0].ID, session.ID, "session"
	} else {
		item, err := exams.CreateExam(t.Context(), scope, scope.ActorID, exam.CreateInput{SchoolID: "school", Name: "synthetic", Subject: "math", ExamType: "midterm_exam", TotalScore: 100,
			GradingMode: "ai_assisted", PublishPolicy: "manual", ClassIDs: []string{"class"}})
		if err != nil {
			t.Fatal(err)
		}
		question.ExamID, id = item.ID, item.ID
	}
	store.mu.Lock()
	store.questions[question.ID] = question
	store.mu.Unlock()
	jobs := NewRubricDraftJobs(store, exams, original.service, original.timeout)
	item, err := exams.GetManagementExam(t.Context(), scope, kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return store, question, jobs, scope, exams, item
}

func rubricArchiveInput(item exam.ManagementExam) exam.ManagementArchiveInput {
	revisions := map[string]int64{}
	for _, child := range item.Exams {
		revisions[child.ID] = child.Revision
	}
	return exam.ManagementArchiveInput{ExpectedRevision: item.Revision, ExpectedSubjectRevisions: revisions, CommandID: uuid.NewString(), Reason: "synthetic archive"}
}
