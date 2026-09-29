package graderdrift

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

const (
	shortWindow = 20
	longWindow  = 50
	ewmaAlpha   = .20
)

// ObservationReader is deliberately narrower than the Seed service. Drift
// receives scored observations, never task images or Gold-paper material.
type ObservationReader interface {
	ListObservations(context.Context, string, seedquality.ObservationFilter) ([]seedquality.Observation, error)
}

// QualificationPauser is optional. A composition root may connect A09 here so
// a newly-created critical incident only stops this grader on this question.
// The interface does not grant A11 any broader role or exam-level suspension.
type QualificationPauser interface {
	SuspendForQualityIncident(context.Context, string, string, string, string, string) error
}

type Service struct {
	store  Store
	seeds  ObservationReader
	pauser QualificationPauser
	now    func() time.Time
}

func NewService(store Store, seeds ObservationReader, pausers ...QualificationPauser) *Service {
	s := &Service{store: store, seeds: seeds, now: func() time.Time { return time.Now().UTC() }}
	if len(pausers) > 0 {
		s.pauser = pausers[0]
	}
	return s
}

// Recompute is deterministic and idempotent. It keeps both 20 and 50 item
// rolling windows when data is available; an undersized cohort is recorded as
// insufficient_data and can never create a warning or critical incident.
func (s *Service) Recompute(ctx context.Context, tenantID string, input RefreshInput) (RefreshResult, error) {
	if s.store == nil || s.seeds == nil || strings.TrimSpace(tenantID) == "" ||
		strings.TrimSpace(input.ExamID) == "" || strings.TrimSpace(input.QuestionID) == "" {
		return RefreshResult{}, ErrInvalidInput
	}
	observations, err := s.seeds.ListObservations(ctx, tenantID, seedquality.ObservationFilter{
		ExamID: input.ExamID, QuestionID: input.QuestionID, GraderID: input.GraderID, Limit: 500,
	})
	if err != nil {
		return RefreshResult{}, err
	}
	byGrader := map[string][]seedquality.Observation{}
	for _, item := range observations {
		if item.ExamID != input.ExamID || item.QuestionID != input.QuestionID || item.GraderID == "" {
			continue
		}
		byGrader[item.GraderID] = append(byGrader[item.GraderID], item)
	}
	graderIDs := make([]string, 0, len(byGrader))
	for graderID := range byGrader {
		graderIDs = append(graderIDs, graderID)
	}
	sort.Strings(graderIDs)

	result := RefreshResult{Windows: []QualityWindow{}, CreatedIncidents: []Incident{}}
	pausedScopes := map[string]bool{}
	for _, graderID := range graderIDs {
		items := byGrader[graderID]
		sort.Slice(items, func(i, j int) bool {
			if items[i].ObservedAt.Equal(items[j].ObservedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].ObservedAt.Before(items[j].ObservedAt)
		})
		for _, size := range []int{shortWindow, longWindow} {
			calculated := calculateRolling(input.ExamID, input.QuestionID, graderID, items, size, s.now())
			for _, window := range calculated {
				stored, err := s.store.UpsertWindow(ctx, tenantID, window)
				if err != nil {
					return RefreshResult{}, err
				}
				result.Windows = append(result.Windows, stored)
				created, err := s.store.CreateIncidentsIfMissing(ctx, tenantID, incidentsFor(stored))
				if err != nil {
					return RefreshResult{}, err
				}
				result.CreatedIncidents = append(result.CreatedIncidents, created...)
				if s.pauser != nil {
					for _, incident := range created {
						pauseKey := incident.ExamID + "\x00" + incident.QuestionID + "\x00" + incident.GraderID
						if incident.Severity == SeverityCritical && !pausedScopes[pauseKey] {
							// A11 must not suspend a teacher for another question.
							_ = s.pauser.SuspendForQualityIncident(ctx, tenantID, incident.ExamID, incident.QuestionID, incident.GraderID, string(incident.Type))
							pausedScopes[pauseKey] = true
						}
					}
				}
			}
		}
	}
	return result, nil
}

// RefreshSeedObservation is the deliberately narrow hook used by the review
// composition root after a blind Seed submission succeeds. A quality refresh
// is observational: a failure here must never undo the submitted observation.
func (s *Service) RefreshSeedObservation(ctx context.Context, tenantID, examID, questionID, graderID string) error {
	_, err := s.Recompute(ctx, tenantID, RefreshInput{
		ExamID: examID, QuestionID: questionID, GraderID: graderID,
	})
	return err
}

func (s *Service) ListWindows(ctx context.Context, tenantID string, filter WindowFilter) ([]QualityWindow, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(filter.ExamID) == "" || strings.TrimSpace(filter.QuestionID) == "" {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	return s.store.ListWindows(ctx, tenantID, filter)
}

func (s *Service) ListIncidents(ctx context.Context, tenantID string, filter IncidentFilter) ([]Incident, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	return s.store.ListIncidents(ctx, tenantID, filter)
}

func (s *Service) ResolveIncident(ctx context.Context, tenantID, incidentID string) (Incident, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(incidentID) == "" {
		return Incident{}, ErrInvalidInput
	}
	return s.store.ResolveIncident(ctx, tenantID, incidentID, s.now())
}

func calculateRolling(examID, questionID, graderID string, observations []seedquality.Observation, size int, now time.Time) []QualityWindow {
	if len(observations) == 0 {
		return nil
	}
	if len(observations) < size {
		return []QualityWindow{calculateWindow(examID, questionID, graderID, observations, size, nil, now)}
	}
	result := make([]QualityWindow, 0, len(observations)-size+1)
	var prior *float64
	for end := size; end <= len(observations); end++ {
		window := calculateWindow(examID, questionID, graderID, observations[end-size:end], size, prior, now)
		prior = window.EWMABias
		result = append(result, window)
	}
	return result
}

// 窗口不足时只记录样本量，不计算趋势状态；完整窗口才更新 EWMA 并触发预警阈值。
func calculateWindow(examID, questionID, graderID string, items []seedquality.Observation, size int, prior *float64, now time.Time) QualityWindow {
	window := QualityWindow{ExamID: examID, QuestionID: questionID, GraderID: graderID, WindowSize: size,
		SampleCount: len(items), Status: WindowInsufficientData, ComputedAt: now.UTC()}
	if len(items) == 0 {
		return window
	}
	window.WindowStart, window.WindowEnd = items[0].ObservedAt.UTC(), items[len(items)-1].ObservedAt.UTC()
	var exact, severe, rubricN, middleN, middleExact int
	var rubricTotal, middleMAE, normalizedBias float64
	for _, item := range items {
		signed := item.SubmittedScore - item.ReferenceScore
		window.MeanError += signed
		window.MAE += math.Abs(signed)
		if math.Abs(signed) < 1e-9 {
			exact++
		}
		maxScore := item.MaxScore
		if maxScore <= 0 {
			maxScore = math.Max(item.ReferenceScore, 1)
		}
		normalizedBias += signed / maxScore
		if math.Abs(signed) >= math.Max(1, maxScore*.4) {
			severe++
		}
		if item.RubricAgreement != nil {
			rubricN++
			rubricTotal += *item.RubricAgreement
		}
		// A dedicated partial-correct slice prevents 0/full-score items from
		// hiding a middle-band regression.
		if item.ReferenceScore > 1e-9 && item.ReferenceScore < maxScore-1e-9 {
			middleN++
			middleMAE += math.Abs(signed)
			if math.Abs(signed) < 1e-9 {
				middleExact++
			}
		}
	}
	n := float64(len(items))
	window.MeanError /= n
	window.MAE /= n
	window.ExactAgreement = float64(exact) / n
	window.SevereRate = float64(severe) / n
	normalizedBias /= n
	window.MiddleScoreSampleCount = middleN
	if rubricN > 0 {
		value := rubricTotal / float64(rubricN)
		window.RubricAgreement = &value
	}
	if middleN > 0 {
		mae := middleMAE / float64(middleN)
		exactAgreement := float64(middleExact) / float64(middleN)
		window.MiddleScoreMAE, window.MiddleScoreExactAgreement = &mae, &exactAgreement
	}
	if len(items) < size {
		return window
	}
	ewma := normalizedBias
	if prior != nil {
		ewma = ewmaAlpha*normalizedBias + (1-ewmaAlpha)*(*prior)
	}
	window.EWMABias = &ewma
	window.Status = statusFor(window, normalizedBias)
	return window
}

func statusFor(window QualityWindow, normalizedBias float64) WindowStatus {
	if window.SampleCount < window.WindowSize {
		return WindowInsufficientData
	}
	bias := normalizedBias
	if window.EWMABias != nil {
		bias = *window.EWMABias
	}
	critical := math.Abs(bias) >= .35 || window.SevereRate >= .25 || (window.RubricAgreement != nil && *window.RubricAgreement < .45)
	if critical {
		return WindowCritical
	}
	warning := math.Abs(bias) >= .20 || window.SevereRate >= .15 || window.MAE >= 1 ||
		(window.RubricAgreement != nil && *window.RubricAgreement < .70)
	if warning {
		return WindowWarning
	}
	return WindowStable
}

// 事件由窗口快照幂等生成，偏差、严重错误率和 rubric 一致率分别保留，便于复核触发原因。
func incidentsFor(window QualityWindow) []Incident {
	if window.Status == WindowInsufficientData || window.SampleCount < window.WindowSize || window.EWMABias == nil {
		return nil
	}
	snapshot := map[string]any{
		"sample_count": window.SampleCount, "mean_error": window.MeanError, "mae": window.MAE,
		"exact_agreement": window.ExactAgreement, "severe_rate": window.SevereRate,
		"ewma_normalized_bias": *window.EWMABias, "middle_score_sample_count": window.MiddleScoreSampleCount,
	}
	if window.RubricAgreement != nil {
		snapshot["rubric_agreement"] = *window.RubricAgreement
	}
	if window.MiddleScoreMAE != nil {
		snapshot["middle_score_mae"] = *window.MiddleScoreMAE
	}
	rangeJSON := map[string]any{"window_start": window.WindowStart, "window_end": window.WindowEnd, "window_size": window.WindowSize}
	result := []Incident{}
	appendIncident := func(kind IncidentType, severity Severity) {
		result = append(result, Incident{ExamID: window.ExamID, QuestionID: window.QuestionID, GraderID: window.GraderID,
			SourceWindowID: window.ID, Type: kind, Severity: severity, MetricSnapshot: cloneMap(snapshot),
			AffectedRange: cloneMap(rangeJSON), Status: IncidentOpen, CreatedAt: window.ComputedAt})
	}
	bias := *window.EWMABias
	if bias >= .20 {
		severity := SeverityWarning
		if bias >= .35 {
			severity = SeverityCritical
		}
		appendIncident(IncidentBiasHigh, severity)
	}
	if bias <= -.20 {
		severity := SeverityWarning
		if bias <= -.35 {
			severity = SeverityCritical
		}
		appendIncident(IncidentBiasLow, severity)
	}
	if window.SevereRate >= .15 {
		severity := SeverityWarning
		if window.SevereRate >= .25 {
			severity = SeverityCritical
		}
		appendIncident(IncidentSevereSeedFailure, severity)
	}
	if math.Abs(bias) < .10 && window.MAE >= 1.25 {
		appendIncident(IncidentHighInconsistency, SeverityWarning)
	}
	if window.RubricAgreement != nil && *window.RubricAgreement < .70 {
		severity := SeverityWarning
		if *window.RubricAgreement < .45 {
			severity = SeverityCritical
		}
		appendIncident(IncidentRubricDisagreement, severity)
	}
	return result
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
