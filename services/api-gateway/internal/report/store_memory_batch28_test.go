package report

import (
	"context"
	"reflect"
	"testing"
)

func TestBatch28QualityCountsKeepIndependentDenominators(t *testing.T) {
	store := NewMemoryStore()
	firstAI, firstHuman := 10.121, 10.124
	secondAI, secondHuman := 10.121, 10.129
	for _, seed := range []GradeSeed{
		{Source: "rule_auto"},
		{Source: "single_review", AIScore: &firstAI, HumanScore: &firstHuman},
		{Source: "single_review", AIScore: &secondAI, HumanScore: &secondHuman},
		{Source: "single_review", HumanScore: &firstHuman},
		{Source: "rule_auto", AIScore: &firstAI},
	} {
		store.AddGrade(seed)
	}
	store.SetQuality(QualitySeed{DoubleMarkDiffs: []float64{1, 2, 3}, ArbitrationCount: 2, OCRTaskCount: 4, OCRFailedCount: 1, LowConfidenceReviewCount: 3})
	// 内存报表不校验种子来源；这里只保护旧口径，不能把 rule_auto 计数理解为真实模型采纳证明。
	want := GradingQualityReport{
		ExamID: "exam-a", TotalSegments: 5, AIGradeCount: 3, AIAcceptedCount: 2,
		AIAdoptionRate:       Metric{Available: true, Value: 0.67, Numerator: 2, Denominator: 3},
		HumanComparableCount: 2, HumanModifiedCount: 1,
		HumanModificationRate:  Metric{Available: true, Value: 0.5, Numerator: 1, Denominator: 2},
		DoubleMarkSessionCount: 3,
		AverageDoubleMarkDiff:  Metric{Available: true, Value: 2, Denominator: 3},
		MaxDoubleMarkDiff:      Metric{Available: true, Value: 3, Denominator: 3},
		ArbitrationCount:       2, OCRTaskCount: 4, OCRFailedCount: 1,
		OCRFailureRate:           Metric{Available: true, Value: 0.25, Numerator: 1, Denominator: 4},
		LowConfidenceReviewCount: 3,
	}
	for i := 0; i < 2; i++ {
		got, err := store.GradingQuality(context.Background(), "tenant-a", "exam-a")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("quality = %#v, %v; want %#v", got, err, want)
		}
	}
}

func TestBatch28QualityEmptyAndConfiguredSegmentCount(t *testing.T) {
	store := NewMemoryStore()
	store.SetQuality(QualitySeed{TotalSegments: 7})
	want := GradingQualityReport{
		ExamID: "empty", TotalSegments: 7,
		AIAdoptionRate:        Metric{Available: false, Reason: "no_ai_grades"},
		HumanModificationRate: Metric{Available: false, Reason: "no_ai_human_comparison"},
		AverageDoubleMarkDiff: Metric{Available: false, Reason: "no_double_mark_sessions"},
		MaxDoubleMarkDiff:     Metric{Available: false, Reason: "no_double_mark_sessions"},
		OCRFailureRate:        Metric{Available: false, Reason: "no_ocr_tasks"},
	}
	got, err := store.GradingQuality(context.Background(), "tenant-a", "empty")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("empty quality = %#v, %v; want %#v", got, err, want)
	}
}
