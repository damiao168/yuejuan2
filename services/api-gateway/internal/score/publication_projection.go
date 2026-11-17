package score

import "edugrade-enterprise/services/api-gateway/internal/scorerelease"

// publishedSubmissionGrade 仅把正式发布的冻结事实投影到旧响应结构，不创建可编辑的成绩记录。
// ID 和 Revision 表示发布版本；原成绩创建人、确认信息、匿名号和答案片段不在冻结事实中，保留空值。
func publishedSubmissionGrade(tenantID string, release scorerelease.Release, fact scorerelease.SubmissionFact) SubmissionGrade {
	grade := SubmissionGrade{
		ID: release.ID, TenantID: tenantID, ExamID: release.ExamID,
		SubmissionID: fact.SubmissionID, StudentID: fact.StudentID,
		TotalScore: fact.TotalScore, MaxScore: fact.MaxScore,
		Status: "published", Locked: true, Revision: int64(release.Version), PublishedBy: release.PublishedBy,
	}
	if release.PublishedAt != nil {
		publishedAt := *release.PublishedAt
		grade.PublishedAt = &publishedAt
	}
	for _, question := range fact.Questions {
		grade.Items = append(grade.Items, FinalGrade{
			ID: question.FinalGradeID, TenantID: tenantID, ExamID: release.ExamID,
			QuestionID: question.QuestionID, QuestionNo: question.QuestionNo, SubmissionID: fact.SubmissionID,
			Score: question.Score, MaxScore: question.MaxScore, Source: question.SourceType,
			Status: "locked", Locked: true,
		})
	}
	return grade
}

// studentPublishedGrade 只接收正式发布服务已经按可见性过滤的结果及已定位的冻结答卷 ID。
// 隐藏的逐题成绩不能从其他来源补回；旧 DTO 的成绩 ID 和 Source 字段不填入内部成绩或复核来源身份。
func studentPublishedGrade(tenantID, studentID, submissionID string, result scorerelease.StudentResult) SubmissionGrade {
	grade := SubmissionGrade{
		ID: result.ReleaseID, TenantID: tenantID, ExamID: result.ExamID,
		SubmissionID: submissionID, StudentID: studentID,
		TotalScore: result.TotalScore, MaxScore: result.MaxScore,
		Status: "published", Locked: true, Revision: int64(result.ReleaseVersion),
	}
	if result.Exam != nil && !result.Exam.PublishedAt.IsZero() {
		publishedAt := result.Exam.PublishedAt
		grade.PublishedAt = &publishedAt
	}
	for _, question := range result.Questions {
		grade.Items = append(grade.Items, FinalGrade{
			TenantID: tenantID, ExamID: result.ExamID, SubmissionID: submissionID,
			QuestionID: question.QuestionID, QuestionNo: question.QuestionNo,
			Score: question.Score, MaxScore: question.MaxScore,
			Status: "locked", Locked: true,
		})
	}
	return grade
}
