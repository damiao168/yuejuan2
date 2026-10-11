package paper

import (
	"context"
	"errors"
	"testing"
)

func TestBatch42ApplyPaperImportPreservesPartialUpdatesAndIDConsumption(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{
		{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1},
		{QuestionNo: "2", QuestionType: "short_answer", Score: 10, SortOrder: 2},
	})
	drafts := []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10), importedDraft("2", "short_answer", 10)}
	for index := range drafts {
		drafts[index].Solution = &SolutionInput{Steps: []SolutionStep{{StepNo: 1, Content: "Solve"}},
			SourceRefs: []PaperImportSourceRef{{SourceID: "source-1"}}}
	}
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, drafts, nil)
	if err != nil || len(preview.Issues) != 0 {
		t.Fatalf("fixture failed to reconcile: %#v, %v", preview, err)
	}
	firstID, secondID := preview.Questions[0].MatchedQuestionID, preview.Questions[1].MatchedQuestionID
	secondBefore := store.questions[secondID]
	store.rubrics[secondID] = []Rubric{{ID: "locked-rubric", QuestionID: secondID, Version: "v1", Status: "locked"}}
	nextBefore := store.next
	_, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user")
	if !errors.Is(err, ErrRubricLocked) {
		t.Fatalf("expected locked second rubric, got %v", err)
	}
	first, second := store.questions[firstID], store.questions[secondID]
	if first.Stem != drafts[0].Stem || first.AnswerKey == nil || first.Solution == nil || len(store.rubrics[firstID]) != 1 {
		t.Fatalf("first question update was lost: %#v", first)
	}
	if second.Stem != secondBefore.Stem || second.AnswerKey != secondBefore.AnswerKey || second.Solution != secondBefore.Solution {
		t.Fatalf("failed second question was written back: %#v", second)
	}
	// 首题分配答案、解析、量规三个 ID，次题在已锁量规检查前仍分配答案和解析两个 ID。
	if store.next != nextBefore+5 {
		t.Fatalf("ID consumption changed: got %d want %d", store.next, nextBefore+5)
	}
	stored, err := store.GetPaperImport(context.Background(), "tenant", job.ID)
	if err != nil || stored.Status != "review_required" || stored.AppliedAt != nil {
		t.Fatalf("failed import was marked applied: %#v, %v", stored, err)
	}
}
