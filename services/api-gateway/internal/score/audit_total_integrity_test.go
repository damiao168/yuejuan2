package score

import (
	"errors"
	"testing"
)

func TestConfirmationAndPublicationRejectStaleSubmissionTotals(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmation", true: "publication"}[confirmed], func(t *testing.T) {
			store := seededScoreStore()
			ctx := t.Context()
			if _, err := store.FinalizeExam(ctx, tenantID, "exam-1", "manager"); err != nil {
				t.Fatal(err)
			}
			if confirmed {
				if _, err := store.ConfirmGrades(ctx, tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked"}); err != nil {
					t.Fatal(err)
				}
			}
			// 模拟汇总后双评/仲裁落下当前终分，已有总分仍保留旧值。
			store.mu.Lock()
			for id, grade := range store.finals {
				grade.Score++
				store.finals[id] = grade
				break
			}
			store.mu.Unlock()
			quality, err := store.CheckQuality(ctx, tenantID, "exam-1", confirmed)
			if err != nil || !hasIssue(quality.Issues, "score_integrity_mismatch") {
				t.Fatal(quality, err)
			}
			if confirmed {
				// 正式快照独立保存当前题分和旧总分，保护旧入口不能绕过统一的完整性门禁。
				fact := scorePublicationFact("submission-1", "student-1", 5, 4)
				fact.TotalScore = 8
				attachScorePublication(store, "exam-1", fact)
				_, err = store.PublishGrades(ctx, tenantID, "exam-1", "manager", PublishInput{Reason: "publish"})
			} else {
				_, err = store.ConfirmGrades(ctx, tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked"})
			}
			if !errors.Is(err, ErrQualityGateFailed) {
				t.Fatal("stale total accepted", err)
			}
		})
	}
}

func TestConfirmedTotalsRecoverWhenNewFinalArrivesBeforeFirstPublication(t *testing.T) {
	store := seededScoreStore()
	ctx := t.Context()
	if _, err := store.FinalizeExam(ctx, tenantID, "exam-1", "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmGrades(ctx, tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatal(err)
	}
	var segmentID string
	var nextScore, maxScore float64
	store.mu.Lock()
	for _, finalID := range store.finalBySeg {
		final := store.finals[finalID]
		segmentID, nextScore, maxScore = final.AnswerSegmentID, final.Score+1, final.MaxScore
		break
	}
	store.mu.Unlock()
	store.AddFinalGrade(GradeSeed{AnswerSegmentID: segmentID, Score: nextScore, MaxScore: maxScore, Source: "double_mark_auto"})
	result, err := store.FinalizeExam(ctx, tenantID, "exam-1", "manager")
	if err != nil || !result.Quality.Passed {
		t.Fatalf("new final cannot recover: %+v %v", result, err)
	}
	if _, err := store.ConfirmGrades(ctx, tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked latest final"}); err != nil {
		t.Fatal(err)
	}
	attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 5, 4))
	if _, err := store.PublishGrades(ctx, tenantID, "exam-1", "manager", PublishInput{Reason: "publish latest"}); err != nil {
		t.Fatal(err)
	}
}
