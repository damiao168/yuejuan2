package scorerelease

import (
	"math"
	"sort"
)

// 少于该人数时不返回统计和排名，避免学生从聚合值推断同学成绩。
const privacyMinCohortSize = 10

func scoreRate(score, maxScore float64) float64 {
	if maxScore <= 0 {
		return 0
	}
	return math.Round(score/maxScore*1000) / 1000
}

func buildStudentReference(items []ReleaseItem, studentScore float64, policy VisibilityPolicy) *StudentReference {
	if !policy.ShowCohortStatistics && !policy.ShowScoreDistribution && !policy.ShowPercentile && !policy.ShowExactRank {
		return nil
	}
	scores := make([]float64, 0, len(items))
	for _, item := range items {
		if item.StudentID != "" {
			scores = append(scores, item.TotalScore)
		}
	}
	ref := &StudentReference{Scope: "grade", SampleSize: len(scores)}
	if len(scores) < privacyMinCohortSize {
		ref.UnavailableReason = "small_cohort"
		return ref
	}
	sort.Float64s(scores)
	ref.StatisticsAvailable = true
	if policy.ShowCohortStatistics {
		mean, median := mean(scores), quantile(scores, .5)
		ref.MeanScore, ref.MedianScore = &mean, &median
	}
	if policy.ShowScoreDistribution {
		q1, q3, minScore, maxScore := quantile(scores, .25), quantile(scores, .75), scores[0], scores[len(scores)-1]
		ref.Q1, ref.Q3, ref.MinScore, ref.MaxScore = &q1, &q3, &minScore, &maxScore
	}
	below, equal := 0, 0
	for _, score := range scores {
		switch {
		case score < studentScore:
			below++
		case math.Abs(score-studentScore) < .000001:
			equal++
		}
	}
	if policy.ShowPercentile {
		percentile := math.Round((float64(below)+.5*float64(equal))/float64(len(scores))*1000) / 10
		ref.Percentile = &percentile
	}
	if policy.ShowExactRank {
		rank := 1
		for _, score := range scores {
			if score > studentScore {
				rank++
			}
		}
		ref.Rank = &rank
	}
	return ref
}

func studentQuestionView(question ReleaseQuestion, all []ReleaseQuestion, policy VisibilityPolicy) StudentQuestion {
	view := StudentQuestion{
		QuestionID: question.QuestionID, QuestionNo: question.QuestionNo,
		QuestionType: question.Explanation.QuestionType,
		Score:        question.Score, MaxScore: question.MaxScore,
		ScoreRate: scoreRate(question.Score, question.MaxScore),
	}
	if policy.ShowQuestionStem {
		view.Stem = question.Explanation.Stem
	}
	if policy.ShowKnowledgeAnalysis {
		view.KnowledgePoints = append([]string(nil), question.Explanation.KnowledgePoints...)
	}
	if policy.ShowQuestionStatistics {
		view.Cohort = buildQuestionReference(question.QuestionID, all)
	}
	if policy.ShowAnswers {
		view.CorrectAnswer = question.Explanation.CorrectAnswer
		view.ActualAnswer = question.Explanation.ActualAnswer
	}
	return view
}

func buildQuestionReference(questionID string, all []ReleaseQuestion) *StudentQuestionReference {
	count, full, zero := 0, 0, 0
	totalRate := 0.0
	scores := []float64{}
	for _, item := range all {
		if item.QuestionID != questionID {
			continue
		}
		count++
		scores = append(scores, item.Score)
		totalRate += scoreRate(item.Score, item.MaxScore)
		if math.Abs(item.Score-item.MaxScore) < .000001 {
			full++
		}
		if math.Abs(item.Score) < .000001 {
			zero++
		}
	}
	if count < privacyMinCohortSize {
		return nil
	}
	sort.Float64s(scores)
	meanScore := mean(scores)
	medianScore := quantile(scores, .5)
	return &StudentQuestionReference{
		SampleSize:      count,
		MeanScoreRate:   math.Round(totalRate/float64(count)*1000) / 1000,
		FullScoreRate:   math.Round(float64(full)/float64(count)*1000) / 1000,
		ZeroScoreRate:   math.Round(float64(zero)/float64(count)*1000) / 1000,
		ClassMeanScore:  &meanScore,
		SchoolMeanScore: &meanScore,
		MedianScore:     &medianScore,
	}
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func quantile(values []float64, p float64) float64 {
	if len(values) == 1 {
		return values[0]
	}
	// 使用排序后相邻位置的线性插值，保证中位数等分位点稳定且可复现。
	position := p * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}
