package server

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

// 两个内存存储不共享工作表；只复制实际定稿/确认所得事实，正式发布仍由真实协调器和门禁执行。
// PostgreSQL 路由使用同事务准备器，不需要这段测试夹具同步。
func syncMemoryPublicationFacts(t *testing.T, working score.Store, releases *scorerelease.MemoryStore, tenant, exam string) {
	t.Helper()
	list, err := working.ListExamGrades(t.Context(), tenant, exam, score.GradeListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	facts := make([]scorerelease.SubmissionFact, 0, len(list.Grades))
	for _, grade := range list.Grades {
		fact := scorerelease.SubmissionFact{StudentID: grade.StudentID, SubmissionID: grade.SubmissionID, TotalScore: grade.TotalScore, MaxScore: grade.MaxScore, Status: grade.Status}
		for _, item := range grade.Items {
			fact.Questions = append(fact.Questions, scorerelease.QuestionFact{QuestionID: item.QuestionID, QuestionNo: item.QuestionNo, FinalGradeID: item.ID, Score: item.Score, MaxScore: item.MaxScore, SourceType: item.Source})
		}
		facts = append(facts, fact)
	}
	quality, err := working.CheckQuality(t.Context(), tenant, exam, false)
	if err != nil {
		t.Fatal(err)
	}
	issues := make([]scorerelease.GateIssue, 0, len(quality.Issues))
	for _, issue := range quality.Issues {
		issues = append(issues, scorerelease.GateIssue{Code: issue.Code, Message: issue.Message, Blocking: issue.Blocking, Count: issue.Count})
	}
	releases.SeedFacts(exam, facts)
	releases.SetGateIssues(exam, issues)
}
