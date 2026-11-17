package score

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

func TestPublishedSubmissionProjectionKeepsFrozenFactsAndPublicationIdentity(t *testing.T) {
	publishedAt := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	release := scorerelease.Release{ID: "release-v4", TenantID: "tenant-1", ExamID: "exam-1", Version: 4, PublishedBy: "publisher-1", PublishedAt: &publishedAt, CreatedBy: "draft-author-not-grade-author"}
	fact := scorerelease.SubmissionFact{StudentID: "student-frozen", SubmissionID: "submission-frozen", TotalScore: 8.75, MaxScore: 20, Status: "confirmed", Questions: []scorerelease.QuestionFact{
		{QuestionID: "q2", QuestionNo: "2", FinalGradeID: "final-q2", Score: 4.5, MaxScore: 10, SourceType: "single_review", SourceID: "private-review-id"},
		{QuestionID: "q1", QuestionNo: "1", FinalGradeID: "final-q1", Score: 4.25, MaxScore: 10, SourceType: "objective", Explanation: scorerelease.StudentExplanation{Feedback: "private-feedback-outside-old-dto"}},
	}}
	grade := publishedSubmissionGrade("tenant-1", release, fact)
	if grade.ID != release.ID || grade.TenantID != "tenant-1" || grade.ExamID != release.ExamID || grade.Revision != 4 || grade.StudentID != fact.StudentID || grade.SubmissionID != fact.SubmissionID || grade.TotalScore != 8.75 || grade.MaxScore != 20 {
		t.Fatalf("frozen identity or decimal scores changed: %+v", grade)
	}
	if grade.Status != "published" || !grade.Locked || grade.PublishedBy != release.PublishedBy || grade.PublishedAt == nil || !grade.PublishedAt.Equal(publishedAt) {
		t.Fatalf("published metadata missing: %+v", grade)
	}
	if grade.AnonymousCode != "" || grade.CreatedBy != "" || !grade.CreatedAt.IsZero() || grade.ConfirmedBy != "" || grade.ConfirmedAt != nil {
		t.Fatalf("unavailable mutable grade metadata was invented: %+v", grade)
	}
	if len(grade.Items) != 2 {
		t.Fatalf("frozen questions lost: %+v", grade.Items)
	}
	for i, item := range grade.Items {
		question := fact.Questions[i]
		if item.ID != question.FinalGradeID || item.QuestionID != question.QuestionID || item.QuestionNo != question.QuestionNo || item.Score != question.Score || item.MaxScore != question.MaxScore || item.Source != question.SourceType || item.Status != "locked" || !item.Locked || item.SubmissionID != fact.SubmissionID {
			t.Fatalf("question facts or frozen order changed: %+v", item)
		}
		if item.AnswerSegmentID != "" || item.CreatedBy != "" || item.AnonymousCode != "" {
			t.Fatalf("projection invented internal grade metadata: %+v", item)
		}
	}
	raw, err := json.Marshal(grade)
	if err != nil || strings.Contains(string(raw), "private-") || strings.Contains(string(raw), release.CreatedBy) {
		t.Fatalf("old projection leaked non-projected facts: %s %v", raw, err)
	}
	grade.Items[0].Score = 0
	*grade.PublishedAt = publishedAt.Add(time.Hour)
	if fact.Questions[0].Score != 4.5 || !release.PublishedAt.Equal(publishedAt) {
		t.Fatal("editing a compatibility response modified the frozen input")
	}
}

func TestStudentPublishedProjectionUsesOnlyVisibleQuestionsAndKnownMetadata(t *testing.T) {
	publishedAt := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	for _, scenario := range []string{"visible questions", "hidden questions", "no exam metadata", "unknown publication time"} {
		t.Run(scenario, func(t *testing.T) {
			result := scorerelease.StudentResult{
				ExamID: "exam-1", ReleaseID: "release-v3", ReleaseVersion: 3, TotalScore: 7.25, MaxScore: 10,
				OverallTotalScore: 85, OverallMaxScore: 100, Exam: &scorerelease.StudentExam{PublishedAt: publishedAt},
				Questions: []scorerelease.StudentQuestion{{QuestionID: "q-visible", QuestionNo: "1", Score: 7.25, MaxScore: 10, Feedback: "visible-feedback-not-a-grade-source", ActualAnswer: "visible-answer"}},
			}
			switch scenario {
			case "hidden questions":
				result.Questions = nil
			case "no exam metadata":
				result.Exam = nil
			case "unknown publication time":
				result.Exam.PublishedAt = time.Time{}
			}
			grade := studentPublishedGrade("tenant-1", "student-1", "frozen-submission", result)
			if grade.ID != "release-v3" || grade.Revision != 3 || grade.ExamID != "exam-1" || grade.TenantID != "tenant-1" || grade.StudentID != "student-1" || grade.SubmissionID != "frozen-submission" || grade.TotalScore != 7.25 || grade.MaxScore != 10 || grade.Status != "published" || !grade.Locked {
				t.Fatalf("student projection substituted mutable identity or overall totals: %+v", grade)
			}
			if grade.PublishedBy != "" || grade.CreatedBy != "" || grade.ConfirmedBy != "" || grade.ConfirmedAt != nil || grade.AnonymousCode != "" || !grade.CreatedAt.IsZero() {
				t.Fatalf("student projection exposed or invented staff metadata: %+v", grade)
			}
			if scenario == "no exam metadata" || scenario == "unknown publication time" {
				if grade.PublishedAt != nil {
					t.Fatal("unknown publication time was synthesized")
				}
			} else if grade.PublishedAt == nil || !grade.PublishedAt.Equal(publishedAt) {
				t.Fatal("frozen publication time was lost")
			}
			if scenario == "hidden questions" {
				raw, err := json.Marshal(grade)
				if err != nil || len(grade.Items) != 0 || strings.Contains(string(raw), `"items"`) || strings.Contains(string(raw), "q-visible") {
					t.Fatalf("hidden question detail was restored: %s %v", raw, err)
				}
				return
			}
			if len(grade.Items) != 1 || grade.Items[0].QuestionID != "q-visible" || grade.Items[0].QuestionNo != "1" || grade.Items[0].Score != 7.25 || grade.Items[0].MaxScore != 10 || grade.Items[0].SubmissionID != "frozen-submission" || grade.Items[0].Status != "locked" || !grade.Items[0].Locked {
				t.Fatalf("visible question projection changed: %+v", grade.Items)
			}
			if grade.Items[0].ID != "" || grade.Items[0].Source != "" || grade.Items[0].AnswerSegmentID != "" || grade.Items[0].CreatedBy != "" {
				t.Fatalf("student item acquired internal grade identities: %+v", grade.Items[0])
			}
			grade.Items[0].Score = 0
			if result.Questions[0].Score != 7.25 {
				t.Fatal("compatibility item aliases its public source")
			}
			if grade.PublishedAt != nil {
				*grade.PublishedAt = publishedAt.Add(time.Hour)
				if !result.Exam.PublishedAt.Equal(publishedAt) {
					t.Fatal("compatibility timestamp aliases its public source")
				}
			}
		})
	}
}
