package graderdrift

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

type batch18ObservationReader struct{ items []seedquality.Observation }

func (r batch18ObservationReader) ListObservations(context.Context, string, seedquality.ObservationFilter) ([]seedquality.Observation, error) {
	return r.items, nil
}

type batch18RecordingStore struct {
	*MemoryStore
	events       *[]string
	upserts      int
	creates      int
	failUpsertAt int
	failCreateAt int
	failure      error
}

func (s *batch18RecordingStore) UpsertWindow(ctx context.Context, tenantID string, window QualityWindow) (QualityWindow, error) {
	s.upserts++
	*s.events = append(*s.events, "upsert")
	if s.upserts == s.failUpsertAt {
		return QualityWindow{}, s.failure
	}
	return s.MemoryStore.UpsertWindow(ctx, tenantID, window)
}

func (s *batch18RecordingStore) CreateIncidentsIfMissing(ctx context.Context, tenantID string, incidents []Incident) ([]Incident, error) {
	s.creates++
	*s.events = append(*s.events, "create")
	if s.creates == s.failCreateAt {
		return nil, s.failure
	}
	return s.MemoryStore.CreateIncidentsIfMissing(ctx, tenantID, incidents)
}

type batch18FailingPauser struct {
	events *[]string
	calls  []string
}

func (p *batch18FailingPauser) SuspendForQualityIncident(_ context.Context, tenantID, examID, questionID, graderID, reason string) error {
	*p.events = append(*p.events, "pause:"+graderID)
	p.calls = append(p.calls, tenantID+":"+examID+":"+questionID+":"+graderID+":"+reason)
	return errors.New("pause unavailable")
}

func TestRecomputeBatch18KeepsSortClockAndFailedPauseScope(t *testing.T) {
	observed := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	items := makeBatch18SortedWindowObservations(observed)

	events := []string{}
	store := &batch18RecordingStore{MemoryStore: NewMemoryStore(), events: &events}
	pause := &batch18FailingPauser{events: &events}
	service := NewService(store, batch18ObservationReader{items: items}, pause)
	clockCalls := 0
	service.now = func() time.Time {
		clockCalls++
		return observed.Add(time.Duration(clockCalls) * time.Hour)
	}

	// 同一时间用 ID 排序，两个相邻窗口会得到不同均值；暂停报错仍只尝试一次题目作用域。
	result, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a"})
	if err != nil || len(result.Windows) != 6 || clockCalls != 4 {
		t.Fatalf("result=%+v err=%v clock calls=%d", result, err, clockCalls)
	}
	wantGraders := []string{"grader-a", "grader-a", "grader-a", "grader-b", "grader-b", "grader-b"}
	wantSizes := []int{20, 20, 50, 20, 20, 50}
	wantClocks := []int{1, 1, 2, 3, 3, 4}
	for index, window := range result.Windows {
		if window.GraderID != wantGraders[index] || window.WindowSize != wantSizes[index] || !window.ComputedAt.Equal(observed.Add(time.Duration(wantClocks[index])*time.Hour)) {
			t.Fatalf("window order / clock changed at %d: %+v", index, window)
		}
	}
	assertBatch18Float(t, result.Windows[0].MeanError, 5.5)
	assertBatch18Float(t, result.Windows[1].MeanError, 6)
	wantPauses := []string{"tenant-a:exam-a:question-a:grader-a:grader_bias_high", "tenant-a:exam-a:question-a:grader-b:grader_bias_high"}
	if len(result.CreatedIncidents) != 6 || !reflect.DeepEqual(pause.calls, wantPauses) || len(events) < 3 || !reflect.DeepEqual(events[:3], []string{"upsert", "create", "pause:grader-a"}) {
		t.Fatalf("incident / pause order changed: incidents=%+v pauses=%v events=%v", result.CreatedIncidents, pause.calls, events)
	}
	again, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a"})
	if err != nil || len(again.CreatedIncidents) != 0 || !reflect.DeepEqual(pause.calls, wantPauses) {
		t.Fatalf("persisted events must not retry failed pauses: result=%+v err=%v calls=%v", again, err, pause.calls)
	}
}

func TestRecomputeBatch18KeepsPartialPersistenceOnFailure(t *testing.T) {
	cases := []struct {
		name         string
		failUpsertAt int
		failCreateAt int
		clockCalls   int
		events       []string
	}{
		{name: "later window fails", failUpsertAt: 2, clockCalls: 2, events: []string{"upsert", "create", "pause:grader-a", "upsert"}},
		{name: "event creation fails", failCreateAt: 1, clockCalls: 1, events: []string{"upsert", "create"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			failure := errors.New("store unavailable")
			store := &batch18RecordingStore{MemoryStore: NewMemoryStore(), events: &events, failUpsertAt: test.failUpsertAt, failCreateAt: test.failCreateAt, failure: failure}
			pause := &batch18FailingPauser{events: &events}
			items := makeObservations(20, func(int) (float64, float64, float64) { return 12, 6, 12 })
			service := NewService(store, batch18ObservationReader{items: items}, pause)
			clockCalls := 0
			service.now = func() time.Time { clockCalls++; return time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC) }
			// 写事件和后续窗口没有包在同一事务里；失败只清空返回值，不撤销已有窗口。
			result, err := service.Recompute(context.Background(), "tenant-a", RefreshInput{ExamID: "exam-a", QuestionID: "question-a"})
			if !errors.Is(err, failure) || !reflect.DeepEqual(result, RefreshResult{}) || clockCalls != test.clockCalls || !reflect.DeepEqual(events, test.events) {
				t.Fatalf("failure boundary changed: result=%+v err=%v clock=%d events=%v", result, err, clockCalls, events)
			}
			windows, err := store.ListWindows(context.Background(), "tenant-a", WindowFilter{ExamID: "exam-a", QuestionID: "question-a"})
			if err != nil || len(windows) != 1 || windows[0].WindowSize != shortWindow {
				t.Fatalf("previous window was lost: windows=%+v err=%v", windows, err)
			}
		})
	}
}

func TestCalculateWindowBatch18KeepsMetricsForIncompleteSamples(t *testing.T) {
	rubricLow, rubricHigh, prior := .2, .8, -.25
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.FixedZone("test", 8*60*60))
	items := []seedquality.Observation{
		{SubmittedScore: 1, ReferenceScore: 0, MaxScore: 0, RubricAgreement: &rubricLow, ObservedAt: now},
		{SubmittedScore: 1, ReferenceScore: 1, MaxScore: 0, ObservedAt: now.Add(time.Minute)},
		{SubmittedScore: 2, ReferenceScore: 1, MaxScore: 4, RubricAgreement: &rubricHigh, ObservedAt: now.Add(2 * time.Minute)},
	}
	for _, size := range []int{3, 4} {
		window := calculateWindow("exam-a", "question-a", "grader-a", items, size, &prior, now)
		assertBatch18Float(t, window.MeanError, 2.0/3)
		assertBatch18Float(t, window.MAE, 2.0/3)
		assertBatch18Float(t, window.ExactAgreement, 1.0/3)
		assertBatch18Float(t, window.SevereRate, 1.0/3)
		if window.RubricAgreement == nil || window.MiddleScoreMAE == nil || window.MiddleScoreExactAgreement == nil || window.MiddleScoreSampleCount != 1 || window.ComputedAt.Location() != time.UTC {
			t.Fatalf("optional metrics / UTC changed: %+v", window)
		}
		assertBatch18Float(t, *window.RubricAgreement, .5)
		assertBatch18Float(t, *window.MiddleScoreMAE, 1)
		assertBatch18Float(t, *window.MiddleScoreExactAgreement, 0)
		assertBatch18WindowTrend(t, window, size == 3, .2*(1.25/3)+.8*prior)
	}
}

func assertBatch18WindowTrend(t *testing.T, window QualityWindow, complete bool, ewma float64) {
	t.Helper()
	if !complete {
		if window.Status != WindowInsufficientData || window.EWMABias != nil || len(incidentsFor(window)) != 0 {
			t.Fatalf("incomplete samples must not emit trends or incidents: %+v", window)
		}
		return
	}
	if window.Status != WindowCritical || window.EWMABias == nil {
		t.Fatalf("full sample trend changed: %+v", window)
	}
	assertBatch18Float(t, *window.EWMABias, ewma)
}

func TestIncidentsForBatch18PreservesThresholdsAndSnapshotIsolation(t *testing.T) {
	rubricBoundary, rubricCritical, rubricStable := .45, .449, .70
	cases := []struct {
		name       string
		bias       float64
		severeRate float64
		mae        float64
		rubric     *float64
		kind       IncidentType
		severity   Severity
	}{
		{name: "bias below warning", bias: .199},
		{name: "high warning boundary", bias: .20, kind: IncidentBiasHigh, severity: SeverityWarning},
		{name: "high critical boundary", bias: .35, kind: IncidentBiasHigh, severity: SeverityCritical},
		{name: "low warning boundary", bias: -.20, kind: IncidentBiasLow, severity: SeverityWarning},
		{name: "low critical boundary", bias: -.35, kind: IncidentBiasLow, severity: SeverityCritical},
		{name: "severe below warning", severeRate: .149},
		{name: "severe warning boundary", severeRate: .15, kind: IncidentSevereSeedFailure, severity: SeverityWarning},
		{name: "severe critical boundary", severeRate: .25, kind: IncidentSevereSeedFailure, severity: SeverityCritical},
		{name: "rubric warning boundary", rubric: &rubricStable},
		{name: "rubric critical boundary", rubric: &rubricBoundary, kind: IncidentRubricDisagreement, severity: SeverityWarning},
		{name: "rubric below critical", rubric: &rubricCritical, kind: IncidentRubricDisagreement, severity: SeverityCritical},
		{name: "inconsistency boundary", bias: .099, mae: 1.25, kind: IncidentHighInconsistency, severity: SeverityWarning},
		{name: "inconsistency bias boundary", bias: .10, mae: 1.25},
		{name: "inconsistency below MAE", mae: 1.249},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			window := QualityWindow{WindowSize: 20, SampleCount: 20, EWMABias: &test.bias, SevereRate: test.severeRate, MAE: test.mae, RubricAgreement: test.rubric}
			assertBatch18Incident(t, incidentsFor(window), test.kind, test.severity)
		})
	}
	bias, middleMAE := .35, 2.0
	window := QualityWindow{ID: "window-a", ExamID: "exam-a", QuestionID: "question-a", GraderID: "grader-a", WindowSize: 20, SampleCount: 20,
		EWMABias: &bias, SevereRate: .25, RubricAgreement: &rubricCritical, MiddleScoreMAE: &middleMAE, MiddleScoreSampleCount: 10}
	incidents := incidentsFor(window)
	wantKinds := []IncidentType{IncidentBiasHigh, IncidentSevereSeedFailure, IncidentRubricDisagreement}
	if len(incidents) != len(wantKinds) {
		t.Fatalf("candidate count changed: %+v", incidents)
	}
	for index, incident := range incidents {
		if incident.Type != wantKinds[index] || incident.Severity != SeverityCritical || incident.SourceWindowID != window.ID {
			t.Fatalf("candidate order / source changed: %+v", incidents)
		}
	}
	incidents[0].MetricSnapshot["sample_count"] = -1
	incidents[0].AffectedRange["window_size"] = -1
	if incidents[1].MetricSnapshot["sample_count"] != 20 || incidents[1].AffectedRange["window_size"] != 20 || incidents[1].MetricSnapshot["middle_score_mae"] != middleMAE {
		t.Fatalf("candidate snapshots share mutable maps or lost optional metrics: %+v", incidents)
	}
}

func assertBatch18Incident(t *testing.T, incidents []Incident, kind IncidentType, severity Severity) {
	t.Helper()
	if kind == "" {
		if len(incidents) != 0 {
			t.Fatalf("unexpected incidents: %+v", incidents)
		}
		return
	}
	if len(incidents) != 1 || incidents[0].Type != kind || incidents[0].Severity != severity {
		t.Fatalf("incident boundary changed: %+v", incidents)
	}
}

func assertBatch18Float(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("metric changed: got=%v want=%v", got, want)
	}
}

// 两位阅卷者的观测时间相同、ID逆序；首个异常分数用来区分相邻窗口。
func makeBatch18SortedWindowObservations(observed time.Time) []seedquality.Observation {
	items := []seedquality.Observation{}
	rubric := .4
	for _, graderID := range []string{"grader-b", "grader-a"} {
		for index := 20; index >= 0; index-- {
			score := 10.0
			if index == 0 {
				score = 0
			}
			items = append(items, seedquality.Observation{ID: fmt.Sprintf("%02d", index), ExamID: "exam-a", QuestionID: "question-a", GraderID: graderID,
				SubmittedScore: score, ReferenceScore: 4, MaxScore: 10, RubricAgreement: &rubric, ObservedAt: observed})
		}
	}
	items = append(items,
		seedquality.Observation{ExamID: "other-exam", QuestionID: "question-a", GraderID: "grader-c"},
		seedquality.Observation{ExamID: "exam-a", QuestionID: "other-question", GraderID: "grader-c"},
		seedquality.Observation{ExamID: "exam-a", QuestionID: "question-a"},
	)
	return items
}
