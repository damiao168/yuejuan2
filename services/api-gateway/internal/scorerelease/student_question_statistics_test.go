package scorerelease

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
)

func TestQuestionReferenceWithoutFrozenScopeOmitsClassAndSchoolScores(t *testing.T) {
	questions := make([]ReleaseQuestion, privacyMinCohortSize)
	for i := range questions {
		questions[i] = ReleaseQuestion{QuestionFact: QuestionFact{QuestionID: "q", Score: float64(i), MaxScore: 10}}
	}
	ref := buildQuestionReference("q", questions)
	if ref == nil || ref.SampleSize != privacyMinCohortSize || ref.MedianScore == nil || *ref.MedianScore != 4.5 {
		t.Fatalf("exam-wide reference lost: %+v", ref)
	}
	if ref.ClassMeanScore != nil || ref.SchoolMeanScore != nil || ref.ClassUnavailableReason != "scope_unavailable" {
		t.Fatalf("missing frozen scope produced class/school scores: %+v", ref)
	}
	if small := buildQuestionReference("q", questions[:privacyMinCohortSize-1]); small != nil {
		t.Fatalf("small cohort produced a reference: %+v", small)
	}
}

func TestQuestionReferenceJSONKeepsRealZeroAndOmitsUnavailableScores(t *testing.T) {
	zero := 0.0
	ref := StudentQuestionScoreComparison{SchoolMeanScore: &zero, SchoolMaxScore: &zero}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	value := string(raw)
	if !strings.Contains(value, `"school_mean_score":0`) || !strings.Contains(value, `"school_max_score":0`) || strings.Contains(value, `"class_mean_score"`) || strings.Contains(value, `"class_max_score"`) {
		t.Fatalf("zero and missing scores became indistinguishable: %s", raw)
	}
	ref.ClassMeanScore, ref.ClassMaxScore = &zero, &zero
	raw, err = json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"class_mean_score":0`) || !strings.Contains(string(raw), `"class_max_score":0`) {
		t.Fatalf("real class zeros were omitted: %s", raw)
	}
}

func TestQuestionComparisonDoesNotReopenLegacyCohortStatistics(t *testing.T) {
	zero := 0.0
	comparison := &StudentQuestionScoreComparison{ClassMeanScore: &zero, ClassMaxScore: &zero, SchoolMeanScore: &zero, SchoolMaxScore: &zero}
	cohort := &StudentQuestionReference{SampleSize: privacyMinCohortSize}
	stats := map[string]studentQuestionAggregates{"q": {Comparison: comparison, Cohort: cohort}}
	question := ReleaseQuestion{QuestionFact: QuestionFact{QuestionID: "q"}}
	visible := postgresStudentQuestion(question, VisibilityPolicy{ShowQuestionScores: true}, nil, nil, stats)
	if visible.ScoreComparison != comparison || visible.Cohort != nil || visible.CohortUnavailableReason != "not_public" {
		t.Fatalf("visible comparison changed legacy policy: %+v", visible)
	}
	public := postgresStudentQuestion(question, VisibilityPolicy{ShowQuestionScores: true, ShowQuestionStatistics: true}, nil, nil, stats)
	if public.ScoreComparison != comparison || public.Cohort != cohort || public.CohortUnavailableReason != "" {
		t.Fatalf("explicit cohort policy was ignored: %+v", public)
	}
	hidden := postgresStudentQuestion(question, VisibilityPolicy{}, nil, nil, stats)
	if hidden.ScoreComparison != nil || hidden.Cohort != nil {
		t.Fatalf("hidden question policy exposed comparison: %+v", hidden)
	}
}

func TestStudentResultReturnsSingleQuestionComparisonWithLegacyAnalyticsHidden(t *testing.T) {
	steps := studentBatch50ReadSteps(t, true)
	steps[5].rows = [][]driver.Value{{"frozen-release", "submission", "q", "1", "final", float64(0), float64(0), "single_review", "", []byte(`{}`)}}
	steps[7].rows = [][]driver.Value{{"q", int64(1), int64(1), float64(0), float64(0), float64(0), float64(0), float64(0), float64(0), float64(1), float64(1)}}
	result, err := studentBatch50Store(t, steps).StudentResult(t.Context(), "tenant", "exam", "student")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Questions) != 1 || result.Questions[0].ScoreComparison == nil || result.Questions[0].Cohort != nil || result.Questions[0].CohortUnavailableReason != "not_public" {
		t.Fatalf("single published question comparison was suppressed: %+v", result.Questions)
	}
	comparison := result.Questions[0].ScoreComparison
	for _, score := range []*float64{comparison.ClassMeanScore, comparison.ClassMaxScore, comparison.SchoolMeanScore, comparison.SchoolMaxScore} {
		if score == nil || *score != 0 {
			t.Fatalf("published zero comparison missing: %+v", comparison)
		}
	}
}
