package scorerelease

import (
	"context"
	"sort"
	"time"
)

// StudentPreview 只读取指定发布的冻结成绩，不发布草稿、不改变学生当前版本。
// 答卷图像和跨科当前版本数据仍由正式学生接口校验，预览不代用学生身份读取这些资源。
func (s *Service) StudentPreview(ctx context.Context, tenantID, releaseID, studentID string) (StudentResult, error) {
	if !validIDs(tenantID, releaseID, studentID) {
		return StudentResult{}, ErrInvalidInput
	}
	detail, err := s.store.Get(ctx, tenantID, releaseID)
	if err != nil {
		return StudentResult{}, err
	}
	return frozenStudentResult(detail, studentID, s.now().UTC())
}

// 预览与学生内存读取共用投影；PostgreSQL 学生读取也通过 studentQuestionView 应用相同字段策略。
// 只能加入学生公开字段，不能直接序列化带有评分来源和内部身份的 ReleaseQuestion。
func frozenStudentResult(detail Detail, studentID string, now time.Time) (StudentResult, error) {
	for _, item := range detail.Items {
		if item.StudentID != studentID {
			continue
		}
		policy := detail.Release.VisibilityPolicy
		result := StudentResult{ExamID: detail.Release.ExamID, ReleaseID: detail.Release.ID, ReleaseVersion: detail.Release.Version,
			TotalScore: item.TotalScore, MaxScore: item.MaxScore, OverallTotalScore: item.TotalScore, OverallMaxScore: item.MaxScore,
			ScoreRate: scoreRate(item.TotalScore, item.MaxScore), AppealWindow: appealView(detail.Release.AppealWindow, now)}
		result.Reference = buildStudentReference(detail.Items, item.TotalScore, policy)
		result.Rankings = &StudentRankings{ClassUnavailableReason: "not_public", GradeUnavailableReason: "not_public"}
		if policy.ShowExactRank {
			reason := "scope_unavailable"
			if result.Reference != nil && result.Reference.UnavailableReason == "small_cohort" {
				reason = "small_cohort"
			}
			result.Rankings.ClassUnavailableReason = reason
			result.Rankings.GradeUnavailableReason = reason
		}
		if policy.ShowQuestionScores {
			result.Questions = frozenStudentQuestions(detail.Questions, item.SubmissionID, policy)
		}
		return result, nil
	}
	return StudentResult{}, ErrNotFound
}

// 仅在题目分数可见时调用；没有匹配题目也返回空数组，隐藏时由调用方保留 nil。
func frozenStudentQuestions(questions []ReleaseQuestion, submissionID string, policy VisibilityPolicy) []StudentQuestion {
	result := []StudentQuestion{}
	for _, question := range questions {
		if question.SubmissionID != submissionID {
			continue
		}
		view := studentQuestionView(question, questions, policy)
		if policy.ShowFeedback {
			view.Feedback = question.Explanation.Feedback
		}
		if policy.ShowRubricSummary {
			view.RubricSummary = append([]string(nil), question.Explanation.RubricSummary...)
		}
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QuestionNo < result[j].QuestionNo })
	return result
}
