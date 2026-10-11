package scorerelease

import (
	"math"
	"reflect"
	"testing"
)

func TestBatch27StudentReferenceKeepsPrivacySwitchesAndPositionComparisons(t *testing.T) {
	items := []ReleaseItem{}
	for _, score := range []float64{10, 5.0000005, 4.9999995, 5, 0, 9, 8, 7, 6, 1} {
		items = append(items, ReleaseItem{StudentID: "synthetic", TotalScore: score})
	}
	items = append(items, ReleaseItem{TotalScore: 100000})
	before := append([]ReleaseItem(nil), items...)
	if got := buildStudentReference(items, 5, VisibilityPolicy{}); got != nil {
		t.Fatalf("disabled statistics exposed facts: %#v", got)
	}
	positionPolicy := VisibilityPolicy{ShowPercentile: true, ShowExactRank: true}
	got := buildStudentReference(items, 5, positionPolicy)
	if got == nil || !got.StatisticsAvailable || got.SampleSize != 10 || got.Scope != "exam" || got.Percentile == nil || *got.Percentile != 40 ||
		got.Rank == nil || *got.Rank != 7 || got.MeanScore != nil || got.MedianScore != nil || got.Q1 != nil || got.MinScore != nil || !reflect.DeepEqual(items, before) {
		t.Fatalf("position comparisons, switches or input changed: %#v", got)
	}
	statistics := buildStudentReference(items, 5, VisibilityPolicy{ShowCohortStatistics: true, ShowScoreDistribution: true})
	if statistics == nil || statistics.MeanScore == nil || statistics.MedianScore == nil || math.Abs(*statistics.MedianScore-5.50000025) > 1e-9 ||
		statistics.MinScore == nil || *statistics.MinScore != 0 || statistics.MaxScore == nil || *statistics.MaxScore != 10 ||
		statistics.Q1 == nil || statistics.Q3 == nil || statistics.Percentile != nil || statistics.Rank != nil {
		t.Fatalf("released statistics or hidden position changed: %#v", statistics)
	}
	// 低于人数门槛时只保留不可用标记，不能因排名或分位开关绕过隐私限制。
	small := buildStudentReference(append(items[:9:9], items[10]), 5, positionPolicy)
	if small == nil || small.SampleSize != 9 || small.StatisticsAvailable || small.UnavailableReason != "small_cohort" || small.Percentile != nil || small.Rank != nil {
		t.Fatalf("small cohort leaked facts: %#v", small)
	}
}
