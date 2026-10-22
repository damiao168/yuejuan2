package report

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestStudentReportHonorsPublishedQuestionAndKnowledgeVisibility(t *testing.T) {
	data := dataset{
		ExamID:                "exam-a",
		ShowQuestionScores:    false,
		ShowKnowledgeAnalysis: false,
		Submissions:           []submissionRecord{{ID: "submission-a", StudentID: "student-a", TotalScore: 8, MaxScore: 10}},
		Grades:                []gradeRecord{{SubmissionID: "submission-a", StudentID: "student-a", QuestionID: "question-a", QuestionNo: "Q1", Score: 8, MaxScore: 10, KnowledgePoints: []string{"比例"}}},
	}
	report := buildStudentReport(data, "student-a")
	if len(report.Questions) != 0 || len(report.KnowledgeMastery) != 0 {
		t.Fatalf("hidden release fields leaked through student report: %#v", report)
	}
}

func TestStudentReportHandlerSeparatesStudentPolicyFromManagementView(t *testing.T) {
	for _, scores := range []bool{false, true} {
		for _, knowledge := range []bool{false, true} {
			for _, feedback := range []bool{false, true} {
				store := seededReportStore()
				store.data.ShowQuestionScores, store.data.ShowKnowledgeAnalysis, store.data.ShowFeedback = scores, knowledge, feedback
				h := NewHandler(store, nil)
				for _, management := range []bool{false, true} {
					permission := "student:report:read"
					if management {
						permission = "report:read"
					}
					user := auth.User{TenantID: "tenant-1", ID: "actor", Permissions: []string{permission}, DataScope: map[string]any{"student": map[string]any{"scope": "self", "student_id": "student-1"}}}
					r := httptest.NewRequest(http.MethodGet, "/api/v1/students/student-1/reports/exam-1", nil)
					r.SetPathValue("studentId", "student-1")
					r.SetPathValue("examId", "exam-1")
					w := httptest.NewRecorder()
					h.StudentReport(w, r.WithContext(auth.WithUser(r.Context(), user)))
					var body struct {
						Report StudentReport `json:"report"`
					}
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
						t.Fatalf("report status=%d body=%s", w.Code, w.Body.String())
					}
					got := body.Report
					if got.TotalScore != 85 || (len(got.Questions) > 0) != (management || scores) || (len(got.KnowledgeMastery) > 0) != (management || knowledge) || (len(got.TeacherFeedback) > 0) != (management || feedback) || (len(got.AIFeedback) > 0) != (management || feedback) || (len(got.ErrorClues) > 0) != (management || feedback) {
						t.Fatalf("management=%v scores=%v knowledge=%v feedback=%v got=%+v", management, scores, knowledge, feedback, got)
					}
					for _, question := range got.Questions {
						if (len(question.KnowledgePoints) > 0) != (management || knowledge) || (len(question.TeacherFeedback) > 0) != (management || feedback) {
							t.Fatalf("nested visibility leaked: %+v", question)
						}
					}
				}
			}
		}
	}
}
