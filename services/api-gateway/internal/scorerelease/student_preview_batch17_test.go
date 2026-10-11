package scorerelease

import (
	"testing"
	"time"
)

func TestFrozenStudentResultKeepsSelectedSubmissionAndCopiedRubric(t *testing.T) {
	detail := Detail{
		Release: Release{VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowFeedback: true, ShowRubricSummary: true}},
		Items:   []ReleaseItem{{StudentID: "selected", SubmissionID: "chosen", TotalScore: 8, MaxScore: 10}},
		Questions: []ReleaseQuestion{
			{SubmissionID: "chosen", QuestionFact: QuestionFact{QuestionID: "q-2", QuestionNo: "2", Explanation: StudentExplanation{Feedback: "feedback-2"}}},
			{SubmissionID: "other", QuestionFact: QuestionFact{QuestionID: "foreign", QuestionNo: "1"}},
			{SubmissionID: "chosen", QuestionFact: QuestionFact{QuestionID: "q-10", QuestionNo: "10", Explanation: StudentExplanation{Feedback: "feedback-10", RubricSummary: []string{"rubric-10"}}}},
		},
	}
	result, err := frozenStudentResult(detail, "selected", time.Time{})
	if err != nil || len(result.Questions) != 2 {
		t.Fatalf("selected projection=%#v err=%v", result, err)
	}
	if result.Questions[0].QuestionID != "q-10" || result.Questions[1].QuestionID != "q-2" || result.Questions[0].Feedback != "feedback-10" {
		t.Fatalf("question order or feedback changed: %#v", result.Questions)
	}
	// 学生结果里的量规切片可被调用方修改，不能连带改动冻结发布内容。
	result.Questions[0].RubricSummary[0] = "changed"
	if detail.Questions[2].Explanation.RubricSummary[0] != "rubric-10" {
		t.Fatal("student projection aliases frozen rubric")
	}
}

func TestFrozenStudentResultPreservesHiddenAndVisibleEmptyQuestions(t *testing.T) {
	for _, visible := range []bool{false, true} {
		detail := Detail{
			Release: Release{VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: visible}},
			Items:   []ReleaseItem{{StudentID: "selected", SubmissionID: "chosen"}},
		}
		result, err := frozenStudentResult(detail, "selected", time.Time{})
		if err != nil || (result.Questions != nil) != visible || len(result.Questions) != 0 {
			t.Fatalf("visible=%t questions=%#v err=%v", visible, result.Questions, err)
		}
	}
}
