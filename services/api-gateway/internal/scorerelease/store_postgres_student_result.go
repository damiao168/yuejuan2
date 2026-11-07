package scorerelease

// 学生结果继续绑定已解析的发布版本；可见性检查先于题目和图像定位读取，隐藏题目的 nil 语义保留。
import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

func (s *PostgresStore) StudentResult(ctx context.Context, tenantID, examID, studentID string) (StudentResult, error) {
	detail, err := s.studentDetail(ctx, tenantID, examID, studentID)
	if err != nil {
		return StudentResult{}, err
	}
	item, ok := findStudentReleaseItem(detail.Items, studentID)
	if !ok {
		return StudentResult{}, ErrNotFound
	}
	result := StudentResult{ExamID: examID, ReleaseID: detail.Release.ID, ReleaseVersion: detail.Release.Version, TotalScore: item.TotalScore, MaxScore: item.MaxScore, OverallTotalScore: item.TotalScore, OverallMaxScore: item.MaxScore, ScoreRate: scoreRate(item.TotalScore, item.MaxScore),
		AppealWindow: appealView(detail.Release.AppealWindow, s.now().UTC())}
	if err := s.populateStudentResultSession(ctx, tenantID, studentID, detail.Release, &result); err != nil {
		return StudentResult{}, err
	}
	result.Reference = buildStudentReference(detail.Items, item.TotalScore, detail.Release.VisibilityPolicy)
	if err := s.populateStudentResultCohort(ctx, tenantID, studentID, detail.Release.VisibilityPolicy, &result); err != nil {
		return StudentResult{}, err
	}
	// 隐藏题目时连题目、答卷定位和优秀答卷都不读取；Questions 保持 nil，与可见空数组区分。
	if !detail.Release.VisibilityPolicy.ShowQuestionScores {
		return result, nil
	}
	if err := s.populateStudentResultQuestions(ctx, tenantID, studentID, detail.Release, item.SubmissionID, &result); err != nil {
		return StudentResult{}, err
	}
	return result, nil
}

func findStudentReleaseItem(items []ReleaseItem, studentID string) (ReleaseItem, bool) {
	for _, item := range items {
		if item.StudentID == studentID {
			return item, true
		}
	}
	return ReleaseItem{}, false
}

// 主科汇总和公开发布时间沿用已读取的冻结发布，不能为补充展示再解析一次当前发布指针。
func (s *PostgresStore) populateStudentResultSession(ctx context.Context, tenantID, studentID string, release Release, result *StudentResult) error {
	summary, overallScore, overallMax, err := s.studentSessionSummary(ctx, tenantID, result.ExamID, studentID, result.ReleaseID)
	if err != nil {
		return err
	}
	if overallMax > 0 {
		result.OverallTotalScore, result.OverallMaxScore = overallScore, overallMax
		result.SessionSummary = summary
	}
	var exam StudentExam
	err = s.db.QueryRowContext(ctx, `SELECT name, subject, exam_type FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, result.ExamID).Scan(&exam.Name, &exam.Subject, &exam.ExamType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	exam.PublishedAt = *release.PublishedAt
	result.Exam = &exam
	return nil
}

func (s *PostgresStore) populateStudentResultCohort(ctx context.Context, tenantID, studentID string, policy VisibilityPolicy, result *StudentResult) error {
	var err error
	if policy.ShowCohortStatistics {
		result.SubjectBalance, err = s.studentSubjectBalance(ctx, tenantID, result.ExamID, studentID)
		if err != nil {
			return err
		}
	}
	if !policy.ShowExactRank {
		result.Rankings = &StudentRankings{ClassUnavailableReason: "not_public", GradeUnavailableReason: "not_public"}
		return nil
	}
	result.Rankings, err = s.studentRankings(ctx, tenantID, result.ExamID, result.ReleaseID, studentID, result.TotalScore)
	return err
}

// 调用方已通过题目可见性检查；题目和统计继续绑定同一 release 和本人的 submission。
func (s *PostgresStore) populateStudentResultQuestions(ctx context.Context, tenantID, studentID string, release Release, submissionID string, result *StudentResult) error {
	questions, err := s.questions(ctx, tenantID, release.ID, submissionID)
	if err != nil {
		return err
	}
	presentation, pages, err := s.studentPaperPresentation(ctx, tenantID, submissionID)
	if err != nil {
		return err
	}
	result.PaperPages = pages
	// 四项分数比较随逐题成绩展示；旧的题目统计开关和隐私门槛仅控制 Cohort 分析。
	questionStats, err := s.studentQuestionStatistics(ctx, tenantID, result.ExamID, release.ID, studentID)
	if err != nil {
		return err
	}
	result.Questions = postgresStudentQuestions(questions, submissionID, release.VisibilityPolicy, result.Exam, presentation, questionStats)
	if release.VisibilityPolicy.ShowHighScorePaper {
		// 只有存在经过核验的匿名资源后，才提供同学答卷图像。
		result.HighScorePapers, err = s.studentHighScorePapers(ctx, tenantID, release.ID)
		if err != nil {
			return err
		}
		if len(result.HighScorePapers) > 0 {
			// 旧客户端仍读取首份，集合中的匿名标识供新版切卷使用。
			result.HighScorePaper = &result.HighScorePapers[0]
		}
	}
	return nil
}

func postgresStudentQuestions(questions []ReleaseQuestion, submissionID string, policy VisibilityPolicy, exam *StudentExam, presentation map[string]studentQuestionPresentation, stats map[string]studentQuestionAggregates) []StudentQuestion {
	result := []StudentQuestion{}
	for _, question := range questions {
		if question.SubmissionID != submissionID {
			continue
		}
		result = append(result, postgresStudentQuestion(question, policy, exam, presentation, stats))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QuestionNo < result[j].QuestionNo })
	return result
}

func postgresStudentQuestion(question ReleaseQuestion, policy VisibilityPolicy, exam *StudentExam, presentation map[string]studentQuestionPresentation, stats map[string]studentQuestionAggregates) StudentQuestion {
	viewPolicy := policy
	viewPolicy.ShowQuestionStatistics = false // PostgreSQL 已按冻结考生范围和隐私阈值计算统计，不从题目列表重算。
	view := studentQuestionView(question, nil, viewPolicy)
	if exam != nil {
		view.Subject = exam.Subject
	}
	if item, ok := presentation[question.QuestionID]; ok {
		view.PageNo, view.SubmissionPageID, view.AnswerGeometry = item.PageNo, item.SubmissionPageID, item.Geometry
	}
	if stat, ok := stats[question.QuestionID]; ok {
		if policy.ShowQuestionScores {
			view.ScoreComparison = stat.Comparison
		}
		if policy.ShowQuestionStatistics {
			view.Cohort = stat.Cohort
		}
	}
	view.CohortUnavailableReason = "not_public"
	if policy.ShowQuestionStatistics {
		view.CohortUnavailableReason = "small_cohort"
		if view.Cohort != nil {
			view.CohortUnavailableReason = ""
		}
	}
	if policy.ShowFeedback {
		view.Feedback = question.Explanation.Feedback
	}
	if policy.ShowRubricSummary {
		view.RubricSummary = append([]string(nil), question.Explanation.RubricSummary...)
	}
	return view
}

type studentQuestionPresentation struct {
	PageNo           int
	SubmissionPageID string
	Geometry         *StudentImageGeometry
}

func (result StudentResult) QuestionsIsVisible() bool { return result.Questions != nil }
