package report

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
)

func TestExportLeavesUnavailableQualityMetricEmptyWithReason(t *testing.T) {
	result := buildExportCSV("tenant-a", "exam-a", "teacher-a", OverviewReport{}, nil, nil, GradingQualityReport{
		AIAdoptionRate: Metric{Available: false, Reason: "no_ai_grades"},
		OCRFailureRate: Metric{Available: true, Value: 0, Denominator: 4},
	})
	rows, err := csv.NewReader(strings.NewReader(string(result.Content))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, row := range rows[1:] {
		if len(row) >= 3 {
			values[row[1]] = row[2]
		}
	}
	if values["ai_adoption_rate"] != "" || values["ai_adoption_rate_available"] != "false" || values["ai_adoption_rate_reason"] != "no_ai_grades" {
		t.Fatalf("unavailable metric must remain distinguishable from zero: %#v", values)
	}
	if values["ocr_failure_rate"] != "0.00" || values["ocr_failure_rate_available"] != "true" {
		t.Fatalf("available zero metric lost its meaning: %#v", values)
	}
	if values["ai_adoption_rate_numerator"] != "0" || values["ai_adoption_rate_denominator"] != "0" || values["ocr_failure_rate_numerator"] != "0" || values["ocr_failure_rate_denominator"] != "4" {
		t.Fatalf("export lost sample counts: %#v", values)
	}
}

// 通过公开报表和导出方法生成指标，区分人工考试的无样本与已有样本中的真实零值。
func TestMemoryExportQualityAvailabilityMatchesSourceMetrics(t *testing.T) {
	for _, tc := range []struct {
		name        string
		aiScore     *float64
		source      string
		ocrTasks    int
		ocrFailures int
		wantAI      string
		wantOCR     string
	}{
		{name: "manual-without-ai-or-ocr", source: "single_review"},
		{name: "measured-zero", aiScore: floatPointer(4), source: "single_review", ocrTasks: 4, wantAI: "0.00", wantOCR: "0.00"},
		{name: "measured-nonzero", aiScore: floatPointer(4), source: "rule_auto", ocrTasks: 4, ocrFailures: 1, wantAI: "1.00", wantOCR: "0.25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.AddSubmission(SubmissionSeed{ID: "submission", ExamID: "exam", StudentID: "student", TotalScore: 4, MaxScore: 5})
			store.AddGrade(GradeSeed{SubmissionID: "submission", StudentID: "student", QuestionID: "question", Score: 4, MaxScore: 5, Source: tc.source, AIScore: tc.aiScore})
			store.SetQuality(QualitySeed{OCRTaskCount: tc.ocrTasks, OCRFailedCount: tc.ocrFailures})
			quality, err := store.GradingQuality(context.Background(), "tenant", "exam")
			if err != nil {
				t.Fatal(err)
			}
			result, err := store.Export(context.Background(), "tenant", "exam", "actor")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(strings.NewReader(string(result.Content))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, row := range rows[1:] {
				if len(row) == 4 && row[0] == "grading_quality" {
					values[row[1]] = row[2]
				}
			}
			for _, metric := range []struct {
				key  string
				data Metric
				want string
			}{{"ai_adoption_rate", quality.AIAdoptionRate, tc.wantAI}, {"ocr_failure_rate", quality.OCRFailureRate, tc.wantOCR}} {
				if values[metric.key] != metric.want || values[metric.key+"_available"] != fmt.Sprintf("%t", metric.data.Available) || values[metric.key+"_numerator"] != fmt.Sprintf("%d", metric.data.Numerator) || values[metric.key+"_denominator"] != fmt.Sprintf("%d", metric.data.Denominator) || values[metric.key+"_reason"] != metric.data.Reason {
					t.Fatalf("CSV %s diverged from source metric %+v: %#v", metric.key, metric.data, values)
				}
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }
