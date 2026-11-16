package scorerelease

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func regradeDraftFactsStep(releaseID string, fact SubmissionFact) studentBatch50Step {
	rows := make([][]driver.Value, 0, len(fact.Questions))
	for _, question := range fact.Questions {
		explanation, _ := json.Marshal(question.Explanation)
		rows = append(rows, []driver.Value{
			fact.StudentID, fact.SubmissionID, fact.TotalScore, fact.MaxScore, fact.Status,
			question.QuestionID, question.QuestionNo, question.FinalGradeID, question.Score,
			question.MaxScore, question.SourceType, question.SourceID, explanation,
		})
	}
	return studentBatch50Step{fragment: "FROM score_release_item item", args: []any{tenant, releaseID}, columns: 13, rows: rows}
}

func TestSQLRegradeDraftBindsRecoveredVersionToReviewedEvidence(t *testing.T) {
	const baseID, oldID, newID = "existing-published-version", "old-random-key-draft", "new-stable-key-draft"
	const key, reason = "regrade-job:reviewed-task", "verified correction"
	input := CreateRegradeInput{
		SourceReleaseID: baseID, QuestionID: "q1", Reason: reason, IdempotencyKey: key,
		Changes: []RegradeChange{{SubmissionID: "submission", QuestionID: "q1", Score: 4, MaxScore: 5, ReviewedGradeID: "reviewed-grade"}},
	}
	base := SubmissionFact{StudentID: "student", SubmissionID: "submission", TotalScore: 2, MaxScore: 5, Status: "confirmed", Questions: []QuestionFact{{
		QuestionID: "q1", QuestionNo: "1", FinalGradeID: "frozen-grade", Score: 2, MaxScore: 5, SourceType: "single_review", SourceID: "old-review",
		Explanation: StudentExplanation{Feedback: "feedback about original score", RubricSummary: []string{"original rubric notes"}, Stem: "frozen question"},
	}}}
	corrected := SubmissionFact{StudentID: "student", SubmissionID: "submission", TotalScore: 4, MaxScore: 5, Status: "confirmed", Questions: []QuestionFact{{
		QuestionID: "q1", QuestionNo: "1", FinalGradeID: "frozen-grade", Score: 4, MaxScore: 5, SourceType: "single_review", SourceID: "reviewed-grade",
		Explanation: StudentExplanation{Stem: "frozen question"},
	}}}
	wrongEvidence := cloneFacts([]SubmissionFact{corrected})[0]
	wrongEvidence.Questions[0].SourceID = "different-reviewed-grade-with-the-same-score"
	policy, err := json.Marshal(VisibilityPolicy{ShowQuestionScores: true, ShowAnswers: true})
	if err != nil {
		t.Fatal(err)
	}
	const window = `{"enabled":false}`
	createdAt := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	baseRow := []driver.Value{baseID, tenant, exam, int64(1), SourceInitial, "initial reason", StatusPublished, "initial-key", policy, []byte(window), []byte(`{}`), "", "", actor, createdAt, actor, createdAt}
	historicalRow := []driver.Value{oldID, tenant, exam, int64(2), SourceRegrade, reason, StatusDraft, "historical-random-key", policy, []byte(window), []byte(`{}`), baseID, "", actor, createdAt, "", nil}
	newRow := []driver.Value{newID, tenant, exam, int64(3), SourceRegrade, reason, StatusDraft, key, policy, []byte(window), []byte(`{}`), baseID, "", actor, createdAt, "", nil}
	lockAndReadKey := []studentBatch50Step{
		{fragment: "pg_advisory_xact_lock", args: []any{tenant, exam}},
		{fragment: "AND idempotency_key = $3", args: []any{tenant, exam, key}, columns: len(newRow)},
	}
	listHistoricalDrafts := studentBatch50Step{fragment: "AND source_release_id=$4", args: []any{tenant, exam, SourceRegrade, baseID}, columns: len(historicalRow), rows: [][]driver.Value{historicalRow}}
	snapshotWrites := []studentBatch50Step{
		{fragment: "MAX(version)", args: []any{tenant, exam}, columns: 1, rows: [][]driver.Value{{int64(3)}}},
		{fragment: "INSERT INTO score_release (", args: []any{tenant, exam, int64(3), SourceRegrade, reason, key, string(policy), window, baseID, actor}, columns: len(newRow), rows: [][]driver.Value{newRow}},
		{fragment: "INSERT INTO score_release_item (", args: []any{tenant, newID, "student", "submission", float64(4), float64(5), "confirmed", snapshotHash(corrected)}},
		{fragment: "INSERT INTO score_release_question (", args: []any{tenant, newID, "submission", "q1", "1", "frozen-grade", float64(4), float64(5), "single_review", "reviewed-grade", `{"stem":"frozen question"}`}},
	}
	failedQuestionWrite := errors.New("score release question insert failed")
	for _, tc := range []struct {
		name         string
		stableReplay bool
		historical   SubmissionFact
		questionErr  error
		wantID       string
	}{
		{name: "stable key replay before any reconstruction", stableReplay: true, wantID: newID},
		{name: "recover matching historical draft", historical: corrected, wantID: oldID},
		{name: "same score with different evidence gets a separate snapshot", historical: wrongEvidence, wantID: newID},
		{name: "a late question insert failure rolls back the draft and totals", historical: wrongEvidence, questionErr: failedQuestionWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]studentBatch50Step(nil), lockAndReadKey...)
			if tc.stableReplay {
				steps[1].rows = [][]driver.Value{newRow}
			} else {
				steps = append(steps,
					studentBatch50Step{fragment: "AND id = $2::uuid", args: []any{tenant, baseID}, columns: len(baseRow), rows: [][]driver.Value{baseRow}},
					regradeDraftFactsStep(baseID, base), listHistoricalDrafts, regradeDraftFactsStep(oldID, tc.historical),
				)
				// 恢复旧草稿要求整份冻结快照与本次审核来源一致，只对分数会误认其他复评任务。
				if tc.wantID != oldID {
					steps = append(steps, snapshotWrites...)
					steps[len(steps)-1].err = tc.questionErr
				}
			}
			probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: steps}}
			db := sql.OpenDB(publicationTxConnector{probe: probe})
			defer db.Close()
			service := NewService(NewPostgresStore(db, nil))
			got, err := service.CreateFromRegrade(t.Context(), tenant, exam, actor, input)
			if !errors.Is(err, tc.questionErr) || got.ID != tc.wantID {
				t.Fatalf("wrong draft outcome: draft=%+v err=%v", got, err)
			}
			if tc.questionErr == nil && (got.Status != StatusDraft || got.SourceReleaseID != baseID) {
				t.Fatalf("draft lost frozen source: %+v", got)
			}
			if probe.err != nil || probe.next != len(steps) || probe.committed != (tc.questionErr == nil) || probe.aborted != (tc.questionErr != nil) {
				t.Fatalf("unexpected transaction: steps=%d/%d committed=%t rolled back=%t err=%v", probe.next, len(steps), probe.committed, probe.aborted, probe.err)
			}
		})
	}
}
