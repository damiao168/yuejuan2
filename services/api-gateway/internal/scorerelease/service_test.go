package scorerelease

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	tenant = "tenant-1"
	exam   = "exam-1"
	actor  = "manager-1"
)

func TestPublishedReleaseIsImmutableAndNewReleaseCarriesCorrection(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	first, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Source: SourceInitial, Reason: "initial verified release", IdempotencyKey: "release-key-0001", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowFeedback: true, ShowRubricSummary: true}})
	if err != nil {
		t.Fatalf("create first release: %v", err)
	}
	if _, err := service.Publish(context.Background(), tenant, first.ID, actor); err != nil {
		t.Fatalf("publish first release: %v", err)
	}

	store.SeedFacts(exam, []SubmissionFact{fixtureFact(5)})
	second, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Source: SourceAppeal, Reason: "appeal correction reviewed", IdempotencyKey: "release-key-0002", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatalf("create corrected release: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("expected version 2, got %#v", second)
	}
	diff, err := service.Diff(context.Background(), tenant, second.ID, first.ID)
	if err != nil || diff.AffectedCount != 1 || len(diff.Questions) != 1 || diff.Questions[0].OldScore != 4 || diff.Questions[0].NewScore != 5 {
		t.Fatalf("expected a question-level correction diff, got %#v err=%v", diff, err)
	}
	if _, err := service.Publish(context.Background(), tenant, second.ID, actor); err != nil {
		t.Fatalf("publish corrected release: %v", err)
	}

	old, err := service.Get(context.Background(), tenant, first.ID)
	if err != nil || old.Items[0].TotalScore != 4 || old.Questions[0].Score != 4 || old.Release.Status != StatusPublished {
		t.Fatalf("published first release was changed: %#v err=%v", old, err)
	}
	current, err := service.StudentResult(context.Background(), tenant, exam, "student-1")
	if err != nil || current.TotalScore != 5 || current.ReleaseVersion != 2 {
		t.Fatalf("student should see new release only: %#v err=%v", current, err)
	}
}

func TestPublishRecomputesGateAndRollbackIsAnotherVersion(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	first, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Reason: "initial", IdempotencyKey: "release-key-0011", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	store.SetGateIssues(exam, []GateIssue{{Code: "open_quality_incident", Message: "unreviewed critical incident", Blocking: true, Count: 1}})
	if _, err := service.Publish(context.Background(), tenant, first.ID, actor); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("publish must recheck a newly blocked gate, got %v", err)
	}
	store.SetGateIssues(exam, nil)
	if _, err := service.Publish(context.Background(), tenant, first.ID, actor); err != nil {
		t.Fatal(err)
	}

	store.SeedFacts(exam, []SubmissionFact{fixtureFact(2)})
	second, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Source: SourceRegrade, Reason: "regrade", IdempotencyKey: "release-key-0012", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, second.ID, actor); err != nil {
		t.Fatal(err)
	}

	rollback, err := service.CreateRollback(context.Background(), tenant, exam, actor, RollbackInput{SourceReleaseID: first.ID, Reason: "restore verified release", IdempotencyKey: "release-key-0013"})
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Version != 3 || rollback.Source != SourceRollback || rollback.SourceReleaseID != first.ID {
		t.Fatalf("rollback must create a traced new draft: %#v", rollback)
	}
	if _, err := service.Publish(context.Background(), tenant, rollback.ID, actor); err != nil {
		t.Fatal(err)
	}
	current, err := service.StudentResult(context.Background(), tenant, exam, "student-1")
	if err != nil || current.TotalScore != 4 || current.ReleaseVersion != 3 {
		t.Fatalf("rollback should publish a new immutable version: %#v err=%v", current, err)
	}
}

func TestStudentResultCannotLeakInternalReleaseFieldsAndRespectsAppealWindow(t *testing.T) {
	store := NewMemoryStore()
	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return fixed })
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	closes := fixed.Add(time.Hour)
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Reason: "student safe", IdempotencyKey: "release-key-0021", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowFeedback: true, ShowRubricSummary: true}, AppealWindow: AppealWindow{Enabled: true, ClosesAt: &closes, AllowedReasonCodes: []string{"recognition_error", "rubric_disagreement"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentResult(context.Background(), tenant, exam, "student-1")
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(result)
	for _, forbidden := range []string{"final_grade_id", "source_id", "private_note", "reviewer", "prompt", "quality"} {
		if strings.Contains(string(bytes), forbidden) {
			t.Fatalf("student result leaked %q: %s", forbidden, bytes)
		}
	}
	if !result.AppealWindow.Open || len(result.AppealWindow.AllowedReasonCodes) != 2 || result.Questions[0].Feedback != "Teacher feedback" {
		t.Fatalf("unexpected student-safe result: %#v", result)
	}
}

func TestStudentQuestionImageRequiresCurrentPublishedQuestionAndStudentScope(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{
		Reason: "student answer image", IdempotencyKey: "release-key-0022", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	store.SeedStudentQuestionImage(tenant, exam, "student-1", "question-1", "segment-1")
	source, err := service.StudentQuestionImage(context.Background(), tenant, exam, "student-1", "question-1")
	if err != nil || source.AnswerSegmentID != "segment-1" {
		t.Fatalf("student image source = %#v, %v", source, err)
	}
	if _, err := service.StudentQuestionImage(context.Background(), tenant, exam, "other-student", "question-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other student image error = %v, want ErrNotFound", err)
	}
}

func TestStudentReferenceIsReleaseBoundAndPrivacyProtected(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	facts := make([]SubmissionFact, 0, 12)
	for index := 1; index <= 12; index++ {
		score := float64(index * 5)
		facts = append(facts, SubmissionFact{
			StudentID: "student-" + string(rune('a'+index-1)), SubmissionID: "submission-" + string(rune('a'+index-1)),
			TotalScore: score, MaxScore: 100, Status: "confirmed",
			Questions: []QuestionFact{{QuestionID: "question-1", QuestionNo: "1", FinalGradeID: "final-" + string(rune('a'+index-1)), Score: score, MaxScore: 100, SourceType: "single_review"}},
		})
	}
	store.SeedFacts(exam, facts)
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{
		Reason: "student analytics", IdempotencyKey: "release-key-analytics-1",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowCohortStatistics: true, ShowScoreDistribution: true, ShowPercentile: true, ShowQuestionStatistics: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentResult(context.Background(), tenant, exam, "student-l")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reference == nil || !result.Reference.StatisticsAvailable || result.Reference.SampleSize != 12 || result.Reference.Percentile == nil {
		t.Fatalf("expected aggregate-only reference, got %#v", result.Reference)
	}
	if result.Reference.Rank != nil {
		t.Fatalf("exact rank must remain hidden unless explicitly released: %#v", result.Reference)
	}
	if len(result.Questions) != 1 || result.Questions[0].Cohort == nil || result.Questions[0].Cohort.SampleSize != 12 {
		t.Fatalf("expected privacy-safe question aggregate: %#v", result.Questions)
	}
}

func TestStudentTableFieldsRequireExplicitReleasePolicy(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	facts := make([]SubmissionFact, 0, 12)
	for index := 1; index <= 12; index++ {
		fact := fixtureFact(float64(index % 6))
		fact.StudentID = "student-" + string(rune('a'+index-1))
		fact.SubmissionID = "submission-" + string(rune('a'+index-1))
		fact.Questions[0].Explanation.CorrectAnswer = "B"
		fact.Questions[0].Explanation.ActualAnswer = "A"
		facts = append(facts, fact)
	}
	store.SeedFacts(exam, facts)
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{
		Reason: "table fields", IdempotencyKey: "release-key-table-fields",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowQuestionStatistics: true, ShowExactRank: true, ShowAnswers: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentResult(context.Background(), tenant, exam, "student-l")
	if err != nil {
		t.Fatal(err)
	}
	if result.Rankings == nil || result.Rankings.GradeSize != 12 {
		t.Fatalf("expected released rankings, got %#v", result.Rankings)
	}
	if len(result.Questions) != 1 || result.Questions[0].CorrectAnswer != "B" || result.Questions[0].ActualAnswer != "A" || result.Questions[0].Cohort == nil || result.Questions[0].Cohort.MedianScore == nil {
		t.Fatalf("expected released table fields, got %#v", result.Questions)
	}
}

func TestStudentTableFieldsStayHiddenAndSmallCohortsSuppressStatistics(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	facts := make([]SubmissionFact, 0, 9)
	for index := 1; index <= 9; index++ {
		fact := fixtureFact(float64(index % 6))
		fact.StudentID = "student-" + string(rune('a'+index-1))
		fact.SubmissionID = "submission-" + string(rune('a'+index-1))
		fact.Questions[0].Explanation.CorrectAnswer = "B"
		fact.Questions[0].Explanation.ActualAnswer = "A"
		facts = append(facts, fact)
	}
	store.SeedFacts(exam, facts)
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{
		Reason: "privacy protected table", IdempotencyKey: "release-key-table-private",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowQuestionStatistics: true, ShowCohortStatistics: true, ShowExactRank: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentResult(context.Background(), tenant, exam, "student-i")
	if err != nil {
		t.Fatal(err)
	}
	if result.Questions[0].CorrectAnswer != "" || result.Questions[0].ActualAnswer != "" {
		t.Fatalf("answers must require ShowAnswers: %#v", result.Questions[0])
	}
	if result.Reference == nil || result.Reference.StatisticsAvailable || result.Reference.UnavailableReason != "small_cohort" {
		t.Fatalf("small cohort should expose only an unavailable marker: %#v", result.Reference)
	}
	if result.Rankings != nil || result.Questions[0].Cohort != nil {
		t.Fatalf("small cohort leaked exact rank or question statistics: rankings=%#v question=%#v", result.Rankings, result.Questions[0])
	}
}

func TestStudentPaperPageImageRequiresPublishedQuestionAndStudentScope(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{
		Reason: "student paper page", IdempotencyKey: "release-key-paper-page", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	store.SeedStudentQuestionImage(tenant, exam, "student-1", "question-1", "segment-1")

	source, err := service.StudentPaperPageImage(context.Background(), tenant, exam, "student-1", "question-1", false)
	if err != nil || source.AnswerSegmentID != "segment-1" {
		t.Fatalf("own released paper page = %#v, %v", source, err)
	}
	for name, tc := range map[string]struct {
		studentID  string
		questionID string
		highScore  bool
	}{
		"other student":               {studentID: "student-2", questionID: "question-1"},
		"unreleased question":         {studentID: "student-1", questionID: "question-2"},
		"unreleased high score paper": {studentID: "student-1", questionID: "question-1", highScore: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.StudentPaperPageImage(context.Background(), tenant, exam, tc.studentID, tc.questionID, tc.highScore); !errors.Is(err, ErrNotFound) {
				t.Fatalf("StudentPaperPageImage error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestLegacyHighScorePolicyNeverSharesPeerPaper(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store)
	first := fixtureFact(4)
	second := fixtureFact(5)
	second.StudentID, second.SubmissionID = "student-2", "submission-2"
	store.SeedFacts(exam, []SubmissionFact{first, second})
	policy := VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true}
	newRelease, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "anonymous share requires assets", IdempotencyKey: "unsafe-release-1", VisibilityPolicy: policy})
	if err != nil {
		t.Fatalf("new high-score draft: %v", err)
	}
	if _, err := service.Publish(ctx, tenant, newRelease.ID, actor); !errors.Is(err, ErrAnonymousPaperUnavailable) {
		t.Fatalf("publish without anonymous page assets: %v", err)
	}
	legacy, err := store.Create(ctx, tenant, exam, actor, CreateInput{Source: SourceInitial, Reason: "historical policy", IdempotencyKey: "legacy-release-1", VisibilityPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, tenant, legacy.ID, actor); err != nil {
		t.Fatal(err)
	}
	store.SeedStudentQuestionImage(tenant, exam, "student-1", "question-1", "segment-1")
	store.SeedStudentQuestionImage(tenant, exam, "student-2", "question-1", "segment-2")
	result, err := service.StudentResult(ctx, tenant, exam, "student-1")
	if err != nil || result.HighScorePaper != nil {
		t.Fatalf("legacy student result exposed peer paper: %+v %v", result, err)
	}
	if _, err := service.StudentPaperPageImage(ctx, tenant, exam, "student-1", "question-1", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer paper image: %v", err)
	}
	if own, err := service.StudentPaperPageImage(ctx, tenant, exam, "student-1", "question-1", false); err != nil || own.AnswerSegmentID != "segment-1" {
		t.Fatalf("own paper image: %+v %v", own, err)
	}
	rollback, err := service.CreateRollback(ctx, tenant, exam, actor, RollbackInput{SourceReleaseID: legacy.ID, Reason: "copy historical release", IdempotencyKey: "legacy-rollback-1"})
	if err != nil || rollback.VisibilityPolicy.ShowHighScorePaper {
		t.Fatalf("rollback propagated unsafe setting: %+v %v", rollback, err)
	}
	regrade, err := service.CreateFromRegrade(ctx, tenant, exam, actor, CreateRegradeInput{
		SourceReleaseID: legacy.ID, QuestionID: "question-1", Reason: "reviewed correction", IdempotencyKey: "legacy-regrade-1",
		Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 3, MaxScore: 5, ReviewedGradeID: "reviewed-grade-1"}},
	})
	if err != nil || regrade.VisibilityPolicy.ShowHighScorePaper {
		t.Fatalf("regrade propagated unsafe setting: %+v %v", regrade, err)
	}
}

func fixtureFact(score float64) SubmissionFact {
	return SubmissionFact{StudentID: "student-1", SubmissionID: "submission-1", TotalScore: score, MaxScore: 5, Status: "confirmed", Questions: []QuestionFact{{QuestionID: "question-1", QuestionNo: "Q1", FinalGradeID: "final-1", Score: score, MaxScore: 5, SourceType: "single_review", SourceID: "human-grade-1", Explanation: StudentExplanation{Feedback: "Teacher feedback", RubricSummary: []string{"Shows the required method"}}}}}
}
