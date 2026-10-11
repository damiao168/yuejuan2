package qualitydashboard

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

type Service struct {
	sources Sources
	now     func() time.Time
}

func NewService(sources Sources) *Service {
	return &Service{sources: sources, now: func() time.Time { return time.Now().UTC() }}
}

// Get 只汇总题目级聚合质量事实。可选来源缺失时显示数据不足，不假装质量检查已通过。
// 依赖失败仍向调用方暴露。
func (s *Service) Get(ctx context.Context, tenantID, examID string) (Dashboard, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(examID) == "" || s.sources.Questions == nil {
		return Dashboard{}, ErrInvalidInput
	}
	questions, err := s.sources.Questions.ListQualityQuestions(ctx, tenantID, examID)
	if err != nil {
		return Dashboard{}, err
	}
	if len(questions) == 0 {
		return Dashboard{}, ErrNotFound
	}
	out := Dashboard{ExamID: examID, Gate: GateReady, Blocking: []Finding{}, Warnings: []Finding{}, Questions: make([]QuestionQuality, 0, len(questions)), GeneratedAt: s.now().UTC()}
	for _, question := range questions {
		quality, err := s.question(ctx, tenantID, examID, question)
		if err != nil {
			return Dashboard{}, err
		}
		out.Questions = append(out.Questions, quality)
		for _, finding := range quality.Findings {
			switch finding.Status {
			case GateBlocked:
				out.Blocking = append(out.Blocking, finding)
			case GateWarning, GateInsufficientData:
				out.Warnings = append(out.Warnings, finding)
			}
		}
	}
	if len(out.Blocking) > 0 {
		out.Gate = GateBlocked
	} else if len(out.Warnings) > 0 {
		out.Gate = GateWarning
	}
	return out, nil
}

func (s *Service) question(ctx context.Context, tenantID, examID string, question Question) (QuestionQuality, error) {
	quality := QuestionQuality{
		Question:       question,
		Gate:           GateReady,
		Gold:           GoldSummary{Gaps: []string{}, ScoreBands: []Band{}},
		Calibration:    CalibrationSummary{Graders: []CalibrationGrader{}},
		Seed:           SeedSummary{Graders: []GraderSeedMetrics{}},
		HumanAgreement: HumanAgreementSummary{},
		AnswerGroups:   GroupSummary{},
		Drift:          DriftSummary{Incidents: []Incident{}},
		Backmark:       BackmarkSummary{},
		Findings:       []Finding{},
	}

	// 来源按业务顺序投影；非预期读取错误立即返回，缺失的可选证据由各来源形成 finding。
	if err := s.projectGold(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	if err := s.projectCalibration(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	if err := s.projectSeed(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	if s.sources.Review != nil {
		agreement, err := s.humanAgreement(ctx, tenantID, examID, question.ID)
		if err != nil {
			return QuestionQuality{}, err
		}
		quality.HumanAgreement = agreement
	}
	if err := s.projectAnswerGroups(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	if err := s.projectDrift(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	if err := s.projectBackmark(ctx, tenantID, examID, &quality); err != nil {
		return QuestionQuality{}, err
	}
	quality.Gate = gateFor(quality.Findings)
	return quality, nil
}

func (s *Service) projectGold(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	if s.sources.Gold != nil {
		coverage, err := s.sources.Gold.Coverage(ctx, tenantID, examID, question.ID)
		if err != nil && !errors.Is(err, goldpaper.ErrNotFound) {
			return err
		}
		if err == nil {
			quality.Gold = GoldSummary{ActiveApproved: coverage.ActiveApprovedCount, Ready: coverage.Ready, Gaps: append([]string(nil), coverage.Gaps...), ScoreBands: make([]Band, 0, len(coverage.ScoreBands))}
			for _, band := range coverage.ScoreBands {
				quality.Gold.ScoreBands = append(quality.Gold.ScoreBands, Band{Band: band.Band, Count: band.Count})
			}
		}
	}
	if !quality.Gold.Ready {
		quality.Findings = append(quality.Findings, finding(question, "gold_coverage_incomplete", GateWarning,
			"标准卷覆盖不足，当前不能将其视为可靠质量证据", "阅卷负责人", "补充并批准缺失分档或特征的标准卷"))
	}
	return nil
}

func (s *Service) projectCalibration(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	if s.sources.Calibration != nil {
		calibration, err := s.sources.Calibration.GetCalibrationSummary(ctx, tenantID, examID, question.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			quality.Calibration = calibration
		}
	}
	if !quality.Calibration.Configured {
		quality.Findings = append(quality.Findings, finding(question, "calibration_not_configured", GateInsufficientData,
			"尚未配置本题阅卷员标定门槛", "阅卷负责人", "配置标定策略并组织阅卷员完成标定"))
	} else if quality.Calibration.Completed == 0 {
		quality.Findings = append(quality.Findings, finding(question, "calibration_not_completed", GateInsufficientData,
			"已配置标定策略，但尚无完成的标定样本", "阅卷负责人", "完成标准卷标定后再分配高风险阅卷任务"))
	} else if quality.Calibration.Passed < quality.Calibration.Completed {
		quality.Findings = append(quality.Findings, finding(question, "calibration_failed", GateWarning,
			"存在未通过的阅卷员标定，不能仅凭总量判断质量", "阅卷负责人", "安排未通过人员复训并重新标定"))
	}
	return nil
}

func (s *Service) projectSeed(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	seed, err := s.seed(ctx, tenantID, examID, question.ID)
	if err != nil {
		return err
	}
	quality.Seed = seed
	if seed.PolicyStatus == "" || seed.PolicyStatus == string(seedquality.PolicyPaused) {
		quality.Findings = append(quality.Findings, finding(question, "seed_sampling_inactive", GateInsufficientData,
			"盲测抽样未启用，无法持续监测阅卷漂移", "质量负责人", "启用与当前标准卷版本绑定的盲测策略"))
	} else if seed.SampleSize == 0 {
		quality.Findings = append(quality.Findings, finding(question, "seed_samples_missing", GateInsufficientData,
			"盲测策略已启用，但还没有可用的质量观察", "质量负责人", "等待或安排足量盲测后复核质量状态"))
	}
	return nil
}

func (s *Service) projectAnswerGroups(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	if s.sources.Groups == nil {
		return nil
	}
	metrics, err := s.sources.Groups.Metrics(ctx, tenantID, examID, question.ID)
	if err != nil && !errors.Is(err, answergroup.ErrNotFound) {
		return err
	}
	if err == nil {
		quality.AnswerGroups = groupSummary(metrics)
	}
	groups, err := s.sources.Groups.List(ctx, tenantID, examID, question.ID)
	if err != nil && !errors.Is(err, answergroup.ErrNotFound) {
		return err
	}
	for _, group := range groups {
		if group.Status == answergroup.StatusSampling || group.Status == answergroup.StatusReady {
			quality.AnswerGroups.OpenSampleGroups++
		}
	}
	if quality.AnswerGroups.OpenSampleGroups > 0 {
		quality.Findings = append(quality.Findings, finding(question, "answer_group_sampling_pending", GateWarning,
			"存在尚未完成抽样确认的相似答案组", "阅卷负责人", "完成边界与离群样本复核，或撤销该分组候选"))
	}
	return nil
}

func (s *Service) projectDrift(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	if s.sources.Drift == nil {
		return nil
	}
	drift, err := s.sources.Drift.GetDriftSummary(ctx, tenantID, examID, question.ID)
	if err != nil {
		return err
	}
	quality.Drift = drift
	for _, incident := range drift.Incidents {
		if incident.Status != "open" {
			continue
		}
		status := GateWarning
		if incident.Severity == "critical" && question.RiskTier == "R3" {
			status = GateBlocked
		}
		quality.Findings = append(quality.Findings, Finding{
			Code: "quality_incident_" + incident.Type, Status: status,
			Reason: "存在未解决的阅卷质量事件：" + incident.Type,
			Owner:  "质量负责人", Resolution: "处置事件并完成必要的回标或仲裁",
			QuestionID: question.ID, QuestionNo: question.QuestionNo, GraderRef: incident.GraderRef, IncidentRef: incident.ID,
		})
	}
	// 读取器仅保留预聚合读模型时，可省略事件引用。仍必须根据聚合数量执行 R3 发布规则。
	if question.RiskTier == "R3" && drift.OpenCritical > 0 && !hasOpenCritical(drift.Incidents) {
		quality.Findings = append(quality.Findings, finding(question, "quality_incident_critical", GateBlocked,
			"存在未解决的严重阅卷质量事件", "质量负责人", "处置严重事件并完成必要的回标或仲裁"))
	}
	return nil
}

func (s *Service) projectBackmark(ctx context.Context, tenantID, examID string, quality *QuestionQuality) error {
	question := quality.Question
	if s.sources.Backmark != nil {
		backmark, err := s.sources.Backmark.GetBackmarkSummary(ctx, tenantID, examID, question.ID)
		if err != nil {
			return err
		}
		quality.Backmark = backmark
		if backmark.OpenBatches > 0 || backmark.PendingItems > 0 {
			quality.Findings = append(quality.Findings, finding(question, "backmark_pending", GateWarning,
				"存在尚未完成的回标批次或回标任务", "质量负责人", "完成回标差异处置，再确认是否需要重新发布成绩"))
		}
	}
	return nil
}

func (s *Service) seed(ctx context.Context, tenantID, examID, questionID string) (SeedSummary, error) {
	result := SeedSummary{Graders: []GraderSeedMetrics{}}
	if s.sources.Seeds == nil {
		return result, nil
	}
	policy, err := s.sources.Seeds.GetPolicy(ctx, tenantID, examID, questionID)
	if err != nil && !errors.Is(err, seedquality.ErrNotFound) {
		return SeedSummary{}, err
	}
	if err == nil {
		result.PolicyStatus = string(policy.Status)
	}
	observations, err := s.sources.Seeds.ListObservations(ctx, tenantID, seedquality.ObservationFilter{ExamID: examID, QuestionID: questionID, Limit: 10000})
	if err != nil {
		return SeedSummary{}, err
	}
	byGrader := map[string][]seedquality.Observation{}
	for _, observation := range observations {
		byGrader[observation.GraderID] = append(byGrader[observation.GraderID], observation)
	}
	graderIDs := make([]string, 0, len(byGrader))
	for graderID := range byGrader {
		graderIDs = append(graderIDs, graderID)
	}
	sort.Strings(graderIDs)
	for _, graderID := range graderIDs {
		metric := seedMetrics(byGrader[graderID])
		metric.GraderRef = graderID
		result.Graders = append(result.Graders, metric)
		result.SampleSize += metric.SampleSize
		result.ExactAgreement.Numerator += metric.ExactAgreement.Numerator
		result.WithinOneAgreement.Numerator += metric.WithinOneAgreement.Numerator
		result.CriterionAgreement.Numerator += metric.CriterionAgreement.Numerator
		result.CriterionAgreement.Denominator += metric.CriterionAgreement.Denominator
	}
	result.ExactAgreement = ratio(result.ExactAgreement.Numerator, result.SampleSize)
	result.WithinOneAgreement = ratio(result.WithinOneAgreement.Numerator, result.SampleSize)
	result.CriterionAgreement.SampleSize = result.CriterionAgreement.Denominator
	if result.CriterionAgreement.Denominator > 0 {
		result.CriterionAgreement.Rate = rate(result.CriterionAgreement.Numerator, result.CriterionAgreement.Denominator)
	}
	return result, nil
}

func seedMetrics(items []seedquality.Observation) GraderSeedMetrics {
	result := GraderSeedMetrics{SampleSize: len(items)}
	var exact, withinOne, criterionCorrect, criterionCount int
	for _, item := range items {
		if item.AbsoluteError < 1e-9 {
			exact++
		}
		if item.AbsoluteError <= 1 {
			withinOne++
		}
		if item.RubricAgreement != nil {
			criterionCount++
			if *item.RubricAgreement >= 0.999999 {
				criterionCorrect++
			}
		}
	}
	result.ExactAgreement = ratio(exact, len(items))
	result.WithinOneAgreement = ratio(withinOne, len(items))
	result.CriterionAgreement = ratio(criterionCorrect, criterionCount)
	return result
}

func (s *Service) humanAgreement(ctx context.Context, tenantID, examID, questionID string) (HumanAgreementSummary, error) {
	sessions, err := s.sources.Review.ListDoubleMarkSessions(ctx, tenantID, review.DoubleMarkSessionFilter{
		ExamID:     examID,
		QuestionID: questionID,
	})
	if err != nil {
		return HumanAgreementSummary{}, err
	}
	result := HumanAgreementSummary{}
	var within int
	for _, session := range sessions {
		if session.Status == "pending" || session.Status == "first_submitted" || session.Status == "second_submitted" {
			result.OpenCases++
		}
		if session.ScoreDifference == nil {
			continue
		}
		result.SampleSize++
		if math.Abs(*session.ScoreDifference) <= session.Threshold {
			within++
		}
	}
	result.WithinRule = ratio(within, result.SampleSize)
	return result, nil
}

func groupSummary(metrics answergroup.Metrics) GroupSummary {
	result := GroupSummary{GroupCount: metrics.GroupCount, MemberCount: metrics.MemberCount}
	if metrics.GroupCount > 0 {
		value := metrics.GroupHomogeneity
		result.Homogeneity = &value
		override := metrics.BatchOverrideRate
		result.OverrideRate = &override
	}
	// 当前 A07 指标没有各状态数量。在 A07 读取器提供它们之前，该字段明确保持零，不作猜测。
	return result
}

func gateFor(findings []Finding) GateStatus {
	for _, finding := range findings {
		if finding.Status == GateBlocked {
			return GateBlocked
		}
	}
	for _, finding := range findings {
		if finding.Status == GateWarning || finding.Status == GateInsufficientData {
			return GateWarning
		}
	}
	return GateReady
}

func hasOpenCritical(items []Incident) bool {
	for _, item := range items {
		if item.Status == "open" && item.Severity == "critical" {
			return true
		}
	}
	return false
}

func ratio(numerator, denominator int) Ratio {
	return Ratio{Numerator: numerator, Denominator: denominator, SampleSize: denominator, Rate: rate(numerator, denominator)}
}

func rate(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

func finding(question Question, code string, status GateStatus, reason, owner, resolution string) Finding {
	return Finding{Code: code, Status: status, Reason: reason, Owner: owner, Resolution: resolution, QuestionID: question.ID, QuestionNo: question.QuestionNo}
}
