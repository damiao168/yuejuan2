package gradingevaluation

import (
	"reflect"
	"testing"
)

func TestQualitySummaryPreservesMissingCoverageAndRoutingDenominators(t *testing.T) {
	t.Run("no observations", func(t *testing.T) {
		got := calculateQualitySummary(nil)
		want := QualitySummary{ErrorAttribution: []ErrorAttribution{}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("empty summary=%+v, want %+v", got, want)
		}
	})

	t.Run("missing values and explicit zero values", func(t *testing.T) {
		// 无错误但需人工复核的样本只计入总复核率；风险召回率只统计带错误来源的样本。
		items := []Observation{
			{ErrorSource: ErrorNone, NeedsHumanReview: true},
			{PageMatchCorrect: boolPointer(false), CropIoU: floatPointer(0), TranscriptionCER: floatPointer(0), FormulaExact: boolPointer(false), RubricAgreement: floatPointer(0), ErrorSource: ErrorRubric, NeedsHumanReview: true, ReferenceScore: 4, ModelScore: 0, MaxScore: 4},
			{PageMatchCorrect: boolPointer(true), CropIoU: floatPointer(1), TranscriptionCER: floatPointer(.6), FormulaExact: boolPointer(true), RubricAgreement: floatPointer(1), ErrorSource: ErrorRubric, ReferenceScore: 2, ModelScore: 2, MaxScore: 4},
			{PageMatchCorrect: boolPointer(false), ErrorSource: ErrorPageMatching, NeedsHumanReview: true, ReferenceScore: 1, ModelScore: 1, MaxScore: 4},
		}
		got := calculateQualitySummary(items)
		want := QualitySummary{
			SampleCount:                  4,
			PageMatchAccuracy:            CoveredRate{ObservedCount: 3, Rate: floatPointer(.333333)},
			MeanCropIoU:                  CoveredMean{ObservedCount: 2, Mean: floatPointer(.5)},
			MeanTranscriptionCER:         CoveredMean{ObservedCount: 2, Mean: floatPointer(.3)},
			FormulaExactRate:             CoveredRate{ObservedCount: 2, Rate: floatPointer(.5)},
			MeanRubricCriterionAgreement: CoveredMean{ObservedCount: 2, Mean: floatPointer(.5)},
			HumanReviewRate:              .75,
			RiskyErrorRoutingRecall:      .666667,
			ErrorAttribution: []ErrorAttribution{
				{Source: ErrorPageMatching, Count: 1, Rate: .25, HumanReviewCount: 1, HumanRoutingRecall: 1},
				{Source: ErrorRubric, Count: 2, Rate: .5, SevereErrorCount: 1, HumanReviewCount: 1, HumanRoutingRecall: .5},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("summary=%+v, want %+v", got, want)
		}
	})
}

func TestScoreMetricsPreservesFloatingEndpointTolerance(t *testing.T) {
	cases := []struct {
		name      string
		item      Observation
		falseZero float64
		falseFull float64
	}{
		{"near zero with positive reference", Observation{ReferenceScore: 2 * scoreEpsilon, ModelScore: scoreEpsilon / 2, MaxScore: 4}, 1, 0},
		{"reference on zero tolerance boundary", Observation{ReferenceScore: scoreEpsilon, ModelScore: 0, MaxScore: 4}, 0, 0},
		{"model on zero tolerance boundary", Observation{ReferenceScore: 1, ModelScore: scoreEpsilon, MaxScore: 4}, 0, 0},
		{"near full with lower reference", Observation{ReferenceScore: 4 - 2*scoreEpsilon, ModelScore: 4 - scoreEpsilon, MaxScore: 4}, 0, 1},
		{"reference on full tolerance boundary", Observation{ReferenceScore: 4 - scoreEpsilon, ModelScore: 4, MaxScore: 4}, 0, 0},
		{"model below full tolerance boundary", Observation{ReferenceScore: 0, ModelScore: 4 - 2*scoreEpsilon, MaxScore: 4}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calculateMetrics([]Observation{tc.item})
			if got.SampleCount != 1 || got.FalseZeroRate != tc.falseZero || got.FalseFullRate != tc.falseFull {
				t.Fatalf("metrics=%+v, want false zero=%v and false full=%v", got, tc.falseZero, tc.falseFull)
			}
		})
	}
}

func TestConservativeQWKPreservesValuesAndUnavailableReasons(t *testing.T) {
	valid := []Observation{{ReferenceScore: 0, ModelScore: 0, MaxScore: 4}, {ReferenceScore: 2, ModelScore: 2, MaxScore: 4}, {ReferenceScore: 4, ModelScore: 3, MaxScore: 4}}
	cases := []struct {
		name   string
		items  []Observation
		value  float64
		ok     bool
		reason string
	}{
		{"aligned integer scores", valid, .923077, true, ""},
		{"no observations", nil, 0, false, "requires_at_least_two_aligned_observations"},
		{"one observation", valid[:1], 0, false, "requires_at_least_two_aligned_observations"},
		{"fractional max score", []Observation{{MaxScore: 4.5}, {MaxScore: 4.5}}, 0, false, "requires_single_integer_score_scale"},
		{"fractional reference score", []Observation{valid[0], {ReferenceScore: 2.5, ModelScore: 2, MaxScore: 4}}, 0, false, "requires_single_integer_score_scale"},
		{"fractional model score", []Observation{valid[0], {ReferenceScore: 2, ModelScore: 2.5, MaxScore: 4}}, 0, false, "requires_single_integer_score_scale"},
		{"score outside scale", []Observation{valid[0], {ReferenceScore: 2, ModelScore: 5, MaxScore: 4}}, 0, false, "requires_single_integer_score_scale"},
		{"constant scores", []Observation{valid[0], valid[0]}, 0, false, "undefined_for_constant_score_distribution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, ok, reason := conservativeQWK(tc.items)
			if value != tc.value || ok != tc.ok || reason != tc.reason {
				t.Fatalf("QWK=(%v, %v, %q), want (%v, %v, %q)", value, ok, reason, tc.value, tc.ok, tc.reason)
			}
		})
	}
}
