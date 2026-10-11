package appeal

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublishedQuestionAppealAssignmentExcludesEveryOriginalReviewer(t *testing.T) {
	ctx := context.Background()
	store, service, item := publishedQuestionWorkflowFixture(t)
	source := publishedQuestionWorkflowSource()
	source.ReviewerIDs = []string{"teacher-first", "teacher-second"}
	store.SeedSource(source)
	// 同名教师在另一租户或另一成绩上的参与记录，不应误拦独立复核者。
	unrelated := source
	unrelated.TenantID, unrelated.ReviewerIDs = "tenant-other", []string{"independent-reviewer"}
	store.SeedSource(unrelated)
	current := source
	current.ReleaseID, current.FinalGradeID, current.ReviewerIDs = "release-current", "grade-current", []string{"independent-reviewer"}
	store.SeedSource(current)
	for _, reviewer := range source.ReviewerIDs {
		if _, err := service.StartReview(ctx, item.TenantID, item.ID, "manager-1", StartQuestionAppealReviewInput{AssignedTo: reviewer, ExpectedRevision: item.Revision}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("original reviewer %s was allowed: %v", reviewer, err)
		}
	}
	if _, err := service.StartReview(ctx, item.TenantID, item.ID, "manager-1", StartQuestionAppealReviewInput{AssignedTo: "teacher-first", ExpectedRevision: item.Revision + 1}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale assignment bypassed revision check: %v", err)
	}
	assertPublishedQuestionWorkflowUnchanged(t, store, item, 1)
	reviewed, err := service.StartReview(ctx, item.TenantID, item.ID, "manager-1", StartQuestionAppealReviewInput{AssignedTo: "independent-reviewer", ExpectedRevision: item.Revision})
	if err != nil || reviewed.Status != QuestionAppealUnderReview || reviewed.Revision != item.Revision+1 {
		t.Fatalf("independent reviewer assignment: %+v %v", reviewed, err)
	}
}

func TestPublishedQuestionAppealDecisionRequiresMatchingStudentRegradeItem(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*publishedQuestionAppealRegradeJob)
	}{
		{name: "other tenant", mutate: func(job *publishedQuestionAppealRegradeJob) { job.TenantID = "tenant-other" }},
		{name: "other exam", mutate: func(job *publishedQuestionAppealRegradeJob) { job.ExamID = "exam-other" }},
		{name: "other question", mutate: func(job *publishedQuestionAppealRegradeJob) { job.QuestionID = "question-other" }},
		{name: "other source release", mutate: func(job *publishedQuestionAppealRegradeJob) { job.SourceReleaseID = "release-other" }},
		{name: "cancelled job", mutate: func(job *publishedQuestionAppealRegradeJob) { job.Status = "cancelled" }},
		{name: "student omitted from selector", mutate: func(job *publishedQuestionAppealRegradeJob) { job.Items = nil }},
		{name: "other source grade", mutate: func(job *publishedQuestionAppealRegradeJob) {
			item := job.Items["submission-1"]
			item.OldFinalGradeID = "grade-other"
			job.Items["submission-1"] = item
		}},
		{name: "cancelled student item", mutate: func(job *publishedQuestionAppealRegradeJob) {
			item := job.Items["submission-1"]
			item.Status = "cancelled"
			job.Items["submission-1"] = item
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, service, item := publishedQuestionWorkflowFixture(t)
			item = startPublishedQuestionWorkflowReview(t, service, item)
			seedPublishedQuestionWorkflowRegrade(store)
			job := store.regradeJobs["regrade-1"]
			tc.mutate(&job)
			store.regradeJobs["regrade-1"] = job
			input := DecideQuestionAppealInput{Decision: QuestionAppealDecisionReferRegrade, PublicResponse: "进入重评", RegradeJobID: "regrade-1", ExpectedRevision: item.Revision}
			if _, err := service.Decide(context.Background(), item.TenantID, item.ID, "manager-1", input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("decision accepted unrelated regrade: %v", err)
			}
			assertPublishedQuestionWorkflowUnchanged(t, store, item, 2)
		})
	}
}

func TestPublishedQuestionAppealResolutionRequiresCompletedRegradeAndMatchingPublishedFact(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutateJob  func(*publishedQuestionAppealRegradeJob)
		mutateFact func(*PublishedQuestionAppealSource)
	}{
		{name: "unfinished job", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.Status = "diff_review" }},
		{name: "missing finalization", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.FinalizedAt = nil }},
		{name: "cancelled job", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.Status = "cancelled" }},
		{name: "other job tenant", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.TenantID = "tenant-other" }},
		{name: "other job source", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.SourceReleaseID = "release-other" }},
		{name: "missing student regrade item", mutateJob: func(job *publishedQuestionAppealRegradeJob) { job.Items = nil }},
		{name: "unresolved student item", mutateJob: func(job *publishedQuestionAppealRegradeJob) {
			item := job.Items["submission-1"]
			item.Status = "awaiting_review"
			job.Items["submission-1"] = item
		}},
		{name: "other release tenant", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.TenantID = "tenant-other" }},
		{name: "other release exam", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.ExamID = "exam-other" }},
		{name: "same release version", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.ReleaseVersion = 1 }},
		{name: "unpublished release", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.Published = false }},
		{name: "other student", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.StudentID = "student-other" }},
		{name: "other submission", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.SubmissionID = "submission-other" }},
		{name: "other question", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.QuestionID = "question-other" }},
		{name: "other source grade", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.FinalGradeID = "grade-other" }},
		{name: "unrelated newer release", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.ReleaseSource = "initial" }},
		{name: "other release source", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.SourceReleaseID = "release-other" }},
		{name: "missing publication time", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.PublishedAt = nil }},
		{name: "published before finalization", mutateFact: func(fact *PublishedQuestionAppealSource) {
			earlier := fact.PublishedAt.Add(-2 * time.Minute)
			fact.PublishedAt = &earlier
		}},
		{name: "different reviewed score", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.Score = 6 }},
		{name: "different maximum score", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.MaxScore = 10 }},
		{name: "different score source", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.SourceType = "rule_auto" }},
		{name: "different reviewed grade", mutateFact: func(fact *PublishedQuestionAppealSource) { fact.SourceID = "reviewed-other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, service, item := publishedQuestionWorkflowFixture(t)
			item = startPublishedQuestionWorkflowReview(t, service, item)
			seedPublishedQuestionWorkflowRegrade(store)
			item = referPublishedQuestionWorkflowRegrade(t, service, item)
			now := store.now()
			store.SeedRegradeJobState("regrade-1", "ready_for_release", &now)
			fact := publishedQuestionWorkflowResolution(now.Add(time.Minute))
			if tc.mutateFact != nil {
				tc.mutateFact(&fact)
			}
			store.SeedSource(fact)
			if tc.mutateJob != nil {
				job := store.regradeJobs["regrade-1"]
				tc.mutateJob(&job)
				store.regradeJobs["regrade-1"] = job
			}
			if _, err := service.Resolve(context.Background(), item.TenantID, item.ID, "manager-1", ResolveQuestionAppealInput{NewReleaseID: "release-2", ExpectedRevision: item.Revision}); !errors.Is(err, ErrResolutionRelease) {
				t.Fatalf("resolution accepted unmatched facts: %v", err)
			}
			assertPublishedQuestionWorkflowUnchanged(t, store, item, 3)
		})
	}
}

func TestPublishedQuestionAppealResolutionRejectsBareVersionAndStaleRevision(t *testing.T) {
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	seedPublishedQuestionWorkflowRegrade(store)
	item = referPublishedQuestionWorkflowRegrade(t, service, item)
	now := store.now()
	store.SeedRegradeJobState("regrade-1", "ready_for_release", &now)
	store.SeedPublishedResolutionRelease(item.TenantID, item.ExamID, "release-2", 2)
	input := ResolveQuestionAppealInput{NewReleaseID: "release-2", ExpectedRevision: item.Revision}
	if _, err := service.Resolve(context.Background(), item.TenantID, item.ID, "manager-1", input); !errors.Is(err, ErrResolutionRelease) {
		t.Fatalf("bare published version allowed resolution: %v", err)
	}
	store.SeedSource(publishedQuestionWorkflowResolution(now.Add(time.Minute)))
	input.ExpectedRevision++
	if _, err := service.Resolve(context.Background(), item.TenantID, item.ID, "manager-1", input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale resolution error: %v", err)
	}
	assertPublishedQuestionWorkflowUnchanged(t, store, item, 3)
	input.ExpectedRevision = item.Revision
	resolved, err := service.Resolve(context.Background(), item.TenantID, item.ID, "manager-1", input)
	if err != nil || resolved.Status != QuestionAppealResolved || resolved.NewReleaseVersion != 2 || resolved.Revision != item.Revision+1 {
		t.Fatalf("valid completed regrade resolution: %+v %v", resolved, err)
	}
}

func TestPublishedQuestionAppealResolutionAllowsReviewedOutcomeKeepingSourceScore(t *testing.T) {
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	store.SeedRegradeJob("regrade-1", item.TenantID, item.ExamID, item.QuestionID, item.SourceReleaseID,
		PublishedQuestionAppealRegradeItemSource{SubmissionID: item.SubmissionID, OldFinalGradeID: item.SourceFinalGradeID, OldScore: 4, MaxScore: 8, Status: "resolved"})
	item = referPublishedQuestionWorkflowRegrade(t, service, item)
	now := store.now()
	store.SeedRegradeJobState("regrade-1", "ready_for_release", &now)
	fact := publishedQuestionWorkflowResolution(now.Add(time.Minute))
	fact.Score, fact.SourceType, fact.SourceID = 4, "double_mark_auto", "original-source"
	store.SeedSource(fact)
	resolved, err := service.Resolve(context.Background(), item.TenantID, item.ID, "manager-1", ResolveQuestionAppealInput{NewReleaseID: fact.ReleaseID, ExpectedRevision: item.Revision})
	if err != nil || resolved.Status != QuestionAppealResolved {
		t.Fatalf("reviewed outcome keeping source score: %+v %v", resolved, err)
	}
}

func TestPublishedQuestionAppealKeepsLinkedReviewedZeroAfterLaterPublication(t *testing.T) {
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	zero := 0.0
	store.SeedRegradeJob("regrade-1", item.TenantID, item.ExamID, item.QuestionID, item.SourceReleaseID, PublishedQuestionAppealRegradeItemSource{SubmissionID: item.SubmissionID, OldFinalGradeID: item.SourceFinalGradeID, OldScore: 4, MaxScore: 8, ReviewedScore: &zero, ReviewedGradeID: "reviewed-grade-1", Status: "resolved"})
	item = referPublishedQuestionWorkflowRegrade(t, service, item)
	now := store.now()
	store.SeedRegradeJobState("regrade-1", "ready_for_release", &now)
	fact := publishedQuestionWorkflowResolution(now.Add(time.Minute))
	fact.Score = 0
	store.SeedSource(fact)
	resolved, err := service.Resolve(t.Context(), item.TenantID, item.ID, "manager-1", ResolveQuestionAppealInput{NewReleaseID: fact.ReleaseID, ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	later := fact
	later.ReleaseID = "release-later"
	later.ReleaseVersion = 3
	later.Score = 8
	store.SeedSource(later)
	got, err := service.Get(t.Context(), item.TenantID, resolved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewedScore == nil || *got.ReviewedScore != 0 || got.ReviewedMaxScore == nil || *got.ReviewedMaxScore != 8 || got.SourceScore != 4 {
		t.Fatalf("historical outcome substituted: %+v", got)
	}
}

func publishedQuestionWorkflowSource() PublishedQuestionAppealSource {
	return PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		QuestionID: "question-1", QuestionNo: "Q1", FinalGradeID: "grade-original", Score: 4, MaxScore: 8,
	}
}

func publishedQuestionWorkflowFixture(t *testing.T) (*PublishedQuestionAppealMemoryStore, *PublishedQuestionAppealService, PublishedQuestionAppeal) {
	t.Helper()
	store := NewPublishedQuestionAppealMemoryStore()
	store.SetNow(func() time.Time { return time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC) })
	store.SeedSource(publishedQuestionWorkflowSource())
	service := NewPublishedQuestionAppealService(store)
	item, err := service.Create(context.Background(), "tenant-1", "student-1", "student-user-1", CreatePublishedQuestionAppealInput{
		ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-1", ReasonCode: ReasonMissingStepCredit, Reason: "过程分遗漏",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, service, item
}

func startPublishedQuestionWorkflowReview(t *testing.T, service *PublishedQuestionAppealService, item PublishedQuestionAppeal) PublishedQuestionAppeal {
	t.Helper()
	updated, err := service.StartReview(context.Background(), item.TenantID, item.ID, "manager-1", StartQuestionAppealReviewInput{AssignedTo: "independent-reviewer", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func seedPublishedQuestionWorkflowRegrade(store *PublishedQuestionAppealMemoryStore) {
	reviewed := 7.0
	store.SeedRegradeJob("regrade-1", "tenant-1", "exam-1", "question-1", "release-1", PublishedQuestionAppealRegradeItemSource{
		SubmissionID: "submission-1", OldFinalGradeID: "grade-original", OldScore: 4, MaxScore: 8,
		ReviewedScore: &reviewed, ReviewedGradeID: "reviewed-grade-1", Status: "resolved",
	})
}

func referPublishedQuestionWorkflowRegrade(t *testing.T, service *PublishedQuestionAppealService, item PublishedQuestionAppeal) PublishedQuestionAppeal {
	t.Helper()
	updated, err := service.Decide(context.Background(), item.TenantID, item.ID, "manager-1", DecideQuestionAppealInput{
		Decision: QuestionAppealDecisionReferRegrade, PublicResponse: "进入重评", RegradeJobID: "regrade-1", ExpectedRevision: item.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func publishedQuestionWorkflowResolution(publishedAt time.Time) PublishedQuestionAppealSource {
	fact := publishedQuestionWorkflowSource()
	fact.ReleaseID, fact.ReleaseVersion, fact.ReleaseSource, fact.SourceReleaseID, fact.PublishedAt = "release-2", 2, "regrade", "release-1", &publishedAt
	fact.Score, fact.SourceType, fact.SourceID = 7, "single_review", "reviewed-grade-1"
	return fact
}

func assertPublishedQuestionWorkflowUnchanged(t *testing.T, store *PublishedQuestionAppealMemoryStore, expected PublishedQuestionAppeal, eventCount int) {
	t.Helper()
	actual, err := store.GetPublishedQuestionAppeal(context.Background(), expected.TenantID, expected.ID)
	if err != nil || actual.Revision != expected.Revision || actual.Status != expected.Status || actual.AssignedTo != expected.AssignedTo || actual.NewReleaseID != expected.NewReleaseID || actual.RegradeJobID != expected.RegradeJobID {
		t.Fatalf("failed operation changed appeal: %+v %v", actual, err)
	}
	events, err := store.ListPublishedQuestionAppealEvents(context.Background(), expected.TenantID, expected.ID)
	if err != nil || len(events) != eventCount {
		t.Fatalf("failed operation added event: %+v %v", events, err)
	}
}
