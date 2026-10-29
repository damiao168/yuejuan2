package review

import (
	"math"
	"sort"
	"time"
)

// ReviewScoreStatistics 只计算已有正式分数的样本；无样本时比例为 null，不能冒充零正确率。
type ReviewScoreStatistics struct {
	GradedCount      int      `json:"graded_count"`
	FullScoreCount   int      `json:"full_score_count"`
	CorrectRate      *float64 `json:"correct_rate"`
	AverageScoreRate *float64 `json:"average_score_rate"`
}

type ReviewQuestionStatistics struct {
	QuestionID string `json:"question_id"`
	QuestionNo string `json:"question_no"`
	ReviewScoreStatistics
}

// ReviewStatistics 的 personal 口径只含本人正式评阅，exam 口径只含已确认的当前最终成绩。
type ReviewStatistics struct {
	ExamID string `json:"exam_id"`
	Scope  string `json:"scope"`
	ReviewScoreStatistics
	Questions []ReviewQuestionStatistics `json:"questions"`
	UpdatedAt time.Time                  `json:"updated_at"`
}

type reviewStatisticSample struct {
	segmentID, questionID, questionNo string
	score, maximum                    float64
	createdAt                         time.Time
	gradeID                           string
}

func buildReviewStatistics(examID, scope string, samples []reviewStatisticSample) *ReviewStatistics {
	result := &ReviewStatistics{ExamID: examID, Scope: scope, Questions: []ReviewQuestionStatistics{}, UpdatedAt: time.Now().UTC()}
	questions := map[string][]reviewStatisticSample{}
	for _, sample := range samples {
		if sample.maximum <= 0 || math.IsNaN(sample.maximum) || math.IsNaN(sample.score) || math.IsInf(sample.maximum, 0) || math.IsInf(sample.score, 0) || sample.score < 0 || sample.score > sample.maximum {
			continue
		}
		questions[sample.questionID] = append(questions[sample.questionID], sample)
	}
	all := []reviewStatisticSample{}
	for questionID, group := range questions {
		all = append(all, group...)
		result.Questions = append(result.Questions, ReviewQuestionStatistics{QuestionID: questionID, QuestionNo: group[0].questionNo, ReviewScoreStatistics: summarizeReviewSamples(group)})
	}
	sort.Slice(result.Questions, func(i, j int) bool {
		if compared := compareGradingGroupQuestionNo(result.Questions[i].QuestionNo, result.Questions[j].QuestionNo); compared != 0 {
			return compared < 0
		}
		return result.Questions[i].QuestionID < result.Questions[j].QuestionID
	})
	result.ReviewScoreStatistics = summarizeReviewSamples(all)
	return result
}

func summarizeReviewSamples(samples []reviewStatisticSample) ReviewScoreStatistics {
	result := ReviewScoreStatistics{GradedCount: len(samples)}
	if len(samples) == 0 {
		return result
	}
	var totalScore, totalMaximum float64
	for _, sample := range samples {
		totalScore += sample.score
		totalMaximum += sample.maximum
		if math.Abs(sample.score-sample.maximum) <= 0.0001 {
			result.FullScoreCount++
		}
	}
	// 跨题得分率按正式得分合计/满分合计计算，不能让一分题与十分题拥有相同权重。
	correct, average := float64(result.FullScoreCount)/float64(len(samples)), totalScore/totalMaximum
	result.CorrectRate, result.AverageScoreRate = &correct, &average
	return result
}

func (s *MemoryStore) reviewStatisticsLocked(tenantID string, filter ListFilter) *ReviewStatistics {
	filter.Status, filter.CursorID, filter.Limit = "", "", 0
	// 搜索和待分配筛选只影响工作队列，不能让整场统计随当前列表缩小分母。
	filter.Q, filter.Unassigned = "", false
	bySegment := map[string]reviewStatisticSample{}
	scope := "exam"
	if filter.AssignedTo != "" {
		scope = "personal"
	}
	for _, task := range s.tasks {
		if !reviewTaskMatchesFilter(task, tenantID, filter) || !memoryStatisticsScopeAllows(task, filter) || !submittedReviewStatus(task.Status) {
			continue
		}
		// 双评尚未合分时管理员不能从两份独立意见推断最终分；教师只统计自己的正式意见。
		if scope == "exam" && s.sessionByTask[key(tenantID, task.ID)] != "" {
			continue
		}
		for _, grade := range s.grades[key(tenantID, task.ID)] {
			if grade.ReviewerID != task.AssignedTo || scope == "personal" && grade.ReviewerID != filter.AssignedTo {
				continue
			}
			sample := reviewStatisticSample{segmentID: task.AnswerSegmentID, questionID: task.QuestionID, questionNo: task.QuestionNo, score: grade.Score, maximum: grade.MaxScore, createdAt: grade.CreatedAt, gradeID: grade.ID}
			putLatestReviewSample(bySegment, sample)
		}
	}
	if scope == "exam" {
		for _, grade := range s.finalGrades {
			if grade.TenantID != tenantID || grade.ExamID != filter.ExamID || filter.QuestionID != "" && grade.QuestionID != filter.QuestionID {
				continue
			}
			allowed := false
			for _, task := range s.tasks {
				if task.AnswerSegmentID == grade.AnswerSegmentID && reviewTaskMatchesFilter(task, tenantID, filter) && memoryStatisticsScopeAllows(task, filter) {
					allowed = true
					break
				}
			}
			if allowed {
				putLatestReviewSample(bySegment, reviewStatisticSample{segmentID: grade.AnswerSegmentID, questionID: grade.QuestionID, questionNo: grade.QuestionNo, score: grade.Score, maximum: grade.MaxScore, createdAt: grade.CreatedAt, gradeID: grade.ID})
			}
		}
	}
	samples := make([]reviewStatisticSample, 0, len(bySegment))
	for _, sample := range bySegment {
		samples = append(samples, sample)
	}
	return buildReviewStatistics(filter.ExamID, scope, samples)
}

func memoryStatisticsScopeAllows(task ReviewTask, filter ListFilter) bool {
	switch filter.ScopeMode {
	case "", "platform", "tenant":
		return true
	case "school", "assigned":
		// 内存仓库没有考试到管理学校的索引，不能用导航考试证明整场权限；此时只提供本人意见，无样本仍返回 null 比例。
		return filter.ScopeActorID != "" && filter.AssignedTo == filter.ScopeActorID && task.AssignedTo == filter.ScopeActorID
	case "class":
		return containsString(filter.ScopeExamIDs, task.ExamID)
	default:
		return false
	}
}

func putLatestReviewSample(samples map[string]reviewStatisticSample, sample reviewStatisticSample) {
	previous, exists := samples[sample.segmentID]
	if !exists || sample.createdAt.After(previous.createdAt) || sample.createdAt.Equal(previous.createdAt) && sample.gradeID > previous.gradeID {
		samples[sample.segmentID] = sample
	}
}
