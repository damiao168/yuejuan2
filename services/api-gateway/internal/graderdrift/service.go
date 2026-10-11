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

// ObservationReader 比 Seed 服务的能力更窄；漂移模块接收已评分观察，不接收任务图片或 Gold Paper 材料。
type ObservationReader interface {
	ListObservations(context.Context, string, seedquality.ObservationFilter) ([]seedquality.Observation, error)
}

// QualificationPauser 为可选依赖；组装入口可在此连接 A09，使新建的严重事件只暂停该阅卷者在该题的工作。
// 此接口不授予 A11 更广的角色或考试级暂停权。
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

// Recompute 按固定顺序计算 20 项和 50 项滚动窗口；存储层按窗口和事件类型去重。
// 样本不足时仍保留聚合指标，但不能创建警告或严重事件；中途失败不会回滚此前已写入的窗口。
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
	byGrader, graderIDs := groupObservations(input, observations)

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
			if err := s.storeWindows(ctx, tenantID, calculated, &result, pausedScopes); err != nil {
				return RefreshResult{}, err
			}
		}
	}
	return result, nil
}

func groupObservations(input RefreshInput, observations []seedquality.Observation) (map[string][]seedquality.Observation, []string) {
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
	return byGrader, graderIDs
}

func (s *Service) storeWindows(ctx context.Context, tenantID string, windows []QualityWindow, result *RefreshResult, pausedScopes map[string]bool) error {
	for _, window := range windows {
		// 先取得持久化窗口的 ID，再创建事件；只有新建事件才尝试暂停该题资格。
		stored, err := s.store.UpsertWindow(ctx, tenantID, window)
		if err != nil {
			return err
		}
		result.Windows = append(result.Windows, stored)
		created, err := s.store.CreateIncidentsIfMissing(ctx, tenantID, incidentsFor(stored))
		if err != nil {
			return err
		}
		result.CreatedIncidents = append(result.CreatedIncidents, created...)
		s.pauseCriticalIncidents(ctx, tenantID, created, pausedScopes)
	}
	return nil
}

func (s *Service) pauseCriticalIncidents(ctx context.Context, tenantID string, incidents []Incident, pausedScopes map[string]bool) {
	if s.pauser == nil {
		return
	}
	for _, incident := range incidents {
		pauseKey := incident.ExamID + "\x00" + incident.QuestionID + "\x00" + incident.GraderID
		if incident.Severity == SeverityCritical && !pausedScopes[pauseKey] {
			// A11 只处理事件指定的题目资格；暂停失败不撤销事件，本次刷新也不重试同一作用域。
			_ = s.pauser.SuspendForQualityIncident(ctx, tenantID, incident.ExamID, incident.QuestionID, incident.GraderID, string(incident.Type))
			pausedScopes[pauseKey] = true
		}
	}
}

// RefreshSeedObservation 是盲投 Seed 提交成功后，复核组装入口调用的有限钩子。质量刷新只负责观察；此处失败不得撤销已提交观察。
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

// 窗口不足时仍计算已有样本的聚合指标；完整窗口才更新 EWMA 并触发预警阈值。
func calculateWindow(examID, questionID, graderID string, items []seedquality.Observation, size int, prior *float64, now time.Time) QualityWindow {
	window := QualityWindow{ExamID: examID, QuestionID: questionID, GraderID: graderID, WindowSize: size,
		SampleCount: len(items), Status: WindowInsufficientData, ComputedAt: now.UTC()}
	if len(items) == 0 {
		return window
	}
	window.WindowStart, window.WindowEnd = items[0].ObservedAt.UTC(), items[len(items)-1].ObservedAt.UTC()
	totals := sumWindowMetrics(items)
	n := float64(len(items))
	window.MeanError = totals.meanError / n
	window.MAE = totals.mae / n
	window.ExactAgreement = float64(totals.exact) / n
	window.SevereRate = float64(totals.severe) / n
	normalizedBias := totals.normalizedBias / n
	window.MiddleScoreSampleCount = totals.middleN
	if totals.rubricN > 0 {
		value := totals.rubricTotal / float64(totals.rubricN)
		window.RubricAgreement = &value
	}
	if totals.middleN > 0 {
		mae := totals.middleMAE / float64(totals.middleN)
		exactAgreement := float64(totals.middleExact) / float64(totals.middleN)
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

type windowTotals struct {
	meanError, mae, normalizedBias               float64
	exact, severe, rubricN, middleN, middleExact int
	rubricTotal, middleMAE                       float64
}

func sumWindowMetrics(items []seedquality.Observation) windowTotals {
	totals := windowTotals{}
	for _, item := range items {
		signed := item.SubmittedScore - item.ReferenceScore
		totals.meanError += signed
		totals.mae += math.Abs(signed)
		if math.Abs(signed) < 1e-9 {
			totals.exact++
		}
		maxScore := item.MaxScore
		if maxScore <= 0 {
			maxScore = math.Max(item.ReferenceScore, 1)
		}
		totals.normalizedBias += signed / maxScore
		if math.Abs(signed) >= math.Max(1, maxScore*.4) {
			totals.severe++
		}
		if item.RubricAgreement != nil {
			totals.rubricN++
			totals.rubricTotal += *item.RubricAgreement
		}
		// 单独统计部分正确的样本，防止零分或满分条目掩盖中间分段的退化。
		if item.ReferenceScore > 1e-9 && item.ReferenceScore < maxScore-1e-9 {
			totals.middleN++
			totals.middleMAE += math.Abs(signed)
			if math.Abs(signed) < 1e-9 {
				totals.middleExact++
			}
		}
	}
	return totals
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

// 同一完整窗口可以触发多种事件；每条候选独立复制聚合快照，存储层按窗口和事件类型去重。
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
		appendIncident(IncidentBiasHigh, incidentSeverity(bias >= .35))
	}
	if bias <= -.20 {
		appendIncident(IncidentBiasLow, incidentSeverity(bias <= -.35))
	}
	if window.SevereRate >= .15 {
		appendIncident(IncidentSevereSeedFailure, incidentSeverity(window.SevereRate >= .25))
	}
	if math.Abs(bias) < .10 && window.MAE >= 1.25 {
		appendIncident(IncidentHighInconsistency, SeverityWarning)
	}
	if window.RubricAgreement != nil && *window.RubricAgreement < .70 {
		appendIncident(IncidentRubricDisagreement, incidentSeverity(*window.RubricAgreement < .45))
	}
	return result
}

func incidentSeverity(critical bool) Severity {
	if critical {
		return SeverityCritical
	}
	return SeverityWarning
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
