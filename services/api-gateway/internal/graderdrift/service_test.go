package graderdrift

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

type observationReader struct{ items []seedquality.Observation }

func (r observationReader) ListObservations(_ context.Context, _ string, filter seedquality.ObservationFilter) ([]seedquality.Observation, error) {
	result := []seedquality.Observation{}
	for _, item := range r.items {
		if filter.ExamID != "" && item.ExamID != filter.ExamID || filter.QuestionID != "" && item.QuestionID != filter.QuestionID || filter.GraderID != "" && item.GraderID != filter.GraderID {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

type pauser struct{ calls []string }

func (p *pauser) SuspendForQualityIncident(_ context.Context, _, examID, questionID, graderID, reason string) error {
	p.calls = append(p.calls, examID+":"+questionID+":"+graderID+":"+reason)
	return nil
}

func TestRecomputeStableAndInsufficientWindowsNeverCreateFalseIncidents(t *testing.T) {
	items := makeObservations(20, func(_ int) (float64, float64, float64) { return 6, 6, 12 })
	service := NewService(NewMemoryStore(), observationReader{items: items})
	service.now = func() time.Time { return time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC) }

	result, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CreatedIncidents) != 0 {
		t.Fatalf("stable quality must not create incidents: %+v", result.CreatedIncidents)
	}
	var short, long *QualityWindow
	for i := range result.Windows {
		if result.Windows[i].WindowSize == shortWindow {
			short = &result.Windows[i]
		}
		if result.Windows[i].WindowSize == longWindow {
			long = &result.Windows[i]
		}
	}
	if short == nil || short.Status != WindowStable || short.MiddleScoreSampleCount != 20 || short.MiddleScoreMAE == nil || *short.MiddleScoreMAE != 0 {
		t.Fatalf("short window=%+v", short)
	}
	if long == nil || long.Status != WindowInsufficientData || long.EWMABias != nil {
		t.Fatalf("long window must remain insufficient: %+v", long)
	}
}

func TestRecomputeDetectsGradualBiasAndOnlySuspendsQuestionQualification(t *testing.T) {
	items := makeObservations(20, func(_ int) (float64, float64, float64) { return 6, 6, 12 })
	items = append(items, makeObservationsAt(25, len(items), func(_ int) (float64, float64, float64) { return 10.5, 6, 12 })...)
	pause := &pauser{}
	service := NewService(NewMemoryStore(), observationReader{items: items}, pause)
	service.now = func() time.Time { return time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC) }

	result, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a", GraderID: "grader-a"})
	if err != nil {
		t.Fatal(err)
	}
	seenCriticalBias := false
	for _, incident := range result.CreatedIncidents {
		if incident.Type == IncidentBiasHigh && incident.Severity == SeverityCritical {
			seenCriticalBias = true
		}
		if _, leaked := incident.MetricSnapshot["reference_score"]; leaked {
			t.Fatalf("incident leaked Gold reference: %+v", incident)
		}
	}
	if !seenCriticalBias || len(pause.calls) == 0 || pause.calls[0] != "exam-a:question-a:grader-a:grader_bias_high" {
		t.Fatalf("critical bias / narrow suspension missing: incidents=%+v pauses=%v", result.CreatedIncidents, pause.calls)
	}
	// 重复执行确定性刷新可以更新窗口，但不得重复创建事件或反复暂停资格。
	again, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a", GraderID: "grader-a"})
	if err != nil || len(again.CreatedIncidents) != 0 || len(pause.calls) != 1 {
		t.Fatalf("recompute must be idempotent: result=%+v err=%v pauses=%v", again, err, pause.calls)
	}
}

func TestRecomputeSeparatesInconsistencyFromDirectionalBias(t *testing.T) {
	items := makeObservations(20, func(index int) (float64, float64, float64) {
		if index%2 == 0 {
			return 8, 6, 12
		}
		return 4, 6, 12
	})
	service := NewService(NewMemoryStore(), observationReader{items: items})
	result, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, incident := range result.CreatedIncidents {
		if incident.Type == IncidentHighInconsistency {
			return
		}
	}
	t.Fatalf("alternating mistakes should be an inconsistency alert, got %+v", result.CreatedIncidents)
}

func makeObservations(count int, values func(int) (submitted, reference, max float64)) []seedquality.Observation {
	return makeObservationsAt(count, 0, values)
}

func makeObservationsAt(count, offset int, values func(int) (submitted, reference, max float64)) []seedquality.Observation {
	result := make([]seedquality.Observation, 0, count)
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	for index := 0; index < count; index++ {
		submitted, reference, max := values(index)
		errorValue := submitted - reference
		if errorValue < 0 {
			errorValue = -errorValue
		}
		result = append(result, seedquality.Observation{ID: string(rune('a' + offset + index)), ExamID: "exam-a", QuestionID: "question-a", GraderID: "grader-a",
			SubmittedScore: submitted, ReferenceScore: reference, MaxScore: max, AbsoluteError: errorValue,
			ObservedAt: base.Add(time.Duration(offset+index) * time.Minute)})
	}
	return result
}
