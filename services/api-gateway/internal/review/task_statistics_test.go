package review

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestReviewStatisticsDistinguishNoSamplesZeroAndPartialCredit(t *testing.T) {
	statistics := buildReviewStatistics("exam", "personal", nil)
	if statistics.GradedCount != 0 || statistics.CorrectRate != nil || statistics.AverageScoreRate != nil || len(statistics.Questions) != 0 {
		t.Fatalf("no samples were shown as zero: %+v", statistics)
	}
	statistics = buildReviewStatistics("exam", "personal", []reviewStatisticSample{
		{questionID: "q1", questionNo: "1", score: 0, maximum: 2},
		{questionID: "q1", questionNo: "1", score: 2, maximum: 2},
		{questionID: "q2", questionNo: "2", score: 4, maximum: 8},
		{questionID: "invalid", score: 1, maximum: 0},
	})
	if statistics.GradedCount != 3 || statistics.FullScoreCount != 1 || math.Abs(*statistics.CorrectRate-1.0/3) > 0.00001 || *statistics.AverageScoreRate != 0.5 {
		t.Fatalf("wrong denominator/full marks/weighted score rate: %+v", statistics)
	}
	if len(statistics.Questions) != 2 || statistics.Questions[0].GradedCount != 2 || *statistics.Questions[0].CorrectRate != 0.5 {
		t.Fatalf("question statistics: %+v", statistics.Questions)
	}
	if *statistics.Questions[0].AverageScore != 1 || *statistics.Questions[1].AverageScore != 4 {
		t.Fatalf("per-question average mixed raw points with another question or a score rate: %+v", statistics.Questions)
	}
}

func TestReviewQuestionAverageKeepsZeroAndSeparatesDistinctQuestionsWithSameNumber(t *testing.T) {
	// 不同试卷题目可能复用题号；统计按 question_id 隔离，零分也必须计入本题平均分。
	statistics := buildReviewStatistics("exam", "exam", []reviewStatisticSample{
		{questionID: "short", questionNo: "1", score: 0, maximum: 2},
		{questionID: "long", questionNo: "1", score: 8, maximum: 10},
		{questionID: "long", questionNo: "1", score: 10, maximum: 10},
	})
	if len(statistics.Questions) != 2 {
		t.Fatalf("distinct questions sharing a number were merged: %+v", statistics.Questions)
	}
	for _, question := range statistics.Questions {
		want := 0.0
		if question.QuestionID == "long" {
			want = 9
		}
		if question.AverageScore == nil || *question.AverageScore != want {
			t.Fatalf("question=%s average=%v want=%v", question.QuestionID, question.AverageScore, want)
		}
	}
}

func TestReviewQuestionStatisticsFollowNaturalQuestionOrder(t *testing.T) {
	statistics := buildReviewStatistics("exam", "exam", []reviewStatisticSample{
		{questionID: "q10", questionNo: "10", score: 1, maximum: 1},
		{questionID: "q2-10", questionNo: "2(10)", score: 1, maximum: 1},
		{questionID: "q2", questionNo: "2", score: 1, maximum: 1},
		{questionID: "q2-2", questionNo: "2(2)", score: 1, maximum: 1},
		{questionID: "q1", questionNo: "1", score: 1, maximum: 1},
	})
	for index, want := range []string{"1", "2", "2(2)", "2(10)", "10"} {
		if got := statistics.Questions[index].QuestionNo; got != want {
			t.Fatalf("question order[%d]=%s want=%s", index, got, want)
		}
	}
}

func TestPersonalStatisticsKeepLatestOwnFormalGradeOutsideActiveStatus(t *testing.T) {
	store := NewMemoryStore()
	for _, item := range []ReviewTask{
		{ID: "own", Status: "submitted", AssignedTo: "teacher", AnswerSegmentID: "segment"},
		{ID: "other", Status: "submitted", AssignedTo: "other", AnswerSegmentID: "segment"},
		{ID: "draft", Status: "in_progress", AssignedTo: "teacher", AnswerSegmentID: "draft"},
	} {
		item.TenantID, item.ExamID, item.QuestionID = "tenant", "exam", "q1"
		store.tasks[key("tenant", item.ID)] = item
	}
	store.grades[key("tenant", "own")] = []HumanGrade{
		{ID: "old", ReviewerID: "teacher", Score: 0, MaxScore: 5, CreatedAt: time.Unix(1, 0)},
		{ID: "new", ReviewerID: "teacher", Score: 5, MaxScore: 5, CreatedAt: time.Unix(2, 0)},
	}
	store.grades[key("tenant", "other")] = []HumanGrade{{ReviewerID: "other", Score: 0, MaxScore: 5}}
	store.grades[key("tenant", "draft")] = []HumanGrade{{ReviewerID: "teacher", Score: 0, MaxScore: 5}}
	aggregate, err := store.AggregateTasks(context.Background(), "tenant", ListFilter{ExamID: "exam", AssignedTo: "teacher", Status: "active", IncludeStatistics: true})
	if err != nil || aggregate.Statistics == nil || aggregate.Statistics.GradedCount != 1 || *aggregate.Statistics.CorrectRate != 1 {
		t.Fatalf("own formal statistics were replaced by active drafts or another teacher: %+v %v", aggregate, err)
	}
	if aggregate.TotalCount != 1 || aggregate.CompletedCount != 0 {
		t.Fatalf("statistics changed queue filtering: %+v", aggregate)
	}
	if len(aggregate.Statistics.Questions) != 1 || aggregate.Statistics.Questions[0].AverageScore == nil || *aggregate.Statistics.Questions[0].AverageScore != 5 {
		t.Fatalf("question average counted duplicate grades, drafts or another reviewer: %+v", aggregate.Statistics.Questions)
	}
}

func TestMemoryStatisticsDoNotPromoteNavigationExamsToSchoolManagement(t *testing.T) {
	store := NewMemoryStore()
	for _, actor := range []string{"own", "other"} {
		store.tasks[key("tenant", actor)] = ReviewTask{ID: actor, TenantID: "tenant", ExamID: "external", QuestionID: "question", AssignedTo: actor, AnswerSegmentID: actor, Status: "submitted"}
		store.grades[key("tenant", actor)] = []HumanGrade{{ID: actor, ReviewerID: actor, Score: 0, MaxScore: 5}}
	}
	for _, tc := range []struct {
		name, assignedTo, mode string
		exams                  []string
		count                  int
	}{
		{"navigation cannot grant full exam", "", "school", []string{"external"}, 0},
		{"own external review survives", "own", "school", []string{"managed-only"}, 1},
		{"another reviewer stays private", "other", "school", []string{"external"}, 0},
		{"assigned scope cannot impersonate", "other", "assigned", nil, 0},
		{"empty class scope cannot grant exam", "", "class", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := ListFilter{ExamID: "external", IncludeStatistics: true, ScopeMode: tc.mode, ScopeActorID: "own", ScopeSchoolIDs: []string{"managed-school"}, ScopeExamIDs: tc.exams, AssignedTo: tc.assignedTo}
			result, err := store.AggregateTasks(t.Context(), "tenant", filter)
			if err != nil || result.Statistics == nil || result.Statistics.GradedCount != tc.count {
				t.Fatalf("statistics scope=%+v err=%v", result.Statistics, err)
			}
			if tc.count == 0 && (result.Statistics.CorrectRate != nil || result.Statistics.AverageScoreRate != nil) {
				t.Fatal("unavailable scoped samples must not become zero correctness")
			}
			if tc.count > 0 && (result.Statistics.CorrectRate == nil || *result.Statistics.CorrectRate != 0) {
				t.Fatal("a formally graded zero must retain a real zero correctness rate")
			}
		})
	}
}

func TestReviewListSchoolScopeNeverFallsBackToNavigationSchools(t *testing.T) {
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: "actor", TenantID: "tenant", Roles: []string{"school_admin"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	scope.SchoolIDs = []string{"navigation-only"}
	scope.ExamIDs = []string{"external-exam"}
	var filter ListFilter
	applyReviewListScope(&filter, scope)
	if filter.ScopeMode != "school" || filter.ScopeActorID != "actor" || len(filter.ScopeSchoolIDs) != 0 {
		t.Fatalf("empty administrator school binding fell back to navigation: %+v", filter)
	}
}

func TestExplicitScoreOnlyRetainsScoreOwnershipAndRealRubricValidation(t *testing.T) {
	for _, tc := range []struct {
		name, actor string
		input       SubmitGradeInput
		want        error
	}{
		{"zero direct score", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: 0, ExpectedRevision: 1}, nil},
		{"direct score", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: 3.5, ExpectedRevision: 1}, nil},
		{"wrong owner", "other", SubmitGradeInput{ScoreOnly: true, Score: 3.5, ExpectedRevision: 1}, ErrForbidden},
		{"wrong revision", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: 3.5, ExpectedRevision: 2}, ErrRevisionConflict},
		{"negative score", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: -1, ExpectedRevision: 1}, ErrInvalidInput},
		{"over maximum", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: 11, ExpectedRevision: 1}, ErrInvalidInput},
		{"nonfinite score", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: math.NaN(), ExpectedRevision: 1}, ErrInvalidInput},
		{"real selection mismatch", "reviewer-1", SubmitGradeInput{ScoreOnly: true, Score: 4, ExpectedRevision: 1, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}}, ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, task := submissionQualificationFixture(t, assessment.RiskR2)
			_, err := store.SubmitGrade(t.Context(), tenantID, task.ID, tc.actor, tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("score-only boundary: %v, want %v", err, tc.want)
			}
		})
	}
}
